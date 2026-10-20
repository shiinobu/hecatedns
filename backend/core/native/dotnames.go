package native

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/config"
	"github.com/shiinobu/hecatedns/backend/lib/dotnames"
)

const (
	namesPollEvery   = 2 * time.Second
	namesSettleAfter = 2 * time.Second
)

var errNoServedNames = errors.New("dot: no server name to serve, dot.names is empty and the names file lists none")

type nameSet struct {
	keys      map[string]struct{}
	fromFile  int
	uncovered int
}

func (n *nameSet) sameAs(other *nameSet) bool {
	if other == nil || n.fromFile != other.fromFile || len(n.keys) != len(other.keys) {
		return false
	}
	for key := range n.keys {
		if _, ok := other.keys[key]; !ok {
			return false
		}
	}
	return true
}

type namesWatch struct {
	path   string
	config []string
	certs  *certStore
	now    func() time.Time
	log    *slog.Logger
	every  time.Duration

	cur      atomic.Pointer[nameSet]
	seen     dotnames.Stamp
	recheck  bool
	rejected *dotnames.Stamp
}

func newNamesWatch(d config.DoT, certs *certStore, now func() time.Time, log *slog.Logger) (*namesWatch, error) {
	w := &namesWatch{path: d.NamesFile, config: d.Names, certs: certs, now: now, log: log, every: namesPollEvery}
	if w.path == "" {
		set, err := w.build(nil)
		if err != nil {
			return nil, err
		}
		w.cur.Store(set)
		return w, nil
	}
	if err := w.loadFirst(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *namesWatch) loadFirst() error {
	entries, rejected, stamp, err := w.readLenient()
	switch {
	case err != nil:
		if len(w.config) == 0 {
			return fmt.Errorf("dot: no server name to serve, dot.names is empty and the names file cannot be used: %w", err)
		}
		w.rejected = &stamp
		w.log.Error("the DoT names file cannot be used, only dot.names are served", "err", err)
		entries = nil
	case len(rejected) > 0:
		w.rejected = &stamp
		w.log.Error("the DoT names file has unusable lines, the valid ones are served", "lines", rejected, "valid", len(entries))
	}
	set, err := w.build(entries)
	if err != nil {
		return err
	}
	w.publish(set)
	return nil
}

func (w *namesWatch) serves(serverName string) bool {
	_, ok := w.cur.Load().keys[serverNameKey(serverName)]
	return ok
}

func (w *namesWatch) watch(ctx context.Context) {
	if w.path == "" {
		return
	}
	ticker := time.NewTicker(w.every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.check()
		}
	}
}

func (w *namesWatch) check() {
	stamp, err := dotnames.StatFile(w.path)
	if err != nil {
		w.refuse(dotnames.Stamp{}, err)
		return
	}
	if sameStamp(stamp, w.seen) && !w.recheck {
		return
	}
	w.reload()
}

func (w *namesWatch) reload() {
	entries, stamp, err := w.read()
	var next *nameSet
	if err == nil {
		next, err = w.build(entries)
	}
	var osErr *fs.PathError
	if err != nil && w.recheck && errors.As(err, &osErr) {
		return
	}
	if err != nil {
		w.refuse(stamp, err)
		return
	}
	w.rejected = nil
	if !next.sameAs(w.cur.Load()) {
		w.publish(next)
	}
}

func (w *namesWatch) read() ([]dotnames.Entry, dotnames.Stamp, error) {
	entries, stamp, err := dotnames.Read(w.path)
	return entries, w.settle(stamp, err), err
}

func (w *namesWatch) readLenient() ([]dotnames.Entry, []int, dotnames.Stamp, error) {
	entries, rejected, stamp, err := dotnames.ReadLenient(w.path)
	return entries, rejected, w.settle(stamp, err), err
}

func (w *namesWatch) settle(stamp dotnames.Stamp, err error) dotnames.Stamp {
	if err != nil && sameStamp(stamp, dotnames.Stamp{}) {
		if statStamp, statErr := dotnames.StatFile(w.path); statErr == nil {
			stamp = statStamp
		}
	}
	w.seen = stamp
	w.recheck = w.now().Sub(stamp.Time).Abs() < namesSettleAfter
	return stamp
}

func (w *namesWatch) refuse(stamp dotnames.Stamp, err error) {
	if w.rejected != nil && sameStamp(*w.rejected, stamp) {
		return
	}
	w.rejected = &stamp
	w.log.Error("the DoT names file could not be used, the current names stay in use", "err", err)
}

func (w *namesWatch) build(entries []dotnames.Entry) (*nameSet, error) {
	keys := make(map[string]struct{}, len(w.config)+len(entries))
	for _, name := range w.config {
		keys[serverNameKey(name)] = struct{}{}
	}
	for _, e := range entries {
		keys[serverNameKey(e.Host)] = struct{}{}
	}
	if len(keys) == 0 {
		return nil, errNoServedNames
	}
	set := &nameSet{keys: keys, fromFile: len(entries)}
	set.uncovered = w.uncoveredIn(set.hosts())
	return set, nil
}

func (n *nameSet) hosts() []string {
	hosts := make([]string, 0, len(n.keys))
	for key := range n.keys {
		hosts = append(hosts, key)
	}
	return hosts
}

func (w *namesWatch) hosts() []string {
	return w.cur.Load().hosts()
}

func (w *namesWatch) uncoveredIn(hosts []string) int {
	leaf := w.certs.cur.Load().leaf
	n := 0
	for _, host := range hosts {
		if leaf.VerifyHostname(host) != nil {
			n++
		}
	}
	return n
}

func (w *namesWatch) warnUncovered() {
	if n := w.uncoveredIn(w.hosts()); n > 0 {
		w.log.Warn("the DoT certificate does not cover some served names, clients that use them cannot connect", "uncovered", n)
	}
}

func (w *namesWatch) publish(set *nameSet) {
	w.cur.Store(set)
	w.log.Info("the DoT names were loaded", "names", len(set.keys), "from_file", set.fromFile)
	if set.uncovered > 0 {
		w.log.Warn("the DoT certificate does not cover some names from the names file, clients that use them cannot connect", "uncovered", set.uncovered)
	}
}

func sameStamp(a, b dotnames.Stamp) bool {
	return a.Size == b.Size && a.Time.Equal(b.Time)
}
