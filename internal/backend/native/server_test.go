package native

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	"xdns/internal/backend"
	"xdns/internal/config"
)

// startNative runs a native backend on a random loopback port against up, until
// the test ends.
func startNative(t *testing.T, mutate func(*config.Config)) (*server, <-chan error) {
	t.Helper()
	cfg := config.Default()
	cfg.Listen = []string{"127.0.0.1:0"}
	cfg.Bootstrap = nil
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := newServer(cfg, &fakeFilter{}, nil, testTimeout)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned %v after cancel", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Run did not return after cancel")
		}
	})
	select {
	case <-s.Ready():
	case err := <-done:
		t.Fatalf("Run failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server never became ready")
	}
	return s, done
}

func TestServer_AnswersOverUDPAndTCPAndTheAddressesAreReported(t *testing.T) {
	up := goodDNS(t)
	s, _ := startNative(t, func(c *config.Config) { c.Upstreams = []string{up.addr} })

	for _, network := range []string{"udp", "tcp"} {
		addr := s.Addr(network)
		if addr == nil {
			t.Fatalf("Addr(%q) is nil after Ready", network)
		}
		client := &dns.Client{Net: network, Timeout: testTimeout}
		resp, _, err := client.Exchange(queryFor("fine.example", dns.TypeA), addr.String())
		if err != nil || len(resp.Answer) != 1 {
			t.Errorf("%s query = %v, %v; want one answer", network, resp, err)
		}
	}
	if s.Addr("sctp") != nil {
		t.Error("Addr for an unknown network is not nil")
	}
}

func TestServer_IgnoresGarbageAndKeepsServing(t *testing.T) {
	up := goodDNS(t)
	s, _ := startNative(t, func(c *config.Config) { c.Upstreams = []string{up.addr} })
	addr := s.Addr("udp").String()

	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, junk := range [][]byte{{1, 2, 3}, make([]byte, 5000), []byte("GET / HTTP/1.1\r\n\r\n")} {
		_, _ = conn.Write(junk)
	}
	// A response (QR set) must not be answered either.
	echo := queryFor("fine.example", dns.TypeA)
	echo.Response = true
	wire, _ := echo.Pack()
	_, _ = conn.Write(wire)

	client := &dns.Client{Net: "udp", Timeout: testTimeout}
	resp, _, err := client.Exchange(queryFor("fine.example", dns.TypeA), addr)
	if err != nil || len(resp.Answer) != 1 {
		t.Errorf("query after garbage = %v, %v; want a normal answer", resp, err)
	}
}

func TestServer_ANYOverTheWireIsNotImplemented(t *testing.T) {
	up := goodDNS(t)
	s, _ := startNative(t, func(c *config.Config) { c.Upstreams = []string{up.addr} })

	client := &dns.Client{Net: "udp", Timeout: testTimeout}
	resp, _, err := client.Exchange(queryFor("fine.example", dns.TypeANY), s.Addr("udp").String())
	if err != nil || resp.Rcode != dns.RcodeNotImplemented {
		t.Errorf("ANY = %v, %v; want NOTIMP", resp, err)
	}
	if got := len(up.asked()); got != 0 {
		t.Errorf("an ANY query reached the upstream %d times", got)
	}
}

func TestServer_BindFailureIsReportedAndReadyIsClosed(t *testing.T) {
	taken, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	cfg := config.Default()
	cfg.Listen = []string{taken.LocalAddr().String()}
	s, err := newServer(cfg, &fakeFilter{}, nil, testTimeout)
	if err != nil {
		t.Fatal(err)
	}

	runErr := s.Run(context.Background())

	if runErr == nil || !strings.Contains(runErr.Error(), "listening") {
		t.Errorf("Run = %v, want a listen error", runErr)
	}
	select {
	case <-s.Ready():
	default:
		t.Error("Ready was not closed after a failed start")
	}
	if s.Addr("udp") != nil {
		t.Error("Addr is set although nothing is listening")
	}
}

func TestServer_RunTwiceIsRefused(t *testing.T) {
	s, _ := startNative(t, nil)
	if err := s.Run(context.Background()); !errors.Is(err, errRunTwice) {
		t.Errorf("second Run = %v, want errRunTwice", err)
	}
}

func TestServer_FactoryRejectsBadConfiguration(t *testing.T) {
	cases := map[string]func(*config.Config){
		"DNS over TLS upstream": func(c *config.Config) { c.Upstreams = []string{"tls://1.1.1.1"} },
		"bad listen address":    func(c *config.Config) { c.Listen = []string{"not-an-address"} },
		"no listen address":     func(c *config.Config) { c.Listen = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := config.Default()
			mutate(&cfg)
			if _, err := New(backend.Deps{Cfg: cfg, Engine: nil}); err == nil {
				t.Error("a missing engine was accepted")
			}
			if _, err := newServer(cfg, &fakeFilter{}, nil, testTimeout); err == nil {
				t.Errorf("%s was accepted", name)
			}
		})
	}
}

func TestAcceptMsg(t *testing.T) {
	cases := []struct {
		name string
		dh   dns.Header
		want dns.MsgAcceptAction
	}{
		{"query", dns.Header{Qdcount: 1}, dns.MsgAccept},
		{"query with EDNS", dns.Header{Qdcount: 1, Arcount: 1}, dns.MsgAccept},
		{"response", dns.Header{Bits: 1 << 15, Qdcount: 1}, dns.MsgIgnore},
		{"update opcode still reaches the engine", dns.Header{Bits: uint16(dns.OpcodeUpdate) << 11, Qdcount: 1}, dns.MsgAccept},
		{"no question", dns.Header{}, dns.MsgReject},
		{"two questions", dns.Header{Qdcount: 2}, dns.MsgReject},
		{"many records", dns.Header{Qdcount: 1, Ancount: 9}, dns.MsgReject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := acceptMsg(tc.dh); got != tc.want {
				t.Errorf("acceptMsg = %v, want %v", got, tc.want)
			}
		})
	}
}

func acceptWithin(t *testing.T, l net.Listener, d time.Duration) (net.Conn, error) {
	t.Helper()
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := l.Accept()
		ch <- result{c, err}
	}()
	select {
	case r := <-ch:
		return r.conn, r.err
	case <-time.After(d):
		return nil, context.DeadlineExceeded
	}
}

func TestLimitListener_WaitsForAFreeSlotAndReleasesOnClose(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := newLimitListener(raw, 2)
	defer l.Close()
	dial := func() net.Conn {
		c, err := net.Dial("tcp", raw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}

	dial()
	first, err := acceptWithin(t, l, time.Second)
	if err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	dial()
	if _, err := acceptWithin(t, l, time.Second); err != nil {
		t.Fatalf("second Accept: %v", err)
	}

	dial()
	third := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		third <- err
	}()
	select {
	case err := <-third:
		t.Fatalf("third Accept returned (%v) with both slots taken, want it to wait", err)
	case <-time.After(200 * time.Millisecond):
	}

	_ = first.Close()
	_ = first.Close() // closing twice must not free a second slot
	select {
	case err := <-third:
		if err != nil {
			t.Errorf("third Accept after a slot was freed: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("third Accept still waits after a slot was freed")
	}
}

func TestLimitListener_CloseWakesAWaitingAccept(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := newLimitListener(raw, 1)
	l.slots <- struct{}{} // take the only slot

	done := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	_ = l.Close()

	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Errorf("Accept = %v, want net.ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not wake the Accept that waited for a slot")
	}
}
