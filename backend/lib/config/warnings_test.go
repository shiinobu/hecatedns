package config

import (
	"strings"
	"testing"
)

func countContaining(warnings []string, part string) int {
	n := 0
	for _, w := range warnings {
		if strings.Contains(w, part) {
			n++
		}
	}
	return n
}

func TestWarnings_AShortDoTNameLabelIsFlaggedWithoutRepeatingTheName(t *testing.T) {
	tests := []struct {
		name  string
		names []string
		want  int
	}{
		{"label of 16 characters", []string{"k7x9p2qm4t8v3w6z.example.org"}, 0},
		{"label of 15 characters", []string{"k7x9p2qm4t8v3w6.example.org"}, 1},
		{"the base name itself", []string{"hecatedns.duckdns.org"}, 1},
		{"a long and a short name", []string{"k7x9p2qm4t8v3w6z.example.org", "ghost.example.org"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dotConfig()
			c.DoT.Names = tt.names

			warnings := c.Warnings()

			if got := countContaining(warnings, "dot.names["); got != tt.want {
				t.Errorf("name warnings = %d (%v), want %d", got, warnings, tt.want)
			}
			for _, w := range warnings {
				for _, name := range tt.names {
					if strings.Contains(w, name) {
						t.Errorf("a warning repeats a name: %q", w)
					}
				}
			}
		})
	}
}

func TestWarnings_ADisabledDoTSectionIsNotChecked(t *testing.T) {
	c := Default()
	c.DoT = DoT{Enabled: false, Names: []string{"ghost.example.org"}}

	if got := countContaining(c.Warnings(), "dot.names["); got != 0 {
		t.Errorf("a disabled dot section produced %d name warnings", got)
	}
}

func TestWarnings_PlainDNSOpenToTheWorldOnAPublicAddressIsFlagged(t *testing.T) {
	tests := []struct {
		name    string
		listen  []string
		allowed []string
		want    int
	}{
		{"everyone allowed on a public address", []string{"203.0.113.7:53"}, []string{"0.0.0.0/0"}, 1},
		{"IPv6 everyone on a wildcard address", []string{"[::]:53"}, []string{"::/0"}, 1},
		{"everyone allowed on loopback only", []string{"127.0.0.1:5300"}, []string{"0.0.0.0/0"}, 0},
		{"a private range on a public address", []string{"203.0.113.7:53"}, []string{"192.168.0.0/16"}, 0},
		{"everyone allowed but no plain listener", nil, []string{"0.0.0.0/0", "::/0"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Default()
			c.Listen, c.AllowedClients = tt.listen, tt.allowed

			if got := countContaining(c.Warnings(), "open resolver"); got != tt.want {
				t.Errorf("open resolver warnings = %d, want %d: %v", got, tt.want, c.Warnings())
			}
		})
	}
}

func TestWarnings_AnIPv6WildcardWithoutAnIPv4OneIsFlagged(t *testing.T) {
	tests := []struct {
		name   string
		listen []string
		want   int
	}{
		{"both wildcards", []string{"0.0.0.0:853", "[::]:853"}, 0},
		{"only the IPv4 wildcard", []string{"0.0.0.0:853"}, 0},
		{"only the IPv6 wildcard", []string{"[::]:853"}, 1},
		{"IPv6 wildcard with an IPv4 wildcard on another port", []string{"[::]:853", "0.0.0.0:8853"}, 1},
		{"a specific IPv6 address", []string{"[2001:db8::1]:853"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dotConfig()
			c.DoT.Listen = tt.listen

			if got := countContaining(c.Warnings(), "IPv6 only"); got != tt.want {
				t.Errorf("IPv6-only warnings = %d, want %d: %v", got, tt.want, c.Warnings())
			}
		})
	}
}
