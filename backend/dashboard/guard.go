package dashboard

import (
	"strings"

	"golang.org/x/net/publicsuffix"
)

type Guard struct {
	protected []string
	tracked   func() []string
}

func (g *Guard) Track(source func() []string) {
	g.tracked = source
}

func NewGuard(protected ...string) *Guard {
	g := &Guard{}
	seen := make(map[string]bool, len(protected))
	for _, name := range protected {
		name = canonicalName(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		g.protected = append(g.protected, name)
	}
	return g
}

func canonicalName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

func (g *Guard) IsPublicSuffix(name string) bool {
	name = canonicalName(name)
	if name == "" {
		return false
	}
	suffix, _ := publicsuffix.PublicSuffix(name)
	return suffix == name
}

func (g *Guard) IsProtected(name string) bool {
	name = canonicalName(name)
	if name == "" {
		return false
	}
	if covers(g.protected, name) {
		return true
	}
	if g.tracked == nil {
		return false
	}
	tracked := g.tracked()
	canonical := make([]string, len(tracked))
	for i, p := range tracked {
		canonical[i] = canonicalName(p)
	}
	return covers(canonical, name)
}

func covers(protected []string, name string) bool {
	for _, p := range protected {
		if p == name || strings.HasSuffix(p, "."+name) {
			return true
		}
	}
	return false
}

func (g *Guard) Parents(name string) []string {
	name = canonicalName(name)
	var parents []string
	for {
		_, rest, found := strings.Cut(name, ".")
		if !found || rest == "" {
			return parents
		}
		name = rest
		if g.IsPublicSuffix(name) || g.IsProtected(name) {
			return parents
		}
		parents = append(parents, name)
	}
}
