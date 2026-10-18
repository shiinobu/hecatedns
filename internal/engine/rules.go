package engine

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

// RuleAction is what a custom rule does to a matching name.
type RuleAction string

// The two actions of a rules file line.
const (
	RuleBlock RuleAction = "block"
	RuleAllow RuleAction = "allow"
)

// MaxRulesFileBytes is the largest rules file the server reads; the rule CLI
// refuses to rewrite a bigger one, because the server would ignore its tail.
const MaxRulesFileBytes = 4 << 20

// Limits that keep a hostile or broken rules file from exhausting memory.
const (
	maxRuleLineBytes  = 4096
	maxRulesFileBytes = MaxRulesFileBytes
	maxRuleIssues     = 100

	commentMark = "#"
	utf8BOM     = "\xef\xbb\xbf"
)

// Reasons a rules file line or the whole file is not fully used.
var (
	ErrUnknownAction = errors.New("rule must start with block or allow")
	ErrMissingDomain = errors.New("rule has no domain")
	ErrExtraFields   = errors.New("rule has text after the domain")
	ErrLineTooLong   = errors.New("line is longer than 4096 bytes")
	ErrFileTooLarge  = errors.New("file is larger than 4 MiB, the rest is ignored")
	ErrTooManyIssues = errors.New("more problems found, only the first 100 are listed")
)

// Rule is one parsed line of the rules file.
type Rule struct {
	Action RuleAction
	Domain string
}

// String returns the rules file line for r.
func (r Rule) String() string {
	return string(r.Action) + " " + r.Domain
}

// RuleIssue describes a line (1-based) that was skipped, or a file-level
// problem (Line 0 for ErrTooManyIssues). Err never contains the line text.
type RuleIssue struct {
	Line int
	Err  error
}

// ParseRuleLine parses one line of the rules file: "block <domain>" or
// "allow <domain>", keyword in any case, with an optional "# comment". A blank
// or comment-only line is not an error and not a rule (ok is false). The
// domain follows ParseEntry, so it is validated and normalized.
func ParseRuleLine(line string) (rule Rule, ok bool, err error) {
	if i := strings.Index(line, commentMark); i >= 0 {
		line = line[:i]
	}
	fields := strings.Fields(line)
	switch len(fields) {
	case 0:
		return Rule{}, false, nil
	case 1:
		return Rule{}, false, ErrMissingDomain
	case 2:
	default:
		return Rule{}, false, ErrExtraFields
	}
	action := RuleAction(asciiLower(fields[0]))
	if action != RuleBlock && action != RuleAllow {
		return Rule{}, false, ErrUnknownAction
	}
	domain, err := ParseEntry(fields[1])
	if err != nil {
		return Rule{}, false, err
	}
	return Rule{Action: action, Domain: domain}, true, nil
}

// RuleSet is an immutable snapshot of the custom rules. A nil *RuleSet is a
// valid empty set.
type RuleSet struct {
	allow *DomainSet
	block *DomainSet
}

// Allowed returns the most specific allow rule domain that matches name.
func (s *RuleSet) Allowed(name string) (domain string, ok bool) {
	if s == nil {
		return "", false
	}
	return s.allow.Lookup(name)
}

// Blocked returns the most specific block rule domain that matches name.
func (s *RuleSet) Blocked(name string) (domain string, ok bool) {
	if s == nil {
		return "", false
	}
	return s.block.Lookup(name)
}

// Len returns the number of distinct allow plus block domains.
func (s *RuleSet) Len() int {
	if s == nil {
		return 0
	}
	return s.allow.Len() + s.block.Len()
}

// ParseRules reads a whole rules file. Broken lines are skipped and reported
// with their line numbers; they never stop the parse. At most maxRuleIssues
// issues are returned. Only a failure of r itself is returned as an error.
// Lines of 4096 bytes or more are rejected, and bytes after the first 4 MiB
// are ignored together with the (possibly cut) line they fall in.
func ParseRules(r io.Reader) (*RuleSet, []RuleIssue, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxRulesFileBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read rules: %w", err)
	}
	tooLarge := len(data) > maxRulesFileBytes
	if tooLarge {
		data = data[:bytes.LastIndexByte(data[:maxRulesFileBytes], '\n')+1]
	}
	data = bytes.TrimPrefix(data, []byte(utf8BOM))

	var collector ruleCollector
	line := 1
	for ; len(data) > 0; line++ {
		var text []byte
		text, data = nextLine(data)
		collector.add(line, text)
	}
	if tooLarge {
		collector.issue(line, ErrFileTooLarge)
	}
	return collector.build(), collector.issues, nil
}

// nextLine splits data at the first newline and drops a CR before it.
func nextLine(data []byte) (line, rest []byte) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		line, rest = data[:i], data[i+1:]
	} else {
		line = data
	}
	return bytes.TrimSuffix(line, []byte("\r")), rest
}

// ruleCollector gathers the rules and issues of one parse.
type ruleCollector struct {
	allow, block []string
	issues       []RuleIssue
	overflowed   bool
}

func (c *ruleCollector) add(line int, text []byte) {
	if len(text) >= maxRuleLineBytes {
		c.issue(line, ErrLineTooLong)
		return
	}
	rule, ok, err := ParseRuleLine(string(text))
	switch {
	case err != nil:
		c.issue(line, err)
	case !ok:
	case rule.Action == RuleAllow:
		c.allow = append(c.allow, rule.Domain)
	default:
		c.block = append(c.block, rule.Domain)
	}
}

func (c *ruleCollector) issue(line int, err error) {
	if len(c.issues) < maxRuleIssues {
		c.issues = append(c.issues, RuleIssue{Line: line, Err: err})
		return
	}
	if !c.overflowed {
		c.overflowed = true
		c.issues = append(c.issues, RuleIssue{Err: ErrTooManyIssues})
	}
}

func (c *ruleCollector) build() *RuleSet {
	allow, _ := NewDomainSet(c.allow)
	block, _ := NewDomainSet(c.block)
	return &RuleSet{allow: allow, block: block}
}
