package engine

import (
	"net/netip"
	"strings"
	"testing"

	"xdns/internal/config"
)

func mustACL(t *testing.T, cidrs ...string) *ACL {
	t.Helper()
	acl, err := NewACL(cidrs)
	if err != nil {
		t.Fatalf("NewACL(%q): %v", cidrs, err)
	}
	return acl
}

func TestACL_Allows(t *testing.T) {
	tests := []struct {
		name   string
		cidrs  []string
		client string
		want   bool
	}{
		{"ipv4 first address", []string{"192.168.1.0/24"}, "192.168.1.0", true},
		{"ipv4 last address", []string{"192.168.1.0/24"}, "192.168.1.255", true},
		{"ipv4 just below the range", []string{"192.168.1.0/24"}, "192.168.0.255", false},
		{"ipv4 just above the range", []string{"192.168.1.0/24"}, "192.168.2.0", false},
		{"ipv6 first address", []string{"2001:db8::/32"}, "2001:db8::", true},
		{"ipv6 last address", []string{"2001:db8::/32"}, "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff", true},
		{"ipv6 just below the range", []string{"2001:db8::/32"}, "2001:db7:ffff:ffff:ffff:ffff:ffff:ffff", false},
		{"ipv6 just above the range", []string{"2001:db8::/32"}, "2001:db9::", false},
		{"v4 client against a v6-only list", []string{"2001:db8::/32"}, "192.168.1.5", false},
		{"v6 client against a v4-only list", []string{"192.168.0.0/16"}, "2001:db8::1", false},
		{"ipv4-mapped client matches the v4 range", []string{"192.168.1.0/24"}, "::ffff:192.168.1.5", true},
		{"ipv4-mapped client outside the v4 range", []string{"10.0.0.0/8"}, "::ffff:192.168.1.5", false},
		{"ipv4-mapped client at the range edge", []string{"10.0.0.0/8"}, "::ffff:11.0.0.0", false},
		{"single host prefix", []string{"10.1.2.3/32"}, "10.1.2.3", true},
		{"single host prefix, neighbour", []string{"10.1.2.3/32"}, "10.1.2.4", false},
		{"prefix written with host bits set", []string{"192.168.1.77/24"}, "192.168.1.200", true},
		{"zone id is ignored", []string{"fe80::/10"}, "fe80::1%eth0", true},
		{"second prefix in the list", []string{"10.0.0.0/8", "192.168.0.0/16"}, "192.168.9.9", true},
		{"mapped prefix is a v4 prefix", []string{"::ffff:10.0.0.0/104"}, "10.1.1.1", true},
		{"mapped prefix, outside", []string{"::ffff:10.0.0.0/104"}, "11.0.0.1", false},
		{"allow-all v4", []string{"0.0.0.0/0", "::/0"}, "8.8.8.8", true},
		{"allow-all v6", []string{"0.0.0.0/0", "::/0"}, "2606:4700::1111", true},
		{"allow-all mapped", []string{"0.0.0.0/0", "::/0"}, "::ffff:8.8.8.8", true},
		{"v4 allow-all alone does not allow v6", []string{"0.0.0.0/0"}, "2606:4700::1111", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acl := mustACL(t, tt.cidrs...)
			if got := acl.Allows(netip.MustParseAddr(tt.client)); got != tt.want {
				t.Errorf("Allows(%s) with %q = %v, want %v", tt.client, tt.cidrs, got, tt.want)
			}
		})
	}
}

func TestACL_DefaultConfigRanges(t *testing.T) {
	acl := mustACL(t, config.Default().AllowedClients...)
	tests := []struct {
		client string
		want   bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"::2", false},
		{"10.255.255.255", true},
		{"172.15.255.255", false},
		{"172.16.0.0", true},
		{"172.31.255.255", true},
		{"172.32.0.0", false},
		{"192.168.1.23", true},
		{"192.169.0.1", false},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
		{"::ffff:8.8.8.8", false},
		{"::ffff:192.168.1.23", true},
	}
	for _, tt := range tests {
		if got := acl.Allows(netip.MustParseAddr(tt.client)); got != tt.want {
			t.Errorf("Allows(%s) = %v, want %v", tt.client, got, tt.want)
		}
	}
}

func TestACL_InvalidClientAddressIsRefused(t *testing.T) {
	acl := mustACL(t, "0.0.0.0/0", "::/0")
	if acl.Allows(netip.Addr{}) {
		t.Error("the zero netip.Addr was allowed, even by an allow-all list")
	}
}

func TestACL_NilFailsClosed(t *testing.T) {
	var acl *ACL
	if acl.Allows(netip.MustParseAddr("127.0.0.1")) {
		t.Error("a nil ACL allowed a client")
	}
	if (&ACL{}).Allows(netip.MustParseAddr("127.0.0.1")) {
		t.Error("an empty ACL allowed a client")
	}
}

func TestNewACL_RejectsAnEmptyList(t *testing.T) {
	for _, cidrs := range [][]string{nil, {}} {
		if _, err := NewACL(cidrs); err == nil {
			t.Errorf("NewACL(%q) accepted an empty list, which would be read as allow-nobody or allow-all", cidrs)
		}
	}
}

func TestNewACL_ReportsEveryBadEntry(t *testing.T) {
	_, err := NewACL([]string{"10.0.0.0/8", "garbage", "192.168.1.1", "10.0.0.0/33", "300.1.1.1/8", "::1/129"})
	if err == nil {
		t.Fatal("NewACL accepted bad entries")
	}
	for _, want := range []string{"[1]", "[2]", "[3]", "[4]", "[5]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention entry %s:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "[0]") {
		t.Errorf("error blames the valid entry [0]:\n%v", err)
	}
}

func TestNewACL_DoesNotShareStorageWithItsInput(t *testing.T) {
	cidrs := []string{"10.0.0.0/8"}
	acl := mustACL(t, cidrs...)
	cidrs[0] = "0.0.0.0/0"
	if acl.Allows(netip.MustParseAddr("8.8.8.8")) {
		t.Error("the ACL changed after the caller modified the input slice")
	}
}

func FuzzACLAllows(f *testing.F) {
	acl, err := NewACL(config.Default().AllowedClients)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{"127.0.0.1", "::1", "::ffff:10.0.0.1", "fe80::1%eth0", "8.8.8.8", "0.0.0.0", "::", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		addr, err := netip.ParseAddr(text)
		if err != nil {
			return
		}
		_ = acl.Allows(addr)
	})
}

func TestNewACL_MappedPrefixBoundary(t *testing.T) {
	tests := []struct {
		name   string
		cidr   string
		client string
		want   bool
	}{
		{"/96 maps the whole IPv4 space", "::ffff:0:0/96", "8.8.8.8", true},
		{"/96 matches the mapped form too", "::ffff:0:0/96", "::ffff:8.8.8.8", true},
		{"/97 maps to a /1", "::ffff:0.0.0.0/97", "10.0.0.1", true},
		{"/97 maps to a /1, upper half outside", "::ffff:0.0.0.0/97", "200.0.0.1", false},
		{"/95 stays an IPv6 prefix and cannot match IPv4", "::fffe:0:0/95", "8.8.8.8", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mustACL(t, tt.cidr).Allows(netip.MustParseAddr(tt.client)); got != tt.want {
				t.Errorf("Allows(%s) with %s = %v, want %v", tt.client, tt.cidr, got, tt.want)
			}
		})
	}
}
