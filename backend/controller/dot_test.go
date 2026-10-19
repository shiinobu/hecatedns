package controller

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shiinobu/hecatedns/backend/core"
)

const checkDoTName = "k7x9p2qm4t8v3w6z.example.org"

func writeSelfSignedPair(t *testing.T, dir string, dnsNames []string, notAfter time.Time) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dnsNames[0]},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Hour),
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
	certPath = writeFile(t, dir, "cert.pem", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	keyPath = writeFile(t, dir, "key.pem", string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})))
	return certPath, keyPath
}

func dotCheckConfig(t *testing.T, dir, upstream, certPath, keyPath string) string {
	t.Helper()
	return writeFile(t, dir, "dot.yaml", fmt.Sprintf(`
backend: native
listen: []
allowed_clients: [0.0.0.0/0, "::/0"]
upstreams: [%q]
bootstrap: []
data_dir: data
dot:
  enabled: true
  listen: ["127.0.0.1:8853"]
  cert_file: %q
  key_file: %q
  names: [%q]
  reload_every: 10m
`, upstream, filepath.ToSlash(certPath), filepath.ToSlash(keyPath), checkDoTName))
}

func TestCheckLoadsTheDoTCertificateAndReportsItsExpiryWithoutTheNames(t *testing.T) {
	dir := t.TempDir()
	upstream := startTestDNSUpstream(t, false)
	certPath, keyPath := writeSelfSignedPair(t, dir, []string{"example.org", "*.example.org"}, time.Now().Add(60*24*time.Hour))
	cfg := dotCheckConfig(t, dir, upstream, certPath, keyPath)
	var stdout, stderr bytes.Buffer

	code := Execute([]string{"check", "-config", cfg}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	assertContains(t, "stdout", stdout.String(), []string{"dot certificate: PASSED", "self-check: PASSED"})
	if strings.Contains(stdout.String()+stderr.String(), "k7x9p2qm4t8v3w6z") {
		t.Error("the check output repeats a configured server name")
	}
}

func TestCheckFailsWhenTheDoTCertificateDoesNotCoverAName(t *testing.T) {
	dir := t.TempDir()
	upstream := startTestDNSUpstream(t, false)
	certPath, keyPath := writeSelfSignedPair(t, dir, []string{"other.example", "*.other.example"}, time.Now().Add(60*24*time.Hour))
	cfg := dotCheckConfig(t, dir, upstream, certPath, keyPath)
	var stdout, stderr bytes.Buffer

	code := Execute([]string{"check", "-config", cfg}, &stdout, &stderr)

	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	assertContains(t, "stderr", stderr.String(), []string{"dot.names[0]"})
	assertContains(t, "stdout", stdout.String(), []string{"dot certificate: FAILED"})
	if strings.Contains(stdout.String()+stderr.String(), "k7x9p2qm4t8v3w6z") {
		t.Error("the check output repeats a configured server name")
	}
}

func TestCheckFailsWhenTheDoTCertificateFilesAreMissing(t *testing.T) {
	dir := t.TempDir()
	upstream := startTestDNSUpstream(t, false)
	cfg := dotCheckConfig(t, dir, upstream, filepath.Join(dir, "none.pem"), filepath.Join(dir, "none.key"))
	var stdout, stderr bytes.Buffer

	code := Execute([]string{"check", "-config", cfg}, &stdout, &stderr)

	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	assertContains(t, "stdout", stdout.String(), []string{"dot certificate: FAILED"})
}

type fakeBound struct{ addrs map[string]net.Addr }

func (f fakeBound) Ready() <-chan struct{}       { return nil }
func (f fakeBound) Addr(network string) net.Addr { return f.addrs[network] }

func TestAnnounceReportsAServerThatOnlyServesDoT(t *testing.T) {
	bound := fakeBound{addrs: map[string]net.Addr{core.NetworkTLS: &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 853}}}
	var out bytes.Buffer

	announced := announce(&out, "native", bound)

	if !announced {
		t.Fatal("announce returned false for a server that listens on DoT")
	}
	assertContains(t, "stdout", out.String(), []string{"listening: tls 203.0.113.7:853", "press Ctrl+C to stop"})
}
