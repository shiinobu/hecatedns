package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// fallbackPollInterval is used when Run gets a non-positive interval, which
// time.NewTicker would turn into a panic.
const fallbackPollInterval = 2 * time.Second

// RulesFile keeps the custom rules of one file up to date. The file is polled
// by modification time and size, so the CLI (a separate process) can change it
// with an atomic rename and the server picks it up without a restart or a
// socket (decision D11). Readers call Snapshot and never block.
type RulesFile struct {
	path    string
	log     *slog.Logger
	current atomic.Pointer[RuleSet]

	mu     sync.Mutex
	loaded bool
	stamp  fileStamp
}

// fileStamp identifies one version of the rules file.
type fileStamp struct {
	exists  bool
	size    int64
	modTime time.Time
}

// NewRulesFile returns a RulesFile for path. Nothing is read until Reload or
// Run. A nil log discards the messages.
func NewRulesFile(path string, log *slog.Logger) *RulesFile {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &RulesFile{path: path, log: log}
}

// Snapshot returns the current rules. It is never modified after it is
// returned; a reload publishes a new RuleSet. It may be nil before the first
// Reload, which behaves as an empty set.
func (f *RulesFile) Snapshot() *RuleSet {
	return f.current.Load()
}

// Reload re-reads the file if it changed since the last successful load and
// reports whether a new snapshot was published. The first call always
// publishes. A missing file means no rules. If the file cannot be read, the
// previous rules stay in force and the error is returned, so the next call
// tries again.
func (f *RulesFile) Reload() (changed bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	stamp, err := statRules(f.path)
	if err != nil {
		return false, err
	}
	if f.loaded && stamp == f.stamp {
		return false, nil
	}
	set := &RuleSet{}
	if stamp.exists {
		if set, err = f.read(); err != nil {
			return false, err
		}
	}
	f.current.Store(set)
	f.stamp, f.loaded = stamp, true
	return true, nil
}

// statRules stats path, mapping "does not exist" to a stamp without a file.
func statRules(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fileStamp{}, nil
	case err != nil:
		return fileStamp{}, fmt.Errorf("stat rules %s: %w", path, err)
	case info.IsDir():
		return fileStamp{}, fmt.Errorf("rules %s: is a directory", path)
	}
	return fileStamp{exists: true, size: info.Size(), modTime: info.ModTime()}, nil
}

// read parses the rules file and logs what was skipped.
func (f *RulesFile) read() (*RuleSet, error) {
	file, err := os.Open(f.path)
	if err != nil {
		return nil, fmt.Errorf("open rules %s: %w", f.path, err)
	}
	defer file.Close()

	set, issues, err := ParseRules(file)
	if err != nil {
		return nil, fmt.Errorf("rules %s: %w", f.path, err)
	}
	for _, issue := range issues {
		f.log.Warn("rules line skipped", "file", f.path, "line", issue.Line, "reason", issue.Err)
	}
	f.log.Info("rules loaded", "file", f.path, "rules", set.Len(), "skipped", len(issues))
	return set, nil
}

// Run reloads the file every interval until ctx ends. It logs a reload error
// once per distinct message, not on every tick.
func (f *RulesFile) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = fallbackPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastErr string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := f.Reload()
			lastErr = f.noteError(err, lastErr)
		}
	}
}

// noteError logs err unless it repeats prev and returns the text to remember.
func (f *RulesFile) noteError(err error, prev string) string {
	if err == nil {
		return ""
	}
	if msg := err.Error(); msg != prev {
		f.log.Warn("rules reload failed, keeping the previous rules", "error", err)
		return msg
	}
	return prev
}
