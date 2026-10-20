package dashboard

import (
	"sync"
	"time"
)

const maxCacheEntries = 32

type cacheEntry[V any] struct {
	value V
	at    time.Time
}

type ttlCache[K comparable, V any] struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	entries map[K]cacheEntry[V]
}

func newTTLCache[K comparable, V any](ttl time.Duration, now func() time.Time) *ttlCache[K, V] {
	return &ttlCache[K, V]{ttl: ttl, now: now, entries: make(map[K]cacheEntry[V])}
}

func (c *ttlCache[K, V]) get(key K, compute func() V) V {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok && c.now().Sub(e.at) < c.ttl {
		return e.value
	}
	value := compute()
	if len(c.entries) >= maxCacheEntries {
		clear(c.entries)
	}
	c.entries[key] = cacheEntry[V]{value: value, at: c.now()}
	return value
}

func (c *ttlCache[K, V]) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
