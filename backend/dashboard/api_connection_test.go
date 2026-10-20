package dashboard

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

const (
	connMain   = "ghost.example.org"
	connBackup = "ghost.example.net"
	connNameA  = "aaaaaaaaaaaaaaaa." + connMain
	connNameB  = "bbbbbbbbbbbbbbbb." + connMain
	connNameC  = "cccccccccccccccc." + connBackup
)

func connectionFixture(t *testing.T, conn ConnectionOptions) *apiFixture {
	t.Helper()
	return newAPIFixture(t, func(o *APIOptions) { o.Connection = conn })
}

func stringList(t *testing.T, v any) []string {
	t.Helper()
	items, ok := v.([]any)
	if !ok {
		t.Fatalf("%v is not a JSON array", v)
	}
	out := make([]string, len(items))
	for i, item := range items {
		out[i], _ = item.(string)
	}
	return out
}

func connectionNames(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	items, ok := m["names"].([]any)
	if !ok {
		t.Fatalf("names is not a JSON array: %v", m["names"])
	}
	out := make([]map[string]any, len(items))
	for i, item := range items {
		out[i], _ = item.(map[string]any)
	}
	return out
}

func TestAPI_ConnectionFollowsTheContract(t *testing.T) {
	f := connectionFixture(t, ConnectionOptions{
		Names:        []string{connNameA},
		ProbeDomains: []string{connMain, connBackup},
		Scheme:       "https",
		Port:         443,
	})
	rec := f.get("/api/connection")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d, headers %v", rec.Code, rec.Header())
	}
	body := decodeMap(t, rec)
	if got := stringList(t, body["domains"]); !reflect.DeepEqual(got, []string{connMain, connBackup}) {
		t.Errorf("domains = %v", got)
	}
	names := connectionNames(t, body)
	if len(names) != 1 {
		t.Fatalf("names = %v", names)
	}
	want := map[string]any{
		"host": connNameA, "base": connMain, "source": "config", "current": true, "removable": false, "added": nil,
	}
	if !reflect.DeepEqual(names[0], want) {
		t.Errorf("name = %v, want %v", names[0], want)
	}
	if got := field(t, body, "probe", "scheme"); got != "https" {
		t.Errorf("probe.scheme = %v", got)
	}
	if got := field(t, body, "probe", "port"); got != float64(443) {
		t.Errorf("probe.port = %v", got)
	}
	if got := field(t, body, "probe", "samples"); got != float64(3) {
		t.Errorf("probe.samples = %v", got)
	}
	if got := stringList(t, field(t, body, "probe", "domains")); !reflect.DeepEqual(got, []string{connMain, connBackup}) {
		t.Errorf("probe.domains = %v", got)
	}
	if got := field(t, body, "rotation", "available"); got != false {
		t.Errorf("rotation.available = %v", got)
	}
	if got := field(t, body, "rotation", "max_names"); got != float64(4) {
		t.Errorf("rotation.max_names = %v", got)
	}
}

func TestAPI_ConnectionMarksTheNewestNamePerBase(t *testing.T) {
	f := connectionFixture(t, ConnectionOptions{
		Names:        []string{connNameA, connNameB, connNameC},
		ProbeDomains: []string{connMain, connBackup},
		Scheme:       "https",
		Port:         443,
	})
	names := connectionNames(t, decodeMap(t, f.get("/api/connection")))
	if len(names) != 3 {
		t.Fatalf("names = %v", names)
	}
	wantCurrent := []bool{false, true, true}
	wantBase := []string{connMain, connMain, connBackup}
	for i, n := range names {
		if n["current"] != wantCurrent[i] || n["base"] != wantBase[i] {
			t.Errorf("names[%d] = %v, want current %v base %s", i, n, wantCurrent[i], wantBase[i])
		}
	}
}

func TestAPI_ConnectionPicksTheLongestProbeDomainAsBase(t *testing.T) {
	f := connectionFixture(t, ConnectionOptions{
		Names:        []string{connNameA},
		ProbeDomains: []string{"example.org", connMain},
		Scheme:       "http",
		Port:         8080,
	})
	names := connectionNames(t, decodeMap(t, f.get("/api/connection")))
	if len(names) != 1 || names[0]["base"] != connMain {
		t.Fatalf("names = %v", names)
	}
}

func TestAPI_ConnectionDerivesDomainsFromNamesWithoutProbeDomains(t *testing.T) {
	f := connectionFixture(t, ConnectionOptions{
		Names:  []string{"abcdefghij012345.other.example.com", "klmnopqrst012345.other.example.com"},
		Scheme: "https",
		Port:   443,
	})
	body := decodeMap(t, f.get("/api/connection"))
	if got := stringList(t, body["domains"]); !reflect.DeepEqual(got, []string{"other.example.com"}) {
		t.Errorf("domains = %v", got)
	}
	if got := stringList(t, field(t, body, "probe", "domains")); len(got) != 0 {
		t.Errorf("probe.domains = %v, want none", got)
	}
	for _, n := range connectionNames(t, body) {
		if n["base"] != "other.example.com" {
			t.Errorf("name %v has the wrong base", n)
		}
	}
}

func TestAPI_ConnectionListsAreArraysWhenEmpty(t *testing.T) {
	f := connectionFixture(t, ConnectionOptions{Scheme: "http", Port: 8080})
	body := decodeMap(t, f.get("/api/connection"))
	lists := map[string]any{
		"domains":       body["domains"],
		"names":         body["names"],
		"probe.domains": field(t, body, "probe", "domains"),
	}
	for name, value := range lists {
		if items, ok := value.([]any); !ok || len(items) != 0 {
			t.Errorf("%s = %#v, want an empty JSON array", name, value)
		}
	}
}

func TestAPI_ConnectionAcceptsReadsOnly(t *testing.T) {
	f := connectionFixture(t, ConnectionOptions{Scheme: "https", Port: 443})
	rec := httptest.NewRecorder()
	f.api.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/connection", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", rec.Code)
	}
}
