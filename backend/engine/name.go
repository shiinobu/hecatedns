package engine

import (
	"errors"
	"net/netip"
	"strings"
)

// Limits of a DNS name (RFC 1035).
const (
	maxNameLen  = 253
	maxLabelLen = 63

	// maxIPTextLen bounds the text we hand to the IP parser, so a huge line is
	// never parsed as an address. It is longer than any textual IPv6 address
	// with a zone.
	maxIPTextLen = 64

	wildcardPrefix = "*."
)

// Reasons ParseEntry rejects an entry. The messages never echo the input, so a
// hostile or huge line cannot flood a log.
var (
	ErrEmptyEntry  = errors.New("entry is empty")
	ErrIPAddress   = errors.New("entry is an IP address, not a domain")
	ErrNameTooLong = errors.New("entry is longer than 253 bytes")
	ErrSingleLabel = errors.New("entry has a single label, it would block a whole TLD")
	ErrBadLabel    = errors.New("entry has an empty label, a label over 63 bytes or a character outside a-z 0-9 - _")
)

// NormalizeName returns the canonical form used for matching: ASCII
// lower-case and without the trailing dot of a fully qualified name. Only
// ASCII is folded on purpose, because Unicode folding would map characters
// such as U+212A (Kelvin sign) onto ASCII letters.
func NormalizeName(name string) string {
	return strings.TrimSuffix(asciiLower(name), ".")
}

// asciiLower lower-cases the ASCII letters of s and returns s itself, without
// allocating, when there is nothing to change.
func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			return lowerFrom(s, i)
		}
	}
	return s
}

// lowerFrom lower-cases s knowing that s[:first] has no upper-case letter.
func lowerFrom(s string, first int) string {
	b := []byte(s)
	for i := first; i < len(b); i++ {
		if c := b[i]; c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// ParseEntry validates one blocklist or rule domain and returns it
// normalized. A leading "*." is removed. It rejects IP addresses and
// single-label names, so one broken line such as "com" cannot block a whole
// TLD, and any name that is not a syntactically valid host name.
func ParseEntry(raw string) (string, error) {
	entry := strings.TrimSpace(raw)
	entry = NormalizeName(strings.TrimPrefix(entry, wildcardPrefix))
	switch {
	case entry == "":
		return "", ErrEmptyEntry
	case len(entry) <= maxIPTextLen && isIPAddress(entry):
		return "", ErrIPAddress
	case len(entry) > maxNameLen:
		return "", ErrNameTooLong
	case !strings.Contains(entry, "."):
		return "", ErrSingleLabel
	case !validLabels(entry):
		return "", ErrBadLabel
	}
	return entry, nil
}

// isIPAddress reports whether s is a textual IPv4 or IPv6 address.
func isIPAddress(s string) bool {
	_, err := netip.ParseAddr(s)
	return err == nil
}

// validLabels reports whether every dot-separated label of name is 1-63 bytes
// long and made of a-z, 0-9, '-' or '_'. name must already be lower-case.
func validLabels(name string) bool {
	labelLen := 0
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '.':
			if labelLen == 0 {
				return false
			}
			labelLen = 0
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
			labelLen++
			if labelLen > maxLabelLen {
				return false
			}
		default:
			return false
		}
	}
	return labelLen > 0
}
