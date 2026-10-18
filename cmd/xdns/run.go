package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"xdns/internal/backend"
	"xdns/internal/backend/dnsproxybe"
	"xdns/internal/config"
	"xdns/internal/engine"
	"xdns/internal/qlog"
)

// defaultRegistry lists the backends this binary contains. The native backend
// joins in Phase 4.
func defaultRegistry() backend.Registry {
	return backend.Registry{dnsproxybe.Name: dnsproxybe.New}
}

// runServer implements `xdns run`: serve until Ctrl+C or SIGTERM.
func runServer(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runServerCtx(ctx, args, stdout, stderr, defaultRegistry())
}

// runServerCtx is runServer with an explicit context and registry, so tests can
// stop it and substitute a backend that opens no socket.
func runServerCtx(ctx context.Context, args []string, stdout, stderr io.Writer, registry backend.Registry) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", defaultConfigPath, "path ke berkas konfigurasi YAML")
	name := flags.String("backend", "", "pilih backend (menimpa konfigurasi): dnsproxy")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "xdns run: argumen tak terduga %q\n", flags.Args())
		return exitUsage
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintf(stderr, "xdns run: %v\n", err)
		return exitError
	}
	if *name != "" {
		cfg.Backend = *name
	}
	if err := serve(ctx, cfg, stdout, stderr, registry); err != nil {
		fmt.Fprintf(stderr, "xdns run: %v\n", err)
		return exitError
	}
	fmt.Fprintln(stdout, "xdns berhenti")
	return exitOK
}

// serve builds the engine and the backend from cfg and runs the backend until
// ctx ends. Everything it opens is closed on return, the query log last so it
// is flushed.
func serve(ctx context.Context, cfg config.Config, stdout, stderr io.Writer, registry backend.Registry) error {
	log := slog.New(slog.NewTextHandler(stderr, nil))
	for _, warning := range cfg.Warnings() {
		fmt.Fprintf(stdout, "peringatan: %s\n", warning)
	}
	fmt.Fprintln(stdout, "catatan: daftar blokir belum dimuat (Fase 2); yang memblokir hanya aturan kustom (xdns rule)")

	rules := engine.NewRulesFile(cfg.Rules.File, log)
	if _, err := rules.Reload(); err != nil {
		fmt.Fprintf(stdout, "peringatan: aturan kustom belum bisa dibaca, server jalan tanpa aturan: %v\n", err)
	}
	go rules.Run(ctx, cfg.Rules.PollInterval)

	recorder, closeLog, err := openQueryLog(cfg, log)
	if err != nil {
		return err
	}
	defer closeLog()

	eng, err := newEngine(cfg, rules, recorder, log)
	if err != nil {
		return err
	}
	b, err := registry.New(cfg.Backend, backend.Deps{Cfg: cfg, Engine: eng, Log: log})
	if err != nil {
		return err
	}
	return runBackend(ctx, b, stdout)
}

// openQueryLog starts the query log when the configuration enables it. The
// returned close function is always safe to call.
func openQueryLog(cfg config.Config, log *slog.Logger) (engine.Recorder, func(), error) {
	if !cfg.Log.Enabled {
		return nil, func() {}, nil
	}
	w, err := qlog.Open(qlog.Options{Dir: cfg.Log.Dir, RetentionDays: cfg.Log.RetentionDays, Log: log})
	if err != nil {
		return nil, nil, err
	}
	return w, func() {
		if err := w.Close(); err != nil {
			log.Error("closing the query log failed", "error", err)
		}
	}, nil
}

func newEngine(cfg config.Config, rules engine.RuleSource, recorder engine.Recorder, log *slog.Logger) (*engine.Engine, error) {
	acl, err := engine.NewACL(cfg.AllowedClients)
	if err != nil {
		return nil, err
	}
	responder, err := engine.NewResponder(cfg.Block.Mode, cfg.Block.TTL)
	if err != nil {
		return nil, err
	}
	return engine.New(engine.Options{ACL: acl, Rules: rules, Responder: responder, Recorder: recorder, Log: log})
}

// runBackend starts b, reports where it listens once it is up, and waits for it
// to finish.
func runBackend(ctx context.Context, b backend.Backend, stdout io.Writer) error {
	result := make(chan error, 1)
	go func() { result <- b.Run(ctx) }()

	if bound, ok := b.(backend.Bound); ok {
		select {
		case <-bound.Ready():
		case err := <-result:
			return err
		}
		if !announce(stdout, b.Name(), bound) {
			return <-result
		}
	}
	return <-result
}

// announce prints the backend and its listen addresses and reports whether it
// found any. Ready is also closed when startup failed; then there is no address
// and nothing may be announced, whatever the timing of Run's result.
func announce(stdout io.Writer, name string, bound backend.Bound) bool {
	var lines []string
	for _, network := range []string{backend.NetworkUDP, backend.NetworkTCP} {
		if addr := bound.Addr(network); addr != nil {
			lines = append(lines, fmt.Sprintf("mendengarkan: %s %s", network, addr))
		}
	}
	if len(lines) == 0 {
		return false
	}
	fmt.Fprintf(stdout, "xdns berjalan (backend: %s)\n", name)
	for _, line := range lines {
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintln(stdout, "tekan Ctrl+C untuk berhenti")
	return true
}
