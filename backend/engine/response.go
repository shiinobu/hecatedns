package engine

import (
	"fmt"
	"math"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

const (
	ednsUDPSize = 1232

	syntheticNS     = "ns.hecate.invalid."
	syntheticMbox   = "hostmaster.hecate.invalid."
	syntheticSerial = 1
	soaRefresh      = 3600
	soaRetry        = 600
	soaExpire       = 86400
)

type Responder struct {
	mode string
	ttl  uint32
}

func NewResponder(mode string, ttl time.Duration) (Responder, error) {
	switch mode {
	case config.ModeNullIP, config.ModeNXDomain, config.ModeNoData, config.ModeRefused:
		return Responder{mode: mode, ttl: ttlSeconds(ttl)}, nil
	}
	return Responder{}, fmt.Errorf("block mode %q is not %s, %s, %s or %s",
		mode, config.ModeNullIP, config.ModeNXDomain, config.ModeNoData, config.ModeRefused)
}

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
		return refused(req)
	}
	return nullIP(req, r.ttl)
}

func (r Responder) NXDomain(req *dns.Msg) *dns.Msg {
	if req == nil || len(req.Question) != 1 {
		return formErr(req)
	}
	return negative(req, dns.RcodeNameError, r.ttl)
}

func (r Responder) NoData(req *dns.Msg) *dns.Msg {
	if req == nil || len(req.Question) != 1 {
		return formErr(req)
	}
	return negative(req, dns.RcodeSuccess, r.ttl)
}

func Refuse(req *dns.Msg) *dns.Msg {
	if req == nil {
		return formErr(nil)
	}
	resp := refused(req)
	resp.RecursionAvailable = false
	return resp
}

func refused(req *dns.Msg) *dns.Msg {
	resp := reply(req)
	resp.Rcode = dns.RcodeRefused
	return resp
}

func notImplemented(req *dns.Msg) *dns.Msg {
	resp := reply(req)
	resp.Rcode = dns.RcodeNotImplemented
	return resp
}

func reply(req *dns.Msg) *dns.Msg {
	resp := new(dns.Msg).SetReply(req)
	resp.RecursionAvailable = true

	resp.Compress = true
	if opt := req.IsEdns0(); opt != nil {
		resp.SetEdns0(ednsUDPSize, opt.Do())
	}
	return resp
}

func formErr(req *dns.Msg) *dns.Msg {
	if req == nil {
		return &dns.Msg{MsgHdr: dns.MsgHdr{Response: true, RecursionAvailable: true, Rcode: dns.RcodeFormatError}}
	}
	resp := reply(req)
	resp.Rcode = dns.RcodeFormatError
	return resp
}

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
