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
		{"empty allowed_clients", func(c *Config) { c.AllowedClients = nil }, "allowed_clients"},
		{"list name path traversal", func(c *Config) {
			c.Lists.Sources = withSource(func(s *Source) { s.Name = "../../evil" })
		}, "lists.sources[0].name"},
		{"list name with slash", func(c *Config) {
			c.Lists.Sources = withSource(func(s *Source) { s.Name = "a/b" })
		}, "lists.sources[0].name"},
		{"list name with colon", func(c *Config) {
			c.Lists.Sources = withSource(func(s *Source) { s.Name = "a:b" })
		}, "lists.sources[0].name"},
		{"list name with space", func(c *Config) {
			c.Lists.Sources = withSource(func(s *Source) { s.Name = "a b" })
		}, "lists.sources[0].name"},
		{"list name too long", func(c *Config) {
			c.Lists.Sources = withSource(func(s *Source) { s.Name = strings.Repeat("a", 65) })
		}, "lists.sources[0].name"},
		{"list name starting with dash", func(c *Config) {
			c.Lists.Sources = withSource(func(s *Source) { s.Name = "-a" })
		}, "lists.sources[0].name"},
		{"list name reserved CON", func(c *Config) {
			c.Lists.Sources = withSource(func(s *Source) { s.Name = "CON" })
		}, "lists.sources[0].name"},
		{"list name reserved com1 lowercase", func(c *Config) {
			c.Lists.Sources = withSource(func(s *Source) { s.Name = "com1" })
		}, "lists.sources[0].name"},
		{"list names differing only by case", func(c *Config) {
			upper, lower := okSource(), okSource()
			upper.Name, lower.Name = "Hagezi", "hagezi"
			c.Lists.Sources = []Source{upper, lower}
		}, "duplicate"},
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
		{"allow everyone CIDRs", func(c *Config) { c.AllowedClients = []string{"0.0.0.0/0", "::/0"} }},
		{"list name with dash and underscore", func(c *Config) {
			c.Lists.Sources = withSource(func(s *Source) { s.Name = "hagezi-pro_2" })
		}},
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

		if len(got) != 1 || !strings.Contains(got[0], "list") {
			t.Errorf("Warnings() = %v, want one warning mentioning list", got)
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

func TestLoad_RejectsEmptyAllowedClients(t *testing.T) {
	tests := []struct{ name, content string }{
		{"empty list", "allowed_clients: []\n"},
		{"all entries commented out", "allowed_clients:\n  # - 10.0.0.0/8\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := writeConfig(t, tc.content)

			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), "allowed_clients") {
				t.Fatalf("Load error = %v, want it to mention allowed_clients", err)
			}
		})
	}
}

func TestValidate_RejectsHardenedCases(t *testing.T) {
	const tooLongTTL = 100 * 365 * 24 * time.Hour
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"empty rules file", func(c *Config) { c.Rules.File = "" }, "rules.file"},
		{"empty log dir while logging", func(c *Config) { c.Log.Dir = "" }, "log.dir"},
		{"duplicate listen entry", func(c *Config) { c.Listen = []string{"127.0.0.1:5300", "127.0.0.1:5300"} }, "listen[1]"},
		{"duplicate listen written differently", func(c *Config) { c.Listen = []string{"127.0.0.1:5300", "127.0.0.1:05300"} }, "listen[1]"},
		{"duplicate ipv6 listen", func(c *Config) { c.Listen = []string{"[::1]:5300", "[0:0:0:0:0:0:0:1]:5300"} }, "listen[1]"},
		{"listen port zero", func(c *Config) { c.Listen = []string{"127.0.0.1:0"} }, "listen[0]"},
		{"block ttl beyond a DNS TTL", func(c *Config) { c.Block.TTL = tooLongTTL }, "block.ttl"},
		{"cache max ttl beyond a DNS TTL", func(c *Config) { c.Cache.MaxTTL = tooLongTTL }, "cache.max_ttl"},
		{"cache min ttl beyond a DNS TTL", func(c *Config) {
			c.Cache.MinTTL = tooLongTTL
			c.Cache.MaxTTL = tooLongTTL
		}, "cache.min_ttl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want an error mentioning %q", err, tt.want)
			}
		})
	}
}

func TestValidate_AcceptsHardenedBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"empty log dir is fine while logging is off", func(c *Config) {
			c.Log.Enabled = false
			c.Log.Dir = ""
		}},
		{"two different listen ports", func(c *Config) { c.Listen = []string{"127.0.0.1:5300", "127.0.0.1:5301"} }},
		{"same port on different addresses", func(c *Config) { c.Listen = []string{"127.0.0.1:5300", "192.168.1.10:5300"} }},
		{"largest allowed TTL", func(c *Config) { c.Block.TTL = time.Duration(maxTTLSeconds) * time.Second }},
		{"zero block ttl", func(c *Config) { c.Block.TTL = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestValidate_LogDailyCap(t *testing.T) {
	cfg := Default()
	cfg.Log.MaxMBPerDay = 0
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "log.max_mb_per_day") {
		t.Fatalf("Validate() = %v, want an error naming log.max_mb_per_day: a zero cap would mean an unbounded log", err)
	}
	cfg.Log.MaxMBPerDay = -5
	if err := cfg.Validate(); err == nil {
		t.Error("a negative cap was accepted")
	}
	cfg.Log.Enabled = false
	cfg.Log.MaxMBPerDay = 0
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, the cap does not matter while the log is off", err)
	}
}

func TestValidate_MaxTTLZeroMeansNoCap(t *testing.T) {
	cfg := Default()
	cfg.Cache.MinTTL = 30 * time.Second
	cfg.Cache.MaxTTL = 0
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v; max_ttl 0 means no cap, so a minimum below it is fine", err)
	}
	cfg.Cache.MaxTTL = 10 * time.Second
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "cache.min_ttl") {
		t.Errorf("Validate() = %v, want min_ttl above a real max_ttl to stay an error", err)
	}
}

func TestWarnings_DetectsLoopingUpstreamAndBootstrap(t *testing.T) {
	tests := []struct {
		name      string
		listen    []string
		upstreams []string
		bootstrap []string
		wantLoop  int
	}{
		{
			name:      "no loop",
			listen:    []string{"127.0.0.1:5300"},
			upstreams: []string{"https://1.1.1.1/dns-query"},
			bootstrap: []string{"1.1.1.1"},
			wantLoop:  0,
		},
		{
			name:      "upstream ip:port equals listen",
			listen:    []string{"127.0.0.1:5300"},
			upstreams: []string{"127.0.0.1:5300"},
			wantLoop:  1,
		},
		{
			name:      "upstream udp URL equals listen",
			listen:    []string{"127.0.0.1:5300"},
			upstreams: []string{"udp://127.0.0.1:5300"},
			wantLoop:  1,
		},
		{
			name:      "upstream bare IP equals listen on port 53",
			listen:    []string{"127.0.0.1:53"},
			upstreams: []string{"127.0.0.1"},
			wantLoop:  1,
		},
		{
			name:      "upstream loopback against unspecified listen",
			listen:    []string{"0.0.0.0:5300"},
			upstreams: []string{"tcp://127.0.0.1:5300"},
			wantLoop:  1,
		},
		{
			name:      "bootstrap IP equals listen on port 53",
			listen:    []string{"127.0.0.1:53"},
			upstreams: []string{"https://dns.google/dns-query"},
			bootstrap: []string{"127.0.0.1"},
			wantLoop:  1,
		},
		{
			name:      "upstream IPv4 loopback against IPv6 unspecified listen",
			listen:    []string{"[::]:5300"},
			upstreams: []string{"127.0.0.1:5300"},
			wantLoop:  1,
		},
		{
			name:      "upstream unspecified against loopback listen",
			listen:    []string{"127.0.0.1:5300"},
			upstreams: []string{"0.0.0.0:5300"},
			wantLoop:  1,
		},
		{
			name:      "upstream localhost URL against loopback listen",
			listen:    []string{"127.0.0.1:5300"},
			upstreams: []string{"udp://localhost:5300"},
			wantLoop:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Listen = tt.listen
			cfg.Upstreams = tt.upstreams
			cfg.Bootstrap = tt.bootstrap
			cfg.Lists.Sources = withSource(func(s *Source) { s.Enabled = true })

			loops := cfg.LoopWarnings()
			if len(loops) != tt.wantLoop {
				t.Fatalf("LoopWarnings() = %v (len %d), want len %d", loops, len(loops), tt.wantLoop)
			}
			warnings := cfg.Warnings()
			if len(warnings) != tt.wantLoop {
				t.Fatalf("Warnings() = %v (len %d), want len %d", warnings, len(warnings), tt.wantLoop)
			}
		})
	}
}
