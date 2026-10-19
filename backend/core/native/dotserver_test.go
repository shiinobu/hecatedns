package native

import (
	"crypto/tls"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

func (e *dotEnv) dial(t *testing.T) *dns.Conn {
	t.Helper()
	conn, err := tls.Dial("tcp", e.addr(t), &tls.Config{RootCAs: e.pki.pool, ServerName: dotName, NextProtos: []string{"dot"}})
	if err != nil {
		t.Fatalf("DoT dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &dns.Conn{Conn: conn}
}

func TestDoT_AnswersPipelinedQueriesAsTheyFinish(t *testing.T) {
	const delay = 800 * time.Millisecond
	up := startFakeDNS(t, func(req *dns.Msg, _ string) *dns.Msg {
		if strings.HasPrefix(req.Question[0].Name, "slow.") {
			time.Sleep(delay)
		}
		return aReply(req)
	})
	env := startDoT(t, func(c *config.Config) { c.Upstreams = []string{up.addr} })
	conn := env.dial(t)
	slow, fast := queryFor("slow.example", dns.TypeA), queryFor("fast.example", dns.TypeA)
	slow.Id, fast.Id = 1, 2
	start := time.Now()
	for _, q := range []*dns.Msg{slow, fast} {
		if err := conn.WriteMsg(q); err != nil {
			t.Fatal(err)
		}
	}

	first, err := conn.ReadMsg()
	if err != nil {
		t.Fatal(err)
	}
	firstAfter := time.Since(start)
	second, err := conn.ReadMsg()
	if err != nil {
		t.Fatal(err)
	}

	if first.Id != fast.Id || second.Id != slow.Id {
		t.Errorf("answers came in the order %d, %d; want the fast query (%d) first, then the slow one (%d)", first.Id, second.Id, fast.Id, slow.Id)
	}
	if firstAfter >= delay/2 {
		t.Errorf("the fast answer took %v behind a slow query, want well under %v", firstAfter, delay/2)
	}
}
