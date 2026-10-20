package dashboard

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/shiinobu/hecatedns/backend/engine"
	"github.com/shiinobu/hecatedns/backend/service/ruleedit"
)

type ruleJSON struct {
	Action string `json:"action"`
	Domain string `json:"domain"`
}

type rulesListResponse struct {
	Rules []ruleJSON `json:"rules"`
	rulesInfo
}

type addRuleRequest struct {
	Action string `json:"action"`
	Domain string `json:"domain"`
}

type addRuleResponse struct {
	Rule     ruleJSON  `json:"rule"`
	Replaced *ruleJSON `json:"replaced"`
}

type removeRuleRequest struct {
	Domain string `json:"domain"`
}

type removeRuleResponse struct {
	Removed ruleJSON `json:"removed"`
}

func toRuleJSON(rule engine.Rule) ruleJSON {
	return ruleJSON{Action: string(rule.Action), Domain: rule.Domain}
}

func (a *API) handleRules(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.addRule(w, r)
		return
	}
	a.listRules(w)
}

func (a *API) listRules(w http.ResponseWriter) {
	listing, err := ruleedit.List(a.opts.RulesFile)
	if err != nil {
		a.rulesFailure(w, "listing the rules failed", err)
		return
	}
	rules := make([]ruleJSON, 0, len(listing.Rules))
	for _, rule := range listing.Rules {
		rules = append(rules, toRuleJSON(rule))
	}
	writeJSON(a.opts.Log, w, http.StatusOK, rulesListResponse{
		Rules:     rules,
		rulesInfo: rulesInfo{Count: len(rules), Bytes: a.rulesFileBytes(), MaxBytes: engine.MaxRulesFileBytes},
	})
}

func (a *API) rulesFileBytes() int64 {
	stat, err := os.Stat(a.opts.RulesFile)
	if err != nil {
		return 0
	}
	return stat.Size()
}

func (a *API) addRule(w http.ResponseWriter, r *http.Request) {
	var req addRuleRequest
	if !a.decodeRuleBody(w, r, &req) {
		return
	}
	rule, err := ruleedit.ParseRule(req.Action, req.Domain)
	switch {
	case errors.Is(err, ruleedit.ErrBadAction):
		a.rulesRefused(w, "bad_action", "The action must be block or allow.")
		return
	case err != nil:
		a.rulesRefused(w, "invalid_domain", "That is not a valid domain name.")
		return
	}
	if code, message := a.refuseRule(rule); code != "" {
		a.rulesRefused(w, code, message)
		return
	}
	a.rulesMu.Lock()
	res, err := ruleedit.Add(a.opts.RulesFile, rule)
	a.rulesMu.Unlock()
	if errors.Is(err, ruleedit.ErrFileFull) {
		a.rulesRefused(w, "file_full", "The rules file is full. Remove old rules first.")
		return
	}
	if err != nil {
		a.rulesFailure(w, "adding a rule failed", err)
		return
	}
	resp := addRuleResponse{Rule: toRuleJSON(rule)}
	if res.Replaced != nil {
		replaced := toRuleJSON(*res.Replaced)
		resp.Replaced = &replaced
	}
	a.opts.Log.Info("rule added", "action", rule.Action, "replaced", res.ReplacedLines)
	writeJSON(a.opts.Log, w, http.StatusOK, resp)
}

func (a *API) refuseRule(rule engine.Rule) (code, message string) {
	switch {
	case a.opts.Guard.IsPublicSuffix(rule.Domain):
		return "public_suffix", "That is a public suffix: a rule would block a whole registry."
	case rule.Action == engine.RuleBlock && a.opts.Guard.IsProtected(rule.Domain):
		return "protected_name", "That name is protected: blocking it could lock you out."
	}
	return "", ""
}

func (a *API) handleRemoveRule(w http.ResponseWriter, r *http.Request) {
	var req removeRuleRequest
	if !a.decodeRuleBody(w, r, &req) {
		return
	}
	domain, err := ruleedit.ParseDomain(req.Domain)
	if err != nil {
		a.rulesRefused(w, "invalid_domain", "That is not a valid domain name.")
		return
	}
	a.rulesMu.Lock()
	removed, err := ruleedit.Remove(a.opts.RulesFile, domain)
	a.rulesMu.Unlock()
	if err != nil {
		a.rulesFailure(w, "removing a rule failed", err)
		return
	}
	if len(removed) == 0 {
		writeAPIError(a.opts.Log, w, http.StatusNotFound, "not_found", "There is no rule for that domain.")
		return
	}
	a.opts.Log.Info("rule removed", "action", removed[0].Action, "lines", len(removed))
	writeJSON(a.opts.Log, w, http.StatusOK, removeRuleResponse{Removed: toRuleJSON(removed[0])})
}

func (a *API) decodeRuleBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, defaultMaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		a.rulesRefused(w, "bad_request", "The request body is not valid JSON.")
		return false
	}
	return true
}

func (a *API) rulesRefused(w http.ResponseWriter, code, message string) {
	writeAPIError(a.opts.Log, w, http.StatusBadRequest, code, message)
}

func (a *API) rulesFailure(w http.ResponseWriter, what string, err error) {
	a.opts.Log.Error(what, "err", err)
	writeAPIError(a.opts.Log, w, http.StatusInternalServerError, "internal", "Could not use the rules file.")
}
