package native

import (
	"net/netip"
	"sync"
	"testing"
	"time"
)

func testLimiter(clock *fakeClock, rate, burst float64, maxClients int) *limiter {
	return newLimiterAt(rate, burst, maxClients, clock.Now)
}

func TestLimiter_AllowsABurstThenBlocks(t *testing.T) {
	clock := newFakeClock()
	l := testLimiter(clock, 10, 5, 100)
	client := netip.MustParseAddr("192.168.1.150")

	for i := 0; i < 5; i++ {
		if !l.allow(client) {
			t.Fatalf("query %d of the burst was refused", i+1)
		}
	}
	if l.allow(client) {
		t.Error("the query after the burst was allowed")
	}
}

func TestLimiter_RefillsOverTime(t *testing.T) {
	clock := newFakeClock()
	l := testLimiter(clock, 10, 5, 100)
	client := netip.MustParseAddr("192.168.1.150")
	for i := 0; i < 5; i++ {
		l.allow(client)
	}

	clock.Advance(200 * time.Millisecond) // two tokens at 10 per second
	if !l.allow(client) || !l.allow(client) {
		t.Error("two queries should be allowed after 200 ms")
	}
	if l.allow(client) {
		t.Error("a third query was allowed; only two tokens had refilled")
	}
}

func TestLimiter_NeverRefillsPastTheBurst(t *testing.T) {
	clock := newFakeClock()
	l := testLimiter(clock, 10, 3, 100)
	client := netip.MustParseAddr("192.168.1.150")
	l.allow(client)

	clock.Advance(time.Hour)
	allowed := 0
	for i := 0; i < 10; i++ {
		if l.allow(client) {
			allowed++
		}
	}
	if allowed != 3 {
		t.Errorf("allowed %d queries after a long pause, want the burst of 3", allowed)
	}
}

func TestLimiter_ClientsDoNotShareABudget(t *testing.T) {
	clock := newFakeClock()
	l := testLimiter(clock, 10, 1, 100)
	a, b := netip.MustParseAddr("192.168.1.10"), netip.MustParseAddr("192.168.1.11")

	l.allow(a)
	if l.allow(a) {
		t.Error("client a was allowed twice with a burst of 1")
	}
	if !l.allow(b) {
		t.Error("client b was refused because of client a")
	}
}

func TestLimiter_TableStaysBoundedUnderAnAddressFlood(t *testing.T) {
	clock := newFakeClock()
	l := testLimiter(clock, 10, 5, 50)

	for i := 0; i < 1000; i++ {
		addr := netip.AddrFrom4([4]byte{10, byte(i >> 8), byte(i), 1})
		if !l.allow(addr) {
			t.Fatalf("the first query of a new client %v was refused", addr)
		}
	}
	if n := l.tracked(); n > 50 {
		t.Errorf("limiter tracks %d clients, over its limit of 50", n)
	}
}

func TestLimiter_IdleClientsAreForgottenWhenTheTableIsFull(t *testing.T) {
	clock := newFakeClock()
	l := testLimiter(clock, 10, 5, 2)
	l.allow(netip.MustParseAddr("10.0.0.1"))
	l.allow(netip.MustParseAddr("10.0.0.2"))

	clock.Advance(2 * time.Minute)
	l.allow(netip.MustParseAddr("10.0.0.3"))
	if n := l.tracked(); n != 1 {
		t.Errorf("tracked = %d, want the two idle clients dropped and the new one kept", n)
	}
}

func TestLimiter_NilLimiterAllowsEverything(t *testing.T) {
	var l *limiter
	if !l.allow(netip.MustParseAddr("192.168.1.1")) {
		t.Error("a nil limiter refused a query")
	}
}

func TestLimiter_ConcurrentUse(t *testing.T) {
	l := newLimiter()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				l.allow(netip.AddrFrom4([4]byte{10, 0, byte(w), byte(i)}))
			}
		}(w)
	}
	wg.Wait()
}

func TestLimiter_FullTableIsSweptAtMostOncePerSecond(t *testing.T) {
	clock := newFakeClock()
	l := testLimiter(clock, 10, 5, 50)

	for i := 0; i < 1000; i++ {
		l.allow(netip.AddrFrom4([4]byte{10, byte(i >> 8), byte(i), 1}))
	}
	if l.sweeps > 1 {
		t.Errorf("table swept %d times within one second, want at most 1 (each sweep scans every entry under the lock)", l.sweeps)
	}

	clock.Advance(1100 * time.Millisecond)
	l.allow(netip.MustParseAddr("172.16.0.1"))
	if l.sweeps != 2 {
		t.Errorf("sweeps after a second passed = %d, want 2", l.sweeps)
	}
}
