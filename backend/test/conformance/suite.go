// Package conformance holds the test suite every backend must pass: the same
// questions asked of each implementation, against a fake upstream on loopback,
// so a block, an allow or a refusal looks identical whichever backend serves it.
package conformance

import (
	"net"
	"testing"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/core"
	"github.com/shiinobu/hecatedns/backend/engine"
)

// Constructor names a backend factory under test.
type Constructor struct {
	Name    string
	Factory core.Factory
}

type testCase struct {
	name string
	run  func(t *testing.T, factory core.Factory)
}

// Run executes the whole suite once per constructor, as subtests. A backend
// factory must produce a backend that implements core.Bound, because the
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
	{"blocked HTTPS and SVCB answer NODATA with an SOA (direct and via CNAME)", blockedHTTPS},
	{"blocked name over TCP", blockedOverTCP},
	{"blocking ignores case and covers subdomains only", blockedCaseAndSuffix},
	{"passed query returns the upstream answer", passedQuery},
	{"custom allow beats a list block", allowBeatsListBlock},
	{"custom block rule blocks and names its rule", customBlockRule},
	{"list allow beats list block", listAllowBeatsListBlock},
	{"client outside allowed_clients is refused", aclRefuses},
	{"one log record per query, cache hits included", logRecordPerQuery},
	{"opcode other than QUERY is answered NOTIMP, never forwarded", nonQueryOpcode},
	{"CNAME to a blocked domain is blocked on both upstream and cache hit", blockedCNAMEAndCacheHit},
	{"special names answer NXDOMAIN for canary and NODATA for DDR", specialNames}, // agy wrote this
	{"an oversized or malformed UDP datagram does not stop the server", oversizedDatagram},
	{"reverse lookups of private addresses are answered NXDOMAIN and never leave the machine", privateReverse},
}

var blockSetup = setup{blockList: []string{"ads.test"}}

func blockedA(t *testing.T, factory core.Factory) {
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

func blockedAAAA(t *testing.T, factory core.Factory) {
	h := start(t, factory, blockSetup)

	assertNullAnswer(t, h.ask("udp", "ads.test", dns.TypeAAAA), dns.TypeAAAA, net.IPv6zero)
}

func blockedHTTPS(t *testing.T, factory core.Factory) {
	// agy wrote this
	h := start(t, factory, setup{
		blockList: []string{"ads.test"},
		cnames: map[string][]string{
			"cloaked.example": {"tracker.ads.test"},
		},
	})

	for _, qtype := range []uint16{dns.TypeHTTPS, dns.TypeSVCB} {
		for _, name := range []string{"ads.test", "cloaked.example"} {
			resp := h.ask("udp", name, qtype)
			if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 0 {
				t.Errorf("%s for %s: rcode %s with %d answers, want NOERROR and none",
					dns.TypeToString[qtype], name, dns.RcodeToString[resp.Rcode], len(resp.Answer))
			}
			if len(resp.Ns) != 1 || resp.Ns[0].Header().Rrtype != dns.TypeSOA {
				t.Errorf("%s for %s: authority section = %v, want one SOA",
					dns.TypeToString[qtype], name, resp.Ns)
			}
		}
	}
	if got := h.upstream.queries("ads.test"); got != 0 {
		t.Errorf("a blocked name reached the upstream %d times", got)
	}
	if got := h.upstream.queries("cloaked.example"); got != 2 {
		t.Errorf("cloaked.example reached the upstream %d times, want 2 (one HTTPS, one SVCB)", got)
	}
}

func blockedOverTCP(t *testing.T, factory core.Factory) {
	h := start(t, factory, blockSetup)

	assertNullAnswer(t, h.ask("tcp", "ads.test", dns.TypeA), dns.TypeA, net.IPv4zero)
}

func blockedCaseAndSuffix(t *testing.T, factory core.Factory) {
	h := start(t, factory, blockSetup)

	assertNullAnswer(t, h.ask("udp", "WwW.AdS.TeSt", dns.TypeA), dns.TypeA, net.IPv4zero)
	assertPassed(t, h.ask("udp", "notads.test", dns.TypeA), dns.TypeA, upstreamA)
	assertPassed(t, h.ask("udp", "test", dns.TypeA), dns.TypeA, upstreamA)
}

func passedQuery(t *testing.T, factory core.Factory) {
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

func allowBeatsListBlock(t *testing.T, factory core.Factory) {
	h := start(t, factory, setup{blockList: []string{"ads.test"}, rules: "allow ok.ads.test\n"})

	assertPassed(t, h.ask("udp", "www.ok.ads.test", dns.TypeA), dns.TypeA, upstreamA)
	assertNullAnswer(t, h.ask("udp", "www.ads.test", dns.TypeA), dns.TypeA, net.IPv4zero)
}

func customBlockRule(t *testing.T, factory core.Factory) {
	h := start(t, factory, setup{rules: "block redirect.test\n"})

	assertNullAnswer(t, h.ask("udp", "go.redirect.test", dns.TypeA), dns.TypeA, net.IPv4zero)

	e := h.entries(1)[0]
	if e.Action != engine.ActionBlock || e.Reason != engine.ReasonRule || e.Source != "block redirect.test" {
		t.Errorf("record = %+v, want a rule block naming %q", e, "block redirect.test")
	}
}

func listAllowBeatsListBlock(t *testing.T, factory core.Factory) {
	h := start(t, factory, setup{blockList: []string{"ads.test"}, allowList: []string{"good.ads.test"}})

	assertPassed(t, h.ask("udp", "good.ads.test", dns.TypeA), dns.TypeA, upstreamA)
	assertNullAnswer(t, h.ask("udp", "bad.ads.test", dns.TypeA), dns.TypeA, net.IPv4zero)
}

func aclRefuses(t *testing.T, factory core.Factory) {
	h := start(t, factory, setup{allowedClients: []string{"10.0.0.0/8"}, blockList: []string{"ads.test"}})

	for _, name := range []string{"fine.example", "ads.test"} {
		resp := h.ask("udp", name, dns.TypeA)
		if resp.Rcode != dns.RcodeRefused || len(resp.Answer) != 0 {
			t.Errorf("%s from a client outside allowed_clients: rcode %s with %d answers, want REFUSED", name, dns.RcodeToString[resp.Rcode], len(resp.Answer))
		}
		if resp.RecursionAvailable {
			t.Errorf("%s from a client outside allowed_clients: RA is set, but such a client may not use recursion", name)
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

func logRecordPerQuery(t *testing.T, factory core.Factory) {
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

func nonQueryOpcode(t *testing.T, factory core.Factory) {
	h := start(t, factory, setup{rules: "block redirect.test\n"})

	for _, opcode := range []int{dns.OpcodeStatus, dns.OpcodeNotify, dns.OpcodeUpdate} {
		for _, name := range []string{"redirect.test", "fine.example"} {
			resp := h.askOpcode(opcode, name, dns.TypeA)
			if resp.Rcode != dns.RcodeNotImplemented || len(resp.Answer) != 0 {
				t.Errorf("opcode %d for %s: rcode %s with %d answers, want an empty NOTIMP", opcode, name, dns.RcodeToString[resp.Rcode], len(resp.Answer))
			}
		}
	}
	for _, name := range []string{"redirect.test", "fine.example"} {
		if got := h.upstream.queries(name); got != 0 {
			t.Errorf("a non-QUERY message for %s reached the upstream %d times, skipping every rule", name, got)
		}
	}
	for _, e := range h.entries(6) {
		if e.Action != engine.ActionBlock || e.Reason != engine.ReasonInvalid || e.RCode != "NOTIMP" {
			t.Errorf("record = %+v, want a block record with reason invalid and rcode NOTIMP", e)
		}
	}
}

// agy wrote this
func blockedCNAMEAndCacheHit(t *testing.T, factory core.Factory) {
	h := start(t, factory, setup{
		blockList: []string{"ads.test"},
		allowList: []string{"allowed.ads.test"},
		cnames: map[string][]string{
			"cloaked.example": {"hop.example", "tracker.ads.test"},
			"allowed.example": {"allowed.ads.test"},
		},
	})

	// First lookup: resolved upstream, blocked in After via CNAME.
	assertNullAnswer(t, h.ask("udp", "cloaked.example", dns.TypeA), dns.TypeA, net.IPv4zero)
	// Second lookup: served from backend cache (upstream still queried only once), still blocked in After.
	assertNullAnswer(t, h.ask("udp", "cloaked.example", dns.TypeA), dns.TypeA, net.IPv4zero)

	if got := h.upstream.queries("cloaked.example"); got != 1 {
		t.Errorf("upstream queries for cloaked.example = %d, want 1 (second should come from cache)", got)
	}

	// Allowed CNAME target passes.
	assertPassed(t, h.ask("udp", "allowed.example", dns.TypeA), dns.TypeA, upstreamA)

	entries := h.entries(3)
	for i := 0; i < 2; i++ {
		e := entries[i]
		if e.Action != engine.ActionBlock || e.Reason != engine.ReasonCNAME || e.Source != "test-block" || e.Via != "tracker.ads.test" {
			t.Errorf("entry[%d] = %+v, want block/cname/test-block via tracker.ads.test", i, e)
		}
	}
	if entries[2].Action != engine.ActionPass {
		t.Errorf("entry[2] = %+v, want pass for allowed CNAME target", entries[2])
	}
}

// agy wrote this
func specialNames(t *testing.T, factory core.Factory) {
	h := start(t, factory, blockSetup)

	canary := h.ask("udp", "use-application-dns.net", dns.TypeA)
	if canary.Rcode != dns.RcodeNameError || len(canary.Answer) != 0 || len(canary.Ns) != 1 || canary.Ns[0].Header().Rrtype != dns.TypeSOA {
		t.Errorf("use-application-dns.net: rcode=%s answers=%d ns=%v, want NXDOMAIN + 1 SOA",
			dns.RcodeToString[canary.Rcode], len(canary.Answer), canary.Ns)
	}
	if got := h.upstream.queries("use-application-dns.net"); got != 0 {
		t.Errorf("use-application-dns.net reached the upstream %d times", got)
	}

	ddr := h.ask("udp", "_dns.resolver.arpa", dns.TypeSVCB)
	if ddr.Rcode != dns.RcodeSuccess || len(ddr.Answer) != 0 || len(ddr.Ns) != 1 || ddr.Ns[0].Header().Rrtype != dns.TypeSOA {
		t.Errorf("_dns.resolver.arpa: rcode=%s answers=%d ns=%v, want NOERROR (NODATA) + 1 SOA",
			dns.RcodeToString[ddr.Rcode], len(ddr.Answer), ddr.Ns)
	}
	if got := h.upstream.queries("_dns.resolver.arpa"); got != 0 {
		t.Errorf("_dns.resolver.arpa reached the upstream %d times", got)
	}

	entries := h.entries(2)
	if entries[0].Action != engine.ActionBlock || entries[0].Reason != engine.ReasonSpecial || entries[0].Source != engine.SourceFirefoxCanary || entries[0].RCode != "NXDOMAIN" {
		t.Errorf("entry[0] = %+v, want block/special/firefox_canary NXDOMAIN", entries[0])
	}
	if entries[1].Action != engine.ActionBlock || entries[1].Reason != engine.ReasonSpecial || entries[1].Source != engine.SourceDDR || entries[1].RCode != "NOERROR" {
		t.Errorf("entry[1] = %+v, want block/special/ddr NOERROR", entries[1])
	}
}

// oversizedDatagram sends datagrams a UDP server must survive: one far larger
// than any DNS query (on Windows the failed read of such a datagram once stopped
// a server for good), one too short to hold a header, and one that is not DNS.
func oversizedDatagram(t *testing.T, factory core.Factory) {
	h := start(t, factory, blockSetup)

	conn, err := net.Dial("udp", h.bound.Addr(core.NetworkUDP).String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	for _, junk := range [][]byte{make([]byte, 9000), {1, 2, 3}, []byte("GET / HTTP/1.1\r\n\r\n")} {
		if _, err := conn.Write(junk); err != nil {
			t.Fatalf("write %d bytes: %v", len(junk), err)
		}
	}

	assertPassed(t, h.ask("udp", "fine.example", dns.TypeA), dns.TypeA, upstreamA)
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
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) == 0 {
		t.Fatalf("%s for an unblocked name: rcode %s with %d answers, want NOERROR and the upstream address", dns.TypeToString[qtype], dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	if got := answerIP(resp.Answer[len(resp.Answer)-1]); !got.Equal(want) {
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

// privateReverse: a PTR for a private address (RFC 6303) says nothing a public
// resolver could know, and asking it would tell Cloudflare or Google which
// hosts are on the home network. Public addresses still go upstream.
func privateReverse(t *testing.T, factory core.Factory) {
	h := start(t, factory, setup{})

	for _, name := range []string{"99.1.168.192.in-addr.arpa", "1.0.0.10.in-addr.arpa", "9.16.172.in-addr.arpa", "1.0.0.127.in-addr.arpa"} {
		resp := h.ask("udp", name, dns.TypePTR)
		if resp.Rcode != dns.RcodeNameError || len(resp.Answer) != 0 {
			t.Errorf("PTR %s: rcode %s with %d answers, want NXDOMAIN", name, dns.RcodeToString[resp.Rcode], len(resp.Answer))
		}
		if got := h.upstream.queries(name); got != 0 {
			t.Errorf("PTR %s reached the upstream %d times", name, got)
		}
	}
	h.ask("udp", "8.8.8.8.in-addr.arpa", dns.TypePTR)
	if got := h.upstream.queries("8.8.8.8.in-addr.arpa"); got != 1 {
		t.Errorf("PTR for a public address reached the upstream %d times, want 1", got)
	}
	for _, e := range h.entries(5)[:4] {
		if e.Action != engine.ActionPass || e.RCode != "NXDOMAIN" || e.Upstream != "" {
			t.Errorf("record = %+v, want a pass answered locally with NXDOMAIN", e)
		}
	}
}
