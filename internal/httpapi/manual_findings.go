package httpapi

import (
	"net/http"

	"contractaudit/internal/store"
)

func registerManualFindingRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"GET /api/cases/{caseID}/reviews/{runID}/attempts/{attemptID}/manual-findings", h.listManualFindings},
		routeBinding{"POST /api/cases/{caseID}/reviews/{runID}/attempts/{attemptID}/manual-findings", h.createManualFinding},
	)
}

func (h *handler) listManualFindings(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	items, err := h.store.ListManualFindings(r.Context(), user, r.PathValue("caseID"), r.PathValue("runID"), r.PathValue("attemptID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) createManualFinding(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct {
		Claim          string                      `json:"claim"`
		Severity       string                      `json:"severity"`
		Evidence       store.ExperienceEvidenceRef `json:"evidence"`
		IdempotencyKey string                      `json:"idempotency_key"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	key, err := governanceIdempotencyKey(r, body.IdempotencyKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	item, err := h.store.CreateManualFinding(r.Context(), user, store.ManualFindingInput{
		CaseID: r.PathValue("caseID"), RunID: r.PathValue("runID"), AttemptID: r.PathValue("attemptID"),
		Claim: body.Claim, Severity: body.Severity, Evidence: body.Evidence, IdempotencyKey: key,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
