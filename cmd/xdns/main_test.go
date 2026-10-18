package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const exampleConfig = "../../configs/xdns.example.yaml"

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.yaml")
	invalid := writeFile(t, dir, "invalid.yaml", "backend: nope\n")
	empty := writeFile(t, dir, "empty.yaml", "")

	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  []string
		wantErr  []string
	}{
		{"no arguments", nil, exitUsage, nil, []string{"penggunaan"}},
		{"unknown command", []string{"bogus"}, exitUsage, nil, []string{"bogus"}},
		{"run with a missing default config", []string{"run"}, exitError, nil, []string{"xdns.yaml"}},
		{"log with a missing default config", []string{"log"}, exitError, nil, []string{"xdns.yaml"}},
		{"rule without a subcommand shows usage", []string{"rule"}, exitUsage, nil, []string{"penggunaan", "xdns rule add"}},
		{"lists is a stub", []string{"lists"}, exitUsage, nil, []string{"belum diimplementasi"}},
		{"check example config", []string{"check", "-config", exampleConfig}, exitOK,
			[]string{"konfigurasi valid", "backend: dnsproxy", "daftar aktif: 3"}, nil},
		{"check missing file", []string{"check", "-config", missing}, exitError, nil, []string{missing}},
		{"check invalid config", []string{"check", "-config", invalid}, exitError, nil, []string{"backend"}},
		{"check empty config warns", []string{"check", "-config", empty}, exitOK, []string{"peringatan"}, nil},
		{"check a directory", []string{"check", "-config", dir}, exitError, nil, []string{"xdns check"}},
		{"check default path is xdns.yaml", []string{"check"}, exitError, nil, []string{"xdns.yaml"}},
		{"check unknown flag", []string{"check", "-nope"}, exitUsage, nil, []string{"nope"}},
		{"check positional argument", []string{"check", "other.yaml"}, exitUsage, nil, []string{"other.yaml"}},
		{"check extra argument after flag", []string{"check", "-config", exampleConfig, "extra"}, exitUsage, nil, []string{"extra"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(tc.args, &stdout, &stderr)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %q)", code, tc.wantCode, stderr.String())
			}
			assertContains(t, "stdout", stdout.String(), tc.wantOut)
			assertContains(t, "stderr", stderr.String(), tc.wantErr)
		})
	}
}

func assertContains(t *testing.T, stream, got string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%s = %q, want it to contain %q", stream, got, want)
		}
	}
}

func TestUsageListsOnlyWhatIsStillMissing(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(nil, &bytes.Buffer{}, &stderr); code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
	for _, line := range strings.Split(stderr.String(), "\n") {
		for _, done := range []string{"check", "run", "log", "rule"} {
			if strings.HasPrefix(strings.TrimSpace(line), done+" ") && strings.Contains(line, "belum diimplementasi") {
				t.Errorf("usage claims %q is not implemented: %q", done, line)
			}
		}
	}
	assertContains(t, "stderr", stderr.String(), []string{"lists"})
}
