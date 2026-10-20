package dashboard

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"time"

	"github.com/shiinobu/hecatedns/backend/engine"
	"github.com/shiinobu/hecatedns/backend/service/qlog"
)

const (
	defaultLifetimeEvery = time.Hour
	settleGrace          = time.Hour
	dayKeyLayout         = "2006-01-02"
)

type dayCounts struct {
	Total   int `json:"total"`
	Blocked int `json:"blocked"`
	Passed  int `json:"passed"`
	Errors  int `json:"errors"`
}

type lifetimeInfo struct {
	Since   string `json:"since"`
	Total   int    `json:"total"`
	Blocked int    `json:"blocked"`
	Passed  int    `json:"passed"`
	Errors  int    `json:"errors"`
	Partial bool   `json:"partial"`
}

type lifetimeState struct {
	loaded       bool
	days         map[string]dayCounts
	gap          bool
	unsaved      bool
	noWrite      bool
	writeFailing bool
}

func (c *dayCounts) add(action string) {
	c.Total++
	switch action {
	case engine.ActionBlock:
		c.Blocked++
	case engine.ActionPass:
		c.Passed++
	case engine.ActionError:
		c.Errors++
	}
}

func (c dayCounts) valid() bool {
	return c.Total >= 0 && c.Blocked >= 0 && c.Passed >= 0 && c.Errors >= 0
}

func (a *API) countLifetime() *lifetimeInfo {
	if a.opts.DataDir == "" {
		return nil
	}
	return a.refreshLifetime()
}

func (a *API) RunLifetime(ctx context.Context) {
	if a.opts.DataDir == "" {
		return
	}
	a.refreshLifetime()
	ticker := time.NewTicker(a.opts.LifetimeEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.refreshLifetime()
		}
	}
}

func (a *API) refreshLifetime() *lifetimeInfo {
	a.lifetimeMu.Lock()
	defer a.lifetimeMu.Unlock()
	st := &a.lifetime
	path := filepath.Join(a.opts.DataDir, lifetimeFileName)
	if !st.loaded {
		a.loadLifetime(st, path)
	}
	all := maps.Clone(st.days)
	partial := st.gap
	days, err := qlog.Days(a.opts.LogDir, a.opts.Location)
	if err != nil {
		a.opts.Log.Warn("listing the query log days failed", "err", err)
		partial = true
	}
	now := a.opts.Now()
	for _, day := range days {
		key := day.Format(dayKeyLayout)
		if _, kept := st.days[key]; kept {
			continue
		}
		counts, complete := a.countDay(day)
		all[key] = counts
		switch {
		case !complete:
			partial = true
		case settled(day, now):
			st.days[key] = counts
			st.unsaved = true
		}
	}
	a.saveLifetime(st, path)
	return summarizeLifetime(all, a.opts.Location, partial || st.unsaved)
}

func (a *API) loadLifetime(st *lifetimeState, path string) {
	file, err := readLifetimeFile(path)
	st.loaded = true
	st.days = file.Days
	if err == nil {
		return
	}
	st.gap = true
	if !errors.Is(err, errUnusableLifetimeFile) {
		st.noWrite = true
		a.opts.Log.Warn("reading the lifetime counts failed, the file is left as it is", "path", path, "err", err)
		return
	}
	aside, moveErr := setAsideLifetimeFile(path, a.opts.Now())
	if moveErr != nil {
		st.noWrite = true
		a.opts.Log.Warn("the lifetime counts file is unusable and could not be set aside, it will not be overwritten", "path", path, "err", err, "move", moveErr)
		return
	}
	a.opts.Log.Warn("the lifetime counts file was unusable and is kept aside, counting again from the query log", "kept", aside, "err", err)
}

func (a *API) saveLifetime(st *lifetimeState, path string) {
	if !st.unsaved || st.noWrite {
		return
	}
	file := lifetimeFile{Version: lifetimeFileVersion, Days: st.days}
	if err := writeLifetimeFile(path, file); err != nil {
		if !st.writeFailing {
			a.opts.Log.Warn("saving the lifetime counts failed, they are kept in memory and retried", "path", path, "err", err)
		}
		st.writeFailing = true
		return
	}
	st.unsaved = false
	st.writeFailing = false
}

func (a *API) countDay(day time.Time) (counts dayCounts, complete bool) {
	filter := qlog.Filter{Since: day, Until: day.AddDate(0, 0, 1).Add(-time.Nanosecond)}
	cut, err := a.scanLog(filter, func(rec qlog.Record) { counts.add(rec.Action) })
	if err != nil {
		a.opts.Log.Warn("counting a query log day failed", "day", day.Format(dayKeyLayout), "err", err)
		return counts, false
	}
	return counts, !cut
}

func settled(day, now time.Time) bool {
	return !now.Before(day.AddDate(0, 0, 1).Add(settleGrace))
}

func summarizeLifetime(days map[string]dayCounts, loc *time.Location, partial bool) *lifetimeInfo {
	info := &lifetimeInfo{Partial: partial}
	first := ""
	for key, counts := range days {
		info.Total += counts.Total
		info.Blocked += counts.Blocked
		info.Passed += counts.Passed
		info.Errors += counts.Errors
		if first == "" || key < first {
			first = key
		}
	}
	if at, err := time.ParseInLocation(dayKeyLayout, first, loc); err == nil {
		info.Since = at.Format(time.RFC3339)
	}
	return info
}
