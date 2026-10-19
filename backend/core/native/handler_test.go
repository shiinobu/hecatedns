package native

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/engine"
	"github.com/shiinobu/hecatedns/backend/lib/config"
)

type fakeWriter struct {
	remote net.Addr
	mu     sync.Mutex
	sent   []*dns.Msg
}

func udpWriter(ip string) *fakeWriter {
	return &fakeWriter{remote: &net.UDPAddr{IP: net.ParseIP(ip), Port: 40000}}
}

func tcpWriter(ip string) *fakeWriter {
	return &fakeWriter{remote: &net.TCPAddr{IP: net.ParseIP(ip), Port: 40000}}
}

func (w *fakeWriter) LocalAddr() net.Addr  { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 53} }
func (w *fakeWriter) RemoteAddr() net.Addr { return w.remote }
func (w *fakeWriter) WriteMsg(m *dns.Msg) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sent = append(w.sent, m.Copy())
	return nil
}
func (w *fakeWriter) Write([]byte) (int, error) { return 0, errors.New("unused") }
func (w *fakeWriter) Close() error              { return nil }
func (w *fakeWriter) TsigStatus() error         { return nil }
func (w *fakeWriter) TsigTimersOnly(bool)       {}
func (w *fakeWriter) Hijack()                   {}

func (w *fakeWriter) only(t *testing.T) *dns.Msg {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.sent) != 1 {
		t.Fatalf("%d messages written, want exactly one", len(w.sent))
	}
	return w.sent[0]
}

func (w *fakeWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.sent)
}

type fakeFilter struct {
	before func(engine.Query) engine.Decision
	after  func(engine.Query, *dns.Msg, engine.Meta) engine.Decision

	mu      sync.Mutex
	queries []engine.Query
	metas   []engine.Meta
	failed  []error
}

func (f *fakeFilter) Before(_ context.Context, q engine.Query) engine.Decision {
	f.mu.Lock()
	f.queries = append(f.queries, q)
	f.mu.Unlock()
	if f.before != nil {
		return f.before(q)
	}
	return engine.Decision{Verdict: engine.Pass}
}

func (f *fakeFilter) After(_ context.Context, q engine.Query, resp *dns.Msg, m engine.Meta) engine.Decision {
	f.mu.Lock()
	f.metas = append(f.metas, m)
	f.mu.Unlock()
	if f.after != nil {
		return f.after(q, resp, m)
	}
	return engine.Decision{Verdict: engine.Pass}
}

func (f *fakeFilter) Failed(_ context.Context, _ engine.Query, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, err)
}

type fakeExchanger struct {
	fn func(q *dns.Msg) (*dns.Msg, error)

	mu    sync.Mutex
	asked []*dns.Msg
}

func (e *fakeExchanger) Exchange(_ context.Context, q *dns.Msg) (*dns.Msg, string, error) {
	e.mu.Lock()
	e.asked = append(e.asked, q.Copy())
	e.mu.Unlock()
	resp, err := e.fn(q)
	return resp, "fake-upstream", err
}

func (e *fakeExchanger) calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.asked)
}

func okExchanger() *fakeExchanger {
	return &fakeExchanger{fn: func(q *dns.Msg) (*dns.Msg, error) { return aReply(q), nil }}
}

func newTestHandler(f Filter, up exchanger) *handler {
	return newHandler(f, newCache(config.Cache{Enabled: true, SizeMB: 1, MaxTTL: 3600e9}), up, nil, slog.New(slog.DiscardHandler))
}

func TestHandler_PassedQueryIsAnsweredAndTheEngineSeesIt(t *testing.T) {
	f := &fakeFilter{}
	h := newTestHandler(f, okExchanger())
	w := udpWriter("192.168.1.150")
	req := queryFor("fine.example", dns.TypeA)

	h.ServeDNS(w, req)

	resp := w.only(t)
	if resp.Id != req.Id || !resp.Response || !resp.RecursionAvailable || len(resp.Answer) != 1 {
		t.Errorf("reply = %v, want an answer to ID %d with RA set", resp, req.Id)
	}
	if len(f.queries) != 1 || f.queries[0].Client.String() != "192.168.1.150" || f.queries[0].Proto != "udp" {
		t.Errorf("engine saw %+v, want one udp query from 192.168.1.150", f.queries)
	}
	if len(f.metas) != 1 || f.metas[0].Cached || f.metas[0].Upstream != "fake-upstream" {
		t.Errorf("After got meta %+v, want an uncached answer from fake-upstream", f.metas)
	}
}

func TestHandler_TCPAndMappedAddressesReachTheEngineAsPlainIPv4(t *testing.T) {
	f := &fakeFilter{}
	h := newTestHandler(f, okExchanger())
	w := &fakeWriter{remote: &net.TCPAddr{IP: net.ParseIP("::ffff:192.168.1.7"), Port: 1}}

	h.ServeDNS(w, queryFor("fine.example", dns.TypeA))

	if got := f.queries[0]; got.Proto != "tcp" || got.Client != netip.MustParseAddr("192.168.1.7") {
		t.Errorf("query = proto %q client %v, want tcp from the unmapped 192.168.1.7", got.Proto, got.Client)
	}
}

func TestHandler_BlockedQueryNeverReachesTheUpstreamAndIsNotChangedInPlace(t *testing.T) {
	blocked := new(dns.Msg).SetRcode(queryFor("ads.test", dns.TypeA), dns.RcodeNameError)
	f := &fakeFilter{before: func(engine.Query) engine.Decision {
		return engine.Decision{Verdict: engine.Block, Resp: blocked}
	}}
	up := okExchanger()
	h := newTestHandler(f, up)
	w := udpWriter("192.168.1.150")
	req := queryFor("ads.test", dns.TypeA)
	req.Id = 99
	req.SetEdns0(4096, false)

	h.ServeDNS(w, req)

	resp := w.only(t)
	if resp.Rcode != dns.RcodeNameError || resp.Id != 99 || resp.IsEdns0() == nil {
		t.Errorf("reply = %v, want NXDOMAIN with ID 99 and an OPT record", resp)
	}
	if up.calls() != 0 {
		t.Error("a blocked query reached the upstream")
	}
	if blocked.Id == 99 || blocked.IsEdns0() != nil {
		t.Error("the engine's own answer was modified; the handler must shape a copy")
	}
}

func TestHandler_AfterMayReplaceTheAnswer(t *testing.T) {
	replacement := new(dns.Msg).SetRcode(queryFor("cloaked.example", dns.TypeA), dns.RcodeRefused)
	f := &fakeFilter{after: func(engine.Query, *dns.Msg, engine.Meta) engine.Decision {
		return engine.Decision{Verdict: engine.Block, Resp: replacement}
	}}
	h := newTestHandler(f, okExchanger())
	w := udpWriter("192.168.1.150")

	h.ServeDNS(w, queryFor("cloaked.example", dns.TypeA))

	if got := w.only(t).Rcode; got != dns.RcodeRefused {
		t.Errorf("rcode = %s, want the engine's replacement REFUSED", dns.RcodeToString[got])
	}
}

func TestHandler_CacheHitSkipsTheUpstreamButStillConsultsTheEngine(t *testing.T) {
	f := &fakeFilter{}
	up := okExchanger()
	h := newTestHandler(f, up)

	h.ServeDNS(udpWriter("192.168.1.150"), queryFor("fine.example", dns.TypeA))
	w := udpWriter("192.168.1.150")
	h.ServeDNS(w, queryFor("fine.example", dns.TypeA))

	if up.calls() != 1 {
		t.Errorf("upstream asked %d times, want 1", up.calls())
	}
	if len(f.metas) != 2 || !f.metas[1].Cached || f.metas[1].Upstream != "" {
		t.Errorf("After metas = %+v, want the second one cached with no upstream", f.metas)
	}
	if len(w.only(t).Answer) != 1 {
		t.Error("the cached answer was not delivered")
	}
}

func TestHandler_UpstreamFailureIsReportedAndAnsweredServFail(t *testing.T) {
	f := &fakeFilter{}
	up := &fakeExchanger{fn: func(*dns.Msg) (*dns.Msg, error) { return nil, errors.New("all down") }}
	h := newTestHandler(f, up)
	w := udpWriter("192.168.1.150")

	h.ServeDNS(w, queryFor("fine.example", dns.TypeA))

	if got := w.only(t).Rcode; got != dns.RcodeServerFailure {
		t.Errorf("rcode = %s, want SERVFAIL", dns.RcodeToString[got])
	}
	if len(f.failed) != 1 || len(f.metas) != 0 {
		t.Errorf("Failed called %d times and After %d times, want 1 and 0", len(f.failed), len(f.metas))
	}
}

func TestHandler_ANYIsNotImplementedAndNeverForwarded(t *testing.T) {
	f := &fakeFilter{}
	up := okExchanger()
	h := newTestHandler(f, up)
	w := udpWriter("192.168.1.150")

	h.ServeDNS(w, queryFor("fine.example", dns.TypeANY))

	if got := w.only(t).Rcode; got != dns.RcodeNotImplemented {
		t.Errorf("rcode = %s, want NOTIMP", dns.RcodeToString[got])
	}
	if up.calls() != 0 || len(f.metas) != 1 {
		t.Errorf("upstream calls %d, After calls %d; want 0 and 1 (the engine records the query)", up.calls(), len(f.metas))
	}
}

func TestHandler_PanicRecoveryDependsOnTheStage(t *testing.T) {
	cases := []struct {
		name  string
		build func() (*fakeFilter, *fakeExchanger)
		want  int
	}{
		{"before the engine passed it", func() (*fakeFilter, *fakeExchanger) {
			return &fakeFilter{before: func(engine.Query) engine.Decision { panic("boom") }}, okExchanger()
		}, dns.RcodeRefused},
		{"inside the upstream code", func() (*fakeFilter, *fakeExchanger) {
			return &fakeFilter{}, &fakeExchanger{fn: func(*dns.Msg) (*dns.Msg, error) { panic("boom") }}
		}, dns.RcodeServerFailure},
		{"after the answer was resolved", func() (*fakeFilter, *fakeExchanger) {
			return &fakeFilter{after: func(engine.Query, *dns.Msg, engine.Meta) engine.Decision { panic("boom") }}, okExchanger()
		}, dns.RcodeSuccess},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, up := tc.build()
			h := newTestHandler(f, up)
			w := udpWriter("192.168.1.150")

			h.ServeDNS(w, queryFor("fine.example", dns.TypeA))

			if got := w.only(t).Rcode; got != tc.want {
				t.Errorf("rcode = %s, want %s", dns.RcodeToString[got], dns.RcodeToString[tc.want])
			}
		})
	}
}

func TestHandler_RateLimitedQueriesAreDroppedWithoutAnAnswer(t *testing.T) {
	clock := newFakeClock()
	f := &fakeFilter{}
	h := newTestHandler(f, okExchanger())
	h.limiter = newLimiterAt(1, 2, 10, clock.Now)
	w := udpWriter("192.168.1.150")

	for i := 0; i < 5; i++ {
		h.ServeDNS(w, queryFor(fmt.Sprintf("n%d.example", i), dns.TypeA))
	}

	if w.count() != 2 || len(f.queries) != 2 {
		t.Errorf("%d answers and %d engine calls, want 2 and 2 (the burst), the rest dropped", w.count(), len(f.queries))
	}
	if h.dropped.Load() != 3 {
		t.Errorf("dropped = %d, want 3", h.dropped.Load())
	}
}

func bigExchanger(n int) *fakeExchanger {
	return &fakeExchanger{fn: func(q *dns.Msg) (*dns.Msg, error) {
		resp := new(dns.Msg).SetReply(q)
		for i := 0; i < n; i++ {
			resp.Answer = append(resp.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
				A:   net.IPv4(192, 0, 2, byte(i)),
			})
		}
		return resp, nil
	}}
}

func TestHandler_UDPAnswerIsTruncatedToWhatTheClientAccepts(t *testing.T) {
	h := newTestHandler(&fakeFilter{}, bigExchanger(60))

	plain := udpWriter("192.168.1.150")
	h.ServeDNS(plain, queryFor("big.example", dns.TypeA))
	if resp := plain.only(t); !resp.Truncated || resp.Len() > 512 {
		t.Errorf("no EDNS: truncated=%v length=%d, want TC set and at most 512 bytes", resp.Truncated, resp.Len())
	}

	edns := udpWriter("192.168.1.150")
	req := queryFor("big.example", dns.TypeA)
	req.SetEdns0(4096, false)
	h.ServeDNS(edns, req)
	if resp := edns.only(t); resp.Truncated || len(resp.Answer) != 60 {
		t.Errorf("EDNS 4096: truncated=%v answers=%d, want the whole answer", resp.Truncated, len(resp.Answer))
	}

	tcp := tcpWriter("192.168.1.150")
	h.ServeDNS(tcp, queryFor("big.example", dns.TypeA))
	if resp := tcp.only(t); resp.Truncated || len(resp.Answer) != 60 {
		t.Errorf("TCP: truncated=%v answers=%d, want the whole answer", resp.Truncated, len(resp.Answer))
	}
}

func TestHandler_OPTRecordOnlyForClientsThatSentOne(t *testing.T) {
	h := newTestHandler(&fakeFilter{}, okExchanger())

	without := udpWriter("192.168.1.150")
	h.ServeDNS(without, queryFor("a.example", dns.TypeA))
	if without.only(t).IsEdns0() != nil {
		t.Error("a client without EDNS got an OPT record")
	}

	with := udpWriter("192.168.1.150")
	req := queryFor("b.example", dns.TypeA)
	req.SetEdns0(1400, true)
	h.ServeDNS(with, req)
	opt := with.only(t).IsEdns0()
	if opt == nil || !opt.Do() {
		t.Errorf("OPT = %v, want one with the DO bit echoed", opt)
	}
}

func TestHandler_EchoesTheClientsSpellingOfTheName(t *testing.T) {
	h := newTestHandler(&fakeFilter{}, okExchanger())
	w := udpWriter("192.168.1.150")

	h.ServeDNS(w, queryFor("FiNe.ExAmPlE", dns.TypeA))

	if got := w.only(t).Question[0].Name; got != "FiNe.ExAmPlE." {
		t.Errorf("question = %q, want the client's spelling", got)
	}
}

func TestUpstreamQuery_KeepsOnlyTheQuestionAndFlags(t *testing.T) {
	req := queryFor("fine.example", dns.TypeA)
	req.Id = 5
	req.CheckingDisabled = true
	opt := &dns.OPT{Hdr: dns.RR_Header{Name: ".", Rrtype: dns.TypeOPT}}
	opt.SetUDPSize(4096)
	opt.SetDo()
	opt.Option = []dns.EDNS0{
		&dns.EDNS0_SUBNET{Code: dns.EDNS0SUBNET, Family: 1, SourceNetmask: 24, Address: net.IPv4(192, 168, 1, 0)},
		&dns.EDNS0_COOKIE{Code: dns.EDNS0COOKIE, Cookie: "0123456789abcdef"},
	}
	req.Extra = append(req.Extra, opt)

	q := upstreamQuery(req)

	if len(q.Question) != 1 || q.Question[0] != req.Question[0] || !q.RecursionDesired || !q.CheckingDisabled {
		t.Errorf("query = %v, want the same question with RD and CD", q)
	}
	got := q.IsEdns0()
	if got == nil || !got.Do() || len(got.Option) != 0 || got.UDPSize() != ednsBufferSize {
		t.Errorf("OPT = %v, want DO, size %d and no options (no client subnet, no cookie)", got, ednsBufferSize)
	}
	if len(opt.Option) != 2 {
		t.Error("the client's own message was modified")
	}
}

func TestHandler_UpstreamQueriesBeyondTheLimitGetServFailButCacheAndBlocksStillWork(t *testing.T) {
	f := &fakeFilter{before: func(q engine.Query) engine.Decision {
		if q.Req.Question[0].Name == "ads.test." {
			return engine.Decision{Verdict: engine.Block, Resp: new(dns.Msg).SetRcode(q.Req, dns.RcodeNameError)}
		}
		return engine.Decision{Verdict: engine.Pass}
	}}
	up := okExchanger()
	h := newTestHandler(f, up)
	h.ServeDNS(udpWriter("192.168.1.150"), queryFor("cached.example", dns.TypeA))
	h.gate.total = h.gate.maxTotal

	miss := udpWriter("192.168.1.150")
	h.ServeDNS(miss, queryFor("fresh.example", dns.TypeA))
	hit := udpWriter("192.168.1.150")
	h.ServeDNS(hit, queryFor("cached.example", dns.TypeA))
	blocked := udpWriter("192.168.1.150")
	h.ServeDNS(blocked, queryFor("ads.test", dns.TypeA))

	if got := miss.only(t).Rcode; got != dns.RcodeServerFailure {
		t.Errorf("cache miss with every upstream slot taken: rcode %s, want SERVFAIL", dns.RcodeToString[got])
	}
	if len(f.failed) != 1 {
		t.Errorf("Failed called %d times, want 1 so the query is logged", len(f.failed))
	}
	if up.calls() != 1 {
		t.Errorf("upstream asked %d times, want only the warm-up query", up.calls())
	}
	if len(hit.only(t).Answer) != 1 {
		t.Error("a cache hit was refused because the upstream slots were taken")
	}
	if got := blocked.only(t).Rcode; got != dns.RcodeNameError {
		t.Errorf("blocked query rcode %s, want the engine's NXDOMAIN", dns.RcodeToString[got])
	}
}

func TestHandler_OneClientCannotTakeAllTheUpstreamSlots(t *testing.T) {
	h := newTestHandler(&fakeFilter{}, okExchanger())
	greedy := netip.MustParseAddr("192.168.1.66")
	h.gate.perClient[greedy] = h.gate.maxPerClient

	blocked := udpWriter("192.168.1.66")
	h.ServeDNS(blocked, queryFor("one.example", dns.TypeA))
	other := udpWriter("192.168.1.150")
	h.ServeDNS(other, queryFor("two.example", dns.TypeA))

	if got := blocked.only(t).Rcode; got != dns.RcodeServerFailure {
		t.Errorf("client at its limit: rcode %s, want SERVFAIL", dns.RcodeToString[got])
	}
	if got := other.only(t).Rcode; got != dns.RcodeSuccess {
		t.Errorf("another client: rcode %s, want it served normally", dns.RcodeToString[got])
	}
}

func TestHandler_SlotsAreGivenBackAfterEveryQuery(t *testing.T) {
	h := newTestHandler(&fakeFilter{}, okExchanger())
	for i := 0; i < 3; i++ {
		h.ServeDNS(udpWriter("192.168.1.150"), queryFor(fmt.Sprintf("n%d.example", i), dns.TypeA))
	}
	if h.gate.total != 0 || len(h.gate.perClient) != 0 {
		t.Errorf("gate holds %d slots for %d clients after the queries ended, want none", h.gate.total, len(h.gate.perClient))
	}
}

type hangingUpstream struct{ calls chan struct{} }

func (u *hangingUpstream) Exchange(ctx context.Context, _ *dns.Msg) (*dns.Msg, string, error) {
	u.calls <- struct{}{}
	<-ctx.Done()
	return nil, "", ctx.Err()
}

func TestHandler_ResolvingHasADeadline(t *testing.T) {
	f := &fakeFilter{}
	up := &hangingUpstream{calls: make(chan struct{}, 1)}
	h := newTestHandler(f, up)
	h.timeout = 100 * time.Millisecond
	w := udpWriter("192.168.1.150")

	done := make(chan struct{})
	go func() { h.ServeDNS(w, queryFor("slow.example", dns.TypeA)); close(done) }()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a query whose upstream never answers held its slot for more than 3 s")
	}
	if got := w.only(t).Rcode; got != dns.RcodeServerFailure || len(f.failed) != 1 {
		t.Errorf("rcode %s, Failed called %d times; want SERVFAIL and 1", dns.RcodeToString[got], len(f.failed))
	}
}

func TestHandler_ShutdownCancelsQueriesInFlight(t *testing.T) {
	up := &hangingUpstream{calls: make(chan struct{}, 1)}
	h := newTestHandler(&fakeFilter{}, up)
	base, cancel := context.WithCancel(context.Background())
	h.base = base
	w := udpWriter("192.168.1.150")

	done := make(chan struct{})
	go func() { h.ServeDNS(w, queryFor("slow.example", dns.TypeA)); close(done) }()
	<-up.calls
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a query in flight was not cancelled when the backend shut down")
	}
}

func TestHandler_BlockFromAfterWithoutAnAnswerFailsClosed(t *testing.T) {
	f := &fakeFilter{after: func(engine.Query, *dns.Msg, engine.Meta) engine.Decision {
		return engine.Decision{Verdict: engine.Block}
	}}
	h := newTestHandler(f, okExchanger())
	w := udpWriter("192.168.1.150")

	h.ServeDNS(w, queryFor("cloaked.example", dns.TypeA))

	if got := w.only(t).Rcode; got != dns.RcodeServerFailure {
		t.Errorf("rcode = %s, want SERVFAIL rather than the answer the engine wanted blocked", dns.RcodeToString[got])
	}
}

func TestHandler_RecursionAvailableOnResolvedAnswersButNotOnRefusals(t *testing.T) {
	noRA := &fakeExchanger{fn: func(q *dns.Msg) (*dns.Msg, error) {
		resp := aReply(q)
		resp.RecursionAvailable = false
		return resp, nil
	}}
	tests := []struct {
		name   string
		filter *fakeFilter
		want   bool
	}{
		{"forwarded answer", &fakeFilter{}, true},
		{"client outside the ACL", &fakeFilter{before: func(q engine.Query) engine.Decision {
			return engine.Decision{Verdict: engine.Block, Resp: engine.Refuse(q.Req)}
		}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandler(tt.filter, noRA)
			w := udpWriter("192.168.1.150")

			h.ServeDNS(w, queryFor("fine.example", dns.TypeA))

			if got := w.only(t).RecursionAvailable; got != tt.want {
				t.Errorf("RA = %v, want %v", got, tt.want)
			}
		})
	}

	h := newTestHandler(&fakeFilter{}, noRA)
	for i := 0; i < 2; i++ {
		w := udpWriter("192.168.1.150")
		h.ServeDNS(w, queryFor("fine.example", dns.TypeA))
		if !w.only(t).RecursionAvailable {
			t.Errorf("answer %d (the second comes from the cache) has RA unset", i+1)
		}
	}
}
