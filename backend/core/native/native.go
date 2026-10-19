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

	"github.com/shiinobu/hecatedns/backend/core"
	"github.com/shiinobu/hecatedns/backend/engine"
	"github.com/shiinobu/hecatedns/backend/lib/config"
)

const Name = config.BackendNative

var errRunTwice = errors.New("native backend: Run may be called only once, build a new backend")

var (
	_ core.Backend = (*server)(nil)
	_ core.Bound   = (*server)(nil)
)

type server struct {
	addrs   []netip.AddrPort
	handler *handler
	up      *pool
	admit   func(netip.Addr) bool

	ready     chan struct{}
	readyOnce sync.Once
	ran       atomic.Bool
	udpAddr   atomic.Pointer[net.Addr]
	tcpAddr   atomic.Pointer[net.Addr]
}

func New(deps core.Deps) (core.Backend, error) {
	if deps.Engine == nil {
		return nil, errors.New("native backend: an engine is required")
	}
	return newServer(deps.Cfg, deps.Engine, deps.Log, upstreamTimeout)
}

func newServer(cfg config.Config, f Filter, log *slog.Logger, timeout time.Duration) (*server, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	log = log.With("component", Name)
	addrs, listenErr := listenAddrs(cfg.Listen)
	acl, aclErr := engine.NewACL(cfg.AllowedClients)
	up, poolErr := buildPool(cfg, timeout)
	if err := errors.Join(listenErr, aclErr, poolErr); err != nil {
		if up != nil {
			_ = up.Close()
		}
		return nil, fmt.Errorf("native backend: %w", err)
	}
	h := newHandler(f, newCache(cfg.Cache), up, newLimiter(), log)
	return &server{addrs: addrs, handler: h, up: up, admit: h.admitTCP(acl), ready: make(chan struct{})}, nil
}

func (s *server) Name() string { return Name }

func (s *server) Run(ctx context.Context) (err error) {
	if !s.ran.CompareAndSwap(false, true) {
		return errRunTwice
	}
	defer s.markReady()
	defer func() { err = errors.Join(err, s.up.Close()) }()

	socks, err := bind(s.addrs, s.admit)
	if err != nil {
		return fmt.Errorf("native backend: %w", err)
	}
	udp, tcp := socks[0].udp.LocalAddr(), socks[0].tcp.Addr()
	s.udpAddr.Store(&udp)
	s.tcpAddr.Store(&tcp)

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

func (s *server) Ready() <-chan struct{} { return s.ready }

func (s *server) Addr(network string) net.Addr {
	var p *net.Addr
	switch network {
	case core.NetworkUDP:
		p = s.udpAddr.Load()
	case core.NetworkTCP:
		p = s.tcpAddr.Load()
	}
	if p == nil {
		return nil
	}
	return *p
}
