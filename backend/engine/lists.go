package engine

import "slices"

type NamedSet struct {
	Name string
	Set  *DomainSet
}

type ListSet struct {
	allow []NamedSet
	block []NamedSet
}

func NewListSet(allow, block []NamedSet) *ListSet {
	return &ListSet{allow: slices.Clone(allow), block: slices.Clone(block)}
}

func (l *ListSet) LookupAllow(name string) (list string, ok bool) {
	if l == nil {
		return "", false
	}
	return firstMatch(l.allow, name)
}

func (l *ListSet) LookupBlock(name string) (list string, ok bool) {
	if l == nil {
		return "", false
	}
	return firstMatch(l.block, name)
}

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

func firstMatch(lists []NamedSet, name string) (string, bool) {
	for _, list := range lists {
		if list.Set.Match(name) {
			return list.Name, true
		}
	}
	return "", false
}
