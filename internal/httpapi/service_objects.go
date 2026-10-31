package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

func (h *handler) listServiceObjects(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	cursor, limit, err := serviceObjectPageQuery(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.store.ListServiceObjects(r.Context(), user, r.PathValue("caseID"), cursor, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) createServiceObject(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input store.ServiceObjectCreateInput
	if err := readJSON(w, r, &input); err != nil {
		writeErr(w, err)
		return
	}
	result, _, err := h.store.CreateServiceObject(r.Context(), user, r.PathValue("caseID"), input, strings.TrimSpace(r.Header.Get("Idempotency-Key")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (h *handler) getServiceObject(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	result, err := h.store.GetServiceObject(r.Context(), user, r.PathValue("caseID"), r.PathValue("objectID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) listServiceObjectVersions(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	cursor, limit, err := serviceObjectPageQuery(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.store.ListServiceObjectVersions(r.Context(), user, r.PathValue("caseID"), r.PathValue("objectID"), cursor, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) getServiceObjectVersion(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		writeErr(w, fault.Invalid("服务对象版本无效"))
		return
	}
	result, err := h.store.GetServiceObjectVersion(r.Context(), user, r.PathValue("caseID"), r.PathValue("objectID"), version)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) listServiceObjectAssociations(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	cursor, limit, err := serviceObjectPageQuery(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.store.ListServiceObjectAssociations(r.Context(), user, r.PathValue("caseID"), r.PathValue("objectID"), cursor, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) getServiceObjectAssociation(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	version, err := strconv.Atoi(r.PathValue("associationVersion"))
	if err != nil || version < 1 {
		writeErr(w, fault.Invalid("服务对象关联版本无效"))
		return
	}
	result, err := h.store.GetServiceObjectAssociation(r.Context(), user, r.PathValue("caseID"), r.PathValue("objectID"), version)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) getServiceObjectAssociationCandidates(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	materialVersion, err := strconv.Atoi(r.URL.Query().Get("material_version"))
	if err != nil || materialVersion < 1 {
		writeErr(w, fault.Invalid("材料版本无效"))
		return
	}
	input := store.ServiceObjectAssociationPreviewInput{
		RunID: r.URL.Query().Get("run_id"), AttemptID: r.URL.Query().Get("attempt_id"),
		MaterialID: r.URL.Query().Get("material_id"), MaterialVersion: materialVersion,
		DocumentID: r.URL.Query().Get("document_id"), ParsedDocumentID: r.URL.Query().Get("parsed_document_id"),
	}
	result, err := h.store.PreviewServiceObjectAssociation(r.Context(), user, r.PathValue("caseID"), r.PathValue("objectID"), input)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) createServiceObjectAssociation(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input store.ServiceObjectAssociationConfirmInput
	if err := readJSON(w, r, &input); err != nil {
		writeErr(w, err)
		return
	}
	result, _, err := h.store.ConfirmServiceObjectAssociation(r.Context(), user, r.PathValue("caseID"), r.PathValue("objectID"),
		strings.TrimSpace(r.Header.Get("Idempotency-Key")), input)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func serviceObjectPageQuery(r *http.Request) (string, int, error) {
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return "", 0, fault.Invalid("服务对象分页范围为1到100")
		}
		limit = parsed
	}
	return r.URL.Query().Get("cursor"), limit, nil
}
