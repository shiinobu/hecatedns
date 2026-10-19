package native

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

const (
	dotBase = "hecatedns.duckdns.org"
	dotName = "k7x9p2qm4t8v3w6z." + dotBase
)

type testPKI struct {
	pool *x509.CertPool
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "hecate test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testPKI{pool: pool, cert: cert, key: key}
}

func (p *testPKI) issue(t *testing.T, serial int64, dnsNames []string, notAfter time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: dnsNames[0]},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.cert, &key.PublicKey, p.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func writePair(t *testing.T, dir string, certPEM, keyPEM []byte) (certPath, keyPath string) {
	t.Helper()
	certPath, keyPath = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

type logRecord struct {
	level slog.Level
	text  string
}

type logSink struct {
	mu   sync.Mutex
	recs []logRecord
}

func (l *logSink) Enabled(context.Context, slog.Level) bool { return true }
func (l *logSink) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *logSink) WithGroup(string) slog.Handler            { return l }
func (l *logSink) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value.Any())
		return true
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recs = append(l.recs, logRecord{level: r.Level, text: b.String()})
	return nil
}

func (l *logSink) has(level slog.Level, part string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.recs {
		if r.level == level && strings.Contains(r.text, part) {
			return true
		}
	}
	return false
}

func (l *logSink) anywhere(part string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.recs {
		if strings.Contains(r.text, part) {
			return true
		}
	}
	return false
}

func eventually(t *testing.T, within time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type dotEnv struct {
	s        *server
	pki      *testPKI
	filter   *fakeFilter
	logs     *logSink
	certPath string
	keyPath  string
}

func (e *dotEnv) addr(t *testing.T) string {
	t.Helper()
	a := e.s.Addr("tls")
	if a == nil {
		t.Fatal("the server reports no DoT address")
	}
	return a.String()
}

func (e *dotEnv) client(serverName string) *dns.Client {
	return &dns.Client{
		Net:     "tcp-tls",
		Timeout: testTimeout,
		TLSConfig: &tls.Config{
			RootCAs:            e.pki.pool,
			ServerName:         serverName,
			InsecureSkipVerify: serverName == "" || strings.ToUpper(serverName) == serverName,
		},
	}
}

func (e *dotEnv) servedSerial(t *testing.T) int64 {
	t.Helper()
	conn, err := tls.Dial("tcp", e.addr(t), &tls.Config{RootCAs: e.pki.pool, ServerName: dotName})
	if err != nil {
		return 0
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0].SerialNumber.Int64()
}

func dotConfigFor(certPath, keyPath, upstream string) config.Config {
	cfg := config.Default()
	cfg.Backend = config.BackendNative
	cfg.Listen = nil
	cfg.Bootstrap = nil
	cfg.Upstreams = []string{upstream}
	cfg.AllowedClients = []string{"0.0.0.0/0", "::/0"}
	cfg.DoT = config.DoT{
		Enabled:     true,
		Listen:      []string{"127.0.0.1:0"},
		CertFile:    certPath,
		KeyFile:     keyPath,
		Names:       []string{dotName},
		ReloadEvery: 20 * time.Millisecond,
	}
	return cfg
}

func newDoTEnv(t *testing.T, notAfter time.Time, mutate func(*config.Config)) (*dotEnv, error) {
	t.Helper()
	pki := newTestPKI(t)
	certPEM, keyPEM := pki.issue(t, 1, []string{dotBase, "*." + dotBase}, notAfter)
	certPath, keyPath := writePair(t, t.TempDir(), certPEM, keyPEM)
	cfg := dotConfigFor(certPath, keyPath, goodDNS(t).addr)
	if mutate != nil {
		mutate(&cfg)
	}
	env := &dotEnv{pki: pki, filter: &fakeFilter{}, logs: &logSink{}, certPath: certPath, keyPath: keyPath}
	s, err := newServer(cfg, env.filter, slog.New(env.logs), testTimeout)
	if err != nil {
		return nil, err
	}
	env.s = s
	return env, nil
}

func startDoT(t *testing.T, mutate func(*config.Config)) *dotEnv {
	t.Helper()
	return startDoTWith(t, mutate, nil)
}

func startDoTWith(t *testing.T, mutate func(*config.Config), tune func(*dotLimits)) *dotEnv {
	t.Helper()
	env, err := newDoTEnv(t, time.Now().Add(60*24*time.Hour), mutate)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	if tune != nil {
		tune(&env.s.dotLimits)
	}
	runServer(t, env.s)
	return env
}
