package dnsproxybe

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

	"github.com/shiinobu/hecatedns/internal/backend"
	"github.com/shiinobu/hecatedns/internal/config"
)

// Name is the name of this backend in the configuration.
const Name = config.BackendDNSProxy

// shutdownTimeout bounds closing the listeners and upstreams after the run
// context ends.
const shutdownTimeout = 5 * time.Second

var errRunTwice = errors.New("dnsproxy backend: Run may be called only once, build a new backend")

var (
	_ backend.Backend = (*server)(nil)
	_ backend.Bound   = (*server)(nil)
)

// server runs one dnsproxy instance. It is single-use, because a dnsproxy
// Proxy cannot be restarted cleanly after Shutdown.
type server struct {
	conf      *proxy.Config
	res       *resources
	ready     chan struct{}
	readyOnce sync.Once
	ran       atomic.Bool
	proxy     atomic.Pointer[proxy.Proxy]
}

// New builds the dnsproxy backend; it is a backend.Factory. The returned
// backend also implements backend.Bound. Configuration errors are reported
// here, not by Run.
func New(deps backend.Deps) (backend.Backend, error) {
	if deps.Engine == nil {
		return nil, errors.New("dnsproxy backend: an engine is required")
	}
	s, err := newServer(deps.Cfg, deps.Engine, deps.Log, UpstreamTimeout)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// newServer builds a server around any Filter and upstream timeout, so tests
// can inject a fake filter and fail fast against a dead upstream.
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

// Name implements backend.Backend.
func (s *server) Name() string { return Name }

// Run implements backend.Backend: it listens, serves until ctx ends, then
// shuts down. A listen failure is returned at once. Ready is closed in every
// case.
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

// start creates and starts the proxy. On failure it closes the upstreams
// itself: Start closes the listeners it opened, but Shutdown does nothing for a
// proxy that never started.
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

// shutdown stops p with a fresh deadline, since ctx is already done. dnsproxy
// does not wait for requests in flight, so the engine may still see a few
// calls after Run returned.
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

// Ready implements backend.Bound.
func (s *server) Ready() <-chan struct{} { return s.ready }

// Addr implements backend.Bound. It is nil before the listeners are up, after
// a failed start, and for a network other than udp or tcp.
func (s *server) Addr(network string) net.Addr {
	p := s.proxy.Load()
	if p == nil {
		return nil
	}
	switch network {
	case backend.NetworkUDP:
		return p.Addr(proxy.ProtoUDP)
	case backend.NetworkTCP:
		return p.Addr(proxy.ProtoTCP)
	}
	return nil
}
