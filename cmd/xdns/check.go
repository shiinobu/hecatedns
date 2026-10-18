package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"xdns/internal/config"
)

const defaultConfigPath = "xdns.yaml"

// runCheck loads and validates a configuration file and prints a summary.
func runCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", defaultConfigPath, "path ke berkas konfigurasi YAML")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "xdns check: argumen tak terduga %q, gunakan -config <berkas>\n", flags.Args())
		return exitUsage
	}

	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintf(stderr, "xdns check: %v\n", err)
		return exitError
	}
	printSummary(stdout, *path, cfg)
	return exitOK
}

// printSummary writes the human-readable result of a successful check.
func printSummary(w io.Writer, path string, cfg config.Config) {
	fmt.Fprintf(w, "konfigurasi valid: %s\n", path)
	fmt.Fprintf(w, "backend: %s\n", cfg.Backend)
	fmt.Fprintf(w, "listen: %s\n", strings.Join(cfg.Listen, ", "))
	fmt.Fprintf(w, "upstreams: %d\n", len(cfg.Upstreams))
	fmt.Fprintf(w, "daftar aktif: %d\n", enabledSources(cfg))
	for _, warning := range cfg.Warnings() {
		fmt.Fprintf(w, "peringatan: %s\n", warning)
	}
}

// enabledSources counts the blocklist sources that are switched on.
func enabledSources(cfg config.Config) int {
	count := 0
	for _, source := range cfg.Lists.Sources {
		if source.Enabled {
			count++
		}
	}
	return count
}
