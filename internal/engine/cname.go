package engine

// agy wrote this

import (
	"context"

	"github.com/miekg/dns"
)

// inspectAnswer checks whether a resolved upstream answer contains a CNAME
// chain pointing to a blocked domain. It never mutates q.Req or resp, and
// fails open (returns Pass) if an unexpected panic occurs.
func (e *Engine) inspectAnswer(ctx context.Context, q Query, resp *dns.Msg) (dec Decision) {
	defer e.failOpen(ctx, "after", &dec)
	if _, ok := questionName(q.Req); !ok {
		return Decision{Verdict: Pass}
	}
	dec, blocked := checkCNAME(e.ruleSnapshot(), e.lists.Load(), resp)
	if !blocked {
		return Decision{Verdict: Pass}
	}
	dec.Resp = e.responder.Block(q.Req)
	return dec
}

// checkCNAME walks every CNAME record in resp.Answer in order. Targets matched
// by an allow rule or allow list are skipped; the first target matched by a
// block rule or block list produces a Block decision with ReasonCNAME and Via
// set to the normalized target.
func checkCNAME(rules *RuleSet, lists *ListSet, resp *dns.Msg) (Decision, bool) {
	if resp == nil || resp.Rcode != dns.RcodeSuccess || len(resp.Answer) == 0 {
		return Decision{Verdict: Pass}, false
	}
	for _, rr := range resp.Answer {
		cname, ok := rr.(*dns.CNAME)
		if !ok || cname == nil {
			continue
		}
		target := NormalizeName(cname.Target)
		if target == "" {
			continue
		}
		if dec := classify(rules, lists, target); dec.Verdict == Block {
			return Decision{
				Verdict: Block,
				Reason:  ReasonCNAME,
				Source:  dec.Source,
				Via:     target,
			}, true
		}
	}
	return Decision{Verdict: Pass}, false
}
