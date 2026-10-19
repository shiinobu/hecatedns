package native

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

func exchange(t *testing.T, u upstream, q *dns.Msg) (*dns.Msg, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return u.Exchange(ctx, q)
}

func TestPlainUDP_AnswersAndRestoresTheCallersID(t *testing.T) {
	srv := goodDNS(t)
	u := newPlain("udp", srv.addr, testTimeout)

	resp, err := exchange(t, u, queryMsg("fine.example", dns.TypeA))
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if resp.Id != 31337 || len(resp.Answer) != 1 {
		t.Errorf("reply ID %d with %d answers, want the caller's ID 31337 and 1 answer", resp.Id, len(resp.Answer))
	}
	if u.Address() != srv.addr {
		t.Errorf("Address() = %q, want %q", u.Address(), srv.addr)
	}
}

func TestPlainUDP_RejectsAReplyForAnotherQuestion(t *testing.T) {
	srv := startFakeDNS(t, func(req *dns.Msg, _ string) *dns.Msg {
		return aReply(queryMsgWithID("other.example", dns.TypeA, req.Id))
	})
	u := newPlain("udp", srv.addr, testTimeout)

	if _, err := exchange(t, u, queryMsg("fine.example", dns.TypeA)); err == nil {
		t.Error("a reply to another question was accepted")
	}
}

func TestPlainUDP_RejectsAReplyThatIsNotAResponse(t *testing.T) {
	srv := startFakeDNS(t, func(req *dns.Msg, _ string) *dns.Msg {
		resp := aReply(req)
		resp.Response = false
		return resp
	})
	u := newPlain("udp", srv.addr, testTimeout)

	if _, err := exchange(t, u, queryMsg("fine.example", dns.TypeA)); err == nil {
		t.Error("a message without the QR bit was accepted as an answer")
	}
}

func TestPlainUDP_IgnoresAReplyWithAnotherIDAndTimesOut(t *testing.T) {
	srv := startFakeDNS(t, func(req *dns.Msg, _ string) *dns.Msg {
		resp := aReply(req)
		resp.Id = req.Id + 1
		return resp
	})
	u := newPlain("udp", srv.addr, 300*time.Millisecond)

	if _, err := exchange(t, u, queryMsg("fine.example", dns.TypeA)); err == nil {
		t.Error("a reply with a spoofed ID was accepted")
	}
}

func TestPlainUDP_RetriesOverTCPWhenTruncated(t *testing.T) {
	srv := startFakeDNS(t, func(req *dns.Msg, network string) *dns.Msg {
		resp := aReply(req)
		if network == "udp" {
			resp.Answer = nil
			resp.Truncated = true
		}
		return resp
	})
	u := newPlain("udp", srv.addr, testTimeout)

	resp, err := exchange(t, u, queryMsg("big.example", dns.TypeA))
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if resp.Truncated || len(resp.Answer) != 1 {
		t.Errorf("got truncated=%v with %d answers, want the full TCP answer", resp.Truncated, len(resp.Answer))
	}
	if nets := srv.networks(); len(nets) != 2 || nets[0] != "udp" || nets[1] != "tcp" {
		t.Errorf("networks used = %v, want udp then tcp", nets)
	}
}

func TestPlainTCP_Answers(t *testing.T) {
	srv := goodDNS(t)
	u := newPlain("tcp", srv.addr, testTimeout)

	resp, err := exchange(t, u, queryMsg("fine.example", dns.TypeA))
	if err != nil || len(resp.Answer) != 1 {
		t.Fatalf("Exchange = %v, %v; want one answer", resp, err)
	}
	if nets := srv.networks(); len(nets) != 1 || nets[0] != "tcp" {
		t.Errorf("networks used = %v, want only tcp", nets)
	}
}

func TestPlainUDP_FailsWhenNobodyAnswers(t *testing.T) {
	srv := startFakeDNS(t, func(*dns.Msg, string) *dns.Msg { return nil })
	u := newPlain("udp", srv.addr, 200*time.Millisecond)

	if _, err := exchange(t, u, queryMsg("fine.example", dns.TypeA)); err == nil {
		t.Error("expected a timeout error")
	}
}

func TestDoH_PostsTheQueryWithIDZeroAndRestoresTheID(t *testing.T) {
	f := startFakeDoH(t, func(w http.ResponseWriter, _ *http.Request, req *dns.Msg) { writeDNS(w, aReply(req)) })
	u := newTestDoH(t, f, loopbackURL(f), nil)

	resp, err := exchange(t, u, queryMsg("fine.example", dns.TypeA))
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if resp.Id != 31337 || len(resp.Answer) != 1 {
		t.Errorf("reply ID %d with %d answers, want 31337 and 1", resp.Id, len(resp.Answer))
	}
	reqs, methods := f.seen()
	if len(reqs) != 1 || reqs[0].Id != 0 {
		t.Errorf("server saw %d requests, first ID %v; want one request with ID 0 (RFC 8484)", len(reqs), reqs)
	}
	if len(methods) != 1 || methods[0] != "POST application/dns-message" {
		t.Errorf("method and content type = %v, want POST application/dns-message", methods)
	}
}

func TestDoH_RejectsBrokenAnswers(t *testing.T) {
	cases := map[string]func(w http.ResponseWriter, r *http.Request, req *dns.Msg){
		"HTTP 500": func(w http.ResponseWriter, _ *http.Request, _ *dns.Msg) { http.Error(w, "boom", 500) },
		"wrong content type": func(w http.ResponseWriter, _ *http.Request, req *dns.Msg) {
			wire, _ := aReply(req).Pack()
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write(wire)
		},
		"garbage body": func(w http.ResponseWriter, _ *http.Request, _ *dns.Msg) {
			w.Header().Set("Content-Type", "application/dns-message")
			_, _ = w.Write([]byte("not dns"))
		},
		"reply to another question": func(w http.ResponseWriter, _ *http.Request, req *dns.Msg) {
			writeDNS(w, aReply(queryMsgWithID("other.example", dns.TypeA, req.Id)))
		},
		"reply with a non-zero ID": func(w http.ResponseWriter, _ *http.Request, req *dns.Msg) {
			resp := aReply(req)
			resp.Id = 99
			writeDNS(w, resp)
		},
		"body over the DNS limit": func(w http.ResponseWriter, _ *http.Request, _ *dns.Msg) {
			w.Header().Set("Content-Type", "application/dns-message")
			_, _ = w.Write(make([]byte, 70000))
		},
		"redirect": func(w http.ResponseWriter, r *http.Request, _ *dns.Msg) {
			http.Redirect(w, r, "https://evil.example/dns-query", http.StatusFound)
		},
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			f := startFakeDoH(t, reply)
			u := newTestDoH(t, f, loopbackURL(f), nil)
			if _, err := exchange(t, u, queryMsg("fine.example", dns.TypeA)); err == nil {
				t.Errorf("%s was accepted", name)
			}
		})
	}
}

func TestDoH_ReachesAHostnameThroughTheBootstrapResolver(t *testing.T) {
	f := startFakeDoH(t, func(w http.ResponseWriter, _ *http.Request, req *dns.Msg) { writeDNS(w, aReply(req)) })
	boot := startFakeDNS(t, func(req *dns.Msg, _ string) *dns.Msg {
		resp := new(dns.Msg).SetReply(req)
		if req.Question[0].Qtype == dns.TypeA {
			resp.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   []byte{127, 0, 0, 1},
			}}
		}
		return resp
	})
	// The httptest certificate is valid for example.com.
	u := newTestDoH(t, f, "https://example.com:"+f.port()+"/dns-query", newBootstrap([]string{boot.addr}, testTimeout))

	if _, err := exchange(t, u, queryMsg("fine.example", dns.TypeA)); err != nil {
		t.Fatalf("Exchange through bootstrap: %v", err)
	}
	if asked := boot.asked(); len(asked) == 0 || !strings.EqualFold(asked[0].Name, "example.com.") {
		t.Errorf("bootstrap was asked %v, want example.com", asked)
	}
}

func TestDoH_HostnameWithoutBootstrapFails(t *testing.T) {
	f := startFakeDoH(t, func(w http.ResponseWriter, _ *http.Request, req *dns.Msg) { writeDNS(w, aReply(req)) })
	u := newTestDoH(t, f, "https://example.com:"+f.port()+"/dns-query", nil)

	if _, err := exchange(t, u, queryMsg("fine.example", dns.TypeA)); err == nil {
		t.Error("a hostname upstream worked without a bootstrap resolver")
	}
}

func TestDoH_DoesNotTrustAnUnknownCertificate(t *testing.T) {
	f := startFakeDoH(t, func(w http.ResponseWriter, _ *http.Request, req *dns.Msg) { writeDNS(w, aReply(req)) })
	u, err := newDoH(loopbackURL(f), nil, testTimeout, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	if _, err := exchange(t, u, queryMsg("fine.example", dns.TypeA)); err == nil {
		t.Error("a self-signed certificate was accepted with the system roots")
	}
}

func TestBootstrap_FallsBackToTheNextServer(t *testing.T) {
	dead := startFakeDNS(t, func(*dns.Msg, string) *dns.Msg { return nil })
	good := startFakeDNS(t, func(req *dns.Msg, _ string) *dns.Msg {
		resp := new(dns.Msg).SetReply(req)
		if req.Question[0].Qtype == dns.TypeAAAA {
			resp.Answer = []dns.RR{&dns.AAAA{
				Hdr:  dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60},
				AAAA: []byte{0x20, 1, 0xd, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
			}}
		}
		return resp
	})
	b := newBootstrap([]string{dead.addr, good.addr}, 200*time.Millisecond)

	ips, err := b.lookup(context.Background(), "host.example")
	if err != nil || len(ips) != 1 || ips[0].String() != "2001:db8::1" {
		t.Errorf("lookup = %v, %v; want 2001:db8::1 from the second server", ips, err)
	}
}

// stub is a scripted upstream for pool tests.
type stub struct {
	name  string
	fail  bool
	rcode int // when set, the stub answers with this rcode and no records
	delay time.Duration

	mu    sync.Mutex
	calls int
}

func (s *stub) Address() string { return s.name }
func (s *stub) Close() error    { return nil }
func (s *stub) Exchange(ctx context.Context, q *dns.Msg) (*dns.Msg, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.fail {
		return nil, errors.New(s.name + " is down")
	}
	if s.rcode != 0 {
		return new(dns.Msg).SetRcode(q, s.rcode), nil
	}
	return aReply(q), nil
}

func (s *stub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func fixedRand(v float64) func() float64 { return func() float64 { return v } }

func TestPool_FailsOverToTheNextUpstream(t *testing.T) {
	down, up := &stub{name: "down", fail: true}, &stub{name: "up"}
	p := newPool([]upstream{down, up}, fixedRand(0))

	resp, from, err := p.Exchange(context.Background(), queryMsg("fine.example", dns.TypeA))
	if err != nil || len(resp.Answer) != 1 {
		t.Fatalf("Exchange = %v, %v; want an answer from the healthy upstream", resp, err)
	}
	if from != "up" {
		t.Errorf("answered by %q, want up", from)
	}
}

func TestPool_FailsOverWhenAnUpstreamAnswersServFailOrRefused(t *testing.T) {
	for _, rcode := range []int{dns.RcodeServerFailure, dns.RcodeRefused} {
		throttled, healthy := &stub{name: "throttled", rcode: rcode}, &stub{name: "healthy"}
		p := newPool([]upstream{throttled, healthy}, fixedRand(0))

		resp, from, err := p.Exchange(context.Background(), queryMsg("fine.example", dns.TypeA))
		if err != nil || from != "healthy" || len(resp.Answer) != 1 {
			t.Errorf("%s from the first upstream: got %v from %q (%v), want the healthy upstream's answer", dns.RcodeToString[rcode], resp, from, err)
		}
	}
}

func TestPool_ReturnsTheUpstreamsServFailWhenEveryoneFails(t *testing.T) {
	p := newPool([]upstream{&stub{name: "one", rcode: dns.RcodeServerFailure}, &stub{name: "two", rcode: dns.RcodeServerFailure}}, fixedRand(0))

	resp, _, err := p.Exchange(context.Background(), queryMsg("fine.example", dns.TypeA))
	if err != nil || resp == nil || resp.Rcode != dns.RcodeServerFailure {
		t.Errorf("Exchange = %v, %v; want the upstream's SERVFAIL answer, not an error", resp, err)
	}
}

func TestPool_NXDOMAINIsAFinalAnswer(t *testing.T) {
	first, second := &stub{name: "first", rcode: dns.RcodeNameError}, &stub{name: "second"}
	p := newPool([]upstream{first, second}, fixedRand(0))

	resp, from, err := p.Exchange(context.Background(), queryMsg("gone.example", dns.TypeA))
	if err != nil || from != "first" || resp.Rcode != dns.RcodeNameError {
		t.Errorf("got %v from %q (%v), want the first upstream's NXDOMAIN", resp, from, err)
	}
	if second.count() != 0 {
		t.Error("the pool asked a second upstream after a definitive NXDOMAIN")
	}
}

func TestPool_ReportsEveryFailureWhenAllFail(t *testing.T) {
	p := newPool([]upstream{&stub{name: "one", fail: true}, &stub{name: "two", fail: true}}, fixedRand(0))

	_, _, err := p.Exchange(context.Background(), queryMsg("fine.example", dns.TypeA))
	if err == nil || !strings.Contains(err.Error(), "one is down") || !strings.Contains(err.Error(), "two is down") {
		t.Errorf("error = %v, want both upstream failures", err)
	}
}

func TestPool_PrefersTheFasterUpstream(t *testing.T) {
	fast, slow := &stub{name: "fast"}, &stub{name: "slow"}
	p := newPool([]upstream{slow, fast}, fixedRand(0.5))
	p.members[0].srtt = 1000
	p.members[1].srtt = 10

	for i := 0; i < 20; i++ {
		if _, from, err := p.Exchange(context.Background(), queryMsg("fine.example", dns.TypeA)); err != nil || from != "fast" {
			t.Fatalf("query %d answered by %q (%v), want fast", i, from, err)
		}
	}
	if slow.count() != 0 {
		t.Errorf("the slow upstream was asked %d times while the fast one worked", slow.count())
	}
}

func TestPool_AFailingUpstreamLosesItsPreference(t *testing.T) {
	p := newPool([]upstream{&stub{name: "flaky"}, &stub{name: "steady"}}, fixedRand(0.5))

	for i := 0; i < 3; i++ {
		p.record(0, 0, errors.New("timeout"))
	}
	if first := p.order()[0]; first != 1 {
		t.Errorf("after failures the first choice is member %d, want the steady one (1)", first)
	}
}

func TestPool_StopsWhenTheContextEnds(t *testing.T) {
	slow := &stub{name: "slow", delay: time.Second}
	next := &stub{name: "next"}
	p := newPool([]upstream{slow, next}, fixedRand(0))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := p.Exchange(ctx, queryMsg("fine.example", dns.TypeA)); err == nil {
		t.Error("expected an error once the context ended")
	}
	if next.count() != 0 {
		t.Error("the pool kept trying upstreams after its context ended")
	}
}

func TestPool_ConcurrentUse(t *testing.T) {
	p := newPool([]upstream{&stub{name: "a"}, &stub{name: "b", fail: true}}, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if _, _, err := p.Exchange(context.Background(), queryMsg("fine.example", dns.TypeA)); err != nil {
					t.Errorf("Exchange: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestParseUpstream(t *testing.T) {
	cases := []struct {
		line    string
		wantAdr string
		wantErr string
	}{
		{"1.1.1.1", "1.1.1.1:53", ""},
		{"9.9.9.9:5353", "9.9.9.9:5353", ""},
		{"[2606:4700:4700::1111]:53", "[2606:4700:4700::1111]:53", ""},
		{"udp://8.8.8.8", "8.8.8.8:53", ""},
		{"tcp://8.8.4.4:5353", "8.8.4.4:5353", ""},
		{"https://1.1.1.1/dns-query", "https://1.1.1.1/dns-query", ""},
		{"https://dns.example", "https://dns.example/dns-query", ""},
		{"tls://1.1.1.1", "", "DNS over TLS"},
		{"udp://dns.example", "", "IP address"},
		{"ftp://1.1.1.1", "", "unsupported"},
		{"", "", "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			u, err := parseUpstream(tc.line, newBootstrap([]string{"127.0.0.1:53"}, testTimeout), testTimeout)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want one mentioning %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseUpstream: %v", err)
			}
			defer u.Close()
			if u.Address() != tc.wantAdr {
				t.Errorf("Address() = %q, want %q", u.Address(), tc.wantAdr)
			}
		})
	}
}

func TestBuildPool_ReportsEveryBadUpstream(t *testing.T) {
	cfg := config.Default()
	cfg.Upstreams = []string{"tls://1.1.1.1", "ftp://x", "1.1.1.1"}

	_, err := buildPool(cfg, testTimeout)
	if err == nil || !strings.Contains(err.Error(), "upstreams[0]") || !strings.Contains(err.Error(), "upstreams[1]") {
		t.Errorf("error = %v, want both bad entries named", err)
	}
}

func TestBuildPool_BuildsTheDefaultUpstreams(t *testing.T) {
	p, err := buildPool(config.Default(), testTimeout)
	if err != nil {
		t.Fatalf("buildPool: %v", err)
	}
	defer p.Close()
	if len(p.members) != 2 {
		t.Errorf("members = %d, want the two default DoH upstreams", len(p.members))
	}
}

// queryMsgWithID is queryMsg with a chosen ID, to craft replies.
func queryMsgWithID(name string, qtype uint16, id uint16) *dns.Msg {
	q := queryMsg(name, qtype)
	q.Id = id
	return q
}

func TestDoH_HostnameStillWorksWhenTheFirstBootstrapServerIsDead(t *testing.T) {
	f := startFakeDoH(t, func(w http.ResponseWriter, _ *http.Request, req *dns.Msg) { writeDNS(w, aReply(req)) })
	dead := startFakeDNS(t, func(*dns.Msg, string) *dns.Msg { return nil })
	good := startFakeDNS(t, func(req *dns.Msg, _ string) *dns.Msg {
		resp := new(dns.Msg).SetReply(req)
		if req.Question[0].Qtype == dns.TypeA {
			resp.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   []byte{127, 0, 0, 1},
			}}
		}
		return resp
	})
	// Production gives the bootstrap lookup the same deadline as the whole DoH
	// request; a dead first server must not use all of it.
	u := newTestDoH(t, f, "https://example.com:"+f.port()+"/dns-query", newBootstrap([]string{dead.addr, good.addr}, testTimeout))

	if _, err := exchange(t, u, queryMsg("fine.example", dns.TypeA)); err != nil {
		t.Fatalf("Exchange with a dead first bootstrap server: %v", err)
	}
	if asked := good.asked(); len(asked) == 0 {
		t.Error("the second bootstrap server was never asked")
	}
}

func TestShareOfDeadline(t *testing.T) {
	const second = time.Second
	near := func(got, want time.Duration) bool { return got <= want && got > want-200*time.Millisecond }

	parent, cancel := context.WithTimeout(context.Background(), 2*second)
	defer cancel()
	for _, tc := range []struct {
		name  string
		parts int
		want  time.Duration
	}{{"the only part gets everything left", 1, 2 * second}, {"one of two gets half", 2, second}, {"one of four gets a quarter", 4, second / 2}} {
		ctx, cancelShare := shareOfDeadline(parent, tc.parts)
		deadline, ok := ctx.Deadline()
		cancelShare()
		if !ok || !near(time.Until(deadline), tc.want) {
			t.Errorf("%s: deadline in %v, want about %v", tc.name, time.Until(deadline), tc.want)
		}
	}

	ctx, cancelNone := shareOfDeadline(context.Background(), 3)
	defer cancelNone()
	if _, ok := ctx.Deadline(); ok {
		t.Error("a context without a deadline got one")
	}
}
