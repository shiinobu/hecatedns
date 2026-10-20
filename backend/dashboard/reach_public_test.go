package dashboard

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func startAddressDNS(t *testing.T, ips ...string) string {
	t.Helper()
	return startCustomDNS(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		for _, ip := range ips {
			m.Answer = append(m.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP(ip),
			})
		}
		_ = w.WriteMsg(m)
	})
}

func startReachDoT(t *testing.T) (port string, pool *x509.CertPool) {
	t.Helper()
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
	_, port, err = net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port, pool
}

func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func reachCheck(resolvers []string, port string, pool *x509.CertPool) func(context.Context, string) error {
	return NewPublicReachCheck(PublicReachOptions{Resolvers: resolvers, Port: port, RootCAs: pool, Timeout: time.Second, AllowNonPublic: true})
}

func TestPublicReachCheck_PassesWhenTheAnsweredAddressServesTheName(t *testing.T) {
	port, pool := startReachDoT(t)
	check := reachCheck([]string{startAddressDNS(t, "127.0.0.1")}, port, pool)

	if err := check(context.Background(), publicProbeHost); err != nil {
		t.Fatalf("check: %v", err)
	}
}

func TestPublicReachCheck_AnAddressWithoutADoTServerFails(t *testing.T) {
	_, pool := startReachDoT(t)
	check := reachCheck([]string{startAddressDNS(t, "127.0.0.1")}, closedPort(t), pool)

	err := check(context.Background(), publicProbeHost)

	if err == nil {
		t.Fatal("the check passed although nothing serves DoT at the answered address")
	}
	if !strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "abcdefghij0123456789") {
		t.Errorf("err = %v, want the address and no host name", err)
	}
}

func TestPublicReachCheck_OneBadAddressFailsTheCheck(t *testing.T) {
	port, pool := startReachDoT(t)
	check := reachCheck([]string{startAddressDNS(t, "127.0.0.1", "127.0.0.2")}, port, pool)

	err := check(context.Background(), publicProbeHost)

	if err == nil || !strings.Contains(err.Error(), "127.0.0.2") {
		t.Errorf("err = %v, want a failure naming 127.0.0.2", err)
	}
}

func TestPublicReachCheck_ChecksTheAddressesOfEveryResolver(t *testing.T) {
	port, pool := startReachDoT(t)
	check := reachCheck([]string{startAddressDNS(t, "127.0.0.1"), startAddressDNS(t, "127.0.0.2")}, port, pool)

	err := check(context.Background(), publicProbeHost)

	if err == nil || !strings.Contains(err.Error(), "127.0.0.2") {
		t.Errorf("err = %v, want a failure naming the address only the second resolver gave", err)
	}
}

func TestPublicReachCheck_ANameTheServerDoesNotServeFails(t *testing.T) {
	port, pool := startReachDoT(t)
	check := reachCheck([]string{startAddressDNS(t, "127.0.0.1")}, port, pool)

	err := check(context.Background(), "abcdefghij0123456789.other.example.net")

	if err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("err = %v, want a failure naming the address", err)
	}
}

func TestPublicReachCheck_AServerThatNeverAnswersFailsWithinTheTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range held {
			_ = conn.Close()
		}
	})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, conn)
			mu.Unlock()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	check := NewPublicReachCheck(PublicReachOptions{Resolvers: []string{startAddressDNS(t, "127.0.0.1")}, Port: port, Timeout: 300 * time.Millisecond, AllowNonPublic: true})

	started := time.Now()
	err = check(context.Background(), publicProbeHost)

	if err == nil || time.Since(started) > 3*time.Second {
		t.Errorf("err = %v after %v", err, time.Since(started))
	}
}

func TestPublicReachCheck_AnAnswerWithoutAnAddressFails(t *testing.T) {
	check := reachCheck([]string{startCustomDNS(t, emptyAnswer)}, "853", nil)

	err := check(context.Background(), publicProbeHost)

	if err == nil || !strings.Contains(err.Error(), "empty answer") {
		t.Errorf("err = %v", err)
	}
}

func TestPublicReachCheck_ANameThatDoesNotResolveFails(t *testing.T) {
	check := reachCheck([]string{startUDPDNS(t, dns.RcodeNameError)}, "853", nil)

	err := check(context.Background(), publicProbeHost)

	if err == nil || !strings.Contains(err.Error(), "NXDOMAIN") {
		t.Errorf("err = %v", err)
	}
}

func TestPublicReachCheck_NoResolversIsAnError(t *testing.T) {
	if err := reachCheck(nil, "853", nil)(context.Background(), publicProbeHost); err == nil {
		t.Error("a check without resolvers passed")
	}
}

func TestPublicReachCheck_StopsWhenTheContextIsCancelled(t *testing.T) {
	hang := func(dns.ResponseWriter, *dns.Msg) { time.Sleep(2 * time.Second) }
	check := NewPublicReachCheck(PublicReachOptions{Resolvers: []string{startCustomDNS(t, hang)}, Port: "853", Timeout: 5 * time.Second, AllowNonPublic: true})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()
	err := check(ctx, publicProbeHost)

	if err == nil || time.Since(started) > time.Second {
		t.Errorf("err = %v after %v", err, time.Since(started))
	}
}

func startCNAMEOnlyDNS(t *testing.T) string {
	t.Helper()
	return startCustomDNS(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = append(m.Answer, &dns.CNAME{
			Hdr:    dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 60},
			Target: "elsewhere.example.net.",
		})
		_ = w.WriteMsg(m)
	})
}

func TestPublicReachCheck_RefusesAddressesThatAreNotPublic(t *testing.T) {
	port, pool := startReachDoT(t)
	for _, address := range []string{"127.0.0.1", "0.0.0.0", "10.1.2.3", "172.16.0.9", "192.168.1.5", "169.254.1.1", "100.64.0.1", "224.0.0.1"} {
		t.Run(address, func(t *testing.T) {
			check := NewPublicReachCheck(PublicReachOptions{Resolvers: []string{startAddressDNS(t, address)}, Port: port, RootCAs: pool, Timeout: time.Second})

			err := check(context.Background(), publicProbeHost)

			if err == nil || !strings.Contains(err.Error(), "not a public address") || !strings.Contains(err.Error(), address) {
				t.Errorf("err = %v, want a refusal naming %s", err, address)
			}
			if err != nil && strings.Contains(err.Error(), "abcdefghij0123456789") {
				t.Errorf("the error leaks the host name: %v", err)
			}
		})
	}
}

func TestPublicReachCheck_APublicAddressIsDialedNotRefused(t *testing.T) {
	check := NewPublicReachCheck(PublicReachOptions{Resolvers: []string{startAddressDNS(t, "203.0.113.7")}, Port: closedPort(t), Timeout: 200 * time.Millisecond})

	err := check(context.Background(), publicProbeHost)

	if err == nil || strings.Contains(err.Error(), "not a public address") || !strings.Contains(err.Error(), "203.0.113.7") {
		t.Errorf("err = %v, want a connection failure naming the address", err)
	}
}

func TestPublicReachCheck_AnAnswerWithOnlyACNAMEFails(t *testing.T) {
	check := reachCheck([]string{startCNAMEOnlyDNS(t)}, "853", nil)

	err := check(context.Background(), publicProbeHost)

	if err == nil || !strings.Contains(err.Error(), "no address") {
		t.Errorf("err = %v, want a no address failure", err)
	}
}

func TestReachAddresses_NothingToCheckIsAnError(t *testing.T) {
	err := reachAddresses(context.Background(), PublicReachOptions{Port: "853", AllowNonPublic: true}, publicProbeHost, nil)

	if err == nil || !strings.Contains(err.Error(), "no address") {
		t.Errorf("err = %v, want a no address failure", err)
	}
}
