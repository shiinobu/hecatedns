package certstore

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type testPair struct {
	certPEM, keyPEM []byte
	notAfter        time.Time
}

func issue(t *testing.T, names []string, notAfter time.Time) testPair {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		NotBefore:    time.Now().Add(-2 * time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return testPair{
		certPEM:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		keyPEM:   pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		notAfter: tmpl.NotAfter,
	}
}

func writeFile(t *testing.T, path string, data []byte, stamp time.Time) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func writePair(t *testing.T, p Pair, tp testPair, stamp time.Time) {
	t.Helper()
	writeFile(t, p.CertFile, tp.certPEM, stamp)
	writeFile(t, p.KeyFile, tp.keyPEM, stamp)
}

func pairIn(dir, name string) Pair {
	return Pair{CertFile: filepath.Join(dir, name+".crt"), KeyFile: filepath.Join(dir, name+".key")}
}

func newLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(buf, nil)), buf
}

func servedName(t *testing.T, s *Store, sni string) string {
	t.Helper()
	cert, err := s.GetCertificate(&tls.ClientHelloInfo{ServerName: sni})
	if err != nil {
		t.Fatalf("GetCertificate(%q): %v", sni, err)
	}
	return cert.Leaf.DNSNames[0]
}

func TestGetCertificate_PicksPairBySNI(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Now().Add(-time.Hour)
	first, second := pairIn(dir, "a"), pairIn(dir, "b")
	writePair(t, first, issue(t, []string{"dash.example.org"}, time.Now().Add(48*time.Hour)), stamp)
	writePair(t, second, issue(t, []string{"*.ghost.example.org"}, time.Now().Add(24*time.Hour)), stamp)
	log, _ := newLogger()
	s, err := New(Config{Pairs: []Pair{first, second}, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	if got := servedName(t, s, "dash.example.org"); got != "dash.example.org" {
		t.Fatalf("exact: got %s", got)
	}
	if got := servedName(t, s, "A.Ghost.Example.ORG"); got != "*.ghost.example.org" {
		t.Fatalf("wildcard: got %s", got)
	}
	for _, sni := range []string{"", "other.example.org", "a.b.ghost.example.org", "ghost.example.org"} {
		if _, err := s.GetCertificate(&tls.ClientHelloInfo{ServerName: sni}); err == nil {
			t.Fatalf("sni %q: expected an error", sni)
		}
	}
	if s.NotAfter().After(time.Now().Add(25 * time.Hour)) {
		t.Fatalf("NotAfter should be the earliest pair, got %s", s.NotAfter())
	}
}

func TestNotAfterFor_ReportsTheCoveringPairOnly(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Now().Add(-time.Hour)
	first, second := pairIn(dir, "a"), pairIn(dir, "b")
	soon := issue(t, []string{"dash.example.org"}, time.Now().Add(24*time.Hour))
	later := issue(t, []string{"*.ghost.example.org"}, time.Now().Add(72*time.Hour))
	writePair(t, first, soon, stamp)
	writePair(t, second, later, stamp)
	log, _ := newLogger()
	s, err := New(Config{Pairs: []Pair{first, second}, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := s.NotAfterFor("dash.example.org"); !ok || !got.Equal(soon.notAfter.Truncate(time.Second)) {
		t.Fatalf("exact: got %s, %v", got, ok)
	}
	if got, ok := s.NotAfterFor("A.Ghost.Example.ORG"); !ok || !got.Equal(later.notAfter.Truncate(time.Second)) {
		t.Fatalf("wildcard: got %s, %v", got, ok)
	}
	for _, name := range []string{"", "other.example.org", "ghost.example.org"} {
		if _, ok := s.NotAfterFor(name); ok {
			t.Fatalf("name %q: expected no certificate", name)
		}
	}
}

func TestNew_RejectsMissingAndBadPairs(t *testing.T) {
	dir := t.TempDir()
	bad := pairIn(dir, "bad")
	writeFile(t, bad.CertFile, []byte("garbage"), time.Now())
	writeFile(t, bad.KeyFile, []byte("garbage"), time.Now())
	if _, err := New(Config{}); err == nil {
		t.Fatal("no pairs: expected an error")
	}
	_, err := New(Config{Pairs: []Pair{bad, pairIn(dir, "missing")}})
	if err == nil || !strings.Contains(err.Error(), "certs[0]") || !strings.Contains(err.Error(), "certs[1]") {
		t.Fatalf("expected both problems reported, got %v", err)
	}
}

func TestCheck_KeepsOldPairOnGarbageAndAcceptsGoodPair(t *testing.T) {
	dir := t.TempDir()
	p := pairIn(dir, "a")
	base := time.Now().Add(-time.Hour)
	writePair(t, p, issue(t, []string{"dash.example.org"}, time.Now().Add(24*time.Hour)), base)
	log, buf := newLogger()
	s, err := New(Config{Pairs: []Pair{p}, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	old := s.NotAfter()
	writeFile(t, p.CertFile, []byte("garbage"), base.Add(time.Minute))
	for range 3 {
		s.Check()
	}
	if got := servedName(t, s, "dash.example.org"); got != "dash.example.org" || !s.NotAfter().Equal(old) {
		t.Fatal("the old pair must keep serving after a bad reload")
	}
	if n := strings.Count(buf.String(), "level=WARN"); n != 1 {
		t.Fatalf("expected one warning for one problem, got %d:\n%s", n, buf.String())
	}
	next := issue(t, []string{"dash.example.org"}, time.Now().Add(72*time.Hour))
	writePair(t, p, next, base.Add(2*time.Minute))
	s.Check()
	if s.NotAfter().Equal(old) {
		t.Fatal("a good new pair must be swapped in")
	}
}

func TestStore_ConcurrentGetAndReload(t *testing.T) {
	dir := t.TempDir()
	p := pairIn(dir, "a")
	base := time.Now().Add(-time.Hour)
	pairs := []testPair{
		issue(t, []string{"dash.example.org"}, time.Now().Add(24*time.Hour)),
		issue(t, []string{"dash.example.org"}, time.Now().Add(48*time.Hour)),
	}
	writePair(t, p, pairs[0], base)
	log, _ := newLogger()
	s, err := New(Config{Pairs: []Pair{p}, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for ctx.Err() == nil {
				if _, err := s.GetCertificate(&tls.ClientHelloInfo{ServerName: "dash.example.org"}); err != nil {
					t.Error(err)
					return
				}
				_ = s.NotAfter()
			}
		})
	}
	for i := range 40 {
		writePair(t, p, pairs[i%2], base.Add(time.Duration(i+1)*time.Second))
		s.Check()
	}
	cancel()
	wg.Wait()
}

func TestWatch_StopsOnCancel(t *testing.T) {
	dir := t.TempDir()
	p := pairIn(dir, "a")
	writePair(t, p, issue(t, []string{"dash.example.org"}, time.Now().Add(24*time.Hour)), time.Now().Add(-time.Hour))
	s, err := New(Config{Pairs: []Pair{p}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Watch(ctx, time.Millisecond)
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not stop")
	}
}
