package dnsproxybe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/AdguardTeam/dnsproxy/proxy"
	"github.com/AdguardTeam/dnsproxy/upstream"
	"github.com/miekg/dns"

	"xdns/internal/engine"
)

// Filter is the part of the engine the handler calls. *engine.Engine
// satisfies it; tests substitute fakes.
type Filter interface {
	Before(ctx context.Context, q engine.Query) engine.Decision
	After(ctx context.Context, q engine.Query, resp *dns.Msg, m engine.Meta) engine.Decision
	Failed(ctx context.Context, q engine.Query, err error)
}

var (
	_ Filter        = (*engine.Engine)(nil)
	_ proxy.Handler = (*handler)(nil)
)

// handlerStage is how far ServeDNS got, which decides what a panic means.
type handlerStage string

const (
	stageFilter  handlerStage = "filter"  // before the engine passed the query
	stageResolve handlerStage = "resolve" // inside dnsproxy's Resolve
	stageAnswer  handlerStage = "answer"  // Resolve returned, engine sees the answer
)

// handler is the single dnsproxy request handler: the engine first, then
// dnsproxy's own cache and upstream resolution, then the engine again on the
// answer. dnsproxy stores the upstream answer in its cache inside Resolve, so
// the engine's After runs on every cache hit too.
type handler struct {
	filter Filter
	log    *slog.Logger
}

// ServeDNS implements proxy.Handler. Package proxy has no recover, so a panic
// here would end the process: it is recovered and logged, and the query fails
// open. dctx.Res is never left nil, because dnsproxy sends nothing for a nil
// answer and the client would only time out.
func (h *handler) ServeDNS(ctx context.Context, p *proxy.Proxy, dctx *proxy.DNSContext) (err error) {
	stage := stageFilter
	defer func() {
		if r := recover(); r != nil {
			err = h.recovered(ctx, dctx, stage, r)
		}
		if dctx.Res == nil {
			dctx.Res = servFail(dctx.Req)
		}
	}()
	q := engine.Query{Req: dctx.Req, Client: dctx.Addr.Addr().Unmap(), Proto: string(dctx.Proto)}
	if dec := h.filter.Before(ctx, q); dec.Verdict == engine.Block {
		dctx.Res = dec.Resp
		return nil
	}
	stage = stageResolve
	start := time.Now()
	err = p.Resolve(ctx, dctx)
	latency := time.Since(start)
	stage = stageAnswer
	if errors.Is(err, upstream.ErrNoUpstreams) {
		// dnsproxy answered by itself (NXDOMAIN for a private reverse lookup, for
		// which no private upstream is configured). That is an answered query,
		// not an upstream failure.
		h.after(ctx, dctx, q, latency, false)
		return nil
	}
	if err != nil {
		h.filter.Failed(ctx, q, err)
		return fmt.Errorf("dnsproxy backend: resolve: %w", err)
	}
	h.after(ctx, dctx, q, latency, dctx.Upstream == nil)
	return nil
}

// after hands the resolved answer to the engine and replaces it, without
// touching the old message, if the engine blocks it.
func (h *handler) after(ctx context.Context, dctx *proxy.DNSContext, q engine.Query, latency time.Duration, cached bool) {
	meta := engine.Meta{Latency: latency, Cached: cached}
	if dctx.Upstream != nil {
		meta.Upstream = dctx.Upstream.Address()
	}
	if dec := h.filter.After(ctx, q, dctx.Res, meta); dec.Verdict == engine.Block && dec.Resp != nil {
		dctx.Res = dec.Resp
	}
}

// recovered handles a panic of ServeDNS by stage. A panic in the filter stage
// happened before Before returned, so it is unknown whether the client passed
// the ACL: the answer is REFUSED (fail closed). The engine recovers its own
// panics and fails open itself, so this only guards the boundary. A panic inside Resolve leaves an
// answer in an unknown state, so it is replaced by SERVFAIL. After Resolve the
// answer it produced is kept. The panic is logged here, so it is not returned
// to dnsproxy to be logged again.
func (h *handler) recovered(ctx context.Context, dctx *proxy.DNSContext, stage handlerStage, r any) error {
	h.log.ErrorContext(ctx, "dns handler recovered from a panic",
		"stage", string(stage), "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
	switch stage {
	case stageFilter:
		dctx.Res = engine.Refuse(dctx.Req)
	case stageResolve:
		dctx.Res = servFail(dctx.Req)
	}
	return nil
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
