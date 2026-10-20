package dashboard

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const validKeyText = "0123456789ABCDEFGHJKMNPQRS"

func writeKeyHash(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateKey_ShapeAndUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for range 64 {
		key, err := GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		if len(key) != keyLength || strings.Trim(key, keyAlphabet) != "" {
			t.Fatalf("generated key has a bad shape (length %d)", len(key))
		}
		if seen[key] {
			t.Fatal("generated a duplicate key")
		}
		seen[key] = true
	}
}

func TestNormalizeKey(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"canonical", validKeyText, validKeyText},
		{"lower case", strings.ToLower(validKeyText), validKeyText},
		{"groups and spaces", " 0123-4567 89AB-CDEF GHJK-MNPQ RS \n", validKeyText},
		{"ambiguous letters", "oiL3456789abcdefghjkmnpqrs", "0113456789ABCDEFGHJKMNPQRS"},
		{"letter U", "0123456789ABCDEFGHJKMNPQRU", ""},
		{"symbol", "0123456789ABCDEFGHJKMNPQR!", ""},
		{"non ascii", "0123456789ABCDEFGHJKMNPQRé", ""},
		{"inner tab", "0123456789ABC\tDEFGHJKMNPQRS", ""},
		{"too short", validKeyText[:25], ""},
		{"too long", validKeyText + "T", ""},
		{"empty", "", ""},
		{"only separators", " - - ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeKey(tc.in)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("accepted %q", tc.in)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestHashKey_IsLowercaseHexSHA256(t *testing.T) {
	sum := sha256.Sum256([]byte(validKeyText))
	if got := HashKey(validKeyText); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("got %q", got)
	}
}

func TestKeyHashFile_ReloadsWhenMtimeChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.sha256")
	first, second := HashKey(validKeyText), HashKey("ZZZZZZZZZZZZZZZZZZZZZZZZZZ")
	base := time.Now().Add(-time.Hour)
	writeKeyHash(t, path, first+"\n", base)
	kf := newKeyHashFile(path, slog.New(slog.DiscardHandler))
	if kf.current() != first {
		t.Fatal("first hash not loaded")
	}
	writeKeyHash(t, path, strings.ToUpper(second)+"\n", base.Add(time.Minute))
	if kf.current() != second {
		t.Fatal("changed hash not reloaded")
	}
}

func TestKeyHashFile_FailsClosedWithOneWarningPerProblem(t *testing.T) {
	var buf bytes.Buffer
	path := filepath.Join(t.TempDir(), "key.sha256")
	kf := newKeyHashFile(path, slog.New(slog.NewTextHandler(&buf, nil)))
	base := time.Now().Add(-time.Hour)
	steps := []struct {
		content string
		problem string
	}{
		{"", "missing"},
		{" \n", "empty"},
		{"NOTAHASH SECRETISH\n", "malformed"},
	}
	for i, step := range steps {
		if step.problem != "missing" {
			writeKeyHash(t, path, step.content, base.Add(time.Duration(i)*time.Minute))
		}
		for range 3 {
			if got := kf.current(); got != "" {
				t.Fatalf("%s: got a hash", step.problem)
			}
		}
		if !strings.Contains(buf.String(), "problem="+step.problem) {
			t.Fatalf("%s not logged: %s", step.problem, buf.String())
		}
	}
	writeKeyHash(t, path, HashKey(validKeyText), base.Add(time.Hour))
	if kf.current() != HashKey(validKeyText) {
		t.Fatal("recovered hash not loaded")
	}
	if n := strings.Count(buf.String(), "level=WARN"); n != len(steps) || strings.Contains(buf.String(), "SECRETISH") {
		t.Fatalf("want %d warnings without file content, got %d: %s", len(steps), n, buf.String())
	}
}

func TestLoadSecret_CreatesPrivateFileOnceAndReusesIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, "cookie.secret")
	first, err := loadSecret(path)
	if err != nil || len(first) != secretBytes {
		t.Fatalf("got %d bytes, %v", len(first), err)
	}
	second, err := loadSecret(path)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("secret not reused: %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for p, want := range map[string]os.FileMode{path: 0o600, dir: 0o700} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s: mode %v", p, info.Mode().Perm())
		}
	}
}

func TestNewAuth_RefusesShortSecret(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "cookie.secret")
	if err := os.WriteFile(secret, make([]byte, secretBytes-1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewAuth(AuthOptions{KeyHashFile: filepath.Join(dir, "key.sha256"), SecretFile: secret}); err == nil {
		t.Fatal("a short secret was accepted")
	}
}
