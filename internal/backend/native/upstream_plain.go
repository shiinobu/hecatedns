package native

import (
	"context"
	"fmt"
	"time"

	"github.com/miekg/dns"
)

// plainUpstream speaks classic DNS over UDP, or over TCP. A UDP exchange whose
// reply is truncated is repeated over TCP.
type plainUpstream struct {
	network string // "udp" or "tcp"
	addr    string // ip:port
	timeout time.Duration
}

func newPlain(network, addr string, timeout time.Duration) *plainUpstream {
	return &plainUpstream{network: network, addr: addr, timeout: timeout}
}

func (u *plainUpstream) Address() string { return u.addr }
func (u *plainUpstream) Close() error    { return nil }

// Exchange implements upstream. The query goes out under a fresh random ID, so
// a spoofer who cannot read the query cannot guess it.
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
	resp, _, err := client.ExchangeContext(ctx, sent, u.addr)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", network, u.addr, err)
	}
	return resp, nil
}
