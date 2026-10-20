package dashboard

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shiinobu/hecatedns/backend/engine"
	"github.com/shiinobu/hecatedns/backend/service/qlog"
)

const (
	maxQueryRows      = 200
	maxQueryTextBytes = 100
	maxTypesPerGroup  = 8

	decisionAll   = "all"
	defaultWindow = "15m"
)

var queryWindows = map[string]time.Duration{
	"15m": 15 * time.Minute,
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
}

type queryKey struct {
	window, decision, text string
	limit                  int
}

type queryRow struct {
	Name     string   `json:"name"`
	Decision string   `json:"decision"`
	Count    int      `json:"count"`
	Last     string   `json:"last"`
	By       string   `json:"by"`
	List     string   `json:"list"`
	Types    []string `json:"types"`
	Parents  []string `json:"parents"`
}

type queryResponse struct {
	Window    string     `json:"window"`
	Generated string     `json:"generated"`
	Partial   bool       `json:"partial"`
	Rows      []queryRow `json:"rows"`
}

type queryResult struct {
	resp queryResponse
	err  error
}

type groupKey struct {
	name, decision string
}

type group struct {
	key   groupKey
	count int
	last  time.Time
	by    string
	list  string
	types map[string]struct{}
}

func (a *API) handleQueries(w http.ResponseWriter, r *http.Request) {
	key, problem := parseQueryKey(r.URL.Query())
	if problem != "" {
		writeAPIError(a.opts.Log, w, http.StatusBadRequest, "bad_request", problem)
		return
	}
	result := a.queries.get(key, func() queryResult { return a.buildQueries(key) })
	if result.err != nil {
		a.opts.Log.Error("reading the query log failed", "err", result.err)
		writeAPIError(a.opts.Log, w, http.StatusInternalServerError, "internal", "Could not read the query log.")
		return
	}
	writeJSON(a.opts.Log, w, http.StatusOK, result.resp)
}

func parseQueryKey(v url.Values) (queryKey, string) {
	key := queryKey{window: valueOr(v, "window", defaultWindow), decision: valueOr(v, "decision", decisionAll), limit: maxQueryRows}
	if _, ok := queryWindows[key.window]; !ok {
		return key, "window must be 15m, 1h, 6h or 24h."
	}
	if key.decision != decisionAll && key.decision != engine.ActionBlock && key.decision != engine.ActionPass {
		return key, "decision must be all, block or pass."
	}
	if raw := v.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			return key, "limit must be a positive number."
		}
		key.limit = min(limit, maxQueryRows)
	}
	key.text = strings.ToLower(strings.TrimSpace(v.Get("q")))
	if len(key.text) > maxQueryTextBytes {
		return key, "q is too long."
	}
	return key, ""
}

func valueOr(v url.Values, name, fallback string) string {
	if got := v.Get(name); got != "" {
		return got
	}
	return fallback
}

func (a *API) buildQueries(key queryKey) queryResult {
	now := a.opts.Now()
	filter := qlog.Filter{Since: now.Add(-queryWindows[key.window]), BlockedOnly: key.decision == engine.ActionBlock}
	groups := make(map[groupKey]*group)
	capped := false
	stopped, err := a.scanLog(filter, func(rec qlog.Record) {
		if g := a.groupFor(groups, key, rec, &capped); g != nil {
			g.add(rec)
		}
	})
	if err != nil {
		return queryResult{err: err}
	}
	return queryResult{resp: queryResponse{
		Window:    key.window,
		Generated: now.In(a.opts.Location).Format(time.RFC3339),
		Partial:   stopped || capped,
		Rows:      a.rows(groups, key.limit),
	}}
}

func (a *API) groupFor(groups map[groupKey]*group, key queryKey, rec qlog.Record, capped *bool) *group {
	if rec.Action != engine.ActionBlock && rec.Action != engine.ActionPass {
		return nil
	}
	if key.decision != decisionAll && rec.Action != key.decision {
		return nil
	}
	name := strings.ToLower(rec.QName)
	if key.text != "" && !strings.Contains(name, key.text) {
		return nil
	}
	gk := groupKey{name: name, decision: rec.Action}
	if g := groups[gk]; g != nil {
		return g
	}
	if len(groups) >= a.opts.MaxGroups {
		*capped = true
		return nil
	}
	g := &group{key: gk, types: make(map[string]struct{})}
	groups[gk] = g
	return g
}

func (g *group) add(rec qlog.Record) {
	g.count++
	if rec.QType != "" && len(g.types) < maxTypesPerGroup {
		g.types[rec.QType] = struct{}{}
	}
	if !rec.Time.Before(g.last) {
		g.last = rec.Time
		g.by, g.list = attribution(rec)
	}
}

func attribution(rec qlog.Record) (by, list string) {
	allowRulePrefix := string(engine.RuleAllow) + " "
	switch {
	case rec.Reason == engine.ReasonRule:
		return "rule", ""
	case rec.Reason == engine.ReasonAllow && strings.HasPrefix(rec.Source, allowRulePrefix):
		return "rule", ""
	case rec.Source != "" && (rec.Reason == engine.ReasonList || rec.Reason == engine.ReasonCNAME || rec.Reason == engine.ReasonAllow):
		return "list", rec.Source
	}
	return "", ""
}

func (a *API) rows(groups map[groupKey]*group, limit int) []queryRow {
	ordered := make([]*group, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if !ordered[i].last.Equal(ordered[j].last) {
			return ordered[i].last.After(ordered[j].last)
		}
		if ordered[i].key.name != ordered[j].key.name {
			return ordered[i].key.name < ordered[j].key.name
		}
		return ordered[i].key.decision < ordered[j].key.decision
	})
	if len(ordered) > limit {
		ordered = ordered[:limit]
	}
	rows := make([]queryRow, 0, len(ordered))
	for _, g := range ordered {
		rows = append(rows, a.row(g))
	}
	return rows
}

func (a *API) row(g *group) queryRow {
	types := make([]string, 0, len(g.types))
	for t := range g.types {
		types = append(types, t)
	}
	sort.Strings(types)
	parents := a.opts.Guard.Parents(g.key.name)
	if parents == nil {
		parents = []string{}
	}
	return queryRow{
		Name: g.key.name, Decision: g.key.decision, Count: g.count,
		Last: g.last.Format(time.RFC3339), By: g.by, List: g.list,
		Types: types, Parents: parents,
	}
}
