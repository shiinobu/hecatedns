package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	lifetimeFileName    = "lifetime-counts.json"
	lifetimeFileVersion = 1
	lifetimeTempPattern = ".lifetime-*.tmp"
	lifetimeAsideLayout = "20060102T150405Z"
)

var errUnusableLifetimeFile = errors.New("unusable lifetime counts file")

type lifetimeFile struct {
	Version int                  `json:"version"`
	Days    map[string]dayCounts `json:"days"`
}

func newLifetimeFile() lifetimeFile {
	return lifetimeFile{Version: lifetimeFileVersion, Days: map[string]dayCounts{}}
}

func readLifetimeFile(path string) (lifetimeFile, error) {
	empty := newLifetimeFile()
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return empty, nil
	}
	var file lifetimeFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return empty, fmt.Errorf("%w: decode: %v", errUnusableLifetimeFile, err)
	}
	if file.Version != lifetimeFileVersion {
		return empty, fmt.Errorf("%w: version %d", errUnusableLifetimeFile, file.Version)
	}
	if file.Days == nil {
		file.Days = map[string]dayCounts{}
	}
	for key, counts := range file.Days {
		if _, err := time.Parse(dayKeyLayout, key); err != nil || !counts.valid() {
			return empty, fmt.Errorf("%w: entry %q", errUnusableLifetimeFile, key)
		}
	}
	return file, nil
}

func writeLifetimeFile(path string, file lifetimeFile) error {
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), lifetimeTempPattern)
	if err != nil {
		return err
	}
	_, writeErr := tmp.Write(append(raw, '\n'))
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

func setAsideLifetimeFile(path string, now time.Time) (string, error) {
	aside := path + ".bad-" + now.UTC().Format(lifetimeAsideLayout)
	return aside, os.Rename(path, aside)
}
