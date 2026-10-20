package dashboard

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/certstore"
)

const (
	testHost  = "zq8w4kd7lm2nx5vc.ghost.example.org"
	otherHost = "aaaabbbbccccdddd.ghost.example.net"
	testProbe = "ghost.example.org"
)

func testAssets() fstest.MapFS {
	return fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><title>hecate</title>")},
		"app.js":     {Data: []byte("console.log(1)")},
		"app.css":    {Data: []byte("body{}")},
	}
}

func loopback() []netip.AddrPort {
	return []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:0")}
}

func testStore(t *testing.T) (*certstore.Store, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(7),
		Subject:               pkix.Name{CommonName: "*." + testProbe},
		DNSNames:              []string{"*." + testProbe},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pair := certstore.Pair{CertFile: filepath.Join(dir, "c.pem"), KeyFile: filepath.Join(dir, "k.pem")}
	writes := map[string][]byte{
		pair.CertFile: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pair.KeyFile:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}
	for path, data := range writes {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := certstore.New(certstore.Config{Pairs: []certstore.Pair{pair}})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return store, pool
}

func newTestServer(t *testing.T, mut func(*Options)) *Server {
	t.Helper()
	opts := Options{
		Listen:       loopback(),
		Names:        []string{testHost},
		ProbeDomains: []string{testProbe},
		Assets:       testAssets(),
		API: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := io.WriteString(w, `{"ok":true}`); err != nil {
				t.Error(err)
			}
		}),
		Log: slog.New(slog.DiscardHandler),
	}
	if mut != nil {
		mut(&opts)
	}
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func runServer(t *testing.T, s *Server) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(cancel)
	return cancel, done
}

func rawRequest(t *testing.T, conn net.Conn, host string) (*http.Response, error) {
	t.Helper()
	return rawGet(t, conn, host, "/")
}

func rawGet(t *testing.T, conn net.Conn, host, path string) (*http.Response, error) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: "+host+"\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	return http.ReadResponse(bufio.NewReader(conn), nil)
}

func TestNew_ReportsAllProblems(t *testing.T) {
	if _, err := New(Options{}); err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("expected a listen error, got %v", err)
	}
	_, err := New(Options{
		Listen:       []netip.AddrPort{netip.MustParseAddrPort("0.0.0.0:443")},
		Names:        []string{"Bad_Host"},
		ProbeDomains: []string{"x; script-src *"},
		Assets:       fstest.MapFS{"app.js": {Data: []byte("x")}},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"0.0.0.0:443", "names[0]", "probe_domains[0]", "index.html"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestRun_TLSOnlyHTTP11AndKnownSNI(t *testing.T) {
	store, pool := testStore(t)
	s := newTestServer(t, func(o *Options) { o.Certs = store })
	cancel, done := runServer(t, s)
	addr := s.Addrs()[0].String()
	dial := func(cfg *tls.Config) (*tls.Conn, error) {
		cfg.RootCAs = pool
		return tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", addr, cfg)
	}
	conn, err := dial(&tls.Config{ServerName: testHost, NextProtos: []string{"h2", "http/1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	state := conn.ConnectionState()
	if state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != "http/1.1" {
		t.Fatalf("got version %x proto %q", state.Version, state.NegotiatedProtocol)
	}
	resp, err := rawRequest(t, conn, testHost)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Strict-Transport-Security") != "max-age=31536000" {
		t.Fatalf("status %d, hsts %q", resp.StatusCode, resp.Header.Get("Strict-Transport-Security"))
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := dial(&tls.Config{ServerName: testHost, MaxVersion: tls.VersionTLS12}); err == nil {
		t.Fatal("a TLS 1.2 client must be refused")
	}
	if _, err := dial(&tls.Config{ServerName: otherHost}); err == nil {
		t.Fatal("an SNI outside names must be refused")
	}
	waitFor(t, func() bool { return s.Counters().UnknownSNI == 1 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRun_HostAllowListClosesWithoutBody(t *testing.T) {
	s := newTestServer(t, nil)
	runServer(t, s)
	addr := s.Addrs()[0]
	port := addr.Port()
	for _, host := range []string{testHost, "127.0.0.1:" + itoa(port), "localhost:" + itoa(port)} {
		conn := dialPlain(t, addr)
		resp, err := rawRequest(t, conn, host)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("host %q: %v %v", host, resp, err)
		}
	}
	conn := dialPlain(t, addr)
	if _, err := rawRequest(t, conn, "evil.example.org"); err == nil {
		t.Fatal("an unknown host must get no response")
	}
	waitFor(t, func() bool { return s.Counters().UnknownHost == 1 })
}

func TestRun_RefusesConnectionsOverCap(t *testing.T) {
	s := newTestServer(t, nil)
	runServer(t, s)
	addr := s.Addrs()[0]
	for range defaultMaxConns {
		dialPlain(t, addr)
	}
	extra := dialPlain(t, addr)
	if err := extra.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err := extra.Read(make([]byte, 1))
	var ne net.Error
	if err == nil || (errors.As(err, &ne) && ne.Timeout()) {
		t.Fatalf("the connection over the cap must be closed, got %v", err)
	}
	waitFor(t, func() bool { return s.Counters().RejectedConns == 1 })
}

func dialPlain(t *testing.T, addr netip.AddrPort) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr.String(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func itoa(port uint16) string {
	return strconv.Itoa(int(port))
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
