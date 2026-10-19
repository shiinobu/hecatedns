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

const fallbackPollInterval = 2 * time.Second

type RulesFile struct {
	path    string
	log     *slog.Logger
	current atomic.Pointer[RuleSet]

	mu     sync.Mutex
	loaded bool
	stamp  fileStamp
}

type fileStamp struct {
	exists  bool
	size    int64
	modTime time.Time
}

func NewRulesFile(path string, log *slog.Logger) *RulesFile {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &RulesFile{path: path, log: log}
}

func (f *RulesFile) Snapshot() *RuleSet {
	return f.current.Load()
}

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
