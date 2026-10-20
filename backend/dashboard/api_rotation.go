package dashboard

import (
	"context"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/dotnames"
)

const (
	defaultResolveTimeout = 4 * time.Second
	defaultServeWait      = 6 * time.Second
	defaultServePoll      = time.Second
	defaultReachWait      = 4 * time.Second
)

type RotationOptions struct {
	File           string
	Resolves       func(ctx context.Context, host string) error
	Serves         func(ctx context.Context, host string) error
	Reaches        func(ctx context.Context, host string) error
	Random         io.Reader
	ResolveTimeout time.Duration
	ServeWait      time.Duration
	ServePoll      time.Duration
	ReachWait      time.Duration
}

type rotateRequest struct {
	Domain string `json:"domain"`
}

type rotateResponse struct {
	Host  string           `json:"host"`
	Names []connectionName `json:"names"`
}

type removeNameRequest struct {
	Host string `json:"host"`
}

type removeNameResponse struct {
	Names []connectionName `json:"names"`
}

func (a *API) rotationReady() bool {
	r := a.opts.Connection.Rotation
	return r.File != "" && r.Resolves != nil
}

func (a *API) namesFileEntries() ([]dotnames.Entry, error) {
	file := a.opts.Connection.Rotation.File
	if file == "" {
		return nil, nil
	}
	entries, _, err := dotnames.Read(file)
	return entries, err
}

func (a *API) rotationRefused(w http.ResponseWriter, status int, code, message string) {
	writeAPIError(a.opts.Log, w, status, code, message)
}

func (a *API) rotationUnavailable(w http.ResponseWriter) {
	a.rotationRefused(w, http.StatusNotFound, "rotation_unavailable", "Rotation is not set up on this server.")
}

func (a *API) namesFileFailure(w http.ResponseWriter, what string, err error) {
	a.opts.Log.Error(what, "err", err)
	a.rotationRefused(w, http.StatusInternalServerError, "internal", "Could not use the names file.")
}

func (a *API) lockRotation(w http.ResponseWriter) bool {
	if a.rotationMu.TryLock() {
		return true
	}
	a.rotationRefused(w, http.StatusConflict, "busy", "Another change is still running. Try again in a few seconds.")
	return false
}

func (a *API) allowedDomain(domain string) bool {
	return domain != "" && slices.ContainsFunc(a.opts.Connection.ProbeDomains, func(d string) bool {
		return canonicalName(d) == domain
	})
}

func (a *API) handleRotate(w http.ResponseWriter, r *http.Request) {
	var req rotateRequest
	if !a.decodeBody(w, r, &req) {
		return
	}
	if !a.rotationReady() {
		a.rotationUnavailable(w)
		return
	}
	domain := canonicalName(req.Domain)
	if !a.allowedDomain(domain) {
		a.rotationRefused(w, http.StatusBadRequest, "bad_domain", "That domain is not one of the configured domains.")
		return
	}
	if !a.lockRotation(w) {
		return
	}
	defer a.rotationMu.Unlock()
	entries, err := a.namesFileEntries()
	if err != nil {
		a.namesFileFailure(w, "reading the names file failed", err)
		return
	}
	if len(entries) >= rotationMaxNames {
		a.rotationRefused(w, http.StatusConflict, "too_many_names", "Remove an old name before adding another.")
		return
	}
	host, ok := a.newHost(w, domain)
	if !ok || !a.confirmResolves(w, r.Context(), host) {
		return
	}
	next := append(slices.Clone(entries), dotnames.Entry{Host: host, Added: a.opts.Now().UTC()})
	if err := dotnames.Write(a.opts.Connection.Rotation.File, next); err != nil {
		a.namesFileFailure(w, "writing the names file failed", err)
		return
	}
	if !a.confirmServed(w, r.Context(), host, entries) || !a.confirmReached(w, r.Context(), host, entries) {
		return
	}
	a.opts.Log.Info("a connection name was added", "file_names", len(next))
	writeJSON(a.opts.Log, w, http.StatusOK, rotateResponse{Host: host, Names: a.connectionNames(next)})
}

func (a *API) newHost(w http.ResponseWriter, domain string) (string, bool) {
	label, err := dotnames.NewLabel(a.opts.Connection.Rotation.Random)
	if err != nil {
		a.namesFileFailure(w, "making a label failed", err)
		return "", false
	}
	host := label + "." + domain
	if a.opts.Cert != nil {
		if _, covered := a.opts.Cert.NotAfterFor(host); !covered {
			a.rotationRefused(w, http.StatusBadRequest, "bad_domain", "The certificate does not cover that domain.")
			return "", false
		}
	}
	return host, true
}

func (a *API) confirmResolves(w http.ResponseWriter, parent context.Context, host string) bool {
	ctx, cancel := context.WithTimeout(parent, orDefault(a.opts.Connection.Rotation.ResolveTimeout, defaultResolveTimeout))
	defer cancel()
	if err := a.opts.Connection.Rotation.Resolves(ctx, host); err != nil {
		a.opts.Log.Warn("a new connection name did not resolve", "err", err)
		a.rotationRefused(w, http.StatusBadGateway, "resolve_failed", "The new name does not resolve from this server yet.")
		return false
	}
	return true
}

func (a *API) confirmServed(w http.ResponseWriter, parent context.Context, host string, before []dotnames.Entry) bool {
	r := a.opts.Connection.Rotation
	if r.Serves == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(parent, orDefault(r.ServeWait, defaultServeWait))
	defer cancel()
	poll := orDefault(r.ServePoll, defaultServePoll)
	for {
		err := r.Serves(ctx, host)
		if err == nil {
			return true
		}
		select {
		case <-ctx.Done():
			a.opts.Log.Warn("the DNS server did not accept a new connection name, rolling back", "err", err)
			a.refuseAfterRollback(w, before, "not_served", "The DNS server did not accept the new name")
			return false
		case <-time.After(poll):
		}
	}
}

func (a *API) confirmReached(w http.ResponseWriter, parent context.Context, host string, before []dotnames.Entry) bool {
	r := a.opts.Connection.Rotation
	if r.Reaches == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(parent, orDefault(r.ReachWait, defaultReachWait))
	defer cancel()
	err := r.Reaches(ctx, host)
	if err == nil {
		return true
	}
	a.opts.Log.Warn("a new connection name does not lead to this server, rolling back", "err", err)
	a.refuseAfterRollback(w, before, "not_reachable", "The new name does not lead to this server. Check that its DNS record is not proxied")
	return false
}

func (a *API) refuseAfterRollback(w http.ResponseWriter, before []dotnames.Entry, code, reason string) {
	message := reason + ". Nothing was changed."
	if a.rollbackNames(before) != nil {
		message = reason + ", and the names file could not be restored. Check the server."
	}
	a.rotationRefused(w, http.StatusBadGateway, code, message)
}

func (a *API) rollbackNames(before []dotnames.Entry) error {
	err := dotnames.Write(a.opts.Connection.Rotation.File, before)
	if err != nil {
		a.opts.Log.Error("rolling back the names file failed", "err", err)
	}
	return err
}

func (a *API) handleConnectionRemove(w http.ResponseWriter, r *http.Request) {
	var req removeNameRequest
	if !a.decodeBody(w, r, &req) {
		return
	}
	if !a.rotationReady() {
		a.rotationUnavailable(w)
		return
	}
	host := canonicalName(req.Host)
	if slices.Contains(a.opts.Connection.Names, host) {
		a.rotationRefused(w, http.StatusBadRequest, "not_removable", "That name comes from the config file and cannot be removed here.")
		return
	}
	if !a.lockRotation(w) {
		return
	}
	defer a.rotationMu.Unlock()
	entries, err := a.namesFileEntries()
	if err != nil {
		a.namesFileFailure(w, "reading the names file failed", err)
		return
	}
	index := slices.IndexFunc(entries, func(e dotnames.Entry) bool { return e.Host == host })
	if index < 0 {
		a.rotationRefused(w, http.StatusNotFound, "not_found", "There is no such name.")
		return
	}
	if len(a.connectionNames(entries)) <= 1 {
		a.rotationRefused(w, http.StatusBadRequest, "last_name", "The last name cannot be removed.")
		return
	}
	next := slices.Delete(slices.Clone(entries), index, index+1)
	if err := dotnames.Write(a.opts.Connection.Rotation.File, next); err != nil {
		a.namesFileFailure(w, "writing the names file failed", err)
		return
	}
	a.opts.Log.Info("a connection name was removed", "file_names", len(next))
	writeJSON(a.opts.Log, w, http.StatusOK, removeNameResponse{Names: a.connectionNames(next)})
}
