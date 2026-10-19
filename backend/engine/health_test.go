package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

type logLine struct {
	level slog.Level
	msg   string
	attrs map[string]any
}

type captureLog struct {
	mu    sync.Mutex
	lines []logLine
}

func (c *captureLog) Enabled(context.Context, slog.Level) bool { return true }
func (c *captureLog) WithAttrs([]slog.Attr) slog.Handler       { return c }
func (c *captureLog) WithGroup(string) slog.Handler            { return c }
func (c *captureLog) Handle(_ context.Context, r slog.Record) error {
	line := logLine{level: r.Level, msg: r.Message, attrs: map[string]any{}}
	r.Attrs(func(a slog.Attr) bool {
		line.attrs[a.Key] = a.Value.Any()
		return true
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, line)
	return nil
}

func (c *captureLog) at(level slog.Level) []logLine {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []logLine
	for _, l := range c.lines {
		if l.level == level {
			out = append(out, l)
		}
	}
	return out
}

type movingClock struct{ now atomic.Pointer[time.Time] }

func newMovingClock() *movingClock {
	c := &movingClock{}
	t := fixedNow
	c.now.Store(&t)
	return c
}

func (c *movingClock) Now() time.Time { return *c.now.Load() }
func (c *movingClock) Advance(d time.Duration) {
	t := c.Now().Add(d)
	c.now.Store(&t)
}

func newHealthEngine(t *testing.T) (*Engine, *captureLog, *movingClock) {
	t.Helper()
	logs, clock := &captureLog{}, newMovingClock()
	e, err := New(Options{
		ACL:       mustACL(t, "127.0.0.0/8"),
		Responder: mustResponder(t, config.ModeNullIP, 10*time.Second),
		Log:       slog.New(logs),
		Now:       clock.Now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e, logs, clock
}

func failQuery(e *Engine, err error) {
	e.Failed(context.Background(), newQuery("nothing.example", dns.TypeA, "127.0.0.1"), err)
}

func answerQuery(e *Engine, meta Meta) {
	q := newQuery("nothing.example", dns.TypeA, "127.0.0.1")
	e.After(context.Background(), q, new(dns.Msg).SetRcode(q.Req, dns.RcodeSuccess), meta)
}

func TestFailed_WarnsAfterFiveFailuresInARow(t *testing.T) {
	e, logs, _ := newHealthEngine(t)
	for i := 0; i < 4; i++ {
		failQuery(e, errors.New("all upstreams failed"))
	}
	if got := len(logs.at(slog.LevelWarn)); got != 0 {
		t.Fatalf("%d warnings after 4 failures, want none: one bad moment is not an outage", got)
	}

	failQuery(e, errors.New("all upstreams failed: i/o timeout"))

	warns := logs.at(slog.LevelWarn)
	if len(warns) != 1 {
		t.Fatalf("%d warnings after 5 failures, want 1", len(warns))
	}
	if got := warns[0].attrs["consecutive_failures"]; got != int64(5) {
		t.Errorf("consecutive_failures = %v, want 5", got)
	}
	if got := warns[0].attrs["last_error"]; got != "all upstreams failed: i/o timeout" {
		t.Errorf("last_error = %v, want the error text of the failing query", got)
	}
}

func TestFailed_OverloadIsNotAnUpstreamFailure(t *testing.T) {
	e, logs, _ := newHealthEngine(t)
	for i := 0; i < 20; i++ {
		failQuery(e, fmt.Errorf("too many upstream queries in flight: %w", ErrOverloaded))
	}
	for i := 0; i < 4; i++ {
		failQuery(e, errors.New("down"))
	}

	if got := len(logs.at(slog.LevelWarn)); got != 0 {
		t.Fatalf("%d warnings, want none: a busy server is not a failing upstream, and 4 real failures are below the threshold", got)
	}
}

func TestFailed_RepeatsTheWarningOnlyOncePerMinute(t *testing.T) {
	e, logs, clock := newHealthEngine(t)
	for i := 0; i < 500; i++ {
		failQuery(e, errors.New("down"))
	}
	if got := len(logs.at(slog.LevelWarn)); got != 1 {
		t.Fatalf("%d warnings for 500 failures within a second, want 1: a dead upstream must not flood the log", got)
	}

	clock.Advance(59 * time.Second)
	failQuery(e, errors.New("down"))
	if got := len(logs.at(slog.LevelWarn)); got != 1 {
		t.Fatalf("%d warnings after 59 s, want still 1", got)
	}

	clock.Advance(2 * time.Second)
	failQuery(e, errors.New("down"))
	warns := logs.at(slog.LevelWarn)
	if len(warns) != 2 {
		t.Fatalf("%d warnings after a minute, want 2", len(warns))
	}
	if got := warns[1].attrs["consecutive_failures"]; got != int64(502) {
		t.Errorf("second warning counts %v failures, want 502", got)
	}
}

func TestAfter_AnAnswerEndsTheRunOfFailures(t *testing.T) {
	e, logs, _ := newHealthEngine(t)
	for i := 0; i < 4; i++ {
		failQuery(e, errors.New("down"))
	}
	answerQuery(e, Meta{Upstream: "https://1.1.1.1/dns-query"})
	for i := 0; i < 4; i++ {
		failQuery(e, errors.New("down"))
	}
	if got := len(logs.at(slog.LevelWarn)); got != 0 {
		t.Fatalf("%d warnings, want none: the failures were never 5 in a row", got)
	}
}

func TestAfter_SaysWhenTheUpstreamsAreBackAfterAWarning(t *testing.T) {
	e, logs, clock := newHealthEngine(t)
	for i := 0; i < 7; i++ {
		failQuery(e, errors.New("down"))
	}
	answerQuery(e, Meta{Upstream: "https://1.1.1.1/dns-query"})

	infos := logs.at(slog.LevelInfo)
	if len(infos) != 1 || infos[0].attrs["failures_in_a_row"] != int64(7) {
		t.Fatalf("info lines = %+v, want one recovery line counting 7 failures", infos)
	}

	answerQuery(e, Meta{Upstream: "https://1.1.1.1/dns-query"})
	if got := len(logs.at(slog.LevelInfo)); got != 1 {
		t.Errorf("%d recovery lines after two answers, want 1", got)
	}

	clock.Advance(time.Second)
	for i := 0; i < 5; i++ {
		failQuery(e, errors.New("down again"))
	}
	if got := len(logs.at(slog.LevelWarn)); got != 2 {
		t.Errorf("%d warnings after a second outage a second later, want 2: a new outage is announced at once", got)
	}
}

func TestAfter_QuietRecoveryWhenNothingWasWarned(t *testing.T) {
	e, logs, _ := newHealthEngine(t)
	failQuery(e, errors.New("down"))
	answerQuery(e, Meta{Upstream: "u"})
	if got := len(logs.at(slog.LevelInfo)); got != 0 {
		t.Errorf("%d info lines for a blip below the threshold, want none", got)
	}
}

func TestAfter_ACachedAnswerSaysNothingAboutTheUpstreams(t *testing.T) {
	e, logs, _ := newHealthEngine(t)
	for i := 0; i < 5; i++ {
		failQuery(e, errors.New("down"))
	}
	answerQuery(e, Meta{Cached: true})
	answerQuery(e, Meta{Cached: true})
	failQuery(e, errors.New("down"))

	if got := len(logs.at(slog.LevelInfo)); got != 0 {
		t.Errorf("%d recovery lines caused by cache hits, want none", got)
	}
}

func TestFailed_ACancelledQueryIsNotAnUpstreamFailure(t *testing.T) {
	e, logs, _ := newHealthEngine(t)
	for i := 0; i < 20; i++ {
		failQuery(e, context.Canceled)
	}
	if got := len(logs.at(slog.LevelWarn)); got != 0 {
		t.Errorf("%d warnings for client cancellations, want none: the upstreams did nothing wrong", got)
	}
}

func TestFailed_NilErrorStillCounts(t *testing.T) {
	e, logs, _ := newHealthEngine(t)
	for i := 0; i < 5; i++ {
		failQuery(e, nil)
	}
	warns := logs.at(slog.LevelWarn)
	if len(warns) != 1 {
		t.Fatalf("%d warnings, want 1", len(warns))
	}
	if _, ok := warns[0].attrs["last_error"]; ok {
		t.Errorf("last_error present for a nil error: %v", warns[0].attrs)
	}
}

func TestUpstreamHealth_ConcurrentUse(t *testing.T) {
	e, _, _ := newHealthEngine(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 200; j++ {
				if j%3 == 0 {
					answerQuery(e, Meta{Upstream: "u"})
				} else {
					failQuery(e, errors.New("down"))
				}
			}
		})
	}
	wg.Wait()
}

func TestAfter_AnAnswerMadeLocallySaysNothingAboutTheUpstreams(t *testing.T) {
	e, logs, _ := newHealthEngine(t)
	for i := 0; i < 5; i++ {
		failQuery(e, errors.New("down"))
	}
	answerQuery(e, Meta{})
	answerQuery(e, Meta{})

	if got := len(logs.at(slog.LevelInfo)); got != 0 {
		t.Errorf("%d recovery lines caused by answers no upstream gave (a local reverse answer), want none", got)
	}
}
