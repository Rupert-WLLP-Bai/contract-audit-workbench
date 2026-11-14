package httpapi

import (
	"net/http"
)

func (h *handler) casePlaybooks(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	out, err := h.store.ListCasePlaybooks(r.Context(), user, r.PathValue("caseID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) contractPlaybooks(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	out, err := h.store.ListContractPlaybooks(r.Context(), user, r.PathValue("contractID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
