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

	clock.Advance(200 * time.Millisecond)
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
		if !l.allow(addr) && i < 50 {
			t.Fatalf("the first query of client %d was refused while the table still had room", i+1)
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

func ipv6In(subnet, host uint16) netip.Addr {
	return netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, byte(subnet >> 8), byte(subnet), 0, 0, 0, 0, 0, 0, byte(host >> 8), byte(host)})
}

func TestLimiter_FullTableGivesNewSourcesOneSharedBudget(t *testing.T) {
	clock := newFakeClock()
	l := testLimiter(clock, 10, 5, 4)
	for i := 0; i < 4; i++ {
		l.allow(netip.AddrFrom4([4]byte{10, 0, 0, byte(i)}))
	}

	allowed := 0
	for source := 0; source < 4; source++ {
		addr := netip.AddrFrom4([4]byte{10, 0, 1, byte(source)})
		for i := 0; i < 20; i++ {
			if l.allow(addr) {
				allowed++
			}
		}
	}
	if allowed != 5 {
		t.Errorf("%d queries from untracked sources were allowed at one instant, want the shared burst of 5", allowed)
	}
}

func TestLimiter_SharedBudgetOfUntrackedSourcesRefillsOverTime(t *testing.T) {
	clock := newFakeClock()
	l := testLimiter(clock, 10, 2, 1)
	l.allow(netip.MustParseAddr("10.0.0.1"))
	stranger := netip.MustParseAddr("10.0.1.1")
	l.allow(stranger)
	l.allow(stranger)
	if l.allow(stranger) {
		t.Fatal("the shared budget of untracked sources outlasted its burst of 2")
	}

	clock.Advance(200 * time.Millisecond)
	if !l.allow(netip.MustParseAddr("10.0.2.1")) {
		t.Error("untracked sources are still refused after the shared budget refilled")
	}
}

func TestLimiter_TrackedClientKeepsItsOwnBudgetWhileUntrackedSourcesFlood(t *testing.T) {
	clock := newFakeClock()
	l := testLimiter(clock, 10, 5, 2)
	phone := netip.MustParseAddr("192.168.1.150")
	l.allow(phone)
	l.allow(netip.MustParseAddr("10.0.0.2"))
	for i := 0; i < 100; i++ {
		l.allow(netip.AddrFrom4([4]byte{10, 0, 1, byte(i)}))
	}

	for i := 0; i < 4; i++ {
		if !l.allow(phone) {
			t.Fatalf("query %d of the tracked client was refused because of the flood", i+2)
		}
	}
}

func TestLimiter_HostsInOneIPv6PrefixShareABudget(t *testing.T) {
	clock := newFakeClock()
	l := testLimiter(clock, 10, 5, 100)

	allowed := 0
	for host := uint16(1); host <= 20; host++ {
		if l.allow(ipv6In(1, host)) {
			allowed++
		}
	}
	if allowed != 5 {
		t.Errorf("%d queries from 20 hosts of one /64 were allowed, want the burst of 5 shared between them", allowed)
	}
}

func TestLimiter_DifferentIPv6PrefixesDoNotShareABudget(t *testing.T) {
	clock := newFakeClock()
	l := testLimiter(clock, 10, 1, 100)

	l.allow(ipv6In(1, 1))
	if !l.allow(ipv6In(2, 1)) {
		t.Error("a host of another /64 was refused because of the first prefix")
	}
}

func TestLimiter_IPv4MappedAddressSharesTheIPv4Budget(t *testing.T) {
	clock := newFakeClock()
	l := testLimiter(clock, 10, 1, 100)

	l.allow(netip.MustParseAddr("192.168.1.10"))
	if l.allow(netip.MustParseAddr("::ffff:192.168.1.10")) {
		t.Error("the IPv4-mapped form of a client got a second budget")
	}
}

func TestLimitKey(t *testing.T) {
	cases := []struct {
		name string
		in   netip.Addr
		want netip.Addr
	}{
		{"IPv4 is unchanged", netip.MustParseAddr("192.168.1.10"), netip.MustParseAddr("192.168.1.10")},
		{"IPv4-mapped becomes IPv4", netip.MustParseAddr("::ffff:192.168.1.10"), netip.MustParseAddr("192.168.1.10")},
		{"IPv6 keeps its first 64 bits", netip.MustParseAddr("2001:db8:1:2:3:4:5:6"), netip.MustParseAddr("2001:db8:1:2::")},
		{"IPv6 zone is dropped", netip.MustParseAddr("fe80::1%eth0"), netip.MustParseAddr("fe80::")},
		{"invalid address stays invalid", netip.Addr{}, netip.Addr{}},
	}
	for _, tc := range cases {
		if got := limitKey(tc.in); got != tc.want {
			t.Errorf("%s: limitKey(%v) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
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

func TestLimiter_DefaultsAbsorbABrowserAdblockTestBurst(t *testing.T) {
	clock := newFakeClock()
	l := newLimiterAt(rateLimit, rateBurst, maxTrackedClients, clock.Now)
	client := netip.MustParseAddr("192.168.1.150")

	const queries, window = 300, 500 * time.Millisecond
	dropped := 0
	for i := 0; i < queries; i++ {
		if !l.allow(client) {
			dropped++
		}
		clock.Advance(window / queries)
	}
	if dropped != 0 {
		t.Errorf("%d of %d queries sent within %v were dropped; adblock.turtlecute.org sent 255 in about that long from one phone", dropped, queries, window)
	}
}

func TestLimiter_DefaultsStillStopAFlood(t *testing.T) {
	clock := newFakeClock()
	l := newLimiterAt(rateLimit, rateBurst, maxTrackedClients, clock.Now)
	client := netip.MustParseAddr("192.168.1.150")

	const queries = 10000
	allowed := 0
	for i := 0; i < queries; i++ {
		if l.allow(client) {
			allowed++
		}
		clock.Advance(time.Second / queries)
	}
	if limit := int(rateBurst + rateLimit); allowed > limit {
		t.Errorf("%d of %d queries in one second were allowed, want at most %d", allowed, queries, limit)
	}
}
