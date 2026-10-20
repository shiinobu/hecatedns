package controller

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/shiinobu/hecatedns/backend/dashboard"
	"github.com/shiinobu/hecatedns/backend/lib/certstore"
	"github.com/shiinobu/hecatedns/backend/lib/config"
	"github.com/shiinobu/hecatedns/backend/service/lists"
)

func runDashboardServer(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return dashboardRunCtx(ctx, args, stdout, stderr)
}

func dashboardRunCtx(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", defaultConfigPath, "path to the YAML configuration file")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "hecate dashboard: unexpected argument %q\n", flags.Args())
		return exitUsage
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintf(stderr, "hecate dashboard: %v\n", err)
		return exitError
	}
	if !cfg.Dashboard.Enabled {
		fmt.Fprintln(stderr, "hecate dashboard: the dashboard is disabled in the config (dashboard.enabled is false)")
		return exitError
	}
	if err := serveDashboard(ctx, cfg, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "hecate dashboard: %v\n", err)
		return exitError
	}
	fmt.Fprintln(stdout, "hecate dashboard stopped")
	return exitOK
}

func serveDashboard(ctx context.Context, cfg config.Config, stdout, stderr io.Writer) error {
	log := slog.New(slog.NewTextHandler(stderr, nil))
	for _, warning := range cfg.Warnings() {
		fmt.Fprintf(stdout, "warning: %s\n", warning)
	}
	srv, err := newDashboardServer(ctx, cfg, log)
	if err != nil {
		return err
	}
	for _, addr := range srv.Addrs() {
		fmt.Fprintf(stdout, "dashboard listening: %s\n", addr)
	}
	fmt.Fprintln(stdout, "press Ctrl+C to stop")
	return srv.Run(ctx)
}

func newDashboardServer(ctx context.Context, cfg config.Config, log *slog.Logger) (*dashboard.Server, error) {
	listen := make([]netip.AddrPort, 0, len(cfg.Dashboard.Listen))
	for _, raw := range cfg.Dashboard.Listen {
		ap, err := netip.ParseAddrPort(raw)
		if err != nil {
			return nil, fmt.Errorf("dashboard.listen %q: %w", raw, err)
		}
		listen = append(listen, ap)
	}
	store, err := openDashboardCerts(ctx, cfg, log)
	if err != nil {
		return nil, err
	}
	auth, err := dashboard.NewAuth(dashboard.AuthOptions{
		KeyHashFile: cfg.Dashboard.KeyHashFile,
		SecretFile:  cfg.Dashboard.SecretFile,
		SessionDays: cfg.Dashboard.SessionDays,
		Log:         log,
	})
	if err != nil {
		return nil, err
	}
	apiOpts := dashboardAPIOptions(cfg, log)
	if store != nil {
		apiOpts.Cert = store
	}
	return dashboard.New(dashboard.Options{
		Listen:       listen,
		Names:        cfg.Dashboard.Names,
		ProbeDomains: cfg.Dashboard.ProbeDomains,
		Certs:        store,
		API:          auth.Wrap(dashboard.NewAPI(apiOpts)),
		Log:          log,
	})
}

func openDashboardCerts(ctx context.Context, cfg config.Config, log *slog.Logger) (*certstore.Store, error) {
	configured := cfg.Dashboard.EffectiveCerts(cfg.DoT)
	if len(configured) == 0 {
		return nil, nil
	}
	pairs := make([]certstore.Pair, len(configured))
	for i, p := range configured {
		pairs[i] = certstore.Pair{CertFile: p.CertFile, KeyFile: p.KeyFile}
	}
	store, err := certstore.New(certstore.Config{Pairs: pairs, Log: log})
	if err != nil {
		return nil, err
	}
	go store.Watch(ctx, cfg.DoT.ReloadEvery)
	return store, nil
}

func dashboardAPIOptions(cfg config.Config, log *slog.Logger) dashboard.APIOptions {
	return dashboard.APIOptions{
		LogDir:     cfg.Log.Dir,
		LogEnabled: cfg.Log.Enabled,
		Location:   time.Local,
		RulesFile:  cfg.Rules.File,
		ListsDir:   lists.Dir(cfg.DataDir),
		Lists:      cfg.Lists.Sources,
		Resolver:   dashboardResolver(cfg),
		Guard:      dashboardGuard(cfg),
		Log:        log,
	}
}

func dashboardGuard(cfg config.Config) *dashboard.Guard {
	names := slices.Concat(cfg.Dashboard.Names, cfg.Dashboard.ProbeDomains, cfg.DoT.Names, cfg.Bootstrap)
	for _, upstream := range cfg.Upstreams {
		names = append(names, upstreamHost(upstream))
	}
	return dashboard.NewGuard(names...)
}

func upstreamHost(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Hostname()
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		return host
	}
	return raw
}

func dashboardResolver(cfg config.Config) func(context.Context) dashboard.ResolverResult {
	if cfg.DoT.Enabled && len(cfg.DoT.Listen) > 0 && len(cfg.DoT.Names) > 0 {
		if addr, ok := loopbackTarget(cfg.DoT.Listen[0]); ok {
			return dashboard.NewResolverCheck(dashboard.ResolverOptions{Addr: addr, ServerName: cfg.DoT.Names[0]})
		}
	}
	if len(cfg.Listen) > 0 {
		if addr, ok := loopbackTarget(cfg.Listen[0]); ok {
			return dashboard.NewResolverCheck(dashboard.ResolverOptions{Addr: addr})
		}
	}
	return nil
}

func loopbackTarget(listen string) (string, bool) {
	ap, err := netip.ParseAddrPort(listen)
	if err != nil {
		return "", false
	}
	addr := ap.Addr()
	if addr.IsUnspecified() {
		addr = netip.AddrFrom4([4]byte{127, 0, 0, 1})
	}
	return net.JoinHostPort(addr.String(), fmt.Sprint(ap.Port())), true
}
