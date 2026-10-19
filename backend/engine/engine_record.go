package engine

import (
	"context"
	"fmt"
	"runtime/debug"
	"strconv"

	"github.com/miekg/dns"
)

type discardRecorder struct{}

func (discardRecorder) Record(Entry) {}

func (e *Engine) recordBlocked(ctx context.Context, q Query, dec Decision) {
	defer e.recoverLog(ctx, "record block")
	entry := e.newEntry(q, ActionBlock)
	entry.Reason, entry.Source, entry.Via = dec.Reason, dec.Source, dec.Via
	entry.RCode = responseRcode(dec.Resp)
	e.recorder.Record(entry)
}

func (e *Engine) recordForwarded(ctx context.Context, q Query, resp *dns.Msg, m Meta) {
	defer e.recoverLog(ctx, "after")
	entry := e.newEntry(q, ActionPass)
	entry.Upstream, entry.Latency, entry.Cached = m.Upstream, m.Latency, m.Cached
	entry.RCode = responseRcode(resp)
	entry.Reason, entry.Source = e.allowReason(ctx, q)
	e.recorder.Record(entry)
}

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

func (e *Engine) newEntry(q Query, action string) Entry {
	entry := Entry{Time: e.now(), Client: q.Client.Unmap(), Proto: q.Proto, Action: action}
	if q.Req != nil && len(q.Req.Question) > 0 {
		question := q.Req.Question[0]
		entry.QName = NormalizeName(question.Name)
		entry.QType = dns.Type(question.Qtype).String()
	}
	return entry
}

func responseRcode(resp *dns.Msg) string {
	if resp == nil {
		return ""
	}
	return rcodeString(resp.Rcode)
}

func rcodeString(rcode int) string {
	if name, ok := dns.RcodeToString[rcode]; ok {
		return name
	}
	return "RCODE" + strconv.Itoa(rcode)
}

func (e *Engine) failOpen(ctx context.Context, stage string, dec *Decision) {
	if r := recover(); r != nil {
		e.logPanic(ctx, stage, r)
		*dec = Decision{Verdict: Pass}
	}
}

func (e *Engine) failClosed(ctx context.Context, allowed *bool) {
	if r := recover(); r != nil {
		e.logPanic(ctx, "acl", r)
		*allowed = false
	}
}

func (e *Engine) recoverLog(ctx context.Context, stage string) {
	if r := recover(); r != nil {
		e.logPanic(ctx, stage, r)
	}
}

func (e *Engine) logPanic(ctx context.Context, stage string, r any) {
	e.log.ErrorContext(ctx, "engine recovered from a panic",
		"stage", stage, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
}
