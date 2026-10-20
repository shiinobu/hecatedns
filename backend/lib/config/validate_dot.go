package config

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

const (
	minDoTReloadEvery = time.Second

	maxHostNameLen  = 253
	maxHostLabelLen = 63
)

func (c Config) validateDoT() []error {
	d := c.DoT
	if !d.Enabled {
		return nil
	}
	var errs []error
	if c.Backend != BackendNative {
		errs = append(errs, fmt.Errorf("dot: needs backend %s, but the backend is %q, which is not hardened for the internet", BackendNative, c.Backend))
	}
	errs = append(errs, c.validateDoTListen()...)
	if d.CertFile == "" {
		errs = append(errs, errors.New("dot.cert_file: must not be empty"))
	}
	if d.KeyFile == "" {
		errs = append(errs, errors.New("dot.key_file: must not be empty"))
	}
	errs = append(errs, validateDoTNames(d.Names, d.NamesFile != "")...)
	if d.ReloadEvery < minDoTReloadEvery {
		errs = append(errs, fmt.Errorf("dot.reload_every: %v is below the minimum of %v", d.ReloadEvery, minDoTReloadEvery))
	}
	return errs
}

func (c Config) validateDoTListen() []error {
	if len(c.DoT.Listen) == 0 {
		return []error{errors.New("dot.listen: at least one address is required")}
	}
	plain := make(map[netip.AddrPort]bool, len(c.Listen))
	for _, addr := range c.Listen {
		if parsed, err := netip.ParseAddrPort(addr); err == nil {
			plain[parsed] = true
		}
	}
	var errs []error
	seen := make(map[netip.AddrPort]bool, len(c.DoT.Listen))
	for i, addr := range c.DoT.Listen {
		parsed, err := netip.ParseAddrPort(addr)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("dot.listen[%d]: %q must be IP:port, hostnames are not allowed: %w", i, addr, err))
			continue
		case parsed.Port() == 0:
			errs = append(errs, fmt.Errorf("dot.listen[%d]: %q must name a port, 0 is not allowed", i, addr))
		case seen[parsed]:
			errs = append(errs, fmt.Errorf("dot.listen[%d]: %q is listed twice", i, addr))
		case plain[parsed]:
			errs = append(errs, fmt.Errorf("dot.listen[%d]: %q is also in listen, the plain DNS and the DoT server cannot share an address", i, addr))
		}
		seen[parsed] = true
	}
	return errs
}

func validateDoTNames(names []string, hasNamesFile bool) []error {
	if len(names) == 0 && !hasNamesFile {
		return []error{errors.New("dot.names: at least one server name is required (or set dot.names_file), a DoT server without names would serve nobody")}
	}
	var errs []error
	seen := make(map[string]bool, len(names))
	for i, name := range names {
		switch {
		case !validHostName(name):
			errs = append(errs, fmt.Errorf("dot.names[%d]: must be a lowercase host name of at least two labels (letters, digits and hyphens), without wildcard, trailing dot or IP address", i))
		case seen[name]:
			errs = append(errs, fmt.Errorf("dot.names[%d]: is listed twice", i))
		}
		seen[name] = true
	}
	return errs
}

func ValidHostName(name string) bool {
	return validHostName(name)
}

func validHostName(name string) bool {
	if name == "" || len(name) > maxHostNameLen || name != strings.ToLower(name) {
		return false
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return false
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !validHostLabel(label) {
			return false
		}
	}
	return true
}

func validHostLabel(label string) bool {
	if label == "" || len(label) > maxHostLabelLen || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		if c != '-' && (c < '0' || c > '9') && (c < 'a' || c > 'z') {
			return false
		}
	}
	return true
}

const minSecretLabelLen = 16

func (c Config) dotWarnings() []string {
	if !c.DoT.Enabled {
		return nil
	}
	var out []string
	for i, name := range c.DoT.Names {
		label, _, _ := strings.Cut(name, ".")
		if len(label) < minSecretLabelLen {
			out = append(out, fmt.Sprintf("dot.names[%d]: the first label is shorter than %d characters, a short name is easy to guess or to find", i, minSecretLabelLen))
		}
	}
	return append(out, c.dotListenWarnings()...)
}

func (c Config) dotListenWarnings() []string {
	ipv4Wildcard := make(map[uint16]bool)
	for _, listen := range c.DoT.Listen {
		if ap, err := netip.ParseAddrPort(listen); err == nil && ap.Addr().Is4() && ap.Addr().IsUnspecified() {
			ipv4Wildcard[ap.Port()] = true
		}
	}
	var out []string
	for i, listen := range c.DoT.Listen {
		ap, err := netip.ParseAddrPort(listen)
		if err != nil || !ap.Addr().Is6() || !ap.Addr().IsUnspecified() || ipv4Wildcard[ap.Port()] {
			continue
		}
		out = append(out, fmt.Sprintf("dot.listen[%d]: the IPv6 wildcard serves IPv6 only; add 0.0.0.0:%d for IPv4 clients", i, ap.Port()))
	}
	return out
}
