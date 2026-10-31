package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

// registerGovernanceRoutes mounts Core-owned case feedback and the separate
// admin-audience knowledge governance API.
func registerGovernanceRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"POST /api/cases/{caseID}/experience-feedback", h.submitExperienceFeedback},
		routeBinding{"GET /api/cases/{caseID}/experience-feedback/{feedbackID}", h.getExperienceFeedback},
		routeBinding{"POST /api/cases/{caseID}/experience-feedback/{feedbackID}/distillations", h.requestExperienceDistillation},
		routeBinding{"GET /api/cases/{caseID}/experience-feedback/{feedbackID}/distillations/{jobID}", h.getExperienceDistillation},
		routeBinding{"GET /api/cases/{caseID}/experience-candidates/{candidateID}/versions/{versionID}", h.getExperienceCandidate},
		routeBinding{"GET /api/cases/{caseID}/experience-usage", h.listExperienceUsageOptions},
		routeBinding{"POST /api/cases/{caseID}/experience-usage", h.recordExperienceUsage},
		routeBinding{"GET /api/admin/governed-knowledge", h.listGovernedKnowledgeVersions},
		routeBinding{"POST /api/admin/governed-knowledge", h.createGovernedKnowledge},
		routeBinding{"GET /api/admin/governed-knowledge/{knowledgeID}/versions/{versionID}", h.getGovernedKnowledgeVersion},
		routeBinding{"GET /api/admin/governed-knowledge/{knowledgeID}/versions/{versionID}/experience-metrics", h.getExperienceMetrics},
		routeBinding{"GET /api/admin/governed-knowledge/{knowledgeID}/versions/{versionID}/preview", h.previewGovernedPolicy},
		routeBinding{"POST /api/admin/governed-knowledge/{knowledgeID}/versions/{versionID}/commands", h.governedKnowledgeCommand},
		routeBinding{"GET /api/admin/governed-knowledge/{knowledgeID}/history", h.listGovernedKnowledgeHistory},
	)
}

// optionalPathIdentity distinguishes omitted identities from explicit null or
// empty identities, which cannot match a non-empty path segment.
type optionalPathIdentity struct {
	value   string
	present bool
}

func (identity *optionalPathIdentity) UnmarshalJSON(raw []byte) error {
	identity.present = true
	return json.Unmarshal(raw, &identity.value)
}

func governancePathIdentity(path string, body optionalPathIdentity) error {
	if body.present && body.value != path {
		return fault.Invalid("请求正文中的编号与路径不一致")
	}
	return nil
}

// Existing write APIs use Idempotency-Key. Store input also supports an
// idempotency_key body field; if both are supplied they must identify the same
// request rather than silently choosing one.
func governanceIdempotencyKey(r *http.Request, body string) (string, error) {
	header := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if header != "" && body != "" && header != body {
		return "", fault.Invalid("请求头和正文中的请求标识（幂等键）不一致")
	}
	if header != "" {
		return header, nil
	}
	return body, nil
}

func (h *handler) submitExperienceFeedback(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct {
		CaseID         optionalPathIdentity          `json:"case_id"`
		RunID          string                        `json:"run_id"`
		AttemptID      string                        `json:"attempt_id"`
		FindingID      string                        `json:"finding_id"`
		DecisionID     string                        `json:"decision_id"`
		Reason         string                        `json:"reason"`
		EvidenceRefs   []store.ExperienceEvidenceRef `json:"evidence_refs"`
		IdempotencyKey string                        `json:"idempotency_key"`
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
	key, err := governanceIdempotencyKey(r, body.IdempotencyKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	item, err := h.store.SubmitExperienceFeedback(r.Context(), user, store.ExperienceFeedbackInput{
		CaseID: caseID, RunID: body.RunID, AttemptID: body.AttemptID, FindingID: body.FindingID,
		DecisionID: body.DecisionID, Reason: body.Reason, EvidenceRefs: body.EvidenceRefs, IdempotencyKey: key,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, experienceFeedbackJSON(item))
}

func (h *handler) getExperienceFeedback(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	item, err := h.store.GetExperienceFeedback(r.Context(), user, r.PathValue("caseID"), r.PathValue("feedbackID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, experienceFeedbackJSON(item))
}

func experienceFeedbackJSON(item store.ExperienceFeedback) map[string]any {
	refs := make([]any, len(item.EvidenceRefs))
	for i, ref := range item.EvidenceRefs {
		refs[i] = map[string]any{
			"document_id": ref.DocumentID, "parsed_document_id": ref.ParsedDocumentID,
			"quote": ref.Quote, "start": ref.Start, "end": ref.End,
		}
	}
	return map[string]any{
		"feedback_id": item.FeedbackID, "tenant_id": item.TenantID, "org_id": item.OrgID,
		"case_id": item.CaseID, "run_id": item.RunID, "attempt_id": item.AttemptID,
		"finding_id": item.FindingID, "decision_id": item.DecisionID,
		"decision_action": item.DecisionAction, "decision_disposition": item.DecisionDisposition,
		"reason": item.Reason, "evidence_refs": refs, "applicant_id": item.ApplicantID,
		"created_at": item.CreatedAt,
	}
}

func (h *handler) requestExperienceDistillation(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct {
		CaseID         optionalPathIdentity `json:"case_id"`
		FeedbackID     optionalPathIdentity `json:"feedback_id"`
		CandidateID    string               `json:"candidate_id"`
		BaseVersionID  string               `json:"base_version_id"`
		IdempotencyKey string               `json:"idempotency_key"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	caseID, feedbackID := r.PathValue("caseID"), r.PathValue("feedbackID")
	if err := governancePathIdentity(caseID, body.CaseID); err != nil {
		writeErr(w, err)
		return
	}
	if err := governancePathIdentity(feedbackID, body.FeedbackID); err != nil {
		writeErr(w, err)
		return
	}
	key, err := governanceIdempotencyKey(r, body.IdempotencyKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !h.experienceDeliveryEnabled {
		writeErr(w, fault.New(http.StatusServiceUnavailable, "experience_delivery_unavailable", "经验提炼结果接收服务未配置"))
		return
	}
	receipt, err := h.store.RequestExperienceDistillation(r.Context(), user, store.ExperienceDistillRequest{
		CaseID: caseID, FeedbackID: feedbackID, CandidateID: body.CandidateID,
		BaseVersionID: body.BaseVersionID, IdempotencyKey: key,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, receipt)
}

func (h *handler) getExperienceDistillation(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, r *http.Request) (any, error) {
		return h.store.GetExperienceDistillation(r.Context(), user, r.PathValue("caseID"), r.PathValue("feedbackID"), r.PathValue("jobID"))
	})
}

func (h *handler) getExperienceCandidate(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, r *http.Request) (any, error) {
		return h.store.GetExperienceCandidate(r.Context(), user, r.PathValue("caseID"), r.PathValue("candidateID"), r.PathValue("versionID"))
	})
}

func (h *handler) listGovernedKnowledgeVersions(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("kind") == "legal" {
		h.adminLegalKnowledgeSearch(w, r)
		return
	}
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	items, err := h.store.ListGovernedKnowledgeVersions(user)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) createGovernedKnowledge(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body store.CreateGovernedKnowledgeInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	key, err := governanceIdempotencyKey(r, body.IdempotencyKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	body.IdempotencyKey = key
	item, err := h.store.CreateGovernedKnowledge(user, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *handler) getGovernedKnowledgeVersion(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	item, err := h.store.GetGovernedKnowledgeVersion(user, r.PathValue("knowledgeID"), r.PathValue("versionID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *handler) previewGovernedPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	preview, err := h.store.PreviewGovernedPolicy(user, r.PathValue("knowledgeID"), r.PathValue("versionID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (h *handler) governedKnowledgeCommand(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body struct {
		KnowledgeID             optionalPathIdentity `json:"knowledge_id"`
		VersionID               optionalPathIdentity `json:"version_id"`
		Action                  string               `json:"action"`
		ExpectedRevision        int64                `json:"expected_revision"`
		ExpectedContentSHA256   string               `json:"expected_content_sha256"`
		ExpectedPointerRevision int64                `json:"expected_pointer_revision"`
		Reason                  string               `json:"reason"`
		CrossCaseReviewed       bool                 `json:"cross_case_reviewed"`
		IdempotencyKey          string               `json:"idempotency_key"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	knowledgeID, versionID := r.PathValue("knowledgeID"), r.PathValue("versionID")
	if err := governancePathIdentity(knowledgeID, body.KnowledgeID); err != nil {
		writeErr(w, err)
		return
	}
	if err := governancePathIdentity(versionID, body.VersionID); err != nil {
		writeErr(w, err)
		return
	}
	key, err := governanceIdempotencyKey(r, body.IdempotencyKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	item, err := h.store.GovernedKnowledgeCommand(user, store.GovernedKnowledgeCommandInput{
		KnowledgeID: knowledgeID, VersionID: versionID, Action: body.Action,
		ExpectedRevision: body.ExpectedRevision, ExpectedContentSHA256: body.ExpectedContentSHA256,
		ExpectedPointerRevision: body.ExpectedPointerRevision, Reason: body.Reason,
		CrossCaseReviewed: body.CrossCaseReviewed, IdempotencyKey: key,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *handler) listGovernedKnowledgeHistory(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	items, err := h.store.ListGovernedKnowledgeHistory(user, r.PathValue("knowledgeID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
