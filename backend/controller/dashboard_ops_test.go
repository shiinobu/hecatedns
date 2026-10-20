package controller

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestDashboardRun_QueuesAnOperationThroughTheSpoolDirectory(t *testing.T) {
	f := newDashboardRunFixture(t, true)
	req, res := filepath.Join(f.dir, "ops-req"), filepath.Join(f.dir, "ops-result")
	for _, dir := range []string{req, res} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ops := fmt.Sprintf("  ops:\n    request_dir: %q\n    result_dir: %q\n", filepath.ToSlash(req), filepath.ToSlash(res))
	if err := os.WriteFile(f.config, []byte(readText(t, f.config)+ops), 0o600); err != nil {
		t.Fatal(err)
	}
	run := f.start(t)
	c := newDashboardClient(t, f.port)
	c.waitUntilUp(t)
	c.login(t, f.key)

	if status, body := c.do(http.MethodGet, "/api/ops", ""); status != http.StatusOK || body["enabled"] != true {
		t.Fatalf("ops = %d %v", status, body)
	}
	if status, body := c.do(http.MethodPost, "/api/ops/run", `{"action":"health"}`); status != http.StatusAccepted || body["state"] != "queued" {
		t.Fatalf("run = %d %v", status, body)
	}
	if _, err := os.Stat(filepath.Join(req, "health")); err != nil {
		t.Errorf("no request file in the spool directory: %v", err)
	}
	if status, body := c.do(http.MethodPost, "/api/ops/run", `{"action":"health"}`); status != http.StatusConflict {
		t.Errorf("second run = %d %v, want 409", status, body)
	}
	run.stop()
}

func TestDashboardRun_OperationsAreOffWithoutTheOpsConfig(t *testing.T) {
	f := newDashboardRunFixture(t, true)
	run := f.start(t)
	c := newDashboardClient(t, f.port)
	c.waitUntilUp(t)
	c.login(t, f.key)

	if status, body := c.do(http.MethodGet, "/api/ops", ""); status != http.StatusOK || body["enabled"] != false {
		t.Errorf("ops = %d %v, want enabled false", status, body)
	}
	if status, _ := c.do(http.MethodPost, "/api/ops/run", `{"action":"health"}`); status != http.StatusServiceUnavailable {
		t.Errorf("run without ops = %d, want 503", status)
	}
	run.stop()
}
