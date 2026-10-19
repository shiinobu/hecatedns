package engine

import (
	"strings"
	"sync/atomic"
)

type DomainSet struct {
	entries map[string]struct{}
}

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

type DomainSetBuilder struct {
	entries map[string]struct{}
}

func NewDomainSetBuilder(capacity int) *DomainSetBuilder {
	return &DomainSetBuilder{entries: make(map[string]struct{}, capacity)}
}

func (b *DomainSetBuilder) Has(entry string) bool {
	_, ok := b.entries[entry]
	return ok
}

func (b *DomainSetBuilder) Len() int {
	return len(b.entries)
}

func (b *DomainSetBuilder) AddNormalized(entry string) int {
	b.entries[entry] = struct{}{}
	return len(b.entries)
}

func (b *DomainSetBuilder) Build() *DomainSet {
	entries := b.entries
	b.entries = nil
	return &DomainSet{entries: entries}
}

func (s *DomainSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.entries)
}

func (s *DomainSet) Find(match func(entry string) bool) (string, bool) {
	if s.Len() == 0 || match == nil {
		return "", false
	}
	var best string
	for entry := range s.entries {
		if best != "" && entry >= best {
			continue
		}
		if match(entry) {
			best = entry
		}
	}
	return best, best != ""
}

func (s *DomainSet) Match(name string) bool {
	_, ok := s.Lookup(name)
	return ok
}

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

type Matcher struct {
	current atomic.Pointer[DomainSet]
}

func (m *Matcher) Swap(set *DomainSet) {
	m.current.Store(set)
}

func (m *Matcher) Load() *DomainSet {
	return m.current.Load()
}

func (m *Matcher) Match(name string) bool {
	return m.Load().Match(name)
}

func (m *Matcher) Lookup(name string) (entry string, ok bool) {
	return m.Load().Lookup(name)
}
