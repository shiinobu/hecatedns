package ruleedit

import (
	"errors"
	"fmt"
	"strings"

	"github.com/shiinobu/hecatedns/backend/engine"
)

const domainEchoLimit = 80

var (
	ErrFileFull  = errors.New("the rules file is full")
	ErrBadAction = errors.New("bad rule action")
)

type Skipped struct {
	Line int
	Err  error
}

type Listing struct {
	Rules   []engine.Rule
	Skipped []Skipped
}

type AddResult struct {
	Replaced      *engine.Rule
	ReplacedLines int
}

func List(path string) (Listing, error) {
	text, err := readRulesText(path)
	if err != nil {
		return Listing{}, err
	}
	var listing Listing
	for i, line := range text.lines {
		rule, ok, parseErr := parseLine(lineForParsing(i, line))
		switch {
		case parseErr != nil:
			listing.Skipped = append(listing.Skipped, Skipped{Line: i + 1, Err: parseErr})
		case ok:
			listing.Rules = append(listing.Rules, rule)
		}
	}
	return listing, nil
}

func Add(path string, rule engine.Rule) (AddResult, error) {
	text, err := readRulesText(path)
	if err != nil {
		return AddResult{}, err
	}
	removed := text.remove(rule.Domain)
	text.add(rule)
	data := text.bytes()
	if len(data) > engine.MaxRulesFileBytes {
		return AddResult{}, fmt.Errorf("%w: %s would become too large (over %d bytes); the server ignores the rest, so the new rule would not take effect. Remove old rules first", ErrFileFull, path, engine.MaxRulesFileBytes)
	}
	if err := writeFileAtomic(path, data); err != nil {
		return AddResult{}, err
	}
	res := AddResult{ReplacedLines: len(removed)}
	if len(removed) > 0 {
		res.Replaced = &removed[0]
	}
	return res, nil
}

func Remove(path, domain string) ([]engine.Rule, error) {
	text, err := readRulesText(path)
	if err != nil {
		return nil, err
	}
	removed := text.remove(domain)
	if len(removed) == 0 {
		return nil, nil
	}
	if err := writeFileAtomic(path, text.bytes()); err != nil {
		return nil, err
	}
	return removed, nil
}

func ParseRule(action, domain string) (engine.Rule, error) {
	kind := engine.RuleAction(strings.ToLower(action))
	if kind != engine.RuleBlock && kind != engine.RuleAllow {
		return engine.Rule{}, fmt.Errorf("%w: action %q must be block or allow", ErrBadAction, truncate(action))
	}
	entry, err := ParseDomain(domain)
	if err != nil {
		return engine.Rule{}, err
	}
	return engine.Rule{Action: kind, Domain: entry}, nil
}

func ParseDomain(domain string) (string, error) {
	entry, err := engine.ParseEntry(domain)
	if err != nil {
		return "", fmt.Errorf("domain %q rejected: %w%s", truncate(domain), err, punycodeHint(domain))
	}
	return entry, nil
}

func punycodeHint(domain string) string {
	for i := 0; i < len(domain); i++ {
		if domain[i] >= 0x80 {
			return " (write names with non-ASCII letters in punycode form, prefix xn--)"
		}
	}
	return ""
}

func truncate(s string) string {
	if len(s) > domainEchoLimit {
		return s[:domainEchoLimit] + "..."
	}
	return s
}
