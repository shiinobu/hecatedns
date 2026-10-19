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

type bootstrap struct {
	servers []*plainUpstream
}

func newBootstrap(servers []string, timeout time.Duration) *bootstrap {
	b := &bootstrap{}
	for _, s := range servers {
		b.servers = append(b.servers, newPlain("udp", s, timeout))
	}
	return b
}

func shareOfDeadline(ctx context.Context, parts int) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok || parts <= 1 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Until(deadline)/time.Duration(parts))
}

func (b *bootstrap) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	if b == nil || len(b.servers) == 0 {
		return nil, fmt.Errorf("cannot resolve %s: no bootstrap resolver configured", host)
	}
	var errs []error
	for i, server := range b.servers {
		serverCtx, cancel := shareOfDeadline(ctx, len(b.servers)-i)
		ips, err := lookupVia(serverCtx, server, host)
		cancel()
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

		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		ips, err := b.lookup(ctx, host)
		if err != nil {
			return nil, err
		}
		var errs []error
		for i, ip := range ips {
			dialCtx, cancel := shareOfDeadline(ctx, len(ips)-i)
			conn, err := dialer.DialContext(dialCtx, network, net.JoinHostPort(ip.String(), port))
			cancel()
			if err == nil {
				return conn, nil
			}
			errs = append(errs, err)
		}
		return nil, errors.Join(errs...)
	}
}
