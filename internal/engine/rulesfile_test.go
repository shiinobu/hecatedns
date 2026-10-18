package engine

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeRules(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func bumpMtime(t *testing.T, path string) {
	t.Helper()
	later := time.Now().Add(time.Hour)
	if info, err := os.Stat(path); err == nil {
		later = info.ModTime().Add(time.Minute)
	}
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newTestRulesFile(t *testing.T) (*RulesFile, string, *lockedBuffer) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.txt")
	logs := &lockedBuffer{}
	return NewRulesFile(path, slog.New(slog.NewTextHandler(logs, nil))), path, logs
}

func TestRulesFile_MissingFileMeansNoRules(t *testing.T) {
	rf, _, _ := newTestRulesFile(t)
	changed, err := rf.Reload()
	if err != nil || !changed {
		t.Fatalf("Reload() = %v, %v; want true, nil (the first load always publishes)", changed, err)
	}
	if rf.Snapshot().Len() != 0 {
		t.Error("a missing file produced rules")
	}
	if changed, err := rf.Reload(); err != nil || changed {
		t.Errorf("second Reload() = %v, %v; want false, nil", changed, err)
	}
}

func TestRulesFile_LoadsAndDetectsChanges(t *testing.T) {
	rf, path, _ := newTestRulesFile(t)
	writeRules(t, path, "block ads.example.com\n")
	if _, err := rf.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, ok := rf.Snapshot().Blocked("ads.example.com"); !ok {
		t.Fatal("rule not loaded")
	}
	first := rf.Snapshot()

	if changed, err := rf.Reload(); err != nil || changed {
		t.Fatalf("unchanged file: Reload() = %v, %v; want false, nil", changed, err)
	}
	if rf.Snapshot() != first {
		t.Error("an unchanged file produced a new snapshot")
	}

	writeRules(t, path, "block ads.example.com\nallow cdn.example.com\n")
	if changed, err := rf.Reload(); err != nil || !changed {
		t.Fatalf("grown file: Reload() = %v, %v; want true, nil", changed, err)
	}
	if _, ok := rf.Snapshot().Allowed("cdn.example.com"); !ok {
		t.Error("new rule not loaded")
	}
	if rf.Snapshot() == first {
		t.Error("the old snapshot was modified in place instead of replaced")
	}
	if _, ok := first.Allowed("cdn.example.com"); ok {
		t.Error("a published snapshot changed after a reload")
	}
}

func TestRulesFile_SameSizeButNewModTimeReloads(t *testing.T) {
	rf, path, _ := newTestRulesFile(t)
	writeRules(t, path, "block aaa.example.com\n")
	if _, err := rf.Reload(); err != nil {
		t.Fatal(err)
	}
	writeRules(t, path, "block bbb.example.com\n")
	bumpMtime(t, path)
	if changed, err := rf.Reload(); err != nil || !changed {
		t.Fatalf("Reload() = %v, %v; want true, nil", changed, err)
	}
	if _, ok := rf.Snapshot().Blocked("bbb.example.com"); !ok {
		t.Error("same-size edit was not picked up")
	}
}

func TestRulesFile_AtomicReplaceByRename(t *testing.T) {
	rf, path, _ := newTestRulesFile(t)
	writeRules(t, path, "block old.example.com\n")
	if _, err := rf.Reload(); err != nil {
		t.Fatal(err)
	}
	tmp := path + ".tmp"
	writeRules(t, tmp, "block new.example.com\nblock another.example.com\n")
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if changed, err := rf.Reload(); err != nil || !changed {
		t.Fatalf("Reload() = %v, %v; want true, nil", changed, err)
	}
	snap := rf.Snapshot()
	if _, ok := snap.Blocked("new.example.com"); !ok {
		t.Error("renamed-in file not loaded")
	}
	if _, ok := snap.Blocked("old.example.com"); ok {
		t.Error("old rule survived the replacement")
	}
}

func TestRulesFile_DeletedFileClearsRulesAndCreatedFileLoads(t *testing.T) {
	rf, path, _ := newTestRulesFile(t)
	writeRules(t, path, "block ads.example.com\n")
	if _, err := rf.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if changed, err := rf.Reload(); err != nil || !changed {
		t.Fatalf("after delete: Reload() = %v, %v; want true, nil", changed, err)
	}
	if rf.Snapshot().Len() != 0 {
		t.Error("rules survived deleting the file")
	}
	writeRules(t, path, "block back.example.com\n")
	if changed, err := rf.Reload(); err != nil || !changed {
		t.Fatalf("after re-create: Reload() = %v, %v; want true, nil", changed, err)
	}
	if _, ok := rf.Snapshot().Blocked("back.example.com"); !ok {
		t.Error("re-created file not loaded")
	}
}

func TestRulesFile_UnreadablePathKeepsTheOldRules(t *testing.T) {
	rf, path, _ := newTestRulesFile(t)
	writeRules(t, path, "block ads.example.com\n")
	if _, err := rf.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if changed, err := rf.Reload(); err == nil || changed {
		t.Fatalf("Reload() on a directory = %v, %v; want false and an error", changed, err)
	}
	if _, ok := rf.Snapshot().Blocked("ads.example.com"); !ok {
		t.Error("a failed reload threw away the working rules")
	}
}

func TestRulesFile_LogsBadLinesWithNumbers(t *testing.T) {
	rf, path, logs := newTestRulesFile(t)
	writeRules(t, path, "block ok.example.com\nnonsense here\nblock com\n")
	if _, err := rf.Reload(); err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	for _, want := range []string{"line=2", "line=3"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "nonsense") {
		t.Errorf("log echoes the rule line, which is untrusted input:\n%s", out)
	}
}

func TestRulesFile_RunPicksUpChangesUntilCancelled(t *testing.T) {
	rf, path, _ := newTestRulesFile(t)
	if _, err := rf.Reload(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		rf.Run(ctx, 5*time.Millisecond)
	}()

	writeRules(t, path, "block live.example.com\n")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := rf.Snapshot().Blocked("live.example.com"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Run did not pick up the new rule within 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

func TestRulesFile_ConcurrentReadersDuringReload(t *testing.T) {
	rf, path, _ := newTestRulesFile(t)
	writeRules(t, path, "block stable.example.com\n")
	if _, err := rf.Reload(); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 6; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if _, ok := rf.Snapshot().Blocked("x.stable.example.com"); !ok {
						t.Error("stable rule vanished during a reload")
						return
					}
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		writeRules(t, path, "block stable.example.com\n"+strings.Repeat("# pad\n", i))
		bumpMtime(t, path)
		if _, err := rf.Reload(); err != nil {
			t.Error(err)
		}
	}
	close(stop)
	readers.Wait()
}
