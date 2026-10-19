package native

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

func dialFrom(t *testing.T, ip, addr string) net.Conn {
	t.Helper()
	dialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(ip)}, Timeout: time.Second}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		t.Skipf("this host cannot connect from %s: %v", ip, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func closedWithin(conn net.Conn, wait time.Duration) bool {
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	buf := make([]byte, 512)
	for {
		if _, err := conn.Read(buf); err != nil {
			return !isTimeout(err)
		}
	}
}

func mustAnswer(t *testing.T, conn *dns.Conn, name string) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := conn.WriteMsg(queryFor(name, dns.TypeA)); err != nil {
		t.Fatalf("writing the query for %s: %v", name, err)
	}
	resp, err := conn.ReadMsg()
	if err != nil || len(resp.Answer) != 1 {
		t.Fatalf("query for %s = %v, %v; want one answer", name, resp, err)
	}
}

func TestDoT_SilentConnectionsFillOnlyThePreHandshakePool(t *testing.T) {
	env := startDoTWith(t, nil, func(l *dotLimits) {
		l.preTotal, l.preIP = 6, 2
		l.total, l.perIP = 2, 2
		l.handshake = 1500 * time.Millisecond
	})
	established := env.dial(t)
	mustAnswer(t, established, "before.example")
	var silent []net.Conn
	for _, ip := range []string{"127.0.0.2", "127.0.0.3", "127.0.0.4"} {
		for range 2 {
			silent = append(silent, dialFrom(t, ip, env.addr(t)))
		}
	}
	for _, conn := range silent {
		if closedWithin(conn, 40*time.Millisecond) {
			t.Fatal("a silent connection within the pre-handshake cap was dropped at once: an established connection must not use the pre-handshake pool")
		}
	}
	beyond := dialFrom(t, "127.0.0.5", env.addr(t))

	if !closedWithin(beyond, 300*time.Millisecond) {
		t.Error("a connection beyond the pre-handshake cap was kept")
	}
	mustAnswer(t, established, "during.example")
	eventually(t, 2*time.Second, "a warning about the full pool", func() bool {
		return env.logs.has(slog.LevelWarn, "reason="+reasonTLSBusy)
	})
	for _, conn := range silent {
		if !closedWithin(conn, 3*time.Second) {
			t.Error("a silent connection outlived the handshake timeout")
		}
	}
	mustAnswer(t, env.dial(t), "after.example")
}

func TestDoT_EstablishedConnectionsAreCappedPerAddress(t *testing.T) {
	env := startDoTWith(t, nil, func(l *dotLimits) { l.total, l.perIP = 2, 1 })
	first := env.dial(t)
	mustAnswer(t, first, "one.example")
	second := env.dial(t)

	_ = second.SetDeadline(time.Now().Add(2 * time.Second))
	if err := second.WriteMsg(queryFor("two.example", dns.TypeA)); err == nil {
		if _, err := second.ReadMsg(); err == nil {
			t.Error("a second connection from one address was served")
		}
	}
	eventually(t, 2*time.Second, "a warning about the per-address cap", func() bool {
		return env.logs.has(slog.LevelWarn, "reason="+reasonTLSBusyIP)
	})
	_ = first.Close()
	eventually(t, 3*time.Second, "the slot of the closed connection to be free", func() bool {
		conn := env.dial(t)
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		if conn.WriteMsg(queryFor("three.example", dns.TypeA)) != nil {
			return false
		}
		resp, err := conn.ReadMsg()
		return err == nil && len(resp.Answer) == 1
	})
}

func TestDoT_TotalEstablishedConnectionsAreCapped(t *testing.T) {
	env := startDoTWith(t, nil, func(l *dotLimits) { l.total, l.perIP = 2, 2 })
	a, b := env.dial(t), env.dial(t)
	mustAnswer(t, a, "a.example")
	mustAnswer(t, b, "b.example")
	third := dialFrom(t, "127.0.0.2", env.addr(t))
	secure := tls.Client(third, &tls.Config{RootCAs: env.pki.pool, ServerName: dotName})

	if err := secure.Handshake(); err == nil {
		conn := &dns.Conn{Conn: secure}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		if conn.WriteMsg(queryFor("c.example", dns.TypeA)) == nil {
			if _, err := conn.ReadMsg(); err == nil {
				t.Error("a connection beyond the total cap was served")
			}
		}
	}
	eventually(t, 2*time.Second, "a warning about the full pool", func() bool {
		return env.logs.has(slog.LevelWarn, "reason="+reasonTLSBusy)
	})
}

func TestDoT_AConnectionEndsAfterTheQueryLimit(t *testing.T) {
	env := startDoTWith(t, nil, func(l *dotLimits) { l.maxQueries = 3 })
	conn := env.dial(t)
	for i := range 3 {
		mustAnswer(t, conn, fmt.Sprintf("q%d.example", i))
	}

	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if conn.WriteMsg(queryFor("q3.example", dns.TypeA)) == nil {
		if _, err := conn.ReadMsg(); err == nil {
			t.Error("a fourth query was answered on a connection limited to three")
		}
	}
}

func TestDoT_FramesThatCannotBeQueriesCloseTheConnection(t *testing.T) {
	cases := map[string][]byte{
		"a frame longer than allowed":     {0x20, 0x00},
		"a frame shorter than a header":   {0x00, 0x05, 1, 2, 3, 4, 5},
		"a frame announced and cut short": {0x00, 0x40, 1, 2, 3},
		"a zero-length frame":             {0x00, 0x00},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			env := startDoTWith(t, nil, func(l *dotLimits) { l.frame = 300 * time.Millisecond })
			conn := env.dial(t)

			if _, err := conn.Conn.Write(raw); err != nil {
				t.Fatal(err)
			}

			if !closedWithin(conn.Conn, 2*time.Second) {
				t.Error("the connection stayed open")
			}
		})
	}
}

func TestDoT_AResponseSentAsAQueryIsIgnoredAndTheConnectionStaysUsable(t *testing.T) {
	env := startDoT(t, nil)
	conn := env.dial(t)
	bogus := queryFor("bogus.example", dns.TypeA)
	bogus.Response = true
	if err := conn.WriteMsg(bogus); err != nil {
		t.Fatal(err)
	}

	mustAnswer(t, conn, "real.example")
}

func TestDoT_AnUnreadableQueryGetsFormatError(t *testing.T) {
	env := startDoT(t, nil)
	conn := env.dial(t)
	cutName := []byte{0x00, 0x12, 0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x3f, 'a', 'b', 'c', 'd', 'e'}
	if _, err := conn.Conn.Write(cutName); err != nil {
		t.Fatal(err)
	}

	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	resp, err := conn.ReadMsg()

	if err != nil || resp.Id != 0x1234 || resp.Rcode != dns.RcodeFormatError {
		t.Errorf("answer to a question whose name is cut off = %v, %v; want FORMERR with id 0x1234", resp, err)
	}
}

func TestDoT_APipelineIsBoundedByTheInFlightLimit(t *testing.T) {
	const delay = 500 * time.Millisecond
	up := startFakeDNS(t, func(req *dns.Msg, _ string) *dns.Msg {
		time.Sleep(delay)
		return aReply(req)
	})
	env := startDoTWith(t, func(c *config.Config) { c.Upstreams = []string{up.addr} }, func(l *dotLimits) { l.inFlight = 2 })
	conn := env.dial(t)
	start := time.Now()
	for i := range 3 {
		if err := conn.WriteMsg(queryFor(fmt.Sprintf("slow%d.example", i), dns.TypeA)); err != nil {
			t.Fatal(err)
		}
	}

	var at [3]time.Duration
	for i := range at {
		if _, err := conn.ReadMsg(); err != nil {
			t.Fatal(err)
		}
		at[i] = time.Since(start)
	}

	if at[1] >= delay+delay/2 || at[2] < at[1]+delay/2 {
		t.Errorf("answers arrived at %v; want two together after about %v and the third a full delay later", at, delay)
	}
}

func TestDoT_GarbageOnThePortIsReportedWithoutTheClientSendingAName(t *testing.T) {
	env := startDoT(t, nil)
	conn := dialFrom(t, "127.0.0.1", env.addr(t))

	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))

	if !closedWithin(conn, 2*time.Second) {
		t.Fatal("the connection stayed open")
	}
	eventually(t, 2*time.Second, "a warning about the failed handshake", func() bool {
		return env.logs.has(slog.LevelWarn, "reason="+reasonTLSHandshake)
	})
}

func TestDoT_ShutdownEndsOpenConnectionsQuickly(t *testing.T) {
	env, err := newDoTEnv(t, time.Now().Add(60*24*time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- env.s.Run(ctx) }()
	select {
	case <-env.s.Ready():
	case err := <-done:
		t.Fatalf("Run failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server never became ready")
	}
	idle := env.dial(t)
	mustAnswer(t, idle, "idle.example")
	dialFrom(t, "127.0.0.1", env.addr(t))
	midway := dialFrom(t, "127.0.0.2", env.addr(t))
	_, _ = midway.Write([]byte{0x16, 0x03, 0x01})

	start := time.Now()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v after cancel", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return with connections open")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("shutdown with open connections took %v, want it prompt", took)
	}
}

func pipedTLSServer(t *testing.T) *tls.Conn {
	t.Helper()
	pki := newTestPKI(t)
	certPEM, keyPEM := pki.issue(t, 1, []string{dotBase}, time.Now().Add(time.Hour))
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	serverEnd, clientEnd := net.Pipe()
	t.Cleanup(func() { _ = serverEnd.Close(); _ = clientEnd.Close() })
	server := tls.Server(serverEnd, &tls.Config{Certificates: []tls.Certificate{pair}, SessionTicketsDisabled: true})
	client := tls.Client(clientEnd, &tls.Config{RootCAs: pki.pool, ServerName: dotBase})
	handshakes := make(chan error, 2)
	go func() { handshakes <- server.Handshake() }()
	go func() { handshakes <- client.Handshake() }()
	for range 2 {
		if err := <-handshakes; err != nil {
			t.Fatalf("handshake: %v", err)
		}
	}
	return server
}

func TestDotWriter_AWriteToAPeerThatNeverReadsFailsAtTheWriteTimeout(t *testing.T) {
	w := &dotWriter{conn: pipedTLSServer(t), timeout: 100 * time.Millisecond}
	result := make(chan error, 1)

	go func() {
		_, err := w.Write(make([]byte, 512))
		result <- err
	}()

	select {
	case err := <-result:
		if err == nil {
			t.Error("a write to a peer that never reads succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the write blocked past its timeout")
	}
}

func TestDotWriter_CloseAfterAFailedWriteDoesNotWaitForThePeer(t *testing.T) {
	w := &dotWriter{conn: pipedTLSServer(t), timeout: 100 * time.Millisecond}
	if _, err := w.Write(make([]byte, 512)); err == nil {
		t.Fatal("a write to a peer that never reads succeeded")
	}
	closed := make(chan struct{})

	start := time.Now()
	go func() {
		_ = w.Close()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatalf("Close still blocked after %v: it waits for a peer that never reads to take the close_notify alert", time.Since(start))
	}
}

func TestConnPool_ReleaseIsIdempotentAndTheCapsHold(t *testing.T) {
	pool := newConnPool(2, 2)
	ip := netip.MustParseAddr("192.0.2.1")
	release, _ := pool.acquire(ip)
	release()
	release()

	_, first := pool.acquire(ip)
	_, second := pool.acquire(ip)
	_, third := pool.acquire(ip)

	if first != "" || second != "" || third != reasonTLSBusy {
		t.Errorf("refusals = %q, %q, %q; want none, none and %q", first, second, third, reasonTLSBusy)
	}
}

func TestDoT_InFlightQueriesAreBoundedAcrossAllConnections(t *testing.T) {
	const delay = 300 * time.Millisecond
	var running, peak atomic.Int32
	up := startFakeDNS(t, func(req *dns.Msg, _ string) *dns.Msg {
		now := running.Add(1)
		for {
			seen := peak.Load()
			if now <= seen || peak.CompareAndSwap(seen, now) {
				break
			}
		}
		time.Sleep(delay)
		running.Add(-1)
		return aReply(req)
	})
	env := startDoTWith(t, func(c *config.Config) { c.Upstreams = []string{up.addr} }, func(l *dotLimits) { l.inFlightTotal = 2 })
	conns := []*dns.Conn{env.dial(t), env.dial(t)}
	for i, conn := range conns {
		for j := range 2 {
			if err := conn.WriteMsg(queryFor(fmt.Sprintf("slow%d-%d.example", i, j), dns.TypeA)); err != nil {
				t.Fatal(err)
			}
		}
	}

	for _, conn := range conns {
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		for range 2 {
			if _, err := conn.ReadMsg(); err != nil {
				t.Fatal(err)
			}
		}
	}

	if got := peak.Load(); got > 2 {
		t.Errorf("%d queries were in flight at once, want at most 2 across all connections", got)
	}
}
