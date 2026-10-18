package lists

// agy wrote this

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"xdns/internal/config"
	"xdns/internal/engine"
)

const (
	defaultUpdateInterval = 24 * time.Hour
	defaultPollInterval   = 5 * time.Second
	jitterDivisor         = 10 // up to +10% jitter
)

// Swapper receives newly built list snapshots. *engine.Engine implements it.
type Swapper interface {
	SwapLists(set *engine.ListSet)
}

// ManagerOptions configures a Manager.
type ManagerOptions struct {
	Dir            string
	Sources        []config.Source
	Bootstrap      []string
	Target         Swapper
	Fetcher        *Fetcher
	UpdateInterval time.Duration
	PollInterval   time.Duration
	Jitter         func(time.Duration) time.Duration
	Log            *slog.Logger
}

type fileStamp struct {
	size    int64
	modTime time.Time
}

// Manager loads cached blocklists at startup, refreshes them over HTTPS on a
// jittered schedule, watches for on-disk updates (from `xdns lists update`),
// and atomically publishes immutable *engine.ListSet snapshots.
type Manager struct {
	dir            string
	sources        []config.Source
	target         Swapper
	fetcher        *Fetcher
	updateInterval time.Duration
	pollInterval   time.Duration
	jitter         func(time.Duration) time.Duration
	log            *slog.Logger

	current atomic.Pointer[engine.ListSet]

	mu     sync.Mutex
	sets   map[string]*engine.DomainSet
	stamps map[string]fileStamp
}

// NewManager constructs a Manager from opts.
func NewManager(opts ManagerOptions) *Manager {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	fetcher := opts.Fetcher
	if fetcher == nil {
		fetcher = &Fetcher{Dir: opts.Dir, Bootstrap: slices.Clone(opts.Bootstrap)}
	}
	updateInt := opts.UpdateInterval
	if updateInt <= 0 {
		updateInt = defaultUpdateInterval
	}
	pollInt := opts.PollInterval
	if pollInt <= 0 {
		pollInt = defaultPollInterval
	}
	jitter := opts.Jitter
	if jitter == nil {
		jitter = defaultJitter
	}
	return &Manager{
		dir:            opts.Dir,
		sources:        slices.Clone(opts.Sources),
		target:         opts.Target,
		fetcher:        fetcher,
		updateInterval: updateInt,
		pollInterval:   pollInt,
		jitter:         jitter,
		log:            log,
		sets:           make(map[string]*engine.DomainSet),
		stamps:         make(map[string]fileStamp),
	}
}

func defaultJitter(d time.Duration) time.Duration {
	maxJ := d / jitterDivisor
	if maxJ <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(maxJ)))
}

// Snapshot returns the latest published ListSet, or nil if none was built yet.
func (m *Manager) Snapshot() *engine.ListSet {
	return m.current.Load()
}

// LoadCache loads all enabled cached lists from disk and publishes a snapshot.
// Missing cache files are skipped quietly (they will be downloaded by Refresh);
// damaged cache files are logged and returned as a joined error while the
// remaining lists still load.
func (m *Manager) LoadCache() (loaded int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for _, src := range m.sources {
		if !src.Enabled {
			continue
		}
		stamp, exists, statErr := statList(m.dir, src.Name)
		if statErr != nil {
			m.log.Error("cached list stat failed", "list", src.Name, "error", statErr)
			errs = append(errs, statErr)
			continue
		}
		if !exists {
			continue
		}
		set, meta, loadErr := LoadCached(m.dir, src)
		if loadErr != nil {
			m.log.Error("cached list corrupt, ignoring", "list", src.Name, "error", loadErr)
			errs = append(errs, loadErr)
			continue
		}
		m.sets[src.Name] = set
		m.stamps[src.Name] = stamp
		loaded++
		m.log.Info("cached list loaded", "list", src.Name, "entries", meta.Count, "rejected", meta.Rejected)
	}
	m.publishLocked()
	return loaded, errors.Join(errs...)
}

// ReloadCache checks whether any enabled list file on disk changed since the
// last load or fetch (for example after `xdns lists update`) and publishes a
// new snapshot when it did.
func (m *Manager) ReloadCache() (changed bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for _, src := range m.sources {
		if !src.Enabled {
			continue
		}
		stamp, exists, statErr := statList(m.dir, src.Name)
		if statErr != nil {
			errs = append(errs, statErr)
			continue
		}
		if !exists {
			continue
		}
		if prev, ok := m.stamps[src.Name]; ok && prev == stamp {
			continue
		}
		set, meta, loadErr := LoadCached(m.dir, src)
		if loadErr != nil {
			m.log.Error("cached list reload failed, keeping previous copy", "list", src.Name, "error", loadErr)
			errs = append(errs, loadErr)
			continue
		}
		m.sets[src.Name] = set
		m.stamps[src.Name] = stamp
		changed = true
		m.log.Info("list reloaded from disk", "list", src.Name, "entries", meta.Count)
	}
	if changed {
		m.publishLocked()
	}
	return changed, errors.Join(errs...)
}

// Refresh downloads all enabled lists over HTTPS, updates the on-disk cache,
// and publishes a new snapshot. If a download fails, that list keeps its
// previous in-memory and on-disk copy.
func (m *Manager) Refresh(ctx context.Context) error {
	type fetched struct {
		src   config.Source
		res   FetchResult
		stamp fileStamp
	}
	var updates []fetched
	var errs []error

	for _, src := range m.sources {
		if !src.Enabled {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res, err := m.fetcher.Fetch(ctx, src)
		if err != nil {
			m.log.Error("list refresh failed, keeping cached copy", "list", src.Name, "error", err)
			errs = append(errs, err)
			continue
		}
		stamp, _, _ := statList(m.dir, src.Name)
		updates = append(updates, fetched{src: src, res: res, stamp: stamp})
		m.log.Info("list refreshed", "list", src.Name, "entries", res.Meta.Count, "not_modified", res.NotModified)
	}

	m.mu.Lock()
	for _, u := range updates {
		m.sets[u.src.Name] = u.res.Set
		m.stamps[u.src.Name] = u.stamp
	}
	m.publishLocked()
	m.mu.Unlock()

	return errors.Join(errs...)
}

// Run refreshes lists in the background immediately, then periodically every
// UpdateInterval (+ jitter) while also polling disk stamps every PollInterval
// until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	_ = m.Refresh(ctx)

	timer := time.NewTimer(m.nextDelay())
	defer timer.Stop()

	poll := time.NewTicker(m.pollInterval)
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			_, _ = m.ReloadCache()
		case <-timer.C:
			_ = m.Refresh(ctx)
			timer.Reset(m.nextDelay())
		}
	}
}

func (m *Manager) nextDelay() time.Duration {
	j := m.jitter(m.updateInterval)
	if j < 0 {
		j = 0
	}
	return m.updateInterval + j
}

func (m *Manager) publishLocked() {
	var allow, block []engine.NamedSet
	for _, src := range m.sources {
		if !src.Enabled {
			continue
		}
		set, ok := m.sets[src.Name]
		if !ok || set == nil {
			continue
		}
		named := engine.NamedSet{Name: src.Name, Set: set}
		if src.Kind == config.KindAllow {
			allow = append(allow, named)
		} else {
			block = append(block, named)
		}
	}
	snap := engine.NewListSet(allow, block)
	m.current.Store(snap)
	if m.target != nil {
		m.target.SwapLists(snap)
	}
}

func statList(dir, name string) (fileStamp, bool, error) {
	if !validListName(name) {
		return fileStamp{}, false, ErrBadListName
	}
	info, err := os.Stat(listPath(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return fileStamp{}, false, nil
	}
	if err != nil {
		return fileStamp{}, false, err
	}
	return fileStamp{size: info.Size(), modTime: info.ModTime()}, true, nil
}
