package controller

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shiinobu/hecatedns/backend/dashboard"
)

const leakWindow = 5

func genKey(t *testing.T, dir string) (keyPath, hashPath string, stdout, stderr *bytes.Buffer, code int) {
	t.Helper()
	keyPath, hashPath = filepath.Join(dir, "dashboard-key.txt"), filepath.Join(dir, "key.sha256")
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	code = Execute([]string{"dashboard", "genkey", "-out", keyPath, "-hash-out", hashPath}, stdout, stderr)
	return keyPath, hashPath, stdout, stderr, code
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestDashboardGenKeyWritesKeyAndHashWithoutPrintingThem(t *testing.T) {
	keyPath, hashPath, stdout, stderr, code := genKey(t, t.TempDir())
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	key, err := dashboard.NormalizeKey(readText(t, keyPath))
	if err != nil {
		t.Fatalf("key file does not hold a valid key: %v", err)
	}
	hash := strings.TrimSpace(readText(t, hashPath))
	if hash != dashboard.HashKey(key) {
		t.Fatal("hash file does not match the key")
	}
	assertContains(t, "stdout", stdout.String(), []string{keyPath, hashPath})
	out := stdout.String() + stderr.String()
	for i := 0; i+leakWindow <= len(key); i++ {
		if strings.Contains(out, key[i:i+leakWindow]) {
			t.Fatal("output contains part of the key")
		}
	}
	if strings.Contains(out, hash) {
		t.Fatal("output contains the hash")
	}
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v", info.Mode().Perm())
	}
}

func TestDashboardGenKeyRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dashboard-key.txt", "keep")
	keyPath, hashPath, _, stderr, code := genKey(t, dir)
	if code != exitError || stderr.Len() == 0 {
		t.Fatalf("exit %d", code)
	}
	if readText(t, keyPath) != "keep" {
		t.Fatal("existing key file was changed")
	}
	if _, err := os.Stat(hashPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("hash file written after a refusal: %v", err)
	}
}

func TestDashboardHashKeyRoundTrip(t *testing.T) {
	keyPath, hashPath, _, stderr, code := genKey(t, t.TempDir())
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var stdout bytes.Buffer
	stderr.Reset()
	keyText := strings.ToLower(readText(t, keyPath))
	if code := runDashboard([]string{"hashkey"}, strings.NewReader(keyText), &stdout, stderr); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if stdout.String() != readText(t, hashPath) {
		t.Fatal("hashkey output differs from the genkey hash file")
	}
}

func TestDashboardHashKeyRejectsInvalidKey(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runDashboard([]string{"hashkey"}, strings.NewReader("not-a-key-UUUU\n"), &stdout, &stderr)
	if code != exitError || stdout.Len() != 0 || stderr.Len() == 0 || strings.Contains(stderr.String(), "UUUU") {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestDashboardUsage(t *testing.T) {
	for _, args := range [][]string{
		{"dashboard"}, {"dashboard", "run"}, {"dashboard", "nope"}, {"dashboard", "genkey"},
		{"dashboard", "genkey", "-out", "a"}, {"dashboard", "genkey", "-out", "a", "-hash-out", "a"},
		{"dashboard", "genkey", "-out", "a", "-hash-out", "b", "extra"}, {"dashboard", "hashkey", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		if code := Execute(args, &stdout, &stderr); code != exitUsage || stdout.Len() != 0 {
			t.Errorf("%v: exit %d stdout %q", args, code, stdout.String())
		}
	}
}
