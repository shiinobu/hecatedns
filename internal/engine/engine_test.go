package engine

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"xdns/internal/config"
)

var fixedNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// memRecorder keeps every record; safe for concurrent use.
type memRecorder struct {
	mu      sync.Mutex
	entries []Entry
}

func (r *memRecorder) Record(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
}

func (r *memRecorder) all() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Entry(nil), r.entries...)
}

// staticRules is a RuleSource with a fixed snapshot.
type staticRules struct{ set *RuleSet }

func (s staticRules) Snapshot() *RuleSet { return s.set }

// levelCounter is a slog.Handler that only counts records per level.
type levelCounter struct {
	warns  atomic.Int64
	errors atomic.Int64
}

func (c *levelCounter) Enabled(context.Context, slog.Level) bool { return true }
func (c *levelCounter) WithAttrs([]slog.Attr) slog.Handler       { return c }
func (c *levelCounter) WithGroup(string) slog.Handler            { return c }
func (c *levelCounter) Handle(_ context.Context, r slog.Record) error {
	switch {
	case r.Level >= slog.LevelError:
		c.errors.Add(1)
	case r.Level >= slog.LevelWarn:
		c.warns.Add(1)
	}
	return nil
}

const fixtureRules = `allow good.example
block bad.example
allow ok.bad.example
block rulelistallow.example
`

func fixtureLists(t *testing.T) *ListSet {
	t.Helper()
	return NewListSet(
		[]NamedSet{{Name: "allowlist", Set: mustSet(t, "listallow.example", "rulelistallow.example")}},
		[]NamedSet{
			{Name: "ads", Set: mustSet(t, "ads.example", "listallow.example")},
			{Name: "trackers", Set: mustSet(t, "trk.example", "ads.example")},
		},
	)
}

// newFixtureEngine returns an engine with the fixture rules and lists, a fixed
// clock and the given recorder and log handler (both may be nil).
func newFixtureEngine(t *testing.T, rec Recorder, logs slog.Handler) *Engine {
	t.Helper()
	rules, _ := parseRulesText(t, fixtureRules)
	opts := Options{
		ACL:       mustACL(t, "127.0.0.0/8", "::1/128"),
		Rules:     staticRules{set: rules},
		Responder: mustResponder(t, config.ModeNullIP, 10*time.Second),
		Recorder:  rec,
		Now:       func() time.Time { return fixedNow },
	}
	if logs != nil {
		opts.Log = slog.New(logs)
	}
	e, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.SwapLists(fixtureLists(t))
	return e
}

func newQuery(name string, qtype uint16, client string) Query {
	return Query{Req: newRequest(name, qtype), Client: netip.MustParseAddr(client), Proto: "udp"}
}

func TestNewRequiresACL(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("New without ACL returned no error, want an error (fail closed)")
	}
}

func TestBeforeDecisions(t *testing.T) {
	tests := []struct {
		name       string
		qname      string
		client     string
		verdict    Verdict
		reason     string
		source     string
		wantRcode  int
		wantQName  string
		wantClient string
	}{
		{"no match passes", "nothing.example", "127.0.0.1", Pass, "", "", 0, "", ""},
		{"custom allow", "www.good.example", "127.0.0.1", Pass, ReasonAllow, "allow good.example", 0, "", ""},
		{"custom block", "bad.example", "127.0.0.1", Block, ReasonRule, "block bad.example", dns.RcodeSuccess, "bad.example", "127.0.0.1"},
		{"custom allow beats custom block", "x.ok.bad.example", "127.0.0.1", Pass, ReasonAllow, "allow ok.bad.example", 0, "", ""},
		{"block list, first list wins", "sub.ads.example", "127.0.0.1", Block, ReasonList, "ads", dns.RcodeSuccess, "sub.ads.example", "127.0.0.1"},
		{"block list, second list", "trk.example", "::1", Block, ReasonList, "trackers", dns.RcodeSuccess, "trk.example", "::1"},
		{"allow list beats block list", "listallow.example", "127.0.0.1", Pass, ReasonAllow, "allowlist", 0, "", ""},
		{"custom block beats allow list", "rulelistallow.example", "127.0.0.1", Block, ReasonRule, "block rulelistallow.example", dns.RcodeSuccess, "rulelistallow.example", "127.0.0.1"},
		{"name is normalized", "BAD.Example.", "127.0.0.1", Block, ReasonRule, "block bad.example", dns.RcodeSuccess, "bad.example", "127.0.0.1"},
		{"mapped IPv4 client is allowed", "bad.example", "::ffff:127.0.0.1", Block, ReasonRule, "block bad.example", dns.RcodeSuccess, "bad.example", "127.0.0.1"},
		{"client outside ACL is refused before matching", "good.example", "192.0.2.1", Block, ReasonACL, "", dns.RcodeRefused, "good.example", "192.0.2.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &memRecorder{}
			e := newFixtureEngine(t, rec, nil)
			q := newQuery(tt.qname, dns.TypeA, tt.client)

			dec := e.Before(context.Background(), q)

			if dec.Verdict != tt.verdict || dec.Reason != tt.reason || dec.Source != tt.source {
				t.Fatalf("Before = {%v %q %q}, want {%v %q %q}", dec.Verdict, dec.Reason, dec.Source, tt.verdict, tt.reason, tt.source)
			}
			entries := rec.all()
			if tt.verdict == Pass {
				if dec.Resp != nil || len(entries) != 0 {
					t.Fatalf("Pass: Resp = %v, records = %d; want nil Resp and no record", dec.Resp, len(entries))
				}
				return
			}
			checkBlockAnswer(t, q, dec, tt.wantRcode)
			want := Entry{
				Time: fixedNow, Client: netip.MustParseAddr(tt.wantClient), Proto: "udp",
				QName: tt.wantQName, QType: "A", Action: ActionBlock, Reason: tt.reason,
				Source: tt.source, RCode: dns.RcodeToString[tt.wantRcode],
			}
			if len(entries) != 1 || entries[0] != want {
				t.Fatalf("records = %+v, want exactly [%+v]", entries, want)
			}
		})
	}
}

// checkBlockAnswer checks the response of a Block decision.
func checkBlockAnswer(t *testing.T, q Query, dec Decision, wantRcode int) {
	t.Helper()
	if dec.Resp == nil {
		t.Fatal("Block without Resp")
	}
	if dec.Resp.Id != q.Req.Id || dec.Resp.Rcode != wantRcode {
		t.Fatalf("Resp id %d rcode %d, want id %d rcode %d", dec.Resp.Id, dec.Resp.Rcode, q.Req.Id, wantRcode)
	}
	if wantRcode == dns.RcodeRefused {
		return
	}
	if len(dec.Resp.Answer) != 1 || dec.Resp.Answer[0].(*dns.A).A.String() != "0.0.0.0" {
		t.Fatalf("Resp answer = %v, want one A 0.0.0.0", dec.Resp.Answer)
	}
}

func TestBeforeInvalidRequestsAreAnsweredAndRecorded(t *testing.T) {
	twoQuestions := newRequest("bad.example", dns.TypeA)
	twoQuestions.Question = append(twoQuestions.Question, dns.Question{Name: "ads.example.", Qtype: dns.TypeA, Qclass: dns.ClassINET})
	noQuestion := newRequest("bad.example", dns.TypeA)
	noQuestion.Question = nil
	notify := newRequest("good.example", dns.TypeSOA)
	notify.Opcode = dns.OpcodeNotify
	status := newRequest("ads.example", dns.TypeA)
	status.Opcode = dns.OpcodeStatus
	update := newRequest("nothing.example", dns.TypeA)
	update.Opcode = dns.OpcodeUpdate

	tests := []struct {
		name      string
		req       *dns.Msg
		wantRcode int
	}{
		{"two questions", twoQuestions, dns.RcodeFormatError},
		{"no question", noQuestion, dns.RcodeFormatError},
		{"nil request", nil, dns.RcodeFormatError},
		{"opcode NOTIFY on an allowed name", notify, dns.RcodeNotImplemented},
		{"opcode STATUS on a blocked name", status, dns.RcodeNotImplemented},
		{"opcode UPDATE on an unknown name", update, dns.RcodeNotImplemented},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &memRecorder{}
			e := newFixtureEngine(t, rec, nil)
			q := Query{Req: tt.req, Client: netip.MustParseAddr("127.0.0.1"), Proto: "tcp"}

			dec := e.Before(context.Background(), q)

			if dec.Verdict != Block || dec.Reason != ReasonInvalid || dec.Resp == nil || dec.Resp.Rcode != tt.wantRcode {
				t.Fatalf("Before = %+v, want a Block with reason invalid and rcode %s", dec, dns.RcodeToString[tt.wantRcode])
			}
			records := rec.all()
			if len(records) != 1 || records[0].Action != ActionBlock || records[0].Reason != ReasonInvalid || records[0].RCode != dns.RcodeToString[tt.wantRcode] {
				t.Fatalf("records = %+v, want exactly one block record with reason invalid", records)
			}
		})
	}
}

func TestBeforeChecksTheACLBeforeAnsweringMalformedRequests(t *testing.T) {
	notify := newRequest("good.example", dns.TypeSOA)
	notify.Opcode = dns.OpcodeNotify
	e := newFixtureEngine(t, &memRecorder{}, nil)

	dec := e.Before(context.Background(), Query{Req: notify, Client: netip.MustParseAddr("8.8.8.8"), Proto: "udp"})

	if dec.Reason != ReasonACL || dec.Resp == nil || dec.Resp.Rcode != dns.RcodeRefused {
		t.Fatalf("Before = %+v, want the ACL refusal to come first", dec)
	}
}

func TestAfterRecordsForwardedQuery(t *testing.T) {
	tests := []struct {
		name   string
		qname  string
		rcode  int
		nilMsg bool
		meta   Meta
		reason string
		source string
		rcText string
	}{
		{"plain pass", "nothing.example", dns.RcodeSuccess, false, Meta{Upstream: "127.0.0.1:53", Latency: 5 * time.Millisecond}, "", "", "NOERROR"},
		{"cached", "nothing.example", dns.RcodeSuccess, false, Meta{Latency: time.Millisecond, Cached: true}, "", "", "NOERROR"},
		{"allowed by rule", "good.example", dns.RcodeSuccess, false, Meta{Upstream: "u"}, ReasonAllow, "allow good.example", "NOERROR"},
		{"allowed by list", "listallow.example", dns.RcodeSuccess, false, Meta{Upstream: "u"}, ReasonAllow, "allowlist", "NOERROR"},
		{"nxdomain", "nothing.example", dns.RcodeNameError, false, Meta{Upstream: "u"}, "", "", "NXDOMAIN"},
		{"no response", "nothing.example", 0, true, Meta{Upstream: "u"}, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &memRecorder{}
			e := newFixtureEngine(t, rec, nil)
			q := newQuery(tt.qname, dns.TypeAAAA, "::ffff:127.0.0.1")
			var resp *dns.Msg
			if !tt.nilMsg {
				resp = new(dns.Msg).SetRcode(q.Req, tt.rcode)
			}

			dec := e.After(context.Background(), q, resp, tt.meta)

			if dec.Verdict != Pass || dec.Resp != nil {
				t.Fatalf("After = %+v, want Pass without Resp", dec)
			}
			want := Entry{
				Time: fixedNow, Client: netip.MustParseAddr("127.0.0.1"), Proto: "udp",
				QName: tt.qname, QType: "AAAA", Action: ActionPass, Reason: tt.reason, Source: tt.source,
				Upstream: tt.meta.Upstream, RCode: tt.rcText, Latency: tt.meta.Latency, Cached: tt.meta.Cached,
			}
			if got := rec.all(); len(got) != 1 || got[0] != want {
				t.Fatalf("records = %+v, want exactly [%+v]", got, want)
			}
		})
	}
}

func TestFailedRecordsError(t *testing.T) {
	rec := &memRecorder{}
	e := newFixtureEngine(t, rec, nil)
	q := newQuery("nothing.example", dns.TypeA, "127.0.0.1")

	e.Failed(context.Background(), q, errors.New("all upstreams failed"))
	e.Failed(context.Background(), q, nil)

	got := rec.all()
	want := Entry{
		Time: fixedNow, Client: netip.MustParseAddr("127.0.0.1"), Proto: "udp", QName: "nothing.example",
		QType: "A", Action: ActionError, RCode: "SERVFAIL", Error: "all upstreams failed",
	}
	if len(got) != 2 || got[0] != want {
		t.Fatalf("records = %+v, want first %+v", got, want)
	}
	if got[1].Error != "" || got[1].Action != ActionError {
		t.Fatalf("Failed(nil) record = %+v, want action error with empty Error", got[1])
	}
}

func TestNewDefaultsAreUsable(t *testing.T) {
	e, err := New(Options{ACL: mustACL(t, "127.0.0.0/8")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	q := newQuery("bad.example", dns.TypeA, "127.0.0.1")
	ctx := context.Background()

	if dec := e.Before(ctx, q); dec.Verdict != Pass {
		t.Fatalf("Before without rules or lists = %+v, want Pass", dec)
	}
	e.After(ctx, q, new(dns.Msg).SetReply(q.Req), Meta{})
	e.Failed(ctx, q, errors.New("boom"))
	if dec := e.Before(ctx, newQuery("bad.example", dns.TypeA, "10.0.0.1")); dec.Verdict != Block || dec.Reason != ReasonACL {
		t.Fatalf("Before from outside the ACL = %+v, want REFUSED block", dec)
	}
}

func TestBeforeWarnsOnceWhileNoBlockListWasLoaded(t *testing.T) {
	logs := &levelCounter{}
	rules, _ := parseRulesText(t, fixtureRules)
	e, err := New(Options{ACL: mustACL(t, "127.0.0.0/8"), Rules: staticRules{set: rules}, Log: slog.New(logs)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if dec := e.Before(context.Background(), newQuery("bad.example", dns.TypeA, "127.0.0.1")); dec.Verdict != Block {
				t.Errorf("custom rule without lists = %v, want Block", dec.Verdict)
			}
		})
	}
	wg.Wait()
	if got := logs.warns.Load(); got != 1 {
		t.Fatalf("warnings = %d, want exactly 1", got)
	}
}

func TestBeforeDoesNotWarnOnceBlockListLoaded(t *testing.T) {
	logs := &levelCounter{}
	e := newFixtureEngine(t, nil, logs)
	e.SwapLists(nil)

	e.Before(context.Background(), newQuery("nothing.example", dns.TypeA, "127.0.0.1"))

	if got := logs.warns.Load(); got != 0 {
		t.Fatalf("warnings = %d after a block list was loaded once, want 0", got)
	}
}

func TestEngineDoesNotMutateInputs(t *testing.T) {
	e := newFixtureEngine(t, &memRecorder{}, nil)
	ctx := context.Background()
	for _, name := range []string{"bad.example", "nothing.example", "good.example"} {
		for _, client := range []string{"127.0.0.1", "192.0.2.1"} {
			q := newQuery(name, dns.TypeA, client)
			q.Req.SetEdns0(4096, true)
			resp := new(dns.Msg).SetReply(q.Req)
			resp.Answer = []dns.RR{&dns.CNAME{Hdr: dns.RR_Header{Name: q.Req.Question[0].Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 60}, Target: "ads.example."}}
			// Copy turns a nil OPT option slice into an empty one, so compare a
			// copy taken before with a copy taken after, never the original.
			reqBefore, respBefore := q.Req.Copy(), resp.Copy()

			e.Before(ctx, q)
			e.After(ctx, q, resp, Meta{})
			e.Failed(ctx, q, errors.New("x"))

			if !reflect.DeepEqual(q.Req.Copy(), reqBefore) || !reflect.DeepEqual(resp.Copy(), respBefore) ||
				q.Req.String() != reqBefore.String() || resp.String() != respBefore.String() {
				t.Fatalf("%s from %s: request or response was mutated", name, client)
			}
		}
	}
}

func TestVerdictString(t *testing.T) {
	for v, want := range map[Verdict]string{Pass: "pass", Block: "block", Verdict(7): "verdict(7)"} {
		if got := v.String(); got != want {
			t.Errorf("Verdict(%d).String() = %q, want %q", v, got, want)
		}
	}
}

func TestRcodeString(t *testing.T) {
	for rcode, want := range map[int]string{dns.RcodeSuccess: "NOERROR", dns.RcodeRefused: "REFUSED", 3000: "RCODE3000"} {
		if got := rcodeString(rcode); got != want {
			t.Errorf("rcodeString(%d) = %q, want %q", rcode, got, want)
		}
	}
}
