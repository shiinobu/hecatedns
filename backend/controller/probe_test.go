package controller

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

const (
	probeName   = "abcdefghij0123456789.example.org"
	probeCanary = "canary.example.net"
)

type probeEnv struct {
	addr string
	pool *x509.CertPool
	stop func()
}

func probeAnswer(ips ...string) dns.HandlerFunc {
	return func(w dns.ResponseWriter, r *dns.Msg) {
		resp := new(dns.Msg).SetReply(r)
		for _, ip := range ips {
			resp.Answer = append(resp.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP(ip),
			})
		}
		_ = w.WriteMsg(resp)
	}
}

func startProbeDoT(t *testing.T, names []string, notAfter time.Time) probeEnv {
	t.Helper()
	certPath, keyPath := writeSelfSignedPair(t, t.TempDir(), names, notAfter)
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	pem, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13, NextProtos: []string{"dot"}})
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{Listener: ln, Handler: probeAnswer("93.184.216.34")}
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	var once sync.Once
	stop := func() { once.Do(func() { _ = srv.Shutdown() }) }
	t.Cleanup(stop)
	return probeEnv{addr: ln.Addr().String(), pool: pool, stop: stop}
}

func startProbeResolver(t *testing.T, rcode int, ips ...string) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	handler := probeAnswer(ips...)
	srv := &dns.Server{
		PacketConn: conn,
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
			if rcode == dns.RcodeSuccess {
				handler(w, r)
				return
			}
			_ = w.WriteMsg(new(dns.Msg).SetRcode(r, rcode))
		}),
	}
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })
	return conn.LocalAddr().String()
}

func closedTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func lookupReturning(addresses ...string) func(context.Context, string) ([]string, error) {
	return func(context.Context, string) ([]string, error) { return addresses, nil }
}

func probeOptionsFor(env probeEnv, resolvers ...string) probeOptions {
	return probeOptions{
		Name:      probeName,
		Addr:      env.addr,
		Resolvers: resolvers,
		Canary:    probeCanary,
		Timeout:   2 * time.Second,
		RootCAs:   env.pool,
		Lookup:    lookupReturning("127.0.0.1"),
	}
}

func stageOf(t *testing.T, stages []probeStage, name string) probeStage {
	t.Helper()
	for _, s := range stages {
		if s.Stage == name {
			return s
		}
	}
	t.Fatalf("no stage %q in %+v", name, stages)
	return probeStage{}
}

func TestProbe_AllStagesPassInOrder(t *testing.T) {
	env := startProbeDoT(t, []string{probeName}, time.Now().Add(90*24*time.Hour))
	resolver := startProbeResolver(t, dns.RcodeSuccess, "127.0.0.1")

	stages := probe(context.Background(), probeOptionsFor(env, resolver))

	var names []string
	for _, s := range stages {
		names = append(names, s.Stage)
		if !s.OK {
			t.Errorf("stage %q failed: %s", s.Stage, s.Detail)
		}
	}
	if want := []string{"resolve system", "resolve " + resolver, "tls", "query"}; strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("stages = %v, want %v", names, want)
	}
	if !probePassed(stages) {
		t.Error("the path did not pass")
	}
	if detail := stageOf(t, stages, "tls").Detail; !strings.Contains(detail, "TLS 1.3") || !strings.Contains(detail, "ALPN dot") || !strings.Contains(detail, "89 days") {
		t.Errorf("tls detail = %q", detail)
	}
	if detail := stageOf(t, stages, "query").Detail; !strings.Contains(detail, "NOERROR") || !strings.Contains(detail, "1 answer") {
		t.Errorf("query detail = %q", detail)
	}
}

func TestProbe_AFailingResolverIsReportedAndTheOthersStillRun(t *testing.T) {
	env := startProbeDoT(t, []string{probeName}, time.Now().Add(90*24*time.Hour))
	bad := startProbeResolver(t, dns.RcodeNameError)
	good := startProbeResolver(t, dns.RcodeSuccess, "127.0.0.1")

	stages := probe(context.Background(), probeOptionsFor(env, bad, good))

	if s := stageOf(t, stages, "resolve "+bad); s.OK || !strings.Contains(s.Detail, "NXDOMAIN") {
		t.Errorf("bad resolver stage = %+v", s)
	}
	if !stageOf(t, stages, "resolve "+good).OK || !stageOf(t, stages, "tls").OK || !stageOf(t, stages, "query").OK {
		t.Errorf("the other stages did not pass: %+v", stages)
	}
	if !probePassed(stages) {
		t.Error("the exit code must follow the system path, not the public resolvers")
	}
}

func TestProbe_AServFailIsReportedAsSuch(t *testing.T) {
	env := startProbeDoT(t, []string{probeName}, time.Now().Add(90*24*time.Hour))
	broken := startProbeResolver(t, dns.RcodeServerFailure)

	stages := probe(context.Background(), probeOptionsFor(env, broken))

	if s := stageOf(t, stages, "resolve "+broken); s.OK || !strings.Contains(s.Detail, "SERVFAIL") {
		t.Errorf("stage = %+v", s)
	}
}

func TestProbe_AFailingSystemResolverFailsThePathButTheOtherStagesRun(t *testing.T) {
	env := startProbeDoT(t, []string{probeName}, time.Now().Add(90*24*time.Hour))
	resolver := startProbeResolver(t, dns.RcodeSuccess, "127.0.0.1")
	opts := probeOptionsFor(env, resolver)
	opts.Lookup = func(_ context.Context, name string) ([]string, error) {
		return nil, errors.New("lookup " + name + ": no such host")
	}

	stages := probe(context.Background(), opts)

	system := stageOf(t, stages, "resolve system")
	if system.OK || !strings.Contains(system.Detail, "no such host") || strings.Contains(system.Detail, probeName) {
		t.Errorf("system stage = %+v", system)
	}
	if !stageOf(t, stages, "tls").OK || !stageOf(t, stages, "query").OK {
		t.Errorf("tls and query should still run: %+v", stages)
	}
	if probePassed(stages) {
		t.Error("the path passed although the system resolver failed")
	}
}

func TestProbe_AWrongServerNameFailsTheTLSStageAndSkipsTheQuery(t *testing.T) {
	env := startProbeDoT(t, []string{"other.example.org"}, time.Now().Add(90*24*time.Hour))

	stages := probe(context.Background(), probeOptionsFor(env))

	tlsStage := stageOf(t, stages, "tls")
	if tlsStage.OK || strings.Contains(tlsStage.Detail, probeName) {
		t.Errorf("tls stage = %+v", tlsStage)
	}
	if query := stageOf(t, stages, "query"); query.OK || !strings.Contains(query.Detail, "skipped") {
		t.Errorf("query stage = %+v", query)
	}
	if probePassed(stages) {
		t.Error("the path passed")
	}
}

func TestProbe_AnExpiredCertificateFailsTheTLSStageWithTheDate(t *testing.T) {
	notAfter := time.Now().Add(-48 * time.Hour)
	env := startProbeDoT(t, []string{probeName}, notAfter)

	stages := probe(context.Background(), probeOptionsFor(env))

	tlsStage := stageOf(t, stages, "tls")
	if tlsStage.OK || !strings.Contains(tlsStage.Detail, "expired") || !strings.Contains(tlsStage.Detail, notAfter.UTC().Format("2006-01-02")) {
		t.Errorf("tls stage = %+v", tlsStage)
	}
}

func TestProbe_NothingListeningFailsTheTLSStage(t *testing.T) {
	env := probeEnv{addr: closedTCPAddr(t), pool: x509.NewCertPool()}
	opts := probeOptionsFor(env)
	opts.Timeout = 300 * time.Millisecond

	stages := probe(context.Background(), opts)

	if s := stageOf(t, stages, "tls"); s.OK || s.Detail == "" {
		t.Errorf("tls stage = %+v", s)
	}
	if probePassed(stages) {
		t.Error("the path passed")
	}
}

func TestProbe_WithoutAnAddressTheTLSStageUsesTheSystemAnswerAndTheDoTPort(t *testing.T) {
	opts := probeOptions{Name: probeName, Canary: probeCanary, Timeout: 300 * time.Millisecond, Lookup: lookupReturning("127.0.0.1")}

	stages := probe(context.Background(), opts)

	if s := stageOf(t, stages, "tls"); s.OK || !strings.Contains(s.Detail, "127.0.0.1:853") {
		t.Errorf("tls stage = %+v, want an attempt on 127.0.0.1:853", s)
	}
}

func TestProbeVerdict_NamesTheLikelyCause(t *testing.T) {
	pass := func(name string) probeStage { return probeStage{Stage: name, OK: true} }
	fail := func(name string) probeStage { return probeStage{Stage: name, Detail: "x"} }
	cases := []struct {
		name   string
		stages []probeStage
		want   string
	}{
		{"all pass", []probeStage{pass("resolve system"), pass("tls"), pass("query")}, "PASS"},
		{"own resolver only", []probeStage{fail("resolve system"), pass("resolve 1.1.1.1:53"), pass("tls"), pass("query")}, "own resolver"},
		{"no resolver", []probeStage{fail("resolve system"), fail("resolve 1.1.1.1:53"), fail("tls"), fail("query")}, "no resolver"},
		{"tls", []probeStage{pass("resolve system"), fail("tls"), fail("query")}, "handshake"},
		{"query", []probeStage{pass("resolve system"), pass("tls"), fail("query")}, "did not answer"},
	}
	for _, tc := range cases {
		if got := probeVerdict(tc.stages); !strings.Contains(got, tc.want) {
			t.Errorf("%s: verdict %q does not contain %q", tc.name, got, tc.want)
		}
	}
}

func TestReadProbeName_UsesTheLastEntrySkipsCommentsAndTimes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.txt")
	text := "# the names\n\nfirstfirstfirstfirst.example.org 2026-10-20T10:00:00Z\n" + probeName + " 2026-10-21T00:10:00Z\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readProbeName(path)

	if err != nil || got != probeName {
		t.Errorf("name = %q, err = %v", got, err)
	}
	empty := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(empty, []byte("# nothing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProbeName(empty); err == nil {
		t.Error("a file without a name was accepted")
	}
}

func withSystemLookup(t *testing.T, fn func(context.Context, string) ([]string, error)) {
	t.Helper()
	old := systemLookup
	systemLookup = fn
	t.Cleanup(func() { systemLookup = old })
}

func TestRunProbe_NeverPrintsTheName(t *testing.T) {
	withSystemLookup(t, func(_ context.Context, name string) ([]string, error) {
		return nil, errors.New("lookup " + name + ": no such host")
	})
	dir := t.TempDir()
	nameFile := filepath.Join(dir, "name.txt")
	if err := os.WriteFile(nameFile, []byte(probeName+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := startProbeResolver(t, dns.RcodeNameError)
	var stdout, stderr bytes.Buffer

	code := Execute([]string{"probe", "-name-file", nameFile, "-addr", closedTCPAddr(t), "-resolvers", resolver, "-timeout", "300ms"}, &stdout, &stderr)

	if code != exitError {
		t.Errorf("exit code = %d, want %d\n%s", code, exitError, stdout.String())
	}
	all := stdout.String() + stderr.String()
	if strings.Contains(all, probeName) || strings.Contains(all, "abcdefghij0123456789") {
		t.Errorf("the output leaks the name:\n%s", all)
	}
	for _, want := range []string{"resolve system", "tls", "query", "result: FAIL", "<name>"} {
		if !strings.Contains(all, want) {
			t.Errorf("the output has no %q:\n%s", want, all)
		}
	}
}

func TestRunProbe_ReadsTheNewestServedNameFromTheConfig(t *testing.T) {
	withSystemLookup(t, lookupReturning())
	dir := t.TempDir()
	certPath, keyPath := writeSelfSignedPair(t, dir, []string{checkDoTName, probeName}, time.Now().Add(90*24*time.Hour))
	upstream := startTestDNSUpstream(t, false)
	configPath := dotCheckConfig(t, dir, upstream, certPath, keyPath)
	var stdout, stderr bytes.Buffer

	code := Execute([]string{"probe", "-config", configPath, "-addr", closedTCPAddr(t), "-resolvers", upstream, "-timeout", "300ms"}, &stdout, &stderr)

	if code != exitError {
		t.Errorf("exit code = %d\n%s%s", code, stdout.String(), stderr.String())
	}
	all := stdout.String() + stderr.String()
	if strings.Contains(all, checkDoTName) || strings.Contains(all, "k7x9p2qm4t8v3w6z") {
		t.Errorf("the output leaks the configured name:\n%s", all)
	}
	if !strings.Contains(all, "resolve system") {
		t.Errorf("the probe did not run:\n%s", all)
	}
}

func TestRunProbe_NeedsExactlyOneSourceOfTheName(t *testing.T) {
	for name, args := range map[string][]string{
		"none":    {"probe"},
		"both":    {"probe", "-config", "a.yaml", "-name-file", "b.txt"},
		"extra":   {"probe", "-name-file", "b.txt", "stray"},
		"no file": {"probe", "-name-file", filepath.Join(t.TempDir(), "missing.txt")},
	} {
		var stdout, stderr bytes.Buffer
		code := Execute(args, &stdout, &stderr)
		want := exitUsage
		if name == "no file" {
			want = exitError
		}
		if code != want || stderr.Len() == 0 {
			t.Errorf("%s: exit %d (want %d), stderr %q", name, code, want, stderr.String())
		}
	}
}

func TestUsage_ListsTheProbeCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	Execute(nil, &stdout, &stderr)

	if !strings.Contains(stderr.String(), "probe") {
		t.Errorf("usage = %q", stderr.String())
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func TestWatchProbe_PrintsOnlyWhenTheStateChanges(t *testing.T) {
	env := startProbeDoT(t, []string{probeName}, time.Now().Add(90*24*time.Hour))
	opts := probeOptionsFor(env)
	opts.Timeout = 300 * time.Millisecond
	var out lockedBuffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		watchProbe(ctx, opts, 30*time.Millisecond, time.Hour, &out)
		close(done)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(out.String(), "result: PASS") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	passBlocks := strings.Count(out.String(), "result: PASS")

	env.stop()
	for !strings.Contains(out.String(), "result: FAIL") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	if passBlocks != 1 {
		t.Errorf("an unchanged state was printed %d times, want once", passBlocks)
	}
	if !strings.Contains(out.String(), "result: FAIL") {
		t.Errorf("the change to a failure was not printed:\n%s", out.String())
	}
	if strings.Contains(out.String(), probeName) {
		t.Errorf("the watch output leaks the name:\n%s", out.String())
	}
}

func TestWatchProbe_PrintsAHeartbeatWhenNothingChanges(t *testing.T) {
	env := startProbeDoT(t, []string{probeName}, time.Now().Add(90*24*time.Hour))
	opts := probeOptionsFor(env)
	var out lockedBuffer
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	watchProbe(ctx, opts, 30*time.Millisecond, 100*time.Millisecond, &out)

	if got := strings.Count(out.String(), "still"); got < 2 {
		t.Errorf("heartbeats = %d, want at least 2:\n%s", got, out.String())
	}
}
