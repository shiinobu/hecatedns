package dotnames

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

const (
	MaxBytes   = 4096
	MaxEntries = 16
	LabelLen   = 20

	fileMode       = 0o640
	tempPattern    = ".dot-names-*.tmp"
	alphabet       = "abcdefghijklmnopqrstuvwxyz0123456789"
	unbiasedBelow  = 252
	renameAttempts = 5
	renameWait     = 20 * time.Millisecond
)

var (
	ErrTooLarge = errors.New("dotnames: the names file is too large")
	ErrTooMany  = errors.New("dotnames: the names file has too many names")
)

type Entry struct {
	Host  string
	Added time.Time
}

type Stamp struct {
	Time time.Time
	Size int64
}

func Parse(data []byte) ([]Entry, error) {
	entries, _, err := parse(data, true)
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func ParseLenient(data []byte) ([]Entry, []int) {
	entries, rejected, _ := parse(data, false)
	return entries, rejected
}

func parse(data []byte, strict bool) ([]Entry, []int, error) {
	var entries []Entry
	var rejected []int
	seen := make(map[string]bool)
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entry, err := parseLine(i+1, line)
		if err == nil && seen[entry.Host] {
			err = fmt.Errorf("dotnames: line %d repeats an earlier host name", i+1)
		}
		if err == nil && len(entries) >= MaxEntries {
			err = fmt.Errorf("%w (at most %d)", ErrTooMany, MaxEntries)
		}
		if err != nil {
			if strict {
				return nil, nil, err
			}
			rejected = append(rejected, i+1)
			continue
		}
		seen[entry.Host] = true
		entries = append(entries, entry)
	}
	return entries, rejected, nil
}

func parseLine(number int, line string) (Entry, error) {
	fields := strings.Fields(line)
	if len(fields) > 2 {
		return Entry{}, fmt.Errorf("dotnames: line %d has more than a host name and a time", number)
	}
	if !config.ValidHostName(fields[0]) {
		return Entry{}, fmt.Errorf("dotnames: line %d is not a valid lowercase host name", number)
	}
	entry := Entry{Host: fields[0]}
	if len(fields) == 2 {
		added, err := time.Parse(time.RFC3339, fields[1])
		if err != nil {
			return Entry{}, fmt.Errorf("dotnames: line %d has an unreadable time", number)
		}
		entry.Added = added
	}
	return entry, nil
}

func Read(path string) ([]Entry, Stamp, error) {
	data, stamp, err := readBytes(path)
	if err != nil {
		return nil, stamp, err
	}
	entries, err := Parse(data)
	return entries, stamp, err
}

func ReadLenient(path string) ([]Entry, []int, Stamp, error) {
	data, stamp, err := readBytes(path)
	if err != nil {
		return nil, nil, stamp, err
	}
	entries, rejected := ParseLenient(data)
	return entries, rejected, stamp, nil
}

func readBytes(path string) ([]byte, Stamp, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, Stamp{}, nil
	}
	if err != nil {
		return nil, Stamp{}, fmt.Errorf("dotnames: opening the names file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, Stamp{}, fmt.Errorf("dotnames: reading the names file: %w", err)
	}
	stamp := Stamp{Time: info.ModTime(), Size: info.Size()}
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return nil, stamp, fmt.Errorf("dotnames: reading the names file: %w", err)
	}
	if len(data) > MaxBytes {
		return nil, stamp, ErrTooLarge
	}
	return data, stamp, nil
}

func StatFile(path string) (Stamp, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Stamp{}, nil
	}
	if err != nil {
		return Stamp{}, fmt.Errorf("dotnames: reading the names file: %w", err)
	}
	return Stamp{Time: info.ModTime(), Size: info.Size()}, nil
}

func Hosts(entries []Entry) []string {
	hosts := make([]string, len(entries))
	for i, e := range entries {
		hosts[i] = e.Host
	}
	return hosts
}

func Write(path string, entries []Entry) error {
	data, err := encode(entries)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

func encode(entries []Entry) ([]byte, error) {
	if len(entries) > MaxEntries {
		return nil, fmt.Errorf("%w (at most %d)", ErrTooMany, MaxEntries)
	}
	var b strings.Builder
	seen := make(map[string]bool, len(entries))
	for i, e := range entries {
		if !config.ValidHostName(e.Host) || seen[e.Host] {
			return nil, fmt.Errorf("dotnames: entry %d is not a valid, unique lowercase host name", i)
		}
		seen[e.Host] = true
		b.WriteString(e.Host)
		if !e.Added.IsZero() {
			b.WriteString(" ")
			b.WriteString(e.Added.UTC().Format(time.RFC3339))
		}
		b.WriteString("\n")
	}
	if b.Len() > MaxBytes {
		return nil, ErrTooLarge
	}
	return []byte(b.String()), nil
}

func writeFileAtomic(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), tempPattern)
	if err != nil {
		return fmt.Errorf("dotnames: creating a temporary file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Chmod(fileMode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("dotnames: writing the names file: %w", err)
	}
	return renameWithRetry(tmp.Name(), path)
}

func renameWithRetry(from, to string) (err error) {
	for range renameAttempts {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(renameWait)
	}
	return fmt.Errorf("dotnames: replacing the names file: %w", err)
}

func NewLabel(src io.Reader) (string, error) {
	if src == nil {
		src = rand.Reader
	}
	label := make([]byte, 0, LabelLen)
	for len(label) < LabelLen {
		buf := make([]byte, LabelLen-len(label))
		if _, err := io.ReadFull(src, buf); err != nil {
			return "", fmt.Errorf("dotnames: reading random bytes: %w", err)
		}
		for _, b := range buf {
			if b < unbiasedBelow {
				label = append(label, alphabet[int(b)%len(alphabet)])
			}
		}
	}
	return string(label), nil
}
