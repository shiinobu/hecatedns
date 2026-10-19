package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var logZone = time.FixedZone("WIB", 7*60*60)

// logFixture is a config whose log directory holds a few days of records.
type logFixture struct {
	config string
	now    time.Time
}

func logLine(ts, qname, action, extra string) string {
	return `{"ts":"` + ts + `","client":"192.168.1.23","proto":"udp","qname":"` + qname + `","qtype":"A","action":"` + action + `"` + extra + `,"ms":0}`
}

func newLogFixture(t *testing.T) logFixture {
	t.Helper()
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	yesterday := logLine("2026-10-17T20:00:00.000+07:00", "yesterday.example.com", "pass", `,"rcode":"NOERROR"`)
	today := strings.Join([]string{
		logLine("2026-10-18T09:00:00.000+07:00", "morning.example.com", "pass", `,"rcode":"NOERROR"`),
		logLine("2026-10-18T14:32:07.412+07:00", "ads.contoh.com", "block", `,"reason":"list","source":"hagezi-popups"`),
		logLine("2026-10-18T14:32:20.000+07:00", "redirect.situs.com", "block", `,"reason":"rule","source":"block redirect.situs.com"`),
		logLine("2026-10-18T14:55:00.000+07:00", "www.contoh.com", "pass", `,"rcode":"NOERROR"`),
		logLine("2026-10-18T14:58:30.000+07:00", "recent.example.com", "pass", `,"rcode":"NOERROR"`),
		"damaged line",
	}, "\n")
	for name, content := range map[string]string{"queries-20261017.jsonl": yesterday, "queries-20261018.jsonl": today} {
		if err := os.WriteFile(filepath.Join(logDir, name), []byte(content+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return logFixture{
		config: writeFile(t, dir, "hecate.yaml", "log:\n  enabled: true\n  dir: logs\n"),
		now:    time.Date(2026, 10, 18, 15, 0, 0, 0, logZone),
	}
}

func (f logFixture) run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = runLogAt(append([]string{"-config", f.config}, args...), &out, &errOut, f.now)
	return code, out.String(), errOut.String()
}

func TestLog_DefaultsToTheLastTenMinutes(t *testing.T) {
	f := newLogFixture(t)

	code, stdout, stderr := f.run(t)

	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	for _, recent := range []string{"recent.example.com", "www.contoh.com"} {
		if !strings.Contains(stdout, recent) {
			t.Errorf("stdout lacks %q from the last ten minutes:\n%s", recent, stdout)
		}
	}
	for _, old := range []string{"ads.contoh.com", "morning.example.com", "yesterday"} {
		if strings.Contains(stdout, old) {
			t.Errorf("stdout holds %q, older than ten minutes:\n%s", old, stdout)
		}
	}
}

func TestLog_Filters(t *testing.T) {
	f := newLogFixture(t)
	tests := []struct {
		name    string
		args    []string
		want    []string
		notWant []string
	}{
		{"since a day", []string{"-since", "1d"}, []string{"yesterday.example.com", "morning.example.com", "recent.example.com"}, nil},
		{"since two hours", []string{"-since", "2h"}, []string{"www.contoh.com", "recent.example.com", "ads.contoh.com"}, []string{"morning.example.com", "yesterday"}},
		{"blocked only", []string{"-since", "1d", "-blocked"}, []string{"ads.contoh.com", "redirect.situs.com"}, []string{"morning", "www.contoh.com"}},
		{"grep", []string{"-since", "1d", "-grep", "POPUPS"}, []string{"ads.contoh.com"}, []string{"redirect.situs.com", "morning"}},
		{"around a time", []string{"-around", "14:32", "-window", "30s"}, []string{"ads.contoh.com", "redirect.situs.com"}, []string{"www.contoh.com", "morning"}},
		{"around with a narrow window", []string{"-around", "14:32", "-window", "5s"}, nil, []string{"ads.contoh.com", "redirect.situs.com"}},
		{"around a time later than now means yesterday", []string{"-around", "20:00", "-window", "1m"}, []string{"yesterday.example.com"}, []string{"morning"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := f.run(t, tt.args...)
			if code != exitOK {
				t.Fatalf("exit = %d, stderr = %q", code, stderr)
			}
			assertContains(t, "stdout", stdout, tt.want)
			for _, unwanted := range tt.notWant {
				if strings.Contains(stdout, unwanted) {
					t.Errorf("stdout holds %q:\n%s", unwanted, stdout)
				}
			}
		})
	}
}

func TestLog_PrintsReadableLinesAndASummary(t *testing.T) {
	f := newLogFixture(t)

	_, stdout, stderr := f.run(t, "-around", "14:32", "-window", "30s")

	want := "2026-10-18 14:32:07.412 BLOK  192.168.1.23 A ads.contoh.com list:hagezi-popups\n"
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout, want)
	}
	assertContains(t, "stderr", stderr, []string{"2 catatan", "1 baris rusak"})
}

func TestLog_NothingFound(t *testing.T) {
	f := newLogFixture(t)

	code, stdout, stderr := f.run(t, "-since", "1d", "-grep", "no-such-domain")

	if code != exitOK || stdout != "" || !strings.Contains(stderr, "tidak ada catatan") {
		t.Errorf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestLog_MissingLogDirectory(t *testing.T) {
	dir := t.TempDir()
	config := writeFile(t, dir, "hecate.yaml", "log:\n  dir: nothing-here\n")
	var out, errOut bytes.Buffer

	code := runLogAt([]string{"-config", config}, &out, &errOut, time.Now())

	if code != exitOK || !strings.Contains(errOut.String(), "tidak ada catatan") {
		t.Errorf("exit = %d, stderr = %q", code, errOut.String())
	}
}

func TestLog_MentionsADisabledLog(t *testing.T) {
	dir := t.TempDir()
	config := writeFile(t, dir, "hecate.yaml", "log:\n  enabled: false\n")
	var out, errOut bytes.Buffer

	code := runLogAt([]string{"-config", config}, &out, &errOut, time.Now())

	if code != exitOK || !strings.Contains(errOut.String(), "log.enabled") {
		t.Errorf("exit = %d, stderr = %q", code, errOut.String())
	}
}

func TestLog_UsageErrors(t *testing.T) {
	f := newLogFixture(t)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"since and around together", []string{"-since", "1h", "-around", "14:32"}, "bersamaan"},
		{"bad since", []string{"-since", "banyak"}, "tidak sah"},
		{"bad around", []string{"-around", "25:99"}, "JJ:MM"},
		{"bad window", []string{"-around", "14:32", "-window", "0s"}, "jendela"},
		{"unknown flag", []string{"-bogus"}, "bogus"},
		{"stray argument", []string{"stray"}, "stray"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := f.run(t, tt.args...)
			if code != exitUsage || !strings.Contains(stderr, tt.want) {
				t.Errorf("exit = %d, stderr = %q; want exit %d and %q", code, stderr, exitUsage, tt.want)
			}
		})
	}
}

func TestLog_ConfigProblems(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	var out, errOut bytes.Buffer

	code := runLogAt([]string{"-config", missing}, &out, &errOut, time.Now())

	if code != exitError || !strings.Contains(errOut.String(), missing) {
		t.Errorf("exit = %d, stderr = %q", code, errOut.String())
	}
}

func TestLog_DispatchedByRun(t *testing.T) {
	f := newLogFixture(t)
	var out, errOut bytes.Buffer

	code := run([]string{"log", "-config", f.config, "-since", "1d"}, &out, &errOut)

	if code != exitOK {
		t.Errorf("exit = %d, stderr = %q", code, errOut.String())
	}
}
