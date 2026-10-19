package controller

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/service/lists"
)

const exampleConfig = "../../configs/hecate.example.yaml"

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// agy wrote this
func startTestDNSUpstream(t *testing.T, forceSafeSearch bool) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	started := make(chan struct{})
	srv := &dns.Server{
		PacketConn: conn,
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
			resp := new(dns.Msg).SetReply(r)
			if len(r.Question) == 1 {
				q := r.Question[0]
				if forceSafeSearch {
					resp.Answer = append(resp.Answer, &dns.CNAME{
						Hdr:    dns.RR_Header{Name: q.Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 60},
						Target: "forcesafesearch.google.com.",
					})
				}
				resp.Answer = append(resp.Answer, &dns.A{
					Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
					A:   net.IPv4(142, 250, 4, 100),
				})
			}
			_ = w.WriteMsg(resp)
		}),
		NotifyStartedFunc: func() { close(started) },
	}
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })
	return conn.LocalAddr().String()
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.yaml")
	invalid := writeFile(t, dir, "invalid.yaml", "backend: nope\n")

	// agy wrote this
	okUpstream := startTestDNSUpstream(t, false)
	safeSearchUpstream := startTestDNSUpstream(t, true)

	if err := os.MkdirAll(lists.Dir(filepath.Join(dir, "data")), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, lists.Dir(filepath.Join(dir, "data")), "hagezi-pro.txt", "ads.pro.test\n")

	validCfg := writeFile(t, dir, "valid.yaml", fmt.Sprintf(`
backend: dnsproxy
upstreams: [%q]
bootstrap: []
data_dir: data
lists:
  update_interval: 24h
  sources:
    - { name: hagezi-pro, kind: block, format: domains, enabled: true, url: "https://example.com/pro.txt" }
    - { name: hagezi-popups, kind: block, format: domains, enabled: true, url: "https://example.com/popups.txt" }
    - { name: abpindo, kind: block, format: domains, enabled: true, url: "https://example.com/abpindo.txt" }
`, okUpstream))
	empty := writeFile(t, dir, "empty.yaml", fmt.Sprintf("upstreams: [%q]\nbootstrap: []\n", okUpstream))
	safeSearchCfg := writeFile(t, dir, "safesearch.yaml", fmt.Sprintf("upstreams: [%q]\nbootstrap: []\n", safeSearchUpstream))
	loopCfg := writeFile(t, dir, "loop.yaml", "listen: [127.0.0.1:5300]\nupstreams: [127.0.0.1:5300]\nbootstrap: []\n")

	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  []string
		wantErr  []string
	}{
		{"no arguments", nil, exitUsage, nil, []string{"penggunaan"}},
		{"unknown command", []string{"bogus"}, exitUsage, nil, []string{"bogus"}},
		{"run with a missing default config", []string{"run"}, exitError, nil, []string{"hecate.yaml"}},
		{"log with a missing default config", []string{"log"}, exitError, nil, []string{"hecate.yaml"}},
		{"rule without a subcommand shows usage", []string{"rule"}, exitUsage, nil, []string{"penggunaan", "hecate rule add"}},
		{"lists without a subcommand shows usage", []string{"lists"}, exitUsage, nil, []string{"penggunaan", "hecate lists"}},
		{"check valid config with cached list", []string{"check", "-config", validCfg}, exitOK,
			[]string{"konfigurasi valid", "backend: dnsproxy", "daftar aktif: 3", "ads.pro.test -> 0.0.0.0", "uji mandiri: LULUS"}, nil},
		{"check missing file", []string{"check", "-config", missing}, exitError, nil, []string{missing}},
		{"check invalid config", []string{"check", "-config", invalid}, exitError, nil, []string{"backend"}},
		{"check empty config warns", []string{"check", "-config", empty}, exitOK, []string{"peringatan", "uji mandiri: LULUS"}, nil},
		{"check SafeSearch forced fails", []string{"check", "-config", safeSearchCfg}, exitError,
			[]string{"uji mandiri: GAGAL"}, []string{"forcesafesearch.google.com"}},
		{"check looping upstream fails", []string{"check", "-config", loopCfg}, exitError,
			[]string{"peringatan", "uji mandiri: GAGAL"}, []string{"127.0.0.1:5300"}},
		{"check a directory", []string{"check", "-config", dir}, exitError, nil, []string{"hecate check"}},
		{"check default path is hecate.yaml", []string{"check"}, exitError, nil, []string{"hecate.yaml"}},
		{"check unknown flag", []string{"check", "-nope"}, exitUsage, nil, []string{"nope"}},
		{"check positional argument", []string{"check", "other.yaml"}, exitUsage, nil, []string{"other.yaml"}},
		{"check extra argument after flag", []string{"check", "-config", validCfg, "extra"}, exitUsage, nil, []string{"extra"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := Execute(tc.args, &stdout, &stderr)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %q)", code, tc.wantCode, stderr.String())
			}
			assertContains(t, "stdout", stdout.String(), tc.wantOut)
			assertContains(t, "stderr", stderr.String(), tc.wantErr)
		})
	}
}

func assertContains(t *testing.T, stream, got string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%s = %q, want it to contain %q", stream, got, want)
		}
	}
}

func TestUsageListsOnlyWhatIsStillMissing(t *testing.T) {
	var stderr bytes.Buffer
	if code := Execute(nil, &bytes.Buffer{}, &stderr); code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
	// agy wrote this
	for _, line := range strings.Split(stderr.String(), "\n") {
		for _, done := range []string{"check", "run", "log", "rule", "lists"} {
			if strings.HasPrefix(strings.TrimSpace(line), done+" ") && strings.Contains(line, "belum diimplementasi") {
				t.Errorf("usage claims %q is not implemented: %q", done, line)
			}
		}
	}
}
