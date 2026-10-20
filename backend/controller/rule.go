package controller

import (
	"fmt"
	"io"
	"strings"

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
