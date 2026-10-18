package engine

import (
	"strings"
	"sync/atomic"
)

// DomainSet is an immutable set of domains with suffix matching: an entry
// matches itself and every subdomain of it, on label boundaries. It is built
// once by NewDomainSet and never modified afterwards, so any number of
// goroutines may read it. A nil *DomainSet is a valid empty set.
type DomainSet struct {
	entries map[string]struct{}
}

// NewDomainSet builds a set from raw entries. Each entry goes through
// ParseEntry; the ones it rejects are skipped and counted in rejected, they
// never stop the build. Duplicates collapse. The input slice is not retained.
func NewDomainSet(raw []string) (set *DomainSet, rejected int) {
	entries := make(map[string]struct{}, len(raw))
	for _, r := range raw {
		entry, err := ParseEntry(r)
		if err != nil {
			rejected++
			continue
		}
		entries[entry] = struct{}{}
	}
	return &DomainSet{entries: entries}, rejected
}

// Len returns the number of distinct entries.
func (s *DomainSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.entries)
}

// Match reports whether name equals an entry or is a subdomain of one.
func (s *DomainSet) Match(name string) bool {
	_, ok := s.Lookup(name)
	return ok
}

// Lookup returns the most specific entry that matches name. The name is
// normalized first (ASCII lower-case, no trailing dot). The walk goes from the
// full name up to the registrable part and never tests the bare last label, so
// an entry can never match a whole TLD.
func (s *DomainSet) Lookup(name string) (entry string, ok bool) {
	if s.Len() == 0 {
		return "", false
	}
	name = NormalizeName(name)
	if len(name) > maxNameLen {
		return "", false
	}
	for {
		dot := strings.IndexByte(name, '.')
		if dot < 0 {
			return "", false
		}
		if _, found := s.entries[name]; found {
			return name, true
		}
		name = name[dot+1:]
	}
}

// Matcher holds the current DomainSet and replaces it atomically. Readers
// never block and always see a complete set, the old one or the new one. The
// zero value is an empty matcher.
type Matcher struct {
	current atomic.Pointer[DomainSet]
}

// Swap publishes set as the current snapshot. The set must not be modified
// afterwards (DomainSet has no way to do so).
func (m *Matcher) Swap(set *DomainSet) {
	m.current.Store(set)
}

// Load returns the current snapshot, possibly nil.
func (m *Matcher) Load() *DomainSet {
	return m.current.Load()
}

// Match reports whether the current snapshot matches name.
func (m *Matcher) Match(name string) bool {
	return m.Load().Match(name)
}

// Lookup returns the most specific entry of the current snapshot matching name.
func (m *Matcher) Lookup(name string) (entry string, ok bool) {
	return m.Load().Lookup(name)
}
