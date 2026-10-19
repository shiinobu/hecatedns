package native

import (
	"net/netip"
	"sync"
)

const (
	maxInFlight = 256

	maxInFlightPerClient = 64
)

type gate struct {
	mu           sync.Mutex
	total        int
	maxTotal     int
	perClient    map[netip.Addr]int
	maxPerClient int
}

func newGate() *gate {
	return &gate{maxTotal: maxInFlight, maxPerClient: maxInFlightPerClient, perClient: make(map[netip.Addr]int)}
}

func (g *gate) acquire(client netip.Addr) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.total >= g.maxTotal || g.perClient[client] >= g.maxPerClient {
		return false
	}
	g.total++
	g.perClient[client]++
	return true
}

func (g *gate) release(client netip.Addr) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.total--
	if g.perClient[client]--; g.perClient[client] <= 0 {
		delete(g.perClient, client)
	}
}
