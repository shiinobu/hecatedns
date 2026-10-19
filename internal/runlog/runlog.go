// Package runlog keeps the server's operational log in a file of bounded size,
// so a server started without a console (a Task Scheduler task) still leaves
// its warnings behind and the disk cannot fill with them.
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
	// FileName is the current log file inside the directory; the previous one
	// is FileName + ".1".
	FileName = "hecate.log"

	dirMode  = 0o700
	fileMode = 0o600

	// overshootFactor is how far past the cap the current file may grow while
	// rotation keeps failing, as a multiple of the cap.
	overshootFactor = 2
)

// ErrClosed is returned by Write after Close.
var ErrClosed = errors.New("run log: closed")

// File is an io.Writer that appends to FileName and, when a write would make
// the file larger than the cap, moves it to the backup name first and starts a
// new file. At most two files exist, so the disk use is about twice the cap.
// A File is safe for concurrent use.
type File struct {
	dir string
	max int64

	// rename and openFile are the file system calls rotation uses; tests
	// replace them to make rotation fail.
	rename   func(oldPath, newPath string) error
	openFile func(name string, flag int, perm os.FileMode) (*os.File, error)

	mu     sync.Mutex
	file   *os.File
	size   int64
	closed bool
}

// Open creates dir if needed and opens the log for appending. maxBytes must be
// at least 1. What an earlier run left in the file counts toward the cap.
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

// Write appends p, rotating first when p would not fit. A single write larger
// than the cap is kept whole. When rotation fails (on Windows another program
// holding the file, such as `Get-Content -Wait`, blocks the rename) the line is
// appended to the current file anyway, up to twice the cap, so the log neither
// loses lines nor grows without a bound; a later write tries to rotate again.
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

// rotate moves the current file over the backup and opens a fresh one. Whatever
// the outcome, f.file is either an open file to append to or nil, and the next
// Write opens it again; it is never left pointing at a closed file.
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

// Close closes the file; later writes fail with ErrClosed. Closing twice is
// harmless.
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

// Tee returns a writer that sends everything to console and to file. The
// result is the console's: the file is a convenience, so its failure (a full
// disk) never turns into an error for the logger or hides the line from the
// console. The file is written even when the console fails or is missing.
func Tee(console io.Writer, file *File) io.Writer {
	return tee{console: console, file: file}
}

func (t tee) Write(p []byte) (int, error) {
	_, _ = t.file.Write(p)
	return t.console.Write(p)
}
