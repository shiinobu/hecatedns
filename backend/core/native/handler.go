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
	resolveTimeout = 2*upstreamTimeout + time.Second

	dropLogEvery = 10 * time.Second
)

var errBusy = errors.New("too many upstream queries in flight")

type Filter interface {
	Before(ctx context.Context, q engine.Query) engine.Decision
	After(ctx context.Context, q engine.Query, resp *dns.Msg, m engine.Meta) engine.Decision
	Failed(ctx context.Context, q engine.Query, err error)
}

var _ Filter = (*engine.Engine)(nil)

type exchanger interface {
	Exchange(ctx context.Context, q *dns.Msg) (resp *dns.Msg, from string, err error)
}

type handlerStage string

const (
	stageFilter  handlerStage = "filter"
	stageResolve handlerStage = "resolve"
	stageAnswer  handlerStage = "answer"
)

type handler struct {
	filter  Filter
	cache   *cache
	up      exchanger
	limiter *limiter
	gate    *gate
	log     *slog.Logger

	base    context.Context
	timeout time.Duration

	dropped  atomic.Uint64
	lastWarn atomic.Int64
}

func newHandler(f Filter, c *cache, up exchanger, l *limiter, log *slog.Logger) *handler {
	return &handler{
		filter: f, cache: c, up: up, limiter: l, gate: newGate(), log: log,
		base: context.Background(), timeout: resolveTimeout,
	}
}

var _ dns.Handler = (*handler)(nil)

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

func (h *handler) write(w dns.ResponseWriter, req, resp *dns.Msg, proto string) {
	shapeReply(req, resp)
	if proto == "udp" {
		resp.Truncate(udpPayloadSize(req))
	}
	if err := w.WriteMsg(resp); err != nil {
		h.log.Debug("writing the answer failed", "client", w.RemoteAddr().String(), "err", err)
	}
}

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

func udpPayloadSize(req *dns.Msg) int {
	size := dns.MinMsgSize
	if opt := req.IsEdns0(); opt != nil {
		size = int(opt.UDPSize())
	}
	return min(max(size, dns.MinMsgSize), ednsBufferSize)
}

func (h *handler) drop(reason string, client netip.Addr) {
	n := h.dropped.Add(1)
	now := time.Now().UnixNano()
	last := h.lastWarn.Load()
	if now-last < int64(dropLogEvery) || !h.lastWarn.CompareAndSwap(last, now) {
		return
	}
	h.log.Warn("dropping queries", "reason", reason, "client", client.String(), "dropped_total", n)
}

func (h *handler) admitTCP(acl *engine.ACL) func(netip.Addr) bool {
	return func(client netip.Addr) bool {
		if acl.Allows(client) {
			return true
		}
		h.drop("tcp client outside allowed_clients", client)
		return false
	}
}

func servFail(req *dns.Msg) *dns.Msg {
	if req == nil {
		return &dns.Msg{MsgHdr: dns.MsgHdr{Response: true, RecursionAvailable: true, Rcode: dns.RcodeServerFailure}}
	}
	resp := new(dns.Msg).SetRcode(req, dns.RcodeServerFailure)
	resp.RecursionAvailable = true
	return resp
}

func blockReply(req *dns.Msg, dec engine.Decision) *dns.Msg {
	if dec.Resp == nil {
		return servFail(req)
	}
	return dec.Resp.Copy()
}
