package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"contractaudit/internal/store"
)

func (h *handler) previewOperatorMappingChange(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct {
		CAS       store.OperatorMappingChangeCAS   `json:"cas"`
		Patch     store.OperatorMappingChangePatch `json:"patch"`
		BasisRefs json.RawMessage                  `json:"basis_refs"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	preview, err := h.store.PreviewOperatorMappingChange(r.Context(), user, r.PathValue("caseID"), r.PathValue("mappingID"), body.CAS, body.Patch, body.BasisRefs)
	if err != nil {
		writeOperatorErr(w, err, "没有找到对象映射")
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (h *handler) proposeOperatorMappingChange(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body store.OperatorMappingChangeCreateInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	request, _, err := h.store.ProposeOperatorMappingChange(r.Context(), user, r.PathValue("caseID"), r.PathValue("mappingID"), body, key)
	if err != nil {
		writeOperatorErr(w, err, "没有找到对象映射")
		return
	}
	// Replays use the same created status and original immutable response.
	writeJSON(w, http.StatusCreated, request)
}

func (h *handler) getOperatorMappingChange(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	request, decision, err := h.store.GetOperatorMappingChange(r.Context(), user, r.PathValue("caseID"), r.PathValue("requestID"))
	if err != nil {
		writeOperatorErr(w, err, "没有找到对象映射更正")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": request, "decision": decision})
}

func (h *handler) decideOperatorMappingChange(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body store.OperatorMappingChangeDecisionInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	result, _, err := h.store.DecideOperatorMappingChange(r.Context(), user, r.PathValue("caseID"), r.PathValue("requestID"), body, key)
	if err != nil {
		writeOperatorErr(w, err, "没有找到对象映射更正")
		return
	}
	writeJSON(w, http.StatusCreated, result)
}
