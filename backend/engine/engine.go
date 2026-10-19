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

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

type Verdict uint8

const (
	Pass Verdict = iota
	Block
)

func (v Verdict) String() string {
	switch v {
	case Pass:
		return ActionPass
	case Block:
		return ActionBlock
	}
	return fmt.Sprintf("verdict(%d)", uint8(v))
}

type Query struct {
	Req    *dns.Msg
	Client netip.Addr
	Proto  string
}

type Decision struct {
	Verdict Verdict

	Resp *dns.Msg

	Reason string

	Source string

	Via string
}

type Meta struct {
	Upstream string
	Latency  time.Duration
	Cached   bool
}

type RuleSource interface {
	Snapshot() *RuleSet
}

type Options struct {
	ACL *ACL

	Rules RuleSource

	Responder Responder

	Special config.SpecialNames

	Recorder Recorder

	Log *slog.Logger

	Now func() time.Time
}

type Engine struct {
	allows    func(netip.Addr) bool
	rules     RuleSource
	responder Responder
	special   config.SpecialNames
	recorder  Recorder
	log       *slog.Logger
	now       func() time.Time

	lists          atomic.Pointer[ListSet]
	blockListsSeen atomic.Bool
	warnedNoLists  atomic.Bool
	health         upstreamHealth
}

func New(opts Options) (*Engine, error) {
	if opts.ACL == nil {
		return nil, errors.New("engine: an ACL is required, nobody could use the server without allowed_clients")
	}
	e := &Engine{
		allows:    opts.ACL.Allows,
		rules:     opts.Rules,
		responder: opts.Responder,
		special:   opts.Special,
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

func (e *Engine) SwapLists(l *ListSet) {
	e.lists.Store(l)
	if l.HasBlock() {
		e.blockListsSeen.Store(true)
	}
}

func (e *Engine) Before(ctx context.Context, q Query) Decision {
	if !e.clientAllowed(ctx, q.Client) {
		return e.refuse(ctx, q)
	}
	return e.filter(ctx, q)
}

func (e *Engine) After(ctx context.Context, q Query, resp *dns.Msg, m Meta) Decision {
	if resp != nil && m.Upstream != "" {
		e.health.answered(e.log)
	}

	if dec := e.inspectAnswer(ctx, q, resp); dec.Verdict == Block {
		e.recordBlocked(ctx, q, dec)
		return dec
	}
	e.recordForwarded(ctx, q, resp, m)
	return Decision{Verdict: Pass}
}

func (e *Engine) Failed(ctx context.Context, q Query, err error) {
	defer e.recoverLog(ctx, "failed")
	e.health.failed(err, e.now(), e.log)
	entry := e.newEntry(q, ActionError)
	entry.RCode = rcodeString(dns.RcodeServerFailure)
	if err != nil {
		entry.Error = err.Error()
	}
	e.recorder.Record(entry)
}

func (e *Engine) clientAllowed(ctx context.Context, client netip.Addr) (allowed bool) {
	defer e.failClosed(ctx, &allowed)
	return e.allows(client)
}

func (e *Engine) refuse(ctx context.Context, q Query) Decision {
	dec := Decision{Verdict: Block, Resp: Refuse(q.Req), Reason: ReasonACL}
	e.recordBlocked(ctx, q, dec)
	return dec
}

func (e *Engine) reject(ctx context.Context, q Query) Decision {
	resp := formErr(q.Req)
	if q.Req != nil && len(q.Req.Question) == 1 {
		resp = notImplemented(q.Req)
	}
	dec := Decision{Verdict: Block, Resp: resp, Reason: ReasonInvalid}
	e.recordBlocked(ctx, q, dec)
	return dec
}

func (e *Engine) filter(ctx context.Context, q Query) (dec Decision) {
	defer e.failOpen(ctx, "before", &dec)
	name, ok := questionName(q.Req)
	if !ok {
		return e.reject(ctx, q)
	}

	if dec, ok := e.checkSpecial(q.Req, name); ok {
		e.recordBlocked(ctx, q, dec)
		return dec
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

func questionName(req *dns.Msg) (string, bool) {
	if req == nil || len(req.Question) != 1 || req.Opcode != dns.OpcodeQuery {
		return "", false
	}
	return NormalizeName(req.Question[0].Name), true
}

func (e *Engine) ruleSnapshot() *RuleSet {
	if e.rules == nil {
		return nil
	}
	return e.rules.Snapshot()
}

func (e *Engine) warnIfNoBlockLists(ctx context.Context) {
	if e.blockListsSeen.Load() || e.warnedNoLists.Load() || !e.warnedNoLists.CompareAndSwap(false, true) {
		return
	}
	e.log.WarnContext(ctx, "no block list is loaded yet, only the custom rules are applied")
}
