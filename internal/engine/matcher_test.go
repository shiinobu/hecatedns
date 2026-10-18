package engine

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func mustSet(t *testing.T, entries ...string) *DomainSet {
	t.Helper()
	set, rejected := NewDomainSet(entries)
	if rejected != 0 {
		t.Fatalf("NewDomainSet(%q) rejected %d entries", entries, rejected)
	}
	return set
}

func TestDomainSet_Match(t *testing.T) {
	set := mustSet(t, "example.com", "ads.tracker.net", "co.id")
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"exact", "example.com", true},
		{"subdomain", "www.example.com", true},
		{"deeper subdomain", "a.b.c.example.com", true},
		{"uppercase", "WWW.Example.COM", true},
		{"trailing dot", "example.com.", true},
		{"similar suffix is not a subdomain", "notexample.com", false},
		{"entry in the middle of the name", "example.com.evil.net", false},
		{"label in the middle", "example.evil.com", false},
		{"parent of an entry", "tracker.net", false},
		{"sibling of an entry", "other.tracker.net", false},
		{"entry deeper than the name", "tracker.net", false},
		{"multi label entry exact", "ads.tracker.net", true},
		{"multi label entry subdomain", "x.ads.tracker.net", true},
		{"registry-style entry", "shop.co.id", true},
		{"bare tld of an entry", "id", false},
		{"unrelated", "example.org", false},
		{"empty query", "", false},
		{"root query", ".", false},
		{"single label query", "com", false},
		{"name over 253 bytes", strings.Repeat("a.", 130) + "example.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := set.Match(tt.query); got != tt.want {
				t.Errorf("Match(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestDomainSet_LookupReturnsTheMatchingEntry(t *testing.T) {
	set := mustSet(t, "example.com", "ads.tracker.net")
	tests := []struct {
		query string
		want  string
		ok    bool
	}{
		{"www.example.com", "example.com", true},
		{"example.com", "example.com", true},
		{"x.y.ads.tracker.net", "ads.tracker.net", true},
		{"tracker.net", "", false},
	}
	for _, tt := range tests {
		got, ok := set.Lookup(tt.query)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Lookup(%q) = %q, %v; want %q, %v", tt.query, got, ok, tt.want, tt.ok)
		}
	}
}

func TestDomainSet_LookupPrefersTheMostSpecificEntry(t *testing.T) {
	set := mustSet(t, "example.com", "ads.example.com")
	if got, _ := set.Lookup("x.ads.example.com"); got != "ads.example.com" {
		t.Errorf("Lookup returned %q, want the most specific entry ads.example.com", got)
	}
}

func TestNewDomainSet_RejectsBadEntriesAndCountsThem(t *testing.T) {
	entries := []string{"example.com", "com", "192.168.0.1", "", "Good.NET", "bad entry.org", "*.wild.example"}
	set, rejected := NewDomainSet(entries)
	if rejected != 4 {
		t.Errorf("rejected = %d, want 4 (com, ip, empty, bad entry.org)", rejected)
	}
	if set.Len() != 3 {
		t.Errorf("Len() = %d, want 3", set.Len())
	}
	for _, query := range []string{"example.com", "good.net", "x.wild.example"} {
		if !set.Match(query) {
			t.Errorf("Match(%q) = false, want true", query)
		}
	}
	if set.Match("anything.com") {
		t.Error("the rejected single-label entry com must not block the whole TLD")
	}
}

func TestNewDomainSet_DuplicatesCountOnce(t *testing.T) {
	set, rejected := NewDomainSet([]string{"example.com", "EXAMPLE.com.", "example.com"})
	if rejected != 0 || set.Len() != 1 {
		t.Errorf("got Len() = %d, rejected = %d; want 1 and 0", set.Len(), rejected)
	}
}

func TestDomainSet_EmptyAndNilSetsMatchNothing(t *testing.T) {
	var nilSet *DomainSet
	empty, rejected := NewDomainSet(nil)
	if rejected != 0 {
		t.Fatalf("rejected = %d, want 0", rejected)
	}
	for name, set := range map[string]*DomainSet{"nil": nilSet, "empty": empty} {
		if set.Match("example.com") {
			t.Errorf("%s set matched example.com", name)
		}
		if got, ok := set.Lookup("example.com"); got != "" || ok {
			t.Errorf("%s set Lookup = %q, %v; want empty and false", name, got, ok)
		}
		if set.Len() != 0 {
			t.Errorf("%s set Len() = %d, want 0", name, set.Len())
		}
	}
}

func TestDomainSet_DoesNotShareStorageWithItsInput(t *testing.T) {
	entries := []string{"example.com"}
	set, _ := NewDomainSet(entries)
	entries[0] = "other.org"
	if !set.Match("example.com") || set.Match("other.org") {
		t.Error("the set changed after the caller modified the input slice")
	}
}

func TestMatcher_StartsEmptyAndSwaps(t *testing.T) {
	var m Matcher
	if m.Match("example.com") {
		t.Fatal("zero Matcher matched a name")
	}
	m.Swap(mustSet(t, "example.com"))
	if !m.Match("www.example.com") {
		t.Error("Matcher did not match after Swap")
	}
	if got, ok := m.Lookup("www.example.com"); got != "example.com" || !ok {
		t.Errorf("Lookup = %q, %v; want example.com, true", got, ok)
	}
	m.Swap(nil)
	if m.Match("example.com") {
		t.Error("Matcher still matched after Swap(nil)")
	}
}

func TestMatcher_SwapWhileReading(t *testing.T) {
	var m Matcher
	m.Swap(mustSet(t, "a.com"))
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if !m.Match("x.a.com") {
						t.Error("a.com disappeared during a swap")
						return
					}
				}
			}
		}()
	}
	for i := 0; i < 500; i++ {
		m.Swap(mustSet(t, "a.com", fmt.Sprintf("b%d.com", i)))
	}
	close(stop)
	readers.Wait()
}

func BenchmarkDomainSet_Match(b *testing.B) {
	const size = 20000
	entries := make([]string, size)
	for i := range entries {
		entries[i] = fmt.Sprintf("host%d.tracker%d.example.com", i, i%97)
	}
	set, _ := NewDomainSet(entries)
	queries := []string{"host5.tracker5.example.com", "deep.sub.host9.tracker9.example.com", "www.not-blocked.org"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = set.Match(queries[i%len(queries)])
	}
}
