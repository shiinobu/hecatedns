package dnsproxy

import (
	"context"
	"net/netip"
	"testing"

	"github.com/AdguardTeam/dnsproxy/proxy"
	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/engine"
)

type fakeFilter struct {
	before engine.Decision
	got    engine.Query
	panics bool
}

func (f *fakeFilter) Before(_ context.Context, q engine.Query) engine.Decision {
	f.got = q
	if f.panics {
		panic("before exploded")
	}
	return f.before
}

func (f *fakeFilter) After(context.Context, engine.Query, *dns.Msg, engine.Meta) engine.Decision {
	return engine.Decision{Verdict: engine.Pass}
}

func (f *fakeFilter) Failed(context.Context, engine.Query, error) {}

func newDNSContext() *proxy.DNSContext {
	req := new(dns.Msg).SetQuestion("handler.example.", dns.TypeA)
	req.Id = 4242
	return &proxy.DNSContext{
		Req:   req,
		Addr:  netip.MustParseAddrPort("[::ffff:127.0.0.1]:5353"),
		Proto: proxy.ProtoUDP,
	}
}

func TestHandlerBlockAnswersWithoutResolve(t *testing.T) {
	dctx := newDNSContext()
	blockResp := new(dns.Msg).SetRcode(dctx.Req, dns.RcodeNameError)
	f := &fakeFilter{before: engine.Decision{Verdict: engine.Block, Resp: blockResp}}
	h := &handler{filter: f, log: discardLog}

	err := h.ServeDNS(context.Background(), nil, dctx)

	if err != nil || dctx.Res != blockResp {
		t.Fatalf("ServeDNS = %v, Res %v; want the block answer and no error", err, dctx.Res)
	}
	if f.got.Req != dctx.Req || f.got.Client != netip.MustParseAddr("127.0.0.1") || f.got.Proto != "udp" {
		t.Fatalf("query = %+v, want the request, the unmapped client and proto udp", f.got)
	}
}

func TestHandlerACLRefusalOverAStreamClosesTheConnectionWithoutAnAnswer(t *testing.T) {
	for _, proto := range []proxy.Proto{proxy.ProtoTCP, proxy.ProtoTLS} {
		t.Run(string(proto), func(t *testing.T) {
			dctx := newDNSContext()
			dctx.Proto = proto
			f := &fakeFilter{before: engine.Decision{Verdict: engine.Block, Resp: engine.Refuse(dctx.Req), Reason: engine.ReasonACL}}
			h := &handler{filter: f, log: discardLog}

			err := h.ServeDNS(context.Background(), nil, dctx)

			if err != nil || dctx.Res != nil {
				t.Fatalf("ServeDNS = %v, Res %v; want no error and no response, so dnsproxy closes the connection", err, dctx.Res)
			}
		})
	}
}

func TestHandlerRefusalsThatAreNotTheACLAndUDPRefusalsStillGetAnAnswer(t *testing.T) {
	tests := []struct {
		name   string
		proto  proxy.Proto
		reason string
	}{
		{"ACL refusal over UDP", proxy.ProtoUDP, engine.ReasonACL},
		{"list block over TCP", proxy.ProtoTCP, engine.ReasonList},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dctx := newDNSContext()
			dctx.Proto = tt.proto
			answer := engine.Refuse(dctx.Req)
			f := &fakeFilter{before: engine.Decision{Verdict: engine.Block, Resp: answer, Reason: tt.reason}}
			h := &handler{filter: f, log: discardLog}

			err := h.ServeDNS(context.Background(), nil, dctx)

			if err != nil || dctx.Res != answer {
				t.Fatalf("ServeDNS = %v, Res %v; want the decision's answer", err, dctx.Res)
			}
		})
	}
}

func TestHandlerNeverLeavesResponseNil(t *testing.T) {
	tests := []struct {
		name   string
		filter *fakeFilter
	}{
		{"block without response", &fakeFilter{before: engine.Decision{Verdict: engine.Block}}},
		{"resolve panics", &fakeFilter{before: engine.Decision{Verdict: engine.Pass}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dctx := newDNSContext()
			h := &handler{filter: tt.filter, log: discardLog}

			_ = h.ServeDNS(context.Background(), nil, dctx)

			if dctx.Res == nil || dctx.Res.Rcode != dns.RcodeServerFailure || dctx.Res.Id != dctx.Req.Id {
				t.Fatalf("Res = %v, want a SERVFAIL reply to the request", dctx.Res)
			}
		})
	}
}

func TestHandlerFilterPanicFailsClosed(t *testing.T) {
	dctx := newDNSContext()
	h := &handler{filter: &fakeFilter{panics: true}, log: discardLog}

	if err := h.ServeDNS(context.Background(), nil, dctx); err != nil {
		t.Fatalf("ServeDNS = %v, want nil", err)
	}
	if dctx.Res == nil || dctx.Res.Rcode != dns.RcodeRefused || dctx.Res.Id != dctx.Req.Id {
		t.Fatalf("Res = %v, want a REFUSED reply to the request", dctx.Res)
	}
}

func TestServFailWithoutRequest(t *testing.T) {
	if resp := servFail(nil); resp == nil || resp.Rcode != dns.RcodeServerFailure || !resp.Response {
		t.Fatalf("servFail(nil) = %v, want a SERVFAIL response", resp)
	}
}
