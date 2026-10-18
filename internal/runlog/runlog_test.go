package runlog

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func open(t *testing.T, dir string, max int64) *File {
	t.Helper()
	f, err := Open(dir, max)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestOpen_CreatesTheDirectoryAndAppends(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs", "deeper")
	f := open(t, dir, 1<<20)
	if _, err := f.Write([]byte("one\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("two\n")); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, FileName)); got != "one\ntwo\n" {
		t.Errorf("file = %q, want both lines", got)
	}
}

func TestOpen_RejectsBadArguments(t *testing.T) {
	if _, err := Open("", 100); err == nil {
		t.Error("an empty directory was accepted")
	}
	if _, err := Open(t.TempDir(), 0); err == nil {
		t.Error("a size cap of 0 was accepted, the log must never grow without a bound")
	}
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(file, 100); err == nil {
		t.Error("a directory that is a file was accepted")
	}
}

func TestWrite_RotatesToOneBackupWhenTheCapIsReached(t *testing.T) {
	dir := t.TempDir()
	f := open(t, dir, 100)
	line := func(c string) []byte { return []byte(strings.Repeat(c, 59) + "\n") }

	for _, c := range []string{"a", "b", "c"} {
		if _, err := f.Write(line(c)); err != nil {
			t.Fatal(err)
		}
	}

	if got := read(t, filepath.Join(dir, FileName)); got != string(line("c")) {
		t.Errorf("current file = %q, want only the newest line", got)
	}
	if got := read(t, filepath.Join(dir, FileName+".1")); got != string(line("b")) {
		t.Errorf("backup = %q, want the line before it; the oldest was dropped", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("%d files in the directory, want 2 (current and one backup)", len(entries))
	}
}

func TestWrite_NeverLeavesTheCurrentFileOverTheCap(t *testing.T) {
	dir := t.TempDir()
	f := open(t, dir, 1000)
	for i := 0; i < 500; i++ {
		if _, err := f.Write([]byte(strings.Repeat("x", 37) + "\n")); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(dir, FileName))
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > 1000 {
			t.Fatalf("current file is %d bytes after write %d, cap is 1000", info.Size(), i+1)
		}
	}
}

func TestOpen_CountsWhatIsAlreadyInTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(strings.Repeat("o", 90)), 0o600); err != nil {
		t.Fatal(err)
	}
	f := open(t, dir, 100)
	if _, err := f.Write([]byte(strings.Repeat("n", 20))); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, FileName+".1")); got != strings.Repeat("o", 90) {
		t.Errorf("backup = %q, want what the previous run left: a restart must not reset the size", got)
	}
	if got := read(t, filepath.Join(dir, FileName)); got != strings.Repeat("n", 20) {
		t.Errorf("current file = %q, want only the new write", got)
	}
}

func TestWrite_OneWriteLargerThanTheCapIsKeptWhole(t *testing.T) {
	dir := t.TempDir()
	f := open(t, dir, 10)
	big := strings.Repeat("z", 50)
	if n, err := f.Write([]byte(big)); err != nil || n != 50 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if got := read(t, filepath.Join(dir, FileName)); got != big {
		t.Errorf("file = %q, want the whole write: a log line is never cut", got)
	}
}

func TestWrite_ConcurrentWritersLoseNothingBelowTheCap(t *testing.T) {
	dir := t.TempDir()
	f := open(t, dir, 1<<20)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 100; j++ {
				_, _ = f.Write([]byte("line\n"))
			}
		})
	}
	wg.Wait()
	if got := strings.Count(read(t, filepath.Join(dir, FileName)), "line\n"); got != 800 {
		t.Errorf("%d lines on disk, want 800", got)
	}
}

func TestWrite_AfterCloseFailsWithoutPanicking(t *testing.T) {
	f := open(t, t.TempDir(), 100)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, err := f.Write([]byte("late\n")); err == nil {
		t.Error("Write after Close succeeded")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestTee_FileFailureNeverHidesTheConsole(t *testing.T) {
	var console bytes.Buffer
	f := open(t, t.TempDir(), 100)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	w := Tee(&console, f)

	n, err := w.Write([]byte("hello\n"))

	if err != nil || n != 6 {
		t.Errorf("Write = %d, %v, want the console's result: the file is a convenience", n, err)
	}
	if console.String() != "hello\n" {
		t.Errorf("console = %q", console.String())
	}
}

func TestTee_WritesToBoth(t *testing.T) {
	dir := t.TempDir()
	var console bytes.Buffer
	f := open(t, dir, 1<<20)
	if _, err := Tee(&console, f).Write([]byte("both\n")); err != nil {
		t.Fatal(err)
	}
	if console.String() != "both\n" || read(t, filepath.Join(dir, FileName)) != "both\n" {
		t.Errorf("console %q, file %q, want both", console.String(), read(t, filepath.Join(dir, FileName)))
	}
}

func TestTee_ConsoleFailureStillWritesTheFile(t *testing.T) {
	dir := t.TempDir()
	f := open(t, dir, 1<<20)
	_, err := Tee(failingWriter{}, f).Write([]byte("kept\n"))
	if err == nil {
		t.Error("the console error was swallowed")
	}
	if got := read(t, filepath.Join(dir, FileName)); got != "kept\n" {
		t.Errorf("file = %q, want the line even when the console is gone (a task without a console)", got)
	}
}
