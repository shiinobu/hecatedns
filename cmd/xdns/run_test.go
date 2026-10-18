package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"xdns/internal/backend"
	"xdns/internal/engine"
	"xdns/internal/qlog"
)

// fakeBackend stands in for a real DNS server: no sockets, it only exercises
// the wiring that `xdns run` builds around it.
type fakeBackend struct {
	name  string
	deps  backend.Deps
	ready chan struct{}
	up    atomic.Bool
	once  sync.Once
	// serve runs between "ready" and "ctx done"; its error ends Run early.
	serve func(deps backend.Deps) error
}

func (f *fakeBackend) Name() string { return f.name }

func (f *fakeBackend) Ready() <-chan struct{} { return f.ready }

func (f *fakeBackend) Addr(network string) net.Addr {
	if !f.up.Load() {
		return nil
	}
	switch network {
	case backend.NetworkUDP:
		return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5300}
	case backend.NetworkTCP:
		return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5300}
	}
	return nil
}

func (f *fakeBackend) Run(ctx context.Context) error {
	defer f.once.Do(func() { close(f.ready) })
	if f.serve != nil {
		if err := f.serve(f.deps); err != nil {
			return err
		}
	}
	f.up.Store(true)
	f.once.Do(func() { close(f.ready) })
	<-ctx.Done()
	return nil
}

func fakeFactory(name string, serve func(backend.Deps) error) backend.Factory {
	return func(deps backend.Deps) (backend.Backend, error) {
		return &fakeBackend{name: name, deps: deps, ready: make(chan struct{}), serve: serve}, nil
	}
}

type runFixture struct {
	dir    string
	config string
	rules  string
	logDir string
}

func newRunFixture(t *testing.T, extraYAML string) runFixture {
	t.Helper()
	dir := t.TempDir()
	yaml := "rules:\n  file: rules.txt\n  poll_interval: 50ms\nlog:\n  dir: logs\n" + extraYAML
	return runFixture{
		dir:    dir,
		config: writeFile(t, dir, "xdns.yaml", yaml),
		rules:  filepath.Join(dir, "rules.txt"),
		logDir: filepath.Join(dir, "logs"),
	}
}

// start runs `xdns run` in a goroutine and returns a function that stops it
// (cancelling the context) and reports its exit code and output.
func (f runFixture) start(t *testing.T, registry backend.Registry, args ...string) *runHandle {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var out, errOut safeBuffer
	done := make(chan int, 1)
	go func() {
		done <- runServerCtx(ctx, append([]string{"-config", f.config}, args...), &out, &errOut, registry)
	}()
	stop := func() (int, string, string) {
		cancel()
		select {
		case code := <-done:
			return code, out.String(), errOut.String()
		case <-time.After(10 * time.Second):
			t.Fatal("xdns run did not stop after its context was cancelled")
			return 0, "", ""
		}
	}
	t.Cleanup(cancel)
	return &runHandle{stop: stop, stdout: out.String}
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func blockingQuery(name string, client string) engine.Query {
	req := new(dns.Msg)
	req.SetQuestion(dns.Fqdn(name), dns.TypeA)
	return engine.Query{Req: req, Client: netip.MustParseAddr(client), Proto: "udp"}
}

func TestRunServer_StartsTheBackendReportsItAndStopsOnCancel(t *testing.T) {
	f := newRunFixture(t, "")
	registry := backend.Registry{"dnsproxy": fakeFactory("dnsproxy", nil)}

	h := f.start(t, registry)
	waitFor(t, "the banner", func() bool { return strings.Contains(h.stdout(), "Ctrl+C") })
	code, stdout, stderr := h.stop()

	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	assertContains(t, "stdout", stdout, []string{
		"xdns berjalan (backend: dnsproxy)", "mendengarkan: udp 127.0.0.1:5300", "mendengarkan: tcp 127.0.0.1:5300", "Ctrl+C", "berhenti",
	})
}

func TestRunServer_BuildsAnEngineThatAppliesRulesACLAndLogsEveryBlock(t *testing.T) {
	f := newRunFixture(t, "allowed_clients: [127.0.0.0/8]\n")
	if err := os.WriteFile(f.rules, []byte("block contoh.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	decisions := make(chan engine.Decision, 3)
	serve := func(deps backend.Deps) error {
		ctx := context.Background()
		decisions <- deps.Engine.Before(ctx, blockingQuery("www.contoh.test", "127.0.0.1"))
		decisions <- deps.Engine.Before(ctx, blockingQuery("www.contoh.test", "8.8.8.8"))
		decisions <- deps.Engine.Before(ctx, blockingQuery("allowed.example.com", "127.0.0.1"))
		return nil
	}
	h := f.start(t, backend.Registry{"dnsproxy": fakeFactory("dnsproxy", serve)})

	byReason := map[string]engine.Decision{}
	var passes int
	for i := 0; i < 3; i++ {
		select {
		case d := <-decisions:
			if d.Verdict == engine.Block {
				byReason[d.Reason] = d
			} else {
				passes++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the backend never received a decision")
		}
	}
	code, _, stderr := h.stop()

	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if rule, ok := byReason[engine.ReasonRule]; !ok || rule.Source != "block contoh.test" {
		t.Errorf("rule block = %+v, want the custom rule line", rule)
	}
	if _, ok := byReason[engine.ReasonACL]; !ok {
		t.Error("a client outside allowed_clients was not refused")
	}
	if passes != 1 {
		t.Errorf("passes = %d, want 1", passes)
	}

	var logged []qlog.Record
	if _, err := qlog.Read(f.logDir, qlog.Filter{}, func(r qlog.Record) { logged = append(logged, r) }); err != nil {
		t.Fatalf("read log: %v", err)
	}
	if len(logged) != 2 {
		t.Errorf("log holds %d records, want the 2 blocks (the pass is logged by After, which this fake never calls)", len(logged))
	}
}

func TestRunServer_PicksUpARuleAddedWhileRunning(t *testing.T) {
	f := newRunFixture(t, "")
	var deps backend.Deps
	var depsReady = make(chan struct{})
	serve := func(d backend.Deps) error {
		deps = d
		close(depsReady)
		return nil
	}
	h := f.start(t, backend.Registry{"dnsproxy": fakeFactory("dnsproxy", serve)})
	<-depsReady

	q := blockingQuery("late.example.com", "127.0.0.1")
	if d := deps.Engine.Before(context.Background(), q); d.Verdict != engine.Pass {
		t.Fatalf("decision before the rule = %+v, want Pass", d)
	}
	if err := os.WriteFile(f.rules, []byte("block late.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the engine to see the new rule", func() bool {
		return deps.Engine.Before(context.Background(), q).Verdict == engine.Block
	})
	h.stop()
}

func TestRunServer_BackendFlagOverridesTheConfig(t *testing.T) {
	f := newRunFixture(t, "backend: dnsproxy\n")
	picked := make(chan string, 2)
	registry := backend.Registry{
		"dnsproxy": fakeFactory("dnsproxy", func(backend.Deps) error { picked <- "dnsproxy"; return nil }),
		"native":   fakeFactory("native", func(backend.Deps) error { picked <- "native"; return nil }),
	}

	h := f.start(t, registry, "-backend", "native")
	got := <-picked
	waitFor(t, "the banner", func() bool { return strings.Contains(h.stdout(), "Ctrl+C") })
	_, stdout, _ := h.stop()

	if got != "native" || !strings.Contains(stdout, "backend: native") {
		t.Errorf("backend = %q, stdout = %q; want native", got, stdout)
	}
}

func TestRunServer_UnknownOrUnbuiltBackendFails(t *testing.T) {
	f := newRunFixture(t, "")
	registry := backend.Registry{"dnsproxy": fakeFactory("dnsproxy", nil)}

	for _, name := range []string{"native", "bogus"} {
		var out, errOut bytes.Buffer
		code := runServerCtx(context.Background(), []string{"-config", f.config, "-backend", name}, &out, &errOut, registry)
		if code != exitError || !strings.Contains(errOut.String(), name) || !strings.Contains(errOut.String(), "dnsproxy") {
			t.Errorf("backend %q: exit = %d, stderr = %q; want an error naming it and the known backends", name, code, errOut.String())
		}
	}
}

func TestRunServer_ABackendThatCannotStartEndsWithAnError(t *testing.T) {
	f := newRunFixture(t, "")
	registry := backend.Registry{"dnsproxy": fakeFactory("dnsproxy", func(backend.Deps) error { return errors.New("port 5300 is taken") })}

	h := f.start(t, registry)
	code, stdout, stderr := h.stop()

	if code != exitError || !strings.Contains(stderr, "port 5300 is taken") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "mendengarkan") {
		t.Errorf("stdout claims to listen although startup failed: %q", stdout)
	}
}

// agy wrote this
func TestRunServer_PrintsConfigWarningsWithoutPhase1StubNote(t *testing.T) {
	f := newRunFixture(t, "lists:\n  sources: []\n")
	registry := backend.Registry{"dnsproxy": fakeFactory("dnsproxy", nil)}

	h := f.start(t, registry)
	waitFor(t, "the banner", func() bool { return strings.Contains(h.stdout(), "Ctrl+C") })
	_, stdout, _ := h.stop()

	assertContains(t, "stdout", stdout, []string{"peringatan: tidak ada sumber daftar blokir yang aktif"})
	if strings.Contains(stdout, "belum dimuat (Fase 2)") {
		t.Errorf("Phase 1 stub note still printed in Phase 2: %q", stdout)
	}
}

func TestRunServer_LoadsCachedBlockAndAllowListsIntoEngine(t *testing.T) {
	yaml := `data_dir: data
lists:
  sources:
    - { name: safe-allow, kind: allow, format: domains, url: "https://127.0.0.1:1/allow.txt" }
    - { name: ads-block,  kind: block, format: domains, url: "https://127.0.0.1:1/block.txt" }
`
	f := newRunFixture(t, yaml)
	listsDir := filepath.Join(f.dir, "data", "lists")
	if err := os.MkdirAll(listsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(listsDir, "safe-allow.txt"), []byte("safe.ads.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(listsDir, "ads-block.txt"), []byte("ads.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	decisions := make(chan engine.Decision, 2)
	serve := func(deps backend.Deps) error {
		ctx := context.Background()
		decisions <- deps.Engine.Before(ctx, blockingQuery("tracker.ads.example.com", "127.0.0.1"))
		decisions <- deps.Engine.Before(ctx, blockingQuery("safe.ads.example.com", "127.0.0.1"))
		return nil
	}
	h := f.start(t, backend.Registry{"dnsproxy": fakeFactory("dnsproxy", serve)})
	dBlock := <-decisions
	dAllow := <-decisions
	code, _, stderr := h.stop()

	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if dBlock.Verdict != engine.Block || dBlock.Reason != engine.ReasonList || dBlock.Source != "ads-block" {
		t.Errorf("block decision = %+v, want Block/list/ads-block", dBlock)
	}
	if dAllow.Verdict != engine.Pass || dAllow.Reason != engine.ReasonAllow || dAllow.Source != "safe-allow" {
		t.Errorf("allow decision = %+v, want Pass/allow/safe-allow", dAllow)
	}
}

func TestRunServer_LogDisabledCreatesNoLogDirectory(t *testing.T) {
	f := newRunFixture(t, "")
	disabled := writeFile(t, f.dir, "off.yaml", "rules:\n  file: rules.txt\nlog:\n  enabled: false\n  dir: logs\n")
	registry := backend.Registry{"dnsproxy": fakeFactory("dnsproxy", nil)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var out, errOut safeBuffer
	go func() { done <- runServerCtx(ctx, []string{"-config", disabled}, &out, &errOut, registry) }()
	time.Sleep(100 * time.Millisecond)
	cancel()

	if code := <-done; code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, errOut.String())
	}
	if _, err := os.Stat(f.logDir); err == nil {
		t.Error("the log directory was created although log.enabled is false")
	}
}

func TestRunServer_ConfigAndUsageProblems(t *testing.T) {
	f := newRunFixture(t, "")
	registry := backend.Registry{"dnsproxy": fakeFactory("dnsproxy", nil)}
	missing := filepath.Join(f.dir, "nope.yaml")
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{"missing config", []string{"-config", missing}, exitError, missing},
		{"unknown flag", []string{"-bogus"}, exitUsage, "bogus"},
		{"stray argument", []string{"-config", f.config, "stray"}, exitUsage, "stray"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := runServerCtx(context.Background(), tt.args, &out, &errOut, registry)
			if code != tt.wantCode || !strings.Contains(errOut.String(), tt.wantErr) {
				t.Errorf("exit = %d, stderr = %q; want exit %d and %q", code, errOut.String(), tt.wantCode, tt.wantErr)
			}
		})
	}
}

func TestDefaultRegistry_HasBothBackends(t *testing.T) {
	names := defaultRegistry().Names()
	if len(names) != 2 || names[0] != "dnsproxy" || names[1] != "native" {
		t.Errorf("registered backends = %v, want [dnsproxy native]", names)
	}
}

// runHandle controls a `xdns run` started by runFixture.start.
type runHandle struct {
	stop   func() (code int, stdout, stderr string)
	stdout func() string
}
