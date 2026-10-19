package engine

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

// panicRules is a RuleSource whose Snapshot always panics.
type panicRules struct{}

func (panicRules) Snapshot() *RuleSet { panic("rules exploded") }

// panicRecorder counts the calls and then panics.
type panicRecorder struct{ calls atomic.Int64 }

func (r *panicRecorder) Record(Entry) {
	r.calls.Add(1)
	panic("recorder exploded")
}

// swapRules is a RuleSource that publishes snapshots atomically, like
// RulesFile, but without touching the file system.
type swapRules struct{ current atomic.Pointer[RuleSet] }

func (s *swapRules) Snapshot() *RuleSet { return s.current.Load() }

func TestBeforeFailsOpenWhenRulesPanic(t *testing.T) {
	logs := &levelCounter{}
	rec := &memRecorder{}
	e, err := New(Options{ACL: mustACL(t, "127.0.0.0/8"), Rules: panicRules{}, Recorder: rec, Log: slog.New(logs)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.SwapLists(fixtureLists(t))
	q := newQuery("ads.example", dns.TypeA, "127.0.0.1")
	ctx := context.Background()

	if dec := e.Before(ctx, q); dec.Verdict != Pass || dec.Resp != nil {
		t.Fatalf("Before with panicking rules = %+v, want Pass (fail open)", dec)
	}
	if n := len(rec.all()); n != 0 {
		t.Fatalf("Before recorded %d entries on fail-open, want 0 (After writes the record)", n)
	}
	if dec := e.After(ctx, q, new(dns.Msg).SetReply(q.Req), Meta{Upstream: "u"}); dec.Verdict != Pass {
		t.Fatalf("After with panicking rules = %+v, want Pass", dec)
	}
	got := rec.all()
	if len(got) != 1 || got[0].Action != ActionPass || got[0].RCode != "NOERROR" {
		t.Fatalf("records = %+v, want exactly one pass record from After", got)
	}
	if logs.errors.Load() < 2 {
		t.Fatalf("logged %d errors, want the recovered panics logged", logs.errors.Load())
	}
}

func TestPanickingRecorderNeverChangesTheAnswer(t *testing.T) {
	rec := &panicRecorder{}
	logs := &levelCounter{}
	e := newFixtureEngine(t, rec, logs)
	ctx := context.Background()

	blocked := e.Before(ctx, newQuery("bad.example", dns.TypeA, "127.0.0.1"))
	refused := e.Before(ctx, newQuery("bad.example", dns.TypeA, "192.0.2.1"))
	passed := newQuery("nothing.example", dns.TypeA, "127.0.0.1")
	after := e.After(ctx, passed, new(dns.Msg).SetReply(passed.Req), Meta{})
	e.Failed(ctx, passed, errors.New("down"))

	if blocked.Verdict != Block || blocked.Reason != ReasonRule || blocked.Resp == nil {
		t.Fatalf("blocked query = %+v, want the rule block to stand", blocked)
	}
	if refused.Verdict != Block || refused.Resp == nil || refused.Resp.Rcode != dns.RcodeRefused {
		t.Fatalf("refused query = %+v, want REFUSED to stand", refused)
	}
	if after.Verdict != Pass {
		t.Fatalf("After = %+v, want Pass", after)
	}
	if rec.calls.Load() != 4 || logs.errors.Load() != 4 {
		t.Fatalf("recorder calls %d, errors logged %d; want 4 and 4", rec.calls.Load(), logs.errors.Load())
	}
}

func TestACLPanicFailsClosed(t *testing.T) {
	rec := &memRecorder{}
	e := newFixtureEngine(t, rec, nil)
	e.allows = func(netip.Addr) bool { panic("acl exploded") }

	dec := e.Before(context.Background(), newQuery("good.example", dns.TypeA, "127.0.0.1"))

	if dec.Verdict != Block || dec.Reason != ReasonACL || dec.Resp == nil || dec.Resp.Rcode != dns.RcodeRefused {
		t.Fatalf("Before with panicking ACL = %+v, want REFUSED block (fail closed)", dec)
	}
	if got := rec.all(); len(got) != 1 || got[0].RCode != "REFUSED" {
		t.Fatalf("records = %+v, want one REFUSED record", got)
	}
}

func TestPanickingClockKeepsDecisions(t *testing.T) {
	e := newFixtureEngine(t, &memRecorder{}, nil)
	e.now = func() time.Time { panic("clock exploded") }
	ctx := context.Background()

	if dec := e.Before(ctx, newQuery("bad.example", dns.TypeA, "127.0.0.1")); dec.Verdict != Block {
		t.Fatalf("Before = %+v, want the block to stand when the record cannot be built", dec)
	}
	if dec := e.Before(ctx, newQuery("bad.example", dns.TypeA, "192.0.2.1")); dec.Verdict != Block || dec.Reason != ReasonACL {
		t.Fatalf("Before from outside the ACL = %+v, want REFUSED", dec)
	}
	q := newQuery("nothing.example", dns.TypeA, "127.0.0.1")
	if dec := e.After(ctx, q, nil, Meta{}); dec.Verdict != Pass {
		t.Fatalf("After = %+v, want Pass", dec)
	}
	e.Failed(ctx, q, errors.New("down"))
}

// concurrencyCase is one query of the concurrency test with the decision that
// must hold whatever rules and lists snapshot is current.
type concurrencyCase struct {
	name    string
	verdict Verdict
	reason  string
}

func TestEngineConcurrentQueriesWithReloads(t *testing.T) {
	ruleVersions := []*RuleSet{}
	for _, text := range []string{
		"block stable-rule.example\nallow keep.example\n",
		"block stable-rule.example\nallow keep.example\nblock flip.example\n",
	} {
		set, _ := parseRulesText(t, text)
		ruleVersions = append(ruleVersions, set)
	}
	listVersions := []*ListSet{
		NewListSet(nil, []NamedSet{{Name: "ads", Set: mustSet(t, "stable-list.example")}}),
		NewListSet(
			[]NamedSet{{Name: "allow", Set: mustSet(t, "flip.example")}},
			[]NamedSet{{Name: "ads", Set: mustSet(t, "stable-list.example", "flip.example")}},
		),
	}
	rules := &swapRules{}
	rules.current.Store(ruleVersions[0])
	rec := &memRecorder{}
	e, err := New(Options{
		ACL: mustACL(t, "127.0.0.0/8", "::1/128"), Rules: rules, Recorder: rec,
		Responder: mustResponder(t, config.ModeNullIP, time.Second),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.SwapLists(listVersions[0])

	stop := make(chan struct{})
	var background sync.WaitGroup
	background.Go(func() { churn(stop, func(i int) { rules.current.Store(ruleVersions[i%2]) }) })
	background.Go(func() { churn(stop, func(i int) { e.SwapLists(listVersions[i%2]) }) })

	const workers, perWorker = 32, 300
	cases := []concurrencyCase{
		{"stable-rule.example", Block, ReasonRule},
		{"x.stable-list.example", Block, ReasonList},
		{"keep.example", Pass, ReasonAllow},
		{"nothing.example", Pass, ""},
		{"flip.example", Pass, ""}, // any verdict is fine, it must just be consistent
	}
	var queries atomic.Int64
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() { runWorker(t, e, cases, w, perWorker, &queries) })
	}
	wg.Wait()
	close(stop)
	background.Wait()

	if got, want := int64(len(rec.all())), queries.Load(); got != want || want != workers*perWorker {
		t.Fatalf("records = %d for %d queries (want %d), want exactly one record per query", got, want, workers*perWorker)
	}
}

// churn calls swap with an increasing counter until stop is closed.
func churn(stop <-chan struct{}, swap func(int)) {
	for i := 0; ; i++ {
		select {
		case <-stop:
			return
		default:
			swap(i)
		}
	}
}

// runWorker plays the backend for perWorker queries: Before, then After or
// Failed for the forwarded ones.
func runWorker(t *testing.T, e *Engine, cases []concurrencyCase, w, perWorker int, queries *atomic.Int64) {
	ctx := context.Background()
	clients := []string{"127.0.0.1", "::ffff:127.0.0.2", "::1"}
	for i := range perWorker {
		c := cases[(w+i)%len(cases)]
		q := newQuery(c.name, dns.TypeA, clients[i%len(clients)])
		queries.Add(1)
		dec := e.Before(ctx, q)
		checkConcurrentDecision(t, c, dec)
		if dec.Verdict == Block {
			continue
		}
		if i%7 == 0 {
			e.Failed(ctx, q, errors.New("upstream down"))
			continue
		}
		e.After(ctx, q, new(dns.Msg).SetReply(q.Req), Meta{Upstream: "u", Latency: time.Millisecond})
	}
}

// checkConcurrentDecision checks the parts of dec that no reload can change.
func checkConcurrentDecision(t *testing.T, c concurrencyCase, dec Decision) {
	if dec.Verdict == Block && dec.Resp == nil {
		t.Errorf("%s: Block without Resp", c.name)
	}
	switch c.name {
	case "stable-rule.example", "keep.example", "x.stable-list.example":
		if dec.Verdict != c.verdict || dec.Reason != c.reason {
			t.Errorf("%s = {%v %q}, want {%v %q}", c.name, dec.Verdict, dec.Reason, c.verdict, c.reason)
		}
	case "nothing.example":
		if dec.Verdict != Pass || dec.Reason != "" {
			t.Errorf("%s = {%v %q}, want plain Pass", c.name, dec.Verdict, dec.Reason)
		}
	}
}
