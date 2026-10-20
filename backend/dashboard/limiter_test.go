package dashboard

import (
	"strconv"
	"testing"
	"time"
)

var limiterStart = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

func failN(l *loginLimiter, remote string, n int, now time.Time) {
	for range n {
		l.fail(sourceKey(remote), now)
	}
}

func TestLoginLimiter_PerSourceAndIPv6Prefix(t *testing.T) {
	l := newLoginLimiter(5, 60, 4096, time.Minute)
	failN(l, "192.0.2.1:1", 4, limiterStart)
	if wait := l.retryAfter(sourceKey("192.0.2.1:2"), limiterStart); wait != 0 {
		t.Fatalf("limited after 4 failures: %v", wait)
	}
	failN(l, "192.0.2.1:1", 1, limiterStart)
	if wait := l.retryAfter(sourceKey("192.0.2.1:9"), limiterStart); wait != time.Minute {
		t.Fatalf("want a one minute wait, got %v", wait)
	}
	if wait := l.retryAfter(sourceKey("192.0.2.2:1"), limiterStart); wait != 0 {
		t.Fatalf("another source limited: %v", wait)
	}
	failN(l, "[2001:db8::1]:53", 5, limiterStart)
	if l.retryAfter(sourceKey("[2001:db8::ffff:1]:1"), limiterStart) == 0 {
		t.Fatal("same /64 not limited")
	}
	if l.retryAfter(sourceKey("[2001:db8:0:1::1]:1"), limiterStart) != 0 {
		t.Fatal("another /64 limited")
	}
	if sourceKey("nonsense") != sourceKey("") || sourceKey("[::ffff:192.0.2.1]:1") != sourceKey("192.0.2.1:2") {
		t.Fatal("source keys are not normalised")
	}
}

func TestLoginLimiter_GlobalCap(t *testing.T) {
	l := newLoginLimiter(5, 60, 4096, time.Minute)
	for i := range 12 {
		failN(l, "192.0.2."+strconv.Itoa(i)+":1", 5, limiterStart)
	}
	if wait := l.retryAfter(sourceKey("198.51.100.1:1"), limiterStart.Add(10*time.Second)); wait != 50*time.Second {
		t.Fatalf("global cap not applied: %v", wait)
	}
}

func TestLoginLimiter_WindowResetsWithClock(t *testing.T) {
	l := newLoginLimiter(5, 60, 4096, time.Minute)
	failN(l, "192.0.2.1:1", 5, limiterStart)
	if wait := l.retryAfter(sourceKey("192.0.2.1:1"), limiterStart.Add(59*time.Second)); wait != time.Second {
		t.Fatalf("want one second left, got %v", wait)
	}
	if wait := l.retryAfter(sourceKey("192.0.2.1:1"), limiterStart.Add(time.Minute)); wait != 0 {
		t.Fatalf("window did not reset: %v", wait)
	}
}

func TestLoginLimiter_BoundedSourcesShareOverflow(t *testing.T) {
	l := newLoginLimiter(5, 1000, 2, time.Minute)
	failN(l, "192.0.2.1:1", 1, limiterStart)
	failN(l, "192.0.2.2:1", 1, limiterStart)
	failN(l, "192.0.2.3:1", 5, limiterStart)
	if l.retryAfter(sourceKey("192.0.2.4:1"), limiterStart) == 0 {
		t.Fatal("overflow sources do not share a bucket")
	}
	if l.retryAfter(sourceKey("192.0.2.1:1"), limiterStart) != 0 || l.tracked() != 2 {
		t.Fatalf("tracked %d", l.tracked())
	}
	failN(l, "192.0.2.5:1", 1, limiterStart.Add(2*time.Minute))
	if l.tracked() != 1 {
		t.Fatalf("expired sources not forgotten: %d", l.tracked())
	}
}
