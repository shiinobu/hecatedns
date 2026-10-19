package engine

import (
	"context"
	"fmt"
	"runtime/debug"
	"strconv"

	"github.com/miekg/dns"
)

// discardRecorder drops every record; it stands in for a nil Recorder.
type discardRecorder struct{}

func (discardRecorder) Record(Entry) {}

// recordBlocked writes the record of a query answered by the engine. A panic
// while building or writing the record is logged and loses only the record:
// the decision already made must stand, a refusal in particular.
func (e *Engine) recordBlocked(ctx context.Context, q Query, dec Decision) {
	defer e.recoverLog(ctx, "record block")
	entry := e.newEntry(q, ActionBlock)
	entry.Reason, entry.Source, entry.Via = dec.Reason, dec.Source, dec.Via
	entry.RCode = responseRcode(dec.Resp)
	e.recorder.Record(entry)
}

// recordForwarded writes the record of a resolved query, with the allow rule
// or list that let it through, if any.
func (e *Engine) recordForwarded(ctx context.Context, q Query, resp *dns.Msg, m Meta) {
	defer e.recoverLog(ctx, "after")
	entry := e.newEntry(q, ActionPass)
	entry.Upstream, entry.Latency, entry.Cached = m.Upstream, m.Latency, m.Cached
	entry.RCode = responseRcode(resp)
	entry.Reason, entry.Source = e.allowReason(ctx, q)
	e.recorder.Record(entry)
}

// allowReason repeats the policy lookup for the record of a forwarded query;
// it is cheap and keeps Before free of per-query state. A panic in it costs
// only the reason, never the record.
func (e *Engine) allowReason(ctx context.Context, q Query) (reason, source string) {
	defer e.recoverLog(ctx, "after allow lookup")
	name, ok := questionName(q.Req)
	if !ok {
		return "", ""
	}
	if dec := classify(e.ruleSnapshot(), e.lists.Load(), name); dec.Reason == ReasonAllow {
		return dec.Reason, dec.Source
	}
	return "", ""
}

// newEntry starts a record with the fields every action has.
func (e *Engine) newEntry(q Query, action string) Entry {
	entry := Entry{Time: e.now(), Client: q.Client.Unmap(), Proto: q.Proto, Action: action}
	if q.Req != nil && len(q.Req.Question) > 0 {
		question := q.Req.Question[0]
		entry.QName = NormalizeName(question.Name)
		entry.QType = dns.Type(question.Qtype).String()
	}
	return entry
}

// responseRcode returns the rcode name of resp, empty for no response.
func responseRcode(resp *dns.Msg) string {
	if resp == nil {
		return ""
	}
	return rcodeString(resp.Rcode)
}

// rcodeString returns the mnemonic of rcode, or RCODEn for an unknown one.
func rcodeString(rcode int) string {
	if name, ok := dns.RcodeToString[rcode]; ok {
		return name
	}
	return "RCODE" + strconv.Itoa(rcode)
}

// agy wrote this
// failOpen must be deferred directly: it turns a panic into a Pass decision.
func (e *Engine) failOpen(ctx context.Context, stage string, dec *Decision) {
	if r := recover(); r != nil {
		e.logPanic(ctx, stage, r)
		*dec = Decision{Verdict: Pass}
	}
}

// failClosed must be deferred directly: it turns a panic into a denial.
func (e *Engine) failClosed(ctx context.Context, allowed *bool) {
	if r := recover(); r != nil {
		e.logPanic(ctx, "acl", r)
		*allowed = false
	}
}

// recoverLog must be deferred directly: it logs a panic and swallows it.
func (e *Engine) recoverLog(ctx context.Context, stage string) {
	if r := recover(); r != nil {
		e.logPanic(ctx, stage, r)
	}
}

// logPanic logs a recovered panic with its stack: a panic is a bug, and the
// stack is what makes it fixable.
func (e *Engine) logPanic(ctx context.Context, stage string, r any) {
	e.log.ErrorContext(ctx, "engine recovered from a panic",
		"stage", stage, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
}
