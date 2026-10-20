package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const dashboardName = "q3w8e5r2t7y4u9i6o1p0.ghost.example.org"

func dashboardConfig() Config {
	c := Default()
	c.Dashboard = Dashboard{
		Enabled:      true,
		Listen:       []string{"0.0.0.0:443", "[::]:443"},
		Names:        []string{dashboardName},
		ProbeDomains: []string{"ghost.example.org"},
		Certs:        []CertPair{{CertFile: "/etc/hecate/tls/cert.pem", KeyFile: "/etc/hecate/tls/key.pem"}},
		KeyHashFile:  "/var/lib/hecate/dashboard/key.sha256",
		SecretFile:   "/var/lib/hecate/dashboard/cookie.secret",
		SessionDays:  30,
	}
	return c
}

func TestDashboard_IsOffByDefaultAndTheDefaultConfigStaysValid(t *testing.T) {
	c := Default()

	if c.Dashboard.Enabled {
		t.Error("the dashboard is enabled in the default config")
	}
	if c.Dashboard.SessionDays != 30 {
		t.Errorf("default dashboard.session_days = %d, want 30", c.Dashboard.SessionDays)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("the default config is invalid: %v", err)
	}
}

func TestDashboard_DisabledSettingsAreNotChecked(t *testing.T) {
	c := Default()
	c.Dashboard = Dashboard{Listen: []string{"not an address"}, SessionDays: 400}

	if err := c.Validate(); err != nil {
		t.Errorf("a disabled dashboard was validated: %v", err)
	}
}

func TestDashboard_AcceptsTheVPSSetup(t *testing.T) {
	if err := dashboardConfig().Validate(); err != nil {
		t.Errorf("the VPS setup is invalid: %v", err)
	}
}

func TestDashboard_AcceptsALoopbackSetupWithoutNamesOrCertificates(t *testing.T) {
	c := Default()
	c.Dashboard = Dashboard{
		Enabled:     true,
		Listen:      []string{"127.0.0.1:8443"},
		KeyHashFile: "data/dashboard/key.sha256",
		SecretFile:  "data/dashboard/cookie.secret",
		SessionDays: 7,
	}

	if err := c.Validate(); err != nil {
		t.Errorf("the PC setup is invalid: %v", err)
	}
}

func TestDashboard_Rejects(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{"no listen address", func(c *Config) { c.Dashboard.Listen = nil }, "dashboard.listen: at least one address"},
		{"hostname as listen", func(c *Config) { c.Dashboard.Listen = []string{"localhost:443"} }, "dashboard.listen[0]"},
		{"port zero", func(c *Config) { c.Dashboard.Listen = []string{"0.0.0.0:0"} }, "port"},
		{"listen twice", func(c *Config) { c.Dashboard.Listen = []string{"0.0.0.0:443", "0.0.0.0:443"} }, "listed twice"},
		{"shares the plain DNS address", func(c *Config) { c.Listen = []string{"0.0.0.0:443"} }, "also in listen"},
		{"shares the DoT address", func(c *Config) {
			c.Backend = BackendNative
			c.DoT = DoT{Enabled: true, Listen: []string{"0.0.0.0:443"}, CertFile: "c", KeyFile: "k", Names: []string{"k7x9p2qm4t8v3w6z.example.org"}, ReloadEvery: c.DoT.ReloadEvery}
		}, "also in dot.listen"},
		{"public listen without names", func(c *Config) { c.Dashboard.Names = nil }, "dashboard.names: at least one"},
		{"uppercase name", func(c *Config) { c.Dashboard.Names = []string{"Ghost.Example.org"} }, "dashboard.names[0]"},
		{"wildcard name", func(c *Config) { c.Dashboard.Names = []string{"*.ghost.example.org"} }, "dashboard.names[0]"},
		{"IP as name", func(c *Config) { c.Dashboard.Names = []string{"203.0.113.9"} }, "dashboard.names[0]"},
		{"name twice", func(c *Config) { c.Dashboard.Names = []string{dashboardName, dashboardName} }, "listed twice"},
		{"bad probe domain", func(c *Config) { c.Dashboard.ProbeDomains = []string{"localhost"} }, "dashboard.probe_domains[0]"},
		{"probe domain twice", func(c *Config) { c.Dashboard.ProbeDomains = []string{"ghost.example.org", "ghost.example.org"} }, "dashboard.probe_domains[1]: is listed twice"},
		{"public listen without any certificate", func(c *Config) { c.Dashboard.Certs = nil }, "needs a certificate"},
		{"certificate without key", func(c *Config) { c.Dashboard.Certs = []CertPair{{CertFile: "c.pem"}} }, "dashboard.certs[0].key_file"},
		{"certificate without file", func(c *Config) { c.Dashboard.Certs = []CertPair{{KeyFile: "k.pem"}} }, "dashboard.certs[0].cert_file"},
		{"no key hash file", func(c *Config) { c.Dashboard.KeyHashFile = "" }, "dashboard.key_hash_file"},
		{"no secret file", func(c *Config) { c.Dashboard.SecretFile = "" }, "dashboard.secret_file"},
		{"session of zero days", func(c *Config) { c.Dashboard.SessionDays = 0 }, "dashboard.session_days"},
		{"session over 90 days", func(c *Config) { c.Dashboard.SessionDays = 91 }, "dashboard.session_days"},
		{"ops without a result directory", func(c *Config) { c.Dashboard.Ops = DashboardOps{RequestDir: "/run/hecate-ops/req"} }, "dashboard.ops.result_dir"},
		{"ops without a request directory", func(c *Config) { c.Dashboard.Ops = DashboardOps{ResultDir: "/var/lib/hecate-ops/result"} }, "dashboard.ops.request_dir"},
		{"ops directories the same", func(c *Config) { c.Dashboard.Ops = DashboardOps{RequestDir: "/run/ops", ResultDir: "/run/ops/"} }, "dashboard.ops: request_dir and result_dir must differ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dashboardConfig()
			tt.edit(&c)

			err := c.Validate()

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestDashboard_AcceptsOpsDirectoriesOrNone(t *testing.T) {
	c := dashboardConfig()
	if err := c.Validate(); err != nil {
		t.Errorf("no ops directories: %v", err)
	}
	c.Dashboard.Ops = DashboardOps{RequestDir: "/run/hecate-ops/req", ResultDir: "/var/lib/hecate-ops/result"}
	if err := c.Validate(); err != nil {
		t.Errorf("both ops directories: %v", err)
	}
}

func TestDashboard_ReportsAllProblemsAtOnce(t *testing.T) {
	c := dashboardConfig()
	c.Dashboard.Listen = nil
	c.Dashboard.KeyHashFile = ""
	c.Dashboard.SessionDays = 0

	err := c.Validate()

	if err == nil {
		t.Fatal("Validate accepted three problems")
	}
	for _, want := range []string{"dashboard.listen", "dashboard.key_hash_file", "dashboard.session_days"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate = %v, missing %q", err, want)
		}
	}
}

func TestDashboard_UsesTheDoTCertificateWhenCertsAreEmpty(t *testing.T) {
	c := dashboardConfig()
	c.Dashboard.Certs = nil
	c.Backend = BackendNative
	c.Listen = nil
	c.DoT = DoT{Enabled: true, Listen: []string{"0.0.0.0:853"}, CertFile: "/dot/cert.pem", KeyFile: "/dot/key.pem", Names: []string{"k7x9p2qm4t8v3w6z.example.org"}, ReloadEvery: c.DoT.ReloadEvery}

	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	got := c.Dashboard.EffectiveCerts(c.DoT)
	if len(got) != 1 || got[0].CertFile != "/dot/cert.pem" || got[0].KeyFile != "/dot/key.pem" {
		t.Errorf("EffectiveCerts = %+v, want the DoT pair", got)
	}
}

func TestDashboard_OwnCertsWinOverTheDoTPair(t *testing.T) {
	c := dashboardConfig()
	dot := DoT{Enabled: true, CertFile: "/dot/cert.pem", KeyFile: "/dot/key.pem"}

	got := c.Dashboard.EffectiveCerts(dot)

	if len(got) != 1 || got[0].CertFile != "/etc/hecate/tls/cert.pem" {
		t.Errorf("EffectiveCerts = %+v, want the dashboard's own pair", got)
	}
}

func TestDashboard_WarnsAboutAShortFirstLabel(t *testing.T) {
	c := dashboardConfig()
	c.Dashboard.Names = []string{"ghost.example.org"}

	var found bool
	for _, w := range c.Warnings() {
		found = found || strings.Contains(w, "dashboard.names[0]")
	}

	if !found {
		t.Errorf("Warnings = %v, want a short-label warning for dashboard.names[0]", c.Warnings())
	}
	if warnings := dashboardConfig().Warnings(); containsPart(warnings, "dashboard.names") {
		t.Errorf("a long label produced a warning: %v", warnings)
	}
}

func containsPart(list []string, part string) bool {
	for _, s := range list {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

func TestLoad_ResolvesDashboardPathsAndRejectsUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	yaml := "dashboard:\n  enabled: true\n  listen: [\"127.0.0.1:8443\"]\n  key_hash_file: dash/key.sha256\n  secret_file: dash/cookie.secret\n  certs:\n    - cert_file: tls/c.pem\n      key_file: tls/k.pem\n  ops:\n    request_dir: ops/req\n    result_dir: ops/result\n"
	path := filepath.Join(dir, "hecate.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)

	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	d := cfg.Dashboard
	if d.KeyHashFile != filepath.Join(dir, "dash", "key.sha256") || d.SecretFile != filepath.Join(dir, "dash", "cookie.secret") {
		t.Errorf("paths not resolved: %q %q", d.KeyHashFile, d.SecretFile)
	}
	if len(d.Certs) != 1 || d.Certs[0].CertFile != filepath.Join(dir, "tls", "c.pem") || d.Certs[0].KeyFile != filepath.Join(dir, "tls", "k.pem") {
		t.Errorf("certificate paths not resolved: %+v", d.Certs)
	}
	if d.SessionDays != 30 {
		t.Errorf("session_days = %d, want the default 30", d.SessionDays)
	}
	if d.Ops.RequestDir != filepath.Join(dir, "ops", "req") || d.Ops.ResultDir != filepath.Join(dir, "ops", "result") {
		t.Errorf("ops paths not resolved: %+v", d.Ops)
	}

	if err := os.WriteFile(path, []byte("dashboard:\n  session_day: 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("an unknown dashboard key was accepted")
	}
}
