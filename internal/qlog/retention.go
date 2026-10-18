package qlog

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// logDay returns the day a file name stands for, or false when name is not
// exactly queries-YYYYMMDD.jsonl. Anything else in the directory is not ours.
func logDay(name string, loc *time.Location) (time.Time, bool) {
	rest, ok := strings.CutPrefix(name, filePrefix)
	if !ok {
		return time.Time{}, false
	}
	digits, ok := strings.CutSuffix(rest, fileSuffix)
	if !ok || len(digits) != len(dayLayout) {
		return time.Time{}, false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return time.Time{}, false
		}
	}
	day, err := time.ParseInLocation(dayLayout, digits, loc)
	return day, err == nil
}

// purgeOld deletes query-log files older than the last keepDays calendar days
// (today counts as one) and returns how many it removed. Only regular files
// named exactly queries-YYYYMMDD.jsonl are candidates. A missing directory is
// not an error; files that cannot be removed (for example one a reader still
// has open on Windows) are reported together and do not stop the sweep. log
// may be nil.
func purgeOld(dir string, now time.Time, keepDays int, loc *time.Location, log *slog.Logger) (removed int, err error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("list query logs in %s: %w", dir, err)
	}
	now = now.In(loc)
	cutoff := time.Date(now.Year(), now.Month(), now.Day()-(keepDays-1), 0, 0, 0, 0, loc)

	var errs []error
	for _, entry := range entries {
		day, ok := logDay(entry.Name(), loc)
		if !ok || !entry.Type().IsRegular() || !day.Before(cutoff) {
			continue
		}
		if rmErr := os.Remove(filepath.Join(dir, entry.Name())); rmErr != nil {
			errs = append(errs, fmt.Errorf("remove old query log: %w", rmErr))
			continue
		}
		removed++
		if log != nil {
			log.Info("old query log removed", "file", entry.Name())
		}
	}
	return removed, errors.Join(errs...)
}
