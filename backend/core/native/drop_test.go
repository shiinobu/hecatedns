package native

import (
	"log/slog"
	"net/netip"
	"strings"
	"testing"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

func countWarnings(l *logSink, part string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, r := range l.recs {
		if r.level == slog.LevelWarn && strings.Contains(r.text, part) {
			n++
		}
	}
	return n
}

func TestDrop_EveryReasonGetsItsOwnWarning(t *testing.T) {
	logs := &logSink{}
	h := newHandler(&fakeFilter{}, newCache(config.Cache{Enabled: false, SizeMB: 1}), nil, newLimiter(), slog.New(logs))
	client := netip.MustParseAddr("192.0.2.7")

	h.drop("unknown tls server name", client)
	h.drop("rate limit", client)
	h.drop("too many upstream queries in flight", client)

	for _, reason := range []string{"unknown tls server name", "rate limit", "too many upstream queries in flight"} {
		if got := countWarnings(logs, "reason="+reason); got != 1 {
			t.Errorf("warnings for %q = %d, want 1: scanner noise must not hide another reason", reason, got)
		}
	}
}

func TestDrop_ARepeatedReasonIsLoggedOnceInTheInterval(t *testing.T) {
	logs := &logSink{}
	h := newHandler(&fakeFilter{}, newCache(config.Cache{Enabled: false, SizeMB: 1}), nil, newLimiter(), slog.New(logs))
	client := netip.MustParseAddr("192.0.2.7")

	for range 50 {
		h.drop("rate limit", client)
	}

	if got := countWarnings(logs, "reason=rate limit"); got != 1 {
		t.Errorf("warnings = %d, want 1 within the interval", got)
	}
}
