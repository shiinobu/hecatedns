package engine

import (
	"fmt"
	"math"
	"time"

	"github.com/miekg/dns"

	"xdns/internal/config"
)

const (
	// ednsUDPSize is the UDP payload size advertised when a request carried an
	// OPT record (the value recommended since the 2020 DNS flag day).
	ednsUDPSize = 1232

	// Synthetic SOA fields. The names are under .invalid (RFC 2606), so they
	// can never point at a real server.
	syntheticNS     = "ns.xdns.invalid."
	syntheticMbox   = "hostmaster.xdns.invalid."
	syntheticSerial = 1
	soaRefresh      = 3600
	soaRetry        = 600
	soaExpire       = 86400
)

// Responder builds the answers for blocked queries according to one block
// mode. It is immutable and safe for concurrent use. The zero value behaves
// like config.ModeNullIP with a zero TTL.
type Responder struct {
	mode string
	ttl  uint32
}

// NewResponder returns a Responder for mode (one of the config.Mode*
// constants). ttl is used for the answer records and, as minimum, for the
// synthetic SOA, so clients cache a block only briefly. It is truncated to
// whole seconds and clamped to the range of a DNS TTL.
func NewResponder(mode string, ttl time.Duration) (Responder, error) {
	switch mode {
	case config.ModeNullIP, config.ModeNXDomain, config.ModeNoData, config.ModeRefused:
		return Responder{mode: mode, ttl: ttlSeconds(ttl)}, nil
	}
	return Responder{}, fmt.Errorf("block mode %q is not %s, %s, %s or %s",
		mode, config.ModeNullIP, config.ModeNXDomain, config.ModeNoData, config.ModeRefused)
}

// Block returns the answer for a blocked req. It copies the ID, the question
// and the RD and CD bits, sets RA, mirrors an OPT record when the request had
// one, and never touches req. A request without exactly one question gets
// FORMERR, so the response is always well formed.
func (r Responder) Block(req *dns.Msg) *dns.Msg {
	if req == nil || len(req.Question) != 1 {
		return formErr(req)
	}
	switch r.mode {
	case config.ModeNXDomain:
		return negative(req, dns.RcodeNameError, r.ttl)
	case config.ModeNoData:
		return negative(req, dns.RcodeSuccess, r.ttl)
	case config.ModeRefused:
		return Refuse(req)
	}
	return nullIP(req, r.ttl)
}

// Refuse returns an empty REFUSED answer. It is used for clients outside
// allowed_clients, whatever the block mode is.
func Refuse(req *dns.Msg) *dns.Msg {
	if req == nil {
		return formErr(nil)
	}
	resp := reply(req)
	resp.Rcode = dns.RcodeRefused
	return resp
}

// reply starts a response to req: header bits, question and, if the request
// had an OPT record, an OPT record of our own. req must not be nil.
func reply(req *dns.Msg) *dns.Msg {
	resp := new(dns.Msg).SetReply(req)
	resp.RecursionAvailable = true
	if opt := req.IsEdns0(); opt != nil {
		resp.SetEdns0(ednsUDPSize, opt.Do())
	}
	return resp
}

// formErr returns a FORMERR answer. req may be nil.
func formErr(req *dns.Msg) *dns.Msg {
	if req == nil {
		return &dns.Msg{MsgHdr: dns.MsgHdr{Response: true, RecursionAvailable: true, Rcode: dns.RcodeFormatError}}
	}
	resp := reply(req)
	resp.Rcode = dns.RcodeFormatError
	return resp
}

// nullIP answers A with 0.0.0.0 and AAAA with ::, and every other type with
// NODATA, which is also what makes HTTPS/SVCB lookups of a blocked name empty.
func nullIP(req *dns.Msg, ttl uint32) *dns.Msg {
	q := req.Question[0]
	hdr := dns.RR_Header{Name: q.Name, Rrtype: q.Qtype, Class: q.Qclass, Ttl: ttl}
	switch q.Qtype {
	case dns.TypeA:
		resp := reply(req)
		resp.Answer = []dns.RR{&dns.A{Hdr: hdr, A: make([]byte, 4)}}
		return resp
	case dns.TypeAAAA:
		resp := reply(req)
		resp.Answer = []dns.RR{&dns.AAAA{Hdr: hdr, AAAA: make([]byte, 16)}}
		return resp
	}
	return negative(req, dns.RcodeSuccess, ttl)
}

// negative returns an answer without records and with the given rcode, plus a
// synthetic SOA so resolvers can cache it for ttl seconds (RFC 2308).
func negative(req *dns.Msg, rcode int, ttl uint32) *dns.Msg {
	q := req.Question[0]
	resp := reply(req)
	resp.Rcode = rcode
	resp.Ns = []dns.RR{&dns.SOA{
		Hdr:     dns.RR_Header{Name: q.Name, Rrtype: dns.TypeSOA, Class: q.Qclass, Ttl: ttl},
		Ns:      syntheticNS,
		Mbox:    syntheticMbox,
		Serial:  syntheticSerial,
		Refresh: soaRefresh,
		Retry:   soaRetry,
		Expire:  soaExpire,
		Minttl:  ttl,
	}}
	return resp
}

// ttlSeconds converts d to whole seconds, clamped to 0..MaxUint32.
func ttlSeconds(d time.Duration) uint32 {
	switch seconds := d / time.Second; {
	case seconds <= 0:
		return 0
	case seconds > math.MaxUint32:
		return math.MaxUint32
	default:
		return uint32(seconds)
	}
}
