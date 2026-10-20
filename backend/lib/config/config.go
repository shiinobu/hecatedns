package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	BackendDNSProxy = "dnsproxy"
	BackendNative   = "native"
)

const (
	ModeNullIP   = "null_ip"
	ModeNXDomain = "nxdomain"
	ModeNoData   = "nodata"
	ModeRefused  = "refused"
)

const (
	KindBlock     = "block"
	KindAllow     = "allow"
	FormatDomains = "domains"
	FormatHosts   = "hosts"
)

const (
	defaultListen         = "127.0.0.1:5300"
	defaultCacheSizeMB    = 16
	defaultCacheMaxTTL    = time.Hour
	defaultBlockTTL       = 10 * time.Second
	defaultUpdateInterval = 24 * time.Hour
	defaultPollInterval   = 2 * time.Second
	defaultRetentionDays  = 7
	defaultLogMaxMBPerDay = 256
	defaultDoTReloadEvery = 10 * time.Minute
	defaultSessionDays    = 30
	defaultDataDir        = "data"
	defaultRulesFile      = "data/rules.txt"
	defaultLogDir         = "data/logs"

	utf8BOM = "\xef\xbb\xbf"
)

type Config struct {
	Backend        string       `yaml:"backend"`
	Listen         []string     `yaml:"listen"`
	AllowedClients []string     `yaml:"allowed_clients"`
	Upstreams      []string     `yaml:"upstreams"`
	Bootstrap      []string     `yaml:"bootstrap"`
	Cache          Cache        `yaml:"cache"`
	Block          Block        `yaml:"block"`
	SpecialNames   SpecialNames `yaml:"special_names"`
	Lists          Lists        `yaml:"lists"`
	Rules          Rules        `yaml:"rules"`
	Log            Log          `yaml:"log"`
	DoT            DoT          `yaml:"dot"`
	Dashboard      Dashboard    `yaml:"dashboard"`
	DataDir        string       `yaml:"data_dir"`
}

type Dashboard struct {
	Enabled      bool       `yaml:"enabled"`
	Listen       []string   `yaml:"listen"`
	Names        []string   `yaml:"names"`
	ProbeDomains []string   `yaml:"probe_domains"`
	Certs        []CertPair `yaml:"certs"`
	KeyHashFile  string     `yaml:"key_hash_file"`
	SecretFile   string     `yaml:"secret_file"`
	SessionDays  int        `yaml:"session_days"`
}

type CertPair struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

func (d Dashboard) EffectiveCerts(dot DoT) []CertPair {
	if len(d.Certs) > 0 {
		return d.Certs
	}
	if dot.Enabled && dot.CertFile != "" && dot.KeyFile != "" {
		return []CertPair{{CertFile: dot.CertFile, KeyFile: dot.KeyFile}}
	}
	return nil
}

type DoT struct {
	Enabled     bool          `yaml:"enabled"`
	Listen      []string      `yaml:"listen"`
	CertFile    string        `yaml:"cert_file"`
	KeyFile     string        `yaml:"key_file"`
	Names       []string      `yaml:"names"`
	ReloadEvery time.Duration `yaml:"reload_every"`
}

type Cache struct {
	Enabled bool          `yaml:"enabled"`
	SizeMB  int           `yaml:"size_mb"`
	MinTTL  time.Duration `yaml:"min_ttl"`
	MaxTTL  time.Duration `yaml:"max_ttl"`
}

type Block struct {
	Mode string        `yaml:"mode"`
	TTL  time.Duration `yaml:"ttl"`
}

type SpecialNames struct {
	FirefoxCanary bool `yaml:"firefox_canary"`
	DDR           bool `yaml:"ddr"`
}

type Lists struct {
	UpdateInterval time.Duration `yaml:"update_interval"`
	Sources        []Source      `yaml:"sources"`
}

type Source struct {
	Name    string `yaml:"name"`
	URL     string `yaml:"url"`
	Kind    string `yaml:"kind"`
	Format  string `yaml:"format"`
	Enabled bool   `yaml:"enabled"`
}

var sourceKeys = map[string]bool{"name": true, "url": true, "kind": true, "format": true, "enabled": true}

func (s *Source) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			if !sourceKeys[key.Value] {
				return fmt.Errorf("line %d: field %s not found in type config.Source", key.Line, key.Value)
			}
		}
	}
	type plain Source
	decoded := plain{Enabled: true}
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*s = Source(decoded)
	return nil
}

type Rules struct {
	File         string        `yaml:"file"`
	PollInterval time.Duration `yaml:"poll_interval"`
}

type Log struct {
	Enabled       bool   `yaml:"enabled"`
	Dir           string `yaml:"dir"`
	RetentionDays int    `yaml:"retention_days"`
	MaxMBPerDay   int    `yaml:"max_mb_per_day"`
}

func Default() Config {
	return Config{
		Backend:        BackendDNSProxy,
		Listen:         []string{defaultListen},
		AllowedClients: []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
		Upstreams:      []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"},
		Bootstrap:      []string{"1.1.1.1", "8.8.8.8"},
		Cache:          Cache{Enabled: true, SizeMB: defaultCacheSizeMB, MaxTTL: defaultCacheMaxTTL},
		Block:          Block{Mode: ModeNullIP, TTL: defaultBlockTTL},
		SpecialNames:   SpecialNames{FirefoxCanary: true, DDR: true},
		Lists:          Lists{UpdateInterval: defaultUpdateInterval},
		Rules:          Rules{File: defaultRulesFile, PollInterval: defaultPollInterval},
		Log:            Log{Enabled: true, Dir: defaultLogDir, RetentionDays: defaultRetentionDays, MaxMBPerDay: defaultLogMaxMBPerDay},
		DoT:            DoT{ReloadEvery: defaultDoTReloadEvery},
		Dashboard:      Dashboard{SessionDays: defaultSessionDays},
		DataDir:        defaultDataDir,
	}
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	cfg := Default()
	if err := decodeStrict(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	baseDir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	resolvePaths(&cfg, baseDir)
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

func decodeStrict(data []byte, cfg *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte(utf8BOM))))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	var extra yaml.Node
	switch err := dec.Decode(&extra); {
	case err == nil:
		return errors.New("multiple YAML documents are not supported, remove the extra '---' section")
	case errors.Is(err, io.EOF):
		return nil
	default:
		return err
	}
}

func resolvePaths(cfg *Config, baseDir string) {
	cfg.DataDir = absFrom(baseDir, cfg.DataDir)
	cfg.Rules.File = absFrom(baseDir, cfg.Rules.File)
	cfg.Log.Dir = absFrom(baseDir, cfg.Log.Dir)
	cfg.DoT.CertFile = absFrom(baseDir, cfg.DoT.CertFile)
	cfg.DoT.KeyFile = absFrom(baseDir, cfg.DoT.KeyFile)
	cfg.Dashboard.KeyHashFile = absFrom(baseDir, cfg.Dashboard.KeyHashFile)
	cfg.Dashboard.SecretFile = absFrom(baseDir, cfg.Dashboard.SecretFile)
	for i, pair := range cfg.Dashboard.Certs {
		cfg.Dashboard.Certs[i] = CertPair{CertFile: absFrom(baseDir, pair.CertFile), KeyFile: absFrom(baseDir, pair.KeyFile)}
	}
}

func absFrom(base, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}
