package native

import (
	"net/netip"
	"sync"
)

const (
	// maxInFlight bounds the queries waiting on an upstream at once, as the
	// dnsproxy backend bounds its goroutines (dnsproxy.MaxGoroutines). Only
	// upstream exchanges take a slot: a cache hit or a block answers at once and
	// must keep working while the upstream is slow.
	maxInFlight = 256

	// maxInFlightPerClient keeps one client from taking every slot. A phone
	// opening a busy page has a few dozen lookups in flight, so 64 is generous.
	maxInFlightPerClient = 64
)

// gate limits the upstream queries in flight, in total and per client.
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

// acquire takes a slot for client and reports whether one was free.
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

// release gives back a slot taken by acquire.
func (g *gate) release(client netip.Addr) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.total--
	if g.perClient[client]--; g.perClient[client] <= 0 {
		delete(g.perClient, client)
	}
}
