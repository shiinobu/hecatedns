package engine

import (
	"math"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"

	"xdns/internal/config"
)

const testBlockTTL = 10 * time.Second

func newRequest(name string, qtype uint16) *dns.Msg {
	req := new(dns.Msg)
	req.SetQuestion(dns.Fqdn(name), qtype)
	req.Id = 0x1234
	return req
}

func mustResponder(t *testing.T, mode string, ttl time.Duration) Responder {
	t.Helper()
	r, err := NewResponder(mode, ttl)
	if err != nil {
		t.Fatalf("NewResponder(%q): %v", mode, err)
	}
	return r
}

func TestNewResponder_RejectsUnknownMode(t *testing.T) {
	for _, mode := range []string{"", "NULL_IP", "sinkhole", "null-ip"} {
		if _, err := NewResponder(mode, testBlockTTL); err == nil {
			t.Errorf("NewResponder(%q) accepted an unknown mode", mode)
		}
	}
}

func TestResponder_Block(t *testing.T) {
	const (
		typeHTTPS = dns.TypeHTTPS
	)
	tests := []struct {
		name      string
		mode      string
		qtype     uint16
		wantRcode int
		wantIP    string
		wantSOA   bool
	}{
		{"null_ip A", config.ModeNullIP, dns.TypeA, dns.RcodeSuccess, "0.0.0.0", false},
		{"null_ip AAAA", config.ModeNullIP, dns.TypeAAAA, dns.RcodeSuccess, "::", false},
		{"null_ip HTTPS is NODATA", config.ModeNullIP, typeHTTPS, dns.RcodeSuccess, "", true},
		{"null_ip SVCB is NODATA", config.ModeNullIP, dns.TypeSVCB, dns.RcodeSuccess, "", true},
		{"null_ip TXT is NODATA", config.ModeNullIP, dns.TypeTXT, dns.RcodeSuccess, "", true},
		{"null_ip MX is NODATA", config.ModeNullIP, dns.TypeMX, dns.RcodeSuccess, "", true},
		{"null_ip ANY is NODATA", config.ModeNullIP, dns.TypeANY, dns.RcodeSuccess, "", true},
		{"nxdomain A", config.ModeNXDomain, dns.TypeA, dns.RcodeNameError, "", true},
		{"nxdomain AAAA", config.ModeNXDomain, dns.TypeAAAA, dns.RcodeNameError, "", true},
		{"nxdomain HTTPS", config.ModeNXDomain, typeHTTPS, dns.RcodeNameError, "", true},
		{"nxdomain TXT", config.ModeNXDomain, dns.TypeTXT, dns.RcodeNameError, "", true},
		{"nodata A", config.ModeNoData, dns.TypeA, dns.RcodeSuccess, "", true},
		{"nodata AAAA", config.ModeNoData, dns.TypeAAAA, dns.RcodeSuccess, "", true},
		{"nodata HTTPS", config.ModeNoData, typeHTTPS, dns.RcodeSuccess, "", true},
		{"nodata TXT", config.ModeNoData, dns.TypeTXT, dns.RcodeSuccess, "", true},
		{"refused A", config.ModeRefused, dns.TypeA, dns.RcodeRefused, "", false},
		{"refused AAAA", config.ModeRefused, dns.TypeAAAA, dns.RcodeRefused, "", false},
		{"refused HTTPS", config.ModeRefused, typeHTTPS, dns.RcodeRefused, "", false},
		{"refused TXT", config.ModeRefused, dns.TypeTXT, dns.RcodeRefused, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := mustResponder(t, tt.mode, testBlockTTL).Block(newRequest("ads.example.com", tt.qtype))

			if resp.Rcode != tt.wantRcode {
				t.Errorf("rcode = %s, want %s", dns.RcodeToString[resp.Rcode], dns.RcodeToString[tt.wantRcode])
			}
			checkAnswerIP(t, resp, tt.wantIP)
			checkSOA(t, resp, tt.wantSOA)
			if _, err := resp.Pack(); err != nil {
				t.Errorf("response does not pack: %v", err)
			}
		})
	}
}

func checkAnswerIP(t *testing.T, resp *dns.Msg, wantIP string) {
	t.Helper()
	if wantIP == "" {
		if len(resp.Answer) != 0 {
			t.Errorf("answer section = %v, want empty", resp.Answer)
		}
		return
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("answer section has %d records, want 1", len(resp.Answer))
	}
	rr := resp.Answer[0]
	var got net.IP
	switch v := rr.(type) {
	case *dns.A:
		got = v.A
	case *dns.AAAA:
		got = v.AAAA
	default:
		t.Fatalf("answer is %T, want A or AAAA", rr)
	}
	if !got.Equal(net.ParseIP(wantIP)) {
		t.Errorf("answer IP = %v, want %s", got, wantIP)
	}
	if ttl := rr.Header().Ttl; ttl != uint32(testBlockTTL/time.Second) {
		t.Errorf("answer TTL = %d, want %d", ttl, testBlockTTL/time.Second)
	}
	if rr.Header().Name != "ads.example.com." {
		t.Errorf("answer owner = %q", rr.Header().Name)
	}
}

func checkSOA(t *testing.T, resp *dns.Msg, want bool) {
	t.Helper()
	if !want {
		if len(resp.Ns) != 0 {
			t.Errorf("authority section = %v, want empty", resp.Ns)
		}
		return
	}
	if len(resp.Ns) != 1 {
		t.Fatalf("authority section has %d records, want 1 SOA", len(resp.Ns))
	}
	soa, ok := resp.Ns[0].(*dns.SOA)
	if !ok {
		t.Fatalf("authority record is %T, want *dns.SOA", resp.Ns[0])
	}
	wantTTL := uint32(testBlockTTL / time.Second)
	if soa.Hdr.Ttl != wantTTL || soa.Minttl != wantTTL {
		t.Errorf("SOA TTL = %d, minimum = %d; both must equal block.ttl (%d)", soa.Hdr.Ttl, soa.Minttl, wantTTL)
	}
}

func TestResponder_HeaderCopiedFromRequest(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*dns.Msg)
	}{
		{"recursion desired", func(m *dns.Msg) { m.RecursionDesired = true }},
		{"recursion not desired", func(m *dns.Msg) { m.RecursionDesired = false }},
		{"checking disabled", func(m *dns.Msg) { m.CheckingDisabled = true }},
	}
	for _, mode := range []string{config.ModeNullIP, config.ModeNXDomain, config.ModeNoData, config.ModeRefused} {
		for _, tt := range tests {
			t.Run(mode+"/"+tt.name, func(t *testing.T) {
				req := newRequest("WwW.AdS.Example.com", dns.TypeA)
				tt.mutate(req)
				resp := mustResponder(t, mode, testBlockTTL).Block(req)

				if resp.Id != req.Id {
					t.Errorf("ID = %#x, want %#x", resp.Id, req.Id)
				}
				if !resp.Response {
					t.Error("QR bit is not set")
				}
				if !resp.RecursionAvailable {
					t.Error("RA bit is not set")
				}
				if resp.RecursionDesired != req.RecursionDesired {
					t.Errorf("RD = %v, want %v", resp.RecursionDesired, req.RecursionDesired)
				}
				if resp.CheckingDisabled != req.CheckingDisabled {
					t.Errorf("CD = %v, want %v", resp.CheckingDisabled, req.CheckingDisabled)
				}
				if resp.Authoritative {
					t.Error("AA bit is set on a synthetic answer")
				}
				if len(resp.Question) != 1 || resp.Question[0] != req.Question[0] {
					t.Errorf("question = %v, want %v with its original case", resp.Question, req.Question)
				}
			})
		}
	}
}

func TestResponder_NeverMutatesTheRequest(t *testing.T) {
	req := newRequest("ads.example.com", dns.TypeA)
	req.SetEdns0(4096, true)
	before := req.Copy()
	for _, mode := range []string{config.ModeNullIP, config.ModeNXDomain, config.ModeNoData, config.ModeRefused} {
		mustResponder(t, mode, testBlockTTL).Block(req)
		if req.String() != before.String() {
			t.Fatalf("mode %s changed the request:\n%s\nwas:\n%s", mode, req, before)
		}
	}
}

func TestResponder_EDNSIsMirroredOnlyWhenRequested(t *testing.T) {
	r := mustResponder(t, config.ModeNullIP, testBlockTTL)

	plain := r.Block(newRequest("ads.example.com", dns.TypeA))
	if plain.IsEdns0() != nil {
		t.Error("response carries OPT although the request had none")
	}

	req := newRequest("ads.example.com", dns.TypeA)
	req.SetEdns0(4096, true)
	opt := r.Block(req).IsEdns0()
	if opt == nil {
		t.Fatal("response lacks OPT although the request had one")
	}
	if !opt.Do() {
		t.Error("DO bit of the request was not mirrored")
	}
	if size := opt.UDPSize(); size == 0 || size > 4096 {
		t.Errorf("advertised UDP size = %d, want a sane value", size)
	}
}

func TestResponder_QuestionCountOtherThanOneIsFormErr(t *testing.T) {
	zero := new(dns.Msg)
	zero.Id = 7
	two := newRequest("a.example.com", dns.TypeA)
	two.Question = append(two.Question, dns.Question{Name: "b.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET})

	for name, req := range map[string]*dns.Msg{"zero questions": zero, "two questions": two, "nil request": nil} {
		t.Run(name, func(t *testing.T) {
			for _, mode := range []string{config.ModeNullIP, config.ModeNXDomain, config.ModeNoData, config.ModeRefused} {
				resp := mustResponder(t, mode, testBlockTTL).Block(req)
				if resp == nil || resp.Rcode != dns.RcodeFormatError {
					t.Fatalf("mode %s: response = %v, want FORMERR", mode, resp)
				}
				if len(resp.Answer) != 0 || len(resp.Ns) != 0 {
					t.Errorf("mode %s: FORMERR response carries records", mode)
				}
				if req != nil && resp.Id != req.Id {
					t.Errorf("mode %s: ID = %d, want %d", mode, resp.Id, req.Id)
				}
			}
		})
	}
}

func TestResponder_TTLConversion(t *testing.T) {
	tests := []struct {
		name string
		ttl  time.Duration
		want uint32
	}{
		{"ten seconds", 10 * time.Second, 10},
		{"zero", 0, 0},
		{"negative is clamped to zero", -5 * time.Second, 0},
		{"sub-second rounds down", 1500 * time.Millisecond, 1},
		{"larger than uint32 is clamped", time.Duration(math.MaxInt64), math.MaxUint32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := mustResponder(t, config.ModeNullIP, tt.ttl).Block(newRequest("ads.example.com", dns.TypeA))
			if got := resp.Answer[0].Header().Ttl; got != tt.want {
				t.Errorf("TTL = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestResponder_ForeignClassKeepsItsClass(t *testing.T) {
	req := new(dns.Msg)
	req.SetQuestion("ads.example.com.", dns.TypeA)
	req.Question[0].Qclass = dns.ClassCHAOS
	resp := mustResponder(t, config.ModeNullIP, testBlockTTL).Block(req)
	if len(resp.Answer) == 1 && resp.Answer[0].Header().Class != dns.ClassCHAOS {
		t.Errorf("answer class = %d, want the question class %d", resp.Answer[0].Header().Class, dns.ClassCHAOS)
	}
}

func TestRefuse_IsRefusedWhateverTheBlockMode(t *testing.T) {
	req := newRequest("example.com", dns.TypeA)
	resp := Refuse(req)
	if resp.Rcode != dns.RcodeRefused || len(resp.Answer) != 0 || len(resp.Ns) != 0 {
		t.Errorf("Refuse() = %v, want an empty REFUSED", resp)
	}
	if resp.Id != req.Id || !resp.Response {
		t.Errorf("Refuse() header = %+v", resp.MsgHdr)
	}
	if got := Refuse(nil); got == nil || got.Rcode != dns.RcodeFormatError {
		t.Errorf("Refuse(nil) = %v, want FORMERR", got)
	}
}

func TestNotImplemented(t *testing.T) {
	req := newRequest("example.com", dns.TypeA)
	req.Opcode = dns.OpcodeUpdate
	resp := notImplemented(req)
	if resp.Rcode != dns.RcodeNotImplemented || len(resp.Answer) != 0 || len(resp.Ns) != 0 {
		t.Errorf("notImplemented = %v, want an empty NOTIMP", resp)
	}
	if resp.Id != req.Id || !resp.Response || resp.Opcode != dns.OpcodeUpdate {
		t.Errorf("header = %+v, want ID copied, QR set and the opcode echoed", resp.MsgHdr)
	}
}

func TestAnswersAreCompressed(t *testing.T) {
	req := newRequest("a.very.long.name.of.a.blocked.example.com", dns.TypeHTTPS)
	for _, mode := range []string{config.ModeNullIP, config.ModeNXDomain, config.ModeNoData, config.ModeRefused} {
		resp := mustResponder(t, mode, testBlockTTL).Block(req)
		if !resp.Compress {
			t.Errorf("mode %s: the answer is not marked for compression, it would be 2x the size of the request", mode)
		}
	}
}

func TestRefuseDoesNotAdvertiseRecursion(t *testing.T) {
	if Refuse(newRequest("example.com", dns.TypeA)).RecursionAvailable {
		t.Error("an ACL refusal sets RA, telling a client that may not use the server that recursion is available")
	}
}
