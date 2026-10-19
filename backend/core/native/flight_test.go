package native

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/engine"
)

const joinSettle = 150 * time.Millisecond

type gatedUpstream struct {
	release chan struct{}
	reply   func(q *dns.Msg) (*dns.Msg, error)

	mu    sync.Mutex
	calls int
}

func newGatedUpstream(reply func(q *dns.Msg) (*dns.Msg, error)) *gatedUpstream {
	return &gatedUpstream{release: make(chan struct{}), reply: reply}
}

func (u *gatedUpstream) Exchange(ctx context.Context, q *dns.Msg) (*dns.Msg, string, error) {
	u.mu.Lock()
	u.calls++
	u.mu.Unlock()
	select {
	case <-u.release:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	resp, err := u.reply(q)
	return resp, "gated-upstream", err
}

func (u *gatedUpstream) callCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

func (u *gatedUpstream) waitForFirstCall(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for u.callCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the upstream was never asked")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(joinSettle)
}

type asker struct {
	req *dns.Msg
	w   *fakeWriter
}

func askAll(h *handler, wg *sync.WaitGroup, count int, build func(i int) (*dns.Msg, string)) []asker {
	out := make([]asker, count)
	for i := range out {
		req, ip := build(i)
		out[i] = asker{req: req, w: udpWriter(ip)}
		wg.Go(func() { h.ServeDNS(out[i].w, out[i].req) })
	}
	return out
}

func sameQuestionFromManyClients(name string, qtype uint16) func(i int) (*dns.Msg, string) {
	return func(i int) (*dns.Msg, string) {
		req := queryFor(name, qtype)
		req.Id = uint16(1000 + i)
		return req, fmt.Sprintf("192.168.1.%d", 10+i)
	}
}

func TestHandler_IdenticalQueriesInFlightShareOneUpstreamExchange(t *testing.T) {
	up := newGatedUpstream(func(q *dns.Msg) (*dns.Msg, error) { return aReply(q), nil })
	h := newTestHandler(&fakeFilter{}, up)
	var wg sync.WaitGroup
	askers := askAll(h, &wg, 5, sameQuestionFromManyClients("shared.example", dns.TypeA))

	up.waitForFirstCall(t)
	close(up.release)
	wg.Wait()

	if got := up.callCount(); got != 1 {
		t.Errorf("the upstream was asked %d times for 5 identical queries, want 1", got)
	}
	for i, a := range askers {
		resp := a.w.only(t)
		if resp.Id != a.req.Id || resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
			t.Errorf("client %d got %v, want an answer carrying its own ID %d", i, resp, a.req.Id)
		}
	}
	if h.gate.total != 0 || len(h.gate.perClient) != 0 {
		t.Errorf("gate holds %d slots for %d clients after the queries ended, want none", h.gate.total, len(h.gate.perClient))
	}
}

func TestHandler_ClientsThatShareAnExchangeEachGetTheirOwnCopyOfTheAnswer(t *testing.T) {
	up := newGatedUpstream(func(q *dns.Msg) (*dns.Msg, error) { return aReply(q), nil })
	f := &fakeFilter{after: func(q engine.Query, resp *dns.Msg, _ engine.Meta) engine.Decision {
		resp.Answer[0].Header().Ttl = uint32(q.Client.As4()[3])
		return engine.Decision{Verdict: engine.Pass}
	}}
	h := newTestHandler(f, up)
	var wg sync.WaitGroup
	askers := askAll(h, &wg, 5, sameQuestionFromManyClients("copy.example", dns.TypeA))

	up.waitForFirstCall(t)
	close(up.release)
	wg.Wait()

	for i, a := range askers {
		if got, want := a.w.only(t).Answer[0].Header().Ttl, uint32(10+i); got != want {
			t.Errorf("client %d got TTL %d, want %d set by its own After call; the answer was shared between clients", i, got, want)
		}
	}
}

func TestHandler_QueriesThatDifferInTypeOrDNSSECFlagsDoNotShareAnExchange(t *testing.T) {
	up := newGatedUpstream(func(q *dns.Msg) (*dns.Msg, error) { return aReply(q), nil })
	h := newTestHandler(&fakeFilter{}, up)
	var wg sync.WaitGroup
	askAll(h, &wg, 4, func(i int) (*dns.Msg, string) {
		qtype := dns.TypeA
		if i == 1 {
			qtype = dns.TypeAAAA
		}
		req := queryFor("flags.example", qtype)
		switch i {
		case 2:
			req.SetEdns0(4096, true)
		case 3:
			req.CheckingDisabled = true
		}
		return req, fmt.Sprintf("192.168.1.%d", 10+i)
	})

	deadline := time.Now().Add(3 * time.Second)
	for up.callCount() < 4 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	got := up.callCount()
	close(up.release)
	wg.Wait()

	if got != 4 {
		t.Errorf("the upstream was asked %d times for 4 different questions, want 4", got)
	}
}

func TestHandler_AFailedSharedExchangeFailsEveryWaiterAndIsLoggedForEach(t *testing.T) {
	up := newGatedUpstream(func(*dns.Msg) (*dns.Msg, error) { return nil, errors.New("upstream down") })
	f := &fakeFilter{}
	h := newTestHandler(f, up)
	var wg sync.WaitGroup
	askers := askAll(h, &wg, 5, sameQuestionFromManyClients("down.example", dns.TypeA))

	up.waitForFirstCall(t)
	close(up.release)
	wg.Wait()

	if got := up.callCount(); got != 1 {
		t.Errorf("the upstream was asked %d times, want 1", got)
	}
	for i, a := range askers {
		if got := a.w.only(t).Rcode; got != dns.RcodeServerFailure {
			t.Errorf("client %d: rcode %s, want SERVFAIL", i, dns.RcodeToString[got])
		}
	}
	f.mu.Lock()
	failed := len(f.failed)
	f.mu.Unlock()
	if failed != 5 {
		t.Errorf("Failed was called %d times, want once per client so every query is logged", failed)
	}
}

func TestHandler_WaitersGiveUpWhenTheirDeadlineEnds(t *testing.T) {
	up := newGatedUpstream(func(q *dns.Msg) (*dns.Msg, error) { return aReply(q), nil })
	h := newTestHandler(&fakeFilter{}, up)
	h.timeout = 100 * time.Millisecond
	var wg sync.WaitGroup
	askers := askAll(h, &wg, 5, sameQuestionFromManyClients("slow.example", dns.TypeA))

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("queries waiting for a shared exchange outlived their deadline")
	}

	if got := up.callCount(); got != 1 {
		t.Errorf("the upstream was asked %d times, want 1", got)
	}
	for i, a := range askers {
		if got := a.w.only(t).Rcode; got != dns.RcodeServerFailure {
			t.Errorf("client %d: rcode %s, want SERVFAIL", i, dns.RcodeToString[got])
		}
	}
}

type panickingUpstream struct{}

func (panickingUpstream) Exchange(context.Context, *dns.Msg) (*dns.Msg, string, error) {
	panic("upstream blew up")
}

func TestHandler_ALeaderThatPanicsReleasesItsWaitersAndLeavesNoFlightBehind(t *testing.T) {
	h := newTestHandler(&fakeFilter{}, panickingUpstream{})
	req := queryFor("boom.example", dns.TypeA)
	key, _ := keyFor(req)
	f, leader := h.flights.lead(key)
	if !leader {
		t.Fatal("the first caller was not the leader")
	}
	waited := make(chan error, 1)
	go func() {
		_, _, err := f.wait(context.Background())
		waited <- err
	}()

	func() {
		defer func() { _ = recover() }()
		_, _, _ = h.lead(context.Background(), req, key, f)
	}()

	select {
	case err := <-waited:
		if !errors.Is(err, errFlightAborted) {
			t.Errorf("waiter error = %v, want %v", err, errFlightAborted)
		}
	case <-time.After(time.Second):
		t.Fatal("a waiter stayed blocked after its leader panicked")
	}
	if h.flights.get(key) != nil {
		t.Error("the flight of a panicked leader is still registered, so the next identical query would wait for nothing")
	}
}

func TestFlightWait_ReturnsAtTheCallersOwnDeadlineWhileTheLeaderIsStillRunning(t *testing.T) {
	g := newFlights()
	f, _ := g.lead(cacheKey{})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()

	_, _, err := f.wait(ctx)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("wait error = %v, want the caller's deadline", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("wait took %v, want it to end at the caller's 50ms deadline", took)
	}
}
