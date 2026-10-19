package engine

// agy wrote this

import (
	"context"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

func TestBeforeSpecialNames(t *testing.T) {
	tests := []struct {
		name       string
		special    config.SpecialNames
		blockMode  string
		rules      string
		qname      string
		qtype      uint16
		client     string
		wantVerd   Verdict
		wantReason string
		wantSource string
		wantRcode  int
		wantSOA    bool
	}{
		{
			name:       "firefox canary enabled answers NXDOMAIN even in null_ip mode and before custom allow",
			special:    config.SpecialNames{FirefoxCanary: true, DDR: true},
			blockMode:  config.ModeNullIP,
			rules:      "allow use-application-dns.net\n",
			qname:      "use-application-dns.net",
			qtype:      dns.TypeA,
			client:     "127.0.0.1",
			wantVerd:   Block,
			wantReason: ReasonSpecial,
			wantSource: SourceFirefoxCanary,
			wantRcode:  dns.RcodeNameError,
			wantSOA:    true,
		},
		{
			name:       "firefox canary subdomain and uppercase normalized",
			special:    config.SpecialNames{FirefoxCanary: true},
			blockMode:  config.ModeRefused,
			qname:      "Canary.Use-Application-DNS.Net.",
			qtype:      dns.TypeAAAA,
			client:     "127.0.0.1",
			wantVerd:   Block,
			wantReason: ReasonSpecial,
			wantSource: SourceFirefoxCanary,
			wantRcode:  dns.RcodeNameError,
			wantSOA:    true,
		},
		{
			name:      "firefox canary disabled passes",
			special:   config.SpecialNames{FirefoxCanary: false, DDR: true},
			blockMode: config.ModeNullIP,
			qname:     "use-application-dns.net",
			qtype:     dns.TypeA,
			client:    "127.0.0.1",
			wantVerd:  Pass,
		},
		{
			name:       "ddr enabled answers NODATA even in nxdomain mode and before custom allow",
			special:    config.SpecialNames{FirefoxCanary: true, DDR: true},
			blockMode:  config.ModeNXDomain,
			rules:      "allow _dns.resolver.arpa\n",
			qname:      "_dns.resolver.arpa",
			qtype:      dns.TypeSVCB,
			client:     "127.0.0.1",
			wantVerd:   Block,
			wantReason: ReasonSpecial,
			wantSource: SourceDDR,
			wantRcode:  dns.RcodeSuccess,
			wantSOA:    true,
		},
		{
			name:       "ddr subdomain answers NODATA",
			special:    config.SpecialNames{DDR: true},
			blockMode:  config.ModeNullIP,
			qname:      "sub._dns.resolver.arpa.",
			qtype:      dns.TypeA,
			client:     "127.0.0.1",
			wantVerd:   Block,
			wantReason: ReasonSpecial,
			wantSource: SourceDDR,
			wantRcode:  dns.RcodeSuccess,
			wantSOA:    true,
		},
		{
			name:      "ddr disabled passes",
			special:   config.SpecialNames{FirefoxCanary: true, DDR: false},
			blockMode: config.ModeNullIP,
			qname:     "_dns.resolver.arpa",
			qtype:     dns.TypeSVCB,
			client:    "127.0.0.1",
			wantVerd:  Pass,
		},
		{
			name:      "non-boundary lookalike is not treated as special",
			special:   config.SpecialNames{FirefoxCanary: true, DDR: true},
			blockMode: config.ModeNullIP,
			qname:     "not-use-application-dns.net",
			qtype:     dns.TypeA,
			client:    "127.0.0.1",
			wantVerd:  Pass,
		},
		{
			name:       "ACL refusal still runs before special names",
			special:    config.SpecialNames{FirefoxCanary: true, DDR: true},
			blockMode:  config.ModeNullIP,
			qname:      "use-application-dns.net",
			qtype:      dns.TypeA,
			client:     "192.0.2.99",
			wantVerd:   Block,
			wantReason: ReasonACL,
			wantSource: "",
			wantRcode:  dns.RcodeRefused,
			wantSOA:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &memRecorder{}
			rules, _ := parseRulesText(t, tt.rules)
			e, err := New(Options{
				ACL:       mustACL(t, "127.0.0.0/8", "::1/128"),
				Rules:     staticRules{set: rules},
				Responder: mustResponder(t, tt.blockMode, 10*time.Second),
				Special:   tt.special,
				Recorder:  rec,
				Now:       func() time.Time { return fixedNow },
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			q := newQuery(tt.qname, tt.qtype, tt.client)
			dec := e.Before(context.Background(), q)

			if dec.Verdict != tt.wantVerd || dec.Reason != tt.wantReason || dec.Source != tt.wantSource {
				t.Fatalf("Before = {%v %q %q}, want {%v %q %q}",
					dec.Verdict, dec.Reason, dec.Source, tt.wantVerd, tt.wantReason, tt.wantSource)
			}
			if tt.wantVerd == Pass {
				if dec.Resp != nil {
					t.Fatalf("Pass decision carried Resp: %v", dec.Resp)
				}
				return
			}
			if dec.Resp == nil {
				t.Fatal("Block decision missing Resp")
			}
			if dec.Resp.Rcode != tt.wantRcode {
				t.Fatalf("Resp.Rcode = %s, want %s", dns.RcodeToString[dec.Resp.Rcode], dns.RcodeToString[tt.wantRcode])
			}
			if len(dec.Resp.Answer) != 0 {
				t.Fatalf("Resp.Answer = %v, want empty answer", dec.Resp.Answer)
			}
			if tt.wantSOA {
				if len(dec.Resp.Ns) != 1 || dec.Resp.Ns[0].Header().Rrtype != dns.TypeSOA {
					t.Fatalf("Resp.Ns = %v, want 1 synthetic SOA", dec.Resp.Ns)
				}
			}
			entries := rec.all()
			if len(entries) != 1 || entries[0].Action != ActionBlock || entries[0].Reason != tt.wantReason || entries[0].Source != tt.wantSource {
				t.Fatalf("entries = %+v, want 1 block record with reason=%q source=%q", entries, tt.wantReason, tt.wantSource)
			}
		})
	}
}

func TestResponderNXDomainAndNoDataMalformed(t *testing.T) {
	r := mustResponder(t, config.ModeNullIP, 10*time.Second)
	for _, fn := range []func(*dns.Msg) *dns.Msg{r.NXDomain, r.NoData} {
		if resp := fn(nil); resp.Rcode != dns.RcodeFormatError {
			t.Fatalf("nil req: rcode = %d, want FORMERR", resp.Rcode)
		}
		if resp := fn(&dns.Msg{}); resp.Rcode != dns.RcodeFormatError {
			t.Fatalf("empty req: rcode = %d, want FORMERR", resp.Rcode)
		}
	}
}
