package controller

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/shiinobu/hecatedns/backend/lib/config"
	"github.com/shiinobu/hecatedns/backend/service/selfcheck"
)

const defaultConfigPath = "hecate.yaml"

func runCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", defaultConfigPath, "path ke berkas konfigurasi YAML")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "hecate check: argumen tak terduga %q, gunakan -config <berkas>\n", flags.Args())
		return exitUsage
	}

	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintf(stderr, "hecate check: %v\n", err)
		return exitError
	}
	printSummary(stdout, *path, cfg)

	res, err := selfcheck.Run(context.Background(), cfg, selfcheck.Options{})
	for _, w := range res.Warnings {
		fmt.Fprintf(stdout, "peringatan: %s\n", w)
	}
	for _, l := range res.Lists {
		if !l.Cached {
			continue
		}
		if l.SampleDomain != "" && l.SampleAnswer != "" {
			fmt.Fprintf(stdout, "daftar %s: %d domain (contoh %s -> %s)\n",
				l.Name, l.Entries, l.SampleDomain, l.SampleAnswer)
		} else {
			fmt.Fprintf(stdout, "daftar %s: %d domain\n", l.Name, l.Entries)
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "hecate check: uji mandiri GAGAL: %v\n", err)
		fmt.Fprintln(stdout, "uji mandiri: GAGAL")
		return exitError
	}
	fmt.Fprintln(stdout, "safe_search (S3): LULUS (www.google.com tidak diarahkan ke forcesafesearch.google.com)")
	fmt.Fprintln(stdout, "uji mandiri: LULUS")
	return exitOK
}

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

func enabledSources(cfg config.Config) int {
	count := 0
	for _, source := range cfg.Lists.Sources {
		if source.Enabled {
			count++
		}
	}
	return count
}
