package httpapi

import (
	"net/http"
	"strings"
)

func (h *handler) jurisdictionOperations(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	fromDate, toDate := strings.TrimSpace(r.URL.Query().Get("from_date")), strings.TrimSpace(r.URL.Query().Get("to_date"))
	out, err := h.store.ReadJurisdictionOperations(r.Context(), user, fromDate, toDate)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
