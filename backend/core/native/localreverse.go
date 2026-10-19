package native

import (
	"github.com/AdguardTeam/golibs/netutil"
	"github.com/miekg/dns"
)

const localReverseTTL = 300

func isLocalReverse(q dns.Question) bool {
	switch q.Qtype {
	case dns.TypePTR, dns.TypeSOA, dns.TypeNS:
	default:
		return false
	}
	pref, err := netutil.ExtractReversedAddr(q.Name)
	if err != nil {
		return false
	}
	return netutil.IsLocallyServed(pref.Addr())
}

func localReverseReply(req *dns.Msg) *dns.Msg {
	q := req.Question[0]
	resp := new(dns.Msg).SetRcode(req, dns.RcodeNameError)
	resp.Ns = []dns.RR{&dns.SOA{
		Hdr:     dns.RR_Header{Name: q.Name, Rrtype: dns.TypeSOA, Class: q.Qclass, Ttl: localReverseTTL},
		Ns:      "localhost.",
		Mbox:    "nobody.invalid.",
		Serial:  1,
		Refresh: 3600,
		Retry:   600,
		Expire:  86400,
		Minttl:  localReverseTTL,
	}}
	return resp
}
