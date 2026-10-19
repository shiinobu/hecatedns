package engine

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func buildCNAMEResp(q *dns.Msg, rcode int, targets ...string) *dns.Msg {
	resp := new(dns.Msg).SetRcode(q, rcode)
	owner := q.Question[0].Name
	for _, t := range targets {
		fqdn := dns.Fqdn(t)
		resp.Answer = append(resp.Answer, &dns.CNAME{
			Hdr:    dns.RR_Header{Name: owner, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 120},
			Target: fqdn,
		})
		owner = fqdn
	}
	if rcode == dns.RcodeSuccess && q.Question[0].Qtype == dns.TypeA {
		resp.Answer = append(resp.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: owner, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 120},
			A:   netip.MustParseAddr("192.0.2.9").AsSlice(),
		})
	}
	return resp
}

func TestAfter_CNAMEChain(t *testing.T) {
	tests := []struct {
		name       string
		qtype      uint16
		rcode      int
		targets    []string
		wantBlock  bool
		wantSource string
		wantVia    string
	}{
		{
			name:      "no CNAME in answer passes",
			qtype:     dns.TypeA,
			rcode:     dns.RcodeSuccess,
			targets:   nil,
			wantBlock: false,
		},
		{
			name:      "unblocked CNAME chain passes",
			qtype:     dns.TypeA,
			rcode:     dns.RcodeSuccess,
			targets:   []string{"cdn1.nothing.example", "cdn2.nothing.example"},
			wantBlock: false,
		},
		{
			name:       "single CNAME to blocklist domain is blocked",
			qtype:      dns.TypeA,
			rcode:      dns.RcodeSuccess,
			targets:    []string{"CNAME-Cloak.Ads.Example."},
			wantBlock:  true,
			wantSource: "ads",
			wantVia:    "cname-cloak.ads.example",
		},
		{
			name:       "single CNAME to custom block rule is blocked",
			qtype:      dns.TypeAAAA,
			rcode:      dns.RcodeSuccess,
			targets:    []string{"sub.bad.example"},
			wantBlock:  true,
			wantSource: "block bad.example",
			wantVia:    "sub.bad.example",
		},
		{
			name:      "CNAME target excluded by custom allow rule continues and passes",
			qtype:     dns.TypeA,
			rcode:     dns.RcodeSuccess,
			targets:   []string{"sub.ok.bad.example", "final.nothing.example"},
			wantBlock: false,
		},
		{
			name:      "CNAME target excluded by allow list continues and passes",
			qtype:     dns.TypeA,
			rcode:     dns.RcodeSuccess,
			targets:   []string{"sub.listallow.example", "final.nothing.example"},
			wantBlock: false,
		},
		{
			name:       "long CNAME chain with allowed hop then blocked hop blocks on the blocked hop",
			qtype:      dns.TypeA,
			rcode:      dns.RcodeSuccess,
			targets:    []string{"hop1.nothing.example", "ok.bad.example", "hop3.nothing.example", "deep.trk.example", "final.nothing.example"},
			wantBlock:  true,
			wantSource: "trackers",
			wantVia:    "deep.trk.example",
		},
		{
			name:       "HTTPS query with blocked CNAME target answers NODATA",
			qtype:      dns.TypeHTTPS,
			rcode:      dns.RcodeSuccess,
			targets:    []string{"tracker.ads.example"},
			wantBlock:  true,
			wantSource: "ads",
			wantVia:    "tracker.ads.example",
		},
		{
			name:      "non-NOERROR response with blocked CNAME is not rewritten",
			qtype:     dns.TypeA,
			rcode:     dns.RcodeNameError,
			targets:   []string{"tracker.ads.example"},
			wantBlock: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &memRecorder{}
			e := newFixtureEngine(t, rec, nil)
			q := newQuery("firstparty.example", tt.qtype, "127.0.0.1")
			resp := buildCNAMEResp(q.Req, tt.rcode, tt.targets...)
			meta := Meta{Upstream: "https://1.1.1.1/dns-query", Latency: 12 * time.Millisecond}

			dec := e.After(context.Background(), q, resp, meta)
			entries := rec.all()
			if len(entries) != 1 {
				t.Fatalf("records = %d, want 1", len(entries))
			}

			if !tt.wantBlock {
				if dec.Verdict != Pass || dec.Resp != nil {
					t.Fatalf("After = %+v, want Pass with nil Resp", dec)
				}
				if entries[0].Action != ActionPass {
					t.Errorf("record action = %q, want pass", entries[0].Action)
				}
				return
			}

			if dec.Verdict != Block || dec.Reason != ReasonCNAME || dec.Source != tt.wantSource || dec.Via != tt.wantVia {
				t.Fatalf("After = %+v, want Block reason=%q source=%q via=%q", dec, ReasonCNAME, tt.wantSource, tt.wantVia)
			}
			if dec.Resp == nil || dec.Resp.Question[0].Name != "firstparty.example." {
				t.Fatalf("Resp question = %v, want original question firstparty.example.", dec.Resp)
			}
			switch tt.qtype {
			case dns.TypeA:
				if len(dec.Resp.Answer) != 1 || dec.Resp.Answer[0].(*dns.A).A.String() != "0.0.0.0" {
					t.Errorf("A answer = %v, want 0.0.0.0", dec.Resp.Answer)
				}
			case dns.TypeAAAA:
				if len(dec.Resp.Answer) != 1 || dec.Resp.Answer[0].(*dns.AAAA).AAAA.String() != "::" {
					t.Errorf("AAAA answer = %v, want ::", dec.Resp.Answer)
				}
			case dns.TypeHTTPS:
				if dec.Resp.Rcode != dns.RcodeSuccess || len(dec.Resp.Answer) != 0 || len(dec.Resp.Ns) != 1 {
					t.Errorf("HTTPS answer = rcode %d ans %d ns %d, want NODATA with SOA", dec.Resp.Rcode, len(dec.Resp.Answer), len(dec.Resp.Ns))
				}
			}
			got := entries[0]
			if got.Action != ActionBlock || got.Reason != ReasonCNAME || got.Source != tt.wantSource || got.Via != tt.wantVia || got.QName != "firstparty.example" {
				t.Errorf("record = %+v, want block/cname/%s via %s for firstparty.example", got, tt.wantSource, tt.wantVia)
			}
		})
	}
}

func TestAfter_MalformedInputsDoNotPanic(t *testing.T) {
	rec := &memRecorder{}
	e := newFixtureEngine(t, rec, nil)

	badQ := Query{Req: new(dns.Msg), Client: netip.MustParseAddr("127.0.0.1"), Proto: "udp"}
	resp := &dns.Msg{
		MsgHdr: dns.MsgHdr{Rcode: dns.RcodeSuccess},
		Answer: []dns.RR{
			nil,
			&dns.CNAME{Target: "."},
			&dns.CNAME{Target: "ads.example."},
		},
	}
	dec := e.After(context.Background(), badQ, resp, Meta{})
	if dec.Verdict != Pass {
		t.Fatalf("After with malformed request = %+v, want Pass", dec)
	}

	goodQ := newQuery("site.example", dns.TypeA, "127.0.0.1")
	dec = e.After(context.Background(), goodQ, resp, Meta{})
	if dec.Verdict != Block || dec.Via != "ads.example" {
		t.Fatalf("After with nil RR before blocked CNAME = %+v, want Block via ads.example", dec)
	}
}
