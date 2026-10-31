package httpapi

import (
	"net/http"

	"contractaudit/internal/accounts"
	"contractaudit/internal/agentflow"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

// registerContractCitationRoutes mounts the read-only reader for one verified
// business-agent citation. The route is Execution-owned: it resolves the frozen
// attempt input and its persisted agent projection, never the latest material,
// parse or attempt.
func registerContractCitationRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"GET /api/cases/{caseID}/runs/{runID}/attempts/{attemptID}/contract-citations/{evidenceID}", h.getFrozenContractCitation},
	)
}

// contractCitation is one verified agent citation read at its frozen position.
// original_digest and parse_digest are the exact identities the server verified
// before it returned the text; the business workspace projection still exposes
// neither, so the browser cannot derive the position from a newer parse.
type contractCitation struct {
	CaseID           string  `json:"case_id"`
	RunID            string  `json:"run_id"`
	AttemptID        string  `json:"attempt_id"`
	EvidenceID       string  `json:"evidence_id"`
	DocumentID       string  `json:"document_id"`
	ParsedDocumentID string  `json:"parsed_document_id"`
	Filename         string  `json:"filename"`
	OriginalDigest   string  `json:"original_digest"`
	ParseDigest      string  `json:"parse_digest"`
	Start            int     `json:"start"`
	End              int     `json:"end"`
	Quote            string  `json:"quote"`
	Text             string  `json:"text"`
	MaterialID       *string `json:"material_id"`
	MaterialVersion  *int    `json:"material_version"`
}

func (h *handler) getFrozenContractCitation(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	citation, err := h.contractCitation(r, user,
		r.PathValue("caseID"), r.PathValue("runID"), r.PathValue("attemptID"), r.PathValue("evidenceID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, citation)
}

func (h *handler) contractCitation(r *http.Request, user accounts.User, caseID, runID, attemptID, evidenceID string) (contractCitation, error) {
	workspace, err := h.store.WorkspaceAttempt(user, caseID, runID, attemptID)
	if err != nil {
		return contractCitation{}, err
	}
	citation, err := contractCitationJSON(workspace, caseID, runID, attemptID, evidenceID)
	if err != nil {
		return contractCitation{}, err
	}
	citation.MaterialID, citation.MaterialVersion = h.citationMaterial(user, caseID, citation.DocumentID)
	return citation, nil
}

// contractCitationJSON resolves one verified evidence record of this exact
// attempt. Every rejection is a refusal to locate, never a downgrade to the
// newest material, parse or attempt.
func contractCitationJSON(workspace store.Workspace, caseID, runID, attemptID, evidenceID string) (contractCitation, error) {
	if evidenceID == "" {
		return contractCitation{}, fault.Invalid("请选择一条智能体引用")
	}
	execution := workspace.Execution
	if execution == nil || execution.Attempt == nil || execution.Attempt.ID != attemptID || execution.Attempt.RunID != runID {
		return contractCitation{}, fault.NotFound("没有找到该执行记录")
	}
	attempt := *execution.Attempt
	// Only a frozen attempt keeps the exact documents and parse that produced
	// the citation. An older attempt without a frozen input cannot be replayed.
	if attempt.HistoryKind != "frozen" {
		return contractCitation{}, fault.Conflict("本次执行没有保存当时的冻结输入，不能打开当时的原文")
	}
	if len(execution.AgentReview) == 0 || string(execution.AgentReview) == "null" {
		return contractCitation{}, fault.Conflict("本次执行没有保存智能体审查记录，不能定位引用")
	}
	review, err := decodeBusinessAgentReview(execution.AgentReview)
	if err != nil {
		return contractCitation{}, fault.Conflict("本次执行的智能体审查记录无法读取，不能定位引用")
	}
	if review.AttemptID == "" || review.AttemptID != attemptID {
		return contractCitation{}, fault.Conflict("智能体审查记录不属于本次执行，不能定位引用")
	}
	evidence := findVerifiedEvidence(review.Evidence, evidenceID)
	if evidence == nil {
		return contractCitation{}, fault.NotFound("本次执行没有这条智能体引用")
	}
	if evidence.Verification != "typed_semantic_and_frozen_quote_verified" {
		return contractCitation{}, fault.Conflict("这条引用尚未通过冻结原文核验，不能打开原文定位")
	}
	anchor := evidence.Anchor
	if anchor.Status != "located" || anchor.DocumentID == "" || anchor.ParsedDocumentID == "" ||
		anchor.NormalizedDigest == "" || anchor.Quote == "" {
		return contractCitation{}, fault.Conflict("这条引用缺少冻结原文身份、摘要或区间，不能定位")
	}
	if !containsString(attempt.ParsedDocumentIDs, anchor.ParsedDocumentID) {
		return contractCitation{}, fault.Conflict("这条引用不属于本次执行的冻结解析，不能定位")
	}
	doc, ok := frozenAttemptDocument(workspace.Documents, caseID, anchor)
	if !ok {
		return contractCitation{}, fault.Conflict("本次执行没有保存这条引用对应的冻结原文，不能定位")
	}
	frozen := store.Anchor{
		ID: anchor.ID, DocumentID: anchor.DocumentID, ParsedDocumentID: anchor.ParsedDocumentID,
		NormalizedDigest: anchor.NormalizedDigest, Quote: anchor.Quote, Status: anchor.Status,
		Start: anchor.Start, End: anchor.End, Page: anchor.Page, ElementID: optionalStringValue(anchor.ElementID),
	}
	if !anchorMatchesFrozenIdentity(frozen, doc) {
		return contractCitation{}, fault.Conflict("引用摘要与本次执行的冻结原文不一致，不能当成已验证结论")
	}
	if _, _, _, located := locateFrozenAnchor(frozen, doc); !located {
		return contractCitation{}, fault.Conflict("引用区间与本次执行的冻结原文不一致，不能高亮")
	}
	parsed := doc.ParsedDocument
	return contractCitation{
		CaseID: caseID, RunID: runID, AttemptID: attemptID, EvidenceID: evidence.EvidenceID,
		DocumentID: doc.Document.ID, ParsedDocumentID: parsed.ID, Filename: doc.Document.Filename,
		OriginalDigest: doc.Document.Digest, ParseDigest: parsed.NormalizedDigest,
		Start: frozen.Start, End: frozen.End, Quote: frozen.Quote, Text: doc.Text,
	}, nil
}

func findVerifiedEvidence(items []agentflow.VerifiedEvidence, evidenceID string) *agentflow.VerifiedEvidence {
	for i := range items {
		if items[i].EvidenceID == evidenceID {
			return &items[i]
		}
	}
	return nil
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

// frozenAttemptDocument returns the only document of this case whose frozen
// parse is the one the citation names. A workspace may carry documents appended
// for the previous-run comparison, and a document may be reparsed later; neither
// may serve a citation of this attempt.
func frozenAttemptDocument(docs []store.Interpreted, caseID string, anchor agentflow.EvidenceAnchor) (store.Interpreted, bool) {
	var found store.Interpreted
	matches := 0
	for _, doc := range docs {
		if doc.Document.ID != anchor.DocumentID || doc.Document.CaseID != caseID {
			continue
		}
		parsed := doc.ParsedDocument
		if parsed == nil || parsed.ID != anchor.ParsedDocumentID || parsed.DocumentID != anchor.DocumentID || parsed.Status != "ready" {
			continue
		}
		found, matches = doc, matches+1
	}
	if matches != 1 {
		return store.Interpreted{}, false
	}
	return found, true
}

// citationMaterial reports the material version that produced exactly this
// frozen document, from the append-only material version history. It never
// falls back to the material's current version: an ambiguous or missing binding
// stays null rather than inventing an identity.
func (h *handler) citationMaterial(user accounts.User, caseID, documentID string) (*string, *int) {
	detail, err := h.store.GetCase(user, caseID)
	if err != nil {
		return nil, nil
	}
	materialID := ""
	version := 0
	matches := 0
	for _, item := range detail.MaterialVersions {
		if item.DocumentID != documentID || item.MaterialID == "" || item.Version <= 0 {
			continue
		}
		if matches > 0 && (item.MaterialID != materialID || item.Version != version) {
			return nil, nil
		}
		materialID, version = item.MaterialID, item.Version
		matches++
	}
	if matches != 1 {
		return nil, nil
	}
	return &materialID, &version
}

func optionalStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
