package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	runDashboardLabel = "q3w8e5r2t7y4u9i6o1p0"
	runDashboardName  = runDashboardLabel + ".example.org"
	runDoTName        = "k7x9p2qm4t8v3w6z.example.org"
)

type dashboardRunFixture struct {
	dir      string
	config   string
	rules    string
	key      string
	port     int
	upstream string
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func newDashboardRunFixture(t *testing.T, enabled bool) dashboardRunFixture {
	t.Helper()
	dir := t.TempDir()
	keyPath, hashPath, _, stderr, code := genKey(t, dir)
	if code != exitOK {
		t.Fatalf("genkey: exit %d: %s", code, stderr)
	}
	f := dashboardRunFixture{
		dir:      dir,
		rules:    filepath.Join(dir, "rules.txt"),
		key:      strings.TrimSpace(readText(t, keyPath)),
		port:     freeLoopbackPort(t),
		upstream: startTestDNSUpstream(t, false),
	}
	f.config = writeFile(t, dir, "hecate.yaml", fmt.Sprintf(`
listen: [%q]
allowed_clients: [127.0.0.0/8]
upstreams: ["https://dns.example.net/dns-query"]
bootstrap: ["1.1.1.1"]
data_dir: data
rules:
  file: rules.txt
log:
  dir: logs
dot:
  names: [%q]
dashboard:
  enabled: %t
  listen: ["127.0.0.1:%d"]
  names: [%q]
  key_hash_file: %q
  secret_file: cookie.secret
`, f.upstream, runDoTName, enabled, f.port, runDashboardName, filepath.ToSlash(hashPath)))
	return f
}

type dashboardRun struct {
	stop   func() (int, string, string)
	stdout func() string
}

func (f dashboardRunFixture) start(t *testing.T, extra ...string) *dashboardRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var out, errOut safeBuffer
	done := make(chan int, 1)
	go func() {
		done <- dashboardRunCtx(ctx, append([]string{"-config", f.config}, extra...), &out, &errOut)
	}()
	t.Cleanup(cancel)
	return &dashboardRun{
		stdout: out.String,
		stop: func() (int, string, string) {
			cancel()
			select {
			case code := <-done:
				return code, out.String(), errOut.String()
			case <-time.After(10 * time.Second):
				t.Fatal("hecate dashboard did not stop after its context was cancelled")
				return 0, "", ""
			}
		},
	}
}

type dashboardClient struct {
	t    *testing.T
	base string
	http *http.Client
}

func newDashboardClient(t *testing.T, port int) *dashboardClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &dashboardClient{t: t, base: "http://127.0.0.1:" + strconv.Itoa(port), http: &http.Client{Jar: jar, Timeout: 10 * time.Second}}
}

func (c *dashboardClient) do(method, path, body string) (int, map[string]any) {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Hecate-Dashboard", "1")
		req.Header.Set("Origin", c.base)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		c.t.Fatalf("%s %s: body %q is not a JSON object", method, path, data)
	}
	return resp.StatusCode, out
}

func (c *dashboardClient) waitUntilUp(t *testing.T) {
	t.Helper()
	waitFor(t, "the dashboard to answer", func() bool {
		resp, err := c.http.Get(c.base + "/api/status")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return true
	})
}

func (c *dashboardClient) login(t *testing.T, key string) {
	t.Helper()
	status, body := c.do(http.MethodPost, "/api/login", fmt.Sprintf(`{"key":%q}`, key))
	if status != http.StatusOK {
		t.Fatalf("login = %d %v", status, body)
	}
}

func TestDashboardRun_ServesLoginStatusQueriesAndRules(t *testing.T) {
	f := newDashboardRunFixture(t, true)
	run := f.start(t)
	c := newDashboardClient(t, f.port)
	c.waitUntilUp(t)

	if status, body := c.do(http.MethodGet, "/api/status", ""); status != http.StatusUnauthorized || body["error"] != "unauthorized" {
		t.Fatalf("anonymous status = %d %v", status, body)
	}
	if status, body := c.do(http.MethodPost, "/api/login", `{"key":"AAAAAAAAAAAAAAAAAAAAAAAAAA"}`); status != http.StatusUnauthorized || body["error"] != "bad_key" {
		t.Fatalf("wrong key = %d %v", status, body)
	}
	c.login(t, f.key)

	status, body := c.do(http.MethodGet, "/api/status", "")
	resolver, _ := body["resolver"].(map[string]any)
	if status != http.StatusOK || resolver["ok"] != true || body["log_enabled"] != true {
		t.Fatalf("status = %d %v", status, body)
	}
	if status, body := c.do(http.MethodGet, "/api/queries", ""); status != http.StatusOK || body["rows"] == nil {
		t.Fatalf("queries = %d %v", status, body)
	}
	if status, body := c.do(http.MethodPost, "/api/rules", `{"action":"block","domain":"ads.example.com"}`); status != http.StatusOK || body["replaced"] != nil {
		t.Fatalf("add rule = %d %v", status, body)
	}
	if got := readText(t, f.rules); got != "block ads.example.com\n" {
		t.Fatalf("rules file = %q", got)
	}
	if status, body := c.do(http.MethodGet, "/api/rules", ""); status != http.StatusOK || body["count"] != 1.0 {
		t.Fatalf("list rules = %d %v", status, body)
	}
	if status, _ := c.do(http.MethodPost, "/api/logout", `{}`); status != http.StatusOK {
		t.Fatalf("logout = %d", status)
	}
	if status, _ := c.do(http.MethodGet, "/api/rules", ""); status != http.StatusUnauthorized {
		t.Fatalf("rules after logout = %d", status)
	}

	code, stdout, stderr := run.stop()
	if code != exitOK || !strings.Contains(stdout, "127.0.0.1:"+strconv.Itoa(f.port)) {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	for _, secret := range []string{runDashboardLabel, "k7x9p2qm4t8v3w6z", f.key} {
		if strings.Contains(stdout, secret) || strings.Contains(stderr, secret) {
			t.Fatalf("the output leaks a secret: %q %q", stdout, stderr)
		}
	}
}

func TestDashboardRun_ProtectsTheNamesItDependsOn(t *testing.T) {
	f := newDashboardRunFixture(t, true)
	f.start(t)
	c := newDashboardClient(t, f.port)
	c.waitUntilUp(t)
	c.login(t, f.key)

	for _, domain := range []string{runDashboardName, runDoTName, "example.org", "dns.example.net", "example.net"} {
		status, body := c.do(http.MethodPost, "/api/rules", fmt.Sprintf(`{"action":"block","domain":%q}`, domain))
		if status != http.StatusBadRequest || body["error"] != "protected_name" {
			t.Errorf("block %s = %d %v, want protected_name", domain, status, body)
		}
	}
	if status, body := c.do(http.MethodPost, "/api/rules", `{"action":"block","domain":"co.id"}`); status != http.StatusBadRequest || body["error"] != "public_suffix" {
		t.Errorf("block co.id = %d %v", status, body)
	}
	if status, _ := c.do(http.MethodPost, "/api/rules", `{"action":"block","domain":"ads.example.org"}`); status != http.StatusOK {
		t.Errorf("a sibling of the protected names must stay blockable, got %d", status)
	}
}

func TestDashboardRun_RefusesADisabledDashboard(t *testing.T) {
	f := newDashboardRunFixture(t, false)
	var stdout, stderr bytes.Buffer

	code := dashboardRunCtx(context.Background(), []string{"-config", f.config}, &stdout, &stderr)

	if code != exitError || !strings.Contains(stderr.String(), "disabled") || stdout.Len() != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestDashboardRun_ReportsConfigAndStartupProblems(t *testing.T) {
	f := newDashboardRunFixture(t, true)
	busy, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(f.port))
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	tests := []struct {
		name string
		args []string
		code int
	}{
		{"missing config", []string{"-config", filepath.Join(f.dir, "none.yaml")}, exitError},
		{"port in use", []string{"-config", f.config}, exitError},
		{"unknown flag", []string{"-nope"}, exitUsage},
		{"extra argument", []string{"-config", f.config, "extra"}, exitUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := dashboardRunCtx(context.Background(), tt.args, &stdout, &stderr)

			if code != tt.code || strings.Contains(stdout.String()+stderr.String(), runDashboardLabel) {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestDashboardRun_IsReachableThroughExecute(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Execute([]string{"dashboard", "-config", filepath.Join(t.TempDir(), "none.yaml")}, &stdout, &stderr)

	if code != exitError || !strings.Contains(stderr.String(), "hecate dashboard") {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
}
