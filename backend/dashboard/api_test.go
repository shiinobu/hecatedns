package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shiinobu/hecatedns/backend/engine"
	"github.com/shiinobu/hecatedns/backend/lib/config"
)

const apiTSLayout = "2006-01-02T15:04:05.000Z07:00"

var apiNow = time.Date(2026, 10, 20, 10, 42, 5, 0, time.UTC)

type apiFixture struct {
	t        *testing.T
	api      *API
	clock    *fakeClock
	logDir   string
	rules    string
	listsDir string
	resolved atomic.Int64
}

type fakeCert struct {
	notAfter time.Time
	byName   map[string]time.Time
}

func (c fakeCert) NotAfter() time.Time { return c.notAfter }

func (c fakeCert) NotAfterFor(name string) (time.Time, bool) {
	at, ok := c.byName[name]
	return at, ok
}

func newAPIFixture(t *testing.T, mut func(*APIOptions)) *apiFixture {
	t.Helper()
	root := t.TempDir()
	f := &apiFixture{
		t:        t,
		clock:    &fakeClock{now: apiNow},
		logDir:   filepath.Join(root, "logs"),
		rules:    filepath.Join(root, "rules.txt"),
		listsDir: filepath.Join(root, "lists"),
	}
	opts := APIOptions{
		LogDir:     f.logDir,
		LogEnabled: true,
		Location:   time.UTC,
		RulesFile:  f.rules,
		ListsDir:   f.listsDir,
		Resolver: func(context.Context) ResolverResult {
			f.resolved.Add(1)
			return ResolverResult{OK: true, MS: 38, Name: "example.com"}
		},
		Now: f.clock.Now,
	}
	if mut != nil {
		mut(&opts)
	}
	f.api = NewAPI(opts)
	return f
}

func (f *apiFixture) log(at time.Time, qname, qtype, action, reason, source string) {
	f.t.Helper()
	line := `{"ts":"` + at.Format(apiTSLayout) + `","client":"10.0.0.2","proto":"tls","qname":` + jsonString(qname) +
		`,"qtype":"` + qtype + `","action":"` + action + `","reason":"` + reason + `","source":` + jsonString(source) + `,"ms":3}` + "\n"
	if err := os.MkdirAll(f.logDir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	path := filepath.Join(f.logDir, "queries-"+at.Format("20060102")+".jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		f.t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(line); err != nil {
		f.t.Fatal(err)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (f *apiFixture) get(target string) *httptest.ResponseRecorder {
	f.t.Helper()
	rec := httptest.NewRecorder()
	f.api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q is not a JSON object: %v", rec.Body.String(), err)
	}
	return out
}

func field(t *testing.T, m map[string]any, path ...string) any {
	t.Helper()
	var cur any = m
	for _, key := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("path %v: %v is not an object", path, cur)
		}
		cur = obj[key]
	}
	return cur
}

func rowNames(t *testing.T, m map[string]any) []string {
	t.Helper()
	rows, _ := m["rows"].([]any)
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.(map[string]any)["name"].(string))
	}
	return names
}

func TestStatus_ReportsEverythingTheScreenNeeds(t *testing.T) {
	f := newAPIFixture(t, func(o *APIOptions) {
		o.Lists = []config.Source{
			{Name: "pro", Kind: "block", Format: "domains", Enabled: true},
			{Name: "extra", Kind: "block", Format: "domains", Enabled: false},
			{Name: "gone", Kind: "block", Format: "domains", Enabled: true},
		}
	})
	if err := os.MkdirAll(f.listsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"pro.txt":       "ads.example.com\n",
		"pro.meta.json": `{"fetched_at":"2026-10-20T04:30:00Z","count":203414}`,
	} {
		if err := os.WriteFile(filepath.Join(f.listsDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rules := "block a.example.com\nallow b.example.com\n"
	if err := os.WriteFile(f.rules, []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
	f.log(apiNow.Add(-time.Hour), "a.example.com", "A", "block", "rule", "block a.example.com")
	f.log(apiNow.Add(-2*time.Hour), "ads.example.com", "A", "block", "list", "pro")
	f.log(apiNow.Add(-3*time.Hour), "ok.example.com", "A", "pass", "", "")
	f.log(apiNow.Add(-4*time.Hour), "ok2.example.com", "AAAA", "pass", "", "")
	f.log(apiNow.Add(-5*time.Hour), "bad.example.com", "A", "error", "", "")
	f.log(apiNow.Add(-25*time.Hour), "old.example.com", "A", "pass", "", "")

	rec := f.get("/api/status")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := decodeMap(t, rec)
	checks := map[string]any{
		"time":            "2026-10-20T10:42:05Z",
		"log_enabled":     true,
		"counts.total":    5.0,
		"counts.blocked":  2.0,
		"counts.passed":   2.0,
		"counts.errors":   1.0,
		"counts.partial":  false,
		"rules.count":     2.0,
		"rules.bytes":     float64(len(rules)),
		"rules.max_bytes": float64(engine.MaxRulesFileBytes),
		"resolver.ok":     true,
		"resolver.ms":     38.0,
		"resolver.name":   "example.com",
		"resolver.error":  "",
		"lists.0.state":   "ok",
		"lists.0.domains": 203414.0,
		"lists.0.updated": "2026-10-20T04:30:00Z",
		"lists.1.state":   "disabled",
		"lists.1.domains": nil,
		"lists.2.state":   "missing",
		"lists.2.updated": nil,
		"certificate":     nil,
	}
	for path, want := range checks {
		if v := lookup(t, got, path); v != want {
			t.Errorf("%s = %v, want %v", path, v, want)
		}
	}
}

func lookup(t *testing.T, m map[string]any, path string) any {
	t.Helper()
	var cur any = m
	for _, key := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[key]
		case []any:
			idx := int(key[0] - '0')
			cur = node[idx]
		default:
			t.Fatalf("path %s: cannot descend into %v", path, cur)
		}
	}
	return cur
}

func TestStatus_CertificateDaysLeft(t *testing.T) {
	f := newAPIFixture(t, func(o *APIOptions) {
		o.Cert = fakeCert{notAfter: apiNow.Add(71*24*time.Hour + 3*time.Hour)}
	})

	got := decodeMap(t, f.get("/api/status"))

	if v := lookup(t, got, "certificate.days_left"); v != 71.0 {
		t.Errorf("days_left = %v, want 71", v)
	}
	if v := lookup(t, got, "certificate.expires"); v != "2026-12-30T13:42:05Z" {
		t.Errorf("expires = %v", v)
	}
}

func TestStatus_LogOffGivesNullCounts(t *testing.T) {
	f := newAPIFixture(t, func(o *APIOptions) { o.LogEnabled = false })
	f.log(apiNow.Add(-time.Hour), "a.example.com", "A", "pass", "", "")

	got := decodeMap(t, f.get("/api/status"))

	if got["log_enabled"] != false || got["counts"] != nil {
		t.Fatalf("log_enabled = %v, counts = %v", got["log_enabled"], got["counts"])
	}
}

func TestStatus_CountsArePartialWhenTheScanIsCut(t *testing.T) {
	f := newAPIFixture(t, func(o *APIOptions) { o.ScanRecords = 3 })
	for i := range 5 {
		f.log(apiNow.Add(-time.Duration(i+1)*time.Minute), "a.example.com", "A", "pass", "", "")
	}

	got := decodeMap(t, f.get("/api/status"))

	if lookup(t, got, "counts.partial") != true || lookup(t, got, "counts.total") != 3.0 {
		t.Fatalf("counts = %v", got["counts"])
	}
}

func TestStatus_AFailingResolverIsShownNotHidden(t *testing.T) {
	f := newAPIFixture(t, func(o *APIOptions) {
		o.Resolver = func(context.Context) ResolverResult {
			return ResolverResult{Name: "example.com", Error: "timeout"}
		}
	})

	rec := f.get("/api/status")

	got := decodeMap(t, rec)
	if rec.Code != http.StatusOK || lookup(t, got, "resolver.ok") != false || lookup(t, got, "resolver.ms") != 0.0 || lookup(t, got, "resolver.error") != "timeout" {
		t.Fatalf("status %d, resolver = %v", rec.Code, got["resolver"])
	}
}

func TestStatus_IsCachedForAMinute(t *testing.T) {
	f := newAPIFixture(t, nil)

	f.get("/api/status")
	f.get("/api/status")
	afterTwo := f.resolved.Load()
	f.clock.Advance(61 * time.Second)
	f.get("/api/status")

	if afterTwo != 1 || f.resolved.Load() != 2 {
		t.Fatalf("resolver ran %d times after two requests and %d after the cache expired", afterTwo, f.resolved.Load())
	}
}

func TestAPI_OnlyGetAndOnlyKnownPaths(t *testing.T) {
	f := newAPIFixture(t, nil)
	tests := []struct {
		method, target string
		status         int
		code           string
	}{
		{http.MethodPost, "/api/status", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodPost, "/api/queries", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodGet, "/api/nothing", http.StatusNotFound, "not_found"},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		f.api.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.target, nil))
		got := decodeMap(t, rec)
		if rec.Code != tt.status || got["error"] != tt.code {
			t.Errorf("%s %s = %d %v, want %d %s", tt.method, tt.target, rec.Code, got, tt.status, tt.code)
		}
	}
}

func TestQueries_GroupByNameAndDecisionNewestFirst(t *testing.T) {
	f := newAPIFixture(t, nil)
	f.log(apiNow.Add(-10*time.Minute), "acs.news.example.co.id", "A", "pass", "", "")
	f.log(apiNow.Add(-9*time.Minute), "acs.news.example.co.id", "AAAA", "pass", "", "")
	f.log(apiNow.Add(-8*time.Minute), "acs.news.example.co.id", "A", "pass", "", "")
	f.log(apiNow.Add(-7*time.Minute), "ADS.tracker.example.com", "A", "block", "list", "pro")
	f.log(apiNow.Add(-6*time.Minute), "ads.tracker.example.com", "A", "block", "list", "pro")
	f.log(apiNow.Add(-5*time.Minute), "mine.example.com", "A", "block", "rule", "block mine.example.com")
	f.log(apiNow.Add(-4*time.Minute), "mine.example.com", "A", "pass", "allow", "allow mine.example.com")
	f.log(apiNow.Add(-3*time.Minute), "bad.example.com", "A", "error", "", "")

	rec := f.get("/api/queries")

	got := decodeMap(t, rec)
	if rec.Code != http.StatusOK || got["window"] != "15m" || got["partial"] != false || got["generated"] != "2026-10-20T10:42:05Z" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	want := []string{"mine.example.com", "mine.example.com", "ads.tracker.example.com", "acs.news.example.co.id"}
	if names := rowNames(t, got); strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v, want %v", names, want)
	}
	checks := map[string]any{
		"rows.0.decision": "pass", "rows.0.by": "rule", "rows.0.count": 1.0, "rows.0.last": "2026-10-20T10:38:05Z",
		"rows.1.decision": "block", "rows.1.by": "rule",
		"rows.2.count": 2.0, "rows.2.by": "list", "rows.2.list": "pro",
		"rows.3.count": 3.0, "rows.3.by": "", "rows.3.list": "",
	}
	for path, wantValue := range checks {
		if v := lookup(t, got, path); v != wantValue {
			t.Errorf("%s = %v, want %v", path, v, wantValue)
		}
	}
	types := lookup(t, got, "rows.3.types").([]any)
	if len(types) != 2 || types[0] != "A" || types[1] != "AAAA" {
		t.Errorf("types = %v, want [A AAAA]", types)
	}
	parents := lookup(t, got, "rows.3.parents").([]any)
	if len(parents) != 2 || parents[0] != "news.example.co.id" || parents[1] != "example.co.id" {
		t.Errorf("parents = %v, want the names above the public suffix, nearest first", parents)
	}
}

func TestQueries_Filters(t *testing.T) {
	f := newAPIFixture(t, nil)
	f.log(apiNow.Add(-5*time.Minute), "ads.example.com", "A", "block", "list", "pro")
	f.log(apiNow.Add(-4*time.Minute), "Shop.Example.net", "A", "pass", "", "")
	f.log(apiNow.Add(-30*time.Minute), "older.example.org", "A", "pass", "", "")
	tests := []struct {
		name, target string
		want         []string
	}{
		{"default window is 15m", "/api/queries", []string{"shop.example.net", "ads.example.com"}},
		{"wider window", "/api/queries?window=1h", []string{"shop.example.net", "ads.example.com", "older.example.org"}},
		{"only blocked", "/api/queries?decision=block", []string{"ads.example.com"}},
		{"only passed", "/api/queries?decision=pass&window=1h", []string{"shop.example.net", "older.example.org"}},
		{"text matches the name, any case", "/api/queries?q=SHOP", []string{"shop.example.net"}},
		{"text with no match", "/api/queries?q=zzz", []string{}},
		{"limit", "/api/queries?window=1h&limit=1", []string{"shop.example.net"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rowNames(t, decodeMap(t, f.get(tt.target)))
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("rows = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueries_RejectsBadParameters(t *testing.T) {
	f := newAPIFixture(t, nil)
	for _, target := range []string{
		"/api/queries?window=2h",
		"/api/queries?decision=maybe",
		"/api/queries?limit=0",
		"/api/queries?limit=abc",
		"/api/queries?limit=-5",
		"/api/queries?offset=-1",
		"/api/queries?offset=abc",
		"/api/queries?q=" + strings.Repeat("a", 101),
	} {
		rec := f.get(target)
		if rec.Code != http.StatusBadRequest || decodeMap(t, rec)["error"] != "bad_request" {
			t.Errorf("%s = %d %s, want 400 bad_request", target, rec.Code, rec.Body)
		}
	}
}

func TestQueries_LimitAboveTheCapIsClamped(t *testing.T) {
	f := newAPIFixture(t, nil)
	for i := range maxQueryRows + 20 {
		f.log(apiNow.Add(-time.Minute), "h"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+string(rune('a'+i/26%26))+".example.com", "A", "pass", "", "")
	}

	got := rowNames(t, decodeMap(t, f.get("/api/queries?limit=5000")))

	if len(got) != maxQueryRows {
		t.Fatalf("rows = %d, want the cap of %d", len(got), maxQueryRows)
	}
}

func TestQueries_PagesWithOffsetAndReportsTheTotal(t *testing.T) {
	f := newAPIFixture(t, nil)
	for i, name := range []string{"a", "b", "c", "d", "e"} {
		f.log(apiNow.Add(-time.Duration(i+1)*time.Minute), name+".example.com", "A", "pass", "", "")
	}
	tests := []struct {
		name, target string
		want         []string
	}{
		{"first page", "/api/queries?limit=2&offset=0", []string{"a.example.com", "b.example.com"}},
		{"second page", "/api/queries?limit=2&offset=2", []string{"c.example.com", "d.example.com"}},
		{"short last page", "/api/queries?limit=2&offset=4", []string{"e.example.com"}},
		{"past the end", "/api/queries?limit=2&offset=10", []string{}},
		{"offset defaults to zero", "/api/queries?limit=2", []string{"a.example.com", "b.example.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeMap(t, f.get(tt.target))

			if names := rowNames(t, got); strings.Join(names, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("rows = %v, want %v", names, tt.want)
			}
			if total := got["total"]; total != 5.0 {
				t.Errorf("total = %v, want 5 whatever the page", total)
			}
		})
	}
}

func TestQueries_ParentsLeaveOutProtectedNames(t *testing.T) {
	f := newAPIFixture(t, func(o *APIOptions) { o.Guard = NewGuard("ghost.example.org") })
	f.log(apiNow.Add(-time.Minute), "x.ghost.example.org", "A", "pass", "", "")
	f.log(apiNow.Add(-2*time.Minute), "x.other.example.org", "A", "pass", "", "")

	got := decodeMap(t, f.get("/api/queries"))

	if p := lookup(t, got, "rows.0.parents").([]any); len(p) != 0 {
		t.Errorf("a protected parent was offered: %v", p)
	}
	if p := lookup(t, got, "rows.1.parents").([]any); len(p) != 1 || p[0] != "other.example.org" {
		t.Errorf("parents = %v, want [other.example.org]", p)
	}
}

func TestQueries_HostileNamesStayData(t *testing.T) {
	f := newAPIFixture(t, nil)
	f.log(apiNow.Add(-time.Minute), `<script>alert("x")</script>.example.com`, "A", "pass", "", "")

	rec := f.get("/api/queries")

	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatalf("raw markup in the JSON body: %s", rec.Body)
	}
	if names := rowNames(t, decodeMap(t, rec)); len(names) != 1 || !strings.HasPrefix(names[0], "<script>") {
		t.Fatalf("the name did not survive as data: %v", names)
	}
}

func TestQueries_PartialWhenTheScanOrTheGroupCountIsCut(t *testing.T) {
	for _, tt := range []struct {
		name string
		mut  func(*APIOptions)
	}{
		{"record cap", func(o *APIOptions) { o.ScanRecords = 2 }},
		{"group cap", func(o *APIOptions) { o.MaxGroups = 2 }},
		{"time budget", func(o *APIOptions) {
			step := &steppingClock{now: apiNow, step: time.Second}
			o.Now = step.Now
			o.ScanBudget = 2 * time.Second
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newAPIFixture(t, tt.mut)
			for i, name := range []string{"a", "b", "c", "d", "e"} {
				f.log(apiNow.Add(-time.Duration(i+1)*time.Second), name+".example.com", "A", "pass", "", "")
			}

			got := decodeMap(t, f.get("/api/queries"))

			if got["partial"] != true || len(rowNames(t, got)) >= 5 {
				t.Fatalf("partial = %v, rows = %v", got["partial"], rowNames(t, got))
			}
		})
	}
}

type steppingClock struct {
	now  time.Time
	step time.Duration
}

func (c *steppingClock) Now() time.Time {
	c.now = c.now.Add(c.step)
	return c.now
}

func TestQueries_AreCachedForFiveSeconds(t *testing.T) {
	f := newAPIFixture(t, nil)
	f.log(apiNow.Add(-time.Minute), "a.example.com", "A", "pass", "", "")
	first := rowNames(t, decodeMap(t, f.get("/api/queries")))
	f.log(apiNow.Add(-30*time.Second), "b.example.com", "A", "pass", "", "")

	cached := rowNames(t, decodeMap(t, f.get("/api/queries")))
	f.clock.Advance(6 * time.Second)
	fresh := rowNames(t, decodeMap(t, f.get("/api/queries")))

	if len(first) != 1 || len(cached) != 1 || len(fresh) != 2 {
		t.Fatalf("rows %v, then %v inside the cache window, then %v after it", first, cached, fresh)
	}
}

func TestQueries_MissingLogMeansNoRows(t *testing.T) {
	f := newAPIFixture(t, nil)

	rec := f.get("/api/queries")

	if rec.Code != http.StatusOK || len(rowNames(t, decodeMap(t, rec))) != 0 || !strings.Contains(rec.Body.String(), `"rows":[]`) {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
}
