package native

import (
	"container/list"
	"math"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

const (
	bytesPerMB = 1 << 20

	entryOverhead = 128

	recordOverhead = 64

	minLifetime = time.Second
)

type cacheKey struct {
	name   string
	qtype  uint16
	qclass uint16
	cd     bool
	do     bool
}

type cacheEntry struct {
	key      cacheKey
	msg      *dns.Msg
	stored   time.Time
	lifetime time.Duration
	size     int
}

type cache struct {
	mu     sync.Mutex
	items  map[cacheKey]*list.Element
	order  *list.List
	used   int
	limit  int
	minTTL time.Duration
	maxTTL time.Duration
	now    func() time.Time
}

func newCache(cfg config.Cache) *cache { return newCacheAt(cfg, time.Now) }

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

func cacheLimit(mb int) int {
	switch {
	case mb <= 0:
		return 0
	case mb > math.MaxInt/bytesPerMB:
		return math.MaxInt
	}
	return mb * bytesPerMB
}

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

func (c *cache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func (c *cache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[cacheKey]*list.Element)
	c.order.Init()
	c.used = 0
}

func (c *cache) removeLocked(el *list.Element) {
	e := c.order.Remove(el).(*cacheEntry)
	delete(c.items, e.key)
	c.used -= e.size
}

func keyFor(req *dns.Msg) (cacheKey, bool) {
	if req == nil || len(req.Question) != 1 {
		return cacheKey{}, false
	}
	q := req.Question[0]
	key := cacheKey{name: dns.CanonicalName(q.Name), qtype: q.Qtype, qclass: q.Qclass, cd: req.CheckingDisabled}
	if opt := req.IsEdns0(); opt != nil {
		key.do = opt.Do()
	}
	return key, true
}

func answersQuestion(req, resp *dns.Msg) bool {
	if len(resp.Question) != 1 {
		return false
	}
	a, b := req.Question[0], resp.Question[0]
	return a.Qtype == b.Qtype && a.Qclass == b.Qclass && dns.CanonicalName(a.Name) == dns.CanonicalName(b.Name)
}

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

func negativeTTL(ns []dns.RR) (uint32, bool) {
	for _, rr := range ns {
		if soa, ok := rr.(*dns.SOA); ok {
			return min(soa.Hdr.Ttl, soa.Minttl), true
		}
	}
	return 0, false
}

func withoutOPT(rrs []dns.RR) []dns.RR {
	out := make([]dns.RR, 0, len(rrs))
	for _, rr := range rrs {
		if rr.Header().Rrtype != dns.TypeOPT {
			out = append(out, rr)
		}
	}
	return out
}

func setTTL(m *dns.Msg, ttl uint32) {
	for _, section := range [][]dns.RR{m.Answer, m.Ns, m.Extra} {
		for _, rr := range section {
			if rr.Header().Rrtype != dns.TypeOPT {
				rr.Header().Ttl = ttl
			}
		}
	}
}
