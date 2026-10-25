package httpapi

import "net/http"

func (h *handler) healthSnapshot(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, h.systemHealth.Snapshot(r.Context()))
}

func (h *handler) adminSystemHealth(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actorForAudience(w, r, "admin"); !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, h.systemHealth.Snapshot(r.Context()))
}
