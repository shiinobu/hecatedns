package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

// Verdict says whether a query goes to the upstreams or is answered by the
// engine.
type Verdict uint8

// The two verdicts.
const (
	Pass Verdict = iota
	Block
)

// String returns "pass" or "block".
func (v Verdict) String() string {
	switch v {
	case Pass:
		return ActionPass
	case Block:
		return ActionBlock
	}
	return fmt.Sprintf("verdict(%d)", uint8(v))
}

// Query is one client request as the backend received it. The engine only
// reads Req, it never modifies it. Client may be an IPv4-mapped IPv6 address.
type Query struct {
	Req    *dns.Msg
	Client netip.Addr
	Proto  string // "udp", "tcp", "tls" or "https"
}

// Decision is the result of Before or After.
type Decision struct {
	Verdict Verdict
	// Resp is the answer to send when Verdict is Block, nil otherwise. It is a
	// new message owned by the caller.
	Resp *dns.Msg
	// Reason is one of the Reason* constants, empty for a plain pass.
	Reason string
	// Source is the matching rule line ("block ads.example") or list name.
	Source string
	// Via is the matching CNAME target when Reason is ReasonCNAME.
	Via string
}

// Meta describes how the backend resolved a forwarded query.
type Meta struct {
	Upstream string
	Latency  time.Duration
	Cached   bool
}

// RuleSource provides the current custom rules. *RulesFile satisfies it.
// Snapshot must be safe for concurrent use; it may return nil (no rules).
type RuleSource interface {
	Snapshot() *RuleSet
}

// Options configures New.
type Options struct {
	// ACL is required: without it nobody may use the server (fail closed).
	ACL *ACL
	// Rules supplies the custom rules; nil means none.
	Rules RuleSource
	// Responder builds the answers for blocked names.
	Responder Responder
	// Recorder receives the query-log records; nil discards them.
	Recorder Recorder
	// Log receives operational messages; nil discards them.
	Log *slog.Logger
	// Now is the clock for the record times; nil means time.Now.
	Now func() time.Time
}

// Engine decides for every query whether it is answered locally or forwarded.
// It does no network I/O and is safe for concurrent use: its configuration is
// fixed by New, and the lists and rules are immutable snapshots read through
// atomic pointers. Errors and panics inside the engine fail open (the query is
// forwarded), except in the client ACL check, which fails closed.
type Engine struct {
	allows    func(netip.Addr) bool
	rules     RuleSource
	responder Responder
	recorder  Recorder
	log       *slog.Logger
	now       func() time.Time

	lists          atomic.Pointer[ListSet]
	blockListsSeen atomic.Bool
	warnedNoLists  atomic.Bool
}

// New returns an engine without lists; call SwapLists once they are loaded.
func New(opts Options) (*Engine, error) {
	if opts.ACL == nil {
		return nil, errors.New("engine: an ACL is required, nobody could use the server without allowed_clients")
	}
	e := &Engine{
		allows:    opts.ACL.Allows,
		rules:     opts.Rules,
		responder: opts.Responder,
		recorder:  opts.Recorder,
		log:       opts.Log,
		now:       opts.Now,
	}
	if e.recorder == nil {
		e.recorder = discardRecorder{}
	}
	if e.log == nil {
		e.log = slog.New(slog.DiscardHandler)
	}
	if e.now == nil {
		e.now = time.Now
	}
	return e, nil
}

// SwapLists publishes l as the current lists snapshot; nil means no lists. l
// must not be modified afterwards (ListSet offers no way to do so).
func (e *Engine) SwapLists(l *ListSet) {
	e.lists.Store(l)
	if l.HasBlock() {
		e.blockListsSeen.Store(true)
	}
}

// Before decides a query before it is resolved. A Block decision carries the
// answer and has already been recorded; a Pass decision is recorded later by
// After or Failed. A request that is not exactly one question with opcode
// QUERY is answered here with FORMERR or NOTIMP and recorded.
func (e *Engine) Before(ctx context.Context, q Query) Decision {
	if !e.clientAllowed(ctx, q.Client) {
		return e.refuse(ctx, q)
	}
	return e.filter(ctx, q)
}

// After inspects the answer of a forwarded query and records it. It never
// modifies resp. In this phase it always returns Pass.
func (e *Engine) After(ctx context.Context, q Query, resp *dns.Msg, m Meta) Decision {
	e.recordForwarded(ctx, q, resp, m)
	return Decision{Verdict: Pass}
}

// Failed records a forwarded query that no upstream could answer; the backend
// replies SERVFAIL.
func (e *Engine) Failed(ctx context.Context, q Query, err error) {
	defer e.recoverLog(ctx, "failed")
	entry := e.newEntry(q, ActionError)
	entry.RCode = rcodeString(dns.RcodeServerFailure)
	if err != nil {
		entry.Error = err.Error()
	}
	e.recorder.Record(entry)
}

// clientAllowed runs the ACL check; a panic in it denies the client.
func (e *Engine) clientAllowed(ctx context.Context, client netip.Addr) (allowed bool) {
	defer e.failClosed(ctx, &allowed)
	return e.allows(client)
}

// refuse answers a client outside the ACL with REFUSED, whatever it asked.
func (e *Engine) refuse(ctx context.Context, q Query) Decision {
	dec := Decision{Verdict: Block, Resp: Refuse(q.Req), Reason: ReasonACL}
	e.recordBlocked(ctx, q, dec)
	return dec
}

// reject answers a request that is not exactly one question with opcode QUERY.
// dnsproxy only checks the question count before it calls the handler and would
// forward a STATUS, NOTIFY or UPDATE message upstream, past every rule, so the
// engine answers FORMERR or NOTIMP itself and records it.
func (e *Engine) reject(ctx context.Context, q Query) Decision {
	resp := formErr(q.Req)
	if q.Req != nil && len(q.Req.Question) == 1 {
		resp = notImplemented(q.Req)
	}
	dec := Decision{Verdict: Block, Resp: resp, Reason: ReasonInvalid}
	e.recordBlocked(ctx, q, dec)
	return dec
}

// filter applies the custom rules and the lists to an allowed client's query.
func (e *Engine) filter(ctx context.Context, q Query) (dec Decision) {
	defer e.failOpen(ctx, &dec)
	name, ok := questionName(q.Req)
	if !ok {
		return e.reject(ctx, q)
	}
	e.warnIfNoBlockLists(ctx)
	dec = classify(e.ruleSnapshot(), e.lists.Load(), name)
	if dec.Verdict != Block {
		return dec
	}
	dec.Resp = e.responder.Block(q.Req)
	e.recordBlocked(ctx, q, dec)
	return dec
}

// classify applies rules and lists to a normalized name with the priority
// custom allow > custom block > list allow > list block, so a rule written by
// the owner always beats a downloaded list. The Decision has no Resp.
func classify(rules *RuleSet, lists *ListSet, name string) Decision {
	if domain, ok := rules.Allowed(name); ok {
		return Decision{Verdict: Pass, Reason: ReasonAllow, Source: Rule{Action: RuleAllow, Domain: domain}.String()}
	}
	if domain, ok := rules.Blocked(name); ok {
		return Decision{Verdict: Block, Reason: ReasonRule, Source: Rule{Action: RuleBlock, Domain: domain}.String()}
	}
	if list, ok := lists.LookupAllow(name); ok {
		return Decision{Verdict: Pass, Reason: ReasonAllow, Source: list}
	}
	if list, ok := lists.LookupBlock(name); ok {
		return Decision{Verdict: Block, Reason: ReasonList, Source: list}
	}
	return Decision{Verdict: Pass}
}

// questionName returns the normalized name of a well-formed query: exactly one
// question and opcode QUERY.
func questionName(req *dns.Msg) (string, bool) {
	if req == nil || len(req.Question) != 1 || req.Opcode != dns.OpcodeQuery {
		return "", false
	}
	return NormalizeName(req.Question[0].Name), true
}

// ruleSnapshot returns the current custom rules, nil when there are none.
func (e *Engine) ruleSnapshot() *RuleSet {
	if e.rules == nil {
		return nil
	}
	return e.rules.Snapshot()
}

// warnIfNoBlockLists logs, once per engine, that queries are filtered by the
// custom rules only because no block list has been loaded so far.
func (e *Engine) warnIfNoBlockLists(ctx context.Context) {
	if e.blockListsSeen.Load() || e.warnedNoLists.Load() || !e.warnedNoLists.CompareAndSwap(false, true) {
		return
	}
	e.log.WarnContext(ctx, "no block list is loaded yet, only the custom rules are applied")
}
