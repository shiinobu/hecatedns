package engine

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"already normal", "example.com", "example.com"},
		{"uppercase", "WWW.Example.COM", "www.example.com"},
		{"trailing dot", "example.com.", "example.com"},
		{"uppercase and trailing dot", "Example.COM.", "example.com"},
		{"root", ".", ""},
		{"empty", "", ""},
		{"only one trailing dot removed", "example.com..", "example.com."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeName(tt.in); got != tt.want {
				t.Errorf("NormalizeName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseEntry_Accepts(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "example.com", "example.com"},
		{"uppercase", "Ads.Example.COM", "ads.example.com"},
		{"trailing dot", "example.com.", "example.com"},
		{"wildcard prefix", "*.example.com", "example.com"},
		{"surrounding space", "  example.com\t", "example.com"},
		{"underscore label", "_dmarc.example.com", "_dmarc.example.com"},
		{"hyphen and digits", "a-1.b-2.example.co.id", "a-1.b-2.example.co.id"},
		{"punycode", "xn--mnchen-3ya.de", "xn--mnchen-3ya.de"},
		{"label of 63 chars", strings.Repeat("a", 63) + ".com", strings.Repeat("a", 63) + ".com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEntry(tt.in)
			if err != nil {
				t.Fatalf("ParseEntry(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseEntry(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseEntry_Rejects(t *testing.T) {
	longName := strings.Repeat("a.", 127) + "com"
	tests := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "", ErrEmptyEntry},
		{"blank", " \t ", ErrEmptyEntry},
		{"single label", "com", ErrSingleLabel},
		{"single label with dot", "com.", ErrSingleLabel},
		{"wildcard of a single label", "*.com", ErrSingleLabel},
		{"bare wildcard", "*", ErrSingleLabel},
		{"ipv4", "192.168.1.1", ErrIPAddress},
		{"ipv6", "2001:db8::1", ErrIPAddress},
		{"empty label", "a..example.com", ErrBadLabel},
		{"leading dot", ".example.com", ErrBadLabel},
		{"label over 63", strings.Repeat("a", 64) + ".com", ErrBadLabel},
		{"space inside", "exa mple.com", ErrBadLabel},
		{"slash", "example.com/path", ErrBadLabel},
		{"url", "https://example.com", ErrBadLabel},
		{"unicode", "münchen.de", ErrBadLabel},
		{"control character", "exa\x00mple.com", ErrBadLabel},
		{"invalid utf8", "ex\xff.com", ErrBadLabel},
		{"name over 253", longName + ".example", ErrNameTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEntry(tt.in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ParseEntry(%q) = %q, %v; want error %v", tt.in, got, err, tt.want)
			}
			if got != "" {
				t.Errorf("ParseEntry(%q) returned %q together with an error", tt.in, got)
			}
		})
	}
}

func TestParseEntry_ErrorDoesNotEchoHugeInput(t *testing.T) {
	huge := strings.Repeat("x", 1<<20) + ".com"
	_, err := ParseEntry(huge)
	if err == nil {
		t.Fatal("ParseEntry accepted a 1 MiB name")
	}
	if len(err.Error()) > 200 {
		t.Errorf("error message is %d bytes, it must not echo the input", len(err.Error()))
	}
}
