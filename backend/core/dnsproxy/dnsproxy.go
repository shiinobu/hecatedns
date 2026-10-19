package dnsproxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AdguardTeam/dnsproxy/proxy"

	"github.com/shiinobu/hecatedns/backend/core"
	"github.com/shiinobu/hecatedns/backend/lib/config"
)

const Name = config.BackendDNSProxy

const shutdownTimeout = 5 * time.Second

var errRunTwice = errors.New("dnsproxy backend: Run may be called only once, build a new backend")

var (
	_ core.Backend = (*server)(nil)
	_ core.Bound   = (*server)(nil)
)

type server struct {
	conf      *proxy.Config
	res       *resources
	ready     chan struct{}
	readyOnce sync.Once
	ran       atomic.Bool
	proxy     atomic.Pointer[proxy.Proxy]
}

func New(deps core.Deps) (core.Backend, error) {
	if deps.Engine == nil {
		return nil, errors.New("dnsproxy backend: an engine is required")
	}
	s, err := newServer(deps.Cfg, deps.Engine, deps.Log, UpstreamTimeout)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func newServer(cfg config.Config, f Filter, log *slog.Logger, timeout time.Duration) (*server, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	log = log.With("component", Name)
	conf, res, err := buildProxyConfig(cfg, &handler{filter: f, log: log}, log, timeout)
	if err != nil {
		return nil, fmt.Errorf("dnsproxy backend: %w", err)
	}
	return &server{conf: conf, res: res, ready: make(chan struct{})}, nil
}

func (s *server) Name() string { return Name }

func (s *server) Run(ctx context.Context) (err error) {
	if !s.ran.CompareAndSwap(false, true) {
		return errRunTwice
	}
	defer s.markReady()
	defer func() { err = errors.Join(err, s.res.closeBootstrap()) }()

	p, err := s.start(ctx)
	if err != nil {
		return err
	}
	s.proxy.Store(p)
	s.markReady()
	<-ctx.Done()
	return shutdown(ctx, p)
}

func (s *server) start(ctx context.Context) (*proxy.Proxy, error) {
	p, err := proxy.New(s.conf)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("dnsproxy backend: %w", err), s.res.closeUpstreams())
	}
	if err := p.Start(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("dnsproxy backend: start: %w", err), s.res.closeUpstreams())
	}
	return p, nil
}

func shutdown(ctx context.Context, p *proxy.Proxy) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := p.Shutdown(stopCtx); err != nil {
		return fmt.Errorf("dnsproxy backend: shutdown: %w", err)
	}
	return nil
}

func (s *server) markReady() {
	s.readyOnce.Do(func() { close(s.ready) })
}

func (s *server) Ready() <-chan struct{} { return s.ready }

func (s *server) Addr(network string) net.Addr {
	p := s.proxy.Load()
	if p == nil {
		return nil
	}
	switch network {
	case core.NetworkUDP:
		return p.Addr(proxy.ProtoUDP)
	case core.NetworkTCP:
		return p.Addr(proxy.ProtoTCP)
	}
	return nil
}
