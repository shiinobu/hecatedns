package engine

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

type RuleAction string

const (
	RuleBlock RuleAction = "block"
	RuleAllow RuleAction = "allow"
)

const MaxRuleLineBytes = 4096

const MaxRulesFileBytes = 4 << 20

const (
	maxRuleLineBytes  = MaxRuleLineBytes
	maxRulesFileBytes = MaxRulesFileBytes
	maxRuleIssues     = 100

	commentMark = "#"
	utf8BOM     = "\xef\xbb\xbf"
)

var (
	ErrUnknownAction = errors.New("rule must start with block or allow")
	ErrMissingDomain = errors.New("rule has no domain")
	ErrExtraFields   = errors.New("rule has text after the domain")
	ErrLineTooLong   = errors.New("line is longer than 4096 bytes")
	ErrFileTooLarge  = errors.New("file is larger than 4 MiB, the rest is ignored")
	ErrTooManyIssues = errors.New("more problems found, only the first 100 are listed")
)

type Rule struct {
	Action RuleAction
	Domain string
}

func (r Rule) String() string {
	return string(r.Action) + " " + r.Domain
}

type RuleIssue struct {
	Line int
	Err  error
}

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

type RuleSet struct {
	allow *DomainSet
	block *DomainSet
}

func (s *RuleSet) Allowed(name string) (domain string, ok bool) {
	if s == nil {
		return "", false
	}
	return s.allow.Lookup(name)
}

func (s *RuleSet) Blocked(name string) (domain string, ok bool) {
	if s == nil {
		return "", false
	}
	return s.block.Lookup(name)
}

func (s *RuleSet) Len() int {
	if s == nil {
		return 0
	}
	return s.allow.Len() + s.block.Len()
}

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
		collector.fileIssue(line, ErrFileTooLarge)
	}
	return collector.build(), collector.issues, nil
}

func nextLine(data []byte) (line, rest []byte) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		line, rest = data[:i], data[i+1:]
	} else {
		line = data
	}
	return bytes.TrimSuffix(line, []byte("\r")), rest
}

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

func (c *ruleCollector) fileIssue(line int, err error) {
	c.issues = append(c.issues, RuleIssue{Line: line, Err: err})
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
