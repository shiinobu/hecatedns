package dashboard

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/shiinobu/hecatedns/backend/engine"
	"github.com/shiinobu/hecatedns/backend/service/ruleedit"
)

func (f *apiFixture) post(target, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	rec := httptest.NewRecorder()
	f.api.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, target, strings.NewReader(body)))
	return rec
}

func (f *apiFixture) rulesText() string {
	f.t.Helper()
	data, err := os.ReadFile(f.rules)
	if err != nil {
		return ""
	}
	return string(data)
}

func (f *apiFixture) seedRules(text string) {
	f.t.Helper()
	if err := os.WriteFile(f.rules, []byte(text), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func TestRules_ListIsEmptyWhenThereIsNoFile(t *testing.T) {
	f := newAPIFixture(t, nil)

	rec := f.get("/api/rules")

	got := decodeMap(t, rec)
	rules, isList := got["rules"].([]any)
	if rec.Code != http.StatusOK || !isList || len(rules) != 0 {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if got["count"] != 0.0 || got["bytes"] != 0.0 || got["max_bytes"] != float64(engine.MaxRulesFileBytes) {
		t.Fatalf("body %s", rec.Body)
	}
}

func TestRules_ListKeepsFileOrderAndReportsSize(t *testing.T) {
	f := newAPIFixture(t, nil)
	f.seedRules("# mine\nblock b.example.com\nallow a.example.com\n")

	rec := f.get("/api/rules")

	got := decodeMap(t, rec)
	checks := map[string]any{
		"rules.0.action": "block", "rules.0.domain": "b.example.com",
		"rules.1.action": "allow", "rules.1.domain": "a.example.com",
		"count": 2.0, "bytes": float64(len(f.rulesText())),
	}
	for path, want := range checks {
		if v := lookup(t, got, path); v != want {
			t.Errorf("%s = %v, want %v", path, v, want)
		}
	}
}

func TestRules_AddWritesTheRuleAndAnswersWithIt(t *testing.T) {
	f := newAPIFixture(t, nil)

	rec := f.post("/api/rules", `{"action":"block","domain":"Ads.Example.COM"}`)

	got := decodeMap(t, rec)
	if rec.Code != http.StatusOK || lookup(t, got, "rule.action") != "block" || lookup(t, got, "rule.domain") != "ads.example.com" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if replaced, present := got["replaced"]; !present || replaced != nil {
		t.Fatalf("replaced = %v (present %v), want an explicit null", replaced, present)
	}
	if f.rulesText() != "block ads.example.com\n" {
		t.Fatalf("file = %q", f.rulesText())
	}
}

func TestRules_AddReturnsTheRuleItReplaced(t *testing.T) {
	f := newAPIFixture(t, nil)
	f.seedRules("allow ads.example.com\nblock other.example.com\n")

	rec := f.post("/api/rules", `{"action":"block","domain":"ads.example.com"}`)

	got := decodeMap(t, rec)
	if rec.Code != http.StatusOK || lookup(t, got, "replaced.action") != "allow" || lookup(t, got, "replaced.domain") != "ads.example.com" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if f.rulesText() != "block other.example.com\nblock ads.example.com\n" {
		t.Fatalf("file = %q", f.rulesText())
	}
}

func TestRules_AddRefusals(t *testing.T) {
	long := strings.Repeat("a", 300) + ".com"
	tests := []struct {
		name, body, code string
	}{
		{"bad action", `{"action":"pass","domain":"x.example.com"}`, "bad_action"},
		{"empty action", `{"domain":"x.example.com"}`, "bad_action"},
		{"empty domain", `{"action":"block","domain":""}`, "invalid_domain"},
		{"space", `{"action":"block","domain":"has space.com"}`, "invalid_domain"},
		{"newline injects a second rule", `{"action":"block","domain":"a.com\nallow b.com"}`, "invalid_domain"},
		{"a url", `{"action":"block","domain":"https://x.example.com/path"}`, "invalid_domain"},
		{"empty label", `{"action":"block","domain":"a..example.com"}`, "invalid_domain"},
		{"single label", `{"action":"block","domain":"localhost"}`, "invalid_domain"},
		{"too long", `{"action":"block","domain":"` + long + `"}`, "invalid_domain"},
		{"public suffix block", `{"action":"block","domain":"co.id"}`, "public_suffix"},
		{"public suffix allow", `{"action":"allow","domain":"my.id"}`, "public_suffix"},
		{"top level", `{"action":"block","domain":"com"}`, "invalid_domain"},
		{"protected block", `{"action":"block","domain":"secret.my.id"}`, "protected_name"},
		{"protected itself", `{"action":"block","domain":"dash.secret.my.id"}`, "protected_name"},
		{"protected upper case", `{"action":"BLOCK","domain":"DASH.Secret.My.ID"}`, "protected_name"},
		{"not json", `block ads.example.com`, "bad_request"},
		{"unknown field", `{"action":"block","domain":"x.example.com","force":true}`, "bad_request"},
		{"empty body", ``, "bad_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAPIFixture(t, func(o *APIOptions) { o.Guard = NewGuard("dash.secret.my.id") })
			f.seedRules("block keep.example.com\n")

			rec := f.post("/api/rules", tt.body)

			got := decodeMap(t, rec)
			if rec.Code != http.StatusBadRequest || got["error"] != tt.code {
				t.Fatalf("status %d, body %s, want 400 %s", rec.Code, rec.Body, tt.code)
			}
			if msg, _ := got["message"].(string); msg == "" || strings.Contains(msg, "dash.secret") {
				t.Fatalf("message = %q", msg)
			}
			if f.rulesText() != "block keep.example.com\n" {
				t.Fatalf("a refused request changed the file: %q", f.rulesText())
			}
		})
	}
}

func TestRules_AllowOfAProtectedNameIsAccepted(t *testing.T) {
	f := newAPIFixture(t, func(o *APIOptions) { o.Guard = NewGuard("dash.secret.my.id") })

	rec := f.post("/api/rules", `{"action":"allow","domain":"dash.secret.my.id"}`)

	if rec.Code != http.StatusOK || f.rulesText() != "allow dash.secret.my.id\n" {
		t.Fatalf("status %d, body %s, file %q", rec.Code, rec.Body, f.rulesText())
	}
}

func TestRules_ASiblingOfAProtectedNameCanBeBlocked(t *testing.T) {
	f := newAPIFixture(t, func(o *APIOptions) { o.Guard = NewGuard("dash.secret.my.id") })

	rec := f.post("/api/rules", `{"action":"block","domain":"ads.secret.my.id"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
}

func TestRules_AddRefusesWhenTheFileIsFull(t *testing.T) {
	f := newAPIFixture(t, nil)
	line := "block a.example.com\n"
	filler := strings.Repeat(line, engine.MaxRulesFileBytes/len(line))
	f.seedRules(filler)

	rec := f.post("/api/rules", `{"action":"block","domain":"new.example.com"}`)

	got := decodeMap(t, rec)
	if rec.Code != http.StatusBadRequest || got["error"] != "file_full" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if f.rulesText() != filler {
		t.Fatal("a refused request changed the file")
	}
}

func TestRules_RemoveReturnsWhatItRemoved(t *testing.T) {
	f := newAPIFixture(t, nil)
	f.seedRules("block a.example.com\nblock b.example.com\n")

	rec := f.post("/api/rules/remove", `{"domain":"A.example.com"}`)

	got := decodeMap(t, rec)
	if rec.Code != http.StatusOK || lookup(t, got, "removed.action") != "block" || lookup(t, got, "removed.domain") != "a.example.com" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if f.rulesText() != "block b.example.com\n" {
		t.Fatalf("file = %q", f.rulesText())
	}
}

func TestRules_RemoveErrors(t *testing.T) {
	tests := []struct {
		name, body, code string
		status           int
	}{
		{"unknown domain", `{"domain":"other.example.com"}`, "not_found", http.StatusNotFound},
		{"invalid domain", `{"domain":"has space.com"}`, "invalid_domain", http.StatusBadRequest},
		{"not json", `nope`, "bad_request", http.StatusBadRequest},
		{"unknown field", `{"domain":"a.example.com","all":true}`, "bad_request", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAPIFixture(t, nil)
			f.seedRules("block a.example.com\n")

			rec := f.post("/api/rules/remove", tt.body)

			got := decodeMap(t, rec)
			if rec.Code != tt.status || got["error"] != tt.code {
				t.Fatalf("status %d, body %s, want %d %s", rec.Code, rec.Body, tt.status, tt.code)
			}
			if f.rulesText() != "block a.example.com\n" {
				t.Fatalf("file = %q", f.rulesText())
			}
		})
	}
}

func TestRules_UndoDataRoundTrips(t *testing.T) {
	f := newAPIFixture(t, nil)
	f.seedRules("allow a.example.com\n")
	f.post("/api/rules", `{"action":"block","domain":"a.example.com"}`)

	rec := f.post("/api/rules", `{"action":"allow","domain":"a.example.com"}`)

	got := decodeMap(t, rec)
	if lookup(t, got, "replaced.action") != "block" || f.rulesText() != "allow a.example.com\n" {
		t.Fatalf("body %s, file %q", rec.Body, f.rulesText())
	}
}

func TestRules_OnlyTheDocumentedMethods(t *testing.T) {
	f := newAPIFixture(t, nil)
	tests := []struct {
		method, target string
		status         int
		code           string
	}{
		{http.MethodDelete, "/api/rules", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodPut, "/api/rules", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodGet, "/api/rules/remove", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodPost, "/api/rules/other", http.StatusNotFound, "not_found"},
		{http.MethodGet, "/api/rules/", http.StatusNotFound, "not_found"},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		f.api.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.target, nil))
		got := decodeMap(t, rec)
		if rec.Code != tt.status || got["error"] != tt.code {
			t.Errorf("%s %s = %d %v, want %d %s", tt.method, tt.target, rec.Code, got, tt.status, tt.code)
		}
	}
	head := httptest.NewRecorder()
	f.api.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/api/rules", nil))
	if head.Code != http.StatusOK {
		t.Errorf("HEAD /api/rules = %d, want 200", head.Code)
	}
}

func TestRules_ParallelAddsAreNotLost(t *testing.T) {
	f := newAPIFixture(t, nil)
	const writers = 24
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.post("/api/rules", fmt.Sprintf(`{"action":"block","domain":"n%d.example.com"}`, i))
		}()
	}
	wg.Wait()

	listing, err := ruleedit.List(f.rules)

	if err != nil || len(listing.Rules) != writers {
		t.Fatalf("rules = %d, err = %v, want %d", len(listing.Rules), err, writers)
	}
}

func TestRules_ParallelAddAndRemoveOfOneNameStaysConsistent(t *testing.T) {
	f := newAPIFixture(t, nil)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			f.post("/api/rules", `{"action":"block","domain":"same.example.com"}`)
		}()
		go func() {
			defer wg.Done()
			f.post("/api/rules/remove", `{"domain":"same.example.com"}`)
		}()
	}
	wg.Wait()

	listing, err := ruleedit.List(f.rules)

	if err != nil || len(listing.Rules) > 1 || len(listing.Skipped) != 0 {
		t.Fatalf("listing = %+v, err = %v", listing, err)
	}
}

func TestRules_AFailingWriteIsAnInternalErrorWithoutDetails(t *testing.T) {
	f := newAPIFixture(t, func(o *APIOptions) { o.RulesFile = t.TempDir() })

	rec := f.post("/api/rules", `{"action":"block","domain":"x.example.com"}`)

	got := decodeMap(t, rec)
	if rec.Code != http.StatusInternalServerError || got["error"] != "internal" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), os.TempDir()) {
		t.Fatalf("the response leaks a path: %s", rec.Body)
	}
}

func TestRules_ListFailureIsAnInternalError(t *testing.T) {
	f := newAPIFixture(t, func(o *APIOptions) { o.RulesFile = t.TempDir() })

	rec := f.get("/api/rules")

	if rec.Code != http.StatusInternalServerError || decodeMap(t, rec)["error"] != "internal" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
}
