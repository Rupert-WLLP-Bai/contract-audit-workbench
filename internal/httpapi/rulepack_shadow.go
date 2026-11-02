// File metadata (manifest mirror):
// Purpose: Exposes admin-only rule-pack shadow sample and report routes; records candidate evaluations separately from published rule decisions and never changes the active rule pack.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi

import (
	"net/http"

	"contractaudit/internal/store"
)

func registerRuleShadowRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"GET /api/admin/rulepacks/{packID}/versions/{version}/shadow/samples", h.ruleShadowSamples},
		routeBinding{"POST /api/admin/rulepacks/{packID}/versions/{version}/shadow", h.createRuleShadow},
		routeBinding{"GET /api/admin/rulepacks/{packID}/versions/{version}/shadow/reports", h.ruleShadowReports},
		routeBinding{"GET /api/admin/rulepacks/{packID}/versions/{version}/shadow/reports/{reportID}", h.ruleShadowReport},
	)
}

func (h *handler) ruleShadowSamples(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	items, err := h.store.RuleShadowSamples(r.Context(), user, r.PathValue("packID"), r.PathValue("version"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "selection_limit": 20, "scan_limit": 250})
}

func (h *handler) createRuleShadow(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var input store.RuleShadowInput
	if err := readJSON(w, r, &input); err != nil {
		writeErr(w, err)
		return
	}
	input.PackID, input.Version = r.PathValue("packID"), r.PathValue("version")
	report, err := h.store.CreateRuleShadowReport(r.Context(), user, input)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, report)
}

func (h *handler) ruleShadowReports(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	items, err := h.store.RuleShadowReports(r.Context(), user, r.PathValue("packID"), r.PathValue("version"), "")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) ruleShadowReport(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	items, err := h.store.RuleShadowReports(r.Context(), user, r.PathValue("packID"), r.PathValue("version"), r.PathValue("reportID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items[0])
}
