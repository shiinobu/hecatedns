package qlog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDays_ListsOnlyRegularDayFilesInOrder(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"queries-20261020.jsonl", "queries-20261018.jsonl", "notes.txt", "queries-today.jsonl", "lifetime-counts.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "queries-20261019.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}

	days, err := Days(dir, time.UTC)

	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)}
	if len(days) != len(want) || !days[0].Equal(want[0]) || !days[1].Equal(want[1]) {
		t.Errorf("days = %v, want %v", days, want)
	}
}

func TestDays_ReturnsNothingForAMissingDirectory(t *testing.T) {
	days, err := Days(filepath.Join(t.TempDir(), "absent"), time.UTC)

	if err != nil || len(days) != 0 {
		t.Errorf("days = %v, err = %v, want none and no error", days, err)
	}
}

func TestDays_ReportsTheLocationOfTheDay(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "queries-20261020.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	jakarta := time.FixedZone("WIB", 7*3600)

	days, err := Days(dir, jakarta)

	if err != nil || len(days) != 1 {
		t.Fatalf("days = %v, err = %v", days, err)
	}
	if want := time.Date(2026, 10, 20, 0, 0, 0, 0, jakarta); !days[0].Equal(want) || days[0].Location() != jakarta {
		t.Errorf("day = %v, want midnight %v", days[0], want)
	}
}
