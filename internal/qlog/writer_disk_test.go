package qlog

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shiinobu/hecatedns/internal/engine"
)

var errDiskFull = errors.New("There is not enough space on the disk.")

// fakeDisk is a file system with a byte capacity. A write that does not fit
// stores what fits and fails, as a full disk does.
type fakeDisk struct {
	mu       sync.Mutex
	data     bytes.Buffer
	capacity int
}

func (d *fakeDisk) open(string) (logFile, error) { return &diskFile{d: d}, nil }

func (d *fakeDisk) setCapacity(n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.capacity = n
}

func (d *fakeDisk) used() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.data.Len()
}

func (d *fakeDisk) contents() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.data.String()
}

type diskFile struct{ d *fakeDisk }

func (f *diskFile) Write(p []byte) (int, error) {
	f.d.mu.Lock()
	defer f.d.mu.Unlock()
	free := f.d.capacity - f.d.data.Len()
	if free < len(p) {
		n, _ := f.d.data.Write(p[:max(free, 0)])
		return n, errDiskFull
	}
	return f.d.data.Write(p)
}

func (f *diskFile) Close() error               { return nil }
func (f *diskFile) Stat() (os.FileInfo, error) { return nil, errors.New("no stat on the fake disk") }

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func namedEntry(at time.Time, name string) engine.Entry {
	e := blockEntry(at)
	e.QName = name
	return e
}

func TestWriter_RecoversWhenTheDiskHasRoomAgain(t *testing.T) {
	at := time.Date(2026, 10, 19, 9, 0, 0, 0, testZone)
	disk := &fakeDisk{capacity: 1 << 20}
	opts := testOptions(t, newFakeClock(at))
	opts.open = disk.open
	w := openWriter(t, opts)

	for i := 0; i < 3; i++ {
		w.Record(namedEntry(at, "before.contoh.com"))
	}
	eventually(t, "the first three records on disk", func() bool { return strings.Count(disk.contents(), "before.contoh.com") == 3 })

	disk.setCapacity(disk.used() + 10)
	for i := 0; i < 5; i++ {
		w.Record(namedEntry(at, "during.contoh.com"))
	}
	eventually(t, "the five records during the outage to be dropped", func() bool { return w.Dropped() >= 5 })

	disk.setCapacity(1 << 20)
	for i := 0; i < 3; i++ {
		w.Record(namedEntry(at, "after.contoh.com"))
	}
	eventually(t, "records to reach the disk again", func() bool { return strings.Count(disk.contents(), "after.contoh.com") == 3 })

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got, want := w.Written(), uint64(6); got != want {
		t.Errorf("Written() = %d, want %d: the 3 before and the 3 after; the records of the outage never reached the disk", got, want)
	}
	if got, want := w.Dropped(), uint64(5); got != want {
		t.Errorf("Dropped() = %d, want %d (the records of the outage)", got, want)
	}
}

func TestWriter_RecordsThatBufioAlreadyFlushedAreNotCountedAsDroppedWhenALaterFlushFails(t *testing.T) {
	at := time.Date(2026, 10, 19, 9, 0, 0, 0, testZone)
	disk := &fakeDisk{capacity: 1 << 20}
	opts := testOptions(t, newFakeClock(at))
	opts.open = disk.open
	w, err := newWriter(opts)
	if err != nil {
		t.Fatal(err)
	}
	const records = 500
	for i := 0; i < records; i++ {
		w.write(namedEntry(at, "busy.contoh.com"))
	}
	if disk.used() == 0 {
		t.Fatalf("nothing reached the disk after %d records: the test must outgrow the %d-byte buffer", records, fileBufferBytes)
	}
	disk.setCapacity(disk.used())

	if err := w.flush(); err == nil {
		t.Fatal("flush onto a full disk succeeded")
	}

	onDisk := uint64(strings.Count(disk.contents(), "\n"))
	if got := w.Written(); got != onDisk {
		t.Errorf("Written() = %d, but %d whole records are on the disk: the ones bufio flushed by itself were persisted", got, onDisk)
	}
	if got := w.Written() + w.Dropped(); got != records {
		t.Errorf("Written()+Dropped() = %d, want %d: every record is one or the other", got, records)
	}
}

func TestWriter_FirstRecordAfterAPartialWriteStartsOnItsOwnLine(t *testing.T) {
	at := time.Date(2026, 10, 19, 9, 0, 0, 0, testZone)
	disk := &fakeDisk{capacity: 20}
	opts := testOptions(t, newFakeClock(at))
	opts.open = disk.open
	w := openWriter(t, opts)

	w.Record(namedEntry(at, "torn.contoh.com"))
	eventually(t, "the torn record to be dropped", func() bool { return w.Dropped() >= 1 })
	disk.setCapacity(1 << 20)
	w.Record(namedEntry(at, "whole.contoh.com"))
	eventually(t, "the next record on disk", func() bool { return strings.Contains(disk.contents(), "whole.contoh.com") })

	var found bool
	for _, line := range strings.Split(disk.contents(), "\n") {
		if strings.Contains(line, "whole.contoh.com") {
			found = true
			if !strings.HasPrefix(line, "{") {
				t.Errorf("the record after the torn one shares its line with the damage: %q", line)
			}
		}
	}
	if !found {
		t.Fatal("record after the outage missing")
	}
}
