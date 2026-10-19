package controller

import (
	"fmt"
	"io"
	"strings"

	"github.com/shiinobu/hecatedns/backend/engine"
	"github.com/shiinobu/hecatedns/backend/lib/config"
)

const ruleUsage = `usage:
  hecate rule add block|allow <domain> [-config file]   add a rule (replaces the domain's old rule)
  hecate rule remove <domain> [-config file]            remove the domain's rule
  hecate rule list [-config file]                       show the rules
`

func extractConfigFlag(args []string) (path string, rest []string, err error) {
	path = defaultConfigPath
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		if !strings.HasPrefix(arg, "-") || name != "config" {
			rest = append(rest, arg)
			continue
		}
		if !hasValue {
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("option %s needs a file name", arg)
			}
			i++
			value = args[i]
		}
		path = value
	}
	return path, rest, nil
}

func runRule(args []string, stdout, stderr io.Writer) int {
	configPath, rest, err := extractConfigFlag(args)
	if err != nil {
		fmt.Fprintf(stderr, "hecate rule: %v\n", err)
		return exitUsage
	}
	if !validRuleArgs(rest) {
		fmt.Fprint(stderr, ruleUsage)
		return exitUsage
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "hecate rule: %v\n", err)
		return exitError
	}
	var cmdErr error
	switch rest[0] {
	case "add":
		cmdErr = ruleAdd(cfg, rest[1], rest[2], stdout)
	case "remove":
		cmdErr = ruleRemove(cfg, rest[1], stdout)
	default:
		cmdErr = ruleList(cfg, stdout, stderr)
	}
	if cmdErr != nil {
		fmt.Fprintf(stderr, "hecate rule %s: %v\n", rest[0], cmdErr)
		return exitError
	}
	return exitOK
}

func validRuleArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "add":
		return len(args) == 3
	case "remove":
		return len(args) == 2
	case "list":
		return len(args) == 1
	}
	return false
}

func ruleAdd(cfg config.Config, action, domain string, stdout io.Writer) error {
	rule, err := parseRuleArgs(action, domain)
	if err != nil {
		return err
	}
	text, err := readRulesText(cfg.Rules.File)
	if err != nil {
		return err
	}
	replaced := text.remove(rule.Domain)
	text.add(rule)
	data := text.bytes()
	if len(data) > engine.MaxRulesFileBytes {
		return fmt.Errorf("%s would become too large (over %d bytes); the server ignores the rest, so the new rule would not take effect. Remove old rules first", cfg.Rules.File, engine.MaxRulesFileBytes)
	}
	if err := writeFileAtomic(cfg.Rules.File, data); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "rule added: %s\n", rule)
	if replaced > 0 {
		fmt.Fprintf(stdout, "replaced %d old line(s) for %s\n", replaced, rule.Domain)
	}
	fmt.Fprintf(stdout, "the server re-reads %s by itself (every %s), no restart needed\n", cfg.Rules.File, cfg.Rules.PollInterval)
	return nil
}

func parseRuleArgs(action, domain string) (engine.Rule, error) {
	kind := engine.RuleAction(strings.ToLower(action))
	if kind != engine.RuleBlock && kind != engine.RuleAllow {
		return engine.Rule{}, fmt.Errorf("action %q must be block or allow", action)
	}
	entry, err := engine.ParseEntry(domain)
	if err != nil {
		return engine.Rule{}, fmt.Errorf("domain %q rejected: %w%s", truncate(domain), err, punycodeHint(domain))
	}
	return engine.Rule{Action: kind, Domain: entry}, nil
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
	const limit = 80
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}

func ruleRemove(cfg config.Config, domain string, stdout io.Writer) error {
	entry, err := engine.ParseEntry(domain)
	if err != nil {
		return fmt.Errorf("domain %q rejected: %w%s", truncate(domain), err, punycodeHint(domain))
	}
	text, err := readRulesText(cfg.Rules.File)
	if err != nil {
		return err
	}
	removed := text.remove(entry)
	if removed == 0 {
		return fmt.Errorf("no rule for %s in %s", entry, cfg.Rules.File)
	}
	if err := writeFileAtomic(cfg.Rules.File, text.bytes()); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "rule removed: %d lines for %s\n", removed, entry)
	return nil
}

func ruleList(cfg config.Config, stdout, stderr io.Writer) error {
	text, err := readRulesText(cfg.Rules.File)
	if err != nil {
		return err
	}
	listed := 0
	for i, line := range text.lines {
		if i == 0 {
			line = strings.TrimPrefix(line, byteOrderMark)
		}
		rule, ok, parseErr := parseLine(line)
		switch {
		case parseErr != nil:
			fmt.Fprintf(stderr, "line %d skipped by the server: %v\n", i+1, parseErr)
		case ok:
			fmt.Fprintln(stdout, rule)
			listed++
		}
	}
	if listed == 0 {
		fmt.Fprintf(stderr, "no rules yet in %s\n", cfg.Rules.File)
	}
	return nil
}
