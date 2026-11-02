package httpapi

import (
	"net/http"

	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

func (h *handler) listSupportGrants(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	grants, err := h.store.ListSupportGrants(r.Context(), user)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"grants": grants})
}
func (h *handler) requestSupportGrant(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.contextSwitchEnabled {
		writeErr(w, fault.New(403, "context_switch_disabled", "支援授权尚未开放"))
		return
	}
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body store.SupportGrantRequest
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	grant, err := h.store.RequestSupportGrant(r.Context(), user, r.PathValue("caseID"), body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, grant)
}
func (h *handler) changeSupportGrant(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !h.contextSwitchEnabled {
			writeErr(w, fault.New(403, "context_switch_disabled", "支援授权尚未开放"))
			return
		}
		user, ok := h.actor(w, r)
		if !ok {
			return
		}
		var body struct {
			Expected string `json:"expected_revision"`
		}
		if err := readJSON(w, r, &body); err != nil {
			writeErr(w, err)
			return
		}
		grant, err := h.store.ChangeSupportGrant(r.Context(), user, r.PathValue("grantID"), action, body.Expected)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, grant)
	}
}
func (h *handler) readSupportCase(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	result, err := h.store.ReadSupportCase(r.Context(), user, r.PathValue("grantID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, result)
}
