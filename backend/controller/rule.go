package controller

import (
	"fmt"
	"io"
	"strings"

	"github.com/shiinobu/hecatedns/backend/engine"
	"github.com/shiinobu/hecatedns/backend/lib/config"
)

const ruleUsage = `penggunaan:
  hecate rule add block|allow <domain> [-config berkas]   tambah aturan (menggantikan aturan lama domain itu)
  hecate rule remove <domain> [-config berkas]            hapus aturan domain itu
  hecate rule list [-config berkas]                       tampilkan aturan
`

// extractConfigFlag removes "-config <file>" (or "--config", "-config=file")
// from args wherever it stands, so it works before and after the words of the
// subcommand. The default path is used when the flag is absent.
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
				return "", nil, fmt.Errorf("opsi %s memerlukan nama berkas", arg)
			}
			i++
			value = args[i]
		}
		path = value
	}
	return path, rest, nil
}

// runRule dispatches the rule subcommands: add, remove and list.
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

// validRuleArgs reports whether args is "add <action> <domain>", "remove
// <domain>" or "list".
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
		return fmt.Errorf("%s menjadi terlalu besar (lebih dari %d byte); server mengabaikan sisanya, jadi aturan baru tidak akan berlaku. Hapus aturan lama dulu", cfg.Rules.File, engine.MaxRulesFileBytes)
	}
	if err := writeFileAtomic(cfg.Rules.File, data); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "aturan ditambahkan: %s\n", rule)
	if replaced > 0 {
		fmt.Fprintf(stdout, "menggantikan %d baris lama untuk %s\n", replaced, rule.Domain)
	}
	fmt.Fprintf(stdout, "server membaca ulang %s sendiri (setiap %s), tanpa restart\n", cfg.Rules.File, cfg.Rules.PollInterval)
	return nil
}

// parseRuleArgs validates the action word and the domain typed on the command
// line. The domain goes through the same checks as a rules file line.
func parseRuleArgs(action, domain string) (engine.Rule, error) {
	kind := engine.RuleAction(strings.ToLower(action))
	if kind != engine.RuleBlock && kind != engine.RuleAllow {
		return engine.Rule{}, fmt.Errorf("aksi %q harus block atau allow", action)
	}
	entry, err := engine.ParseEntry(domain)
	if err != nil {
		return engine.Rule{}, fmt.Errorf("domain %q ditolak: %w%s", truncate(domain), err, punycodeHint(domain))
	}
	return engine.Rule{Action: kind, Domain: entry}, nil
}

// punycodeHint explains how to write a name with non-ASCII letters, which
// rules and blocklists only accept in their xn-- form.
func punycodeHint(domain string) string {
	for i := 0; i < len(domain); i++ {
		if domain[i] >= 0x80 {
			return " (tulis nama dengan huruf non-ASCII dalam bentuk punycode, awalan xn--)"
		}
	}
	return ""
}

// truncate keeps an echoed command-line value short in an error message.
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
		return fmt.Errorf("domain %q ditolak: %w%s", truncate(domain), err, punycodeHint(domain))
	}
	text, err := readRulesText(cfg.Rules.File)
	if err != nil {
		return err
	}
	removed := text.remove(entry)
	if removed == 0 {
		return fmt.Errorf("tidak ada aturan untuk %s di %s", entry, cfg.Rules.File)
	}
	if err := writeFileAtomic(cfg.Rules.File, text.bytes()); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "aturan dihapus: %d baris untuk %s\n", removed, entry)
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
			fmt.Fprintf(stderr, "baris %d dilewati server: %v\n", i+1, parseErr)
		case ok:
			fmt.Fprintln(stdout, rule)
			listed++
		}
	}
	if listed == 0 {
		fmt.Fprintf(stderr, "belum ada aturan di %s\n", cfg.Rules.File)
	}
	return nil
}
