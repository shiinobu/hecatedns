package native

import (
	"container/list"
	"math"
	"sync"
	"time"

	"github.com/miekg/dns"

	"xdns/internal/config"
)

const (
	bytesPerMB = 1 << 20

	// entryOverhead is the estimated cost of an entry beyond its packed message:
	// the map slot, the list element, the key and the Msg struct.
	entryOverhead = 128

	// recordOverhead is the estimated heap cost of one resource record beyond its
	// packed size (the interface value, the header, the name string). The packed
	// length alone would let a cache full of small records use several times its
	// configured size.
	recordOverhead = 64

	// minLifetime is the shortest remaining life an entry is still served with.
	// A smaller value would show up as TTL 0, which clients read as "do not use".
	minLifetime = time.Second
)

// cacheKey identifies an answer: the lower-cased question plus the DNSSEC OK
// bit, because a client that asks for DNSSEC records must not be served an
// answer stored without them.
type cacheKey struct {
	name   string
	qtype  uint16
	qclass uint16
	do     bool
}

// cacheEntry is one stored answer. msg is never modified after it is stored, so
// readers copy it outside the lock.
type cacheEntry struct {
	key      cacheKey
	msg      *dns.Msg
	stored   time.Time
	lifetime time.Duration
	size     int
}

// cache is a TTL-aware answer cache with a byte limit and least-recently-used
// eviction. It holds the UPSTREAM answer, unmodified by the engine, so the
// engine's After runs on every hit. A nil *cache is a disabled cache.
type cache struct {
	mu     sync.Mutex
	items  map[cacheKey]*list.Element
	order  *list.List // front is the most recently used
	used   int
	limit  int
	minTTL time.Duration
	maxTTL time.Duration
	now    func() time.Time
}

// newCache builds the cache from the configuration; it returns nil when the
// cache is disabled.
func newCache(cfg config.Cache) *cache { return newCacheAt(cfg, time.Now) }

// newCacheAt is newCache with an injectable clock for tests.
func newCacheAt(cfg config.Cache, now func() time.Time) *cache {
	if !cfg.Enabled {
		return nil
	}
	return &cache{
		items:  make(map[cacheKey]*list.Element),
		order:  list.New(),
		limit:  cacheLimit(cfg.SizeMB),
		minTTL: cfg.MinTTL,
		maxTTL: cfg.MaxTTL,
		now:    now,
	}
}

// cacheLimit converts megabytes to bytes without overflowing int.
func cacheLimit(mb int) int {
	switch {
	case mb <= 0:
		return 0
	case mb > math.MaxInt/bytesPerMB:
		return math.MaxInt
	}
	return mb * bytesPerMB
}

// get returns a private copy of the stored answer for req, with the ID of req,
// the question spelled as the client wrote it, and every TTL set to the life
// the entry has left. It reports false for a miss, which includes an entry that
// has just expired.
func (c *cache) get(req *dns.Msg) (*dns.Msg, bool) {
	if c == nil {
		return nil, false
	}
	key, ok := keyFor(req)
	if !ok {
		return nil, false
	}
	c.mu.Lock()
	el, found := c.items[key]
	if !found {
		c.mu.Unlock()
		return nil, false
	}
	e := el.Value.(*cacheEntry)
	remaining := e.lifetime - c.now().Sub(e.stored)
	if remaining < minLifetime {
		c.removeLocked(el)
		c.mu.Unlock()
		return nil, false
	}
	c.order.MoveToFront(el)
	c.mu.Unlock()

	out := e.msg.Copy()
	out.Id = req.Id
	out.Question = append([]dns.Question(nil), req.Question...)
	setTTL(out, uint32(remaining/time.Second))
	return out, true
}

// put stores resp as the answer to req if it is worth keeping: a NOERROR or
// NXDOMAIN answer that is not truncated, matches the question and has a
// lifetime. It stores a copy.
func (c *cache) put(req, resp *dns.Msg) {
	if c == nil || resp == nil {
		return
	}
	key, ok := keyFor(req)
	if !ok || !answersQuestion(req, resp) {
		return
	}
	life, ok := c.lifetime(resp)
	if !ok {
		return
	}
	msg := resp.Copy()
	msg.Extra = withoutOPT(msg.Extra)
	records := len(msg.Answer) + len(msg.Ns) + len(msg.Extra)
	size := msg.Len() + len(key.name) + entryOverhead + records*recordOverhead
	e := &cacheEntry{key: key, msg: msg, stored: c.now(), lifetime: life, size: size}

	c.mu.Lock()
	defer c.mu.Unlock()
	if old, found := c.items[key]; found {
		c.removeLocked(old)
	}
	if e.size > c.limit {
		return
	}
	for c.used+e.size > c.limit {
		c.removeLocked(c.order.Back())
	}
	c.items[key] = c.order.PushFront(e)
	c.used += e.size
}

// lifetime reports how long resp may be served: the smallest TTL of its records
// (for a negative answer the SOA TTL or minimum, whichever is smaller, RFC 2308),
// clamped to the configured bounds.
func (c *cache) lifetime(resp *dns.Msg) (time.Duration, bool) {
	if !resp.Response || resp.Truncated {
		return 0, false
	}
	if resp.Rcode != dns.RcodeSuccess && resp.Rcode != dns.RcodeNameError {
		return 0, false
	}
	var seconds uint32
	var ok bool
	if len(resp.Answer) > 0 {
		seconds, ok = smallestTTL(resp.Answer, resp.Ns), true
	} else {
		seconds, ok = negativeTTL(resp.Ns)
	}
	if !ok {
		return 0, false
	}
	d := time.Duration(seconds) * time.Second
	if c.maxTTL > 0 && d > c.maxTTL {
		d = c.maxTTL
	}
	if d < c.minTTL {
		d = c.minTTL
	}
	return d, d >= minLifetime
}

// len reports the number of stored entries.
func (c *cache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// clear drops every entry.
func (c *cache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[cacheKey]*list.Element)
	c.order.Init()
	c.used = 0
}

// removeLocked removes one list element; the caller holds c.mu.
func (c *cache) removeLocked(el *list.Element) {
	e := c.order.Remove(el).(*cacheEntry)
	delete(c.items, e.key)
	c.used -= e.size
}

// keyFor derives the cache key; a message without exactly one question has none.
func keyFor(req *dns.Msg) (cacheKey, bool) {
	if req == nil || len(req.Question) != 1 {
		return cacheKey{}, false
	}
	q := req.Question[0]
	key := cacheKey{name: dns.CanonicalName(q.Name), qtype: q.Qtype, qclass: q.Qclass}
	if opt := req.IsEdns0(); opt != nil {
		key.do = opt.Do()
	}
	return key, true
}

// answersQuestion reports whether resp repeats the single question of req.
func answersQuestion(req, resp *dns.Msg) bool {
	if len(resp.Question) != 1 {
		return false
	}
	a, b := req.Question[0], resp.Question[0]
	return a.Qtype == b.Qtype && a.Qclass == b.Qclass && dns.CanonicalName(a.Name) == dns.CanonicalName(b.Name)
}

// smallestTTL returns the smallest TTL among the records of the given sections.
func smallestTTL(sections ...[]dns.RR) uint32 {
	smallest := uint32(math.MaxUint32)
	for _, section := range sections {
		for _, rr := range section {
			if ttl := rr.Header().Ttl; ttl < smallest {
				smallest = ttl
			}
		}
	}
	return smallest
}

// negativeTTL returns the lifetime of an answer without records, taken from the
// SOA in its authority section; without an SOA there is nothing to go by.
func negativeTTL(ns []dns.RR) (uint32, bool) {
	for _, rr := range ns {
		if soa, ok := rr.(*dns.SOA); ok {
			return min(soa.Hdr.Ttl, soa.Minttl), true
		}
	}
	return 0, false
}

// withoutOPT returns the records without any OPT pseudo-record. It builds a new
// slice and leaves the argument alone.
func withoutOPT(rrs []dns.RR) []dns.RR {
	out := make([]dns.RR, 0, len(rrs))
	for _, rr := range rrs {
		if rr.Header().Rrtype != dns.TypeOPT {
			out = append(out, rr)
		}
	}
	return out
}

// setTTL sets the TTL of every record of m except the OPT pseudo-record, where
// the same field carries the extended rcode and flags.
func setTTL(m *dns.Msg, ttl uint32) {
	for _, section := range [][]dns.RR{m.Answer, m.Ns, m.Extra} {
		for _, rr := range section {
			if rr.Header().Rrtype != dns.TypeOPT {
				rr.Header().Ttl = ttl
			}
		}
	}
}
