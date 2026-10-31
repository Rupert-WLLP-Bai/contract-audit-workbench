package httpapi

import (
	"net/http"

	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

func (h *handler) listExperienceUsageOptions(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	result, err := h.store.ListExperienceUsageOptions(r.Context(), user, store.ExperienceUsageInput{
		CaseID: r.PathValue("caseID"), RunID: query.Get("run_id"), AttemptID: query.Get("attempt_id"), FindingID: query.Get("finding_id"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) recordExperienceUsage(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct {
		CaseID         optionalPathIdentity `json:"case_id"`
		RunID          string               `json:"run_id"`
		AttemptID      string               `json:"attempt_id"`
		FindingID      string               `json:"finding_id"`
		Ref            store.KnowledgeRef   `json:"ref"`
		ExpectedAction *string              `json:"expected_action"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	caseID := r.PathValue("caseID")
	if err := governancePathIdentity(caseID, body.CaseID); err != nil {
		writeErr(w, err)
		return
	}
	if body.ExpectedAction == nil {
		writeErr(w, fault.Invalid("请明确选择经验的预期方向"))
		return
	}
	usage, err := h.store.RecordExperienceUsage(r.Context(), user, store.ExperienceUsageInput{
		CaseID: caseID, RunID: body.RunID, AttemptID: body.AttemptID, FindingID: body.FindingID,
		Ref: body.Ref, ExpectedAction: *body.ExpectedAction,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, usage)
}

func (h *handler) getExperienceMetrics(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	result, err := h.store.ExperienceMetricsForVersion(r.Context(), user, r.PathValue("knowledgeID"), r.PathValue("versionID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
