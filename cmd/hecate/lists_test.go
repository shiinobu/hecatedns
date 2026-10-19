package main

// agy wrote this

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/shiinobu/hecatedns/internal/lists"
)

func TestListsCLI(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pro.txt":
			if r.Header.Get("If-None-Match") == `"v1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"v1"`)
			_, _ = fmt.Fprintln(w, "ads.pro.test\ntracker.pro.test")
		case "/fail.txt":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfgPath := writeFile(t, dir, "hecate.yaml", fmt.Sprintf(`
data_dir: data
lists:
  update_interval: 24h
  sources:
    - name: pro
      url: %s/pro.txt
      kind: block
      format: domains
      enabled: true
    - name: disabled-src
      url: %s/pro.txt
      kind: block
      format: domains
      enabled: false
`, srv.URL, srv.URL))

	t.Run("usage errors", func(t *testing.T) {
		for _, args := range [][]string{
			nil,
			{"unknown"},
			{"status", "extra"},
			{"-config"},
		} {
			var out, errOut bytes.Buffer
			if code := runListsWith(context.Background(), args, &out, &errOut, srv.Client()); code != exitUsage {
				t.Errorf("args %v: code = %d, want %d", args, code, exitUsage)
			}
		}
	})

	t.Run("status before update reports not downloaded", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := runListsWith(context.Background(), []string{"status", "-config", cfgPath}, &out, &errOut, srv.Client())
		if code != exitOK {
			t.Fatalf("status exit = %d, stderr = %q", code, errOut.String())
		}
		assertContains(t, "stdout", out.String(), []string{"pro", "belum diunduh", "disabled-src", "nonaktif"})
	})

	t.Run("update downloads and status reports counts", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := runListsWith(context.Background(), []string{"update", "-config", cfgPath}, &out, &errOut, srv.Client())
		if code != exitOK {
			t.Fatalf("update exit = %d, stderr = %q", code, errOut.String())
		}
		assertContains(t, "stdout", out.String(), []string{"pro: diperbarui, 2 domain"})

		// Second update hits 304 Not Modified.
		out.Reset()
		code = runListsWith(context.Background(), []string{"update", "-config", cfgPath}, &out, &errOut, srv.Client())
		if code != exitOK {
			t.Fatalf("second update exit = %d, stderr = %q", code, errOut.String())
		}
		assertContains(t, "stdout", out.String(), []string{"pro: belum berubah (2 domain)"})

		out.Reset()
		code = runListsWith(context.Background(), []string{"status", "-config", cfgPath}, &out, &errOut, srv.Client())
		if code != exitOK {
			t.Fatalf("status exit = %d, stderr = %q", code, errOut.String())
		}
		assertContains(t, "stdout", out.String(), []string{"pro (block): 2 domain"})
	})

	t.Run("update failure returns exitError", func(t *testing.T) {
		failCfg := writeFile(t, dir, "fail.yaml", fmt.Sprintf(`
data_dir: data
lists:
  update_interval: 24h
  sources:
    - name: broken
      url: %s/fail.txt
      kind: block
      format: domains
      enabled: true
`, srv.URL))
		var out, errOut bytes.Buffer
		code := runListsWith(context.Background(), []string{"update", "-config", failCfg}, &out, &errOut, srv.Client())
		if code != exitError {
			t.Fatalf("update exit = %d, want %d", code, exitError)
		}
		assertContains(t, "stderr", errOut.String(), []string{"broken"})
	})

	t.Run("corrupt cache file reported by status", func(t *testing.T) {
		corruptDir := lists.Dir(filepath.Join(dir, "data"))
		if err := os.WriteFile(filepath.Join(corruptDir, "pro.txt"), []byte("# empty\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errOut bytes.Buffer
		code := runListsWith(context.Background(), []string{"status", "-config", cfgPath}, &out, &errOut, srv.Client())
		if code != exitOK {
			t.Fatalf("status exit = %d, want %d", code, exitOK)
		}
		assertContains(t, "stdout", out.String(), []string{"pro (block): rusak"})
	})
}
