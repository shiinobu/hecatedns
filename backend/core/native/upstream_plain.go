package native

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/miekg/dns"
)

type plainUpstream struct {
	network string
	addr    string
	timeout time.Duration
}

func newPlain(network, addr string, timeout time.Duration) *plainUpstream {
	return &plainUpstream{network: network, addr: addr, timeout: timeout}
}

func (u *plainUpstream) Address() string { return u.addr }
func (u *plainUpstream) Close() error    { return nil }

func (u *plainUpstream) Exchange(ctx context.Context, q *dns.Msg) (*dns.Msg, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	sent := q.Copy()
	sent.Id = dns.Id()
	resp, err := u.send(ctx, u.network, sent)
	if err == nil && resp.Truncated && u.network == "udp" {
		resp, err = u.send(ctx, "tcp", sent)
	}
	if err != nil {
		return nil, err
	}
	if err := checkReply(sent, resp); err != nil {
		return nil, fmt.Errorf("%s: %w", u.addr, err)
	}
	resp.Id = q.Id
	return resp, nil
}

func (u *plainUpstream) send(ctx context.Context, network string, sent *dns.Msg) (*dns.Msg, error) {
	client := &dns.Client{Net: network, UDPSize: ednsBufferSize, Timeout: u.timeout}
	conn, err := client.DialContext(ctx, u.addr)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", network, u.addr, err)
	}
	defer conn.Close()

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	resp, _, err := client.ExchangeWithConnContext(ctx, sent, conn)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			err = fmt.Errorf("%w: %v", context.Canceled, err)
		}
		return nil, fmt.Errorf("%s %s: %w", network, u.addr, err)
	}
	return resp, nil
}
