package native

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

const (
	upstreamTimeout = 5 * time.Second

	ednsBufferSize = 1232

	initialSRTT    = 100.0
	maxSRTT        = 30000.0
	failurePenalty = 500.0
	srttKeep       = 0.8
)

type upstream interface {
	Address() string

	Exchange(ctx context.Context, q *dns.Msg) (*dns.Msg, error)
	Close() error
}

func checkReply(sent, got *dns.Msg) error {
	switch {
	case !got.Response:
		return errors.New("reply is not a response")
	case got.Id != sent.Id:
		return fmt.Errorf("reply ID %d, want %d", got.Id, sent.Id)
	case got.Opcode != sent.Opcode:
		return fmt.Errorf("reply opcode %d, want %d", got.Opcode, sent.Opcode)
	case len(got.Question) != 1 || len(sent.Question) != 1:
		return errors.New("reply does not repeat exactly one question")
	}
	a, b := sent.Question[0], got.Question[0]
	if a.Qtype != b.Qtype || a.Qclass != b.Qclass || !strings.EqualFold(a.Name, b.Name) {
		return fmt.Errorf("reply is for %s %s, want %s %s",
			b.Name, dns.TypeToString[b.Qtype], a.Name, dns.TypeToString[a.Qtype])
	}
	return nil
}

type member struct {
	up   upstream
	srtt float64
}

type pool struct {
	mu      sync.Mutex
	members []*member
	rnd     func() float64
}

func newPool(ups []upstream, rnd func() float64) *pool {
	if rnd == nil {
		rnd = rand.Float64
	}
	members := make([]*member, len(ups))
	for i, u := range ups {
		members[i] = &member{up: u, srtt: initialSRTT}
	}
	return &pool{members: members, rnd: rnd}
}

func (p *pool) Exchange(ctx context.Context, q *dns.Msg) (*dns.Msg, string, error) {
	var errs []error
	var fallback *dns.Msg
	var fallbackFrom string
	for _, i := range p.order() {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		m := p.members[i]
		start := time.Now()
		resp, err := m.up.Exchange(ctx, q)
		if err == nil && failedRcode(resp.Rcode) {
			if fallback == nil {
				fallback, fallbackFrom = resp, m.up.Address()
			}
			err = fmt.Errorf("answered %s", dns.RcodeToString[resp.Rcode])
			resp = nil
		}
		if ctx.Err() == nil {
			p.record(i, time.Since(start), err)
		}
		if err == nil {
			return resp, m.up.Address(), nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", m.up.Address(), err))
	}
	if fallback != nil {
		return fallback, fallbackFrom, nil
	}
	return nil, "", fmt.Errorf("all upstreams failed: %w", errors.Join(errs...))
}

func failedRcode(rcode int) bool {
	return rcode == dns.RcodeServerFailure || rcode == dns.RcodeRefused
}

func (p *pool) order() []int {
	p.mu.Lock()
	weights := make([]float64, len(p.members))
	for i, m := range p.members {
		weights[i] = 1 / max(m.srtt, 1)
	}
	p.mu.Unlock()

	remaining := make([]int, len(weights))
	for i := range remaining {
		remaining[i] = i
	}
	out := make([]int, 0, len(weights))
	for len(remaining) > 0 {
		var total float64
		for _, i := range remaining {
			total += weights[i]
		}
		target, pick := p.rnd()*total, len(remaining)-1
		var acc float64
		for k, i := range remaining {
			if acc += weights[i]; target < acc {
				pick = k
				break
			}
		}
		out = append(out, remaining[pick])
		remaining = append(remaining[:pick], remaining[pick+1:]...)
	}
	return out
}

func (p *pool) record(i int, rtt time.Duration, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := p.members[i]
	if err != nil {
		m.srtt = min(m.srtt*2+failurePenalty, maxSRTT)
		return
	}
	m.srtt = srttKeep*m.srtt + (1-srttKeep)*float64(rtt)/float64(time.Millisecond)
}

func (p *pool) Close() error {
	var errs []error
	for _, m := range p.members {
		if err := m.up.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing %s: %w", m.up.Address(), err))
		}
	}
	return errors.Join(errs...)
}
