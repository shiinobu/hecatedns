package qlog

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"xdns/internal/engine"
)

var testZone = time.FixedZone("WIB", 7*60*60)

type fakeClock struct{ now atomic.Pointer[time.Time] }

func newFakeClock(t time.Time) *fakeClock {
	c := &fakeClock{}
	c.Set(t)
	return c
}

func (c *fakeClock) Now() time.Time  { return *c.now.Load() }
func (c *fakeClock) Set(t time.Time) { c.now.Store(&t) }

func testOptions(t *testing.T, clock *fakeClock) Options {
	t.Helper()
	return Options{Dir: filepath.Join(t.TempDir(), "logs"), RetentionDays: 7, Now: clock.Now, Location: testZone}
}

func openWriter(t *testing.T, opts Options) *Writer {
	t.Helper()
	w, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func blockEntry(at time.Time) engine.Entry {
	return engine.Entry{
		Time: at, Client: netip.MustParseAddr("192.168.1.23"), Proto: "udp",
		QName: "ads.contoh.com", QType: "A", Action: engine.ActionBlock,
		Reason: engine.ReasonList, Source: "hagezi-popups", RCode: "NOERROR",
	}
}

func passEntry(at time.Time) engine.Entry {
	return engine.Entry{
		Time: at, Client: netip.MustParseAddr("192.168.1.23"), Proto: "udp",
		QName: "www.contoh.com", QType: "A", Action: engine.ActionPass,
		Upstream: "https://1.1.1.1/dns-query", RCode: "NOERROR", Latency: 38 * time.Millisecond,
	}
}

func TestWriter_WritesTheDocumentedJSONLines(t *testing.T) {
	at := time.Date(2026, 10, 18, 14, 32, 7, 412_000_000, testZone)
	w := openWriter(t, testOptions(t, newFakeClock(at)))

	w.Record(blockEntry(at))
	w.Record(passEntry(at))
	w.Record(engine.Entry{
		Time: at, QName: "smetrics.situs.com", QType: "A", Action: engine.ActionBlock,
		Reason: engine.ReasonCNAME, Source: "hagezi-pro", Via: "x.pelacak.net",
	})
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	want := []string{
		`{"ts":"2026-10-18T14:32:07.412+07:00","client":"192.168.1.23","proto":"udp","qname":"ads.contoh.com","qtype":"A","action":"block","reason":"list","source":"hagezi-popups","rcode":"NOERROR","ms":0}`,
		`{"ts":"2026-10-18T14:32:07.412+07:00","client":"192.168.1.23","proto":"udp","qname":"www.contoh.com","qtype":"A","action":"pass","upstream":"https://1.1.1.1/dns-query","rcode":"NOERROR","ms":38,"cached":false}`,
		`{"ts":"2026-10-18T14:32:07.412+07:00","qname":"smetrics.situs.com","qtype":"A","action":"block","reason":"cname","source":"hagezi-pro","via":"x.pelacak.net","ms":0}`,
	}
	got := readLines(t, filepath.Join(w.opts.Dir, "queries-20261018.jsonl"))
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got %s\nwant %s", i, got[i], want[i])
		}
	}
}

func TestWriter_ErrorEntryCarriesTheErrorText(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	w := openWriter(t, testOptions(t, newFakeClock(at)))
	w.Record(engine.Entry{Time: at, QName: "a.example.com", QType: "A", Action: engine.ActionError, RCode: "SERVFAIL", Error: "all upstreams failed"})
	_ = w.Close()

	lines := readLines(t, filepath.Join(w.opts.Dir, "queries-20261018.jsonl"))
	if len(lines) != 1 || !strings.Contains(lines[0], `"error":"all upstreams failed"`) || strings.Contains(lines[0], `"cached"`) {
		t.Errorf("lines = %q", lines)
	}
}

func TestWriter_CachedPassIsMarked(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	w := openWriter(t, testOptions(t, newFakeClock(at)))
	e := passEntry(at)
	e.Cached = true
	w.Record(e)
	_ = w.Close()
	lines := readLines(t, filepath.Join(w.opts.Dir, "queries-20261018.jsonl"))
	if len(lines) != 1 || !strings.HasSuffix(lines[0], `"ms":38,"cached":true}`) {
		t.Errorf("lines = %q", lines)
	}
}

func TestWriter_ZeroTimeUsesTheClock(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	w := openWriter(t, testOptions(t, newFakeClock(at)))
	e := blockEntry(time.Time{})
	w.Record(e)
	_ = w.Close()
	lines := readLines(t, filepath.Join(w.opts.Dir, "queries-20261018.jsonl"))
	if len(lines) != 1 || !strings.HasPrefix(lines[0], `{"ts":"2026-10-18T09:00:00.000+07:00"`) {
		t.Errorf("lines = %q", lines)
	}
}

func TestWriter_RotatesPerLocalDay(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 10, 18, 23, 59, 0, 0, testZone))
	w := openWriter(t, testOptions(t, clock))

	w.Record(blockEntry(time.Date(2026, 10, 18, 23, 59, 59, 0, testZone)))
	w.Record(blockEntry(time.Date(2026, 10, 19, 0, 0, 1, 0, testZone)))
	w.Record(blockEntry(time.Date(2026, 10, 19, 0, 0, 2, 0, testZone)))
	_ = w.Close()

	if got := len(readLines(t, filepath.Join(w.opts.Dir, "queries-20261018.jsonl"))); got != 1 {
		t.Errorf("day 18 has %d lines, want 1", got)
	}
	if got := len(readLines(t, filepath.Join(w.opts.Dir, "queries-20261019.jsonl"))); got != 2 {
		t.Errorf("day 19 has %d lines, want 2", got)
	}
}

func TestWriter_AppendsToAnExistingFile(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	opts := testOptions(t, newFakeClock(at))
	first := openWriter(t, opts)
	first.Record(blockEntry(at))
	_ = first.Close()

	second := openWriter(t, opts)
	second.Record(blockEntry(at))
	_ = second.Close()

	if got := len(readLines(t, filepath.Join(opts.Dir, "queries-20261018.jsonl"))); got != 2 {
		t.Errorf("file has %d lines after a restart, want 2", got)
	}
}

func TestWriter_StringsAreSanitizedAndBounded(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	w := openWriter(t, testOptions(t, newFakeClock(at)))
	e := blockEntry(at)
	e.QName = "evil\n{\"forged\":1}\x1b[31m.example\u202e.com\xff"
	e.Source = strings.Repeat("s", 5000)
	e.Error = "line1\r\nline2\x00"
	w.Record(e)
	_ = w.Close()

	lines := readLines(t, filepath.Join(w.opts.Dir, "queries-20261018.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("a hostile name produced %d lines, want 1", len(lines))
	}
	var rec Record
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("line is not valid JSON: %v\n%s", err, lines[0])
	}
	for _, field := range []string{rec.QName, rec.Error} {
		for _, r := range field {
			if r < 0x20 || r == 0x7f || r == 0x202e || r == 0xfffd {
				t.Errorf("control or format character %U survived in %q", r, field)
			}
		}
	}
	if len(rec.Source) > maxFieldLen {
		t.Errorf("source is %d bytes, want at most %d", len(rec.Source), maxFieldLen)
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"plain", "example.com", 255, "example.com"},
		{"newline", "a\nb", 255, "a?b"},
		{"escape", "a\x1b[0mb", 255, "a?[0mb"},
		{"invalid utf8", "a\xffb", 255, "a?b"},
		{"valid non-ascii is kept", "m\u00fcnchen", 255, "m\u00fcnchen"},
		{"bidi override", "a\u202eb", 255, "a?b"},
		{"truncated on a rune boundary", "\u00fc\u00fc\u00fc", 3, "\u00fc"},
		{"empty", "", 255, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitize(tt.in, tt.limit); got != tt.want {
				t.Errorf("sanitize(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
			}
		})
	}
}

func TestWriter_FullBufferDropsAndCounts(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	opts := testOptions(t, newFakeClock(at))
	opts.BufferSize = 2
	w, err := newWriter(opts)
	if err != nil {
		t.Fatal(err)
	}
	// The consumer is not started yet, so nothing drains the queue.
	for i := 0; i < 5; i++ {
		w.Record(blockEntry(at))
	}
	if got := w.Dropped(); got != 3 {
		t.Fatalf("Dropped() = %d, want 3", got)
	}
	w.start()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := len(readLines(t, filepath.Join(opts.Dir, "queries-20261018.jsonl"))); got != 2 {
		t.Errorf("written lines = %d, want the 2 that fitted", got)
	}
	if w.Written() != 2 {
		t.Errorf("Written() = %d, want 2", w.Written())
	}
}

func TestWriter_RecordNeverBlocksWhenTheDiskIsStuck(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	opts := testOptions(t, newFakeClock(at))
	opts.BufferSize = 1
	w, err := newWriter(opts)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			w.Record(blockEntry(at))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Record blocked on a full queue")
	}
}

func TestWriter_RecordAfterCloseIsHarmless(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	w := openWriter(t, testOptions(t, newFakeClock(at)))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			w.Record(blockEntry(at))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Record blocked after Close")
	}
}

func TestWriter_ConcurrentRecordersAccountForEveryEntry(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	opts := testOptions(t, newFakeClock(at))
	opts.BufferSize = 64
	w := openWriter(t, opts)

	const goroutines, perGoroutine = 8, 500
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				w.Record(blockEntry(at))
			}
		}()
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	lines := len(readLines(t, filepath.Join(opts.Dir, "queries-20261018.jsonl")))
	if total := uint64(lines) + w.Dropped(); total != goroutines*perGoroutine {
		t.Errorf("written %d + dropped %d = %d, want %d", lines, w.Dropped(), total, goroutines*perGoroutine)
	}
	if uint64(lines) != w.Written() {
		t.Errorf("Written() = %d but the file has %d lines", w.Written(), lines)
	}
}

func TestOpen_RejectsBadOptions(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	good := testOptions(t, newFakeClock(at))

	noDir := good
	noDir.Dir = ""
	noRetention := good
	noRetention.RetentionDays = 0
	fileAsDir := good
	fileAsDir.Dir = filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(fileAsDir.Dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, opts := range map[string]Options{"empty dir": noDir, "zero retention": noRetention, "dir is a file": fileAsDir} {
		if w, err := Open(opts); err == nil {
			_ = w.Close()
			t.Errorf("%s: Open succeeded", name)
		}
	}
}

func TestOpen_CreatesTheDirectoryAndStaysReadableByLineScanners(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	opts := testOptions(t, newFakeClock(at))
	w := openWriter(t, opts)
	w.Record(blockEntry(at))
	_ = w.Close()

	f, err := os.Open(filepath.Join(opts.Dir, "queries-20261018.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	n := 0
	for scanner.Scan() {
		n++
		if !json.Valid(scanner.Bytes()) {
			t.Errorf("line %d is not valid JSON", n)
		}
	}
	if n != 1 {
		t.Errorf("scanned %d lines, want 1", n)
	}
}

func TestWriter_RecordsBecomeReadableWithoutClose(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	opts := testOptions(t, newFakeClock(at))
	w := openWriter(t, opts)
	w.Record(blockEntry(at))

	// `xdns log` reads the file while the server is still running, so an idle
	// writer must flush by itself.
	path := filepath.Join(opts.Dir, "queries-20261018.jsonl")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), "ads.contoh.com") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the record never reached the file while the writer was idle")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func FuzzSanitize(f *testing.F) {
	for _, seed := range []string{"", "example.com", "a\nb", "\xff\xfe", "münchen", "‮", "\x00\x1b[0m", strings.Repeat("x", 600)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := sanitize(in, maxFieldLen)
		if len(out) > maxFieldLen {
			t.Fatalf("output is %d bytes", len(out))
		}
		if !utf8.ValidString(out) {
			t.Fatalf("output %q is not valid UTF-8", out)
		}
		for _, r := range out {
			if !unicode.IsPrint(r) {
				t.Fatalf("output %q holds the non-printable rune %U", out, r)
			}
		}
	})
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

func TestWriter_DailyByteCapDropsAndCounts(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	opts := testOptions(t, newFakeClock(at))
	opts.MaxBytesPerDay = 1000
	opts.BufferSize = 64
	var logs strings.Builder
	opts.Log = slog.New(slog.NewTextHandler(&logs, nil))
	w := openWriter(t, opts)

	const sent = 20
	for i := 0; i < sent; i++ {
		w.Record(blockEntry(at))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(opts.Dir, "queries-20261018.jsonl")
	if size := fileSize(t, path); size > opts.MaxBytesPerDay {
		t.Errorf("file is %d bytes, over the %d byte cap", size, opts.MaxBytesPerDay)
	}
	lines := readLines(t, path)
	for i, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Errorf("line %d is cut or damaged: %q", i, line)
		}
	}
	if w.Written() != uint64(len(lines)) || w.Dropped() == 0 || w.Written()+w.Dropped() != sent {
		t.Errorf("written %d, dropped %d, lines %d; want every record accounted for and some dropped", w.Written(), w.Dropped(), len(lines))
	}
	if n := strings.Count(logs.String(), "daily size cap"); n != 1 {
		t.Errorf("the cap was announced %d times, want once per day:\n%s", n, logs.String())
	}
}

func TestWriter_ByteCapStartsAgainOnTheNextDay(t *testing.T) {
	day1 := time.Date(2026, 10, 18, 23, 59, 0, 0, testZone)
	day2 := time.Date(2026, 10, 19, 0, 0, 5, 0, testZone)
	opts := testOptions(t, newFakeClock(day1))
	opts.MaxBytesPerDay = 500
	w := openWriter(t, opts)

	for i := 0; i < 10; i++ {
		w.Record(blockEntry(day1))
	}
	for i := 0; i < 2; i++ {
		w.Record(blockEntry(day2))
	}
	_ = w.Close()

	if got := len(readLines(t, filepath.Join(opts.Dir, "queries-20261019.jsonl"))); got != 2 {
		t.Errorf("the new day has %d lines, want its 2 records: yesterday's cap must not carry over", got)
	}
}

func TestWriter_ByteCapCountsWhatIsAlreadyInTheFile(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	opts := testOptions(t, newFakeClock(at))
	opts.MaxBytesPerDay = 1000
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(opts.Dir, "queries-20261018.jsonl")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 950)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := openWriter(t, opts)

	w.Record(blockEntry(at))
	_ = w.Close()

	if w.Written() != 0 || w.Dropped() != 1 {
		t.Errorf("written %d, dropped %d; a restart must not forget the bytes already written today", w.Written(), w.Dropped())
	}
}

func TestWriter_NoCapMeansUnlimited(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	w := openWriter(t, testOptions(t, newFakeClock(at)))
	for i := 0; i < 50; i++ {
		w.Record(blockEntry(at))
	}
	_ = w.Close()
	if w.Dropped() != 0 || w.Written() != 50 {
		t.Errorf("written %d, dropped %d, want 50 and 0 without a cap", w.Written(), w.Dropped())
	}
}

func TestWriter_RecordsAfterCloseAreCounted(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, testZone)
	w := openWriter(t, testOptions(t, newFakeClock(at)))
	_ = w.Close()

	for i := 0; i < 3; i++ {
		w.Record(blockEntry(at))
	}
	if w.Dropped() != 3 {
		t.Errorf("Dropped() = %d, want 3: a record that arrives after Close is lost and must be counted", w.Dropped())
	}
}

func TestOpen_ArmsTheMidnightTimerBeforeItReturns(t *testing.T) {
	// Regression for the flaky midnight purge: the first delay must be measured
	// from the clock at Open, not from whenever the writer goroutine is
	// scheduled, so the timer has to exist as soon as Open returns.
	at := time.Date(2026, 10, 18, 23, 59, 59, 0, testZone)
	w := openWriter(t, testOptions(t, newFakeClock(at)))
	if w.midnight == nil {
		t.Fatal("Open returned without arming the midnight timer")
	}
}
