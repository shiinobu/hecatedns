package controller

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/shiinobu/hecatedns/backend/dashboard"
)

const dashboardUsage = `usage:
  hecate dashboard -config <file>                               run the dashboard server
  hecate dashboard genkey -out <keyfile> -hash-out <hashfile>   create an access key and the hash for the server
  hecate dashboard hashkey < <keyfile>                          print the hash of the key read from stdin
`

const maxKeyInputBytes = 1 << 10

func runDashboard(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, dashboardUsage)
		return exitUsage
	}
	switch {
	case strings.HasPrefix(args[0], "-"):
		return runDashboardServer(args, stdout, stderr)
	case args[0] == "genkey":
		return dashboardGenKey(args[1:], stdout, stderr)
	case args[0] == "hashkey":
		return dashboardHashKey(args[1:], stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "hecate dashboard: unknown subcommand %q\n\n%s", args[0], dashboardUsage)
		return exitUsage
	}
}

func dashboardGenKey(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("dashboard genkey", flag.ContinueOnError)
	flags.SetOutput(stderr)
	keyPath := flags.String("out", "", "file that receives the access key (must not exist)")
	hashPath := flags.String("hash-out", "", "file that receives the SHA-256 of the key")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *keyPath == "" || *hashPath == "" ||
		filepath.Clean(*keyPath) == filepath.Clean(*hashPath) {
		fmt.Fprint(stderr, dashboardUsage)
		return exitUsage
	}
	if err := writeNewKey(*keyPath, *hashPath); err != nil {
		fmt.Fprintf(stderr, "hecate dashboard genkey: %v\n", err)
		return exitError
	}
	fmt.Fprintf(stdout, "key written to %s\nhash written to %s\n", *keyPath, *hashPath)
	return exitOK
}

func writeNewKey(keyPath, hashPath string) error {
	key, err := dashboard.GenerateKey()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create key file: %w", err)
	}
	_, writeErr := io.WriteString(f, key+"\n")
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return errors.Join(fmt.Errorf("write key file: %w", err), os.Remove(keyPath))
	}
	if err := os.WriteFile(hashPath, []byte(dashboard.HashKey(key)+"\n"), 0o600); err != nil {
		return errors.Join(fmt.Errorf("write hash file: %w", err), os.Remove(keyPath))
	}
	return nil
}

func dashboardHashKey(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprint(stderr, dashboardUsage)
		return exitUsage
	}
	data, err := io.ReadAll(io.LimitReader(stdin, maxKeyInputBytes))
	if err != nil {
		fmt.Fprintf(stderr, "hecate dashboard hashkey: read stdin: %v\n", err)
		return exitError
	}
	key, err := dashboard.NormalizeKey(string(data))
	if err != nil {
		fmt.Fprintln(stderr, "hecate dashboard hashkey: the input is not a valid access key")
		return exitError
	}
	fmt.Fprintln(stdout, dashboard.HashKey(key))
	return exitOK
}
