package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	goodLine    = `{"ts":"2026-10-19T10:00:05Z","qname":"fine.example.","qtype":"A","action":"pass"}`
	laterLine   = `{"ts":"2026-10-19T10:00:07Z","qname":"later.example.","qtype":"A","action":"block","reason":"list","source":"hagezi"}`
	oldLine     = `{"ts":"2026-10-19T09:00:00Z","qname":"old.example.","qtype":"A","action":"pass"}`
	brokenLine  = `{"ts":"2026-10-19T10:00:06Z","qname":"broken.exam`
	notJSONLine = `this is not json at all`
)

func writeLog(t *testing.T, dir, name string, lines ...string) {
	t.Helper()
	var body string
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEachRecord_KeepsReadingAfterAnUndecodableLineAndCountsIt(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "queries-20261019.jsonl", goodLine, brokenLine, notJSONLine, laterLine)

	var names []string
	skipped, err := eachRecord(filepath.Join(dir, "queries-20261019.jsonl"), func(r logRecord) { names = append(names, r.QName) })

	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "fine.example." || names[1] != "later.example." {
		t.Errorf("records read = %v, want the two decodable ones around the broken lines", names)
	}
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2", skipped)
	}
}

func TestEachRecord_ReportsAMissingFile(t *testing.T) {
	if _, err := eachRecord(filepath.Join(t.TempDir(), "queries-none.jsonl"), func(logRecord) {}); err == nil {
		t.Error("a missing file gave no error")
	}
}

func TestReadLog_KeepsReadingAfterAnUndecodableLineAndCountsIt(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "queries-20261019.jsonl", oldLine, goodLine, brokenLine, laterLine)
	since := time.Date(2026, 10, 19, 9, 30, 0, 0, time.UTC)

	got, skipped, err := readLog(dir, since)

	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
	if _, ok := got[key{"fine.example", "A"}]; !ok {
		t.Error("the record before the broken line is missing")
	}
	rec, ok := got[key{"later.example", "A"}]
	if !ok || rec.Action != "block" || rec.Source != "hagezi" {
		t.Errorf("record after the broken line = %+v (found %v), want the block by hagezi", rec, ok)
	}
	if _, ok := got[key{"old.example", "A"}]; ok {
		t.Error("a record older than the start of the run was kept")
	}
}
