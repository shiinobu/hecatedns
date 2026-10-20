package dashboard

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"slices"
	"time"

	"github.com/miekg/dns"
)

var (
	errNoPublicAddress = errors.New("no address in the public answers")
	carrierGradeNAT    = netip.MustParsePrefix("100.64.0.0/10")
)

type PublicReachOptions struct {
	Resolvers      []string
	Port           string
	RootCAs        *x509.CertPool
	Timeout        time.Duration
	AllowNonPublic bool
}

func NewPublicReachCheck(o PublicReachOptions) func(context.Context, string) error {
	client := &dns.Client{Net: netUDP, Timeout: orDefault(o.Timeout, defaultResolverTimeout)}
	return func(ctx context.Context, host string) error {
		if len(o.Resolvers) == 0 {
			return errNoPublicResolvers
		}
		answers, err := parallelEach(o.Resolvers, func(addr string) ([]string, error) {
			return lookupAddresses(ctx, client, addr, host)
		})
		if err != nil {
			return err
		}
		seen := map[string]struct{}{}
		for _, list := range answers {
			for _, address := range list {
				seen[address] = struct{}{}
			}
		}
		return reachAddresses(ctx, o, host, slices.Sorted(maps.Keys(seen)))
	}
}

func reachAddresses(ctx context.Context, o PublicReachOptions, host string, addresses []string) error {
	if len(addresses) == 0 {
		return errNoPublicAddress
	}
	_, err := parallelEach(addresses, func(address string) (struct{}, error) {
		if !o.AllowNonPublic && !publicAddress(address) {
			return struct{}{}, fmt.Errorf("%s: not a public address", address)
		}
		return struct{}{}, reachAddress(ctx, o, net.JoinHostPort(address, o.Port), host)
	})
	return err
}

func publicAddress(address string) bool {
	addr, err := netip.ParseAddr(address)
	return err == nil && addr.IsGlobalUnicast() && !addr.IsPrivate() && !carrierGradeNAT.Contains(addr)
}

func lookupAddresses(ctx context.Context, client *dns.Client, resolver, host string) ([]string, error) {
	resp, err := exchangeA(ctx, client, resolver, host)
	if err != nil {
		return nil, err
	}
	var addresses []string
	for _, record := range resp.Answer {
		if a, ok := record.(*dns.A); ok {
			addresses = append(addresses, a.A.String())
		}
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("%s: no address", resolver)
	}
	return addresses, nil
}

func reachAddress(ctx context.Context, o PublicReachOptions, target, host string) error {
	result := NewResolverCheck(ResolverOptions{Addr: target, ServerName: host, RootCAs: o.RootCAs, Timeout: o.Timeout})(ctx)
	if !result.OK {
		return fmt.Errorf("%s: %s", target, result.Error)
	}
	return nil
}
