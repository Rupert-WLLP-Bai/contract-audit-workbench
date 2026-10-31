package httpapi

import (
	"net/http"
)

func (h *handler) reviewReadiness(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	readiness, err := h.store.GetReviewReadiness(r.Context(), user, r.PathValue("caseID"), r.URL.Query().Get("business_at"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, readiness)
}
