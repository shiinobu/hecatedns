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

const bytesPerMBShift = 20

const runLogMaxBytes = 5 << bytesPerMBShift

func defaultRegistry() core.Registry {
	return core.Registry{
		dnsproxy.Name: dnsproxy.New,
		native.Name:   native.New,
	}
}

func runServer(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runServerCtx(ctx, args, stdout, stderr, defaultRegistry())
}

func runServerCtx(ctx context.Context, args []string, stdout, stderr io.Writer, registry core.Registry) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", defaultConfigPath, "path to the YAML configuration file")
	name := flags.String("backend", "", "select the backend (overrides the config): dnsproxy")
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
		fmt.Fprintf(stdout, "warning: %s\n", warning)
	}

	rules := engine.NewRulesFile(cfg.Rules.File, log)
	if _, err := rules.Reload(); err != nil {
		fmt.Fprintf(stdout, "warning: custom rules could not be read, the server runs without rules: %v\n", err)
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

	mgr := lists.NewManager(lists.ManagerOptions{
		Dir:            lists.Dir(cfg.DataDir),
		Sources:        cfg.Lists.Sources,
		Bootstrap:      cfg.Bootstrap,
		Target:         eng,
		UpdateInterval: cfg.Lists.UpdateInterval,
		Log:            log,
	})
	if _, err := mgr.LoadCache(); err != nil {
		fmt.Fprintf(stdout, "warning: some block-list caches failed to load: %v\n", err)
	}
	go mgr.Run(ctx)

	b, err := registry.New(cfg.Backend, core.Deps{Cfg: cfg, Engine: eng, Log: log})
	if err != nil {
		return err
	}
	return runBackend(ctx, b, stdout)
}

func openRunLog(cfg config.Config, stderr, stdout io.Writer) (io.Writer, func()) {
	if !cfg.Log.Enabled {
		return stderr, func() {}
	}
	file, err := runlog.Open(cfg.Log.Dir, runLogMaxBytes)
	if err != nil {
		fmt.Fprintf(stdout, "warning: the operational log cannot be written to a file, only to the screen: %v\n", err)
		return stderr, func() {}
	}
	return runlog.Tee(stderr, file), func() { _ = file.Close() }
}

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

	return engine.New(engine.Options{ACL: acl, Rules: rules, Responder: responder, Special: cfg.SpecialNames, Recorder: recorder, Log: log})
}

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

func announce(stdout io.Writer, name string, bound core.Bound) bool {
	var lines []string
	for _, network := range []string{core.NetworkUDP, core.NetworkTCP} {
		if addr := bound.Addr(network); addr != nil {
			lines = append(lines, fmt.Sprintf("listening: %s %s", network, addr))
		}
	}
	if len(lines) == 0 {
		return false
	}
	fmt.Fprintf(stdout, "hecate berjalan (backend: %s)\n", name)
	for _, line := range lines {
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintln(stdout, "press Ctrl+C to stop")
	return true
}
