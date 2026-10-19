package runlog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	FileName = "hecate.log"

	dirMode  = 0o700
	fileMode = 0o600

	overshootFactor = 2
)

var ErrClosed = errors.New("run log: closed")

type File struct {
	dir string
	max int64

	rename   func(oldPath, newPath string) error
	openFile func(name string, flag int, perm os.FileMode) (*os.File, error)

	mu     sync.Mutex
	file   *os.File
	size   int64
	closed bool
}

func Open(dir string, maxBytes int64) (*File, error) {
	if dir == "" {
		return nil, errors.New("run log: directory is empty")
	}
	if maxBytes < 1 {
		return nil, fmt.Errorf("run log: size cap of %d bytes, need at least 1", maxBytes)
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("run log: create %s: %w", dir, err)
	}
	f := &File{dir: dir, max: maxBytes, rename: os.Rename, openFile: os.OpenFile}
	if err := f.openCurrent(); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *File) path() string { return filepath.Join(f.dir, FileName) }

func (f *File) openCurrent() error {
	file, err := f.openFile(f.path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return fmt.Errorf("run log: open: %w", err)
	}
	f.file, f.size = file, 0
	if info, err := file.Stat(); err == nil {
		f.size = info.Size()
	}
	return nil
}

func (f *File) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, ErrClosed
	}
	if f.file == nil {
		if err := f.openCurrent(); err != nil {
			return 0, err
		}
	}
	if f.size > 0 && f.size+int64(len(p)) > f.max {
		if err := f.rotate(); err != nil && (f.file == nil || f.size+int64(len(p)) > f.max*overshootFactor) {
			return 0, err
		}
	}
	n, err := f.file.Write(p)
	f.size += int64(n)
	return n, err
}

func (f *File) rotate() error {
	_ = f.file.Close()
	f.file = nil
	renameErr := f.rename(f.path(), f.path()+".1")
	if err := f.openCurrent(); err != nil {
		return err
	}
	if renameErr != nil {
		return fmt.Errorf("run log: rotate: %w", renameErr)
	}
	return nil
}

func (f *File) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	if f.file == nil {
		return nil
	}
	return f.file.Close()
}

type tee struct {
	console io.Writer
	file    *File
}

func Tee(console io.Writer, file *File) io.Writer {
	return tee{console: console, file: file}
}

func (t tee) Write(p []byte) (int, error) {
	_, _ = t.file.Write(p)
	return t.console.Write(p)
}
