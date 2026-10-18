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

// Backend names accepted by the backend setting.
const (
	BackendDNSProxy = "dnsproxy"
	BackendNative   = "native"
)

// Block modes: the shape of the answer returned for a blocked name.
const (
	ModeNullIP   = "null_ip"
	ModeNXDomain = "nxdomain"
	ModeNoData   = "nodata"
	ModeRefused  = "refused"
)

// Blocklist source kinds and formats.
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
	defaultDataDir        = "data"
	defaultRulesFile      = "data/rules.txt"
	defaultLogDir         = "data/logs"

	utf8BOM = "\xef\xbb\xbf"
)

// Config is the complete xdns configuration. Keys omitted from the YAML file
// keep the values from Default().
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
	DataDir        string       `yaml:"data_dir"`
}

// Cache configures the answer cache of the selected backend.
type Cache struct {
	Enabled bool          `yaml:"enabled"`
	SizeMB  int           `yaml:"size_mb"`
	MinTTL  time.Duration `yaml:"min_ttl"`
	MaxTTL  time.Duration `yaml:"max_ttl"`
}

// Block configures the answer returned for a blocked name.
type Block struct {
	Mode string        `yaml:"mode"`
	TTL  time.Duration `yaml:"ttl"`
}

// SpecialNames toggles the handling of names that steer client behaviour.
type SpecialNames struct {
	FirefoxCanary bool `yaml:"firefox_canary"`
	DDR           bool `yaml:"ddr"`
}

// Lists configures the blocklist sources and how often they are refreshed.
type Lists struct {
	UpdateInterval time.Duration `yaml:"update_interval"`
	Sources        []Source      `yaml:"sources"`
}

// Source is one downloadable blocklist.
type Source struct {
	Name    string `yaml:"name"`
	URL     string `yaml:"url"`
	Kind    string `yaml:"kind"`
	Format  string `yaml:"format"`
	Enabled bool   `yaml:"enabled"`
}

// sourceKeys are the keys a list source may have; UnmarshalYAML rejects any
// other, because its own decoding step is not strict.
var sourceKeys = map[string]bool{"name": true, "url": true, "kind": true, "format": true, "enabled": true}

// UnmarshalYAML decodes a source in which an omitted enabled key means true: a
// list somebody wrote down is meant to be used, and a silent false would leave
// the server without protection. A node.Decode call is not strict, so unknown
// keys are rejected here by hand; duplicate keys are still caught by the decoder.
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

// Rules configures the custom allow/block rules file.
type Rules struct {
	File         string        `yaml:"file"`
	PollInterval time.Duration `yaml:"poll_interval"`
}

// Log configures the query log.
type Log struct {
	Enabled       bool   `yaml:"enabled"`
	Dir           string `yaml:"dir"`
	RetentionDays int    `yaml:"retention_days"`
}

// Default returns the built-in configuration. Every call returns fresh
// slices, so callers may modify the result freely.
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
		Log:            Log{Enabled: true, Dir: defaultLogDir, RetentionDays: defaultRetentionDays},
		DataDir:        defaultDataDir,
	}
}

// Load reads the YAML file at path, decodes it strictly over Default() (an
// unknown or duplicated key is an error), resolves relative paths against
// the directory that contains the file, and validates the result.
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

// decodeStrict decodes the single YAML document in data into cfg, tolerating
// a UTF-8 BOM and treating an empty document as "no overrides". A second
// document is an error, because its settings would be silently ignored.
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

// resolvePaths makes the relative path settings absolute against baseDir.
func resolvePaths(cfg *Config, baseDir string) {
	cfg.DataDir = absFrom(baseDir, cfg.DataDir)
	cfg.Rules.File = absFrom(baseDir, cfg.Rules.File)
	cfg.Log.Dir = absFrom(baseDir, cfg.Log.Dir)
}

// absFrom joins p onto base unless p is empty or already absolute.
func absFrom(base, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}
