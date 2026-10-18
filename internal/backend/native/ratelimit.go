package native

import (
	"net/netip"
	"sync"
	"time"
)

const (
	// A client may send a burst of rateBurst queries and then rateLimit per
	// second. A phone opening a busy page sends a few dozen at once, so the
	// burst is generous; sustained 100 per second is far above any real client
	// and only a flood reaches it.
	rateLimit = 100.0
	rateBurst = 200.0

	// maxTrackedClients bounds the table, which spoofed UDP source addresses
	// could otherwise grow without limit.
	maxTrackedClients = 4096

	// idleAfter is how long a client may be silent before it is forgotten when
	// the table is full.
	idleAfter = time.Minute

	// sweepEvery is the shortest time between two scans of a full table. A scan
	// visits every entry under the lock, so scanning for every new source would let
	// a flood of spoofed addresses serialise all queries.
	sweepEvery = time.Second
)

// bucket is one client's token bucket.
type bucket struct {
	tokens float64
	last   time.Time
}

// limiter is a per-client token bucket limiter. A nil *limiter allows
// everything. When the table is full of busy clients, a new client is served
// without being tracked: refusing it would let a flood lock out legitimate users.
type limiter struct {
	mu      sync.Mutex
	buckets map[netip.Addr]*bucket
	rate    float64
	burst   float64
	max     int

	lastSweep time.Time
	sweeps    int // number of sweeps so far, for tests
	now       func() time.Time
}

func newLimiter() *limiter {
	return newLimiterAt(rateLimit, rateBurst, maxTrackedClients, time.Now)
}

func newLimiterAt(rate, burst float64, maxClients int, now func() time.Time) *limiter {
	return &limiter{buckets: make(map[netip.Addr]*bucket), rate: rate, burst: burst, max: maxClients, now: now}
}

// allow takes one token from client's bucket and reports whether one was there.
func (l *limiter) allow(client netip.Addr) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[client]
	if !ok {
		if len(l.buckets) >= l.max && now.Sub(l.lastSweep) >= sweepEvery {
			l.forgetIdleLocked(now)
			l.lastSweep = now
			l.sweeps++
		}
		if len(l.buckets) >= l.max {
			return true
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[client] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// forgetIdleLocked drops clients that have been silent for idleAfter; the
// caller holds l.mu.
func (l *limiter) forgetIdleLocked(now time.Time) {
	for addr, b := range l.buckets {
		if now.Sub(b.last) >= idleAfter {
			delete(l.buckets, addr)
		}
	}
}

// tracked reports how many clients the limiter currently tracks.
func (l *limiter) tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
