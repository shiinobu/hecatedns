package controller

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shiinobu/hecatedns/backend/core/native"
	"github.com/shiinobu/hecatedns/backend/lib/config"
	"github.com/shiinobu/hecatedns/backend/service/selfcheck"
)

const (
	defaultConfigPath = "hecate.yaml"

	dotExpiryWarning = 14 * 24 * time.Hour
)

func runCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", defaultConfigPath, "path to the YAML configuration file")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "hecate check: unexpected argument %q, use -config <file>\n", flags.Args())
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
		fmt.Fprintf(stdout, "warning: %s\n", w)
	}
	for _, l := range res.Lists {
		if !l.Cached {
			continue
		}
		if l.SampleDomain != "" && l.SampleAnswer != "" {
			fmt.Fprintf(stdout, "list %s: %d domains (example %s -> %s)\n",
				l.Name, l.Entries, l.SampleDomain, l.SampleAnswer)
		} else {
			fmt.Fprintf(stdout, "list %s: %d domains\n", l.Name, l.Entries)
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "hecate check: self-check FAILED: %v\n", err)
		fmt.Fprintln(stdout, "self-check: FAILED")
		return exitError
	}
	if !checkDoTCertificate(stdout, stderr, cfg) {
		return exitError
	}
	fmt.Fprintln(stdout, "safe_search (S3): PASSED (www.google.com is not redirected to forcesafesearch.google.com)")
	fmt.Fprintln(stdout, "self-check: PASSED")
	return exitOK
}

func checkDoTCertificate(stdout, stderr io.Writer, cfg config.Config) bool {
	if !cfg.DoT.Enabled {
		return true
	}
	expires, err := native.CheckDoT(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "hecate check: dot certificate: %v\n", err)
		fmt.Fprintln(stdout, "dot certificate: FAILED")
		return false
	}
	fmt.Fprintf(stdout, "dot certificate: PASSED (covers every configured name, valid until %s)\n", expires.UTC().Format(time.RFC3339))
	if left := time.Until(expires); left < dotExpiryWarning {
		fmt.Fprintf(stdout, "warning: the DoT certificate expires in %d hours, renew it\n", int(left/time.Hour))
	}
	return true
}

func printSummary(w io.Writer, path string, cfg config.Config) {
	fmt.Fprintf(w, "configuration valid: %s\n", path)
	fmt.Fprintf(w, "backend: %s\n", cfg.Backend)
	fmt.Fprintf(w, "listen: %s\n", listenSummary(cfg.Listen))
	if cfg.DoT.Enabled {
		fmt.Fprintf(w, "dot: %s (%d server names, certificate %s)\n", strings.Join(cfg.DoT.Listen, ", "), len(cfg.DoT.Names), cfg.DoT.CertFile)
	}
	fmt.Fprintf(w, "upstreams: %d\n", len(cfg.Upstreams))
	fmt.Fprintf(w, "active lists: %d\n", enabledSources(cfg))
	for _, warning := range cfg.Warnings() {
		fmt.Fprintf(w, "warning: %s\n", warning)
	}
}

func listenSummary(listen []string) string {
	if len(listen) == 0 {
		return "none, plain DNS is off"
	}
	return strings.Join(listen, ", ")
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
