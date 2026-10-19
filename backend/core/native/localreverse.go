package native

import (
	"github.com/AdguardTeam/golibs/netutil"
	"github.com/miekg/dns"
)

// localReverseTTL is how long a client may keep a locally made reverse answer.
const localReverseTTL = 300

// isLocalReverse reports whether q asks for the reverse name of an address that
// RFC 6303 says is served locally (10/8, 172.16/12, 192.168/16, 127/8, link
// local, unique local and the like). The dnsproxy backend answers these itself
// for PTR, SOA and NS, and so does native: a public resolver knows nothing about
// them, and asking would tell it which hosts are on the home network.
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

// localReverseReply answers req with NXDOMAIN and a synthetic SOA, so that
// resolvers can cache the answer (RFC 2308). req must have one question.
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
