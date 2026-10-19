package dnsproxy

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

	"github.com/shiinobu/hecatedns/backend/engine"
)

type Filter interface {
	Before(ctx context.Context, q engine.Query) engine.Decision
	After(ctx context.Context, q engine.Query, resp *dns.Msg, m engine.Meta) engine.Decision
	Failed(ctx context.Context, q engine.Query, err error)
}

var (
	_ Filter        = (*engine.Engine)(nil)
	_ proxy.Handler = (*handler)(nil)
)

type handlerStage string

const (
	stageFilter  handlerStage = "filter"
	stageResolve handlerStage = "resolve"
	stageAnswer  handlerStage = "answer"
)

type handler struct {
	filter Filter
	log    *slog.Logger
}

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

func (h *handler) after(ctx context.Context, dctx *proxy.DNSContext, q engine.Query, latency time.Duration, cached bool) {
	meta := engine.Meta{Latency: latency, Cached: cached}
	if dctx.Upstream != nil {
		meta.Upstream = dctx.Upstream.Address()
	}
	if dec := h.filter.After(ctx, q, dctx.Res, meta); dec.Verdict == engine.Block && dec.Resp != nil {
		dctx.Res = dec.Resp
	}
}

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

func servFail(req *dns.Msg) *dns.Msg {
	if req == nil {
		return &dns.Msg{MsgHdr: dns.MsgHdr{Response: true, RecursionAvailable: true, Rcode: dns.RcodeServerFailure}}
	}
	resp := new(dns.Msg).SetRcode(req, dns.RcodeServerFailure)
	resp.RecursionAvailable = true
	return resp
}
