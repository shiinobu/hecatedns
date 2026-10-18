package dnsproxybe

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/AdguardTeam/dnsproxy/proxy"

	"xdns/internal/config"
)

var discardLog = slog.New(slog.DiscardHandler)

func TestProxyConfigMapsSettings(t *testing.T) {
	cfg := config.Default()
	cfg.Listen = []string{"127.0.0.1:5300", "[::1]:5353"}
	cfg.Upstreams = []string{"127.0.0.1:5399", "https://dns.example/dns-query"}
	cfg.Bootstrap = []string{"192.0.2.53", "192.0.2.54"}
	cfg.Cache = config.Cache{Enabled: true, SizeMB: 16, MinTTL: 30 * time.Second, MaxTTL: time.Hour}
	h := &handler{log: discardLog}

	conf, res, err := buildProxyConfig(cfg, h, discardLog, UpstreamTimeout)
	if err != nil {
		t.Fatalf("buildProxyConfig: %v", err)
	}
	t.Cleanup(func() { _ = res.close() })

	if len(conf.UDPListenAddr) != 2 || conf.UDPListenAddr[1].String() != "[::1]:5353" ||
		len(conf.TCPListenAddr) != 2 || conf.TCPListenAddr[0].String() != "127.0.0.1:5300" {
		t.Fatalf("listen = %v / %v, want both entries on udp and tcp", conf.UDPListenAddr, conf.TCPListenAddr)
	}
	if n := len(conf.UpstreamConfig.Upstreams); n != 2 || len(res.bootstrap) != 2 {
		t.Fatalf("upstreams %d, bootstrap resolvers %d; want 2 and 2", n, len(res.bootstrap))
	}
	if !conf.CacheEnabled || conf.CacheSizeBytes != 16<<20 || conf.CacheMinTTL != 30 || conf.CacheMaxTTL != 3600 {
		t.Fatalf("cache = %v %d %d %d, want enabled 16 MiB 30 3600", conf.CacheEnabled, conf.CacheSizeBytes, conf.CacheMinTTL, conf.CacheMaxTTL)
	}
	if !conf.RefuseAny || conf.PendingRequests == nil || !conf.PendingRequests.Enabled || conf.MaxGoroutines != MaxGoroutines {
		t.Fatalf("hardening = refuseAny %v pending %v goroutines %d", conf.RefuseAny, conf.PendingRequests, conf.MaxGoroutines)
	}
	if conf.DNSSECEnabled || conf.EnableEDNSClientSubnet || conf.CacheOptimistic || conf.UseDNS64 {
		t.Fatal("DNSSEC, ECS, optimistic cache and DNS64 must stay off")
	}
	if conf.UpstreamMode != proxy.UpstreamModeLoadBalance || conf.RequestHandler != h || conf.Logger == nil {
		t.Fatalf("mode %q handler %v logger %v", conf.UpstreamMode, conf.RequestHandler, conf.Logger)
	}
}

func TestProxyConfigReportsEveryBadEntry(t *testing.T) {
	cfg := config.Default()
	cfg.Listen = []string{"nope", "127.0.0.1:53", "1.2.3.4"}
	cfg.Upstreams = []string{"foo://x"}
	cfg.Bootstrap = []string{"dns.example"}

	_, _, err := buildProxyConfig(cfg, &handler{}, discardLog, UpstreamTimeout)

	if err == nil {
		t.Fatal("buildProxyConfig returned no error")
	}
	for _, part := range []string{"listen[0]", "listen[2]", "upstreams", "bootstrap[0]"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error %q does not mention %s", err, part)
		}
	}
}

func TestProxyConfigRequiresListenAndUpstreams(t *testing.T) {
	tests := []struct {
		name      string
		listen    []string
		upstreams []string
		want      string
	}{
		{"no listen", nil, []string{"127.0.0.1:53"}, "listen"},
		{"no upstreams", []string{"127.0.0.1:0"}, nil, "upstreams"},
		{"only a comment", []string{"127.0.0.1:0"}, []string{"# none"}, "upstreams"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Listen, cfg.Upstreams = tt.listen, tt.upstreams
			if _, _, err := buildProxyConfig(cfg, &handler{}, discardLog, UpstreamTimeout); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("buildProxyConfig = %v, want an error about %s", err, tt.want)
			}
		})
	}
}

// TestDoHByIPBuildsWithoutBootstrap proves the facts-file item: an IP DoH
// upstream needs no bootstrap. Only construction is done, no exchange.
func TestDoHByIPBuildsWithoutBootstrap(t *testing.T) {
	cfg := config.Default()
	cfg.Listen = []string{"127.0.0.1:0"}
	cfg.Upstreams = []string{"https://1.1.1.1/dns-query"}
	cfg.Bootstrap = nil

	conf, res, err := buildProxyConfig(cfg, &handler{}, discardLog, UpstreamTimeout)
	if err != nil {
		t.Fatalf("buildProxyConfig: %v", err)
	}
	defer func() {
		if err := res.close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	if len(res.bootstrap) != 0 || len(conf.UpstreamConfig.Upstreams) != 1 {
		t.Fatalf("bootstrap %d upstreams %d, want 0 and 1", len(res.bootstrap), len(conf.UpstreamConfig.Upstreams))
	}
	if addr := conf.UpstreamConfig.Upstreams[0].Address(); !strings.Contains(addr, "1.1.1.1") {
		t.Fatalf("upstream address %q, want 1.1.1.1", addr)
	}
}

// TestProxyStartTwice proves the facts-file item about Start: a second Start
// is an error, the first keeps serving.
func TestProxyStartTwice(t *testing.T) {
	up := startFakeUpstream(t, 300)
	conf, res, err := buildProxyConfig(testConfig(up.addr), proxy.DefaultHandler{}, discardLog, UpstreamTimeout)
	if err != nil {
		t.Fatalf("buildProxyConfig: %v", err)
	}
	defer func() { _ = res.closeBootstrap() }()
	p, err := proxy.New(conf)
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}
	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer func() { _ = p.Shutdown(ctx) }()

	if err := p.Start(ctx); err == nil || !strings.Contains(err.Error(), "already started") {
		t.Fatalf("second Start = %v, want an already-started error", err)
	}
	if p.Addr(proxy.ProtoUDP) == nil {
		t.Fatal("proxy stopped listening after the second Start")
	}
}

func TestTTLSeconds(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want uint32
	}{
		{0, 0},
		{-time.Second, 0},
		{500 * time.Millisecond, 1},
		{time.Second, 1},
		{90*time.Second + 900*time.Millisecond, 90},
		{time.Hour, 3600},
		{time.Duration(math.MaxInt64), math.MaxUint32},
	}
	for _, tt := range tests {
		if got := ttlSeconds(tt.in); got != tt.want {
			t.Errorf("ttlSeconds(%v) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestCacheSizeBytes(t *testing.T) {
	tests := []struct {
		in, want int
	}{
		{0, 0},
		{-5, 0},
		{16, 16 << 20},
		{math.MaxInt, math.MaxInt},
		{math.MaxInt >> 20, (math.MaxInt >> 20) << 20},
	}
	for _, tt := range tests {
		if got := cacheSizeBytes(tt.in); got != tt.want {
			t.Errorf("cacheSizeBytes(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
