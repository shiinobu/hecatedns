package dnsproxybe

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"xdns/internal/backend"
	"xdns/internal/config"
	"xdns/internal/engine"
)

func TestNewRequiresEngine(t *testing.T) {
	if _, err := New(backend.Deps{Cfg: testConfig("127.0.0.1:53")}); err == nil {
		t.Fatal("New without engine returned no error")
	}
}

func TestBlockedNameNeverReachesUpstream(t *testing.T) {
	up := startFakeUpstream(t, 300)
	cfg := testConfig(up.addr)
	rec := &memRecorder{}
	eng := newTestEngine(t, cfg, "block ads.example\n", map[string][]string{"trackers": {"trk.example"}}, rec)
	r := startEngineBackend(t, cfg, eng)

	if b := r.b; b.Name() != config.BackendDNSProxy {
		t.Fatalf("Name() = %q, want %q", b.Name(), config.BackendDNSProxy)
	}
	udp := exchange(t, "udp", r.addr(t, backend.NetworkUDP), "www.ads.example", dns.TypeA, 1)
	tcp := exchange(t, "tcp", r.addr(t, backend.NetworkTCP), "trk.example", dns.TypeA, 2)

	if got := answerIP(t, udp); got != "0.0.0.0" {
		t.Fatalf("udp blocked answer %s, want 0.0.0.0", got)
	}
	if got := answerIP(t, tcp); got != "0.0.0.0" {
		t.Fatalf("tcp blocked answer %s, want 0.0.0.0", got)
	}
	if n := up.queries.Load(); n != 0 {
		t.Fatalf("upstream saw %d queries, want 0", n)
	}
	entries := rec.all()
	if len(entries) != 2 || entries[0].Reason != engine.ReasonRule || entries[1].Reason != engine.ReasonList ||
		entries[0].Proto != "udp" || entries[1].Proto != "tcp" || entries[1].Source != "trackers" {
		t.Fatalf("records = %+v, want a udp rule block and a tcp list block", entries)
	}
}

func TestPassedNameGetsUpstreamAnswer(t *testing.T) {
	up := startFakeUpstream(t, 300)
	cfg := testConfig(up.addr)
	rec := &memRecorder{}
	eng := newTestEngine(t, cfg, "block example.org\nallow www.example.org\n", map[string][]string{"ads": {"www.example.org"}}, rec)
	r := startEngineBackend(t, cfg, eng)
	addr := r.addr(t, backend.NetworkUDP)

	if got := answerIP(t, exchange(t, "udp", addr, "www.example.net", dns.TypeA, 1)); got != upstreamIP {
		t.Fatalf("passed answer %s, want %s", got, upstreamIP)
	}
	if got := answerIP(t, exchange(t, "udp", addr, "www.example.org", dns.TypeA, 2)); got != upstreamIP {
		t.Fatalf("allowed answer %s, want %s (custom allow beats block rule and list)", got, upstreamIP)
	}
	entries := rec.all()
	if len(entries) != 2 {
		t.Fatalf("records = %+v, want 2", entries)
	}
	_, port, _ := net.SplitHostPort(up.addr)
	for _, e := range entries {
		if e.Action != engine.ActionPass || e.RCode != "NOERROR" || e.Cached || !strings.Contains(e.Upstream, port) {
			t.Fatalf("record %+v, want an uncached NOERROR pass from upstream port %s", e, port)
		}
	}
	if entries[1].Reason != engine.ReasonAllow || entries[1].Source != "allow www.example.org" {
		t.Fatalf("allowed record = %+v, want reason allow from the rule", entries[1])
	}
}

func TestClientOutsideACLIsRefused(t *testing.T) {
	up := startFakeUpstream(t, 300)
	cfg := testConfig(up.addr)
	cfg.AllowedClients = []string{"10.0.0.0/8"}
	rec := &memRecorder{}
	r := startEngineBackend(t, cfg, newTestEngine(t, cfg, "", nil, rec))

	resp := exchange(t, "udp", r.addr(t, backend.NetworkUDP), "www.example.net", dns.TypeA, 1)

	if resp.Rcode != dns.RcodeRefused || up.queries.Load() != 0 {
		t.Fatalf("rcode %s with %d upstream queries, want REFUSED and 0", dns.RcodeToString[resp.Rcode], up.queries.Load())
	}
	if got := rec.all(); len(got) != 1 || got[0].Reason != engine.ReasonACL {
		t.Fatalf("records = %+v, want one acl record", got)
	}
}

func TestCacheHitStillRunsAfter(t *testing.T) {
	up := startFakeUpstream(t, 300)
	cfg := testConfig(up.addr)
	rec := &memRecorder{}
	r := startEngineBackend(t, cfg, newTestEngine(t, cfg, "", nil, rec))
	addr := r.addr(t, backend.NetworkUDP)

	first := exchange(t, "udp", addr, "cached.example", dns.TypeA, 1)
	second := exchange(t, "udp", addr, "cached.example", dns.TypeA, 2)

	if answerIP(t, first) != upstreamIP || answerIP(t, second) != upstreamIP {
		t.Fatal("cached answer differs from the upstream answer")
	}
	if n := up.queries.Load(); n != 1 {
		t.Fatalf("upstream saw %d queries, want 1 (second from cache)", n)
	}
	entries := rec.all()
	if len(entries) != 2 || entries[0].Cached || !entries[1].Cached || entries[1].Upstream != "" {
		t.Fatalf("records = %+v, want one uncached and one cached pass", entries)
	}
}

func TestCacheMaxTTL(t *testing.T) {
	tests := []struct {
		name    string
		maxTTL  time.Duration
		wantTTL uint32
	}{
		{"zero means no cap", 0, 7200},
		{"cap applies", time.Minute, 60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			up := startFakeUpstream(t, 7200)
			cfg := testConfig(up.addr)
			cfg.Cache.MaxTTL = tt.maxTTL
			r := startEngineBackend(t, cfg, newTestEngine(t, cfg, "", nil, nil))
			addr := r.addr(t, backend.NetworkUDP)

			for id := uint16(1); id <= 2; id++ {
				resp := exchange(t, "udp", addr, "ttl.example", dns.TypeA, id)
				answerIP(t, resp)
				if ttl := resp.Answer[0].Header().Ttl; ttl > tt.wantTTL || ttl+2 < tt.wantTTL {
					t.Fatalf("query %d: TTL %d, want about %d", id, ttl, tt.wantTTL)
				}
			}
		})
	}
}

func TestUpstreamDownGivesServfailAndErrorRecord(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	deadAddr := pc.LocalAddr().String()
	if err := pc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	cfg := testConfig(deadAddr)
	rec := &memRecorder{}
	// Windows reports no ICMP error for UDP, so the dead upstream is only
	// noticed by the timeout; keep it short.
	s, err := newServer(cfg, newTestEngine(t, cfg, "", nil, rec), nil, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	r := startRunning(t, s)

	resp := exchange(t, "udp", r.addr(t, backend.NetworkUDP), "down.example", dns.TypeA, 1)

	if resp.Rcode != dns.RcodeServerFailure {
		t.Fatalf("rcode %s, want SERVFAIL", dns.RcodeToString[resp.Rcode])
	}
	got := rec.all()
	if len(got) != 1 || got[0].Action != engine.ActionError || got[0].RCode != "SERVFAIL" || got[0].Error == "" {
		t.Fatalf("records = %+v, want one error record", got)
	}
}

// panicFilter panics in the named stage and otherwise passes everything.
type panicFilter struct{ stage string }

func (f panicFilter) Before(context.Context, engine.Query) engine.Decision {
	if f.stage == "before" {
		panic("before exploded")
	}
	return engine.Decision{Verdict: engine.Pass}
}

func (f panicFilter) After(context.Context, engine.Query, *dns.Msg, engine.Meta) engine.Decision {
	if f.stage == "after" {
		panic("after exploded")
	}
	return engine.Decision{Verdict: engine.Pass}
}

func (f panicFilter) Failed(context.Context, engine.Query, error) { panic("failed exploded") }

func TestPanickingFilterStillAnswers(t *testing.T) {
	for _, stage := range []string{"before", "after"} {
		t.Run(stage, func(t *testing.T) {
			up := startFakeUpstream(t, 300)
			s, err := newServer(testConfig(up.addr), panicFilter{stage: stage}, nil, UpstreamTimeout)
			if err != nil {
				t.Fatalf("newServer: %v", err)
			}
			r := startRunning(t, s)

			resp := exchange(t, "udp", r.addr(t, backend.NetworkUDP), "panic.example", dns.TypeA, 1)

			if got := answerIP(t, resp); got != upstreamIP {
				t.Fatalf("answer %s, want the upstream answer %s (fail open)", got, upstreamIP)
			}
		})
	}
}

func TestRunStopsOnCancelAndFreesPorts(t *testing.T) {
	up := startFakeUpstream(t, 300)
	cfg := testConfig(up.addr)
	r := startEngineBackend(t, cfg, newTestEngine(t, cfg, "", nil, nil))
	udpAddr, tcpAddr := r.addr(t, backend.NetworkUDP).String(), r.addr(t, backend.NetworkTCP).String()

	if err := r.shutdown(t); err != nil {
		t.Fatalf("Run after cancel = %v, want nil", err)
	}
	pc, err := net.ListenPacket("udp", udpAddr)
	if err != nil {
		t.Fatalf("udp port still taken after shutdown: %v", err)
	}
	_ = pc.Close()
	ln, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		t.Fatalf("tcp port still taken after shutdown: %v", err)
	}
	_ = ln.Close()
}

func TestRunTwiceFails(t *testing.T) {
	up := startFakeUpstream(t, 300)
	cfg := testConfig(up.addr)
	r := startEngineBackend(t, cfg, newTestEngine(t, cfg, "", nil, nil))

	if err := r.b.Run(context.Background()); err == nil {
		t.Fatal("second Run returned nil, want an error")
	}
}

// occupyTCPPortWithFreeUDP returns a TCP listener whose port number can still
// be bound over UDP. dnsproxy binds UDP first, so the TCP bind is then the one
// that fails, which is what the bind-failure test is about. Picking a TCP port
// first does not work on Windows: its excluded UDP port ranges (Hyper-V,
// WinNAT) can make the UDP bind fail instead.
func occupyTCPPortWithFreeUDP(t *testing.T) net.Listener {
	t.Helper()
	for attempt := 0; attempt < 50; attempt++ {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("pick a udp port: %v", err)
		}
		port := pc.LocalAddr().(*net.UDPAddr).Port
		_ = pc.Close()
		if ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err == nil {
			return ln
		}
	}
	t.Fatal("no port is free for both tcp and udp")
	return nil
}

func TestBindFailureEndsRunAndReleasesListeners(t *testing.T) {
	taken := occupyTCPPortWithFreeUDP(t)
	defer taken.Close()
	addr := taken.Addr().String()
	cfg := testConfig("127.0.0.1:53")
	cfg.Listen = []string{addr}
	b, err := New(backend.Deps{Cfg: cfg, Engine: newTestEngine(t, cfg, "", nil, nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- b.Run(context.Background()) }()
	select {
	case err = <-done:
	case <-time.After(testTimeout):
		t.Fatal("Run did not return on a bind failure")
	}

	bound := b.(backend.Bound)
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(taken.Addr().(*net.TCPAddr).Port)) {
		t.Fatalf("Run = %v, want a listen error naming the port", err)
	}
	select {
	case <-bound.Ready():
	default:
		t.Fatal("Ready not closed after a failed start")
	}
	if a := bound.Addr(backend.NetworkUDP); a != nil {
		t.Fatalf("Addr(udp) = %v after a failed start, want nil", a)
	}
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatalf("udp listener opened before the failure was not released: %v", err)
	}
	_ = pc.Close()
}

func TestAddrUnknownNetwork(t *testing.T) {
	up := startFakeUpstream(t, 300)
	cfg := testConfig(up.addr)
	r := startEngineBackend(t, cfg, newTestEngine(t, cfg, "", nil, nil))

	if a := r.b.(backend.Bound).Addr("quic"); a != nil {
		t.Fatalf("Addr(quic) = %v, want nil", a)
	}
}

func TestNewReportsConfigErrors(t *testing.T) {
	cfg := testConfig("foo://bad")
	cfg.Listen = []string{"not-an-address"}
	_, err := New(backend.Deps{Cfg: cfg, Engine: newTestEngine(t, testConfig("127.0.0.1:53"), "", nil, nil)})
	if err == nil || !strings.Contains(err.Error(), "listen[0]") || !strings.Contains(err.Error(), "upstreams") {
		t.Fatalf("New = %v, want listen and upstream errors reported together", err)
	}
}

// blockFirstAfter blocks the first answer it sees in After and passes the rest.
type blockFirstAfter struct{ calls atomic.Int64 }

func (f *blockFirstAfter) Before(context.Context, engine.Query) engine.Decision {
	return engine.Decision{Verdict: engine.Pass}
}

func (f *blockFirstAfter) After(_ context.Context, q engine.Query, _ *dns.Msg, _ engine.Meta) engine.Decision {
	if f.calls.Add(1) > 1 {
		return engine.Decision{Verdict: engine.Pass}
	}
	return engine.Decision{Verdict: engine.Block, Resp: new(dns.Msg).SetRcode(q.Req, dns.RcodeNameError)}
}

func (f *blockFirstAfter) Failed(context.Context, engine.Query, error) {}

// TestAfterBlockReplacesAnswerButNotCache proves that a Block from After
// reaches the client while dnsproxy's cache keeps the upstream answer, which
// the Phase 2 CNAME check relies on.
func TestAfterBlockReplacesAnswerButNotCache(t *testing.T) {
	up := startFakeUpstream(t, 300)
	s, err := newServer(testConfig(up.addr), &blockFirstAfter{}, nil, UpstreamTimeout)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	addr := startRunning(t, s).addr(t, backend.NetworkUDP)

	first := exchange(t, "udp", addr, "after.example", dns.TypeA, 1)
	second := exchange(t, "udp", addr, "after.example", dns.TypeA, 2)

	if first.Rcode != dns.RcodeNameError {
		t.Fatalf("first answer %s, want the NXDOMAIN from After", dns.RcodeToString[first.Rcode])
	}
	if got := answerIP(t, second); got != upstreamIP || up.queries.Load() != 1 {
		t.Fatalf("second answer %s with %d upstream queries, want the cached upstream answer and 1", got, up.queries.Load())
	}
}
