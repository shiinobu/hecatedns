package native

import (
	"crypto/tls"
	"net"
	"net/netip"
	"testing"
)

func tcp6Available() bool {
	l, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

func wildcardFreeOn(port uint16) bool {
	var held []interface{ Close() error }
	defer func() {
		for _, c := range held {
			_ = c.Close()
		}
	}()
	for _, ip := range []string{"0.0.0.0", "::"} {
		addr := netip.AddrPortFrom(netip.MustParseAddr(ip), port).String()
		tcp, err := net.Listen("tcp"+familyOf(ip), addr)
		if err != nil {
			return false
		}
		held = append(held, tcp)
		udp, err := net.ListenPacket("udp"+familyOf(ip), addr)
		if err != nil {
			return false
		}
		held = append(held, udp)
	}
	return true
}

func familyOf(ip string) string {
	if ip == "::" {
		return "6"
	}
	return "4"
}

func freeWildcardPort(t *testing.T) uint16 {
	t.Helper()
	for range 50 {
		probe, err := net.Listen("tcp6", "[::]:0")
		if err != nil {
			t.Fatal(err)
		}
		port := uint16(probe.Addr().(*net.TCPAddr).Port)
		_ = probe.Close()
		if wildcardFreeOn(port) {
			return port
		}
	}
	t.Fatal("no port is free on every wildcard socket")
	return 0
}

func wildcardPair(port uint16) []netip.AddrPort {
	return []netip.AddrPort{
		netip.AddrPortFrom(netip.IPv4Unspecified(), port),
		netip.AddrPortFrom(netip.IPv6Unspecified(), port),
	}
}

func TestBindDoT_AWildcardPairOfBothFamiliesBindsTogether(t *testing.T) {
	if !tcp6Available() {
		t.Skip("this host has no IPv6")
	}
	listeners, err := bindDoT(wildcardPair(freeWildcardPort(t)), admitAll, &tls.Config{})
	if err != nil {
		t.Fatalf("0.0.0.0 and [::] on one port must bind together: %v", err)
	}
	for _, l := range listeners {
		_ = l.Close()
	}
	if len(listeners) != 2 {
		t.Fatalf("listeners = %d, want 2", len(listeners))
	}
}

func TestBind_AWildcardPairOfBothFamiliesBindsTogether(t *testing.T) {
	if !tcp6Available() {
		t.Skip("this host has no IPv6")
	}
	socks, err := bind(wildcardPair(freeWildcardPort(t)), admitAll)
	if err != nil {
		t.Fatalf("0.0.0.0 and [::] on one port must bind together: %v", err)
	}
	for _, s := range socks {
		_ = s.udp.Close()
		_ = s.tcp.Close()
	}
	if len(socks) != 2 {
		t.Fatalf("sockets = %d, want 2", len(socks))
	}
}

func TestBindDoT_AnIPv6WildcardDoesNotAnswerOnIPv4(t *testing.T) {
	if !tcp6Available() {
		t.Skip("this host has no IPv6")
	}
	port := freeWildcardPort(t)
	listeners, err := bindDoT([]netip.AddrPort{netip.AddrPortFrom(netip.IPv6Unspecified(), port)}, admitAll, &tls.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer listeners[0].Close()
	conn, err := net.Dial("tcp4", netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port).String())
	if err == nil {
		_ = conn.Close()
		t.Fatal("an IPv6-only listener must not accept IPv4 connections")
	}
}
