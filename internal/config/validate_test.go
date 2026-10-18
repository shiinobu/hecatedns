package config

import (
	"strings"
	"testing"
	"time"
)

func okSource() Source {
	return Source{
		Name:    "a",
		URL:     "https://example.com/a.txt",
		Kind:    KindBlock,
		Format:  FormatDomains,
		Enabled: true,
	}
}

func withSource(edit func(*Source)) []Source {
	s := okSource()
	edit(&s)
	return []Source{s}
}

func TestValidate_Rejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"unknown backend", func(c *Config) { c.Backend = "foo" }, "backend"},
		{"empty listen", func(c *Config) { c.Listen = nil }, "listen"},
		{"listen hostname", func(c *Config) { c.Listen = []string{"localhost:5300"} }, "listen[0]"},
		{"listen without port", func(c *Config) { c.Listen = []string{"127.0.0.1"} }, "listen[0]"},
		{"bad CIDR", func(c *Config) { c.AllowedClients = []string{"10.0.0.0/33"} }, "allowed_clients[0]"},
		{"no upstreams", func(c *Config) { c.Upstreams = nil }, "upstreams"},
		{"bad upstream scheme", func(c *Config) { c.Upstreams = []string{"ftp://1.1.1.1"} }, "upstreams[0]"},
		{"hostname upstream without bootstrap", func(c *Config) {
			c.Upstreams = []string{"https://dns.google/dns-query"}
			c.Bootstrap = nil
		}, "bootstrap"},
		{"bootstrap not an IP", func(c *Config) { c.Bootstrap = []string{"dns.google"} }, "bootstrap[0]"},
		{"bad block mode", func(c *Config) { c.Block.Mode = "weird" }, "block.mode"},
		{"negative block ttl", func(c *Config) { c.Block.TTL = -time.Second }, "block.ttl"},
		{"cache size zero", func(c *Config) { c.Cache.SizeMB = 0 }, "cache.size_mb"},
		{"cache min above max", func(c *Config) {
			c.Cache.MinTTL = 2 * time.Hour
			c.Cache.MaxTTL = time.Hour
		}, "cache.min_ttl"},
		{"list not https", func(c *Config) {
			c.Lists.Sources = withSource(func(s *Source) { s.URL = "http://x/y.txt" })
		}, "lists.sources[0].url"},
		{"list bad kind", func(c *Config) {
			c.Lists.Sources = withSource(func(s *Source) { s.Kind = "x" })
		}, "lists.sources[0].kind"},
		{"list unsupported format", func(c *Config) {
			c.Lists.Sources = withSource(func(s *Source) { s.Format = "adblock" })
		}, "lists.sources[0].format"},
		{"list empty name", func(c *Config) {
			c.Lists.Sources = withSource(func(s *Source) { s.Name = "" })
		}, "lists.sources[0].name"},
		{"duplicate list name", func(c *Config) {
			c.Lists.Sources = []Source{okSource(), okSource()}
		}, "duplicate"},
		{"zero update interval with sources", func(c *Config) {
			c.Lists.Sources = []Source{okSource()}
			c.Lists.UpdateInterval = 0
		}, "lists.update_interval"},
		{"zero poll interval", func(c *Config) { c.Rules.PollInterval = 0 }, "rules.poll_interval"},
		{"zero retention when log enabled", func(c *Config) { c.Log.RetentionDays = 0 }, "log.retention_days"},
		{"empty data_dir", func(c *Config) { c.DataDir = "" }, "data_dir"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(&cfg)

			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}

func TestValidate_Accepts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"defaults", func(*Config) {}},
		{"IPv6 listener and client", func(c *Config) {
			c.Listen = []string{"[::1]:5300"}
			c.AllowedClients = []string{"::1/128"}
		}},
		{"bare IP upstream", func(c *Config) { c.Upstreams = []string{"1.1.1.1"} }},
		{"IP with port upstream", func(c *Config) { c.Upstreams = []string{"1.1.1.1:53"} }},
		{"udp scheme upstream", func(c *Config) { c.Upstreams = []string{"udp://1.1.1.1"} }},
		{"tcp scheme upstream with port", func(c *Config) { c.Upstreams = []string{"tcp://1.1.1.1:53"} }},
		{"tls scheme upstream", func(c *Config) { c.Upstreams = []string{"tls://1.1.1.1:853"} }},
		{"https IP upstream", func(c *Config) { c.Upstreams = []string{"https://1.1.1.1/dns-query"} }},
		{"hostname upstream with bootstrap", func(c *Config) {
			c.Upstreams = []string{"https://dns.google/dns-query"}
			c.Bootstrap = []string{"1.1.1.1"}
		}},
		{"enabled source", func(c *Config) { c.Lists.Sources = []Source{okSource()} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(&cfg)

			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestValidate_ReportsAllErrors(t *testing.T) {
	cfg := Default()
	cfg.Backend = "foo"
	cfg.Block.Mode = "weird"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want errors")
	}
	for _, want := range []string{"backend", "block.mode"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate() = %q, want it to mention %q", err, want)
		}
	}
}

func TestWarnings(t *testing.T) {
	t.Run("no sources", func(t *testing.T) {
		got := Default().Warnings()

		if len(got) != 1 || !strings.Contains(got[0], "daftar") {
			t.Errorf("Warnings() = %v, want one warning mentioning daftar", got)
		}
	})
	t.Run("only disabled sources", func(t *testing.T) {
		cfg := Default()
		cfg.Lists.Sources = withSource(func(s *Source) { s.Enabled = false })

		if got := cfg.Warnings(); len(got) != 1 {
			t.Errorf("Warnings() = %v, want one warning", got)
		}
	})
	t.Run("example config", func(t *testing.T) {
		cfg, err := Load(exampleConfigPath)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		if got := cfg.Warnings(); len(got) != 0 {
			t.Errorf("Warnings() = %v, want none", got)
		}
	})
}

func TestLoad_InvalidConfigFails(t *testing.T) {
	path, _ := writeConfig(t, "backend: nope\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("Load error = %v, want it to mention backend", err)
	}
}
