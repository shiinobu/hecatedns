package dnsproxybe

import (
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"xdns/internal/backend"
	"xdns/internal/config"
	"xdns/internal/engine"
)

const (
	testTimeout = 5 * time.Second
	upstreamIP  = "192.0.2.1"
)

// fakeUpstream is a plain UDP DNS server on loopback that answers every A
// query with upstreamIP and counts what it receives.
type fakeUpstream struct {
	addr    string
	ttl     uint32
	queries atomic.Int64
}

func startFakeUpstream(t *testing.T, ttl uint32) *fakeUpstream {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fake upstream: %v", err)
	}
	f := &fakeUpstream{addr: pc.LocalAddr().String(), ttl: ttl}
	started := make(chan struct{})
	srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(f.serve), NotifyStartedFunc: func() { close(started) }}
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })
	return f
}

func (f *fakeUpstream) serve(w dns.ResponseWriter, req *dns.Msg) {
	f.queries.Add(1)
	resp := new(dns.Msg).SetReply(req)
	if q := req.Question[0]; q.Qtype == dns.TypeA {
		hdr := dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: f.ttl}
		resp.Answer = []dns.RR{&dns.A{Hdr: hdr, A: net.ParseIP(upstreamIP).To4()}}
	}
	_ = w.WriteMsg(resp)
}

// memRecorder keeps every record; safe for concurrent use.
type memRecorder struct {
	mu      sync.Mutex
	entries []engine.Entry
}

func (r *memRecorder) Record(e engine.Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
}

func (r *memRecorder) all() []engine.Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]engine.Entry(nil), r.entries...)
}

type staticRules struct{ set *engine.RuleSet }

func (s staticRules) Snapshot() *engine.RuleSet { return s.set }

// testConfig returns the default configuration listening on a random
// loopback port and forwarding to upstream.
func testConfig(upstream string) config.Config {
	cfg := config.Default()
	cfg.Listen = []string{"127.0.0.1:0"}
	cfg.Upstreams = []string{upstream}
	cfg.Bootstrap = nil
	return cfg
}

// newTestEngine builds an engine from cfg with the given rules text and block
// lists (name -> domains).
func newTestEngine(t *testing.T, cfg config.Config, rules string, blockLists map[string][]string, rec engine.Recorder) *engine.Engine {
	t.Helper()
	acl, err := engine.NewACL(cfg.AllowedClients)
	if err != nil {
		t.Fatalf("NewACL: %v", err)
	}
	responder, err := engine.NewResponder(cfg.Block.Mode, cfg.Block.TTL)
	if err != nil {
		t.Fatalf("NewResponder: %v", err)
	}
	set, issues, err := engine.ParseRules(strings.NewReader(rules))
	if err != nil || len(issues) != 0 {
		t.Fatalf("ParseRules: %v %v", err, issues)
	}
	e, err := engine.New(engine.Options{ACL: acl, Rules: staticRules{set: set}, Responder: responder, Recorder: rec})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	var block []engine.NamedSet
	for name, domains := range blockLists {
		domainSet, _ := engine.NewDomainSet(domains)
		block = append(block, engine.NamedSet{Name: name, Set: domainSet})
	}
	e.SwapLists(engine.NewListSet(nil, block))
	return e
}

// running is a backend started by startRunning. done is closed when Run
// returned; err is its result.
type running struct {
	b    backend.Backend
	stop context.CancelFunc
	done chan struct{}
	err  error
}

func (r *running) addr(t *testing.T, network string) net.Addr {
	t.Helper()
	addr := r.b.(backend.Bound).Addr(network)
	if addr == nil {
		t.Fatalf("no %s address", network)
	}
	return addr
}

// shutdown cancels Run and returns its result.
func (r *running) shutdown(t *testing.T) error {
	t.Helper()
	r.stop()
	select {
	case <-r.done:
		return r.err
	case <-time.After(testTimeout):
		t.Fatal("Run did not return after cancel")
		return nil
	}
}

// startRunning runs b until the test ends and waits until it listens.
func startRunning(t *testing.T, b backend.Backend) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{b: b, stop: cancel, done: make(chan struct{})}
	go func() {
		r.err = b.Run(ctx)
		close(r.done)
	}()
	select {
	case <-b.(backend.Bound).Ready():
	case <-time.After(testTimeout):
		t.Fatal("backend not ready")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(testTimeout):
			t.Error("Run did not return at cleanup")
		}
	})
	return r
}

// startEngineBackend builds the backend for cfg and eng with New and runs it.
func startEngineBackend(t *testing.T, cfg config.Config, eng *engine.Engine) *running {
	t.Helper()
	b, err := New(backend.Deps{Cfg: cfg, Engine: eng})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return startRunning(t, b)
}

// exchange sends one query over network to addr and returns the answer.
func exchange(t *testing.T, network string, addr net.Addr, name string, qtype, id uint16) *dns.Msg {
	t.Helper()
	req := new(dns.Msg).SetQuestion(dns.Fqdn(name), qtype)
	req.Id = id
	client := &dns.Client{Net: network, Timeout: testTimeout}
	resp, _, err := client.Exchange(req, addr.String())
	if err != nil {
		t.Fatalf("exchange %s %s: %v", network, name, err)
	}
	if resp.Id != id {
		t.Fatalf("answer id %d, want %d", resp.Id, id)
	}
	return resp
}

// answerIP returns the address of the single A record of resp.
func answerIP(t *testing.T, resp *dns.Msg) string {
	t.Helper()
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Fatalf("answer rcode %s with %d records, want NOERROR with one A", dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	a, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("answer record %v is not A", resp.Answer[0])
	}
	return a.A.String()
}
