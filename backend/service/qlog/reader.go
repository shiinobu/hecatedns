package qlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxLineBytes = 64 << 10

type Filter struct {
	Since, Until time.Time

	Grep string

	BlockedOnly bool

	Location *time.Location
}

type ReadStats struct {
	Matched int

	Malformed int

	Stopped bool
}

var errStopped = errors.New("read stopped by the caller")

func Read(dir string, f Filter, emit func(Record)) (ReadStats, error) {
	return ReadWhile(dir, f, func(r Record) bool {
		emit(r)
		return true
	})
}

func ReadWhile(dir string, f Filter, emit func(Record) bool) (ReadStats, error) {
	loc := cmpLocation(f.Location)
	files, err := dayFiles(dir, f, loc)
	if err != nil {
		return ReadStats{}, err
	}
	var stats ReadStats
	for _, name := range files {
		err := readFile(filepath.Join(dir, name), f, emit, &stats)
		if errors.Is(err, errStopped) {
			stats.Stopped = true
			return stats, nil
		}
		if err != nil {
			return stats, err
		}
	}
	return stats, nil
}

func dayFiles(dir string, f Filter, loc *time.Location) ([]string, error) {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query log directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("query log directory %s: not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list query logs in %s: %w", dir, err)
	}
	first, last := dayBounds(f, loc)
	var names []string
	for _, entry := range entries {
		day, ok := logDay(entry.Name(), loc)
		if !ok || !entry.Type().IsRegular() {
			continue
		}
		if (!first.IsZero() && day.Before(first)) || (!last.IsZero() && day.After(last)) {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func dayBounds(f Filter, loc *time.Location) (first, last time.Time) {
	if !f.Since.IsZero() {
		s := f.Since.In(loc)
		first = time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, loc)
	}
	if !f.Until.IsZero() {
		u := f.Until.In(loc)
		last = time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, loc)
	}
	return first, last
}

func readFile(path string, f Filter, emit func(Record) bool, stats *ReadStats) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open query log: %w", err)
	}
	defer file.Close()

	lines := bufio.NewReaderSize(file, maxLineBytes)
	want := strings.ToLower(f.Grep)
	for {
		line, tooLong, err := nextLogLine(lines)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read query log %s: %w", filepath.Base(path), err)
		}
		if tooLong {
			stats.Malformed++
			continue
		}
		if len(line) == 0 {
			continue
		}
		rec, ok := decodeRecord(line)
		if !ok {
			stats.Malformed++
			continue
		}
		if matches(rec, f, want) {
			stats.Matched++
			if !emit(rec) {
				return errStopped
			}
		}
	}
}

func nextLogLine(r *bufio.Reader) (line []byte, tooLong bool, err error) {
	chunk, prefix, err := r.ReadLine()
	if err != nil {
		return nil, false, err
	}
	if !prefix {
		return chunk, false, nil
	}
	for prefix && err == nil {
		_, prefix, err = r.ReadLine()
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, true, err
	}
	return nil, true, nil
}

func decodeRecord(line []byte) (Record, bool) {
	var rec Record
	if err := json.Unmarshal(line, &rec); err != nil {
		return Record{}, false
	}
	at, err := time.Parse(tsLayout, rec.TS)
	if err != nil {
		return Record{}, false
	}
	rec.Time = at
	for _, field := range []*string{&rec.Client, &rec.Proto, &rec.QName, &rec.QType, &rec.Action,
		&rec.Reason, &rec.Source, &rec.Via, &rec.Upstream, &rec.RCode} {
		*field = sanitize(*field, maxFieldLen)
	}
	rec.Error = sanitize(rec.Error, maxErrorLen)
	return rec, true
}

func matches(rec Record, f Filter, want string) bool {
	if !f.Since.IsZero() && rec.Time.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && rec.Time.After(f.Until) {
		return false
	}
	if f.BlockedOnly && rec.Action != "block" {
		return false
	}
	if want == "" {
		return true
	}
	for _, text := range []string{rec.QName, rec.Client, rec.Source, rec.Via, rec.Upstream, rec.Reason} {
		if strings.Contains(strings.ToLower(text), want) {
			return true
		}
	}
	return false
}
