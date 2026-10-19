package native

import (
	"context"
	"crypto/tls"
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

	dotAddrs  []netip.AddrPort
	dotTLS    *tls.Config
	certs     *certStore
	dotEvery  time.Duration
	dotLimits dotLimits

	ready     chan struct{}
	readyOnce sync.Once
	ran       atomic.Bool
	udpAddr   atomic.Pointer[net.Addr]
	tcpAddr   atomic.Pointer[net.Addr]
	dotAddr   atomic.Pointer[net.Addr]
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
	addrs, listenErr := plainAddrs(cfg)
	acl, aclErr := engine.NewACL(cfg.AllowedClients)
	store, dotAddrs, dotErr := newDoT(cfg, time.Now, log)
	up, poolErr := buildPool(cfg, timeout)
	if err := errors.Join(listenErr, aclErr, dotErr, poolErr); err != nil {
		if up != nil {
			_ = up.Close()
		}
		return nil, fmt.Errorf("native backend: %w", err)
	}
	h := newHandler(f, newCache(cfg.Cache), up, newLimiter(), log)
	s := &server{addrs: addrs, handler: h, up: up, admit: h.admitTCP(acl), dotLimits: defaultDoTLimits(), ready: make(chan struct{})}
	if store != nil {
		s.dotAddrs, s.certs, s.dotEvery = dotAddrs, store, cfg.DoT.ReloadEvery
		s.dotTLS = h.dotTLSConfig(store, cfg.DoT.Names)
	}
	return s, nil
}

func plainAddrs(cfg config.Config) ([]netip.AddrPort, error) {
	if len(cfg.Listen) == 0 && cfg.DoT.Enabled {
		return nil, nil
	}
	return listenAddrs(cfg.Listen)
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
	dots, err := s.bindDoT()
	if err != nil {
		for _, c := range closersOf(socks, nil) {
			_ = c.Close()
		}
		return fmt.Errorf("native backend: %w", err)
	}
	s.storeAddrs(socks, dots)

	queries, stopQueries := context.WithCancel(context.Background())
	defer stopQueries()
	s.handler.base = queries
	if s.certs != nil {
		go s.certs.watch(queries, s.dotEvery)
	}

	var started sync.WaitGroup
	var srvs []runner
	for _, srv := range servers(socks, s.handler, started.Done) {
		srvs = append(srvs, srv)
	}
	for _, dot := range dots {
		dot.started = started.Done
		srvs = append(srvs, dot)
	}
	started.Add(len(srvs))
	return serve(ctx, srvs, closersOf(socks, dots), &started, s.markReady, stopQueries)
}

func (s *server) bindDoT() ([]*dotServer, error) {
	if s.certs == nil {
		return nil, nil
	}
	return bindDoT(s.dotAddrs, dotParams{tls: s.dotTLS, admit: s.admit, owner: s.handler, lim: s.dotLimits})
}

func (s *server) storeAddrs(socks []socket, dots []*dotServer) {
	if len(socks) > 0 {
		udp, tcp := socks[0].udp.LocalAddr(), socks[0].tcp.Addr()
		s.udpAddr.Store(&udp)
		s.tcpAddr.Store(&tcp)
	}
	if len(dots) > 0 {
		dot := dots[0].Addr()
		s.dotAddr.Store(&dot)
	}
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
	case core.NetworkTLS:
		p = s.dotAddr.Load()
	}
	if p == nil {
		return nil
	}
	return *p
}
