package dashboard

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func answerHandler(rcode int) dns.HandlerFunc {
	return func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = rcode
		if rcode == dns.RcodeSuccess {
			m.Answer = append(m.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP("93.184.216.34"),
			})
		}
		_ = w.WriteMsg(m)
	}
}

func startUDPDNS(t *testing.T, rcode int) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: answerHandler(rcode)}
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

func TestResolverCheck_PlainUDPAnswer(t *testing.T) {
	addr := startUDPDNS(t, dns.RcodeSuccess)

	got := NewResolverCheck(ResolverOptions{Addr: addr})(context.Background())

	if !got.OK || got.Name != "example.com" || got.Error != "" || got.MS < 0 {
		t.Fatalf("result = %+v", got)
	}
}

func TestResolverCheck_RefusedIsAFailureWithTheCode(t *testing.T) {
	addr := startUDPDNS(t, dns.RcodeRefused)

	got := NewResolverCheck(ResolverOptions{Addr: addr})(context.Background())

	if got.OK || got.MS != 0 || got.Error != "REFUSED" {
		t.Fatalf("result = %+v, want a REFUSED failure with ms 0", got)
	}
}

func TestResolverCheck_NothingListeningIsAFailure(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	_ = pc.Close()

	got := NewResolverCheck(ResolverOptions{Addr: addr, Timeout: 300 * time.Millisecond})(context.Background())

	if got.OK || got.MS != 0 || got.Error == "" {
		t.Fatalf("result = %+v, want a failure with an error text", got)
	}
}

func TestResolverCheck_RealDoTQueryWithTheServerName(t *testing.T) {
	store, pool := testStore(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{GetCertificate: store.GetCertificate, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{Listener: ln, Handler: answerHandler(dns.RcodeSuccess)}
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })
	opts := ResolverOptions{Addr: ln.Addr().String(), ServerName: "abc." + testProbe, RootCAs: pool}

	good := NewResolverCheck(opts)(context.Background())
	opts.ServerName = "wrong.example.net"
	wrongName := NewResolverCheck(opts)(context.Background())

	if !good.OK {
		t.Fatalf("DoT check failed: %+v", good)
	}
	if wrongName.OK || wrongName.Error == "" {
		t.Fatalf("a certificate for another name was accepted: %+v", wrongName)
	}
}
