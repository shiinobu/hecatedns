package qlog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shiinobu/hecatedns/backend/engine"
)

const (
	defaultBufferSize = 4096
	fileBufferBytes   = 64 << 10
	dirMode           = 0o700
	fileMode          = 0o600

	midnightMargin = 50 * time.Millisecond

	dropReportInterval = 30 * time.Second
)

type Options struct {
	Dir string

	RetentionDays int

	MaxBytesPerDay int64

	BufferSize int

	Now func() time.Time

	Location *time.Location

	Log *slog.Logger

	open func(path string) (logFile, error)
}

type logFile interface {
	io.Writer
	io.Closer
	Stat() (os.FileInfo, error)
}

func openLogFile(path string) (logFile, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
}

type Writer struct {
	opts Options
	loc  *time.Location
	log  *slog.Logger

	queue    chan engine.Entry
	stop     chan struct{}
	done     chan struct{}
	midnight *time.Timer

	started   atomic.Bool
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error

	written atomic.Uint64
	dropped atomic.Uint64

	file          logFile
	out           *bufio.Writer
	day           string
	dayBytes      int64
	capWarnedDay  string
	encoded       bytes.Buffer
	pending       uint64
	torn          bool
	reported      uint64
	lastWriteErr  string
	lastWriteErrT time.Time
}

var _ engine.Recorder = (*Writer)(nil)

func Open(opts Options) (*Writer, error) {
	w, err := newWriter(opts)
	if err != nil {
		return nil, err
	}
	w.start()
	return w, nil
}

func newWriter(opts Options) (*Writer, error) {
	if opts.Dir == "" {
		return nil, errors.New("query log: directory is empty")
	}
	if opts.RetentionDays < 1 {
		return nil, fmt.Errorf("query log: retention of %d days, need at least 1", opts.RetentionDays)
	}
	if opts.BufferSize <= 0 {
		opts.BufferSize = defaultBufferSize
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.open == nil {
		opts.open = openLogFile
	}
	w := &Writer{
		opts:  opts,
		loc:   cmpLocation(opts.Location),
		log:   opts.Log,
		queue: make(chan engine.Entry, opts.BufferSize),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	if w.log == nil {
		w.log = slog.New(slog.DiscardHandler)
	}
	if err := os.MkdirAll(opts.Dir, dirMode); err != nil {
		return nil, fmt.Errorf("query log: create %s: %w", opts.Dir, err)
	}
	w.purge()
	return w, nil
}

func cmpLocation(loc *time.Location) *time.Location {
	if loc == nil {
		return time.Local
	}
	return loc
}

func (w *Writer) start() {
	if w.started.CompareAndSwap(false, true) {
		w.midnight = time.NewTimer(w.untilMidnight())
		go w.run()
	}
}

func (w *Writer) Record(e engine.Entry) {
	if w.closed.Load() {
		w.dropped.Add(1)
		return
	}
	select {
	case w.queue <- e:
	default:
		w.dropped.Add(1)
	}
}

func (w *Writer) Written() uint64 { return w.written.Load() }

func (w *Writer) Dropped() uint64 { return w.dropped.Load() }

func (w *Writer) Close() error {
	w.closeOnce.Do(func() {
		w.closed.Store(true)
		close(w.stop)
		if w.started.Load() {
			<-w.done
		} else {
			w.shutdown()
		}
	})
	return w.closeErr
}

func (w *Writer) run() {
	defer close(w.done)
	defer w.midnight.Stop()
	report := time.NewTicker(dropReportInterval)
	defer report.Stop()
	for {
		select {
		case e := <-w.queue:
			w.write(e)
			if len(w.queue) == 0 {
				w.flush()
			}
		case <-w.midnight.C:
			w.purge()
			w.midnight.Reset(w.untilMidnight())
		case <-report.C:
			w.reportDrops()
		case <-w.stop:
			w.shutdown()
			return
		}
	}
}

func (w *Writer) shutdown() {
	w.drain()
	w.closeErr = errors.Join(w.flush(), w.closeFile())
	w.log.Info("query log closed", "written", w.written.Load(), "dropped", w.dropped.Load())
}

func (w *Writer) drain() {
	for {
		select {
		case e := <-w.queue:
			w.write(e)
		default:
			return
		}
	}
}

func (w *Writer) untilMidnight() time.Duration {
	now := w.opts.Now().In(w.loc)
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, w.loc)
	return max(next.Sub(now), 0) + midnightMargin
}

func (w *Writer) purge() {
	removed, err := purgeOld(w.opts.Dir, w.opts.Now(), w.opts.RetentionDays, w.loc, w.log)
	if err != nil {
		w.log.Warn("query log retention sweep failed", "error", err)
	}
	if removed > 0 {
		w.log.Info("query log retention sweep", "removed", removed)
	}
}

func (w *Writer) reportDrops() {
	if total := w.dropped.Load(); total > w.reported {
		w.log.Warn("query log is not keeping up, records were dropped", "dropped_total", total, "since_last_report", total-w.reported)
		w.reported = total
	}
}

func (w *Writer) write(e engine.Entry) {
	rec := newRecord(e, w.loc, w.opts.Now())
	if err := w.encode(rec); err != nil {
		w.fail(err)
		return
	}
	if err := w.selectFile(rec.Time.Format(dayLayout)); err != nil {
		w.fail(err)
		return
	}
	if w.overCap(rec.Time.Format(dayLayout)) {
		return
	}
	buffered, added := w.out.Buffered(), 0
	if w.torn && w.out.WriteByte('\n') == nil {
		w.torn = false
		added = 1
	}
	n, err := w.out.Write(w.encoded.Bytes())
	w.dayBytes += int64(n)
	if err != nil {
		w.discardBuffered()
		w.fail(fmt.Errorf("write query log: %w", err))
		return
	}
	if w.out.Buffered() < buffered+added+n {
		w.pending = 0
	}
	w.written.Add(1)
	w.pending++
}

func (w *Writer) discardBuffered() {
	w.out.Reset(w.file)
	w.torn = true
	if w.pending == 0 {
		return
	}
	w.written.Add(^(w.pending - 1))
	w.dropped.Add(w.pending)
	w.pending = 0
}

func (w *Writer) encode(rec Record) error {
	w.encoded.Reset()
	enc := json.NewEncoder(&w.encoded)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rec); err != nil {
		return fmt.Errorf("encode query log record: %w", err)
	}
	return nil
}

func (w *Writer) overCap(day string) bool {
	limit := w.opts.MaxBytesPerDay
	if limit <= 0 || w.dayBytes+int64(w.encoded.Len()) <= limit {
		return false
	}
	w.dropped.Add(1)
	if w.capWarnedDay != day {
		w.capWarnedDay = day
		w.log.Warn("query log reached its daily size cap, further records of this day are dropped", "day", day, "max_bytes", limit)
	}
	return true
}

func (w *Writer) selectFile(day string) error {
	if w.file != nil && w.day == day {
		return nil
	}
	rotating := w.file != nil
	if err := errors.Join(w.flush(), w.closeFile()); err != nil {
		w.log.Warn("closing the previous query log failed", "error", err)
	}
	path := filepath.Join(w.opts.Dir, filePrefix+day+fileSuffix)
	file, err := w.opts.open(path)
	if err != nil {
		return fmt.Errorf("open query log: %w", err)
	}
	w.file, w.out, w.day = file, bufio.NewWriterSize(file, fileBufferBytes), day
	w.pending, w.torn = 0, false
	w.dayBytes = existingSize(file)
	if rotating {
		w.purge()
	}
	return nil
}

func existingSize(file logFile) int64 {
	if info, err := file.Stat(); err == nil {
		return info.Size()
	}
	return 0
}

func (w *Writer) flush() error {
	if w.out == nil {
		return nil
	}
	if err := w.out.Flush(); err != nil {
		w.discardBuffered()
		w.reportFailure(fmt.Errorf("flush query log: %w", err))
		return err
	}
	w.pending = 0
	return nil
}

func (w *Writer) closeFile() error {
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file, w.out, w.day = nil, nil, ""
	if err != nil {
		return fmt.Errorf("close query log: %w", err)
	}
	return nil
}

func (w *Writer) fail(err error) {
	w.dropped.Add(1)
	w.reportFailure(err)
}

func (w *Writer) reportFailure(err error) {
	now := w.opts.Now()
	if msg := err.Error(); msg != w.lastWriteErr || now.Sub(w.lastWriteErrT) > time.Minute {
		w.log.Error("query log write failed, the record was dropped", "error", err)
		w.lastWriteErr, w.lastWriteErrT = msg, now
	}
}
