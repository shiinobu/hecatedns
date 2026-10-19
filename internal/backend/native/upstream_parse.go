package native

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/shiinobu/hecatedns/internal/config"
)

const defaultDNSPort = "53"

// parseUpstream turns one upstreams entry into an upstream. The forms are the
// ones config validation accepts: a bare IP, ip:port, udp://, tcp://, tls:// and
// https://. DNS over TLS is not implemented yet, and udp:// and tcp:// need an IP
// address, since a name would need a lookup before every query.
func parseUpstream(line string, boot *bootstrap, timeout time.Duration) (upstream, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, errors.New("empty upstream")
	}
	if ip, err := netip.ParseAddr(line); err == nil {
		return newPlain("udp", netip.AddrPortFrom(ip, 53).String(), timeout), nil
	}
	if ap, err := netip.ParseAddrPort(line); err == nil {
		return newPlain("udp", ap.String(), timeout), nil
	}
	u, err := url.Parse(line)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "udp", "tcp":
		return parsePlainURL(u, timeout)
	case "tls":
		return nil, errors.New("DNS over TLS (tls://) is not supported by the native backend yet, use https:// or the dnsproxy backend")
	case "https":
		if u.Path == "" {
			u.Path = dohDefaultURI
		}
		return newDoH(u.String(), boot, timeout, nil)
	}
	return nil, fmt.Errorf("unsupported scheme %q, the native backend knows udp, tcp and https", u.Scheme)
}

func parsePlainURL(u *url.URL, timeout time.Duration) (upstream, error) {
	if _, err := netip.ParseAddr(u.Hostname()); err != nil {
		return nil, fmt.Errorf("%s:// needs an IP address, not the name %q", u.Scheme, u.Hostname())
	}
	port := u.Port()
	if port == "" {
		port = defaultDNSPort
	}
	return newPlain(u.Scheme, net.JoinHostPort(u.Hostname(), port), timeout), nil
}

// buildPool builds the upstream pool from the configuration. Every bad entry is
// reported in one joined error and nothing stays open on error.
func buildPool(cfg config.Config, timeout time.Duration) (*pool, error) {
	var boot *bootstrap
	if len(cfg.Bootstrap) > 0 {
		servers := make([]string, len(cfg.Bootstrap))
		for i, ip := range cfg.Bootstrap {
			servers[i] = net.JoinHostPort(ip, defaultDNSPort)
		}
		boot = newBootstrap(servers, timeout)
	}
	var ups []upstream
	var errs []error
	for i, line := range cfg.Upstreams {
		u, err := parseUpstream(line, boot, timeout)
		if err != nil {
			errs = append(errs, fmt.Errorf("upstreams[%d] %q: %w", i, line, err))
			continue
		}
		ups = append(ups, u)
	}
	if len(errs) > 0 {
		for _, u := range ups {
			_ = u.Close()
		}
		return nil, errors.Join(errs...)
	}
	return newPool(ups, nil), nil
}
