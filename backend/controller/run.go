package controller

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/shiinobu/hecatedns/backend/core"
	"github.com/shiinobu/hecatedns/backend/core/dnsproxy"
	"github.com/shiinobu/hecatedns/backend/core/native"
	"github.com/shiinobu/hecatedns/backend/engine"
	"github.com/shiinobu/hecatedns/backend/lib/config"
	"github.com/shiinobu/hecatedns/backend/lib/runlog"
	"github.com/shiinobu/hecatedns/backend/service/lists"
	"github.com/shiinobu/hecatedns/backend/service/qlog"
)

// bytesPerMBShift converts megabytes to bytes with a shift (1 MB = 1 << 20).
const bytesPerMBShift = 20

// runLogMaxBytes caps the operational log file; with its one backup the two
// files stay under 10 MB.
const runLogMaxBytes = 5 << bytesPerMBShift

// defaultRegistry lists the backends this binary contains; `backend:` in the
// configuration picks one.
func defaultRegistry() core.Registry {
	return core.Registry{
		dnsproxy.Name: dnsproxy.New,
		native.Name:   native.New,
	}
}

// runServer implements `hecate run`: serve until Ctrl+C or SIGTERM.
func runServer(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runServerCtx(ctx, args, stdout, stderr, defaultRegistry())
}

// runServerCtx is runServer with an explicit context and registry, so tests can
// stop it and substitute a backend that opens no socket.
func runServerCtx(ctx context.Context, args []string, stdout, stderr io.Writer, registry core.Registry) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", defaultConfigPath, "path ke berkas konfigurasi YAML")
	name := flags.String("backend", "", "pilih backend (menimpa konfigurasi): dnsproxy")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "hecate run: argumen tak terduga %q\n", flags.Args())
		return exitUsage
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintf(stderr, "hecate run: %v\n", err)
		return exitError
	}
	if *name != "" {
		cfg.Backend = *name
	}
	if err := serve(ctx, cfg, stdout, stderr, registry); err != nil {
		fmt.Fprintf(stderr, "hecate run: %v\n", err)
		return exitError
	}
	fmt.Fprintln(stdout, "hecate berhenti")
	return exitOK
}

// serve builds the engine and the backend from cfg and runs the backend until
// ctx ends. Everything it opens is closed on return, the query log last so it
// is flushed. An error that ends the server is written to the operational log
// before that log closes, because a hidden scheduled task has no console to
// show it.
func serve(ctx context.Context, cfg config.Config, stdout, stderr io.Writer, registry core.Registry) (err error) {
	logOut, closeRunLog := openRunLog(cfg, stderr, stdout)
	defer closeRunLog()
	log := slog.New(slog.NewTextHandler(logOut, nil))
	defer func() {
		if err != nil {
			log.Error("hecate stopped because of an error", "error", err)
		}
	}()
	for _, warning := range cfg.Warnings() {
		fmt.Fprintf(stdout, "peringatan: %s\n", warning)
	}

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

	// agy wrote this
	mgr := lists.NewManager(lists.ManagerOptions{
		Dir:            lists.Dir(cfg.DataDir),
		Sources:        cfg.Lists.Sources,
		Bootstrap:      cfg.Bootstrap,
		Target:         eng,
		UpdateInterval: cfg.Lists.UpdateInterval,
		Log:            log,
	})
	if _, err := mgr.LoadCache(); err != nil {
		fmt.Fprintf(stdout, "peringatan: sebagian cache daftar blokir gagal dimuat: %v\n", err)
	}
	go mgr.Run(ctx)

	b, err := registry.New(cfg.Backend, core.Deps{Cfg: cfg, Engine: eng, Log: log})
	if err != nil {
		return err
	}
	return runBackend(ctx, b, stdout)
}

// openRunLog returns the writer for the operational log: stderr, plus the file
// hecate.log in the log directory when logging is enabled, so a server started
// without a console (a scheduled task) still leaves its warnings behind. A file
// that cannot be opened is a warning, not a reason to stop. The returned close
// function is always safe to call.
func openRunLog(cfg config.Config, stderr, stdout io.Writer) (io.Writer, func()) {
	if !cfg.Log.Enabled {
		return stderr, func() {}
	}
	file, err := runlog.Open(cfg.Log.Dir, runLogMaxBytes)
	if err != nil {
		fmt.Fprintf(stdout, "peringatan: log operasional tidak bisa ditulis ke berkas, hanya ke layar: %v\n", err)
		return stderr, func() {}
	}
	return runlog.Tee(stderr, file), func() { _ = file.Close() }
}

// openQueryLog starts the query log when the configuration enables it. The
// returned close function is always safe to call.
func openQueryLog(cfg config.Config, log *slog.Logger) (engine.Recorder, func(), error) {
	if !cfg.Log.Enabled {
		return nil, func() {}, nil
	}
	w, err := qlog.Open(qlog.Options{
		Dir:            cfg.Log.Dir,
		RetentionDays:  cfg.Log.RetentionDays,
		MaxBytesPerDay: int64(cfg.Log.MaxMBPerDay) << bytesPerMBShift,
		Log:            log,
	})
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
	// agy wrote this
	return engine.New(engine.Options{ACL: acl, Rules: rules, Responder: responder, Special: cfg.SpecialNames, Recorder: recorder, Log: log})
}

// runBackend starts b, reports where it listens once it is up, and waits for it
// to finish.
func runBackend(ctx context.Context, b core.Backend, stdout io.Writer) error {
	result := make(chan error, 1)
	go func() { result <- b.Run(ctx) }()

	if bound, ok := b.(core.Bound); ok {
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
func announce(stdout io.Writer, name string, bound core.Bound) bool {
	var lines []string
	for _, network := range []string{core.NetworkUDP, core.NetworkTCP} {
		if addr := bound.Addr(network); addr != nil {
			lines = append(lines, fmt.Sprintf("mendengarkan: %s %s", network, addr))
		}
	}
	if len(lines) == 0 {
		return false
	}
	fmt.Fprintf(stdout, "hecate berjalan (backend: %s)\n", name)
	for _, line := range lines {
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintln(stdout, "tekan Ctrl+C untuk berhenti")
	return true
}
