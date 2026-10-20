package dashboard

import (
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
)

type limitListener struct {
	net.Listener
	slots    chan struct{}
	rejected *atomic.Uint64
	log      *slog.Logger

	closeOnce sync.Once
	closeErr  error
}

func newLimitListener(ln net.Listener, slots chan struct{}, rejected *atomic.Uint64, log *slog.Logger) *limitListener {
	return &limitListener{Listener: ln, slots: slots, rejected: rejected, log: log}
}

func (l *limitListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &slotConn{Conn: conn, slots: l.slots}, nil
		default:
			l.rejected.Add(1)
			if err := conn.Close(); err != nil {
				l.log.Debug("closing a refused connection failed", "err", err)
			}
		}
	}
}

func (l *limitListener) Close() error {
	l.closeOnce.Do(func() { l.closeErr = l.Listener.Close() })
	return l.closeErr
}

type slotConn struct {
	net.Conn
	slots   chan struct{}
	release sync.Once
}

func (c *slotConn) Close() error {
	err := c.Conn.Close()
	c.release.Do(func() { <-c.slots })
	return err
}
