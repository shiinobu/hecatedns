package dashboard

import (
	"context"
	"math"
	"net/http"
	"os"
	"time"

	"github.com/shiinobu/hecatedns/backend/engine"
	"github.com/shiinobu/hecatedns/backend/service/lists"
	"github.com/shiinobu/hecatedns/backend/service/qlog"
	"github.com/shiinobu/hecatedns/backend/service/ruleedit"
)

const (
	statusWindow = 24 * time.Hour
	hoursPerDay  = 24
)

type statusResponse struct {
	Time        string           `json:"time"`
	Resolver    ResolverResult   `json:"resolver"`
	Certificate *certificateInfo `json:"certificate"`
	Lists       []listInfo       `json:"lists"`
	LogEnabled  bool             `json:"log_enabled"`
	Counts      *countsInfo      `json:"counts"`
	Rules       rulesInfo        `json:"rules"`
}

type certificateInfo struct {
	DaysLeft int    `json:"days_left"`
	Expires  string `json:"expires"`
}

type listInfo struct {
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`
	State   string  `json:"state"`
	Domains *int    `json:"domains"`
	Updated *string `json:"updated"`
}

type countsInfo struct {
	Total   int  `json:"total"`
	Blocked int  `json:"blocked"`
	Passed  int  `json:"passed"`
	Errors  int  `json:"errors"`
	Partial bool `json:"partial"`
}

type rulesInfo struct {
	Count    int   `json:"count"`
	Bytes    int64 `json:"bytes"`
	MaxBytes int   `json:"max_bytes"`
}

func (a *API) handleStatus(w http.ResponseWriter, _ *http.Request) {
	resp := a.status.get(struct{}{}, a.buildStatus)
	resp.Time = a.opts.Now().In(a.opts.Location).Format(time.RFC3339)
	writeJSON(a.opts.Log, w, http.StatusOK, resp)
}

func (a *API) buildStatus() statusResponse {
	resp := statusResponse{
		Resolver:   ResolverResult{Name: defaultProbeName, Error: "not configured"},
		Lists:      a.listStatuses(),
		LogEnabled: a.opts.LogEnabled,
		Rules:      a.rulesSummary(),
	}
	if a.opts.Resolver != nil {
		resp.Resolver = a.opts.Resolver(context.Background())
	}
	if a.opts.Cert != nil {
		resp.Certificate = a.certificateInfo(a.opts.Cert.NotAfter())
	}
	if a.opts.LogEnabled {
		resp.Counts = a.countLastDay()
	}
	return resp
}

func (a *API) certificateInfo(notAfter time.Time) *certificateInfo {
	left := notAfter.Sub(a.opts.Now()).Hours() / hoursPerDay
	return &certificateInfo{DaysLeft: int(math.Floor(left)), Expires: notAfter.UTC().Format(time.RFC3339)}
}

func (a *API) listStatuses() []listInfo {
	out := make([]listInfo, 0, len(a.opts.Lists))
	for _, src := range a.opts.Lists {
		state, meta := lists.Inspect(a.opts.ListsDir, src)
		info := listInfo{Name: src.Name, Kind: src.Kind, State: state}
		if state == lists.StateOK {
			domains := meta.Count
			info.Domains = &domains
			if !meta.FetchedAt.IsZero() {
				updated := meta.FetchedAt.In(a.opts.Location).Format(time.RFC3339)
				info.Updated = &updated
			}
		}
		out = append(out, info)
	}
	return out
}

func (a *API) rulesSummary() rulesInfo {
	info := rulesInfo{MaxBytes: engine.MaxRulesFileBytes}
	listing, err := ruleedit.List(a.opts.RulesFile)
	if err != nil {
		a.opts.Log.Warn("reading the rules file for the status failed", "err", err)
		return info
	}
	info.Count = len(listing.Rules)
	if stat, err := os.Stat(a.opts.RulesFile); err == nil {
		info.Bytes = stat.Size()
	}
	return info
}

func (a *API) countLastDay() *countsInfo {
	counts := &countsInfo{}
	since := a.opts.Now().Add(-statusWindow)
	partial, err := a.scanLog(qlog.Filter{Since: since}, func(rec qlog.Record) {
		counts.Total++
		switch rec.Action {
		case engine.ActionBlock:
			counts.Blocked++
		case engine.ActionPass:
			counts.Passed++
		case engine.ActionError:
			counts.Errors++
		}
	})
	if err != nil {
		a.opts.Log.Warn("counting the query log failed", "err", err)
		partial = true
	}
	counts.Partial = partial
	return counts
}
