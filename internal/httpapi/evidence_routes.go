package httpapi

import (
	"net/http"

	"contractaudit/internal/store"
)

// registerEvidenceRoutes mounts the case-bound evidence request workflow.
func registerEvidenceRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"POST /api/cases/{caseID}/evidence-requests", h.createEvidenceRequest},
		routeBinding{"GET /api/cases/{caseID}/evidence-requests", h.listEvidenceRequests},
		routeBinding{"GET /api/cases/{caseID}/evidence-requests/{requestID}", h.getEvidenceRequest},
		routeBinding{"GET /api/cases/{caseID}/evidence-requests/{requestID}/history", h.listEvidenceRequestHistory},
		routeBinding{"POST /api/cases/{caseID}/evidence-requests/{requestID}/receipts", h.submitEvidenceReceipt},
		routeBinding{"POST /api/cases/{caseID}/evidence-requests/{requestID}/receipts/{receiptID}/verify", h.verifyEvidenceRequest},
	)
}

func (h *handler) createEvidenceRequest(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct {
		CaseID                optionalPathIdentity `json:"case_id"`
		TaskID                string               `json:"task_id"`
		TaskVersion           int                  `json:"task_version"`
		SourceRunID           string               `json:"source_run_id"`
		SourceAttemptID       string               `json:"source_attempt_id"`
		SourceEvaluationID    string               `json:"source_evaluation_id"`
		SourceGapID           string               `json:"source_gap_id"`
		Question              string               `json:"question"`
		MaterialRole          string               `json:"material_role"`
		VerificationCondition string               `json:"verification_condition"`
		IdempotencyKey        string               `json:"idempotency_key"`
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
	item, err := h.store.CreateEvidenceRequest(r.Context(), user, caseID, store.EvidenceRequestInput{
		TaskID: body.TaskID, TaskVersion: body.TaskVersion, SourceRunID: body.SourceRunID,
		SourceAttemptID: body.SourceAttemptID, SourceEvaluationID: body.SourceEvaluationID,
		SourceGapID: body.SourceGapID, Question: body.Question, MaterialRole: body.MaterialRole,
		VerificationCondition: body.VerificationCondition, IdempotencyKey: key,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, evidenceRequestJSON(item))
}

func (h *handler) listEvidenceRequests(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	items, err := h.store.ListEvidenceRequests(r.Context(), user, r.PathValue("caseID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = evidenceRequestJSON(item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *handler) getEvidenceRequest(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	item, err := h.store.GetEvidenceRequest(r.Context(), user, r.PathValue("caseID"), r.PathValue("requestID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, evidenceRequestJSON(item))
}

func (h *handler) listEvidenceRequestHistory(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	items, err := h.store.ListEvidenceRequestHistory(r.Context(), user, r.PathValue("caseID"), r.PathValue("requestID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]any, len(items))
	for i, item := range items {
		event := map[string]any{
			"type": item.Type, "actor_id": item.ActorID, "occurred_at": item.OccurredAt,
			"request": evidenceRequestJSON(item.Request),
		}
		if item.Receipt != nil {
			event["receipt"] = evidenceReceiptJSON(*item.Receipt)
		}
		out[i] = event
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *handler) submitEvidenceReceipt(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct {
		CaseID          optionalPathIdentity `json:"case_id"`
		RequestID       optionalPathIdentity `json:"request_id"`
		DocumentID      string               `json:"document_id"`
		Answer          string               `json:"answer"`
		ExpectedVersion int                  `json:"expected_version"`
		IdempotencyKey  string               `json:"idempotency_key"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	caseID, requestID := r.PathValue("caseID"), r.PathValue("requestID")
	if err := governancePathIdentity(caseID, body.CaseID); err != nil {
		writeErr(w, err)
		return
	}
	if err := governancePathIdentity(requestID, body.RequestID); err != nil {
		writeErr(w, err)
		return
	}
	key, err := governanceIdempotencyKey(r, body.IdempotencyKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	item, receipt, err := h.store.SubmitEvidenceReceipt(r.Context(), user, caseID, requestID, store.EvidenceReceiptInput{
		DocumentID: body.DocumentID, Answer: body.Answer, ExpectedVersion: body.ExpectedVersion, IdempotencyKey: key,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"request": evidenceRequestJSON(item), "receipt": evidenceReceiptJSON(receipt)})
}

func (h *handler) verifyEvidenceRequest(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct {
		CaseID          optionalPathIdentity          `json:"case_id"`
		RequestID       optionalPathIdentity          `json:"request_id"`
		ReceiptID       optionalPathIdentity          `json:"receipt_id"`
		RunID           string                        `json:"run_id"`
		AttemptID       string                        `json:"attempt_id"`
		EvaluationID    string                        `json:"evaluation_id"`
		GapID           string                        `json:"gap_id"`
		Evidence        []store.ExperienceEvidenceRef `json:"evidence"`
		Reason          string                        `json:"reason"`
		ExpectedVersion int                           `json:"expected_version"`
		IdempotencyKey  string                        `json:"idempotency_key"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	caseID, requestID, receiptID := r.PathValue("caseID"), r.PathValue("requestID"), r.PathValue("receiptID")
	if err := governancePathIdentity(caseID, body.CaseID); err != nil {
		writeErr(w, err)
		return
	}
	if err := governancePathIdentity(requestID, body.RequestID); err != nil {
		writeErr(w, err)
		return
	}
	if err := governancePathIdentity(receiptID, body.ReceiptID); err != nil {
		writeErr(w, err)
		return
	}
	key, err := governanceIdempotencyKey(r, body.IdempotencyKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	item, err := h.store.VerifyEvidenceRequest(r.Context(), user, caseID, requestID, store.EvidenceRequestVerifyInput{
		RunID: body.RunID, AttemptID: body.AttemptID, EvaluationID: body.EvaluationID, GapID: body.GapID,
		ReceiptID: receiptID, Evidence: body.Evidence, Reason: body.Reason,
		ExpectedVersion: body.ExpectedVersion, IdempotencyKey: key,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, evidenceRequestJSON(item))
}

// These are business projections: technical hashes and parser metadata remain
// available only through the separately authorized diagnostics API.
func evidenceRequestJSON(item store.EvidenceRequest) map[string]any {
	out := map[string]any{
		"id": item.ID, "case_id": item.CaseID, "task_id": item.TaskID, "task_version": item.TaskVersion,
		"assignee_id": item.AssigneeID, "due_at": item.DueAt, "source_run_id": item.SourceRunID,
		"source_attempt_id": item.SourceAttemptID, "question": item.Question,
		"verification_condition": item.VerificationCondition, "status": item.Status, "version": item.Version,
		"created_by": item.CreatedBy, "created_at": item.CreatedAt, "updated_at": item.UpdatedAt,
	}
	if item.SourceGap != nil {
		out["source_gap"] = item.SourceGap
	} else if item.SourceEvaluation != nil {
		evaluation := item.SourceEvaluation
		view := map[string]any{
			"id": evaluation.ID, "run_id": evaluation.RunID, "attempt_id": evaluation.AttemptID,
			"case_id": evaluation.CaseID, "domain": evaluation.Domain, "rule_version": evaluation.RuleVersion,
			"relation": evaluation.Relation, "reason": evaluation.Reason,
		}
		if evaluation.RuleID != "" {
			view["rule_id"] = evaluation.RuleID
		}
		if evaluation.CapabilityID != "" {
			view["capability_id"] = evaluation.CapabilityID
		}
		if evaluation.Mandatory {
			view["mandatory"] = true
		}
		if evaluation.ReasonCode != "" {
			view["reason_code"] = evaluation.ReasonCode
		}
		if len(evaluation.FactIDs) > 0 {
			view["fact_ids"] = evaluation.FactIDs
		}
		if len(evaluation.ObjectRefs) > 0 {
			view["object_refs"] = evaluation.ObjectRefs
		}
		if evaluation.FindingID != "" {
			view["finding_id"] = evaluation.FindingID
		}
		out["source_evaluation"] = view
	}
	if item.MaterialRole != "" {
		out["material_role"] = item.MaterialRole
	}
	if item.Verification != nil {
		v := item.Verification
		refs := make([]any, len(v.Evidence))
		for i, ref := range v.Evidence {
			refs[i] = map[string]any{"document_id": ref.DocumentID, "parsed_document_id": ref.ParsedDocumentID,
				"quote": ref.Quote, "start": ref.Start, "end": ref.End}
		}
		verification := map[string]any{
			"run_id": v.RunID, "attempt_id": v.AttemptID, "receipt_id": v.ReceiptID,
			"evidence": refs, "reason": v.Reason, "verified_by": v.VerifiedBy, "verified_at": v.VerifiedAt,
		}
		if v.EvaluationID != "" {
			verification["evaluation_id"] = v.EvaluationID
		}
		if v.Relation != "" {
			verification["relation"] = v.Relation
		}
		if v.GapID != "" {
			verification["gap_id"] = v.GapID
		}
		if v.GapStatus != "" {
			verification["gap_status"] = v.GapStatus
		}
		out["verification"] = verification
	}
	return out
}

func evidenceReceiptJSON(receipt store.EvidenceReceipt) map[string]any {
	out := map[string]any{
		"id": receipt.ID, "request_id": receipt.RequestID, "case_id": receipt.CaseID,
		"submitted_by": receipt.SubmittedBy, "created_at": receipt.CreatedAt,
	}
	if receipt.DocumentID != "" {
		out["document_id"] = receipt.DocumentID
	}
	if receipt.Answer != "" {
		out["answer"] = receipt.Answer
	}
	return out
}
