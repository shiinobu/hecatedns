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
	// upstreamTimeout bounds one upstream exchange and one bootstrap lookup; it
	// is the value the dnsproxy backend uses.
	upstreamTimeout = 5 * time.Second

	// ednsBufferSize is the UDP payload size advertised to upstreams. 1232 bytes
	// avoids IP fragmentation on every common path (DNS flag day 2020).
	ednsBufferSize = 1232

	// Smoothed round trip time bookkeeping of the pool, in milliseconds.
	initialSRTT    = 100.0
	maxSRTT        = 30000.0
	failurePenalty = 500.0
	srttKeep       = 0.8
)

// upstream resolves a query against one DNS server.
type upstream interface {
	// Address names the server for logs and the query log.
	Address() string
	// Exchange sends a copy of q and returns the validated reply with q's ID
	// restored. It never modifies q. A reply whose ID, QR bit, opcode or
	// question does not match the query is an error, not an answer.
	Exchange(ctx context.Context, q *dns.Msg) (*dns.Msg, error)
	Close() error
}

// checkReply accepts got as the answer to sent only if it is a response to the
// same question with the ID that was sent.
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

// member is one upstream of a pool with its smoothed round trip time.
type member struct {
	up   upstream
	srtt float64 // milliseconds; guarded by pool.mu
}

// pool picks upstreams by weighted random choice (a lower round trip time
// weighs more) and falls over to the others when one fails.
type pool struct {
	mu      sync.Mutex
	members []*member
	rnd     func() float64
}

// newPool builds a pool; rnd returns a number in [0, 1) and may be nil.
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

// Exchange asks the upstreams in weighted random order until one answers. It
// returns the reply and the address of the upstream that gave it. When the
// context ends it stops at once, without blaming the upstream that was slow.
//
// An upstream that answers SERVFAIL or REFUSED (a provider throttling or
// failing) counts as failed and the next one is asked; if nobody does better,
// that answer is returned rather than an error, so the client sees the upstream's
// own verdict. Any other rcode, NXDOMAIN included, is a final answer.
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

// failedRcode reports an rcode that says the upstream could not serve the
// query, as opposed to saying something about the name.
func failedRcode(rcode int) bool {
	return rcode == dns.RcodeServerFailure || rcode == dns.RcodeRefused
}

// order returns the member indexes in the order to try them: a weighted random
// sample without replacement, weight 1/srtt.
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

// record updates the smoothed round trip time of member i: toward the measured
// time on success, sharply up on failure.
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

// Close closes every upstream and joins the errors.
func (p *pool) Close() error {
	var errs []error
	for _, m := range p.members {
		if err := m.up.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing %s: %w", m.up.Address(), err))
		}
	}
	return errors.Join(errs...)
}
