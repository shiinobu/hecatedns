package ops

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type scriptFixture struct {
	t       *testing.T
	script  string
	req     string
	res     string
	backups string
	svc     *Service
}

func newScriptFixture(t *testing.T) *scriptFixture {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX sh available")
	}
	root := t.TempDir()
	f := &scriptFixture{
		t:       t,
		script:  filepath.ToSlash(filepath.Join("..", "..", "..", "deploy", "ops", "run.sh")),
		req:     filepath.Join(root, "req"),
		res:     filepath.Join(root, "result"),
		backups: filepath.Join(root, "backups"),
	}
	for _, dir := range []string{f.req, f.res, f.backups} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.svc = New(Options{RequestDir: f.req, ResultDir: f.res})
	return f
}

func (f *scriptFixture) run(action string, env ...string) (int, string) {
	return f.exec([]string{action}, env...)
}

func (f *scriptFixture) exec(args []string, env ...string) (int, string) {
	f.t.Helper()
	cmd := exec.Command("sh", append([]string{f.script}, args...)...)
	cmd.Env = append(os.Environ(),
		"HECATE_OPS_REQ="+filepath.ToSlash(f.req),
		"HECATE_OPS_RESULT="+filepath.ToSlash(f.res),
		"HECATE_BACKUP_DIR="+filepath.ToSlash(f.backups),
	)
	cmd.Env = append(cmd.Env, env...)
	if !slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, "HECATE_OPS_TEST=") }) {
		cmd.Env = append(cmd.Env, "HECATE_OPS_TEST=1")
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		f.t.Fatalf("running the script: %v", err)
	}
	return code, string(out)
}

func (f *scriptFixture) request(action string) {
	f.t.Helper()
	if err := f.svc.Run(action); err != nil {
		f.t.Fatalf("Run(%s) = %v", action, err)
	}
}

func (f *scriptFixture) status(action string) Status {
	f.t.Helper()
	for _, s := range f.svc.Status() {
		if s.Action == action {
			return s
		}
	}
	f.t.Fatalf("no status for %s", action)
	return Status{}
}

func TestScript_RunsTheActionAndWritesAResultTheServiceReads(t *testing.T) {
	f := newScriptFixture(t)
	f.request(Health)

	code, out := f.run(Health, "HECATE_OPS_CMD_HEALTH=printf 'report line\nname k7x9p2qm4t8v3w6zq.example.org here\n'")

	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(f.req, Health)); err == nil {
		t.Error("the request file was left behind")
	}
	s := f.status(Health)
	if s.State != StateOK || s.Exit != 0 || s.Started.IsZero() || s.Finished.IsZero() {
		t.Fatalf("status = %+v", s)
	}
	text, _ := f.svc.Output(Health)
	if !strings.Contains(text, "report line") || !strings.Contains(text, "<name>") || strings.Contains(text, "k7x9p2qm4t8v3w6zq") {
		t.Errorf("output = %q, want the report with the long name redacted", text)
	}
	entries, _ := os.ReadDir(f.res)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("temporary file %s left behind", e.Name())
		}
	}
}

func TestScript_AFailingCommandIsRecordedAsFailedWithItsExitCode(t *testing.T) {
	f := newScriptFixture(t)

	code, out := f.run(Renew, "HECATE_OPS_CMD_RENEW=echo boom; exit 3")

	if code != 0 {
		t.Fatalf("the wrapper itself exited %d: %s", code, out)
	}
	if s := f.status(Renew); s.State != StateFailed || s.Exit != 3 {
		t.Errorf("status = %+v, want failed with exit 3", s)
	}
	if text, _ := f.svc.Output(Renew); !strings.Contains(text, "boom") {
		t.Errorf("output = %q", text)
	}
}

func TestScript_RefusesAnythingOutsideTheAllowList(t *testing.T) {
	f := newScriptFixture(t)
	for _, name := range []string{"", "reboot", "../health", "health;id", "HEALTH"} {
		code, _ := f.run(name)
		if code != 2 {
			t.Errorf("action %q exited %d, want 2", name, code)
		}
	}
	entries, _ := os.ReadDir(f.res)
	if len(entries) != 0 {
		t.Errorf("a refused action wrote %d files", len(entries))
	}
}

func TestScript_CooldownKeepsTheLastResultAndRunsNothing(t *testing.T) {
	f := newScriptFixture(t)
	f.run(Backup, "HECATE_BACKUP_CMD=echo first")
	first, _ := f.svc.Output(Backup)

	f.request(Health)
	f.run(Health, "HECATE_OPS_CMD_HEALTH=echo one")
	if err := os.WriteFile(filepath.Join(f.req, Health), nil, 0o660); err != nil {
		t.Fatal(err)
	}
	code, _ := f.run(Health, "HECATE_OPS_CMD_HEALTH=echo two")

	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if text, _ := f.svc.Output(Health); !strings.Contains(text, "one") || strings.Contains(text, "two") {
		t.Errorf("the second run inside the cooldown changed the result: %q", text)
	}
	if _, err := os.Stat(filepath.Join(f.req, Health)); err == nil {
		t.Error("the request file survived the skipped run")
	}
	if again, _ := f.svc.Output(Backup); again != first {
		t.Errorf("another action's result changed: %q", again)
	}
}

func TestScript_RemovesAPlantedSymlinkWithoutFollowingIt(t *testing.T) {
	f := newScriptFixture(t)
	target := filepath.Join(t.TempDir(), "precious")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(f.req, Health)); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}

	f.run(Health, "HECATE_OPS_CMD_HEALTH=echo ok")

	if _, err := os.Lstat(filepath.Join(f.req, Health)); err == nil {
		t.Error("the planted symlink is still there")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "keep" {
		t.Errorf("the symlink target changed: %q %v", data, err)
	}
}

func TestScript_CapsTheOutputAtSixtyFourKiB(t *testing.T) {
	f := newScriptFixture(t)

	f.run(Health, "HECATE_OPS_CMD_HEALTH=head -c 300000 /dev/zero | tr '\\0' x")

	text, _ := f.svc.Output(Health)
	if len(text) == 0 || len(text) > maxOutputBytes+len(truncatedNote) {
		t.Errorf("output length = %d, want between 1 and about %d", len(text), maxOutputBytes)
	}
	if f.status(Health).State != StateOK {
		t.Errorf("status = %+v", f.status(Health))
	}
}

func TestScript_BackupRewritesTheListNewestFirst(t *testing.T) {
	f := newScriptFixture(t)
	for _, name := range []string{"hecate-20261018-100000.tar.gz", "hecate-20261020-182812.tar.gz", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(f.backups, name), []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	older := filepath.Join(f.backups, "hecate-20261018-100000.tar.gz")
	if err := os.Chtimes(older, testNow.AddDate(0, 0, -2), testNow.AddDate(0, 0, -2)); err != nil {
		t.Fatal(err)
	}

	code, out := f.run(Backup, "HECATE_BACKUP_CMD=echo done")

	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	got := f.svc.Backups()
	if len(got) != 2 || got[0].Name != "hecate-20261020-182812.tar.gz" || got[1].Name != "hecate-20261018-100000.tar.gz" || got[0].Bytes != 4 {
		t.Errorf("backups = %+v", got)
	}

	if err := os.Remove(filepath.Join(f.backups, "hecate-20261020-182812.tar.gz")); err != nil {
		t.Fatal(err)
	}
	f.exec([]string{Backup, "--list"})
	if got := f.svc.Backups(); len(got) != 1 {
		t.Errorf("after --list: %+v", got)
	}
}
