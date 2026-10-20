package dashboard

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

const publicProbeHost = "abcdefghij0123456789.ghost.example.org"

func startCustomDNS(t *testing.T, handler dns.HandlerFunc) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: handler}
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

func emptyAnswer(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	_ = w.WriteMsg(m)
}

func TestPublicResolveCheck_PassesWhenEveryResolverAnswers(t *testing.T) {
	check := NewPublicResolveCheck([]string{startUDPDNS(t, dns.RcodeSuccess), startUDPDNS(t, dns.RcodeSuccess)}, time.Second)

	if err := check(context.Background(), publicProbeHost); err != nil {
		t.Fatalf("check: %v", err)
	}
}

func TestPublicResolveCheck_OneFailingResolverFailsTheCheck(t *testing.T) {
	bad := startUDPDNS(t, dns.RcodeNameError)
	check := NewPublicResolveCheck([]string{startUDPDNS(t, dns.RcodeSuccess), bad}, time.Second)

	err := check(context.Background(), publicProbeHost)

	if err == nil {
		t.Fatal("check passed although one resolver said NXDOMAIN")
	}
	if !strings.Contains(err.Error(), bad) || !strings.Contains(err.Error(), "NXDOMAIN") {
		t.Errorf("err = %v, want the resolver address and the code", err)
	}
	if strings.Contains(err.Error(), "abcdefghij0123456789") {
		t.Errorf("the error leaks the host name: %v", err)
	}
}

func TestPublicResolveCheck_AnAnswerWithoutRecordsFails(t *testing.T) {
	check := NewPublicResolveCheck([]string{startCustomDNS(t, emptyAnswer)}, time.Second)

	err := check(context.Background(), publicProbeHost)

	if err == nil || !strings.Contains(err.Error(), "empty answer") {
		t.Errorf("err = %v", err)
	}
}

func TestPublicResolveCheck_NothingListeningFailsWithinTheTimeout(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	_ = pc.Close()
	check := NewPublicResolveCheck([]string{addr}, 300*time.Millisecond)

	started := time.Now()
	err = check(context.Background(), publicProbeHost)

	if err == nil || time.Since(started) > 3*time.Second {
		t.Errorf("err = %v after %v", err, time.Since(started))
	}
}

func TestPublicResolveCheck_AskingIsParallel(t *testing.T) {
	slow := func(w dns.ResponseWriter, r *dns.Msg) {
		time.Sleep(300 * time.Millisecond)
		answerHandler(dns.RcodeSuccess)(w, r)
	}
	check := NewPublicResolveCheck([]string{startCustomDNS(t, slow), startCustomDNS(t, slow), startCustomDNS(t, slow)}, 2*time.Second)

	started := time.Now()
	err := check(context.Background(), publicProbeHost)

	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if took := time.Since(started); took > 800*time.Millisecond {
		t.Errorf("three 300 ms resolvers took %v, they must be asked in parallel", took)
	}
}

func TestPublicResolveCheck_NoResolversIsAnError(t *testing.T) {
	if err := NewPublicResolveCheck(nil, time.Second)(context.Background(), publicProbeHost); err == nil {
		t.Error("a check without resolvers passed")
	}
}

func TestPublicResolveCheck_StopsWhenTheContextIsCancelled(t *testing.T) {
	hang := func(dns.ResponseWriter, *dns.Msg) { time.Sleep(2 * time.Second) }
	check := NewPublicResolveCheck([]string{startCustomDNS(t, hang)}, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()
	err := check(ctx, publicProbeHost)

	if err == nil || time.Since(started) > time.Second {
		t.Errorf("err = %v after %v", err, time.Since(started))
	}
}
