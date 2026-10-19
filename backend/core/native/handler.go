package native

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/engine"
)

const (
	// resolveTimeout bounds one query from the moment it is accepted: the pool
	// asks its upstreams one after the other, upstreamTimeout each. Without it a
	// blackholed upstream would hold an in-flight slot for the whole run.
	resolveTimeout = 2*upstreamTimeout + time.Second

	// dropLogEvery is the shortest time between two warnings about dropped
	// queries.
	dropLogEvery = 10 * time.Second
)

// errBusy is the reason a query is answered SERVFAIL when no upstream slot is
// free for it.
var errBusy = errors.New("too many upstream queries in flight")

// Filter is the part of the engine the handler calls. *engine.Engine satisfies
// it; tests substitute fakes.
type Filter interface {
	Before(ctx context.Context, q engine.Query) engine.Decision
	After(ctx context.Context, q engine.Query, resp *dns.Msg, m engine.Meta) engine.Decision
	Failed(ctx context.Context, q engine.Query, err error)
}

var _ Filter = (*engine.Engine)(nil)

// exchanger resolves a query against the upstreams. *pool satisfies it.
type exchanger interface {
	Exchange(ctx context.Context, q *dns.Msg) (resp *dns.Msg, from string, err error)
}

// handlerStage is how far a query got, which decides what a panic means.
type handlerStage string

const (
	stageFilter  handlerStage = "filter"  // before the engine passed the query
	stageResolve handlerStage = "resolve" // cache and upstream
	stageAnswer  handlerStage = "answer"  // the engine looks at the answer
)

// handler is the dns.Handler of the native backend: rate limit, engine Before,
// cache, upstream, engine After. The engine decides; the handler only moves
// messages. The cache holds the upstream answer, so After runs on every hit.
type handler struct {
	filter  Filter
	cache   *cache
	up      exchanger
	limiter *limiter
	gate    *gate
	log     *slog.Logger

	// base is the parent of every query's context; the backend cancels it at
	// shutdown so queries in flight do not hold the shutdown up. timeout bounds
	// one query.
	base    context.Context
	timeout time.Duration

	dropped  atomic.Uint64
	lastWarn atomic.Int64 // unix nanoseconds of the last drop warning
}

func newHandler(f Filter, c *cache, up exchanger, l *limiter, log *slog.Logger) *handler {
	return &handler{
		filter: f, cache: c, up: up, limiter: l, gate: newGate(), log: log,
		base: context.Background(), timeout: resolveTimeout,
	}
}

var _ dns.Handler = (*handler)(nil)

// ServeDNS implements dns.Handler. A rate-limited query is dropped without an
// answer: an answer to a spoofed source address would be an amplifier. The
// rate limiter runs before the engine, so it also covers clients outside the ACL.
func (h *handler) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	client, proto := clientOf(w)
	if !h.limiter.allow(client) {
		h.drop("rate limit", client)
		return
	}
	ctx, cancel := context.WithTimeout(h.base, h.timeout)
	defer cancel()
	h.write(w, req, h.resolve(ctx, req, client, proto), proto)
}

// clientOf returns the client address (IPv4-mapped IPv6 unmapped) and the
// transport of a request.
func clientOf(w dns.ResponseWriter) (netip.Addr, string) {
	proto := "udp"
	if _, ok := w.RemoteAddr().(*net.TCPAddr); ok {
		proto = "tcp"
	}
	if ap, err := netip.ParseAddrPort(w.RemoteAddr().String()); err == nil {
		return ap.Addr().Unmap(), proto
	}
	return netip.Addr{}, proto
}

// resolve produces the answer for one query. Whatever happens, it returns a
// message: a panic is recovered by stage, and a nil answer becomes SERVFAIL.
func (h *handler) resolve(ctx context.Context, req *dns.Msg, client netip.Addr, proto string) (resp *dns.Msg) {
	stage := stageFilter
	defer func() {
		if r := recover(); r != nil {
			resp = h.recovered(ctx, req, stage, resp, r)
		}
		if resp == nil {
			resp = servFail(req)
		}
	}()
	q := engine.Query{Req: req, Client: client, Proto: proto}
	if dec := h.filter.Before(ctx, q); dec.Verdict == engine.Block {
		return blockReply(req, dec)
	}
	stage = stageResolve
	got, meta, err := h.lookup(ctx, req, client)
	if err != nil {
		h.filter.Failed(ctx, q, err)
		return servFail(req)
	}
	got.RecursionAvailable = true
	resp, stage = got, stageAnswer
	if dec := h.filter.After(ctx, q, got, meta); dec.Verdict == engine.Block {
		return blockReply(req, dec)
	}
	return got
}

// lookup answers from the cache or the upstreams. A reverse lookup of a private
// address is answered locally (see isLocalReverse). An ANY query is answered
// NOTIMP, as the dnsproxy backend does, because ANY answers are large and make
// good amplification. Only the upstream exchange takes a slot of the gate.
func (h *handler) lookup(ctx context.Context, req *dns.Msg, client netip.Addr) (*dns.Msg, engine.Meta, error) {
	if req.Question[0].Qtype == dns.TypeANY {
		return new(dns.Msg).SetRcode(req, dns.RcodeNotImplemented), engine.Meta{}, nil
	}
	if isLocalReverse(req.Question[0]) {
		return localReverseReply(req), engine.Meta{}, nil
	}
	if cached, ok := h.cache.get(req); ok {
		return cached, engine.Meta{Cached: true}, nil
	}
	if !h.gate.acquire(client) {
		h.drop("too many upstream queries in flight", client)
		return nil, engine.Meta{}, errBusy
	}
	defer h.gate.release(client)
	start := time.Now()
	resp, from, err := h.up.Exchange(ctx, upstreamQuery(req))
	latency := time.Since(start)
	if err != nil {
		return nil, engine.Meta{}, err
	}
	h.cache.put(req, resp)
	return resp, engine.Meta{Upstream: from, Latency: latency}, nil
}

// upstreamQuery builds the query sent upstream from the client's: the same
// question, recursion desired, and a fresh EDNS section. Nothing else of the
// client's message (cookies, client subnet, padding) leaves the machine.
func upstreamQuery(req *dns.Msg) *dns.Msg {
	q := new(dns.Msg)
	q.Question = []dns.Question{req.Question[0]}
	q.RecursionDesired = true
	q.CheckingDisabled = req.CheckingDisabled
	q.SetEdns0(ednsBufferSize, dnssecOK(req))
	return q
}

func dnssecOK(req *dns.Msg) bool {
	opt := req.IsEdns0()
	return opt != nil && opt.Do()
}

// recovered turns a panic into an answer by stage. A panic before the engine
// passed the query leaves it unknown whether the client passed the ACL, so the
// answer is REFUSED (fail closed). A panic in the cache or upstream code leaves
// no answer, so SERVFAIL. A panic after the answer was resolved keeps the answer.
func (h *handler) recovered(ctx context.Context, req *dns.Msg, stage handlerStage, resp *dns.Msg, r any) *dns.Msg {
	h.log.ErrorContext(ctx, "dns handler recovered from a panic",
		"stage", string(stage), "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
	switch stage {
	case stageFilter:
		return engine.Refuse(req)
	case stageAnswer:
		return resp
	}
	return servFail(req)
}

// write sends resp to the client, shaped for it: the client's ID, recursion
// flags and spelling of the name, an OPT record only if the client sent one, and
// on UDP a size the client can take.
func (h *handler) write(w dns.ResponseWriter, req, resp *dns.Msg, proto string) {
	shapeReply(req, resp)
	if proto == "udp" {
		resp.Truncate(udpPayloadSize(req))
	}
	if err := w.WriteMsg(resp); err != nil {
		h.log.Debug("writing the answer failed", "client", w.RemoteAddr().String(), "err", err)
	}
}

// shapeReply makes resp a reply to req, in place. It leaves RA as it is: the
// engine decides it for blocked and refused answers (a client outside the ACL
// may not use recursion), and resolve sets it on answers it resolved.
func shapeReply(req, resp *dns.Msg) {
	resp.Id = req.Id
	resp.Response = true
	resp.Opcode = req.Opcode
	resp.RecursionDesired = req.RecursionDesired
	resp.Authoritative = false
	resp.CheckingDisabled = req.CheckingDisabled
	if len(req.Question) > 0 {
		resp.Question = append([]dns.Question(nil), req.Question...)
	}
	resp.Extra = withoutOPT(resp.Extra)
	dnssec := dnssecOK(req)
	if !dnssec {
		resp.AuthenticatedData = false
	}
	if req.IsEdns0() != nil {
		resp.SetEdns0(ednsBufferSize, dnssec)
	}
}

// udpPayloadSize is the largest UDP answer the client accepts and we send: its
// EDNS size, at least the classic 512 and at most our own 1232.
func udpPayloadSize(req *dns.Msg) int {
	size := dns.MinMsgSize
	if opt := req.IsEdns0(); opt != nil {
		size = int(opt.UDPSize())
	}
	return min(max(size, dns.MinMsgSize), ednsBufferSize)
}

// drop counts a query that was not served and warns at most once per
// dropLogEvery, so a flood cannot fill the log.
func (h *handler) drop(reason string, client netip.Addr) {
	n := h.dropped.Add(1)
	now := time.Now().UnixNano()
	last := h.lastWarn.Load()
	if now-last < int64(dropLogEvery) || !h.lastWarn.CompareAndSwap(last, now) {
		return
	}
	h.log.Warn("dropping queries", "reason", reason, "client", client.String(), "dropped_total", n)
}

// servFail returns a SERVFAIL reply to req; req may be nil.
func servFail(req *dns.Msg) *dns.Msg {
	if req == nil {
		return &dns.Msg{MsgHdr: dns.MsgHdr{Response: true, RecursionAvailable: true, Rcode: dns.RcodeServerFailure}}
	}
	resp := new(dns.Msg).SetRcode(req, dns.RcodeServerFailure)
	resp.RecursionAvailable = true
	return resp
}

// blockReply returns the engine's answer for a blocked query as a private copy,
// because the handler shapes the message in place. A block decision without an
// answer is an engine bug and becomes SERVFAIL rather than a crash.
func blockReply(req *dns.Msg, dec engine.Decision) *dns.Msg {
	if dec.Resp == nil {
		return servFail(req)
	}
	return dec.Resp.Copy()
}
