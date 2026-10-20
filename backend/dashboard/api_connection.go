package dashboard

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/dotnames"
)

const (
	probeSamples     = 3
	rotationMaxNames = 4
	sourceConfig     = "config"
	sourceFile       = "file"
)

type ConnectionOptions struct {
	Names        []string
	ProbeDomains []string
	Scheme       string
	Port         int
	Rotation     RotationOptions
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
	available := a.rotationReady()
	entries, err := a.namesFileEntries()
	if err != nil {
		a.opts.Log.Error("reading the names file failed", "err", err)
		entries, available = nil, false
	}
	names := a.connectionNames(entries)
	bases := make([]string, 0, len(names))
	for _, n := range names {
		if !slices.Contains(bases, n.Base) {
			bases = append(bases, n.Base)
		}
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
		Rotation: rotationInfo{Available: available, MaxNames: rotationMaxNames},
	}
}

func (a *API) connectionNames(entries []dotnames.Entry) []connectionName {
	c := a.opts.Connection
	seen := make(map[string]bool, len(c.Names)+len(entries))
	names := make([]connectionName, 0, len(c.Names)+len(entries))
	for _, host := range c.Names {
		if !seen[host] {
			seen[host] = true
			names = append(names, a.nameRow(host, sourceConfig, time.Time{}))
		}
	}
	for _, e := range entries {
		if !seen[e.Host] {
			seen[e.Host] = true
			names = append(names, a.nameRow(e.Host, sourceFile, e.Added))
		}
	}
	newest := make(map[string]int, len(names))
	for i, n := range names {
		newest[n.Base] = i
		names[i].Removable = n.Source == sourceFile && len(names) > 1
	}
	for _, i := range newest {
		names[i].Current = true
	}
	return names
}

func (a *API) nameRow(host, source string, added time.Time) connectionName {
	row := connectionName{
		Host:    host,
		Base:    baseOf(host, a.opts.Connection.ProbeDomains),
		Source:  source,
		Expired: a.certExpired(host),
	}
	if !added.IsZero() {
		stamp := added.UTC().Format(time.RFC3339)
		row.Added = &stamp
	}
	return row
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
