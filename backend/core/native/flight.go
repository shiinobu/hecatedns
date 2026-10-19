package native

import (
	"context"
	"errors"
	"sync"

	"github.com/miekg/dns"
)

var errFlightAborted = errors.New("the shared upstream exchange ended without an answer")

type flight struct {
	done chan struct{}
	resp *dns.Msg
	from string
	err  error
}

type flights struct {
	mu sync.Mutex
	m  map[cacheKey]*flight
}

func newFlights() *flights {
	return &flights{m: make(map[cacheKey]*flight)}
}

func (g *flights) get(key cacheKey) *flight {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.m[key]
}

func (g *flights) lead(key cacheKey) (f *flight, leader bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if existing, ok := g.m[key]; ok {
		return existing, false
	}
	f = &flight{done: make(chan struct{})}
	g.m[key] = f
	return f, true
}

func (g *flights) finish(key cacheKey, f *flight, resp *dns.Msg, from string, err error) {
	if resp != nil {
		f.resp = resp.Copy()
	}
	f.from, f.err = from, err
	if resp == nil && err == nil {
		f.err = errFlightAborted
	}
	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()
	close(f.done)
}

func (f *flight) wait(ctx context.Context) (*dns.Msg, string, error) {
	select {
	case <-f.done:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	if f.err != nil {
		return nil, "", f.err
	}
	return f.resp.Copy(), f.from, nil
}
