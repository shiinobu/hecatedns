package native

import (
	"testing"

	"github.com/miekg/dns"
)

func TestIsLocalReverse(t *testing.T) {
	tests := []struct {
		name  string
		qname string
		qtype uint16
		want  bool
	}{
		{"private 192.168/16 PTR", "99.1.168.192.in-addr.arpa.", dns.TypePTR, true},
		{"private 10/8 PTR", "1.0.0.10.in-addr.arpa.", dns.TypePTR, true},
		{"private 172.16/12 PTR", "1.0.16.172.in-addr.arpa.", dns.TypePTR, true},
		{"subnet inside 172.16/12", "16.172.in-addr.arpa.", dns.TypePTR, true},
		{"loopback PTR", "1.0.0.127.in-addr.arpa.", dns.TypePTR, true},
		{"link local PTR", "1.0.254.169.in-addr.arpa.", dns.TypePTR, true},
		{"unique local IPv6 PTR", "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.d.f.ip6.arpa.", dns.TypePTR, true},
		{"SOA of a private zone", "1.168.192.in-addr.arpa.", dns.TypeSOA, true},
		{"NS of a private zone", "1.168.192.in-addr.arpa.", dns.TypeNS, true},
		{"mixed case", "99.1.168.192.IN-ADDR.ARPA.", dns.TypePTR, true},
		{"public PTR", "8.8.8.8.in-addr.arpa.", dns.TypePTR, false},
		{"172.32 is public", "1.0.32.172.in-addr.arpa.", dns.TypePTR, false},
		{"A query for a reverse name", "99.1.168.192.in-addr.arpa.", dns.TypeA, false},
		{"not a reverse name", "example.com.", dns.TypePTR, false},
		{"malformed reverse name", "x.y.in-addr.arpa.", dns.TypePTR, false},
		{"empty name", ".", dns.TypePTR, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLocalReverse(dns.Question{Name: tt.qname, Qtype: tt.qtype, Qclass: dns.ClassINET})
			if got != tt.want {
				t.Errorf("isLocalReverse(%s %s) = %v, want %v", tt.qname, dns.TypeToString[tt.qtype], got, tt.want)
			}
		})
	}
}

func TestLocalReverseReply(t *testing.T) {
	req := queryFor("99.1.168.192.in-addr.arpa.", dns.TypePTR)

	resp := localReverseReply(req)

	if resp.Rcode != dns.RcodeNameError || len(resp.Answer) != 0 {
		t.Fatalf("reply = %v, want NXDOMAIN without answers", resp)
	}
	if len(resp.Ns) != 1 {
		t.Fatalf("authority = %v, want one SOA so resolvers can cache the answer", resp.Ns)
	}
	soa, ok := resp.Ns[0].(*dns.SOA)
	if !ok || soa.Header().Name != "99.1.168.192.in-addr.arpa." || soa.Minttl != localReverseTTL {
		t.Errorf("authority = %v, want an SOA for the asked name with TTL %d", resp.Ns[0], localReverseTTL)
	}
}
