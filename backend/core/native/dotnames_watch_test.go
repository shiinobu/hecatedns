package native

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/config"
	"github.com/shiinobu/hecatedns/backend/lib/dotnames"
)

const (
	raceHandshakers = 8
	raceFlips       = 40
	raceFlipPause   = 5 * time.Millisecond
	raceConnLimit   = 1024
)

type watchFixture struct {
	store *storeFixture
	path  string
	clock time.Time
	watch *namesWatch
}

func newWatchFixture(t *testing.T, names []string, path string, settled bool) *watchFixture {
	t.Helper()
	f := &watchFixture{store: newStoreFixture(t), path: path}
	f.clock = time.Now()
	if info, err := os.Stat(path); err == nil {
		f.clock = info.ModTime()
	}
	if settled {
		f.clock = f.clock.Add(time.Hour)
	}
	w, err := newNamesWatch(config.DoT{Names: names, NamesFile: path}, f.store.store, func() time.Time { return f.clock }, slog.New(f.store.logs))
	if err != nil {
		t.Fatalf("newNamesWatch: %v", err)
	}
	f.watch = w
	return f
}

func writeNamesKeepingTheStamp(t *testing.T, path string, host string) {
	t.Helper()
	before, err := dotnames.StatFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := dotnames.Write(path, []dotnames.Entry{{Host: host}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.Time, before.Time); err != nil {
		t.Fatal(err)
	}
	after, err := dotnames.StatFile(path)
	if err != nil || !after.Time.Equal(before.Time) || after.Size != before.Size {
		t.Fatalf("precondition: the rewrite changed the stamp from %+v to %+v (%v)", before, after, err)
	}
}

func seededNamesPath(t *testing.T, host string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dot-names.txt")
	if err := dotnames.Write(path, []dotnames.Entry{{Host: host}}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNamesWatch_ReadsTheFileAgainOnlyWhenItsStampChanges(t *testing.T) {
	path := seededNamesPath(t, fileDoTName)
	f := newWatchFixture(t, []string{dotName}, path, true)

	writeNamesKeepingTheStamp(t, path, otherFileDoTName)
	f.watch.check()
	if !f.watch.serves(fileDoTName) || f.watch.serves(otherFileDoTName) {
		t.Fatal("the file was read again although its stamp did not change")
	}
	later := f.clock.Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	f.watch.check()

	if f.watch.serves(fileDoTName) || !f.watch.serves(otherFileDoTName) {
		t.Error("one check after the stamp changed did not swap the names")
	}
}

func TestNamesWatch_ReadsAgainWhileTheStampIsTooFreshToTrust(t *testing.T) {
	path := seededNamesPath(t, fileDoTName)
	f := newWatchFixture(t, []string{dotName}, path, false)

	writeNamesKeepingTheStamp(t, path, otherFileDoTName)
	f.watch.check()

	if f.watch.serves(fileDoTName) || !f.watch.serves(otherFileDoTName) {
		t.Error("a same-size rewrite within the file time resolution was missed")
	}
}

func TestNamesWatch_ReportsAFreshUnreadableFileOnlyOnceItHasSettled(t *testing.T) {
	path := seededNamesPath(t, fileDoTName)
	f := newWatchFixture(t, []string{dotName}, path, false)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	f.watch.check()
	if n := f.store.logs.count(slog.LevelError, "names file"); n != 0 {
		t.Fatalf("names file errors logged while the file is fresh = %d, want 0", n)
	}
	f.clock = f.clock.Add(time.Hour)
	f.watch.check()
	f.watch.check()

	if n := f.store.logs.count(slog.LevelError, "names file"); n != 1 {
		t.Errorf("names file errors logged after the file settled = %d, want 1", n)
	}
	if !f.watch.serves(fileDoTName) {
		t.Error("the unreadable file removed a name that was being served")
	}
}

func TestNamesWatch_ReportsAPathThatCannotBeCheckedOnce(t *testing.T) {
	f := newWatchFixture(t, []string{dotName}, filepath.Join(t.TempDir(), "bad\x00name"), true)

	for range 3 {
		f.watch.check()
	}

	if n := f.store.logs.count(slog.LevelError, "names file"); n != 1 {
		t.Errorf("names file errors logged = %d, want 1", n)
	}
	if !f.watch.serves(dotName) {
		t.Error("the configured name is no longer served")
	}
}

func TestNamesWatch_PollerStopsWhenItsContextIsCancelled(t *testing.T) {
	f := newWatchFixture(t, []string{dotName}, seededNamesPath(t, fileDoTName), true)
	f.watch.every = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		f.watch.watch(ctx)
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the names poller is still running after cancel")
	}
}

func TestDoTNames_ServerStopsThePollerWhenRunReturns(t *testing.T) {
	path := seededNamesPath(t, fileDoTName)
	env, err := newDoTEnv(t, time.Now().Add(60*24*time.Hour), namesConfig([]string{dotName}, path))
	if err != nil {
		t.Fatal(err)
	}
	env.s.names.every = namesTestPoll
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- env.s.Run(ctx) }()
	<-env.s.Ready()

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
	if err := dotnames.Write(path, []dotnames.Entry{{Host: otherFileDoTName}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * namesTestPoll)

	if env.s.names.serves(otherFileDoTName) {
		t.Error("the names poller kept running after Run returned")
	}
}

func raiseDoTLimits(l *dotLimits) {
	l.preTotal, l.preIP, l.total, l.perIP = raceConnLimit, raceConnLimit, raceConnLimit, raceConnLimit
}

func TestDoTNames_AtomicRewritesUnderConcurrentHandshakesNeverDropTheConfigName(t *testing.T) {
	env, path := startNamesEnv(t, []string{dotName}, namesLines(fileDoTName), raiseDoTLimits)
	addr := env.addr(t)
	stop := make(chan struct{})
	var handshakers sync.WaitGroup
	var tried, failed atomic.Int64
	for range raceHandshakers {
		handshakers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				tried.Add(1)
				if handshakeWith(addr, env.pki.pool, dotName) != nil {
					failed.Add(1)
				}
				_ = handshakeWith(addr, env.pki.pool, fileDoTName)
				_ = handshakeWith(addr, env.pki.pool, otherFileDoTName)
			}
		})
	}

	sets := [][]dotnames.Entry{{{Host: fileDoTName}}, {{Host: otherFileDoTName}}}
	for i := range raceFlips {
		if err := dotnames.Write(path, sets[i%2]); err != nil {
			t.Errorf("rewrite %d: %v", i, err)
			break
		}
		time.Sleep(raceFlipPause)
	}
	close(stop)
	handshakers.Wait()

	if tried.Load() == 0 || failed.Load() != 0 {
		t.Errorf("configured-name handshakes: %d failed of %d, want none to fail", failed.Load(), tried.Load())
	}
	eventually(t, namesTestWait, "the last set to be served", func() bool {
		return env.accepts(t, otherFileDoTName) && !env.accepts(t, fileDoTName)
	})
	if errs := env.logs.matching(slog.LevelError, "names file"); len(errs) != 0 {
		t.Errorf("names file errors logged during atomic rewrites = %q, want none", errs)
	}
}
