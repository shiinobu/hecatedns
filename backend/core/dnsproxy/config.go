package dnsproxy

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/netip"
	"time"

	"github.com/AdguardTeam/dnsproxy/proxy"
	"github.com/AdguardTeam/dnsproxy/upstream"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

const MaxGoroutines = 256

const UpstreamTimeout = 5 * time.Second

const bytesPerMB = 1 << 20

type resources struct {
	upstreams *proxy.UpstreamConfig
	bootstrap []*upstream.UpstreamResolver
}

func (r *resources) close() error {
	return errors.Join(r.closeUpstreams(), r.closeBootstrap())
}

func (r *resources) closeUpstreams() error {
	if r.upstreams == nil {
		return nil
	}
	if err := r.upstreams.Close(); err != nil {
		return fmt.Errorf("closing upstreams: %w", err)
	}
	return nil
}

func (r *resources) closeBootstrap() error {
	var errs []error
	for _, b := range r.bootstrap {
		if err := b.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing bootstrap %s: %w", b.Address(), err))
		}
	}
	return errors.Join(errs...)
}

func buildProxyConfig(cfg config.Config, h proxy.Handler, log *slog.Logger, timeout time.Duration) (*proxy.Config, *resources, error) {
	udp, tcp, listenErr := listenAddrs(cfg.Listen)
	res := &resources{}
	opts := &upstream.Options{Logger: log, Timeout: timeout}
	boot, bootErr := res.buildBootstrap(cfg.Bootstrap, opts)
	upErr := res.buildUpstreams(cfg.Upstreams, boot, opts)
	if err := errors.Join(listenErr, bootErr, upErr); err != nil {
		return nil, nil, errors.Join(err, res.close())
	}
	return newProxyConfig(cfg, udp, tcp, res.upstreams, h, log), res, nil
}

func newProxyConfig(cfg config.Config, udp []*net.UDPAddr, tcp []*net.TCPAddr, ups *proxy.UpstreamConfig, h proxy.Handler, log *slog.Logger) *proxy.Config {
	return &proxy.Config{
		Logger:          log,
		UDPListenAddr:   udp,
		TCPListenAddr:   tcp,
		UpstreamConfig:  ups,
		UpstreamMode:    proxy.UpstreamModeLoadBalance,
		RequestHandler:  h,
		CacheEnabled:    cfg.Cache.Enabled,
		CacheSizeBytes:  cacheSizeBytes(cfg.Cache.SizeMB),
		CacheMinTTL:     ttlSeconds(cfg.Cache.MinTTL),
		CacheMaxTTL:     ttlSeconds(cfg.Cache.MaxTTL),
		MaxGoroutines:   MaxGoroutines,
		RefuseAny:       true,
		PendingRequests: &proxy.PendingRequestsConfig{Enabled: true},
	}
}

func listenAddrs(listen []string) ([]*net.UDPAddr, []*net.TCPAddr, error) {
	if len(listen) == 0 {
		return nil, nil, errors.New("listen: at least one ip:port address is required")
	}
	udp := make([]*net.UDPAddr, 0, len(listen))
	tcp := make([]*net.TCPAddr, 0, len(listen))
	var errs []error
	for i, entry := range listen {
		addrPort, err := netip.ParseAddrPort(entry)
		if err != nil {
			errs = append(errs, fmt.Errorf("listen[%d] %q is not ip:port: %w", i, entry, err))
			continue
		}
		udp = append(udp, net.UDPAddrFromAddrPort(addrPort))
		tcp = append(tcp, net.TCPAddrFromAddrPort(addrPort))
	}
	return udp, tcp, errors.Join(errs...)
}

func (r *resources) buildBootstrap(ips []string, opts *upstream.Options) (upstream.Resolver, error) {
	resolvers := make(upstream.ConsequentResolver, 0, len(ips))
	var errs []error
	for i, ip := range ips {
		resolver, err := upstream.NewUpstreamResolver(ip, opts)
		if err != nil {
			errs = append(errs, fmt.Errorf("bootstrap[%d] %q: %w", i, ip, err))
			errs = append(errs, closeResolver(resolver))
			continue
		}
		r.bootstrap = append(r.bootstrap, resolver)
		resolvers = append(resolvers, resolver)
	}
	if len(resolvers) == 0 {
		return nil, errors.Join(errs...)
	}
	return resolvers, errors.Join(errs...)
}

func closeResolver(resolver *upstream.UpstreamResolver) error {
	if resolver == nil || resolver.Upstream == nil {
		return nil
	}
	if err := resolver.Close(); err != nil {
		return fmt.Errorf("closing rejected bootstrap: %w", err)
	}
	return nil
}

func (r *resources) buildUpstreams(lines []string, boot upstream.Resolver, base *upstream.Options) error {
	opts := &upstream.Options{Logger: base.Logger, Timeout: base.Timeout, Bootstrap: boot}
	conf, err := proxy.ParseUpstreamsConfig(lines, opts)
	r.upstreams = conf
	if err != nil {
		return fmt.Errorf("upstreams: %w", err)
	}
	if err := conf.Validate(); err != nil {
		return fmt.Errorf("upstreams: %w", err)
	}
	return nil
}

func ttlSeconds(d time.Duration) uint32 {
	switch {
	case d <= 0:
		return 0
	case d < time.Second:
		return 1
	case d/time.Second > math.MaxUint32:
		return math.MaxUint32
	}
	return uint32(d / time.Second)
}

func cacheSizeBytes(mb int) int {
	switch {
	case mb <= 0:
		return 0
	case mb > math.MaxInt/bytesPerMB:
		return math.MaxInt
	}
	return mb * bytesPerMB
}
