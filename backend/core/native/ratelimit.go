package native

import (
	"net/netip"
	"sync"
	"time"
)

const (
	rateLimit = 200.0
	rateBurst = 500.0

	maxTrackedClients = 4096

	idleAfter = time.Minute

	sweepEvery = time.Second
)

type bucket struct {
	tokens float64
	last   time.Time
}

type limiter struct {
	mu      sync.Mutex
	buckets map[netip.Addr]*bucket
	rate    float64
	burst   float64
	max     int

	lastSweep time.Time
	sweeps    int
	now       func() time.Time
}

func newLimiter() *limiter {
	return newLimiterAt(rateLimit, rateBurst, maxTrackedClients, time.Now)
}

func newLimiterAt(rate, burst float64, maxClients int, now func() time.Time) *limiter {
	return &limiter{buckets: make(map[netip.Addr]*bucket), rate: rate, burst: burst, max: maxClients, now: now}
}

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

func (l *limiter) forgetIdleLocked(now time.Time) {
	for addr, b := range l.buckets {
		if now.Sub(b.last) >= idleAfter {
			delete(l.buckets, addr)
		}
	}
}

func (l *limiter) tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
