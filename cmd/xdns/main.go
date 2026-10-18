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
  run     jalankan server           (belum diimplementasi)
  log     baca log query            (belum diimplementasi)
  rule    kelola aturan kustom      (belum diimplementasi)
  lists   kelola daftar blokir      (belum diimplementasi)
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
	case "run", "log", "lists":
		fmt.Fprintf(stderr, "xdns %s: belum diimplementasi\n", command)
		return exitUsage
	default:
		fmt.Fprintf(stderr, "xdns: perintah tidak dikenal %q\n\n%s", command, usage)
		return exitUsage
	}
}
