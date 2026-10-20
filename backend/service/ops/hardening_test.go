package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatus_AStaleOrOddRequestIsClearedInsteadOfQueuedForever(t *testing.T) {
	f := newFixture(t)
	stale := filepath.Join(f.req, Backup)
	if err := os.WriteFile(stale, nil, 0o660); err != nil {
		t.Fatal(err)
	}
	old := testNow.Add(-5 * time.Minute)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	if s := f.status(Backup); s.State != StateIdle {
		t.Errorf("stale request: state = %q, want idle", s.State)
	}
	if _, err := os.Lstat(stale); err == nil {
		t.Error("the stale request file is still there")
	}

	if err := os.MkdirAll(filepath.Join(f.req, Health, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if s := f.status(Health); s.State != StateIdle {
		t.Errorf("directory request: state = %q, want idle", s.State)
	}
	if err := f.svc.Run(Health); err != nil {
		t.Errorf("Run after a directory was cleared = %v", err)
	}

	fresh := filepath.Join(f.req, Renew)
	if err := os.WriteFile(fresh, nil, 0o660); err != nil {
		t.Fatal(err)
	}
	recent := testNow.Add(-30 * time.Second)
	if err := os.Chtimes(fresh, recent, recent); err != nil {
		t.Fatal(err)
	}
	if s := f.status(Renew); s.State != StateQueued {
		t.Errorf("recent request: state = %q, want queued", s.State)
	}
}

func TestScript_ARequestDirectoryIsRemovedSoItCannotRetriggerTheUnit(t *testing.T) {
	f := newScriptFixture(t)
	dir := filepath.Join(f.req, Health)
	if err := os.MkdirAll(filepath.Join(dir, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}

	f.run(Health, "HECATE_OPS_CMD_HEALTH=echo ok")

	if _, err := os.Lstat(dir); err == nil {
		t.Error("the request directory survived the run")
	}
}

func TestScript_OverridesAreIgnoredOutsideTheTestMode(t *testing.T) {
	f := newScriptFixture(t)
	marker := filepath.ToSlash(filepath.Join(t.TempDir(), "marker"))

	f.run(Health, "HECATE_OPS_TEST=0", "HECATE_OPS_CMD_HEALTH=touch "+marker)

	if _, err := os.Stat(marker); err == nil {
		t.Error("an override command ran without the test mode")
	}
	entries, _ := os.ReadDir(f.res)
	if len(entries) != 0 {
		t.Errorf("directory overrides were honoured outside the test mode: %d files in the result directory", len(entries))
	}
}

func TestScript_AHugeSingleLineCannotPinTheRedactionFilter(t *testing.T) {
	f := newScriptFixture(t)
	start := time.Now()

	f.run(Health, "HECATE_OPS_CMD_HEALTH=head -c 90000 /dev/zero | tr '\\0' a; printf '.!\\n'")

	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("the run took %v, the redaction filter is not bounded", took)
	}
	text, _ := f.svc.Output(Health)
	for _, line := range strings.Split(text, "\n") {
		if len(line) > 400 {
			t.Errorf("an output line of %d bytes survived, want at most 400", len(line))
		}
	}
	if f.status(Health).State != StateOK {
		t.Errorf("status = %+v", f.status(Health))
	}
}
