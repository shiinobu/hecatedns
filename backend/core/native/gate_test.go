package native

import (
	"net/netip"
	"testing"
)

func TestGate_HostsInOneIPv6PrefixShareTheClientCap(t *testing.T) {
	g := newGate()
	g.maxPerClient = 2
	first, second, third := ipv6In(1, 1), ipv6In(1, 2), ipv6In(1, 3)

	if !g.acquire(first) || !g.acquire(second) {
		t.Fatal("the first two slots of a /64 were refused")
	}
	if g.acquire(third) {
		t.Error("a third host of the same /64 got a slot, want the per-client cap shared by the prefix")
	}

	g.release(first)
	g.release(second)
	if g.total != 0 || len(g.perClient) != 0 {
		t.Errorf("gate holds %d slots for %d clients after every release, want none", g.total, len(g.perClient))
	}
}

func TestGate_DifferentIPv6PrefixesHaveTheirOwnCap(t *testing.T) {
	g := newGate()
	g.maxPerClient = 1

	if !g.acquire(ipv6In(1, 1)) {
		t.Fatal("the first slot was refused")
	}
	if !g.acquire(ipv6In(2, 1)) {
		t.Error("a host of another /64 was refused because of the first prefix")
	}
}

func TestGate_IPv4MappedAddressSharesTheIPv4Cap(t *testing.T) {
	g := newGate()
	g.maxPerClient = 1

	if !g.acquire(netip.MustParseAddr("192.168.1.10")) {
		t.Fatal("the first slot was refused")
	}
	if g.acquire(netip.MustParseAddr("::ffff:192.168.1.10")) {
		t.Error("the IPv4-mapped form of a client got a second slot")
	}
}
