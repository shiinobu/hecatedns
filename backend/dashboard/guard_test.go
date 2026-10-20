package dashboard

import (
	"reflect"
	"testing"
)

func TestGuard_IsPublicSuffix(t *testing.T) {
	g := NewGuard()
	tests := map[string]bool{
		"com":             true,
		"co.id":           true,
		"my.id":           true,
		"example.com":     false,
		"example.co.id":   false,
		"hecate.my.id":    false,
		"EXAMPLE.COM.":    false,
		"CO.ID.":          true,
		"a.b.example.com": false,
	}
	for name, want := range tests {
		if got := g.IsPublicSuffix(name); got != want {
			t.Errorf("IsPublicSuffix(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestGuard_IsProtected(t *testing.T) {
	g := NewGuard("Q3W.ghost.example.org", "ghost.example.org.", "dns.google")
	tests := map[string]bool{
		"q3w.ghost.example.org":   true,
		"ghost.example.org":       true,
		"example.org":             true,
		"org":                     true,
		"DNS.GOOGLE":              true,
		"google":                  true,
		"other.example.org":       false,
		"x.q3w.ghost.example.org": false,
		"google.com":              false,
		"ads.example.com":         false,
	}
	for name, want := range tests {
		if got := g.IsProtected(name); got != want {
			t.Errorf("IsProtected(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestGuard_Parents(t *testing.T) {
	g := NewGuard("ghost.example.org")
	tests := []struct {
		name string
		want []string
	}{
		{"a.b.example.co.id", []string{"b.example.co.id", "example.co.id"}},
		{"ads.example.com", []string{"example.com"}},
		{"example.com", nil},
		{"com", nil},
		{"localhost", nil},
		{"x.ghost.example.org", nil},
		{"x.other.example.org", []string{"other.example.org"}},
		{"A.Example.COM.", []string{"example.com"}},
	}
	for _, tt := range tests {
		if got := g.Parents(tt.name); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Parents(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
