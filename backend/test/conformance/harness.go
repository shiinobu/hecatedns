package conformance

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/core"
	"github.com/shiinobu/hecatedns/backend/engine"
	"github.com/shiinobu/hecatedns/backend/lib/config"
)

const (
	clientTimeout = 5 * time.Second
	waitStep      = 5 * time.Millisecond
	waitLimit     = 5 * time.Second

	upstreamTTL = 300
)

var (
	upstreamA    = net.IPv4(192, 0, 2, 1)
	upstreamAAAA = net.ParseIP("2001:db8::1")
)

type fakeUpstream struct {
	addr string

	mu     sync.Mutex
	seen   map[string]int
	cnames map[string][]string
}

func startUpstream(t *testing.T, cnames map[string][]string) *fakeUpstream {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fake upstream: listen: %v", err)
	}
	u := &fakeUpstream{addr: conn.LocalAddr().String(), seen: map[string]int{}, cnames: cnames}
	started := make(chan struct{})
	srv := &dns.Server{PacketConn: conn, Handler: dns.HandlerFunc(u.serve), NotifyStartedFunc: func() { close(started) }}
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })
	return u
}

func (u *fakeUpstream) serve(w dns.ResponseWriter, req *dns.Msg) {
	resp := new(dns.Msg).SetReply(req)
	if len(req.Question) == 1 {
		q := req.Question[0]
		lower := strings.ToLower(strings.TrimSuffix(q.Name, "."))
		u.mu.Lock()
		u.seen[lower]++
		chain := u.cnames[lower]
		u.mu.Unlock()

		owner := q.Name
		for _, target := range chain {
			fqdn := dns.Fqdn(target)
			resp.Answer = append(resp.Answer, &dns.CNAME{
				Hdr:    dns.RR_Header{Name: owner, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: upstreamTTL},
				Target: fqdn,
			})
			owner = fqdn
		}
		hdr := dns.RR_Header{Name: owner, Rrtype: q.Qtype, Class: dns.ClassINET, Ttl: upstreamTTL}
		switch q.Qtype {
		case dns.TypeA:
			resp.Answer = append(resp.Answer, &dns.A{Hdr: hdr, A: upstreamA})
		case dns.TypeAAAA:
			resp.Answer = append(resp.Answer, &dns.AAAA{Hdr: hdr, AAAA: upstreamAAAA})
		case dns.TypeHTTPS:
			resp.Answer = append(resp.Answer, &dns.HTTPS{SVCB: dns.SVCB{Hdr: hdr, Priority: 1, Target: "."}})
		case dns.TypeSVCB:
			resp.Answer = append(resp.Answer, &dns.SVCB{Hdr: hdr, Priority: 1, Target: "."})
		}
	}
	_ = w.WriteMsg(resp)
}

func (u *fakeUpstream) queries(name string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.seen[strings.ToLower(name)]
}

type recorder struct {
	mu      sync.Mutex
	entries []engine.Entry
}

func (r *recorder) Record(e engine.Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
}

func (r *recorder) snapshot() []engine.Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]engine.Entry(nil), r.entries...)
}

type setup struct {
	allowedClients []string
	rules          string
	blockList      []string
	allowList      []string
	cnames         map[string][]string
}

type harness struct {
	t        *testing.T
	upstream *fakeUpstream
	log      *recorder
	bound    core.Bound
}

func start(t *testing.T, factory core.Factory, s setup) *harness {
	t.Helper()
	h := &harness{t: t, upstream: startUpstream(t, s.cnames), log: &recorder{}}

	cfg := config.Default()
	cfg.Listen = []string{"127.0.0.1:0"}
	cfg.Upstreams = []string{h.upstream.addr}
	cfg.Bootstrap = nil
	if s.allowedClients != nil {
		cfg.AllowedClients = s.allowedClients
	}

	eng := h.newEngine(cfg, s)
	b, err := factory(core.Deps{Cfg: cfg, Engine: eng})
	if err != nil {
		t.Fatalf("build backend: %v", err)
	}
	bound, ok := b.(core.Bound)
	if !ok {
		t.Fatalf("backend %s does not implement core.Bound, the suite cannot find its port", b.Name())
	}
	h.bound = bound

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("backend Run returned %v after cancel", err)
			}
		case <-time.After(waitLimit):
			t.Error("backend did not stop after its context was cancelled")
		}
	})

	select {
	case <-bound.Ready():
	case err := <-done:
		t.Fatalf("backend failed to start: %v", err)
	case <-time.After(waitLimit):
		t.Fatal("backend never became ready")
	}
	if bound.Addr(core.NetworkUDP) == nil || bound.Addr(core.NetworkTCP) == nil {
		t.Fatal("backend is ready but reports no UDP or TCP address")
	}
	return h
}

func (h *harness) newEngine(cfg config.Config, s setup) *engine.Engine {
	t := h.t
	t.Helper()
	acl, err := engine.NewACL(cfg.AllowedClients)
	if err != nil {
		t.Fatalf("ACL: %v", err)
	}
	responder, err := engine.NewResponder(cfg.Block.Mode, cfg.Block.TTL)
	if err != nil {
		t.Fatalf("responder: %v", err)
	}
	rulesPath := filepath.Join(t.TempDir(), "rules.txt")
	if err := os.WriteFile(rulesPath, []byte(s.rules), 0o600); err != nil {
		t.Fatal(err)
	}
	rules := engine.NewRulesFile(rulesPath, nil)
	if _, err := rules.Reload(); err != nil {
		t.Fatalf("load rules: %v", err)
	}

	eng, err := engine.New(engine.Options{ACL: acl, Rules: rules, Responder: responder, Special: cfg.SpecialNames, Recorder: h.log})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	eng.SwapLists(buildLists(t, s))
	return eng
}

func buildLists(t *testing.T, s setup) *engine.ListSet {
	t.Helper()
	var allow, block []engine.NamedSet
	if s.allowList != nil {
		set, _ := engine.NewDomainSet(s.allowList)
		allow = append(allow, engine.NamedSet{Name: "test-allow", Set: set})
	}
	if s.blockList != nil {
		set, _ := engine.NewDomainSet(s.blockList)
		block = append(block, engine.NamedSet{Name: "test-block", Set: set})
	}
	return engine.NewListSet(allow, block)
}

func (h *harness) ask(proto, name string, qtype uint16) *dns.Msg {
	h.t.Helper()
	network := core.NetworkUDP
	if proto == "tcp" {
		network = core.NetworkTCP
	}
	req := new(dns.Msg)
	req.SetQuestion(dns.Fqdn(name), qtype)
	client := &dns.Client{Net: proto, Timeout: clientTimeout}
	resp, _, err := client.Exchange(req, h.bound.Addr(network).String())
	if err != nil {
		h.t.Fatalf("%s query for %s %s: %v", proto, name, dns.TypeToString[qtype], err)
	}
	return resp
}

func (h *harness) entries(n int) []engine.Entry {
	h.t.Helper()
	deadline := time.Now().Add(waitLimit)
	for {
		got := h.log.snapshot()
		if len(got) >= n || time.Now().After(deadline) {
			if len(got) != n {
				h.t.Fatalf("query log has %d records, want %d: %+v", len(got), n, got)
			}
			return got
		}
		time.Sleep(waitStep)
	}
}

func (h *harness) askOpcode(opcode int, name string, qtype uint16) *dns.Msg {
	h.t.Helper()
	req := new(dns.Msg)
	req.SetQuestion(dns.Fqdn(name), qtype)
	req.Opcode = opcode
	client := &dns.Client{Net: "udp", Timeout: clientTimeout}
	resp, _, err := client.Exchange(req, h.bound.Addr(core.NetworkUDP).String())
	if err != nil {
		h.t.Fatalf("opcode %d query for %s: %v", opcode, name, err)
	}
	return resp
}
