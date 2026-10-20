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
		errs := make([]error, len(resolvers))
		var wg sync.WaitGroup
		for i, addr := range resolvers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = askResolver(ctx, client, addr, host)
			}()
		}
		wg.Wait()
		return errors.Join(errs...)
	}
}

func askResolver(ctx context.Context, client *dns.Client, addr, host string) error {
	query := new(dns.Msg)
	query.SetQuestion(dns.Fqdn(host), dns.TypeA)
	start := time.Now()
	resp, _, err := client.ExchangeContext(ctx, query, addr)
	if result := interpretAnswer(host, time.Since(start), resp, err); !result.OK {
		return fmt.Errorf("%s: %s", addr, result.Error)
	}
	return nil
}
