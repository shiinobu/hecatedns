package lists

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

func TestInspect(t *testing.T) {
	fetched := time.Date(2026, 10, 20, 4, 30, 0, 0, time.UTC)
	write := func(t *testing.T, dir, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name      string
		src       config.Source
		prepare   func(t *testing.T, dir string)
		wantState string
		wantCount int
	}{
		{"disabled list is not looked at", config.Source{Name: "pro", Enabled: false}, func(*testing.T, string) {}, StateDisabled, 0},
		{"no list file", config.Source{Name: "pro", Enabled: true}, func(*testing.T, string) {}, StateMissing, 0},
		{"list and meta", config.Source{Name: "pro", Enabled: true}, func(t *testing.T, dir string) {
			write(t, dir, "pro.txt", "ads.example.com\n")
			write(t, dir, "pro.meta.json", `{"fetched_at":"2026-10-20T04:30:00Z","count":203414}`)
		}, StateOK, 203414},
		{"list without meta", config.Source{Name: "pro", Enabled: true}, func(t *testing.T, dir string) {
			write(t, dir, "pro.txt", "ads.example.com\n")
		}, StateCorrupt, 0},
		{"garbage meta", config.Source{Name: "pro", Enabled: true}, func(t *testing.T, dir string) {
			write(t, dir, "pro.txt", "ads.example.com\n")
			write(t, dir, "pro.meta.json", "{not json")
		}, StateCorrupt, 0},
		{"meta with zero domains", config.Source{Name: "pro", Enabled: true}, func(t *testing.T, dir string) {
			write(t, dir, "pro.txt", "x\n")
			write(t, dir, "pro.meta.json", `{"fetched_at":"2026-10-20T04:30:00Z","count":0}`)
		}, StateCorrupt, 0},
		{"unsafe list name", config.Source{Name: "../pro", Enabled: true}, func(*testing.T, string) {}, StateCorrupt, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.prepare(t, dir)

			state, meta := Inspect(dir, tt.src)

			if state != tt.wantState || meta.Count != tt.wantCount {
				t.Fatalf("Inspect = %q, count %d; want %q, %d", state, meta.Count, tt.wantState, tt.wantCount)
			}
			if tt.wantState == StateOK && !meta.FetchedAt.Equal(fetched) {
				t.Fatalf("fetched = %v, want %v", meta.FetchedAt, fetched)
			}
		})
	}
}
