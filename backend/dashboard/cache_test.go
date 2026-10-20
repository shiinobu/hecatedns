package dashboard

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTTLCache_ServesFromCacheUntilExpiry(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)}
	cache := newTTLCache[string, int](5*time.Second, clock.Now)
	calls := 0
	compute := func() int {
		calls++
		return calls
	}

	first := cache.get("a", compute)
	second := cache.get("a", compute)
	clock.Advance(6 * time.Second)
	third := cache.get("a", compute)
	other := cache.get("b", compute)

	if first != 1 || second != 1 || third != 2 || other != 3 || calls != 3 {
		t.Fatalf("results %d %d %d %d with %d computations", first, second, third, other, calls)
	}
}

func TestTTLCache_ConcurrentCallersShareOneComputation(t *testing.T) {
	cache := newTTLCache[string, int](time.Minute, time.Now)
	var calls atomic.Int64
	var wg sync.WaitGroup

	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.get("k", func() int {
				calls.Add(1)
				time.Sleep(20 * time.Millisecond)
				return 7
			})
		}()
	}
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("computed %d times, want 1", calls.Load())
	}
}

func TestTTLCache_StaysBounded(t *testing.T) {
	cache := newTTLCache[int, int](time.Minute, time.Now)

	for i := range maxCacheEntries * 3 {
		cache.get(i, func() int { return i })
	}

	if n := cache.size(); n > maxCacheEntries {
		t.Fatalf("cache holds %d entries, cap is %d", n, maxCacheEntries)
	}
}
