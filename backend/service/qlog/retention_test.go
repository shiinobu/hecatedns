package qlog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func touch(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func TestPurge_KeepsRetentionDaysIncludingToday(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"queries-20261010.jsonl", "queries-20261011.jsonl",
		"queries-20261012.jsonl", "queries-20261018.jsonl", "queries-20261019.jsonl",
	} {
		touch(t, dir, name)
	}
	now := time.Date(2026, 10, 18, 23, 59, 0, 0, testZone)

	removed, err := purgeOld(dir, now, 7, testZone, nil)
	if err != nil {
		t.Fatalf("purgeOld: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}
	for name, want := range map[string]bool{
		"queries-20261010.jsonl": false, "queries-20261011.jsonl": false,
		"queries-20261012.jsonl": true, "queries-20261018.jsonl": true, "queries-20261019.jsonl": true,
	} {
		if exists(dir, name) != want {
			t.Errorf("%s exists = %v, want %v", name, !want, want)
		}
	}
}

func TestPurge_OneDayRetentionKeepsOnlyToday(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "queries-20261017.jsonl")
	touch(t, dir, "queries-20261018.jsonl")
	if _, err := purgeOld(dir, time.Date(2026, 10, 18, 0, 0, 1, 0, testZone), 1, testZone, nil); err != nil {
		t.Fatal(err)
	}
	if exists(dir, "queries-20261017.jsonl") || !exists(dir, "queries-20261018.jsonl") {
		t.Error("retention of 1 day must keep today only")
	}
}

func TestPurge_NeverTouchesFilesThatAreNotQueryLogs(t *testing.T) {
	dir := t.TempDir()
	keep := []string{
		"notes.txt", "queries-20200101.jsonl.bak", "queries-2020.jsonl", "queries-abcdefgh.jsonl",
		"queries-20201301.jsonl", "queries-20200101", "xqueries-20200101.jsonl", "queries-20200101.JSONL",
		"queries-202001011.jsonl", "queries-.jsonl",
	}
	for _, name := range keep {
		touch(t, dir, name)
	}
	if err := os.MkdirAll(filepath.Join(dir, "queries-20200101.jsonl.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "queries-20190101.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	touch(t, dir, filepath.Join("queries-20190101.jsonl", "inner.txt"))

	removed, err := purgeOld(dir, time.Date(2026, 10, 18, 12, 0, 0, 0, testZone), 7, testZone, nil)
	if err != nil {
		t.Fatalf("purgeOld: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	for _, name := range keep {
		if !exists(dir, name) {
			t.Errorf("%s was deleted", name)
		}
	}
	if !exists(dir, filepath.Join("queries-20190101.jsonl", "inner.txt")) {
		t.Error("a directory named like a log file was removed")
	}
}

func TestPurge_MissingDirectoryIsNotAnError(t *testing.T) {
	removed, err := purgeOld(filepath.Join(t.TempDir(), "nope"), time.Now(), 7, testZone, nil)
	if err != nil || removed != 0 {
		t.Errorf("purgeOld = %d, %v; want 0, nil", removed, err)
	}
}

func TestOpen_PurgesAtStart(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 10, 18, 12, 0, 0, 0, testZone))
	opts := testOptions(t, clock)
	touch(t, opts.Dir, "queries-20260101.jsonl")
	touch(t, opts.Dir, "queries-20261017.jsonl")

	w := openWriter(t, opts)
	_ = w.Close()

	if exists(opts.Dir, "queries-20260101.jsonl") {
		t.Error("an old log survived Open")
	}
	if !exists(opts.Dir, "queries-20261017.jsonl") {
		t.Error("a log inside the retention window was removed")
	}
}

func TestWriter_PurgesAfterMidnight(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 10, 18, 23, 59, 59, 960_000_000, testZone))
	opts := testOptions(t, clock)
	touch(t, opts.Dir, "queries-20261012.jsonl")

	w := openWriter(t, opts)
	if !exists(opts.Dir, "queries-20261012.jsonl") {
		t.Fatal("Open removed a log that is still inside the window")
	}

	clock.Set(time.Date(2026, 10, 19, 0, 0, 1, 0, testZone))
	deadline := time.Now().Add(5 * time.Second)
	for exists(opts.Dir, "queries-20261012.jsonl") {
		if time.Now().After(deadline) {
			t.Fatal("the midnight purge did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = w.Close()
}
