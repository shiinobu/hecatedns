package controller

import (
	"fmt"
	"io"

	"github.com/shiinobu/hecatedns/backend/lib/config"
	"github.com/shiinobu/hecatedns/backend/service/ruleedit"
)

func ruleAdd(cfg config.Config, action, domain string, stdout io.Writer) error {
	rule, err := ruleedit.ParseRule(action, domain)
	if err != nil {
		return err
	}
	res, err := ruleedit.Add(cfg.Rules.File, rule)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "rule added: %s\n", rule)
	if res.ReplacedLines > 0 {
		fmt.Fprintf(stdout, "replaced %d old line(s) for %s\n", res.ReplacedLines, rule.Domain)
	}
	fmt.Fprintf(stdout, "the server re-reads %s by itself (every %s), no restart needed\n", cfg.Rules.File, cfg.Rules.PollInterval)
	return nil
}

func ruleRemove(cfg config.Config, domain string, stdout io.Writer) error {
	entry, err := ruleedit.ParseDomain(domain)
	if err != nil {
		return err
	}
	removed, err := ruleedit.Remove(cfg.Rules.File, entry)
	if err != nil {
		return err
	}
	if len(removed) == 0 {
		return fmt.Errorf("no rule for %s in %s", entry, cfg.Rules.File)
	}
	fmt.Fprintf(stdout, "rule removed: %d lines for %s\n", len(removed), entry)
	return nil
}

func ruleList(cfg config.Config, stdout, stderr io.Writer) error {
	listing, err := ruleedit.List(cfg.Rules.File)
	if err != nil {
		return err
	}
	for _, skipped := range listing.Skipped {
		fmt.Fprintf(stderr, "line %d skipped by the server: %v\n", skipped.Line, skipped.Err)
	}
	for _, rule := range listing.Rules {
		fmt.Fprintln(stdout, rule)
	}
	if len(listing.Rules) == 0 {
		fmt.Fprintf(stderr, "no rules yet in %s\n", cfg.Rules.File)
	}
	return nil
}
