package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func (c *fakeClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = at
}

func octoberAt(day, hour, minute int) time.Time {
	return time.Date(2026, 10, day, hour, minute, 0, 0, time.UTC)
}

func newLifetimeFixture(t *testing.T, mut func(*APIOptions)) (*apiFixture, string) {
	t.Helper()
	dataDir := t.TempDir()
	f := newAPIFixture(t, func(o *APIOptions) {
		o.DataDir = dataDir
		if mut != nil {
			mut(o)
		}
	})
	return f, dataDir
}

func (f *apiFixture) lifetime() map[string]any {
	f.t.Helper()
	got := decodeMap(f.t, f.get("/api/status"))
	life, _ := got["lifetime"].(map[string]any)
	return life
}

func (f *apiFixture) logDay(day int, action string, count int) {
	f.t.Helper()
	for i := 0; i < count; i++ {
		f.log(octoberAt(day, 9, i), "a.example.com", "A", action, "", "")
	}
}

func (f *apiFixture) purgeDay(day int) {
	f.t.Helper()
	name := "queries-" + octoberAt(day, 0, 0).Format("20060102") + ".jsonl"
	if err := os.Remove(filepath.Join(f.logDir, name)); err != nil {
		f.t.Fatal(err)
	}
}

func TestStatus_LifetimeCountsEveryDayInTheLog(t *testing.T) {
	f, _ := newLifetimeFixture(t, nil)
	f.logDay(18, "pass", 2)
	f.logDay(18, "block", 1)
	f.logDay(19, "block", 1)
	f.logDay(20, "pass", 1)

	life := f.lifetime()

	want := map[string]any{"total": 5.0, "blocked": 2.0, "passed": 3.0, "errors": 0.0, "partial": false, "since": "2026-10-18T00:00:00Z"}
	for key, value := range want {
		if life[key] != value {
			t.Errorf("lifetime.%s = %v, want %v (all: %v)", key, life[key], value, life)
		}
	}
}

func TestStatus_LifetimeKeepsADayAfterItsLogIsPurged(t *testing.T) {
	f, dataDir := newLifetimeFixture(t, nil)
	f.logDay(18, "block", 2)
	f.logDay(18, "pass", 1)
	f.logDay(20, "pass", 1)
	if got := f.lifetime()["total"]; got != 4.0 {
		t.Fatalf("total before the purge = %v, want 4", got)
	}

	f.purgeDay(18)
	f.clock.Advance(2 * time.Minute)
	life := f.lifetime()

	if life["total"] != 4.0 || life["blocked"] != 2.0 || life["since"] != "2026-10-18T00:00:00Z" {
		t.Errorf("after the purge: %v, want total 4, blocked 2, since 2026-10-18", life)
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil || len(entries) != 1 {
		t.Errorf("data dir holds %v (err %v), want exactly the counts file", entries, err)
	}
}

func TestStatus_LifetimeGrowsWithNewQueries(t *testing.T) {
	f, _ := newLifetimeFixture(t, nil)
	f.logDay(18, "pass", 2)
	f.logDay(20, "pass", 1)
	if got := f.lifetime()["total"]; got != 3.0 {
		t.Fatalf("first total = %v, want 3", got)
	}

	f.log(octoberAt(20, 10, 30), "b.example.com", "A", "block", "", "")
	f.clock.Advance(2 * time.Minute)

	life := f.lifetime()
	if life["total"] != 4.0 || life["blocked"] != 1.0 {
		t.Errorf("after one more query: %v, want total 4, blocked 1", life)
	}
}

func TestStatus_LifetimeWaitsOneHourBeforeSettlingADay(t *testing.T) {
	f, _ := newLifetimeFixture(t, nil)
	f.clock.set(octoberAt(20, 0, 30))
	f.log(octoberAt(19, 23, 59), "a.example.com", "A", "pass", "", "")
	if got := f.lifetime()["total"]; got != 1.0 {
		t.Fatalf("total at 00:30 = %v, want 1", got)
	}

	f.log(octoberAt(19, 23, 59).Add(30*time.Second), "b.example.com", "A", "pass", "", "")
	f.clock.set(octoberAt(20, 2, 0))

	if got := f.lifetime()["total"]; got != 2.0 {
		t.Errorf("total at 02:00 = %v, want 2: the late record of the day before was lost", got)
	}
}

func TestStatus_LifetimeIsOmittedWithoutADataDir(t *testing.T) {
	f := newAPIFixture(t, nil)
	f.logDay(20, "pass", 1)

	got := decodeMap(t, f.get("/api/status"))

	if got["lifetime"] != nil {
		t.Errorf("lifetime = %v, want null", got["lifetime"])
	}
	if field(t, got, "counts", "total") != 1.0 {
		t.Errorf("the 24 hour counts must not depend on the data dir: %v", got["counts"])
	}
}

func TestStatus_LifetimeDoesNotStoreADayWhoseScanWasCut(t *testing.T) {
	f, dataDir := newLifetimeFixture(t, func(o *APIOptions) { o.ScanRecords = 3 })
	f.logDay(18, "pass", 5)

	life := f.lifetime()

	if life["partial"] != true {
		t.Errorf("lifetime.partial = %v, want true", life["partial"])
	}
	if _, err := os.Stat(filepath.Join(dataDir, lifetimeFileName)); !os.IsNotExist(err) {
		t.Errorf("a cut day was stored (stat err %v)", err)
	}
}

func TestStatus_LifetimeSurvivesManyReadersAtOnce(t *testing.T) {
	f, dataDir := newLifetimeFixture(t, nil)
	f.logDay(18, "pass", 3)
	f.logDay(19, "block", 2)
	f.logDay(20, "pass", 1)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := f.lifetime()["total"]; got != 6.0 {
				t.Errorf("total = %v, want 6", got)
			}
		}()
	}
	wg.Wait()

	f.purgeDay(18)
	f.purgeDay(19)
	f.clock.Advance(2 * time.Minute)
	if got := f.lifetime()["total"]; got != 6.0 {
		t.Errorf("total after the purge = %v, want 6 (the counts file in %s is damaged)", got, dataDir)
	}
}

func (f *apiFixture) setAside(dataDir string) []string {
	f.t.Helper()
	found, err := filepath.Glob(filepath.Join(dataDir, lifetimeFileName+".bad-*"))
	if err != nil {
		f.t.Fatal(err)
	}
	return found
}

func TestStatus_LifetimeSetsAsideAFileItCannotRead(t *testing.T) {
	cases := map[string]string{
		"not json":       `{not json`,
		"newer version":  `{"version":2,"days":{}}`,
		"negative count": `{"version":1,"days":{"2026-10-18":{"total":-1,"blocked":0,"passed":0,"errors":0}}}`,
		"bad day key":    `{"version":1,"days":{"soon":{"total":1,"blocked":0,"passed":1,"errors":0}}}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			f, dataDir := newLifetimeFixture(t, nil)
			if err := os.WriteFile(filepath.Join(dataDir, lifetimeFileName), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			f.logDay(18, "pass", 2)
			f.logDay(20, "pass", 1)

			life := f.lifetime()

			if life["total"] != 3.0 || life["partial"] != true {
				t.Errorf("lifetime = %v, want total 3 from the log and partial", life)
			}
			aside := f.setAside(dataDir)
			if len(aside) != 1 {
				t.Fatalf("set aside files = %v, want exactly one", aside)
			}
			if kept, err := os.ReadFile(aside[0]); err != nil || string(kept) != content {
				t.Errorf("the set aside file changed: %q (err %v)", kept, err)
			}
			f.purgeDay(18)
			f.clock.Advance(2 * time.Minute)
			if got := f.lifetime()["total"]; got != 3.0 {
				t.Errorf("total after the purge = %v, want 3 from the new counts file", got)
			}
		})
	}
}

func TestStatus_LifetimeTrustsAStoredDayOverTheLog(t *testing.T) {
	f, dataDir := newLifetimeFixture(t, nil)
	stored := `{"version":1,"days":{"2026-10-18":{"total":10,"blocked":4,"passed":6,"errors":0}}}`
	if err := os.WriteFile(filepath.Join(dataDir, lifetimeFileName), []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	f.logDay(18, "pass", 2)
	f.logDay(20, "pass", 1)

	life := f.lifetime()

	if life["total"] != 11.0 || life["blocked"] != 4.0 || life["partial"] != false {
		t.Errorf("lifetime = %v, want the stored 10 plus today's 1, blocked 4", life)
	}
}

func TestStatus_LifetimeKeepsCountsWhenTheFileCannotBeWritten(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "plain-file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newAPIFixture(t, func(o *APIOptions) { o.DataDir = notADir })
	f.logDay(18, "pass", 3)
	f.logDay(20, "pass", 1)
	first := f.lifetime()
	if first["total"] != 4.0 || first["partial"] != true {
		t.Fatalf("first lifetime = %v, want total 4 and partial while the file is not saved", first)
	}

	f.purgeDay(18)
	f.clock.Advance(2 * time.Minute)

	if again := f.lifetime(); again["total"] != 4.0 || again["partial"] != true {
		t.Errorf("after the purge = %v, want total 4 kept in memory and still partial", again)
	}
}

func TestStatus_LifetimeKeepsStoredDaysWhenTheLogCannotBeListed(t *testing.T) {
	logIsAFile := filepath.Join(t.TempDir(), "plain-file")
	if err := os.WriteFile(logIsAFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, dataDir := newLifetimeFixture(t, func(o *APIOptions) { o.LogDir = logIsAFile })
	stored := `{"version":1,"days":{"2026-10-18":{"total":7,"blocked":3,"passed":4,"errors":0}}}`
	if err := os.WriteFile(filepath.Join(dataDir, lifetimeFileName), []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}

	life := f.lifetime()

	if life["total"] != 7.0 || life["partial"] != true {
		t.Errorf("lifetime = %v, want the stored 7 and partial", life)
	}
}

func TestRunLifetime_SettlesDaysWithoutAnyoneLoadingTheStatus(t *testing.T) {
	f, dataDir := newLifetimeFixture(t, func(o *APIOptions) { o.LifetimeEvery = 5 * time.Millisecond })
	f.logDay(18, "pass", 2)
	f.logDay(20, "pass", 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.api.RunLifetime(ctx)
	}()

	var stored lifetimeFile
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(filepath.Join(dataDir, lifetimeFileName)); err == nil && json.Unmarshal(raw, &stored) == nil && len(stored.Days) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	if got := stored.Days["2026-10-18"].Total; got != 2 {
		t.Errorf("settled day 2026-10-18 total = %d, want 2 (days: %v)", got, stored.Days)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("RunLifetime did not return after the context was cancelled")
	}
}
