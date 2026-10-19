package config

import (
	"errors"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxTTLSeconds = math.MaxInt32

const maxSourceNameLen = 64

const (
	defaultPortDNS = 53
	defaultPortDoT = 853
	defaultPortDoH = 443
)

func (c Config) Validate() error {
	var errs []error
	errs = append(errs, c.validateBackend()...)
	errs = append(errs, c.validateListen()...)
	errs = append(errs, c.validateDoT()...)
	errs = append(errs, c.validateClients()...)
	errs = append(errs, c.validateUpstreams()...)
	errs = append(errs, c.validateBlock()...)
	errs = append(errs, c.validateCache()...)
	errs = append(errs, c.validateLists()...)
	errs = append(errs, c.validateRules()...)
	errs = append(errs, c.validateLog()...)
	errs = append(errs, c.validateDataDir()...)
	return errors.Join(errs...)
}

func (c Config) Warnings() []string {
	var out []string
	hasBlockSource := false
	for _, s := range c.Lists.Sources {
		if s.Enabled && s.Kind == KindBlock {
			hasBlockSource = true
			break
		}
	}
	if !hasBlockSource {
		out = append(out, "no block list source is enabled: only custom rules will block")
	}
	out = append(out, c.LoopWarnings()...)
	out = append(out, c.openResolverWarnings()...)
	out = append(out, c.dotWarnings()...)
	return out
}

func (c Config) LoopWarnings() []string {
	var listens []netip.AddrPort
	for _, l := range c.Listen {
		if ap, err := netip.ParseAddrPort(l); err == nil {
			listens = append(listens, netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()))
		}
	}
	if len(listens) == 0 {
		return nil
	}
	var out []string
	for _, u := range c.Upstreams {
		target, ok := upstreamAddrPort(u)
		if !ok {
			continue
		}
		if matchesAnyListen(listens, target) {
			out = append(out, fmt.Sprintf("upstream %q equals a listen address: queries would loop back to this server", u))
		}
	}
	for _, b := range c.Bootstrap {
		ip, err := netip.ParseAddr(b)
		if err != nil {
			continue
		}
		target := netip.AddrPortFrom(ip.Unmap(), defaultPortDNS)
		if matchesAnyListen(listens, target) {
			out = append(out, fmt.Sprintf("bootstrap %q (port 53) equals a listen address: bootstrap resolution would loop back to this server", b))
		}
	}
	return out
}

func matchesAnyListen(listens []netip.AddrPort, target netip.AddrPort) bool {
	for _, l := range listens {
		if l.Port() != target.Port() {
			continue
		}
		if l.Addr() == target.Addr() {
			return true
		}
		if (l.Addr().IsUnspecified() || l.Addr().IsLoopback()) &&
			(target.Addr().IsUnspecified() || target.Addr().IsLoopback()) {
			return true
		}
	}
	return false
}

func upstreamAddrPort(u string) (netip.AddrPort, bool) {
	if ip, err := netip.ParseAddr(u); err == nil {
		return netip.AddrPortFrom(ip.Unmap(), defaultPortDNS), true
	}
	if ap, err := netip.ParseAddrPort(u); err == nil {
		return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()), true
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return netip.AddrPort{}, false
	}
	host := parsed.Hostname()
	var ip netip.Addr
	if strings.EqualFold(host, "localhost") {
		ip = netip.AddrFrom4([4]byte{127, 0, 0, 1})
	} else {
		ip, err = netip.ParseAddr(host)
		if err != nil {
			return netip.AddrPort{}, false
		}
	}
	var port uint16
	switch parsed.Scheme {
	case "udp", "tcp":
		port = defaultPortDNS
	case "tls":
		port = defaultPortDoT
	case "https":
		port = defaultPortDoH
	default:
		return netip.AddrPort{}, false
	}
	if p := parsed.Port(); p != "" {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil || n == 0 {
			return netip.AddrPort{}, false
		}
		port = uint16(n)
	}
	return netip.AddrPortFrom(ip.Unmap(), port), true
}

func (c Config) validateBackend() []error {
	if c.Backend == BackendDNSProxy || c.Backend == BackendNative {
		return nil
	}
	return []error{fmt.Errorf("backend: %q is not %q or %q", c.Backend, BackendDNSProxy, BackendNative)}
}

func (c Config) validateListen() []error {
	if len(c.Listen) == 0 {
		if c.DoT.Enabled {
			return nil
		}
		return []error{errors.New("listen: at least one address is required")}
	}
	var errs []error
	seen := make(map[netip.AddrPort]bool, len(c.Listen))
	for i, addr := range c.Listen {
		parsed, err := netip.ParseAddrPort(addr)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("listen[%d]: %q must be IP:port, hostnames are not allowed: %w", i, addr, err))
		case parsed.Port() == 0:
			errs = append(errs, fmt.Errorf("listen[%d]: %q must name a port, 0 is not allowed", i, addr))
		case seen[parsed]:
			errs = append(errs, fmt.Errorf("listen[%d]: %q is listed twice", i, addr))
		}
		seen[parsed] = true
	}
	return errs
}

func (c Config) validateClients() []error {
	if len(c.AllowedClients) == 0 {
		return []error{errors.New("allowed_clients: at least one CIDR is required, use 0.0.0.0/0 and ::/0 to allow everyone")}
	}
	var errs []error
	for i, cidr := range c.AllowedClients {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			errs = append(errs, fmt.Errorf("allowed_clients[%d]: %q is not a CIDR: %w", i, cidr, err))
		}
	}
	return errs
}

func (c Config) validateUpstreams() []error {
	if len(c.Upstreams) == 0 {
		return []error{errors.New("upstreams: at least one upstream is required")}
	}
	var errs []error
	needsBootstrap := false
	for i, u := range c.Upstreams {
		named, err := checkUpstream(u)
		if err != nil {
			errs = append(errs, fmt.Errorf("upstreams[%d]: %q: %w", i, u, err))
			continue
		}
		needsBootstrap = needsBootstrap || named
	}
	if needsBootstrap && len(c.Bootstrap) == 0 {
		errs = append(errs, errors.New("bootstrap: at least one IP is required when an upstream is given by hostname"))
	}
	for i, b := range c.Bootstrap {
		if _, err := netip.ParseAddr(b); err != nil {
			errs = append(errs, fmt.Errorf("bootstrap[%d]: %q is not an IP address", i, b))
		}
	}
	return errs
}

func checkUpstream(u string) (named bool, err error) {
	if _, err := netip.ParseAddr(u); err == nil {
		return false, nil
	}
	if _, err := netip.ParseAddrPort(u); err == nil {
		return false, nil
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return false, err
	}
	switch parsed.Scheme {
	case "udp", "tcp", "tls", "https":
	default:
		return false, fmt.Errorf("unsupported scheme %q, use udp, tcp, tls or https", parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return false, errors.New("missing host")
	}
	_, ipErr := netip.ParseAddr(host)
	return ipErr != nil, nil
}

func (c Config) validateBlock() []error {
	var errs []error
	switch c.Block.Mode {
	case ModeNullIP, ModeNXDomain, ModeNoData, ModeRefused:
	default:
		errs = append(errs, fmt.Errorf("block.mode: %q is not one of %s, %s, %s, %s",
			c.Block.Mode, ModeNullIP, ModeNXDomain, ModeNoData, ModeRefused))
	}
	errs = append(errs, checkTTL("block.ttl", c.Block.TTL)...)
	return errs
}

func checkTTL(field string, ttl time.Duration) []error {
	switch {
	case ttl < 0:
		return []error{fmt.Errorf("%s: must not be negative", field)}
	case ttl > time.Duration(maxTTLSeconds)*time.Second:
		return []error{fmt.Errorf("%s: %v is larger than the largest DNS TTL, %ds", field, ttl, maxTTLSeconds)}
	}
	return nil
}

func (c Config) validateCache() []error {
	if !c.Cache.Enabled {
		return nil
	}
	var errs []error
	if c.Cache.SizeMB < 1 {
		errs = append(errs, errors.New("cache.size_mb: must be at least 1"))
	}
	errs = append(errs, checkTTL("cache.min_ttl", c.Cache.MinTTL)...)
	errs = append(errs, checkTTL("cache.max_ttl", c.Cache.MaxTTL)...)
	if c.Cache.MaxTTL > 0 && c.Cache.MaxTTL < c.Cache.MinTTL {
		errs = append(errs, errors.New("cache.min_ttl: must not exceed cache.max_ttl"))
	}
	return errs
}

func (c Config) validateLists() []error {
	var errs []error
	if len(c.Lists.Sources) > 0 && c.Lists.UpdateInterval <= 0 {
		errs = append(errs, errors.New("lists.update_interval: must be positive when sources are configured"))
	}
	seen := make(map[string]bool, len(c.Lists.Sources))
	for i, s := range c.Lists.Sources {
		key := strings.ToLower(s.Name)
		errs = append(errs, validateSource(i, s, seen[key])...)
		seen[key] = true
	}
	return errs
}

func validateSource(i int, s Source, duplicate bool) []error {
	field := func(name string) string { return fmt.Sprintf("lists.sources[%d].%s", i, name) }
	var errs []error
	switch {
	case s.Name == "":
		errs = append(errs, fmt.Errorf("%s: must not be empty", field("name")))
	case !validSourceName(s.Name):
		errs = append(errs, fmt.Errorf("%s: %q must be 1-%d letters, digits, '-' or '_' starting with a letter or digit, and not a reserved Windows device name",
			field("name"), s.Name, maxSourceNameLen))
	case duplicate:
		errs = append(errs, fmt.Errorf("%s: duplicate name %q", field("name"), s.Name))
	}
	if u, err := url.Parse(s.URL); err != nil || u.Scheme != "https" || u.Host == "" {
		errs = append(errs, fmt.Errorf("%s: %q must be an https URL", field("url"), s.URL))
	}
	if s.Kind != KindBlock && s.Kind != KindAllow {
		errs = append(errs, fmt.Errorf("%s: %q is not %q or %q", field("kind"), s.Kind, KindBlock, KindAllow))
	}
	if s.Format != FormatDomains && s.Format != FormatHosts {
		errs = append(errs, fmt.Errorf("%s: %q is not %q or %q", field("format"), s.Format, FormatDomains, FormatHosts))
	}
	return errs
}

func (c Config) validateRules() []error {
	var errs []error
	if c.Rules.File == "" {
		errs = append(errs, errors.New("rules.file: must not be empty"))
	}
	if c.Rules.PollInterval <= 0 {
		errs = append(errs, errors.New("rules.poll_interval: must be positive"))
	}
	return errs
}

func (c Config) validateLog() []error {
	if !c.Log.Enabled {
		return nil
	}
	var errs []error
	if c.Log.Dir == "" {
		errs = append(errs, errors.New("log.dir: must not be empty while the log is enabled"))
	}
	if c.Log.RetentionDays < 1 {
		errs = append(errs, errors.New("log.retention_days: must be at least 1"))
	}
	if c.Log.MaxMBPerDay < 1 {
		errs = append(errs, errors.New("log.max_mb_per_day: must be at least 1, the log must never grow without a bound"))
	}
	return errs
}

func (c Config) validateDataDir() []error {
	if c.DataDir == "" {
		return []error{errors.New("data_dir: must not be empty")}
	}
	return nil
}

func validSourceName(name string) bool {
	if name == "" || len(name) > maxSourceNameLen {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		isAlnum := c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
		if !isAlnum && (i == 0 || (c != '-' && c != '_')) {
			return false
		}
	}
	return !isWindowsDeviceName(name)
}

func isWindowsDeviceName(name string) bool {
	upper := strings.ToUpper(name)
	switch upper {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) {
		return upper[3] >= '1' && upper[3] <= '9'
	}
	return false
}

func (c Config) openResolverWarnings() []string {
	world := ""
	for _, cidr := range c.AllowedClients {
		if prefix, err := netip.ParsePrefix(cidr); err == nil && prefix.Bits() == 0 {
			world = cidr
			break
		}
	}
	if world == "" {
		return nil
	}
	var out []string
	for _, listen := range c.Listen {
		ap, err := netip.ParseAddrPort(listen)
		if err != nil || ap.Addr().IsLoopback() || ap.Addr().IsPrivate() {
			continue
		}
		out = append(out, fmt.Sprintf("listen %s with allowed_clients %s makes plain DNS an open resolver", listen, world))
	}
	return out
}
