package httpapi

import (
	"net/http"

	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

// reviewDiagnostics is deliberately separate from the business workspace. A
// platform role alone grants no case access; Workspace resolves the current
// trusted grants and checks the requested case, run, and selected attempt.
func (h *handler) reviewDiagnostics(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	if user.Role != "admin" {
		writeErr(w, fault.Forbidden("需要平台管理权限查看审查诊断"))
		return
	}
	ws, err := h.store.Workspace(user, r.PathValue("caseID"), r.PathValue("runID"), r.URL.Query().Get("attempt_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if frozen := ws.Snapshot.GovernedKnowledge; frozen != nil {
		// Diagnostics contain exact knowledge identities. Case access alone does
		// not grant a platform administrator access to their source cases.
		refs := make([]store.KnowledgeRef, 0, len(frozen.RequiredPolicies)+len(frozen.OptionalCandidates)+len(frozen.Excluded))
		for _, item := range frozen.RequiredPolicies {
			refs = append(refs, item.Ref)
		}
		for _, item := range frozen.OptionalCandidates {
			refs = append(refs, item.Ref)
		}
		for _, item := range frozen.Excluded {
			refs = append(refs, item.Ref)
		}
		for _, ref := range refs {
			version, err := h.store.GetGovernedKnowledgeVersion(user, ref.KnowledgeID, ref.VersionID)
			if err != nil {
				writeErr(w, err)
				return
			}
			if version.Ref != ref {
				writeErr(w, fault.Conflict("本次审查保存的制度编号不一致"))
				return
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, reviewDiagnosticsJSON(ws))
}

func reviewDiagnosticsJSON(ws store.Workspace) map[string]any {
	inSnapshot := make(map[string]bool, len(ws.Snapshot.DocumentIDs))
	for _, id := range ws.Snapshot.DocumentIDs {
		inSnapshot[id] = true
	}
	documents := make([]any, len(ws.Documents))
	for i, doc := range ws.Documents {
		documents[i] = documentJSON(doc, false, len(inSnapshot) > 0 && !inSnapshot[doc.Document.ID])
	}
	evidence := make([]any, len(ws.Findings))
	for i, finding := range ws.Findings {
		anchors := make([]any, len(finding.Anchors))
		for j, anchor := range finding.Anchors {
			anchors[j] = map[string]any{
				"id": anchor.ID, "document_id": anchor.DocumentID,
				"parsed_document_id": nilString(anchor.ParsedDocumentID),
				"normalized_digest":  nilString(anchor.NormalizedDigest),
			}
		}
		evidence[i] = map[string]any{"finding_id": finding.ID, "anchors": anchors}
	}
	snapshot := snapshotJSON(ws.Snapshot)
	snapshot["governed_knowledge"] = ws.Snapshot.GovernedKnowledge
	return map[string]any{
		"case_id": ws.Case.ID, "run_id": ws.View.Run.ID,
		"execution": ws.Execution, "snapshot": snapshot,
		"audit_events": auditJSON(ws.Audit), "documents": documents, "evidence": evidence,
	}
}
