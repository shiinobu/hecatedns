package engine

import "testing"

func TestListSetLookupReturnsFirstMatchingList(t *testing.T) {
	lists := NewListSet(
		[]NamedSet{
			{Name: "allow-a", Set: mustSet(t, "keep.example")},
			{Name: "allow-b", Set: mustSet(t, "keep.example", "other.example")},
		},
		[]NamedSet{
			{Name: "ads", Set: mustSet(t, "ads.example")},
			{Name: "trackers", Set: mustSet(t, "ads.example", "trk.example")},
		},
	)
	tests := []struct {
		name     string
		lookup   func(string) (string, bool)
		query    string
		wantList string
		wantOK   bool
	}{
		{"allow first list wins", lists.LookupAllow, "www.keep.example", "allow-a", true},
		{"allow second list", lists.LookupAllow, "other.example", "allow-b", true},
		{"allow no match", lists.LookupAllow, "ads.example", "", false},
		{"block first list wins", lists.LookupBlock, "x.ads.example", "ads", true},
		{"block second list", lists.LookupBlock, "TRK.example.", "trackers", true},
		{"block no match", lists.LookupBlock, "keep.example", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list, ok := tt.lookup(tt.query)
			if list != tt.wantList || ok != tt.wantOK {
				t.Fatalf("lookup(%q) = %q, %v; want %q, %v", tt.query, list, ok, tt.wantList, tt.wantOK)
			}
		})
	}
}

func TestListSetNilIsEmpty(t *testing.T) {
	var lists *ListSet
	if list, ok := lists.LookupAllow("a.example"); ok || list != "" {
		t.Fatalf("nil LookupAllow = %q, %v; want no match", list, ok)
	}
	if list, ok := lists.LookupBlock("a.example"); ok || list != "" {
		t.Fatalf("nil LookupBlock = %q, %v; want no match", list, ok)
	}
	if lists.HasBlock() {
		t.Fatal("nil HasBlock = true, want false")
	}
}

func TestListSetHasBlock(t *testing.T) {
	tests := []struct {
		name  string
		allow []NamedSet
		block []NamedSet
		want  bool
	}{
		{"no lists", nil, nil, false},
		{"allow only", []NamedSet{{Name: "a", Set: mustSet(t, "a.example")}}, nil, false},
		{"nil block set", nil, []NamedSet{{Name: "b", Set: nil}}, false},
		{"empty block set", nil, []NamedSet{{Name: "b", Set: mustSet(t)}}, false},
		{"block with entries", nil, []NamedSet{{Name: "e", Set: mustSet(t)}, {Name: "b", Set: mustSet(t, "b.example")}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewListSet(tt.allow, tt.block).HasBlock(); got != tt.want {
				t.Fatalf("HasBlock() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListSetDoesNotShareInputSlices(t *testing.T) {
	allow := []NamedSet{{Name: "allow", Set: mustSet(t, "keep.example")}}
	block := []NamedSet{{Name: "ads", Set: mustSet(t, "ads.example")}}
	lists := NewListSet(allow, block)

	allow[0] = NamedSet{Name: "changed", Set: mustSet(t, "other.example")}
	block[0] = NamedSet{Name: "changed", Set: mustSet(t, "other.example")}

	if list, ok := lists.LookupAllow("keep.example"); !ok || list != "allow" {
		t.Fatalf("LookupAllow after caller change = %q, %v; want allow, true", list, ok)
	}
	if list, ok := lists.LookupBlock("ads.example"); !ok || list != "ads" {
		t.Fatalf("LookupBlock after caller change = %q, %v; want ads, true", list, ok)
	}
}
