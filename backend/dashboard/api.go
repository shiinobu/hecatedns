package dashboard

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/config"
	"github.com/shiinobu/hecatedns/backend/service/qlog"
)

const (
	defaultScanBudget  = 5 * time.Second
	defaultScanRecords = 2_000_000
	defaultMaxGroups   = 50_000
	defaultStatusTTL   = time.Minute
	defaultQueriesTTL  = 5 * time.Second

	pathStatus      = "/api/status"
	pathQueries     = "/api/queries"
	pathRules       = "/api/rules"
	pathRulesRemove = "/api/rules/remove"
)

var readMethods = []string{http.MethodGet, http.MethodHead}

type endpoint struct {
	methods []string
	handle  http.HandlerFunc
}

type certExpiry interface {
	NotAfter() time.Time
}

type APIOptions struct {
	LogDir     string
	LogEnabled bool
	Location   *time.Location
	RulesFile  string
	ListsDir   string
	Lists      []config.Source
	Resolver   func(context.Context) ResolverResult
	Cert       certExpiry
	Guard      *Guard
	Now        func() time.Time
	Log        *slog.Logger

	ScanBudget  time.Duration
	ScanRecords int
	MaxGroups   int
	StatusTTL   time.Duration
	QueriesTTL  time.Duration
}

type API struct {
	opts    APIOptions
	routes  map[string]endpoint
	status  *ttlCache[struct{}, statusResponse]
	queries *ttlCache[queryKey, queryResult]
	rulesMu sync.Mutex
}

func NewAPI(o APIOptions) *API {
	if o.Location == nil {
		o.Location = time.Local
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Guard == nil {
		o.Guard = NewGuard()
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	o.ScanBudget = orDefault(o.ScanBudget, defaultScanBudget)
	o.ScanRecords = orDefault(o.ScanRecords, defaultScanRecords)
	o.MaxGroups = orDefault(o.MaxGroups, defaultMaxGroups)
	o.StatusTTL = orDefault(o.StatusTTL, defaultStatusTTL)
	o.QueriesTTL = orDefault(o.QueriesTTL, defaultQueriesTTL)
	a := &API{
		opts:    o,
		status:  newTTLCache[struct{}, statusResponse](o.StatusTTL, o.Now),
		queries: newTTLCache[queryKey, queryResult](o.QueriesTTL, o.Now),
	}
	a.routes = a.endpoints()
	return a
}

func (a *API) endpoints() map[string]endpoint {
	return map[string]endpoint{
		pathStatus:      {methods: readMethods, handle: a.handleStatus},
		pathQueries:     {methods: readMethods, handle: a.handleQueries},
		pathRules:       {methods: append(slices.Clone(readMethods), http.MethodPost), handle: a.handleRules},
		pathRulesRemove: {methods: []string{http.MethodPost}, handle: a.handleRemoveRule},
	}
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ep, ok := a.routes[r.URL.Path]
	if !ok {
		writeAPIError(a.opts.Log, w, http.StatusNotFound, "not_found", "No such API path.")
		return
	}
	if !slices.Contains(ep.methods, r.Method) {
		w.Header().Set("Allow", strings.Join(ep.methods, ", "))
		writeAPIError(a.opts.Log, w, http.StatusMethodNotAllowed, "method_not_allowed", "This path does not accept that method.")
		return
	}
	ep.handle(w, r)
}

func (a *API) scanLog(filter qlog.Filter, visit func(qlog.Record)) (partial bool, err error) {
	deadline := a.opts.Now().Add(a.opts.ScanBudget)
	seen := 0
	filter.Location = a.opts.Location
	stats, err := qlog.ReadWhile(a.opts.LogDir, filter, func(rec qlog.Record) bool {
		visit(rec)
		seen++
		return seen < a.opts.ScanRecords && a.opts.Now().Before(deadline)
	})
	return stats.Stopped, err
}

func writeJSON(log *slog.Logger, w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log.Error("encoding a response failed", "err", err)
		status, body = http.StatusInternalServerError, []byte(`{"error":"internal","message":"internal error"}`)
	}
	h := w.Header()
	h.Set("Content-Type", jsonContentType)
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		log.Debug("writing a response failed", "err", err)
	}
}

func writeAPIError(log *slog.Logger, w http.ResponseWriter, status int, code, message string) {
	writeJSON(log, w, status, apiError{Error: code, Message: message})
}
