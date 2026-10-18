package engine

import (
	"errors"
	"fmt"
	"net/netip"
)

// mappedPrefixBits is the length of the ::ffff:0:0/96 prefix that marks an
// IPv4-mapped IPv6 address.
const mappedPrefixBits = 96

// ACL is the client allow list (allowed_clients). It is immutable and safe for
// concurrent use. It fails closed: a nil or empty ACL allows nobody, and so
// does an invalid client address.
type ACL struct {
	prefixes []netip.Prefix
}

// NewACL parses CIDR strings. An empty list is an error, because "no entries"
// must never be read as "everybody"; to allow everyone, list 0.0.0.0/0 and
// ::/0. Every bad entry is reported, not just the first. An IPv4-mapped IPv6
// prefix (::ffff:a.b.c.d/n with n >= 96) is stored as the IPv4 prefix it
// stands for, because clients are matched in their unmapped form.
func NewACL(cidrs []string) (*ACL, error) {
	if len(cidrs) == 0 {
		return nil, errors.New("allowed_clients: at least one CIDR is required, use 0.0.0.0/0 and ::/0 to allow everyone")
	}
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	var errs []error
	for i, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			errs = append(errs, fmt.Errorf("allowed_clients[%d]: %q is not a CIDR: %w", i, cidr, err))
			continue
		}
		prefixes = append(prefixes, canonicalPrefix(prefix))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &ACL{prefixes: prefixes}, nil
}

// canonicalPrefix clears the host bits and converts a mapped IPv6 prefix to
// IPv4.
func canonicalPrefix(p netip.Prefix) netip.Prefix {
	if p.Addr().Is4In6() && p.Bits() >= mappedPrefixBits {
		p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-mappedPrefixBits)
	}
	return p.Masked()
}

// Allows reports whether client may use the server. The address is unmapped
// (::ffff:10.0.0.1 is 10.0.0.1) and its IPv6 zone is dropped before matching.
func (a *ACL) Allows(client netip.Addr) bool {
	if a == nil || !client.IsValid() {
		return false
	}
	client = client.Unmap().WithZone("")
	for _, prefix := range a.prefixes {
		if prefix.Contains(client) {
			return true
		}
	}
	return false
}
