package controller

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/shiinobu/hecatedns/backend/dashboard"
	"github.com/shiinobu/hecatedns/backend/lib/config"
	"github.com/shiinobu/hecatedns/backend/lib/dotnames"
)

const publicResolveTimeout = 3 * time.Second

var publicResolvers = []string{"1.1.1.1:53", "8.8.8.8:53"}

func dashboardRotation(cfg config.Config) dashboard.RotationOptions {
	if cfg.DoT.NamesFile == "" {
		return dashboard.RotationOptions{}
	}
	return dashboard.RotationOptions{
		File:     cfg.DoT.NamesFile,
		Resolves: dashboard.NewPublicResolveCheck(publicResolvers, publicResolveTimeout),
		Serves:   dashboardServes(cfg),
		Reaches:  dashboardReaches(cfg),
	}
}

func dashboardReaches(cfg config.Config) func(context.Context, string) error {
	if !cfg.DoT.Enabled || len(cfg.DoT.Listen) == 0 {
		return nil
	}
	listen, err := netip.ParseAddrPort(cfg.DoT.Listen[0])
	if err != nil {
		return nil
	}
	return dashboard.NewPublicReachCheck(dashboard.PublicReachOptions{
		Resolvers: publicResolvers,
		Port:      strconv.Itoa(int(listen.Port())),
		Timeout:   publicResolveTimeout,
	})
}

func dashboardServes(cfg config.Config) func(context.Context, string) error {
	if !cfg.DoT.Enabled || len(cfg.DoT.Listen) == 0 {
		return nil
	}
	addr, ok := loopbackTarget(cfg.DoT.Listen[0])
	if !ok {
		return nil
	}
	return func(ctx context.Context, host string) error {
		result := dashboard.NewResolverCheck(dashboard.ResolverOptions{Addr: addr, ServerName: host})(ctx)
		if result.OK {
			return nil
		}
		return errors.New(result.Error)
	}
}

func servedDoTNames(cfg config.Config) []string {
	names := slices.Clone(cfg.DoT.Names)
	if cfg.DoT.NamesFile == "" {
		return names
	}
	entries, _, err := dotnames.Read(cfg.DoT.NamesFile)
	if err != nil {
		return names
	}
	return append(names, dotnames.Hosts(entries)...)
}

type fileNames struct {
	path string

	mu     sync.Mutex
	loaded bool
	stamp  dotnames.Stamp
	hosts  []string
}

func newFileNames(path string) *fileNames {
	return &fileNames{path: path}
}

func (f *fileNames) Hosts() []string {
	stamp, statErr := dotnames.StatFile(f.path)
	f.mu.Lock()
	defer f.mu.Unlock()
	if statErr != nil || (f.loaded && stamp == f.stamp) {
		return f.hosts
	}
	f.loaded, f.stamp = true, stamp
	if entries, _, err := dotnames.Read(f.path); err == nil {
		f.hosts = dotnames.Hosts(entries)
	}
	return f.hosts
}
