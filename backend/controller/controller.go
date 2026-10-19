package controller

import (
	"fmt"
	"io"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usage = `usage: hecate <command> [options]

commands:
  check   validate the configuration file (hecate check -config hecate.yaml)
  run     run the server                  (hecate run -config hecate.yaml)
  log     read the query log              (hecate log -since 10m -blocked)
  rule    manage custom rules             (hecate rule add block example.com)
  lists   manage block lists              (hecate lists status | update)
`

func Execute(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	command, rest := args[0], args[1:]
	switch command {
	case "check":
		return runCheck(rest, stdout, stderr)
	case "rule":
		return runRule(rest, stdout, stderr)
	case "log":
		return runLog(rest, stdout, stderr)
	case "run":
		return runServer(rest, stdout, stderr)
	case "lists":
		return runLists(rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "hecate: unknown command %q\n\n%s", command, usage)
		return exitUsage
	}
}
