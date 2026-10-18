package native

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/miekg/dns"
)

// bootstrap resolves the host name of an upstream (https://dns.example/...)
// through plain DNS servers given by IP, because the xdns resolver cannot
// resolve its own upstream's name.
type bootstrap struct {
	servers []*plainUpstream
}

// newBootstrap builds a resolver over servers, each an ip:port address.
func newBootstrap(servers []string, timeout time.Duration) *bootstrap {
	b := &bootstrap{}
	for _, s := range servers {
		b.servers = append(b.servers, newPlain("udp", s, timeout))
	}
	return b
}

// lookup returns the A and AAAA addresses of host, from the first server that
// gives any. The servers are tried in order.
func (b *bootstrap) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	if b == nil || len(b.servers) == 0 {
		return nil, fmt.Errorf("cannot resolve %s: no bootstrap resolver configured", host)
	}
	var errs []error
	for _, server := range b.servers {
		ips, err := lookupVia(ctx, server, host)
		if len(ips) > 0 {
			return ips, nil
		}
		if err == nil {
			err = errors.New("no A or AAAA record")
		}
		errs = append(errs, fmt.Errorf("bootstrap %s: %w", server.Address(), err))
	}
	return nil, fmt.Errorf("resolving %s: %w", host, errors.Join(errs...))
}

// lookupVia asks one server for A and AAAA. It returns what it got; the error is
// set only when a query failed.
func lookupVia(ctx context.Context, server upstream, host string) ([]netip.Addr, error) {
	var ips []netip.Addr
	var errs []error
	for _, qtype := range []uint16{dns.TypeA, dns.TypeAAAA} {
		q := new(dns.Msg)
		q.SetQuestion(dns.Fqdn(host), qtype)
		resp, err := server.Exchange(ctx, q)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, rr := range resp.Answer {
			switch v := rr.(type) {
			case *dns.A:
				if ip, ok := netip.AddrFromSlice(v.A); ok {
					ips = append(ips, ip.Unmap())
				}
			case *dns.AAAA:
				if ip, ok := netip.AddrFromSlice(v.AAAA); ok {
					ips = append(ips, ip)
				}
			}
		}
	}
	return ips, errors.Join(errs...)
}

// dialContext returns a dial function for http.Transport that resolves host
// names through b. It is safe on a nil b, which then resolves nothing.
func (b *bootstrap) dialContext(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if _, err := netip.ParseAddr(host); err == nil {
			return dialer.DialContext(ctx, network, addr)
		}
		ips, err := b.lookup(ctx, host)
		if err != nil {
			return nil, err
		}
		var errs []error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			errs = append(errs, err)
		}
		return nil, errors.Join(errs...)
	}
}
