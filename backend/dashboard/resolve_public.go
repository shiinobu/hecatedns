package dashboard

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/miekg/dns"
)

var errNoPublicResolvers = errors.New("no public resolvers are configured")

func NewPublicResolveCheck(resolvers []string, timeout time.Duration) func(context.Context, string) error {
	client := &dns.Client{Net: netUDP, Timeout: orDefault(timeout, defaultResolverTimeout)}
	return func(ctx context.Context, host string) error {
		if len(resolvers) == 0 {
			return errNoPublicResolvers
		}
		_, err := parallelEach(resolvers, func(addr string) (struct{}, error) {
			_, err := exchangeA(ctx, client, addr, host)
			return struct{}{}, err
		})
		return err
	}
}

func parallelEach[T any](items []string, fn func(item string) (T, error)) ([]T, error) {
	results := make([]T, len(items))
	errs := make([]error, len(items))
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Go(func() { results[i], errs[i] = fn(item) })
	}
	wg.Wait()
	return results, errors.Join(errs...)
}

func exchangeA(ctx context.Context, client *dns.Client, addr, host string) (*dns.Msg, error) {
	query := new(dns.Msg)
	query.SetQuestion(dns.Fqdn(host), dns.TypeA)
	start := time.Now()
	resp, _, err := client.ExchangeContext(ctx, query, addr)
	if result := interpretAnswer(host, time.Since(start), resp, err); !result.OK {
		return nil, fmt.Errorf("%s: %s", addr, result.Error)
	}
	return resp, nil
}
