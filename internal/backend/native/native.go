package native

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"xdns/internal/backend"
	"xdns/internal/config"
)

// Name is the name of this backend in the configuration.
const Name = config.BackendNative

var errRunTwice = errors.New("native backend: Run may be called only once, build a new backend")

var (
	_ backend.Backend = (*server)(nil)
	_ backend.Bound   = (*server)(nil)
)

// server is the native backend: UDP and TCP listeners on every listen address,
// a handler, a cache and an upstream pool. It is single-use.
type server struct {
	addrs   []netip.AddrPort
	handler *handler
	up      *pool

	ready     chan struct{}
	readyOnce sync.Once
	ran       atomic.Bool
	udpAddr   atomic.Pointer[net.Addr]
	tcpAddr   atomic.Pointer[net.Addr]
}

// New builds the native backend; it is a backend.Factory. The returned backend
// also implements backend.Bound. Configuration errors are reported here, not by
// Run, and nothing stays open on error.
func New(deps backend.Deps) (backend.Backend, error) {
	if deps.Engine == nil {
		return nil, errors.New("native backend: an engine is required")
	}
	return newServer(deps.Cfg, deps.Engine, deps.Log, upstreamTimeout)
}

// newServer builds a server around any Filter and upstream timeout, so tests can
// inject a fake filter and fail fast against a dead upstream.
func newServer(cfg config.Config, f Filter, log *slog.Logger, timeout time.Duration) (*server, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	log = log.With("component", Name)
	addrs, listenErr := listenAddrs(cfg.Listen)
	up, poolErr := buildPool(cfg, timeout)
	if err := errors.Join(listenErr, poolErr); err != nil {
		if up != nil {
			_ = up.Close()
		}
		return nil, fmt.Errorf("native backend: %w", err)
	}
	h := newHandler(f, newCache(cfg.Cache), up, newLimiter(), log)
	return &server{addrs: addrs, handler: h, up: up, ready: make(chan struct{})}, nil
}

// Name implements backend.Backend.
func (s *server) Name() string { return Name }

// Run implements backend.Backend: it binds, serves until ctx ends, then shuts
// down. A bind failure is returned at once. Ready is closed in every case.
func (s *server) Run(ctx context.Context) (err error) {
	if !s.ran.CompareAndSwap(false, true) {
		return errRunTwice
	}
	defer s.markReady()
	defer func() { err = errors.Join(err, s.up.Close()) }()

	socks, err := bind(s.addrs)
	if err != nil {
		return fmt.Errorf("native backend: %w", err)
	}
	udp, tcp := socks[0].udp.LocalAddr(), socks[0].tcp.Addr()
	s.udpAddr.Store(&udp)
	s.tcpAddr.Store(&tcp)

	// Queries run under this context, which is cancelled when the shutdown begins.
	queries, stopQueries := context.WithCancel(context.Background())
	defer stopQueries()
	s.handler.base = queries

	var started sync.WaitGroup
	srvs := servers(socks, s.handler, started.Done)
	started.Add(len(srvs))
	return serve(ctx, srvs, socks, &started, s.markReady, stopQueries)
}

func (s *server) markReady() {
	s.readyOnce.Do(func() { close(s.ready) })
}

// Ready implements backend.Bound.
func (s *server) Ready() <-chan struct{} { return s.ready }

// Addr implements backend.Bound. It is nil before the sockets are bound, after a
// failed bind, and for a network other than udp or tcp.
func (s *server) Addr(network string) net.Addr {
	var p *net.Addr
	switch network {
	case backend.NetworkUDP:
		p = s.udpAddr.Load()
	case backend.NetworkTCP:
		p = s.tcpAddr.Load()
	}
	if p == nil {
		return nil
	}
	return *p
}
