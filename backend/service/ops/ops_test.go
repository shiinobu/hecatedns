package ops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

type fixture struct {
	t   *testing.T
	svc *Service
	req string
	res string
	now time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, req: filepath.Join(root, "req"), res: filepath.Join(root, "result"), now: testNow}
	for _, dir := range []string{f.req, f.res} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.svc = New(Options{RequestDir: f.req, ResultDir: f.res, Now: func() time.Time { return f.now }})
	return f
}

func (f *fixture) result(action, body string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.res, action+".result"), []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func (f *fixture) finished(action, status string, exit int, ago time.Duration, output string) {
	f.t.Helper()
	end := f.now.Add(-ago)
	f.result(action, fmt.Sprintf("status=%s\nstarted=%s\nfinished=%s\nexit=%d\n---\n%s", status, stamp(end.Add(-3*time.Second)), stamp(end), exit, output))
}

func (f *fixture) status(action string) Status {
	f.t.Helper()
	for _, s := range f.svc.Status() {
		if s.Action == action {
			return s
		}
	}
	f.t.Fatalf("no status for %s", action)
	return Status{}
}

func TestRun_RefusesUnknownActionsAndADisabledService(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"", "reboot", "../health", "health ", "HEALTH", "renew-force/x"} {
		if err := f.svc.Run(name); !errors.Is(err, ErrUnknown) {
			t.Errorf("Run(%q) = %v, want ErrUnknown", name, err)
		}
	}
	off := New(Options{})
	if off.Enabled() {
		t.Error("a service without directories reports enabled")
	}
	if err := off.Run(Health); !errors.Is(err, ErrDisabled) {
		t.Errorf("Run on a disabled service = %v, want ErrDisabled", err)
	}
	if got := off.Status(); len(got) != 0 {
		t.Errorf("a disabled service lists %d actions, want none", len(got))
	}
}

func TestRun_DropsOneRequestFileAndRefusesASecondWhileItWaits(t *testing.T) {
	f := newFixture(t)

	if err := f.svc.Run(Backup); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.req, Backup)); err != nil {
		t.Fatalf("no request file: %v", err)
	}
	if got := f.status(Backup).State; got != StateQueued {
		t.Errorf("state = %q, want queued", got)
	}
	if err := f.svc.Run(Backup); !errors.Is(err, ErrBusy) {
		t.Errorf("second Run = %v, want ErrBusy", err)
	}
	entries, _ := os.ReadDir(f.req)
	if len(entries) != 1 {
		t.Errorf("request directory holds %d entries, want 1", len(entries))
	}
}

func TestStatus_ReadsTheResultTheRootSideWrote(t *testing.T) {
	f := newFixture(t)
	if got := f.status(Health).State; got != StateIdle {
		t.Errorf("no result: state = %q, want idle", got)
	}

	f.finished(Health, "ok", 0, time.Hour, "all good\n")
	s := f.status(Health)
	if s.State != StateOK || s.Exit != 0 || !s.Finished.Equal(f.now.Add(-time.Hour)) || !s.Started.Equal(f.now.Add(-time.Hour-3*time.Second)) {
		t.Errorf("ok result = %+v", s)
	}

	f.finished(Health, "failed", 3, time.Hour, "")
	if s := f.status(Health); s.State != StateFailed || s.Exit != 3 {
		t.Errorf("failed result = %+v", s)
	}

	f.result(Health, fmt.Sprintf("status=running\nstarted=%s\n---\n", stamp(f.now.Add(-time.Minute))))
	if s := f.status(Health); s.State != StateRunning {
		t.Errorf("running result = %+v", s)
	}
	if err := f.svc.Run(Health); !errors.Is(err, ErrBusy) {
		t.Errorf("Run while running = %v, want ErrBusy", err)
	}
}

func TestStatus_ARunningResultThatNeverEndedIsFailedNotAPermanentLock(t *testing.T) {
	f := newFixture(t)
	f.result(Health, fmt.Sprintf("status=running\nstarted=%s\n---\n", stamp(f.now.Add(-2*time.Hour))))

	if s := f.status(Health); s.State != StateFailed {
		t.Errorf("stale running state = %q, want failed", s.State)
	}
	if err := f.svc.Run(Health); err != nil {
		t.Errorf("Run after a stale running result = %v", err)
	}
}

func TestStatus_GarbageIsIdleNotAnError(t *testing.T) {
	f := newFixture(t)
	for _, body := range []string{"", "---\n", "status=\nexit=x\n", "nonsense", "status=exploded\n---\n", "status=ok\nfinished=yesterday\nexit=zero\n---\n"} {
		f.result(Renew, body)
		s := f.status(Renew)
		if s.State == StateRunning || s.State == StateQueued {
			t.Errorf("body %q gave state %q", body, s.State)
		}
		if s.CooldownSeconds != 0 {
			t.Errorf("body %q gave cooldown %d", body, s.CooldownSeconds)
		}
	}
}

func TestCooldown_HoldsFromTheLastFinishAndReleasesWithTheClock(t *testing.T) {
	f := newFixture(t)
	f.finished(Health, "ok", 0, 4*time.Second, "")

	s := f.status(Health)
	if s.CooldownSeconds != 6 {
		t.Errorf("cooldown_s = %d, want 6", s.CooldownSeconds)
	}
	if err := f.svc.Run(Health); !errors.Is(err, ErrCooldown) {
		t.Fatalf("Run inside the cooldown = %v, want ErrCooldown", err)
	}
	if _, err := os.Stat(filepath.Join(f.req, Health)); err == nil {
		t.Error("a request file was written during the cooldown")
	}

	f.now = f.now.Add(7 * time.Second)
	if err := f.svc.Run(Health); err != nil {
		t.Errorf("Run after the cooldown = %v", err)
	}
}

func TestCooldown_PerActionLengths(t *testing.T) {
	f := newFixture(t)
	want := map[string]time.Duration{Health: 10 * time.Second, Backup: time.Minute, RenewTest: time.Hour, Renew: time.Hour, RenewForce: 48 * time.Hour}
	for action, d := range want {
		f.finished(action, "ok", 0, 0, "")
		if got := time.Duration(f.status(action).CooldownSeconds) * time.Second; got != d {
			t.Errorf("%s cooldown = %v, want %v", action, got, d)
		}
	}
}

func TestOutput_ReturnsTheTextAfterTheSeparatorAndCapsIt(t *testing.T) {
	f := newFixture(t)
	if _, ok := f.svc.Output("reboot"); ok {
		t.Error("output for an unknown action")
	}
	if text, ok := f.svc.Output(Health); !ok || text != "" {
		t.Errorf("missing result: %q %v, want empty and ok", text, ok)
	}

	f.finished(Health, "ok", 0, time.Hour, "line one\n---\nline two\n")
	if text, _ := f.svc.Output(Health); text != "line one\n---\nline two\n" {
		t.Errorf("output = %q", text)
	}

	f.finished(Health, "ok", 0, time.Hour, strings.Repeat("x", 3*maxOutputBytes))
	text, _ := f.svc.Output(Health)
	if len(text) > maxOutputBytes+64 || !strings.Contains(text, "truncated") {
		t.Errorf("output length %d, want capped near %d with a truncation note", len(text), maxOutputBytes)
	}
}

func TestBackups_ListsOnlyWellFormedNamesNewestFirstAndCapped(t *testing.T) {
	f := newFixture(t)
	if got := f.svc.Backups(); len(got) != 0 {
		t.Errorf("no list file: %v", got)
	}

	var b strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "%d %d hecate-202610%02d-120000.tar.gz\n", testNow.Add(time.Duration(i)*time.Hour).Unix(), 1000+i, i+1)
	}
	b.WriteString("1 2 ../../etc/passwd\n9 9 hecate-x.tar.gz\nbroken\n\n-5 7 hecate-20261001-120000.tar.gz\n")
	if err := os.WriteFile(filepath.Join(f.res, "backups.list"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	got := f.svc.Backups()
	if len(got) != maxBackups {
		t.Fatalf("got %d backups, want %d", len(got), maxBackups)
	}
	if got[0].Name != "hecate-20261020-120000.tar.gz" || got[0].Bytes != 1019 || !got[0].Time.Equal(testNow.Add(19*time.Hour)) {
		t.Errorf("newest = %+v", got[0])
	}
	for i := 1; i < len(got); i++ {
		if got[i].Time.After(got[i-1].Time) {
			t.Errorf("not newest first at %d: %v after %v", i, got[i].Time, got[i-1].Time)
		}
	}
	for _, bk := range got {
		if strings.Contains(bk.Name, "/") || strings.Contains(bk.Name, "passwd") {
			t.Errorf("unsafe name listed: %q", bk.Name)
		}
	}
}
