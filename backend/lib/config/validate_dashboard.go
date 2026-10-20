package config

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

const (
	minSessionDays = 1
	maxSessionDays = 90
)

func (c Config) validateDashboard() []error {
	d := c.Dashboard
	if !d.Enabled {
		return nil
	}
	var errs []error
	errs = append(errs, c.validateDashboardListen()...)
	errs = append(errs, validateDashboardNames(d.Names, d.publicListen())...)
	errs = append(errs, validateHostList("dashboard.probe_domains", d.ProbeDomains)...)
	errs = append(errs, c.validateDashboardCerts()...)
	if d.KeyHashFile == "" {
		errs = append(errs, errors.New("dashboard.key_hash_file: must not be empty"))
	}
	if d.SecretFile == "" {
		errs = append(errs, errors.New("dashboard.secret_file: must not be empty"))
	}
	if d.SessionDays < minSessionDays || d.SessionDays > maxSessionDays {
		errs = append(errs, fmt.Errorf("dashboard.session_days: %d must be between %d and %d", d.SessionDays, minSessionDays, maxSessionDays))
	}
	return errs
}

func (d Dashboard) publicListen() bool {
	for _, addr := range d.Listen {
		if ap, err := netip.ParseAddrPort(addr); err == nil && !ap.Addr().IsLoopback() {
			return true
		}
	}
	return false
}

func (c Config) validateDashboardListen() []error {
	if len(c.Dashboard.Listen) == 0 {
		return []error{errors.New("dashboard.listen: at least one address is required")}
	}
	taken := make(map[netip.AddrPort]string)
	for _, addr := range c.Listen {
		if parsed, err := netip.ParseAddrPort(addr); err == nil {
			taken[parsed] = "listen"
		}
	}
	if c.DoT.Enabled {
		for _, addr := range c.DoT.Listen {
			if parsed, err := netip.ParseAddrPort(addr); err == nil {
				taken[parsed] = "dot.listen"
			}
		}
	}
	var errs []error
	seen := make(map[netip.AddrPort]bool, len(c.Dashboard.Listen))
	for i, addr := range c.Dashboard.Listen {
		parsed, err := netip.ParseAddrPort(addr)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("dashboard.listen[%d]: %q must be IP:port, hostnames are not allowed: %w", i, addr, err))
			continue
		case parsed.Port() == 0:
			errs = append(errs, fmt.Errorf("dashboard.listen[%d]: %q must name a port, 0 is not allowed", i, addr))
		case seen[parsed]:
			errs = append(errs, fmt.Errorf("dashboard.listen[%d]: %q is listed twice", i, addr))
		case taken[parsed] != "":
			errs = append(errs, fmt.Errorf("dashboard.listen[%d]: %q is also in %s, the servers cannot share an address", i, addr, taken[parsed]))
		}
		seen[parsed] = true
	}
	return errs
}

func validateDashboardNames(names []string, required bool) []error {
	if len(names) == 0 && required {
		return []error{errors.New("dashboard.names: at least one name is required when the dashboard listens beyond loopback")}
	}
	return validateHostList("dashboard.names", names)
}

func validateHostList(field string, names []string) []error {
	var errs []error
	seen := make(map[string]bool, len(names))
	for i, name := range names {
		switch {
		case !validHostName(name):
			errs = append(errs, fmt.Errorf("%s[%d]: must be a lowercase host name of at least two labels (letters, digits and hyphens), without wildcard, trailing dot or IP address", field, i))
		case seen[name]:
			errs = append(errs, fmt.Errorf("%s[%d]: is listed twice", field, i))
		}
		seen[name] = true
	}
	return errs
}

func (c Config) validateDashboardCerts() []error {
	var errs []error
	for i, pair := range c.Dashboard.Certs {
		if pair.CertFile == "" {
			errs = append(errs, fmt.Errorf("dashboard.certs[%d].cert_file: must not be empty", i))
		}
		if pair.KeyFile == "" {
			errs = append(errs, fmt.Errorf("dashboard.certs[%d].key_file: must not be empty", i))
		}
	}
	if c.Dashboard.publicListen() && len(c.Dashboard.EffectiveCerts(c.DoT)) == 0 {
		errs = append(errs, errors.New("dashboard.certs: the dashboard listens beyond loopback and needs a certificate (list one here, or enable dot with its own certificate)"))
	}
	return errs
}

func (c Config) dashboardWarnings() []string {
	if !c.Dashboard.Enabled {
		return nil
	}
	var out []string
	for i, name := range c.Dashboard.Names {
		label, _, _ := strings.Cut(name, ".")
		if len(label) < minSecretLabelLen {
			out = append(out, fmt.Sprintf("dashboard.names[%d]: the first label is shorter than %d characters, a short name is easy to guess or to find", i, minSecretLabelLen))
		}
	}
	return out
}
