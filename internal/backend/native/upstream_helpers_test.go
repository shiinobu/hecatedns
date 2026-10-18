package native

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

const testTimeout = 2 * time.Second

// fakeDNS is a plain DNS server on one loopback port, reachable over UDP and
// TCP. Its handler decides every answer; it also keeps what it was asked.
type fakeDNS struct {
	addr string

	mu   sync.Mutex
	asks []dns.Question
	nets []string
}

func (f *fakeDNS) asked() []dns.Question {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]dns.Question(nil), f.asks...)
}

func (f *fakeDNS) networks() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.nets...)
}

// startFakeDNS listens on the same loopback port for UDP and TCP. handler gets
// the request and the network it arrived on.
func startFakeDNS(t *testing.T, handler func(req *dns.Msg, network string) *dns.Msg) *fakeDNS {
	t.Helper()
	// A UDP socket whose TCP twin cannot be bound is kept open until the test
	// ends: closing it would hand the same port to the next attempt.
	var skipped []net.PacketConn
	t.Cleanup(func() {
		for _, pc := range skipped {
			_ = pc.Close()
		}
	})
	for attempt := 0; attempt < 100; attempt++ {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("fake DNS: listen udp: %v", err)
		}
		ln, err := net.Listen("tcp", pc.LocalAddr().String())
		if err != nil {
			skipped = append(skipped, pc)
			continue
		}
		f := &fakeDNS{addr: pc.LocalAddr().String()}
		serve := func(network string) dns.HandlerFunc {
			return func(w dns.ResponseWriter, req *dns.Msg) {
				f.mu.Lock()
				f.asks = append(f.asks, req.Question...)
				f.nets = append(f.nets, network)
				f.mu.Unlock()
				resp := handler(req, network)
				if resp == nil {
					return // say nothing: a timeout for the client
				}
				_ = w.WriteMsg(resp)
			}
		}
		started := make(chan struct{}, 2)
		notify := func() { started <- struct{}{} }
		udp := &dns.Server{PacketConn: pc, Handler: serve("udp"), NotifyStartedFunc: notify}
		tcp := &dns.Server{Listener: ln, Handler: serve("tcp"), NotifyStartedFunc: notify}
		go func() { _ = udp.ActivateAndServe() }()
		go func() { _ = tcp.ActivateAndServe() }()
		<-started
		<-started
		t.Cleanup(func() { _ = udp.Shutdown(); _ = tcp.Shutdown() })
		return f
	}
	t.Fatal("fake DNS: no loopback port was free for both UDP and TCP")
	return nil
}

// aReply answers req with one A record for 192.0.2.1.
func aReply(req *dns.Msg) *dns.Msg {
	resp := new(dns.Msg).SetReply(req)
	if len(req.Question) == 1 {
		resp.Answer = []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.IPv4(192, 0, 2, 1),
		}}
	}
	return resp
}

// goodDNS is a fake server that always answers correctly.
func goodDNS(t *testing.T) *fakeDNS {
	t.Helper()
	return startFakeDNS(t, func(req *dns.Msg, _ string) *dns.Msg { return aReply(req) })
}

// fakeDoH is a TLS server speaking DNS over HTTPS on loopback.
type fakeDoH struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*dns.Msg
	methods  []string
}

func (f *fakeDoH) seen() ([]*dns.Msg, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*dns.Msg(nil), f.requests...), append([]string(nil), f.methods...)
}

// pool returns a certificate pool that trusts the server.
func (f *fakeDoH) trust() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(f.Certificate())
	return pool
}

// port returns the port the server listens on.
func (f *fakeDoH) port() string {
	_, port, _ := net.SplitHostPort(f.Listener.Addr().String())
	return port
}

// startFakeDoH serves /dns-query. reply builds the HTTP answer from the decoded
// request; it may write anything, so tests can break the protocol.
func startFakeDoH(t *testing.T, reply func(w http.ResponseWriter, r *http.Request, req *dns.Msg)) *fakeDoH {
	t.Helper()
	f := &fakeDoH{}
	f.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 70000))
		req := new(dns.Msg)
		if err := req.Unpack(body); err != nil {
			http.Error(w, "bad message", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, req)
		f.methods = append(f.methods, r.Method+" "+r.Header.Get("Content-Type"))
		f.mu.Unlock()
		reply(w, r, req)
	}))
	f.Server.EnableHTTP2 = true
	f.Server.StartTLS()
	t.Cleanup(f.Close)
	return f
}

// writeDNS sends msg as a DNS-over-HTTPS answer.
func writeDNS(w http.ResponseWriter, msg *dns.Msg) {
	wire, err := msg.Pack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/dns-message")
	_, _ = w.Write(wire)
}

// newTestDoH builds a DoH upstream for url that trusts only the fake server.
func newTestDoH(t *testing.T, f *fakeDoH, rawURL string, boot *bootstrap) *dohUpstream {
	t.Helper()
	tlsCfg := &tls.Config{RootCAs: f.trust(), MinVersion: tls.VersionTLS12}
	u, err := newDoH(rawURL, boot, testTimeout, tlsCfg)
	if err != nil {
		t.Fatalf("newDoH(%q): %v", rawURL, err)
	}
	t.Cleanup(func() { _ = u.Close() })
	return u
}

// loopbackURL is the DoH endpoint of f addressed by IP, as the default
// upstreams (https://1.1.1.1/dns-query) are.
func loopbackURL(f *fakeDoH) string {
	return "https://127.0.0.1:" + f.port() + "/dns-query"
}

// queryMsg builds a query message for name and type with a fixed ID.
func queryMsg(name string, qtype uint16) *dns.Msg {
	q := new(dns.Msg)
	q.SetQuestion(dns.Fqdn(name), qtype)
	q.Id = 31337
	q.RecursionDesired = true
	return q
}
