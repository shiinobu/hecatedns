package selfcheck

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/lib/config"
	"github.com/shiinobu/hecatedns/backend/service/lists"
)

func startFakeUpstream(t *testing.T, handler func(req *dns.Msg) *dns.Msg) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	started := make(chan struct{})
	srv := &dns.Server{
		PacketConn: conn,
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
			resp := handler(r)
			if resp != nil {
				_ = w.WriteMsg(resp)
			}
		}),
		NotifyStartedFunc: func() { close(started) },
	}
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })
	return conn.LocalAddr().String()
}

func googleOKHandler(req *dns.Msg) *dns.Msg {
	resp := new(dns.Msg).SetReply(req)
	if len(req.Question) == 1 {
		q := req.Question[0]
		resp.Answer = []dns.RR{
			&dns.A{
				Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.IPv4(142, 250, 4, 100),
			},
		}
	}
	return resp
}

func writeCachedList(t *testing.T, dataDir, name, content string) {
	t.Helper()
	dir := lists.Dir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(content), 0o600); err != nil {
		t.Fatalf("write list %s: %v", name, err)
	}
}

func TestRun_HealthyUpstreamsAndCachedListsPass(t *testing.T) {
	upstreamAddr := startFakeUpstream(t, googleOKHandler)
	dataDir := t.TempDir()
	writeCachedList(t, dataDir, "hagezi-pro", "# comment\nads.pro.example\ntracker.pro.example\n")
	writeCachedList(t, dataDir, "hagezi-popups", "popup.bad.example\n")
	writeCachedList(t, dataDir, "abpindo", "iklan.indo.example\n")

	cfg := config.Default()
	cfg.Upstreams = []string{upstreamAddr}
	cfg.Bootstrap = nil
	cfg.DataDir = dataDir
	cfg.Rules.File = filepath.Join(dataDir, "rules.txt")
	cfg.Lists.Sources = []config.Source{
		{Name: "hagezi-pro", URL: "https://example.com/pro.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
		{Name: "hagezi-popups", URL: "https://example.com/popups.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
		{Name: "abpindo", URL: "https://example.com/abpindo.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
	}

	res, err := Run(context.Background(), cfg, Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if !res.SafeSearchOK || res.UpstreamsOK != 1 {
		t.Fatalf("Result = %+v, want SafeSearchOK=true UpstreamsOK=1", res)
	}
	if len(res.Lists) != 3 {
		t.Fatalf("len(res.Lists) = %d, want 3", len(res.Lists))
	}
	for _, l := range res.Lists {
		if !l.Cached || l.SampleDomain == "" || l.SampleAnswer != "0.0.0.0" {
			t.Errorf("ListResult = %+v, want Cached=true, non-empty SampleDomain, SampleAnswer=0.0.0.0", l)
		}
	}
}

func TestRun_SafeSearchForcedFails(t *testing.T) {
	for _, target := range []string{"forcesafesearch.google.com.", "sub.forcesafesearch.google.com."} {
		upstreamAddr := startFakeUpstream(t, func(req *dns.Msg) *dns.Msg {
			resp := new(dns.Msg).SetReply(req)
			if len(req.Question) == 1 {
				q := req.Question[0]
				resp.Answer = []dns.RR{
					&dns.CNAME{
						Hdr:    dns.RR_Header{Name: q.Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 60},
						Target: target,
					},
					&dns.A{
						Hdr: dns.RR_Header{Name: target, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
						A:   net.IPv4(216, 239, 38, 120),
					},
				}
			}
			return resp
		})
		cfg := config.Default()
		cfg.Upstreams = []string{upstreamAddr}
		cfg.Bootstrap = nil
		cfg.DataDir = t.TempDir()
		cfg.Rules.File = filepath.Join(cfg.DataDir, "rules.txt")

		_, err := Run(context.Background(), cfg, Options{Timeout: 2 * time.Second})
		if err == nil {
			t.Fatalf("Run succeeded when upstream forced SafeSearch (%s), want an error", target)
		}
		if !strings.Contains(err.Error(), "forcesafesearch.google.com") || !strings.Contains(err.Error(), upstreamAddr) {
			t.Fatalf("err = %v, want it to mention forcesafesearch.google.com and %s", err, upstreamAddr)
		}
	}
}

func TestRun_LoopingUpstreamFails(t *testing.T) {
	cfg := config.Default()
	cfg.Listen = []string{"127.0.0.1:5300"}
	cfg.Upstreams = []string{"127.0.0.1:5300"}
	cfg.Bootstrap = nil
	cfg.DataDir = t.TempDir()
	cfg.Rules.File = filepath.Join(cfg.DataDir, "rules.txt")

	_, err := Run(context.Background(), cfg, Options{Timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1:5300") {
		t.Fatalf("Run error = %v, want loop error mentioning 127.0.0.1:5300", err)
	}
}

func TestRun_UnreachableUpstreamFails(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadAddr := conn.LocalAddr().String()
	_ = conn.Close()

	cfg := config.Default()
	cfg.Upstreams = []string{deadAddr}
	cfg.Bootstrap = nil
	cfg.DataDir = t.TempDir()
	cfg.Rules.File = filepath.Join(cfg.DataDir, "rules.txt")

	_, err = Run(context.Background(), cfg, Options{Timeout: 300 * time.Millisecond})
	if err == nil {
		t.Fatal("Run succeeded with unreachable upstream, want error")
	}
}

func TestRun_CorruptCachedListFailsAndMissingCachedListWarns(t *testing.T) {
	upstreamAddr := startFakeUpstream(t, googleOKHandler)
	dataDir := t.TempDir()

	cfg := config.Default()
	cfg.Upstreams = []string{upstreamAddr}
	cfg.Bootstrap = nil
	cfg.DataDir = dataDir
	cfg.Rules.File = filepath.Join(dataDir, "rules.txt")
	cfg.Lists.Sources = []config.Source{
		{Name: "missing-list", URL: "https://example.com/missing.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
	}

	res, err := Run(context.Background(), cfg, Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Run with missing cached list failed: %v, want warning only", err)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected warning for missing cached list, got none")
	}

	writeCachedList(t, dataDir, "missing-list", "# only comments, no domains\n")
	if _, err := Run(context.Background(), cfg, Options{Timeout: 2 * time.Second}); err == nil {
		t.Fatal("Run with empty/corrupt cached list on disk succeeded, want error")
	}
}

func TestRun_PartialUpstreamFailureAndBootstrapAndModes(t *testing.T) {
	okUpstream := startFakeUpstream(t, googleOKHandler)
	noAUpstream := startFakeUpstream(t, func(req *dns.Msg) *dns.Msg {
		return new(dns.Msg).SetReply(req)
	})
	servfailUpstream := startFakeUpstream(t, func(req *dns.Msg) *dns.Msg {
		resp := new(dns.Msg).SetReply(req)
		resp.Rcode = dns.RcodeServerFailure
		return resp
	})

	dataDir := t.TempDir()
	writeCachedList(t, dataDir, "allow-list", "trusted.example\n")
	writeCachedList(t, dataDir, "block-list", "bad.example\n")

	cfg := config.Default()
	cfg.Upstreams = []string{servfailUpstream, noAUpstream, okUpstream}
	cfg.Bootstrap = []string{"127.0.0.1"}
	cfg.Block.Mode = config.ModeNXDomain
	cfg.DataDir = dataDir
	cfg.Rules.File = filepath.Join(dataDir, "rules.txt")
	cfg.Lists.Sources = []config.Source{
		{Name: "disabled-list", URL: "https://example.com/dis.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: false},
		{Name: "allow-list", URL: "https://example.com/allow.txt", Kind: config.KindAllow, Format: config.FormatDomains, Enabled: true},
		{Name: "block-list", URL: "https://example.com/block.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
	}

	res, err := Run(context.Background(), cfg, Options{Timeout: 0})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.UpstreamsChecked != 3 || res.UpstreamsOK != 1 || len(res.Warnings) != 2 {
		t.Fatalf("Result = %+v, want Checked=3 OK=1 Warnings=2", res)
	}

	if err := os.WriteFile(cfg.Rules.File, []byte("allow bad.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), cfg, Options{Timeout: time.Second}); err == nil {
		t.Fatal("Run succeeded when custom allow rule unblocked all domains in block-list, want error")
	}
}

func TestRun_SkipsAllowedFirstEntryAndPicksListOwnSample(t *testing.T) {
	upstreamAddr := startFakeUpstream(t, googleOKHandler)
	dataDir := t.TempDir()

	hugeComment := "# " + strings.Repeat("c", 70*1024) + "\n"
	writeCachedList(t, dataDir, "hagezi-pro", "a-shared.ads.example\nb-pro.ads.example\n")
	writeCachedList(t, dataDir, "hagezi-popups", "a-shared.ads.example\nz-popup-only.ads.example\n")
	writeCachedList(t, dataDir, "abpindo", hugeComment+"a-allowed.indo.example\nb-blocked.indo.example\n")

	rulesPath := filepath.Join(dataDir, "rules.txt")
	if err := os.WriteFile(rulesPath, []byte("allow a-allowed.indo.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Upstreams = []string{upstreamAddr}
	cfg.Bootstrap = nil
	cfg.DataDir = dataDir
	cfg.Rules.File = rulesPath
	cfg.Lists.Sources = []config.Source{
		{Name: "hagezi-pro", URL: "https://example.com/pro.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
		{Name: "hagezi-popups", URL: "https://example.com/popups.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
		{Name: "abpindo", URL: "https://example.com/abpindo.txt", Kind: config.KindBlock, Format: config.FormatDomains, Enabled: true},
	}

	res, err := Run(context.Background(), cfg, Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if len(res.Lists) != 3 {
		t.Fatalf("len(res.Lists) = %d, want 3", len(res.Lists))
	}
	if res.Lists[1].SampleDomain != "z-popup-only.ads.example" {
		t.Errorf("hagezi-popups SampleDomain = %q, want \"z-popup-only.ads.example\" (not the entry shared with hagezi-pro)", res.Lists[1].SampleDomain)
	}
	if res.Lists[2].SampleDomain != "b-blocked.indo.example" {
		t.Errorf("abpindo SampleDomain = %q, want \"b-blocked.indo.example\" (skipping allow-listed first entry and >64KiB comment)", res.Lists[2].SampleDomain)
	}

	writeCachedList(t, dataDir, "hagezi-popups", "a-shared.ads.example\n")
	res2, err := Run(context.Background(), cfg, Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Run with subset blocklist: %v", err)
	}
	if res2.Lists[1].SampleDomain != "a-shared.ads.example" || res2.Lists[1].SampleAnswer != "0.0.0.0" {
		t.Errorf("subset blocklist ListResult = %+v", res2.Lists[1])
	}

	cfg.Rules.File = dataDir
	if _, err := Run(context.Background(), cfg, Options{Timeout: time.Second}); err == nil {
		t.Fatal("Run succeeded when cfg.Rules.File is a directory, want error")
	}
}
