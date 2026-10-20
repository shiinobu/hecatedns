package dashboard

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/shiinobu/hecatedns/backend/service/ops"
)

type opsResponse struct {
	Enabled bool        `json:"enabled"`
	Actions []opsAction `json:"actions"`
	Backups []opsBackup `json:"backups"`
	Output  *opsOutput  `json:"output"`
}

type opsAction struct {
	Action    string  `json:"action"`
	State     string  `json:"state"`
	Started   *string `json:"started"`
	Finished  *string `json:"finished"`
	Exit      *int    `json:"exit"`
	CooldownS int     `json:"cooldown_s"`
}

type opsBackup struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
	Time  string `json:"time"`
}

type opsOutput struct {
	Action string `json:"action"`
	Text   string `json:"text"`
}

type opsRunRequest struct {
	Action string `json:"action"`
}

type opsRunResponse struct {
	Action string `json:"action"`
	State  string `json:"state"`
}

type opsCooldownError struct {
	apiError
	CooldownS int `json:"cooldown_s"`
}

func (a *API) handleOps(w http.ResponseWriter, r *http.Request) {
	resp := opsResponse{Enabled: a.opts.Ops.Enabled(), Actions: []opsAction{}, Backups: []opsBackup{}}
	if action := r.URL.Query().Get("action"); action != "" {
		text, ok := a.opts.Ops.Output(action)
		if !ok {
			writeAPIError(a.opts.Log, w, http.StatusNotFound, "unknown_action", "No such action.")
			return
		}
		resp.Output = &opsOutput{Action: action, Text: text}
	}
	for _, st := range a.opts.Ops.Status() {
		resp.Actions = append(resp.Actions, toOpsAction(st))
	}
	for _, bk := range a.opts.Ops.Backups() {
		resp.Backups = append(resp.Backups, opsBackup{Name: bk.Name, Bytes: bk.Bytes, Time: bk.Time.UTC().Format(time.RFC3339)})
	}
	writeJSON(a.opts.Log, w, http.StatusOK, resp)
}

func (a *API) handleOpsRun(w http.ResponseWriter, r *http.Request) {
	var req opsRunRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, defaultMaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeAPIError(a.opts.Log, w, http.StatusBadRequest, "bad_request", "The request body is not valid JSON.")
		return
	}
	err := a.opts.Ops.Run(req.Action)
	switch {
	case err == nil:
		a.opts.Log.Info("operation requested", "action", req.Action)
		writeJSON(a.opts.Log, w, http.StatusAccepted, opsRunResponse{Action: req.Action, State: ops.StateQueued})
	case errors.Is(err, ops.ErrUnknown):
		writeAPIError(a.opts.Log, w, http.StatusNotFound, "unknown_action", "No such action.")
	case errors.Is(err, ops.ErrDisabled):
		writeAPIError(a.opts.Log, w, http.StatusServiceUnavailable, "unavailable", "Operations are not available on this host.")
	case errors.Is(err, ops.ErrBusy):
		writeAPIError(a.opts.Log, w, http.StatusConflict, "busy", "That action is already queued or running.")
	case errors.Is(err, ops.ErrCooldown):
		a.writeOpsCooldown(w, req.Action)
	default:
		a.opts.Log.Error("requesting an operation failed", "action", req.Action, "err", err)
		writeAPIError(a.opts.Log, w, http.StatusInternalServerError, "internal", "Could not queue the action.")
	}
}

func (a *API) writeOpsCooldown(w http.ResponseWriter, action string) {
	wait := 0
	for _, st := range a.opts.Ops.Status() {
		if st.Action == action {
			wait = st.CooldownSeconds
		}
	}
	writeJSON(a.opts.Log, w, http.StatusTooManyRequests, opsCooldownError{
		apiError:  apiError{Error: "cooldown", Message: "That action ran too recently. Try again later."},
		CooldownS: wait,
	})
}

func toOpsAction(st ops.Status) opsAction {
	out := opsAction{Action: st.Action, State: st.State, CooldownS: st.CooldownSeconds}
	if !st.Started.IsZero() {
		out.Started = timeString(st.Started)
	}
	if !st.Finished.IsZero() {
		out.Finished = timeString(st.Finished)
	}
	if st.State == ops.StateOK || st.State == ops.StateFailed {
		exit := st.Exit
		out.Exit = &exit
	}
	return out
}

func timeString(t time.Time) *string {
	s := t.UTC().Format(time.RFC3339)
	return &s
}
