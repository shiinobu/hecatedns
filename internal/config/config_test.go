package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const exampleConfigPath = "../../configs/xdns.example.yaml"

// writeConfig writes content to a fresh temp directory and returns the file
// path and the directory.
func writeConfig(t *testing.T, content string) (path, dir string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "xdns.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path, dir
}

func mustLoad(t *testing.T, content string) Config {
	t.Helper()
	path, _ := writeConfig(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestLoad_ExampleConfig(t *testing.T) {
	cfg, err := Load(exampleConfigPath)
	if err != nil {
		t.Fatalf("Load example: %v", err)
	}
	if cfg.Backend != BackendDNSProxy {
		t.Errorf("Backend = %q, want %q", cfg.Backend, BackendDNSProxy)
	}
	if len(cfg.Lists.Sources) != 3 {
		t.Fatalf("len(Sources) = %d, want 3", len(cfg.Lists.Sources))
	}
	if cfg.Lists.Sources[0].Name != "hagezi-pro" {
		t.Errorf("Sources[0].Name = %q, want hagezi-pro", cfg.Lists.Sources[0].Name)
	}
	if cfg.Block.TTL != 10*time.Second {
		t.Errorf("Block.TTL = %v, want 10s", cfg.Block.TTL)
	}
}

func TestLoad_DefaultsForMissingKeys(t *testing.T) {
	cfg := mustLoad(t, "backend: native\n")

	if cfg.Backend != BackendNative {
		t.Errorf("Backend = %q, want native", cfg.Backend)
	}
	if want := []string{"127.0.0.1:5300"}; !reflect.DeepEqual(cfg.Listen, want) {
		t.Errorf("Listen = %v, want %v", cfg.Listen, want)
	}
	if cfg.Block.Mode != ModeNullIP {
		t.Errorf("Block.Mode = %q, want null_ip", cfg.Block.Mode)
	}
	if cfg.Block.TTL != 10*time.Second {
		t.Errorf("Block.TTL = %v, want 10s", cfg.Block.TTL)
	}
	if cfg.Cache.SizeMB != 16 {
		t.Errorf("Cache.SizeMB = %d, want 16", cfg.Cache.SizeMB)
	}
	if cfg.Lists.UpdateInterval != 24*time.Hour {
		t.Errorf("Lists.UpdateInterval = %v, want 24h", cfg.Lists.UpdateInterval)
	}
	if cfg.Rules.PollInterval != 2*time.Second {
		t.Errorf("Rules.PollInterval = %v, want 2s", cfg.Rules.PollInterval)
	}
	if cfg.Log.RetentionDays != 7 {
		t.Errorf("Log.RetentionDays = %d, want 7", cfg.Log.RetentionDays)
	}
}

func TestLoad_UserListReplacesDefault(t *testing.T) {
	cfg := mustLoad(t, "listen: [\"192.168.1.10:53\"]\n")

	if want := []string{"192.168.1.10:53"}; !reflect.DeepEqual(cfg.Listen, want) {
		t.Errorf("Listen = %v, want exactly %v", cfg.Listen, want)
	}
}

func TestLoad_EmptyFileUsesDefaults(t *testing.T) {
	cfg := mustLoad(t, "")

	if cfg.Backend != BackendDNSProxy {
		t.Errorf("Backend = %q, want dnsproxy", cfg.Backend)
	}
}

func TestLoad_BOMAndCRLF(t *testing.T) {
	content := "\xef\xbb\xbfbackend: native\r\nlisten:\r\n  - 127.0.0.1:5300\r\n"
	cfg := mustLoad(t, content)

	if cfg.Backend != BackendNative {
		t.Errorf("Backend = %q, want native", cfg.Backend)
	}
}

func TestLoad_RejectsUnknownKey(t *testing.T) {
	path, _ := writeConfig(t, "bakend: native\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "bakend") {
		t.Fatalf("Load error = %v, want it to mention bakend", err)
	}
}

func TestLoad_RejectsDuplicateKey(t *testing.T) {
	path, _ := writeConfig(t, "backend: native\nbackend: dnsproxy\n")

	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted a duplicate key")
	}
}

func TestLoad_RejectsMultipleDocuments(t *testing.T) {
	tests := []struct{ name, content string }{
		{"document separator", "backend: native\n---\nlisten: [\"0.0.0.0:53\"]\n"},
		{"end marker then new document", "backend: native\n...\n---\nlisten: [\"0.0.0.0:53\"]\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := writeConfig(t, tc.content)

			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), "document") {
				t.Fatalf("Load error = %v, want it to mention multiple documents", err)
			}
		})
	}
}

func TestLoad_AcceptsEffectivelyEmptyFiles(t *testing.T) {
	tests := []struct{ name, content string }{
		{"empty", ""},
		{"BOM only", "\xef\xbb\xbf"},
		{"comment only", "# nothing here\n"},
		{"document start marker only", "---\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := mustLoad(t, tc.content)

			if cfg.Backend != BackendDNSProxy {
				t.Errorf("Backend = %q, want defaults (dnsproxy)", cfg.Backend)
			}
		})
	}
}

func TestLoad_AcceptsExplicitDocumentStart(t *testing.T) {
	cfg := mustLoad(t, "---\nbackend: native\n")

	if cfg.Backend != BackendNative {
		t.Errorf("Backend = %q, want native", cfg.Backend)
	}
}

func TestLoad_ResolvesRelativePaths(t *testing.T) {
	path, dir := writeConfig(t, "backend: dnsproxy\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := filepath.Join(dir, "data"); cfg.DataDir != want {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, want)
	}
	if want := filepath.Join(dir, "data", "rules.txt"); cfg.Rules.File != want {
		t.Errorf("Rules.File = %q, want %q", cfg.Rules.File, want)
	}
	if want := filepath.Join(dir, "data", "logs"); cfg.Log.Dir != want {
		t.Errorf("Log.Dir = %q, want %q", cfg.Log.Dir, want)
	}
}

func TestLoad_KeepsAbsolutePaths(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "elsewhere")
	cfg := mustLoad(t, fmt.Sprintf("data_dir: '%s'\n", abs))

	if cfg.DataDir != abs {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, abs)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))

	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load error = %v, want it to wrap os.ErrNotExist", err)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path, _ := writeConfig(t, "backend: [unclosed")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("Load error = %v, want it to contain the file path %q", err, path)
	}
}

func TestDefault_ReturnsFreshSlices(t *testing.T) {
	first := Default()
	first.Listen[0] = "mutated"

	if second := Default(); second.Listen[0] == "mutated" {
		t.Error("Default() shares slice storage between calls")
	}
}

const sourceWithoutEnabled = `
lists:
  sources:
    - { name: a, kind: block, format: domains, url: "https://example.com/a.txt" }
    - { name: b, kind: block, format: domains, url: "https://example.com/b.txt", enabled: false }
    - { name: c, kind: allow, format: domains, url: "https://example.com/c.txt", enabled: true }
`

func TestLoad_SourceWithoutEnabledIsEnabled(t *testing.T) {
	cfg := mustLoad(t, sourceWithoutEnabled)

	want := map[string]bool{"a": true, "b": false, "c": true}
	if len(cfg.Lists.Sources) != len(want) {
		t.Fatalf("len(Sources) = %d, want %d", len(cfg.Lists.Sources), len(want))
	}
	for _, s := range cfg.Lists.Sources {
		if s.Enabled != want[s.Name] {
			t.Errorf("source %s: Enabled = %v, want %v", s.Name, s.Enabled, want[s.Name])
		}
	}
}

func TestLoad_SourceBlockStyleWithoutEnabled(t *testing.T) {
	cfg := mustLoad(t, "lists:\n  sources:\n    - name: a\n      kind: block\n      format: domains\n      url: https://example.com/a.txt\n")

	if got := cfg.Lists.Sources[0]; !got.Enabled {
		t.Errorf("source %+v should default to enabled", got)
	}
	if len(cfg.Warnings()) != 0 {
		t.Errorf("Warnings() = %v, want none, a block list is active", cfg.Warnings())
	}
}

func TestLoad_RejectsUnknownKeyInSource(t *testing.T) {
	path, _ := writeConfig(t, "lists:\n  sources:\n    - { name: a, kind: block, format: domains, url: \"https://example.com/a.txt\", enabeld: false }\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "enabeld") {
		t.Fatalf("Load error = %v, want it to name the misspelled key enabeld", err)
	}
}

func TestLoad_RejectsDuplicateKeyInSource(t *testing.T) {
	path, _ := writeConfig(t, "lists:\n  sources:\n    - { name: a, kind: block, format: domains, url: \"https://example.com/a.txt\", enabled: true, enabled: false }\n")

	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted a source with the key enabled twice")
	}
}

func TestLoad_RejectsASourceThatIsNotAMapping(t *testing.T) {
	path, _ := writeConfig(t, "lists:\n  sources:\n    - just-a-string\n")

	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted a scalar as a source")
	}
}
