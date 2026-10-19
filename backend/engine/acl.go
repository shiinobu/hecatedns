package engine

import (
	"errors"
	"fmt"
	"net/netip"
)

const mappedPrefixBits = 96

type ACL struct {
	prefixes []netip.Prefix
}

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

func canonicalPrefix(p netip.Prefix) netip.Prefix {
	if p.Addr().Is4In6() && p.Bits() >= mappedPrefixBits {
		p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-mappedPrefixBits)
	}
	return p.Masked()
}

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
