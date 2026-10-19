package controller

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shiinobu/hecatedns/backend/engine"
)

type ruleFixture struct {
	config string
	rules  string
}

func newRuleFixture(t *testing.T) ruleFixture {
	t.Helper()
	dir := t.TempDir()
	return ruleFixture{
		config: writeFile(t, dir, "hecate.yaml", "rules:\n  file: data/rules.txt\n  poll_interval: 2s\n"),
		rules:  filepath.Join(dir, "data", "rules.txt"),
	}
}

func (f ruleFixture) run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Execute(append([]string{"rule"}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

func (f ruleFixture) content(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(f.rules)
	if err != nil {
		t.Fatalf("read rules file: %v", err)
	}
	return string(data)
}

func (f ruleFixture) seed(t *testing.T, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.rules), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.rules, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRuleAdd_CreatesFileAndDirectory(t *testing.T) {
	f := newRuleFixture(t)

	code, stdout, stderr := f.run(t, "-config", f.config, "add", "block", "Contoh.TEST.")

	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if got := f.content(t); got != "block contoh.test\n" {
		t.Errorf("rules file = %q", got)
	}
	assertContains(t, "stdout", stdout, []string{"aturan ditambahkan: block contoh.test", "tanpa restart"})
}

func TestRuleAdd_ConfigFlagMayComeAfterTheArguments(t *testing.T) {
	f := newRuleFixture(t)

	for _, args := range [][]string{
		{"add", "allow", "a.example.com", "-config", f.config},
		{"add", "allow", "b.example.com", "--config", f.config},
		{"add", "allow", "c.example.com", "-config=" + f.config},
	} {
		if code, _, stderr := f.run(t, args...); code != exitOK {
			t.Fatalf("args %v: exit = %d, stderr = %q", args, code, stderr)
		}
	}
	if got := f.content(t); got != "allow a.example.com\nallow b.example.com\nallow c.example.com\n" {
		t.Errorf("rules file = %q", got)
	}
}

func TestRuleAdd_ReplacesTheOldRuleOfTheSameDomain(t *testing.T) {
	f := newRuleFixture(t)
	f.seed(t, "# my rules\nblock ads.example.com\nblock other.example.com\n")

	code, stdout, stderr := f.run(t, "-config", f.config, "add", "allow", "ADS.example.com")

	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	want := "# my rules\nblock other.example.com\nallow ads.example.com\n"
	if got := f.content(t); got != want {
		t.Errorf("rules file = %q, want %q", got, want)
	}
	assertContains(t, "stdout", stdout, []string{"menggantikan 1 baris"})
}

func TestRuleAdd_KeepsCommentsBrokenLinesBOMAndCRLF(t *testing.T) {
	f := newRuleFixture(t)
	f.seed(t, "\xef\xbb\xbf# header\r\nnonsense line\r\nblock keep.example.com # why\r\n")

	if code, _, stderr := f.run(t, "-config", f.config, "add", "block", "new.example.com"); code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}

	want := "\xef\xbb\xbf# header\r\nnonsense line\r\nblock keep.example.com # why\r\nblock new.example.com\r\n"
	if got := f.content(t); got != want {
		t.Errorf("rules file = %q, want %q", got, want)
	}
}

func TestRuleAdd_ReplacesARuleOnTheFirstLineBehindABOM(t *testing.T) {
	f := newRuleFixture(t)
	f.seed(t, "\xef\xbb\xbfblock first.example.com\n")

	if code, _, stderr := f.run(t, "-config", f.config, "add", "allow", "first.example.com"); code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if got := f.content(t); got != "allow first.example.com\n" {
		t.Errorf("rules file = %q", got)
	}
}

func TestRuleAdd_TakesEffectInTheServersReader(t *testing.T) {
	f := newRuleFixture(t)
	if code, _, stderr := f.run(t, "-config", f.config, "add", "block", "contoh.test"); code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}

	rules := engine.NewRulesFile(f.rules, nil)
	if _, err := rules.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, ok := rules.Snapshot().Blocked("www.contoh.test"); !ok {
		t.Error("the engine's rules reader does not see the rule the CLI wrote")
	}

	if code, _, stderr := f.run(t, "-config", f.config, "add", "allow", "contoh.test"); code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := rules.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, ok := rules.Snapshot().Allowed("www.contoh.test"); !ok {
		t.Error("an allow rule added over a block rule is not visible to the reader")
	}
}

func TestRuleAdd_LeavesNoTemporaryFiles(t *testing.T) {
	f := newRuleFixture(t)
	for _, domain := range []string{"a.example.com", "b.example.com", "c.example.com"} {
		if code, _, stderr := f.run(t, "-config", f.config, "add", "block", domain); code != exitOK {
			t.Fatalf("exit = %d, stderr = %q", code, stderr)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(f.rules))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "rules.txt" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("directory holds %v, want only rules.txt", names)
	}
}

func TestRuleAdd_RejectsBadInput(t *testing.T) {
	f := newRuleFixture(t)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unknown action", []string{"add", "deny", "a.example.com"}, "block atau allow"},
		{"single label domain", []string{"add", "block", "com"}, "ditolak"},
		{"ip address", []string{"add", "block", "10.0.0.1"}, "ditolak"},
		{"domain with a comment", []string{"add", "block", "a.example.com # x"}, "ditolak"},
		{"url", []string{"add", "block", "https://a.example.com/x"}, "ditolak"},
		{"empty domain", []string{"add", "block", ""}, "ditolak"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := f.run(t, append([]string{"-config", f.config}, tt.args...)...)
			if code != exitError || !strings.Contains(stderr, tt.want) {
				t.Errorf("exit = %d, stderr = %q; want exit %d and %q", code, stderr, exitError, tt.want)
			}
		})
	}
	if _, err := os.Stat(f.rules); err == nil {
		t.Error("a rejected rule still created the rules file")
	}
}

func TestRuleRemove(t *testing.T) {
	f := newRuleFixture(t)
	f.seed(t, "# keep me\nblock a.example.com\nallow a.example.com\nblock b.example.com\n")

	code, stdout, stderr := f.run(t, "-config", f.config, "remove", "A.example.com")

	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if got := f.content(t); got != "# keep me\nblock b.example.com\n" {
		t.Errorf("rules file = %q", got)
	}
	assertContains(t, "stdout", stdout, []string{"2 baris", "a.example.com"})
}

func TestRuleRemove_UnknownDomainFails(t *testing.T) {
	f := newRuleFixture(t)
	f.seed(t, "block a.example.com\n")

	code, _, stderr := f.run(t, "-config", f.config, "remove", "zzz.example.com")

	if code != exitError || !strings.Contains(stderr, "tidak ada aturan") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
	if got := f.content(t); got != "block a.example.com\n" {
		t.Errorf("rules file changed to %q", got)
	}
}

func TestRuleRemove_MissingFileFails(t *testing.T) {
	f := newRuleFixture(t)

	code, _, stderr := f.run(t, "-config", f.config, "remove", "a.example.com")

	if code != exitError || !strings.Contains(stderr, "tidak ada aturan") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestRuleList(t *testing.T) {
	f := newRuleFixture(t)
	f.seed(t, "# c\nblock a.example.com\nbroken line here\n\nALLOW B.example.com # note\n")

	code, stdout, stderr := f.run(t, "-config", f.config, "list")

	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if stdout != "block a.example.com\nallow b.example.com\n" {
		t.Errorf("stdout = %q", stdout)
	}
	assertContains(t, "stderr", stderr, []string{"baris 3 dilewati"})
}

func TestRuleList_EmptyAndMissing(t *testing.T) {
	f := newRuleFixture(t)

	code, stdout, stderr := f.run(t, "-config", f.config, "list")

	if code != exitOK || stdout != "" || !strings.Contains(stderr, "belum ada aturan") {
		t.Errorf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestRule_UsageErrors(t *testing.T) {
	f := newRuleFixture(t)
	tests := [][]string{
		nil,
		{"-config", f.config},
		{"add"},
		{"add", "block"},
		{"add", "block", "a.example.com", "extra"},
		{"remove"},
		{"remove", "a.example.com", "extra"},
		{"list", "extra"},
		{"bogus"},
		{"-config"},
	}
	for _, args := range tests {
		code, _, stderr := f.run(t, args...)
		if code != exitUsage || !strings.Contains(stderr, "penggunaan") && !strings.Contains(stderr, "hecate rule") {
			t.Errorf("args %v: exit = %d, stderr = %q; want a usage error", args, code, stderr)
		}
	}
}

func TestRule_ConfigProblems(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.yaml")
	invalid := writeFile(t, dir, "bad.yaml", "backend: nope\n")

	var out, errOut bytes.Buffer
	if code := Execute([]string{"rule", "-config", missing, "list"}, &out, &errOut); code != exitError || !strings.Contains(errOut.String(), missing) {
		t.Errorf("missing config: exit = %d, stderr = %q", code, errOut.String())
	}
	errOut.Reset()
	if code := Execute([]string{"rule", "-config", invalid, "list"}, &out, &errOut); code != exitError || !strings.Contains(errOut.String(), "backend") {
		t.Errorf("invalid config: exit = %d, stderr = %q", code, errOut.String())
	}
}

func TestRule_RefusesAFileLargerThanTheServerReads(t *testing.T) {
	f := newRuleFixture(t)
	f.seed(t, strings.Repeat("# padding padding padding padding padding\n", engine.MaxRulesFileBytes/42+100))

	code, _, stderr := f.run(t, "-config", f.config, "add", "block", "a.example.com")

	if code != exitError || !strings.Contains(stderr, "lebih besar") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestRule_RulesPathIsADirectory(t *testing.T) {
	f := newRuleFixture(t)
	if err := os.MkdirAll(f.rules, 0o700); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := f.run(t, "-config", f.config, "list")

	if code != exitError || !strings.Contains(stderr, "direktori") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestRuleList_ShowsNoRuleThatTheServerWouldSkipAsTooLong(t *testing.T) {
	f := newRuleFixture(t)
	f.seed(t, "block short.example\nblock longline.example # "+strings.Repeat("x", engine.MaxRuleLineBytes)+"\n")

	code, stdout, stderr := f.run(t, "-config", f.config, "list")

	if code != exitOK || stdout != "block short.example\n" {
		t.Errorf("exit = %d, stdout = %q; the server ignores a line of %d bytes or more, so it is not an active rule", code, stdout, engine.MaxRuleLineBytes)
	}
	assertContains(t, "stderr", stderr, []string{"baris 2 dilewati"})
}

func TestRuleAdd_RefusesToGrowTheFileBeyondWhatTheServerReads(t *testing.T) {
	f := newRuleFixture(t)

	body := strings.Repeat("#", engine.MaxRulesFileBytes-11) + "\n"
	f.seed(t, body)

	code, _, stderr := f.run(t, "-config", f.config, "add", "block", "a.example.com")

	if code != exitError || !strings.Contains(stderr, "terlalu besar") {
		t.Errorf("exit = %d, stderr = %q; the new rule would land beyond the 4 MiB the server reads", code, stderr)
	}
	if got := f.content(t); got != body {
		t.Error("a refused add still changed the rules file")
	}
}

func TestRuleAdd_NonASCIIDomainGetsAPunycodeHint(t *testing.T) {
	f := newRuleFixture(t)

	code, _, stderr := f.run(t, "-config", f.config, "add", "block", "bücher.example")

	if code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	assertContains(t, "stderr", stderr, []string{"punycode", "xn--"})
}
