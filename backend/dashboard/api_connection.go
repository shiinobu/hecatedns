package dashboard

import (
	"net/http"
	"slices"
	"strings"
)

const (
	probeSamples     = 3
	rotationMaxNames = 4
	sourceConfig     = "config"
)

type ConnectionOptions struct {
	Names        []string
	ProbeDomains []string
	Scheme       string
	Port         int
}

type connectionResponse struct {
	Domains  []string         `json:"domains"`
	Names    []connectionName `json:"names"`
	Probe    probeInfo        `json:"probe"`
	Rotation rotationInfo     `json:"rotation"`
}

type connectionName struct {
	Host      string  `json:"host"`
	Base      string  `json:"base"`
	Source    string  `json:"source"`
	Current   bool    `json:"current"`
	Removable bool    `json:"removable"`
	Added     *string `json:"added"`
	Expired   bool    `json:"expired"`
}

type probeInfo struct {
	Scheme  string   `json:"scheme"`
	Domains []string `json:"domains"`
	Port    int      `json:"port"`
	Samples int      `json:"samples"`
}

type rotationInfo struct {
	Available bool `json:"available"`
	MaxNames  int  `json:"max_names"`
}

func (a *API) handleConnection(w http.ResponseWriter, _ *http.Request) {
	writeJSON(a.opts.Log, w, http.StatusOK, a.buildConnection())
}

func (a *API) buildConnection() connectionResponse {
	c := a.opts.Connection
	names := make([]connectionName, len(c.Names))
	newest := make(map[string]int, len(c.Names))
	bases := make([]string, 0, len(c.Names))
	for i, host := range c.Names {
		base := baseOf(host, c.ProbeDomains)
		names[i] = connectionName{Host: host, Base: base, Source: sourceConfig, Expired: a.certExpired(host)}
		if _, seen := newest[base]; !seen {
			bases = append(bases, base)
		}
		newest[base] = i
	}
	for _, i := range newest {
		names[i].Current = true
	}
	probes := orEmpty(slices.Clone(c.ProbeDomains))
	domains := probes
	if len(domains) == 0 {
		domains = bases
	}
	return connectionResponse{
		Domains:  domains,
		Names:    names,
		Probe:    probeInfo{Scheme: c.Scheme, Domains: probes, Port: c.Port, Samples: probeSamples},
		Rotation: rotationInfo{MaxNames: rotationMaxNames},
	}
}

func (a *API) certExpired(host string) bool {
	if a.opts.Cert == nil {
		return false
	}
	notAfter, covered := a.opts.Cert.NotAfterFor(host)
	return covered && notAfter.Before(a.opts.Now())
}

func baseOf(host string, probeDomains []string) string {
	best := ""
	for _, domain := range probeDomains {
		if strings.HasSuffix(host, "."+domain) && len(domain) > len(best) {
			best = domain
		}
	}
	if best != "" {
		return best
	}
	if _, rest, found := strings.Cut(host, "."); found {
		return rest
	}
	return host
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
