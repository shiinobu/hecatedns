package dashboard

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shiinobu/hecatedns/backend/service/ops"
)

type opsFixture struct {
	*apiFixture
	req string
	res string
}

func newOpsFixture(t *testing.T) *opsFixture {
	t.Helper()
	root := t.TempDir()
	o := &opsFixture{req: filepath.Join(root, "req"), res: filepath.Join(root, "result")}
	for _, dir := range []string{o.req, o.res} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	o.apiFixture = newAPIFixture(t, func(opts *APIOptions) {
		opts.Ops = ops.New(ops.Options{RequestDir: o.req, ResultDir: o.res, Now: func() time.Time { return apiNow }})
	})
	return o
}

func (o *opsFixture) result(action, status string, exit int, ago time.Duration, output string) {
	o.t.Helper()
	end := apiNow.Add(-ago)
	body := fmt.Sprintf("status=%s\nstarted=%s\nfinished=%s\nexit=%d\n---\n%s", status, end.Add(-2*time.Second).Format(time.RFC3339), end.Format(time.RFC3339), exit, output)
	if err := os.WriteFile(filepath.Join(o.res, action+".result"), []byte(body), 0o644); err != nil {
		o.t.Fatal(err)
	}
}

func actionRow(t *testing.T, m map[string]any, name string) map[string]any {
	t.Helper()
	rows, _ := m["actions"].([]any)
	for _, r := range rows {
		if row, ok := r.(map[string]any); ok && row["action"] == name {
			return row
		}
	}
	t.Fatalf("no action row %q in %v", name, m["actions"])
	return nil
}

func TestOps_DisabledHostSaysSoInsteadOfFailing(t *testing.T) {
	f := newAPIFixture(t, nil)

	rec := f.get("/api/ops")
	got := decodeMap(t, rec)

	if rec.Code != http.StatusOK || got["enabled"] != false {
		t.Fatalf("GET = %d %v, want 200 and enabled false", rec.Code, got)
	}
	if rows, ok := got["actions"].([]any); !ok || len(rows) != 0 {
		t.Errorf("actions = %v, want an empty array", got["actions"])
	}
	if rows, ok := got["backups"].([]any); !ok || len(rows) != 0 {
		t.Errorf("backups = %v, want an empty array", got["backups"])
	}
	if post := f.post("/api/ops/run", `{"action":"health"}`); post.Code != http.StatusServiceUnavailable || decodeMap(t, post)["error"] != "unavailable" {
		t.Errorf("POST on a disabled host = %d %s", post.Code, post.Body)
	}
}

func TestOps_ListsEveryActionWithItsLastResult(t *testing.T) {
	o := newOpsFixture(t)
	o.result("health", "ok", 0, 4*time.Second, "report\n")
	o.result("backup", "failed", 2, time.Hour, "")
	list := fmt.Sprintf("%d 4096 hecate-20261020-120000.tar.gz\n", apiNow.Unix())
	if err := os.WriteFile(filepath.Join(o.res, "backups.list"), []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}

	got := decodeMap(t, o.get("/api/ops"))

	if got["enabled"] != true || got["output"] != nil {
		t.Errorf("enabled = %v output = %v", got["enabled"], got["output"])
	}
	rows, _ := got["actions"].([]any)
	var names []string
	for _, r := range rows {
		names = append(names, r.(map[string]any)["action"].(string))
	}
	want := []string{"health", "backup", "renew-test", "renew", "renew-force"}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Errorf("actions = %v, want %v", names, want)
	}
	h := actionRow(t, got, "health")
	if h["state"] != "ok" || h["exit"] != float64(0) || h["cooldown_s"] != float64(6) || h["finished"] != apiNow.Add(-4*time.Second).Format(time.RFC3339) {
		t.Errorf("health row = %v", h)
	}
	if b := actionRow(t, got, "backup"); b["state"] != "failed" || b["exit"] != float64(2) || b["cooldown_s"] != float64(0) {
		t.Errorf("backup row = %v", b)
	}
	idle := actionRow(t, got, "renew")
	if idle["state"] != "idle" || idle["started"] != nil || idle["finished"] != nil || idle["exit"] != nil {
		t.Errorf("idle row = %v", idle)
	}
	backups, _ := got["backups"].([]any)
	if len(backups) != 1 || backups[0].(map[string]any)["name"] != "hecate-20261020-120000.tar.gz" || backups[0].(map[string]any)["bytes"] != float64(4096) {
		t.Errorf("backups = %v", got["backups"])
	}
}

func TestOps_OutputComesOnlyForTheActionAskedFor(t *testing.T) {
	o := newOpsFixture(t)
	o.result("health", "ok", 0, time.Hour, "line <b>one</b>\n")
	o.result("backup", "ok", 0, time.Hour, "secret-ish\n")

	got := decodeMap(t, o.get("/api/ops?action=health"))

	out, _ := got["output"].(map[string]any)
	if out["action"] != "health" || out["text"] != "line <b>one</b>\n" {
		t.Errorf("output = %v", got["output"])
	}
	if rec := o.get("/api/ops?action=reboot"); rec.Code != http.StatusNotFound || decodeMap(t, rec)["error"] != "unknown_action" {
		t.Errorf("unknown action = %d %s", rec.Code, rec.Body)
	}
}

func TestOps_RunQueuesOnceAndExplainsRefusals(t *testing.T) {
	o := newOpsFixture(t)

	rec := o.post("/api/ops/run", `{"action":"backup"}`)
	got := decodeMap(t, rec)
	if rec.Code != http.StatusAccepted || got["action"] != "backup" || got["state"] != "queued" {
		t.Fatalf("first run = %d %v", rec.Code, got)
	}
	if _, err := os.Stat(filepath.Join(o.req, "backup")); err != nil {
		t.Errorf("no request file: %v", err)
	}
	row := actionRow(t, decodeMap(t, o.get("/api/ops")), "backup")
	if row["state"] != "queued" {
		t.Errorf("state after the run = %v", row["state"])
	}

	tests := []struct {
		name, body string
		status     int
		code       string
	}{
		{"already queued", `{"action":"backup"}`, http.StatusConflict, "busy"},
		{"unknown action", `{"action":"reboot"}`, http.StatusNotFound, "unknown_action"},
		{"empty action", `{"action":""}`, http.StatusNotFound, "unknown_action"},
		{"not json", `action=health`, http.StatusBadRequest, "bad_request"},
		{"unknown field", `{"action":"health","args":"-x"}`, http.StatusBadRequest, "bad_request"},
	}
	for _, tt := range tests {
		rec := o.post("/api/ops/run", tt.body)
		if rec.Code != tt.status || decodeMap(t, rec)["error"] != tt.code {
			t.Errorf("%s: %d %s, want %d %s", tt.name, rec.Code, rec.Body, tt.status, tt.code)
		}
	}
	entries, _ := os.ReadDir(o.req)
	if len(entries) != 1 {
		t.Errorf("request directory holds %d entries, want 1", len(entries))
	}
}

func TestOps_RunInsideTheCooldownIsRefusedWithTheWait(t *testing.T) {
	o := newOpsFixture(t)
	o.result("health", "ok", 0, 4*time.Second, "")

	rec := o.post("/api/ops/run", `{"action":"health"}`)
	got := decodeMap(t, rec)

	if rec.Code != http.StatusTooManyRequests || got["error"] != "cooldown" || got["cooldown_s"] != float64(6) {
		t.Errorf("run in the cooldown = %d %v", rec.Code, got)
	}
}

func TestOps_OnlyTheDocumentedMethods(t *testing.T) {
	o := newOpsFixture(t)
	for _, tt := range []struct{ method, target string }{
		{http.MethodPost, "/api/ops"},
		{http.MethodGet, "/api/ops/run"},
		{http.MethodDelete, "/api/ops/run"},
	} {
		rec := httptest.NewRecorder()
		o.api.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.target, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405", tt.method, tt.target, rec.Code)
		}
	}
}
