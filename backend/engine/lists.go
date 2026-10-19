package engine

import "slices"

// NamedSet is one loaded list: its configured name and its domains.
type NamedSet struct {
	Name string
	Set  *DomainSet
}

// ListSet is an immutable snapshot of all loaded lists, split by kind. The
// lists package builds one after every download and publishes it with
// Engine.SwapLists, so a rules change never rebuilds the large list sets. A nil
// *ListSet is a valid empty snapshot.
type ListSet struct {
	allow []NamedSet
	block []NamedSet
}

// NewListSet returns a snapshot of the given allow-kind and block-kind lists.
// The order is kept: when several lists match a name, the first one is
// reported. The slices are copied, so the caller may reuse them; the
// DomainSets themselves are immutable and shared.
func NewListSet(allow, block []NamedSet) *ListSet {
	return &ListSet{allow: slices.Clone(allow), block: slices.Clone(block)}
}

// LookupAllow returns the name of the first allow-kind list that matches name.
func (l *ListSet) LookupAllow(name string) (list string, ok bool) {
	if l == nil {
		return "", false
	}
	return firstMatch(l.allow, name)
}

// LookupBlock returns the name of the first block-kind list that matches name.
func (l *ListSet) LookupBlock(name string) (list string, ok bool) {
	if l == nil {
		return "", false
	}
	return firstMatch(l.block, name)
}

// HasBlock reports whether at least one block-kind list with at least one
// entry is loaded. An empty list does not count: it blocks nothing, which is
// exactly the situation the "no block list loaded" warning is about.
func (l *ListSet) HasBlock() bool {
	if l == nil {
		return false
	}
	for _, list := range l.block {
		if list.Set.Len() > 0 {
			return true
		}
	}
	return false
}

// firstMatch returns the name of the first list in lists whose set matches
// name.
func firstMatch(lists []NamedSet, name string) (string, bool) {
	for _, list := range lists {
		if list.Set.Match(name) {
			return list.Name, true
		}
	}
	return "", false
}
