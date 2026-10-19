package native

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

type storeFixture struct {
	store    *certStore
	pki      *testPKI
	logs     *logSink
	dir      string
	certPath string
	keyPath  string
	clock    time.Time
	base     time.Time
}

func newStoreFixture(t *testing.T) *storeFixture {
	t.Helper()
	f := &storeFixture{pki: newTestPKI(t), logs: &logSink{}, dir: t.TempDir(), base: time.Now()}
	f.clock = f.base
	certPEM, keyPEM := f.pki.issue(t, 1, []string{dotBase, "*." + dotBase}, f.base.Add(48*time.Hour))
	f.certPath, f.keyPath = writePair(t, f.dir, certPEM, keyPEM)
	store, err := newCertStore(
		config.DoT{CertFile: f.certPath, KeyFile: f.keyPath, Names: []string{dotName}},
		func() time.Time { return f.clock }, slog.New(f.logs))
	if err != nil {
		t.Fatalf("newCertStore: %v", err)
	}
	f.store = store
	return f
}

func (f *storeFixture) serial() int64 {
	return f.store.cur.Load().leaf.SerialNumber.Int64()
}

func (f *storeFixture) renew(t *testing.T, serial int64) {
	t.Helper()
	certPEM, keyPEM := f.pki.issue(t, serial, []string{dotBase, "*." + dotBase}, f.base.Add(48*time.Hour))
	writePair(t, f.dir, certPEM, keyPEM)
}

func TestCertStore_RetriesACertificateThatWasNotYetValid(t *testing.T) {
	f := newStoreFixture(t)
	f.renew(t, 2)

	f.clock = f.base.Add(-2 * time.Hour)
	f.store.check()
	if got := f.serial(); got != 1 {
		t.Fatalf("serial after the early check = %d, want the old certificate (1)", got)
	}
	f.clock = f.base
	f.store.check()

	if got := f.serial(); got != 2 {
		t.Errorf("serial after the clock caught up = %d, want 2: a certificate rejected for its dates must be retried", got)
	}
}

func TestCertStore_DoesNotRetryACertificateThatIsBroken(t *testing.T) {
	f := newStoreFixture(t)
	if err := os.WriteFile(f.certPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	f.store.check()
	f.clock = f.clock.Add(2 * time.Hour)
	f.store.check()

	failures := 0
	for _, r := range f.logs.recs {
		if r.level == slog.LevelError {
			failures++
		}
	}
	if failures != 1 {
		t.Errorf("reload errors logged = %d, want 1 for the same broken files", failures)
	}
}

func TestCertStore_ReportsFilesThatCannotBeRead(t *testing.T) {
	f := newStoreFixture(t)
	if err := os.Remove(f.certPath); err != nil {
		t.Fatal(err)
	}

	f.store.check()

	if !f.logs.has(slog.LevelError, "cannot be read") {
		t.Error("a missing certificate file was not reported")
	}
	if got := f.serial(); got != 1 {
		t.Errorf("serial = %d, want the old certificate to stay in use", got)
	}
}

func TestCertStore_ReadErrorsAreReportedOncePerHour(t *testing.T) {
	f := newStoreFixture(t)
	if err := os.Remove(f.certPath); err != nil {
		t.Fatal(err)
	}

	f.store.check()
	f.store.check()
	f.clock = f.clock.Add(61 * time.Minute)
	f.store.check()

	reports := 0
	for _, r := range f.logs.recs {
		if r.level == slog.LevelError {
			reports++
		}
	}
	if reports != 2 {
		t.Errorf("read errors logged = %d, want 2 (one now, one after an hour)", reports)
	}
}
