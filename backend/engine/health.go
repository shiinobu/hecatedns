package engine

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// upstreamWarnAfter is how many forwarded queries must fail in a row
	// before the engine says so. A single timeout is normal; five in a row
	// with no answer in between means the upstreams or the network are down.
	upstreamWarnAfter = 5

	// upstreamWarnEvery is the shortest time between two warnings, so a dead
	// upstream cannot flood the log.
	upstreamWarnEvery = time.Minute
)

// upstreamHealth counts forwarded queries that failed in a row. Both backends
// report to the engine through After and Failed, so the count and the warning
// are the same for both.
type upstreamHealth struct {
	failures atomic.Int64

	mu       sync.Mutex
	lastWarn time.Time
}

// failed counts one failed forward and warns when the run is long enough. A
// query the client gave up on is not the upstreams' fault and is ignored.
func (h *upstreamHealth) failed(err error, now time.Time, log *slog.Logger) {
	if errors.Is(err, context.Canceled) {
		return
	}
	n := h.failures.Add(1)
	if n < upstreamWarnAfter {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.lastWarn.IsZero() && now.Sub(h.lastWarn) < upstreamWarnEvery {
		return
	}
	h.lastWarn = now
	attrs := []any{"consecutive_failures", n}
	if err != nil {
		attrs = append(attrs, "last_error", err.Error())
	}
	log.Warn("upstream servers keep failing, clients get SERVFAIL", attrs...)
}

// answered ends the run of failures; if the run was long enough to have been
// reported, it says that the upstreams are back.
func (h *upstreamHealth) answered(log *slog.Logger) {
	if h.failures.Load() == 0 {
		return
	}
	n := h.failures.Swap(0)
	if n < upstreamWarnAfter {
		return
	}
	h.mu.Lock()
	h.lastWarn = time.Time{}
	h.mu.Unlock()
	log.Info("upstream servers answer again", "failures_in_a_row", n)
}
