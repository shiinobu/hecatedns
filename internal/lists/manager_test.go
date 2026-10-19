package lists

// agy wrote this

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shiinobu/hecatedns/internal/config"
	"github.com/shiinobu/hecatedns/internal/engine"
)

type recordingSwapper struct {
	cur   atomic.Pointer[engine.ListSet]
	swaps atomic.Int32
}

func (r *recordingSwapper) SwapLists(set *engine.ListSet) {
	r.cur.Store(set)
	r.swaps.Add(1)
}

func TestManager_LoadCacheIgnoresCorruptAndPublishesValid(t *testing.T) {
	dir := t.TempDir()
	// Write a valid block list, a valid allow list, and a corrupt block list.
	if err := os.WriteFile(filepath.Join(dir, "good-block.txt"), []byte("ads.example.com\n"), 0o600); err != nil {
		t.Fatalf("write good-block: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "good-allow.txt"), []byte("safe.ads.example.com\n"), 0o600); err != nil {
		t.Fatalf("write good-allow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "corrupt-block.txt"), []byte("# no valid domains\nsinglelabel\n"), 0o600); err != nil {
		t.Fatalf("write corrupt-block: %v", err)
	}

	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
	target := &recordingSwapper{}

	sources := []config.Source{
		{Name: "good-allow", URL: "https://example.com/allow.txt", Kind: config.KindAllow, Format: config.FormatDomains, Enabled: true},
		{Name: "good-block", URL: "https://example.com/block.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
		{Name: "corrupt-block", URL: "https://example.com/corrupt.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
		{Name: "missing-block", URL: "https://example.com/missing.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
		{Name: "disabled-block", URL: "https://example.com/disabled.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: false},
	}

	mgr := NewManager(ManagerOptions{
		Dir:     dir,
		Sources: sources,
		Target:  target,
		Log:     log,
	})

	loaded, err := mgr.LoadCache()
	if err == nil {
		t.Fatalf("expected error reported for corrupt cache file, got nil")
	}
	if loaded != 2 {
		t.Fatalf("loaded = %d, want 2", loaded)
	}
	snap := target.cur.Load()
	if snap == nil || !snap.HasBlock() {
		t.Fatalf("expected published ListSet with block entries")
	}
	if list, ok := snap.LookupBlock("sub.ads.example.com"); !ok || list != "good-block" {
		t.Errorf("LookupBlock = (%q, %v), want (\"good-block\", true)", list, ok)
	}
	if list, ok := snap.LookupAllow("safe.ads.example.com"); !ok || list != "good-allow" {
		t.Errorf("LookupAllow = (%q, %v), want (\"good-allow\", true)", list, ok)
	}
	if !strings.Contains(logBuf.String(), "corrupt-block") {
		t.Errorf("expected log to mention corrupt-block, got %q", logBuf.String())
	}
}

func TestManager_RefreshUpdatesAndKeepsOldCacheOnFailure(t *testing.T) {
	var failSecond atomic.Bool
	srv, client := newTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/list1.txt":
			_, _ = w.Write([]byte("new1.example.com\n"))
		case "/list2.txt":
			if failSecond.Load() {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte("new2.example.com\n"))
		}
	})

	dir := t.TempDir()
	// Seed list2 in cache before refresh fails.
	if err := os.WriteFile(filepath.Join(dir, "list2.txt"), []byte("cached2.example.com\n"), 0o600); err != nil {
		t.Fatalf("seed list2: %v", err)
	}

	target := &recordingSwapper{}
	sources := []config.Source{
		{Name: "list1", URL: srv.URL + "/list1.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
		{Name: "list2", URL: srv.URL + "/list2.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
	}

	mgr := NewManager(ManagerOptions{
		Dir:     dir,
		Sources: sources,
		Target:  target,
		Fetcher: &Fetcher{Dir: dir, Client: client},
	})
	_, _ = mgr.LoadCache()

	// Make list2 fail during Refresh; list1 must update and list2 must keep cached2.example.com.
	failSecond.Store(true)
	if err := mgr.Refresh(context.Background()); err == nil {
		t.Fatalf("expected Refresh to return error when list2 fails")
	}

	snap := mgr.Snapshot()
	if _, ok := snap.LookupBlock("new1.example.com"); !ok {
		t.Errorf("expected new1.example.com from list1 after Refresh")
	}
	if _, ok := snap.LookupBlock("cached2.example.com"); !ok {
		t.Errorf("expected cached2.example.com from list2 to be preserved after failed Refresh")
	}
}

func TestManager_ReloadCacheDetectsDiskChange(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "list1.txt")
	if err := os.WriteFile(p, []byte("v1.example.com\n"), 0o600); err != nil {
		t.Fatalf("write v1: %v", err)
	}

	target := &recordingSwapper{}
	sources := []config.Source{
		{Name: "list1", URL: "https://example.com/list1.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
	}
	mgr := NewManager(ManagerOptions{Dir: dir, Sources: sources, Target: target})
	if _, err := mgr.LoadCache(); err != nil {
		t.Fatalf("LoadCache: %v", err)
	}

	// Unchanged disk -> ReloadCache returns changed=false.
	changed, err := mgr.ReloadCache()
	if err != nil || changed {
		t.Fatalf("ReloadCache unchanged = (%v, %v), want (false, nil)", changed, err)
	}

	// Simulate rewriting list1.txt with the exact same byte length (15 bytes) and a newer mtime.
	if err := writeFileAtomic(p, []byte("v2.example.com\n")); err != nil {
		t.Fatalf("writeFileAtomic v2: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	changed, err = mgr.ReloadCache()
	if err != nil || !changed {
		t.Fatalf("ReloadCache after same-size disk update = (%v, %v), want (true, nil)", changed, err)
	}
	if _, ok := mgr.Snapshot().LookupBlock("v2.example.com"); !ok {
		t.Errorf("expected v2.example.com after ReloadCache")
	}
}

func TestManager_Refresh304ReusesInMemorySetAndOfflineRefreshSkipsPublish(t *testing.T) {
	var failAll atomic.Bool
	srv, client := newTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		if failAll.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if r.Header.Get("If-None-Match") == `"etag-1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"etag-1"`)
		_, _ = w.Write([]byte("cached.example.com\n"))
	})

	dir := t.TempDir()
	target := &recordingSwapper{}
	sources := []config.Source{
		{Name: "list1", URL: srv.URL + "/list1.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
	}
	mgr := NewManager(ManagerOptions{
		Dir:     dir,
		Sources: sources,
		Target:  target,
		Fetcher: &Fetcher{Dir: dir, Client: client},
	})

	// Initial Refresh populates disk cache with ETag "etag-1".
	if err := mgr.Refresh(context.Background()); err != nil {
		t.Fatalf("initial Refresh: %v", err)
	}
	mgr.mu.Lock()
	setBefore := mgr.sets["list1"]
	mgr.mu.Unlock()

	// Second Refresh gets 304 Not Modified and must reuse the in-memory *engine.DomainSet without re-parsing disk.
	if err := mgr.Refresh(context.Background()); err != nil {
		t.Fatalf("304 Refresh: %v", err)
	}
	mgr.mu.Lock()
	setAfter := mgr.sets["list1"]
	mgr.mu.Unlock()
	if setBefore == nil || setBefore != setAfter {
		t.Errorf("expected 304 Refresh to reuse in-memory DomainSet pointer (%p vs %p)", setBefore, setAfter)
	}

	// Offline Refresh (all sources fail) must not re-publish an unchanged snapshot.
	snapBefore := mgr.Snapshot()
	failAll.Store(true)
	if err := mgr.Refresh(context.Background()); err == nil {
		t.Fatalf("expected error when all sources fail")
	}
	if snapAfter := mgr.Snapshot(); snapAfter != snapBefore {
		t.Errorf("expected offline Refresh to keep Snapshot pointer unchanged (%p vs %p)", snapBefore, snapAfter)
	}
}

func TestManager_RunBackgroundRefreshAndConcurrentReads(t *testing.T) {
	var fetches atomic.Int32
	srv, client := newTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := fetches.Add(1)
		if n == 1 {
			_, _ = w.Write([]byte("first.example.com\n"))
			return
		}
		_, _ = w.Write([]byte("second.example.com\n"))
	})

	dir := t.TempDir()
	target := &recordingSwapper{}
	sources := []config.Source{
		{Name: "live", URL: srv.URL + "/live.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
	}

	mgr := NewManager(ManagerOptions{
		Dir:            dir,
		Sources:        sources,
		Target:         target,
		Fetcher:        &Fetcher{Dir: dir, Client: client},
		UpdateInterval: 25 * time.Millisecond,
		PollInterval:   15 * time.Millisecond,
		Jitter:         func(d time.Duration) time.Duration { return 0 },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Hammer Snapshot() from reader goroutines while Run refreshes in the background.
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				if s := mgr.Snapshot(); s != nil {
					_, _ = s.LookupBlock("sub.second.example.com")
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		mgr.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s := mgr.Snapshot(); s != nil {
			if _, ok := s.LookupBlock("second.example.com"); ok {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s := mgr.Snapshot(); s == nil {
		t.Fatalf("Snapshot still nil after Run")
	} else if _, ok := s.LookupBlock("second.example.com"); !ok {
		t.Fatalf("expected scheduled refresh to load second.example.com, fetches=%d", fetches.Load())
	}

	cancel()
	<-done
	wg.Wait()
}
