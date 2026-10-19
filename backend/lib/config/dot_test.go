package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const secretName = "k7x9p2qm4t8v3w6z.hecatedns.duckdns.org"

func dotConfig() Config {
	c := Default()
	c.Backend = BackendNative
	c.Listen = nil
	c.DoT = DoT{
		Enabled:     true,
		Listen:      []string{"0.0.0.0:853", "[::]:853"},
		CertFile:    "/etc/hecate/tls/cert.pem",
		KeyFile:     "/etc/hecate/tls/key.pem",
		Names:       []string{secretName},
		ReloadEvery: 10 * time.Minute,
	}
	return c
}

func TestDoT_IsOffByDefaultAndTheDefaultConfigStaysValid(t *testing.T) {
	c := Default()

	if c.DoT.Enabled {
		t.Error("DoT is enabled in the default config")
	}
	if c.DoT.ReloadEvery != 10*time.Minute {
		t.Errorf("default dot.reload_every = %v, want 10m", c.DoT.ReloadEvery)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("the default config is invalid: %v", err)
	}
}

func TestDoT_ServerWithoutPlainListenersIsValid(t *testing.T) {
	if err := dotConfig().Validate(); err != nil {
		t.Errorf("a DoT-only native config is invalid: %v", err)
	}
}

func TestDoT_PlainListenIsStillRequiredWhenDoTIsOff(t *testing.T) {
	c := dotConfig()
	c.DoT.Enabled = false

	err := c.Validate()

	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Errorf("Validate = %v, want a listen error: without DoT at least one plain address is needed", err)
	}
}

func TestDoT_DisabledSettingsAreNotChecked(t *testing.T) {
	c := Default()
	c.DoT = DoT{Enabled: false, Names: []string{"NOT A NAME"}, ReloadEvery: -1}

	if err := c.Validate(); err != nil {
		t.Errorf("settings of a disabled DoT were validated: %v", err)
	}
}

func TestDoT_RejectsWhatCannotWork(t *testing.T) {
	long := strings.Repeat("a", 64) + ".example.org"
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"dnsproxy backend", func(c *Config) { c.Backend = BackendDNSProxy }, "dot: needs backend native"},
		{"no listen address", func(c *Config) { c.DoT.Listen = nil }, "dot.listen"},
		{"listen hostname", func(c *Config) { c.DoT.Listen = []string{"localhost:853"} }, "dot.listen[0]"},
		{"listen port zero", func(c *Config) { c.DoT.Listen = []string{"0.0.0.0:0"} }, "dot.listen[0]"},
		{"listen twice", func(c *Config) { c.DoT.Listen = []string{"0.0.0.0:853", "0.0.0.0:853"} }, "dot.listen[1]"},
		{"same address as plain listen", func(c *Config) { c.Listen = []string{"0.0.0.0:853"} }, "dot.listen[0]"},
		{"no certificate file", func(c *Config) { c.DoT.CertFile = "" }, "dot.cert_file"},
		{"no key file", func(c *Config) { c.DoT.KeyFile = "" }, "dot.key_file"},
		{"no names", func(c *Config) { c.DoT.Names = nil }, "dot.names"},
		{"uppercase name", func(c *Config) { c.DoT.Names = []string{"Secret.Example.Org"} }, "dot.names[0]"},
		{"wildcard name", func(c *Config) { c.DoT.Names = []string{"*.example.org"} }, "dot.names[0]"},
		{"IP address as a name", func(c *Config) { c.DoT.Names = []string{"192.0.2.1"} }, "dot.names[0]"},
		{"single label name", func(c *Config) { c.DoT.Names = []string{"localhost"} }, "dot.names[0]"},
		{"trailing dot", func(c *Config) { c.DoT.Names = []string{"secret.example.org."} }, "dot.names[0]"},
		{"empty label", func(c *Config) { c.DoT.Names = []string{"secret..example.org"} }, "dot.names[0]"},
		{"underscore", func(c *Config) { c.DoT.Names = []string{"se_cret.example.org"} }, "dot.names[0]"},
		{"label starts with a hyphen", func(c *Config) { c.DoT.Names = []string{"-secret.example.org"} }, "dot.names[0]"},
		{"label longer than 63", func(c *Config) { c.DoT.Names = []string{long} }, "dot.names[0]"},
		{"duplicate name", func(c *Config) { c.DoT.Names = []string{secretName, secretName} }, "dot.names[1]"},
		{"reload every zero", func(c *Config) { c.DoT.ReloadEvery = 0 }, "dot.reload_every"},
		{"reload every below a second", func(c *Config) { c.DoT.ReloadEvery = time.Millisecond }, "dot.reload_every"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dotConfig()
			tt.mutate(&c)

			err := c.Validate()

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate = %v, want an error mentioning %q", err, tt.want)
			}
		})
	}
}

func TestDoT_ValidationNeverRepeatsAName(t *testing.T) {
	typo := "K7x9p2qm4t8v3w6z.example.org"
	tests := []struct {
		name  string
		names []string
		leak  string
	}{
		{"mistyped name", []string{typo}, typo},
		{"duplicate name", []string{secretName, secretName}, secretName},
		{"wildcard name", []string{"*.k7x9p2qm4t8v3w6z.example.org"}, "k7x9p2qm4t8v3w6z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dotConfig()
			c.DoT.Names = tt.names

			err := c.Validate()

			if err == nil {
				t.Fatal("Validate accepted the names")
			}
			if strings.Contains(err.Error(), tt.leak) {
				t.Errorf("the error repeats the name: %v", err)
			}
		})
	}
}

const vpsExamplePath = "../../../configs/hecate.vps.example.yaml"

func TestLoad_VPSExampleConfigServesOnlyDoTWithAbsolutePaths(t *testing.T) {
	cfg, err := Load(vpsExamplePath)
	if err != nil {
		t.Fatalf("Load VPS example: %v", err)
	}
	raw, err := os.ReadFile(vpsExamplePath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Backend != BackendNative || len(cfg.Listen) != 0 || !cfg.DoT.Enabled {
		t.Errorf("backend %q, listen %v, dot enabled %v; want native, no plain listener and DoT on", cfg.Backend, cfg.Listen, cfg.DoT.Enabled)
	}
	for _, want := range []string{
		"data_dir: /var/lib/hecate\n", "file: /var/lib/hecate/rules.txt\n", "dir: /var/lib/hecate/logs\n",
		"cert_file: /var/lib/hecate/lego/certificates/", "key_file: /var/lib/hecate/lego/certificates/",
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the VPS example does not contain %q; paths must be absolute so they do not depend on the read-only config directory", strings.TrimSpace(want))
		}
	}
	if len(cfg.AllowedClients) != 2 || cfg.AllowedClients[0] != "0.0.0.0/0" || cfg.AllowedClients[1] != "::/0" {
		t.Errorf("allowed_clients = %v, want everyone: the server name is the key on the internet", cfg.AllowedClients)
	}
}

func TestLoad_ReadsAndResolvesTheDoTSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hecate.yaml")
	yaml := "backend: native\nlisten: []\ndot:\n  enabled: true\n  listen: [\"0.0.0.0:853\"]\n  cert_file: tls/cert.pem\n  key_file: tls/key.pem\n  names: [" + secretName + "]\n  reload_every: 30m\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)

	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.DoT.Enabled || c.DoT.ReloadEvery != 30*time.Minute || len(c.DoT.Names) != 1 || c.DoT.Names[0] != secretName {
		t.Errorf("DoT = %+v, want the values from the file", c.DoT)
	}
	if want := filepath.Join(dir, "tls", "cert.pem"); c.DoT.CertFile != want {
		t.Errorf("cert_file = %q, want %q, resolved against the config directory", c.DoT.CertFile, want)
	}
	if want := filepath.Join(dir, "tls", "key.pem"); c.DoT.KeyFile != want {
		t.Errorf("key_file = %q, want %q", c.DoT.KeyFile, want)
	}
}
