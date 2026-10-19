package engine

import (
	"errors"
	"net/netip"
	"strings"
)

const (
	maxNameLen  = 253
	maxLabelLen = 63

	maxIPTextLen = 64

	wildcardPrefix = "*."
)

var (
	ErrEmptyEntry  = errors.New("entry is empty")
	ErrIPAddress   = errors.New("entry is an IP address, not a domain")
	ErrNameTooLong = errors.New("entry is longer than 253 bytes")
	ErrSingleLabel = errors.New("entry has a single label, it would block a whole TLD")
	ErrBadLabel    = errors.New("entry has an empty label, a label over 63 bytes or a character outside a-z 0-9 - _")
)

func NormalizeName(name string) string {
	return strings.TrimSuffix(asciiLower(name), ".")
}

func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			return lowerFrom(s, i)
		}
	}
	return s
}

func lowerFrom(s string, first int) string {
	b := []byte(s)
	for i := first; i < len(b); i++ {
		if c := b[i]; c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

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

func isIPAddress(s string) bool {
	_, err := netip.ParseAddr(s)
	return err == nil
}

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
