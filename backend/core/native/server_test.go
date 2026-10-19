package native

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/core"
	"github.com/shiinobu/hecatedns/backend/lib/config"
)

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
			if _, err := New(core.Deps{Cfg: cfg, Engine: nil}); err == nil {
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
		{"no question", dns.Header{}, dns.MsgIgnore},
		{"two questions", dns.Header{Qdcount: 2}, dns.MsgIgnore},
		{"many records", dns.Header{Qdcount: 1, Ancount: 9}, dns.MsgIgnore},
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
	l := newLimitListener(raw, 2, 10)
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
	_ = first.Close()
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
	l := newLimitListener(raw, 1, 10)
	l.slots <- struct{}{}

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

type errReader struct{ err error }

func (r errReader) ReadTCP(net.Conn, time.Duration) ([]byte, error) { return nil, r.err }
func (r errReader) ReadUDP(*net.UDPConn, time.Duration) ([]byte, *dns.SessionUDP, error) {
	return nil, nil, r.err
}

func TestTolerantReader_OnlyAClosedSocketEndsTheServeLoop(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		temporary bool
	}{
		{"datagram larger than the buffer", wsaEMsgSize, true},
		{"any other read failure", errors.New("wsarecvfrom: some network error"), true},
		{"socket closed by shutdown", net.ErrClosed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := tolerantReader{errReader{tc.err}}.ReadUDP(nil, 0)

			var netErr net.Error
			isTemporary := errors.As(err, &netErr) && netErr.Temporary()
			if isTemporary != tc.temporary {
				t.Errorf("error %v: temporary = %v, want %v", err, isTemporary, tc.temporary)
			}
			if err == nil {
				t.Error("the error was swallowed")
			}
		})
	}
}

func TestLimitListener_CapsConnectionsPerClientAddress(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := newLimitListener(raw, 10, 2)
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

	pending := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			pending <- c
		}
	}()
	third := dial()
	_ = third.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := third.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("third connection from the same address: read = %v, want it closed by the server", err)
	}
	select {
	case <-pending:
		t.Fatal("a third connection from one address was accepted")
	default:
	}

	_ = first.Close()
	dial()
	select {
	case <-pending:
	case <-time.After(time.Second):
		t.Error("a connection was still refused after the address was back under its limit")
	}
}

func TestServer_StopsWhenCancelledBeforeItHasStarted(t *testing.T) {
	probe, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.LocalAddr().String()
	_ = probe.Close()
	cfg := config.Default()
	cfg.Listen = []string{addr}
	cfg.Upstreams = []string{"192.0.2.1"}
	cfg.Bootstrap = nil
	s, err := newServer(cfg, &fakeFilter{}, nil, testTimeout)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run with a cancelled context = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after an immediate cancel; the servers kept running")
	}

	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatalf("the UDP port is still held after Run returned: %v", err)
	}
	_ = pc.Close()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the TCP port is still held after Run returned: %v", err)
	}
	_ = ln.Close()
}

func TestLimitListener_EveryRejectedConnectionGivesItsSlotBack(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := newLimitListener(raw, 2, 1)
	defer l.Close()
	accepted := make(chan net.Conn, 8)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	dial := func() net.Conn {
		c, err := net.Dial("tcp", raw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}

	dial()
	var first net.Conn
	select {
	case first = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("the first connection was not accepted")
	}

	for i := 0; i < 5; i++ {
		c := dial()
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := c.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("rejected connection %d: read = %v, want it closed by the server at once", i+1, err)
		}
	}

	_ = first.Close()
	dial()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Error("no connection was accepted after the address was back under its limit")
	}
}

func TestServer_ShutdownDoesNotWaitForAQueryStuckAtTheUpstream(t *testing.T) {
	stuck := startFakeDNS(t, func(*dns.Msg, string) *dns.Msg { return nil })
	cfg := config.Default()
	cfg.Listen = []string{"127.0.0.1:0"}
	cfg.Upstreams = []string{stuck.addr}
	cfg.Bootstrap = nil
	s, err := newServer(cfg, &fakeFilter{}, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	select {
	case <-s.Ready():
	case err := <-done:
		t.Fatalf("Run failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server never became ready")
	}

	req := new(dns.Msg).SetQuestion("fine.example.", dns.TypeA)
	go func() {
		client := &dns.Client{Net: "udp", Timeout: 3 * time.Second}
		_, _, _ = client.Exchange(req, s.Addr(core.NetworkUDP).String())
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(stuck.asked()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the query never reached the upstream")
		}
		time.Sleep(5 * time.Millisecond)
	}

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v", err)
		}
		if took := time.Since(start); took > 4*time.Second {
			t.Errorf("shutdown took %v with one query stuck at the upstream; it must cancel queries in flight", took)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel while a query was stuck at the upstream")
	}
}

func TestServer_ReplyToAQueryWithABrokenBodyIsNeverLargerThanTheQuery(t *testing.T) {
	s, _ := startNative(t, nil)
	conn, err := net.Dial("udp", s.Addr(core.NetworkUDP).String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	msg := []byte{0x12, 0x34, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0, 3, 'a', 'b'}
	if _, err := conn.Write(msg); err != nil {
		t.Fatal(err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)

	if err == nil && n > len(msg) {
		t.Errorf("a %d-byte query got a %d-byte reply", len(msg), n)
	}
}
