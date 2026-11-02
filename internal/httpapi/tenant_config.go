package httpapi

import (
	"net/http"

	"contractaudit/internal/store"
)

type tenantConfigRequest struct {
	ExpectedRevision string                      `json:"expected_revision"`
	Selection        store.TenantConfigSelection `json:"selection"`
}

type tenantLiveControlRequest struct {
	ExpectedRevision string `json:"expected_revision"`
	Enabled          bool   `json:"enabled"`
}

func (h *handler) tenantConfig(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	out, err := h.store.ReadTenantConfig(r.Context(), user)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) previewTenantConfig(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body tenantConfigRequest
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	preview, err := h.store.PreviewTenantConfig(r.Context(), user, body.ExpectedRevision, body.Selection)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (h *handler) adoptTenantConfig(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body tenantConfigRequest
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.store.AdoptTenantConfig(r.Context(), user, body.ExpectedRevision, body.Selection)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) setTenantLiveControl(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body tenantLiveControlRequest
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.store.SetTenantModelSendControl(r.Context(), user, body.ExpectedRevision, body.Enabled)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
