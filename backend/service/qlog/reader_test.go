package qlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLog(t *testing.T, dir, name string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func jsonLine(ts, qname, action string, extra string) string {
	return `{"ts":"` + ts + `","client":"192.168.1.23","proto":"udp","qname":"` + qname + `","qtype":"A","action":"` + action + `"` + extra + `,"ms":0}`
}

func collect(t *testing.T, dir string, f Filter) ([]Record, ReadStats) {
	t.Helper()
	f.Location = testZone
	var got []Record
	stats, err := Read(dir, f, func(r Record) { got = append(got, r) })
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return got, stats
}

func qnames(records []Record) []string {
	names := make([]string, len(records))
	for i, r := range records {
		names[i] = r.QName
	}
	return names
}

func sameNames(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func fixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeLog(t, dir, "queries-20261017.jsonl",
		jsonLine("2026-10-17T23:59:50.000+07:00", "yesterday.example.com", "pass", `,"rcode":"NOERROR"`),
	)
	writeLog(t, dir, "queries-20261018.jsonl",
		jsonLine("2026-10-18T14:31:00.000+07:00", "early.example.com", "pass", `,"rcode":"NOERROR"`),
		"this line is not json",
		jsonLine("2026-10-18T14:32:07.412+07:00", "ads.contoh.com", "block", `,"reason":"list","source":"hagezi-popups"`),
		`{"ts":"not a time","qname":"badts.example.com","action":"pass","ms":0}`,
		jsonLine("2026-10-18T14:32:20.000+07:00", "Tracker.Example.com", "block", `,"reason":"cname","source":"hagezi-pro","via":"x.pelacak.net"`),
		jsonLine("2026-10-18T15:10:00.000+07:00", "late.example.com", "pass", `,"rcode":"NOERROR"`),
	)
	return dir
}

func TestRead_ReturnsRecordsInOrderAcrossFilesAndCountsMalformedLines(t *testing.T) {
	got, stats := collect(t, fixtureDir(t), Filter{})
	want := []string{"yesterday.example.com", "early.example.com", "ads.contoh.com", "Tracker.Example.com", "late.example.com"}
	if !sameNames(qnames(got), want) {
		t.Errorf("records = %v, want %v", qnames(got), want)
	}
	if stats.Matched != 5 || stats.Malformed != 2 {
		t.Errorf("stats = %+v, want 5 matched and 2 malformed", stats)
	}
}

func TestRead_DecodesTheFields(t *testing.T) {
	got, _ := collect(t, fixtureDir(t), Filter{BlockedOnly: true})
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	r := got[1]
	if r.Source != "hagezi-pro" || r.Via != "x.pelacak.net" || r.Reason != "cname" || r.Action != "block" || r.Client != "192.168.1.23" {
		t.Errorf("record = %+v", r)
	}
	want := time.Date(2026, 10, 18, 14, 32, 20, 0, testZone)
	if !r.Time.Equal(want) {
		t.Errorf("Time = %v, want %v", r.Time, want)
	}
}

func TestRead_Filters(t *testing.T) {
	zoneTime := func(h, m, s int) time.Time { return time.Date(2026, 10, 18, h, m, s, 0, testZone) }
	tests := []struct {
		name   string
		filter Filter
		want   []string
	}{
		{"blocked only", Filter{BlockedOnly: true}, []string{"ads.contoh.com", "Tracker.Example.com"}},
		{"grep is case-insensitive on the name", Filter{Grep: "TRACKER"}, []string{"Tracker.Example.com"}},
		{"grep matches the list name", Filter{Grep: "popups"}, []string{"ads.contoh.com"}},
		{"grep matches the cname target", Filter{Grep: "pelacak"}, []string{"Tracker.Example.com"}},
		{"grep without a hit", Filter{Grep: "no-such-thing"}, nil},
		{"since", Filter{Since: zoneTime(14, 32, 0)}, []string{"ads.contoh.com", "Tracker.Example.com", "late.example.com"}},
		{"until", Filter{Until: zoneTime(14, 32, 10)}, []string{"yesterday.example.com", "early.example.com", "ads.contoh.com"}},
		{"since and until", Filter{Since: zoneTime(14, 32, 0), Until: zoneTime(14, 33, 0)}, []string{"ads.contoh.com", "Tracker.Example.com"}},
		{"window is inclusive", Filter{Since: zoneTime(14, 32, 20), Until: zoneTime(14, 32, 20)}, []string{"Tracker.Example.com"}},
		{"filters combine", Filter{Since: zoneTime(14, 0, 0), BlockedOnly: true, Grep: "ads"}, []string{"ads.contoh.com"}},
	}
	dir := fixtureDir(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := collect(t, dir, tt.filter)
			if !sameNames(qnames(got), tt.want) {
				t.Errorf("records = %v, want %v", qnames(got), tt.want)
			}
		})
	}
}

func TestRead_OnlyOpensFilesInsideTheWindow(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "queries-20261016.jsonl", jsonLine("2026-10-16T10:00:00.000+07:00", "old.example.com", "pass", ""))
	writeLog(t, dir, "queries-20261018.jsonl", jsonLine("2026-10-18T10:00:00.000+07:00", "new.example.com", "pass", ""))

	got, _ := collect(t, dir, Filter{Since: time.Date(2026, 10, 18, 0, 0, 0, 0, testZone)})
	if !sameNames(qnames(got), []string{"new.example.com"}) {
		t.Errorf("records = %v", qnames(got))
	}
}

func TestRead_MissingDirectoryMeansNoLog(t *testing.T) {
	stats, err := Read(filepath.Join(t.TempDir(), "nope"), Filter{}, func(Record) { t.Error("callback called") })
	if err != nil || stats.Matched != 0 {
		t.Errorf("Read = %+v, %v; want no records and no error", stats, err)
	}
}

func TestRead_IgnoresOtherFiles(t *testing.T) {
	dir := fixtureDir(t)
	writeLog(t, dir, "notes.txt", jsonLine("2026-10-18T14:32:07.412+07:00", "notes.example.com", "block", ""))
	writeLog(t, dir, "queries-20261018.jsonl.bak", jsonLine("2026-10-18T14:32:07.412+07:00", "bak.example.com", "block", ""))
	got, _ := collect(t, dir, Filter{})
	for _, name := range qnames(got) {
		if strings.HasPrefix(name, "notes") || strings.HasPrefix(name, "bak") {
			t.Errorf("record %s came from a file that is not a query log", name)
		}
	}
}

func TestRead_HugeLineIsSkippedAndReadingContinues(t *testing.T) {
	dir := t.TempDir()
	huge := `{"ts":"2026-10-18T10:00:00.000+07:00","qname":"` + strings.Repeat("a", 3<<20) + `","action":"pass","ms":0}`
	writeLog(t, dir, "queries-20261018.jsonl",
		jsonLine("2026-10-18T09:00:00.000+07:00", "before.example.com", "pass", ""),
		huge,
		jsonLine("2026-10-18T11:00:00.000+07:00", "after.example.com", "pass", ""),
	)
	got, stats := collect(t, dir, Filter{})
	if !sameNames(qnames(got), []string{"before.example.com", "after.example.com"}) {
		t.Errorf("records = %v", qnames(got))
	}
	if stats.Malformed != 1 {
		t.Errorf("Malformed = %d, want 1 for the huge line", stats.Malformed)
	}
}

func TestRead_SanitizesWhatItReturns(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "queries-20261018.jsonl",
		`{"ts":"2026-10-18T09:00:00.000+07:00","qname":"a\u001b[31mb.example.com","action":"pass","ms":0}`)
	got, _ := collect(t, dir, Filter{})
	if len(got) != 1 || got[0].QName != "a?[31mb.example.com" {
		t.Errorf("records = %+v, want the escape character replaced", got)
	}
}

func TestRead_ReportsALogDirectoryThatCannotBeListed(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(notADir, Filter{Location: testZone}, func(Record) {}); err == nil {
		t.Error("Read ignored a log path that is a regular file")
	}
}

func TestRecordLine(t *testing.T) {
	tests := []struct {
		name string
		rec  Record
		want string
	}{
		{
			"list block",
			Record{Time: time.Date(2026, 10, 18, 14, 32, 7, 412_000_000, testZone), Client: "192.168.1.23", QType: "A", QName: "ads.contoh.com", Action: "block", Reason: "list", Source: "hagezi-popups"},
			"2026-10-18 14:32:07.412 BLOK  192.168.1.23 A ads.contoh.com list:hagezi-popups",
		},
		{
			"cname block",
			Record{Time: time.Date(2026, 10, 18, 14, 32, 20, 0, testZone), Client: "192.168.1.23", QType: "A", QName: "smetrics.situs.com", Action: "block", Reason: "cname", Source: "hagezi-pro", Via: "x.pelacak.net"},
			"2026-10-18 14:32:20.000 BLOK  192.168.1.23 A smetrics.situs.com cname:hagezi-pro via x.pelacak.net",
		},
		{
			"pass",
			Record{Time: time.Date(2026, 10, 18, 14, 32, 8, 0, testZone), Client: "192.168.1.23", QType: "A", QName: "www.contoh.com", Action: "pass", RCode: "NOERROR", MS: 38, Upstream: "https://1.1.1.1/dns-query"},
			"2026-10-18 14:32:08.000 LOLOS 192.168.1.23 A www.contoh.com NOERROR 38ms https://1.1.1.1/dns-query",
		},
		{
			"cached pass",
			Record{Time: time.Date(2026, 10, 18, 14, 32, 9, 0, testZone), Client: "192.168.1.23", QType: "AAAA", QName: "www.contoh.com", Action: "pass", RCode: "NOERROR", Cached: boolPtr(true)},
			"2026-10-18 14:32:09.000 LOLOS 192.168.1.23 AAAA www.contoh.com NOERROR 0ms cache",
		},
		{
			"allowed pass",
			Record{Time: time.Date(2026, 10, 18, 14, 32, 10, 0, testZone), Client: "192.168.1.23", QType: "A", QName: "cdn.example.com", Action: "pass", RCode: "NOERROR", MS: 5, Upstream: "u", Reason: "allow", Source: "allow cdn.example.com"},
			"2026-10-18 14:32:10.000 LOLOS 192.168.1.23 A cdn.example.com NOERROR 5ms u allow:allow cdn.example.com",
		},
		{
			"error",
			Record{Time: time.Date(2026, 10, 18, 14, 32, 11, 0, testZone), Client: "192.168.1.23", QType: "A", QName: "x.example.com", Action: "error", RCode: "SERVFAIL", Error: "all upstreams failed"},
			"2026-10-18 14:32:11.000 GALAT 192.168.1.23 A x.example.com SERVFAIL 0ms all upstreams failed",
		},
		{
			"no client",
			Record{Time: time.Date(2026, 10, 18, 14, 32, 12, 0, testZone), QType: "A", QName: "x.example.com", Action: "pass"},
			"2026-10-18 14:32:12.000 LOLOS - A x.example.com 0ms",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rec.Line(testZone); got != tt.want {
				t.Errorf("Line() =\n %q\nwant\n %q", got, tt.want)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }
