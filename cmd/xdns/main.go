// Command xdns is a DNS server that blocks ads.
package main

import (
	"fmt"
	"io"
	"os"
)

// Process exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usage = `penggunaan: xdns <perintah> [opsi]

perintah:
  check   validasi berkas konfigurasi (xdns check -config xdns.yaml)
  run     jalankan server           (xdns run -config xdns.yaml)
  log     baca log query            (xdns log -since 10m -blocked)
  rule    kelola aturan kustom      (xdns rule add block contoh.com)
  lists   kelola daftar blokir      (xdns lists status | update)
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches args to a subcommand and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
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
		// agy wrote this
		return runLists(rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "xdns: perintah tidak dikenal %q\n\n%s", command, usage)
		return exitUsage
	}
}
