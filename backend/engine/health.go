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
	upstreamWarnAfter = 5

	upstreamWarnEvery = time.Minute
)

var ErrOverloaded = errors.New("the server is overloaded")

type upstreamHealth struct {
	failures atomic.Int64

	mu       sync.Mutex
	lastWarn time.Time
}

func (h *upstreamHealth) failed(err error, now time.Time, log *slog.Logger) {
	if errors.Is(err, context.Canceled) || errors.Is(err, ErrOverloaded) {
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
