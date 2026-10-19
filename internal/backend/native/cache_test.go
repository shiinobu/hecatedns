package native

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/internal/config"
)

// fakeClock is a settable time source for cache tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func testCache(clock *fakeClock, mutate func(*config.Cache)) *cache {
	cfg := config.Cache{Enabled: true, SizeMB: 1, MaxTTL: time.Hour}
	if mutate != nil {
		mutate(&cfg)
	}
	return newCacheAt(cfg, clock.Now)
}

func queryFor(name string, qtype uint16) *dns.Msg {
	req := new(dns.Msg)
	req.SetQuestion(dns.Fqdn(name), qtype)
	req.Id = 4242
	return req
}

// answerFor builds an upstream answer for req holding one A record.
func answerFor(req *dns.Msg, ttl uint32) *dns.Msg {
	resp := new(dns.Msg).SetReply(req)
	resp.Answer = []dns.RR{&dns.A{
		Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl},
		A:   net.IPv4(192, 0, 2, 1),
	}}
	return resp
}

// negativeFor builds an answer without records and with an SOA in the
// authority section.
func negativeFor(req *dns.Msg, rcode int, soaTTL, minimum uint32) *dns.Msg {
	resp := new(dns.Msg).SetRcode(req, rcode)
	resp.Ns = []dns.RR{&dns.SOA{
		Hdr:     dns.RR_Header{Name: "example.", Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: soaTTL},
		Ns:      "ns.example.",
		Mbox:    "hostmaster.example.",
		Minttl:  minimum,
		Refresh: 1, Retry: 1, Expire: 1,
	}}
	return resp
}

func TestCache_MissThenHit(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, nil)
	req := queryFor("fine.example", dns.TypeA)

	if _, ok := c.get(req); ok {
		t.Fatal("an empty cache reported a hit")
	}
	c.put(req, answerFor(req, 300))

	again := queryFor("fine.example", dns.TypeA)
	again.Id = 7
	got, ok := c.get(again)
	if !ok {
		t.Fatal("a stored answer was not found")
	}
	if got.Id != 7 {
		t.Errorf("answer ID = %d, want the ID of the new request, 7", got.Id)
	}
	if len(got.Answer) != 1 || got.Answer[0].(*dns.A).A.String() != "192.0.2.1" {
		t.Errorf("answer = %v, want the stored A record", got.Answer)
	}
}

func TestCache_KeyIgnoresCaseAndEchoesTheClientsSpelling(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, nil)
	req := queryFor("fine.example", dns.TypeA)
	c.put(req, answerFor(req, 300))

	mixed := queryFor("FiNe.ExAmPlE", dns.TypeA)
	got, ok := c.get(mixed)
	if !ok {
		t.Fatal("a query that differs only in letter case missed the cache")
	}
	if got.Question[0].Name != "FiNe.ExAmPlE." {
		t.Errorf("question name = %q, want the client's own spelling", got.Question[0].Name)
	}
}

func TestCache_KeySeparatesTypeClassAndDNSSECBit(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, nil)
	req := queryFor("fine.example", dns.TypeA)
	c.put(req, answerFor(req, 300))

	if _, ok := c.get(queryFor("fine.example", dns.TypeAAAA)); ok {
		t.Error("an AAAA query was answered from the A entry")
	}
	chaos := queryFor("fine.example", dns.TypeA)
	chaos.Question[0].Qclass = dns.ClassCHAOS
	if _, ok := c.get(chaos); ok {
		t.Error("a CHAOS class query was answered from the IN entry")
	}
	withDO := queryFor("fine.example", dns.TypeA)
	withDO.SetEdns0(1232, true)
	if _, ok := c.get(withDO); ok {
		t.Error("a query with the DO bit was answered from an entry stored for a plain query")
	}
}

func TestCache_TTLCountsDownAndEntryExpires(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, nil)
	req := queryFor("fine.example", dns.TypeA)
	c.put(req, answerFor(req, 300))

	clock.Advance(100 * time.Second)
	got, ok := c.get(req)
	if !ok {
		t.Fatal("entry missing after 100 s of a 300 s TTL")
	}
	if ttl := got.Answer[0].Header().Ttl; ttl != 200 {
		t.Errorf("TTL after 100 s = %d, want 200", ttl)
	}

	clock.Advance(200 * time.Second)
	if _, ok := c.get(req); ok {
		t.Error("entry still served at the end of its TTL")
	}
	if n := c.len(); n != 0 {
		t.Errorf("expired entry still held: %d entries", n)
	}
}

func TestCache_NeverServesAZeroTTL(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, nil)
	req := queryFor("fine.example", dns.TypeA)
	c.put(req, answerFor(req, 300))

	clock.Advance(299*time.Second + 500*time.Millisecond)
	got, ok := c.get(req)
	if ok && got.Answer[0].Header().Ttl == 0 {
		t.Error("served an answer whose TTL reads 0")
	}
}

func TestCache_MaxTTLCapsTheLifetime(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, func(cfg *config.Cache) { cfg.MaxTTL = time.Minute })
	req := queryFor("fine.example", dns.TypeA)
	c.put(req, answerFor(req, 3600))

	got, _ := c.get(req)
	if ttl := got.Answer[0].Header().Ttl; ttl != 60 {
		t.Errorf("TTL = %d, want it capped at 60", ttl)
	}
	clock.Advance(61 * time.Second)
	if _, ok := c.get(req); ok {
		t.Error("entry outlived max_ttl")
	}
}

func TestCache_MinTTLRaisesTheLifetime(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, func(cfg *config.Cache) { cfg.MinTTL = 30 * time.Second })
	req := queryFor("fine.example", dns.TypeA)
	c.put(req, answerFor(req, 5))

	clock.Advance(20 * time.Second)
	got, ok := c.get(req)
	if !ok {
		t.Fatal("entry gone before min_ttl")
	}
	if ttl := got.Answer[0].Header().Ttl; ttl != 10 {
		t.Errorf("TTL = %d, want 10 (30 s minimum minus 20 s)", ttl)
	}
}

func TestCache_ZeroTTLIsNotStored(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, nil)
	req := queryFor("fine.example", dns.TypeA)
	c.put(req, answerFor(req, 0))

	if _, ok := c.get(req); ok {
		t.Error("an answer with TTL 0 was cached")
	}
}

func TestCache_UsesTheSmallestTTLOfTheAnswer(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, nil)
	req := queryFor("fine.example", dns.TypeA)
	resp := answerFor(req, 300)
	resp.Answer = append([]dns.RR{&dns.CNAME{
		Hdr:    dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 20},
		Target: "target.example.",
	}}, resp.Answer...)
	c.put(req, resp)

	clock.Advance(21 * time.Second)
	if _, ok := c.get(req); ok {
		t.Error("entry outlived the shortest record of its answer")
	}
}

func TestCache_NegativeAnswersLiveForTheSOAMinimum(t *testing.T) {
	cases := []struct {
		name         string
		rcode        int
		soaTTL, min  uint32
		wantLifetime time.Duration
	}{
		{"NXDOMAIN uses the smaller of SOA TTL and minimum", dns.RcodeNameError, 900, 60, 60 * time.Second},
		{"NODATA uses the smaller of SOA TTL and minimum", dns.RcodeSuccess, 30, 600, 30 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := newFakeClock()
			c := testCache(clock, nil)
			req := queryFor("gone.example", dns.TypeA)
			c.put(req, negativeFor(req, tc.rcode, tc.soaTTL, tc.min))

			clock.Advance(tc.wantLifetime - time.Second)
			got, ok := c.get(req)
			if !ok {
				t.Fatalf("negative answer missing %v before its lifetime ends", time.Second)
			}
			if got.Rcode != tc.rcode {
				t.Errorf("rcode = %s, want %s", dns.RcodeToString[got.Rcode], dns.RcodeToString[tc.rcode])
			}
			clock.Advance(2 * time.Second)
			if _, ok := c.get(req); ok {
				t.Errorf("negative answer outlived %v", tc.wantLifetime)
			}
		})
	}
}

func TestCache_NegativeAnswerWithoutSOAIsNotStored(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, nil)
	req := queryFor("gone.example", dns.TypeA)
	c.put(req, new(dns.Msg).SetRcode(req, dns.RcodeNameError))

	if _, ok := c.get(req); ok {
		t.Error("an NXDOMAIN without an SOA was cached, so its lifetime was a guess")
	}
}

func TestCache_OnlyStoresDefinitiveAnswers(t *testing.T) {
	cases := map[string]func(*dns.Msg) *dns.Msg{
		"SERVFAIL":  func(req *dns.Msg) *dns.Msg { return new(dns.Msg).SetRcode(req, dns.RcodeServerFailure) },
		"REFUSED":   func(req *dns.Msg) *dns.Msg { return new(dns.Msg).SetRcode(req, dns.RcodeRefused) },
		"truncated": func(req *dns.Msg) *dns.Msg { r := answerFor(req, 300); r.Truncated = true; return r },
		"not a response": func(req *dns.Msg) *dns.Msg {
			r := answerFor(req, 300)
			r.Response = false
			return r
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			c := testCache(newFakeClock(), nil)
			req := queryFor("fine.example", dns.TypeA)
			c.put(req, build(req))
			if _, ok := c.get(req); ok {
				t.Errorf("a %s answer was cached", name)
			}
		})
	}
}

func TestCache_RefusesAnAnswerToAnotherQuestion(t *testing.T) {
	c := testCache(newFakeClock(), nil)
	req := queryFor("fine.example", dns.TypeA)
	other := queryFor("other.example", dns.TypeA)
	c.put(req, answerFor(other, 300))

	if _, ok := c.get(req); ok {
		t.Error("an answer to a different question was stored under this question")
	}
}

func TestCache_StoresACopy(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, nil)
	req := queryFor("fine.example", dns.TypeA)
	resp := answerFor(req, 300)
	c.put(req, resp)

	resp.Answer[0].Header().Ttl = 1
	resp.Answer[0].(*dns.A).A[0] = 99
	first, _ := c.get(req)
	first.Answer[0].(*dns.A).A[0] = 77
	first.Answer[0].Header().Ttl = 5

	second, ok := c.get(req)
	if !ok {
		t.Fatal("entry missing")
	}
	a := second.Answer[0].(*dns.A)
	if a.A.String() != "192.0.2.1" || a.Hdr.Ttl != 300 {
		t.Errorf("cached record = %s ttl %d, want the original 192.0.2.1 ttl 300", a.A, a.Hdr.Ttl)
	}
}

func TestCache_DropsTheUpstreamsOPTRecord(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, nil)
	req := queryFor("fine.example", dns.TypeA)
	resp := answerFor(req, 300)
	resp.SetEdns0(1232, false)
	c.put(req, resp)

	got, _ := c.get(req)
	if got.IsEdns0() != nil {
		t.Error("the stored answer still carries the upstream's OPT record; each client gets its own")
	}
}

func TestCache_EvictsTheLeastRecentlyUsedWhenFull(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, nil)
	c.limit = 4 * entrySize(t, c, "name0.example")

	for i := 0; i < 4; i++ {
		req := queryFor(fmt.Sprintf("name%d.example", i), dns.TypeA)
		c.put(req, answerFor(req, 300))
	}
	// Touch name0 so name1 becomes the oldest.
	if _, ok := c.get(queryFor("name0.example", dns.TypeA)); !ok {
		t.Fatal("name0 missing before the cache is full")
	}
	extra := queryFor("name4.example", dns.TypeA)
	c.put(extra, answerFor(extra, 300))

	if _, ok := c.get(queryFor("name1.example", dns.TypeA)); ok {
		t.Error("the least recently used entry survived")
	}
	for _, name := range []string{"name0.example", "name2.example", "name3.example", "name4.example"} {
		if _, ok := c.get(queryFor(name, dns.TypeA)); !ok {
			t.Errorf("%s was evicted although it is newer", name)
		}
	}
	if c.used > c.limit {
		t.Errorf("cache holds %d bytes, over its %d byte limit", c.used, c.limit)
	}
}

func TestCache_RefusesAnEntryLargerThanTheWholeCache(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, nil)
	c.limit = 10
	req := queryFor("fine.example", dns.TypeA)
	c.put(req, answerFor(req, 300))

	if c.len() != 0 || c.used != 0 {
		t.Errorf("cache holds %d entries / %d bytes, want an empty cache", c.len(), c.used)
	}
}

func TestCache_ReplacingAnEntryKeepsTheByteCountExact(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, nil)
	req := queryFor("fine.example", dns.TypeA)
	c.put(req, answerFor(req, 300))
	before := c.used
	c.put(req, answerFor(req, 600))

	if c.len() != 1 || c.used != before {
		t.Errorf("after replacing: %d entries / %d bytes, want 1 entry / %d bytes", c.len(), c.used, before)
	}
}

func TestCache_SizeFollowsConfig(t *testing.T) {
	c := testCache(newFakeClock(), func(cfg *config.Cache) { cfg.SizeMB = 3 })
	if c.limit != 3<<20 {
		t.Errorf("limit = %d, want %d", c.limit, 3<<20)
	}
}

func TestCache_DisabledCacheIsSafeToCall(t *testing.T) {
	c := newCache(config.Cache{Enabled: false, SizeMB: 1})
	req := queryFor("fine.example", dns.TypeA)
	c.put(req, answerFor(req, 300))
	if _, ok := c.get(req); ok {
		t.Error("a disabled cache returned a hit")
	}
}

func TestCache_ConcurrentUse(t *testing.T) {
	c := newCache(config.Cache{Enabled: true, SizeMB: 1, MaxTTL: time.Hour})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				req := queryFor(fmt.Sprintf("name%d.example", (w*31+i)%64), dns.TypeA)
				if got, ok := c.get(req); ok {
					got.Answer[0].Header().Ttl = 1
					continue
				}
				c.put(req, answerFor(req, 300))
			}
		}(w)
	}
	wg.Wait()
	if c.used > c.limit {
		t.Errorf("cache holds %d bytes, over its %d byte limit", c.used, c.limit)
	}
}

// entrySize measures what one entry costs in the cache under test, so a test
// can size the cache to hold an exact number of them.
func entrySize(t *testing.T, c *cache, name string) int {
	t.Helper()
	req := queryFor(name, dns.TypeA)
	c.put(req, answerFor(req, 300))
	size := c.used
	if size == 0 {
		t.Fatal("measuring entry size: nothing was stored")
	}
	c.clear()
	return size
}

func TestCache_KeySeparatesCheckingDisabled(t *testing.T) {
	clock := newFakeClock()
	c := testCache(clock, nil)
	cd := queryFor("fine.example", dns.TypeA)
	cd.CheckingDisabled = true
	c.put(cd, answerFor(cd, 300))

	if _, ok := c.get(queryFor("fine.example", dns.TypeA)); ok {
		t.Error("a query with checking enabled was served an answer fetched with checking disabled")
	}
	if _, ok := c.get(cd); !ok {
		t.Error("the query that stored the answer missed its own entry")
	}
}
