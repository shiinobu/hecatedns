// Package conformance holds the test suite every backend must pass: the same
// questions asked of each implementation, against a fake upstream on loopback,
// so a block, an allow or a refusal looks identical whichever backend serves it.
package conformance

import (
	"net"
	"testing"

	"github.com/miekg/dns"

	"xdns/internal/backend"
	"xdns/internal/engine"
)

// Constructor names a backend factory under test.
type Constructor struct {
	Name    string
	Factory backend.Factory
}

type testCase struct {
	name string
	run  func(t *testing.T, factory backend.Factory)
}

// Run executes the whole suite once per constructor, as subtests. A backend
// factory must produce a backend that implements backend.Bound, because the
// suite listens on random ports and has to ask where it ended up.
func Run(t *testing.T, constructors []Constructor) {
	t.Helper()
	for _, c := range constructors {
		t.Run(c.Name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) { tc.run(t, c.Factory) })
			}
		})
	}
}

var cases = []testCase{
	{"blocked A answers 0.0.0.0 without asking the upstream", blockedA},
	{"blocked AAAA answers ::", blockedAAAA},
	{"blocked HTTPS answers NODATA with an SOA", blockedHTTPS},
	{"blocked name over TCP", blockedOverTCP},
	{"blocking ignores case and covers subdomains only", blockedCaseAndSuffix},
	{"passed query returns the upstream answer", passedQuery},
	{"custom allow beats a list block", allowBeatsListBlock},
	{"custom block rule blocks and names its rule", customBlockRule},
	{"list allow beats list block", listAllowBeatsListBlock},
	{"client outside allowed_clients is refused", aclRefuses},
	{"one log record per query, cache hits included", logRecordPerQuery},
}

var blockSetup = setup{blockList: []string{"ads.test"}}

func blockedA(t *testing.T, factory backend.Factory) {
	h := start(t, factory, blockSetup)

	resp := h.ask("udp", "ads.test", dns.TypeA)

	assertNullAnswer(t, resp, dns.TypeA, net.IPv4zero)
	if got := h.upstream.queries("ads.test"); got != 0 {
		t.Errorf("a blocked name reached the upstream %d times", got)
	}
	e := h.entries(1)[0]
	if e.Action != engine.ActionBlock || e.Reason != engine.ReasonList || e.Source != "test-block" || e.QName != "ads.test." && e.QName != "ads.test" {
		t.Errorf("record = %+v, want a list block by test-block for ads.test", e)
	}
}

func blockedAAAA(t *testing.T, factory backend.Factory) {
	h := start(t, factory, blockSetup)

	assertNullAnswer(t, h.ask("udp", "ads.test", dns.TypeAAAA), dns.TypeAAAA, net.IPv6zero)
}

func blockedHTTPS(t *testing.T, factory backend.Factory) {
	h := start(t, factory, blockSetup)

	resp := h.ask("udp", "ads.test", dns.TypeHTTPS)

	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 0 {
		t.Errorf("HTTPS for a blocked name: rcode %s with %d answers, want NOERROR and none", dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	if len(resp.Ns) != 1 || resp.Ns[0].Header().Rrtype != dns.TypeSOA {
		t.Errorf("authority section = %v, want one SOA", resp.Ns)
	}
	if got := h.upstream.queries("ads.test"); got != 0 {
		t.Errorf("a blocked name reached the upstream %d times", got)
	}
}

func blockedOverTCP(t *testing.T, factory backend.Factory) {
	h := start(t, factory, blockSetup)

	assertNullAnswer(t, h.ask("tcp", "ads.test", dns.TypeA), dns.TypeA, net.IPv4zero)
}

func blockedCaseAndSuffix(t *testing.T, factory backend.Factory) {
	h := start(t, factory, blockSetup)

	assertNullAnswer(t, h.ask("udp", "WwW.AdS.TeSt", dns.TypeA), dns.TypeA, net.IPv4zero)
	assertPassed(t, h.ask("udp", "notads.test", dns.TypeA), dns.TypeA, upstreamA)
	assertPassed(t, h.ask("udp", "test", dns.TypeA), dns.TypeA, upstreamA)
}

func passedQuery(t *testing.T, factory backend.Factory) {
	h := start(t, factory, blockSetup)

	assertPassed(t, h.ask("udp", "fine.example", dns.TypeA), dns.TypeA, upstreamA)
	assertPassed(t, h.ask("udp", "fine.example", dns.TypeAAAA), dns.TypeAAAA, upstreamAAAA)

	if got := h.upstream.queries("fine.example"); got != 2 {
		t.Errorf("upstream saw %d queries for fine.example, want 2 (one A, one AAAA)", got)
	}
	for _, e := range h.entries(2) {
		if e.Action != engine.ActionPass || e.RCode != "NOERROR" || e.Upstream == "" || e.Client.String() != "127.0.0.1" {
			t.Errorf("record = %+v, want a NOERROR pass from 127.0.0.1 with an upstream", e)
		}
	}
}

func allowBeatsListBlock(t *testing.T, factory backend.Factory) {
	h := start(t, factory, setup{blockList: []string{"ads.test"}, rules: "allow ok.ads.test\n"})

	assertPassed(t, h.ask("udp", "www.ok.ads.test", dns.TypeA), dns.TypeA, upstreamA)
	assertNullAnswer(t, h.ask("udp", "www.ads.test", dns.TypeA), dns.TypeA, net.IPv4zero)
}

func customBlockRule(t *testing.T, factory backend.Factory) {
	h := start(t, factory, setup{rules: "block redirect.test\n"})

	assertNullAnswer(t, h.ask("udp", "go.redirect.test", dns.TypeA), dns.TypeA, net.IPv4zero)

	e := h.entries(1)[0]
	if e.Action != engine.ActionBlock || e.Reason != engine.ReasonRule || e.Source != "block redirect.test" {
		t.Errorf("record = %+v, want a rule block naming %q", e, "block redirect.test")
	}
}

func listAllowBeatsListBlock(t *testing.T, factory backend.Factory) {
	h := start(t, factory, setup{blockList: []string{"ads.test"}, allowList: []string{"good.ads.test"}})

	assertPassed(t, h.ask("udp", "good.ads.test", dns.TypeA), dns.TypeA, upstreamA)
	assertNullAnswer(t, h.ask("udp", "bad.ads.test", dns.TypeA), dns.TypeA, net.IPv4zero)
}

func aclRefuses(t *testing.T, factory backend.Factory) {
	h := start(t, factory, setup{allowedClients: []string{"10.0.0.0/8"}, blockList: []string{"ads.test"}})

	for _, name := range []string{"fine.example", "ads.test"} {
		resp := h.ask("udp", name, dns.TypeA)
		if resp.Rcode != dns.RcodeRefused || len(resp.Answer) != 0 {
			t.Errorf("%s from a client outside allowed_clients: rcode %s with %d answers, want REFUSED", name, dns.RcodeToString[resp.Rcode], len(resp.Answer))
		}
	}
	if got := h.upstream.queries("fine.example"); got != 0 {
		t.Errorf("a refused client's query reached the upstream %d times", got)
	}
	for _, e := range h.entries(2) {
		if e.Action != engine.ActionBlock || e.Reason != engine.ReasonACL || e.RCode != "REFUSED" {
			t.Errorf("record = %+v, want an ACL refusal", e)
		}
	}
}

func logRecordPerQuery(t *testing.T, factory backend.Factory) {
	h := start(t, factory, blockSetup)

	h.ask("udp", "ads.test", dns.TypeA)
	h.ask("udp", "fine.example", dns.TypeA)
	h.ask("udp", "fine.example", dns.TypeA)

	got := h.entries(3)
	if got[0].Action != engine.ActionBlock || got[1].Action != engine.ActionPass || got[2].Action != engine.ActionPass {
		t.Fatalf("actions = %s, %s, %s; want block, pass, pass", got[0].Action, got[1].Action, got[2].Action)
	}
	if got[1].Cached {
		t.Error("the first lookup is marked as cached")
	}
	if !got[2].Cached {
		t.Error("the repeated lookup was not served from the backend's cache, or not marked so")
	}
	if n := h.upstream.queries("fine.example"); n != 1 {
		t.Errorf("upstream saw %d queries for a name asked twice, want 1 (the second is cached)", n)
	}
}

func assertNullAnswer(t *testing.T, resp *dns.Msg, qtype uint16, want net.IP) {
	t.Helper()
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Fatalf("%s for a blocked name: rcode %s with %d answers, want NOERROR and one null address", dns.TypeToString[qtype], dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	if got := answerIP(resp.Answer[0]); !got.Equal(want) {
		t.Errorf("%s answer = %v, want %v", dns.TypeToString[qtype], got, want)
	}
}

func assertPassed(t *testing.T, resp *dns.Msg, qtype uint16, want net.IP) {
	t.Helper()
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Fatalf("%s for an unblocked name: rcode %s with %d answers, want NOERROR and the upstream address", dns.TypeToString[qtype], dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	if got := answerIP(resp.Answer[0]); !got.Equal(want) {
		t.Errorf("%s answer = %v, want the upstream's %v", dns.TypeToString[qtype], got, want)
	}
}

func answerIP(rr dns.RR) net.IP {
	switch v := rr.(type) {
	case *dns.A:
		return v.A
	case *dns.AAAA:
		return v.AAAA
	}
	return nil
}
