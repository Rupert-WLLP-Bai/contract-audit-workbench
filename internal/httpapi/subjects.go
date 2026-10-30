package httpapi

import (
	"net/http"
	"strconv"

	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

func (h *handler) createUnboundSubject(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body store.SubjectCreateInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	result, replayed, err := h.store.CreateUnboundSubject(r.Context(), user, body, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeSubjectResult(w, replayed, result)
}

func (h *handler) createCaseSubject(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body store.CaseSubjectCreateInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	result, replayed, err := h.store.CreateCaseSubject(r.Context(), user, r.PathValue("caseID"), body, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeSubjectResult(w, replayed, result)
}

func (h *handler) bindExistingSubject(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body store.BindExistingSubjectInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	result, replayed, err := h.store.BindExistingSubject(r.Context(), user, r.PathValue("caseID"), body, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeSubjectResult(w, replayed, result)
}

func (h *handler) listCaseSubjects(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	result, err := h.store.ListCaseSubjects(r.Context(), user, r.PathValue("caseID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) listConfiguredSubjectSources(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	result, err := h.store.ListConfiguredSubjectSources(r.Context(), user, r.PathValue("caseID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) listSubjectSourceSnapshots(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeErr(w, fault.Invalid("limit必须是整数"))
			return
		}
		limit = parsed
	}
	result, err := h.store.ListSubjectSourceSnapshots(r.Context(), user, r.PathValue("caseID"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) createSubjectSourceSnapshot(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body store.SubjectSourceSnapshotCreateInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	result, replayed, err := h.store.CreateSubjectSourceSnapshot(r.Context(), user, r.PathValue("caseID"), body, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Idempotency-Replayed", strconv.FormatBool(replayed))
	status := http.StatusCreated
	writeJSON(w, status, result)
}

func (h *handler) getSubjectSourceSnapshot(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	result, err := h.store.GetSubjectSourceSnapshot(r.Context(), user, r.PathValue("caseID"), r.PathValue("snapshotID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) verifySubjectSourceSnapshot(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body store.SubjectSourceVerifyInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	result, replayed, err := h.store.VerifySubjectFromControlledSource(r.Context(), user, r.PathValue("caseID"), r.PathValue("snapshotID"), body, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Idempotency-Replayed", strconv.FormatBool(replayed))
	status := http.StatusOK
	if result.State == "pending_decision" {
		status = http.StatusAccepted
	}
	writeJSON(w, status, result)
}

func (h *handler) listMaterialVersionParses(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		writeErr(w, fault.Invalid("材料版本无效"))
		return
	}
	result, err := h.store.ListMaterialVersionParses(r.Context(), user, r.PathValue("caseID"), r.PathValue("materialID"), version)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) getMaterialVersionParsedChunk(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		writeErr(w, fault.Invalid("材料版本无效"))
		return
	}
	queryInt := func(name string, fallback int) (int, error) {
		raw := r.URL.Query().Get(name)
		if raw == "" {
			return fallback, nil
		}
		value, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fault.Invalid(name + "必须是整数")
		}
		return value, nil
	}
	offset, err := queryInt("offset", 0)
	if err != nil {
		writeErr(w, err)
		return
	}
	limit, err := queryInt("limit", 32768)
	if err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.store.GetMaterialVersionParsedChunk(r.Context(), user, r.PathValue("caseID"), r.PathValue("materialID"), version, r.PathValue("parsedDocumentID"), offset, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) listSubjects(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeErr(w, fault.Invalid("limit必须是整数"))
			return
		}
		limit = parsed
	}
	result, err := h.store.ListSubjects(r.Context(), user, r.URL.Query().Get("case_id"), r.URL.Query().Get("q"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) getSubject(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	result, err := h.store.GetSubjectDetail(r.Context(), user, r.PathValue("subjectID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) getSubjectHistory(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	result, err := h.store.GetSubjectHistory(r.Context(), user, r.PathValue("subjectID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) listSubjectCaseLinks(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeErr(w, fault.Invalid("limit必须是整数"))
			return
		}
		limit = parsed
	}
	result, err := h.store.ListSubjectCaseLinks(r.Context(), user, r.PathValue("subjectID"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) proposeSubjectChange(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body store.SubjectChangeCreateInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	result, replayed, err := h.store.ProposeSubjectChange(r.Context(), user, r.PathValue("caseID"), body, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeSubjectResult(w, replayed, result)
}

func (h *handler) listSubjectChanges(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeErr(w, fault.Invalid("limit必须是整数"))
			return
		}
		limit = parsed
	}
	result, err := h.store.ListSubjectChanges(r.Context(), user, r.PathValue("caseID"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) getSubjectChange(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	result, err := h.store.GetSubjectChange(r.Context(), user, r.PathValue("caseID"), r.PathValue("requestID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) getSubjectChangeImpact(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	result, err := h.store.GetSubjectChangeImpact(r.Context(), user, r.PathValue("caseID"), r.PathValue("requestID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) decideSubjectChange(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body store.SubjectChangeDecisionInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	result, replayed, err := h.store.DecideSubjectChange(r.Context(), user, r.PathValue("caseID"), r.PathValue("requestID"), body, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Idempotency-Replayed", strconv.FormatBool(replayed))
	writeJSON(w, http.StatusOK, result)
}

func writeSubjectResult(w http.ResponseWriter, replayed bool, body any) {
	w.Header().Set("Idempotency-Replayed", strconv.FormatBool(replayed))
	writeJSON(w, http.StatusCreated, body)
}
