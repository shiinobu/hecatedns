package dashboard

import (
	"net/netip"
	"sync"
	"time"
)

const (
	loginFailuresPerSource = 5
	loginFailuresOverall   = 60
	loginFailureWindow     = time.Minute
	maxTrackedLoginSources = 4096
	ipv6SourceBits         = 64
)

type failWindow struct {
	start time.Time
	count int
}

func (w failWindow) wait(limit int, window time.Duration, now time.Time) time.Duration {
	end := w.start.Add(window)
	if w.count < limit || !now.Before(end) {
		return 0
	}
	return end.Sub(now)
}

func (w *failWindow) add(window time.Duration, now time.Time) {
	if w.count == 0 || !now.Before(w.start.Add(window)) {
		w.start, w.count = now, 0
	}
	w.count++
}

type loginLimiter struct {
	perSource  int
	overall    int
	maxSources int
	window     time.Duration

	mu        sync.Mutex
	sources   map[netip.Addr]*failWindow
	overflow  failWindow
	all       failWindow
	lastSweep time.Time
}

func newLoginLimiter(perSource, overall, maxSources int, window time.Duration) *loginLimiter {
	return &loginLimiter{
		perSource:  perSource,
		overall:    overall,
		maxSources: maxSources,
		window:     window,
		sources:    make(map[netip.Addr]*failWindow),
	}
}

func (l *loginLimiter) retryAfter(source netip.Addr, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	wait := l.all.wait(l.overall, l.window, now)
	if w, ok := l.sources[source]; ok {
		return max(wait, w.wait(l.perSource, l.window, now))
	}
	if len(l.sources) >= l.maxSources {
		return max(wait, l.overflow.wait(l.perSource, l.window, now))
	}
	return wait
}

func (l *loginLimiter) fail(source netip.Addr, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.all.add(l.window, now)
	l.sweepLocked(now)
	w, ok := l.sources[source]
	if !ok && len(l.sources) >= l.maxSources {
		l.overflow.add(l.window, now)
		return
	}
	if !ok {
		w = &failWindow{}
		l.sources[source] = w
	}
	w.add(l.window, now)
}

func (l *loginLimiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < l.window {
		return
	}
	l.lastSweep = now
	for source, w := range l.sources {
		if !now.Before(w.start.Add(l.window)) {
			delete(l.sources, source)
		}
	}
}

func (l *loginLimiter) tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.sources)
}

func sourceKey(remoteAddr string) netip.Addr {
	ap, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	addr := ap.Addr().Unmap()
	if addr.Is6() {
		return netip.PrefixFrom(addr, ipv6SourceBits).Masked().Addr()
	}
	return addr
}
