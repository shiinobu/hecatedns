package native

import (
	"errors"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

func TestLimitConn_AWriteToAPeerThatNeverReadsEndsAtTheWriteTimeout(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn := &limitConn{Conn: server, writeTimeout: 50 * time.Millisecond, release: func() {}}
	result := make(chan error, 1)

	go func() {
		_, err := conn.Write(make([]byte, 1024))
		result <- err
	}()

	select {
	case err := <-result:
		if !errors.Is(err, net.ErrClosed) && !isTimeout(err) {
			t.Errorf("Write error = %v, want a timeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Write to a peer that never reads blocked past the write timeout")
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

type flakyListener struct {
	net.Listener
	failures int
	calls    int
}

type temporaryAcceptError struct{}

func (temporaryAcceptError) Error() string   { return "too many open files" }
func (temporaryAcceptError) Temporary() bool { return true }
func (temporaryAcceptError) Timeout() bool   { return false }

func (f *flakyListener) Accept() (net.Conn, error) {
	f.calls++
	if f.calls <= f.failures {
		return nil, temporaryAcceptError{}
	}
	return f.Listener.Accept()
}

func TestLimitListener_BacksOffOnTemporaryAcceptErrors(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := newLimitListener(&flakyListener{Listener: raw, failures: 4}, 4, 4, admitAll)
	defer l.Close()
	start := time.Now()

	for range 4 {
		if _, err := l.Accept(); err == nil {
			t.Fatal("Accept returned a connection, want the temporary error")
		}
	}

	if elapsed := time.Since(start); elapsed < 15*acceptBackoffMin {
		t.Errorf("4 temporary errors took %v, want a growing pause between them (at least %v)", elapsed, 15*acceptBackoffMin)
	}
}

func TestLimitListener_TheBackoffEndsAfterASuccessfulAccept(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := newLimitListener(&flakyListener{Listener: raw, failures: 3}, 4, 4, admitAll)
	defer l.Close()
	for range 3 {
		_, _ = l.Accept()
	}
	go func() {
		c, err := net.Dial("tcp", raw.Addr().String())
		if err == nil {
			defer c.Close()
			time.Sleep(200 * time.Millisecond)
		}
	}()
	if _, err := l.Accept(); err != nil {
		t.Fatalf("Accept after the failures: %v", err)
	}
	if l.backoff.delay != 0 {
		t.Errorf("backoff delay after a good accept = %v, want it reset", l.backoff.delay)
	}
}

type failingWriter struct {
	dns.ResponseWriter
	closed atomic.Bool
}

func (f *failingWriter) WriteMsg(*dns.Msg) error { return errors.New("write failed") }
func (f *failingWriter) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("192.0.2.9"), Port: 5555}
}
func (f *failingWriter) Close() error { f.closed.Store(true); return nil }

func TestHandlerWrite_ClosesAStreamWhenTheAnswerCannotBeWritten(t *testing.T) {
	for proto, wantClosed := range map[string]bool{"tcp": true, "tls": true, "udp": false} {
		t.Run(proto, func(t *testing.T) {
			h := newHandler(&fakeFilter{}, newCache(config.Cache{Enabled: false, SizeMB: 1}), nil, newLimiter(), slog.New(slog.DiscardHandler))
			w := &failingWriter{}
			req := queryFor("fine.example", dns.TypeA)

			h.write(w, req, new(dns.Msg).SetReply(req), proto)

			if got := w.closed.Load(); got != wantClosed {
				t.Errorf("connection closed after a failed %s write = %v, want %v", proto, got, wantClosed)
			}
		})
	}
}
