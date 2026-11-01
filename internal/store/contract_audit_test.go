package store

import (
	"strings"
	"testing"
)

func auditTestDocument(digest string) FrozenDocument {
	normalized := strings.Repeat("b", 64)
	return FrozenDocument{
		Document: Document{ID: "doc-0001", Digest: digest}, OriginalHash: digest,
		Ready: true, NormalizedHash: &normalized, ParsedDocumentID: "parsed-0001",
		Text: "交付期为合同生效后三十日。",
	}
}

func auditTestMaterials(digest, purpose string) []frozenVersionMaterial {
	return []frozenVersionMaterial{{
		MaterialID: "material-1", MaterialVersionID: "doc-0001", CaseOrgID: "demo-org",
		CaseID: "case-1", MaterialVersion: 1, Filename: "draft.txt", Digest: digest, Purpose: purpose,
	}}
}

func auditTestFinding(anchor Anchor) auditFindingEvidence {
	return auditFindingEvidence{FindingID: "finding-1", Anchors: []Anchor{anchor}}
}

func auditTestLocatedAnchor() Anchor {
	return Anchor{
		ID: "anchor-1", DocumentID: "doc-0001", ParsedDocumentID: "parsed-0001",
		NormalizedDigest: strings.Repeat("b", 64), Quote: "合同生效后三十日",
		Status: "located", Start: 3, End: 11,
	}
}

func auditReasonPresent(reasons []ContractAuditGateReason, code string) bool {
	for _, reason := range reasons {
		if reason.Code == code {
			return true
		}
	}
	return false
}

func TestAssessAuditEvidenceRequiresFrozenCitationsAndCompletedAttempt(t *testing.T) {
	digest := strings.Repeat("a", 64)
	baseline := auditEvidence{
		RunState: "completed", AttemptState: "completed", HasInput: true,
		AttemptInputHash: "input-hash", InputHash: "input-hash",
		Documents: []FrozenDocument{auditTestDocument(digest)},
		Materials: auditTestMaterials(digest, "draft"),
		Findings:  []auditFindingEvidence{auditTestFinding(auditTestLocatedAnchor())},
	}
	assessment := assessAuditEvidence(baseline)
	if assessment.GateState != auditGateNeedsHuman || len(assessment.Reasons) != 0 ||
		assessment.FindingCount != 1 || assessment.VerifiedFindingCount != 1 || assessment.ModelFailure {
		t.Fatalf("verified audit gate = %+v", assessment)
	}

	cases := map[string]struct {
		mutate    func(*auditEvidence)
		reason    string
		modelFail bool
	}{
		"unlocatable anchor": {func(e *auditEvidence) {
			e.Findings[0].Anchors[0].Status = "unlocatable"
		}, "anchor_not_located", false},
		"missing anchor": {func(e *auditEvidence) {
			e.Findings[0].Anchors = nil
		}, "finding_without_anchor", false},
		"anchor outside attempt": {func(e *auditEvidence) {
			e.Findings[0].Anchors[0].DocumentID = "doc-other"
		}, "anchor_document_not_in_attempt", false},
		"empty quote": {func(e *auditEvidence) {
			e.Findings[0].Anchors[0].Quote = "  "
		}, "anchor_quote_missing", false},
		"anchor digest mismatch": {func(e *auditEvidence) {
			e.Findings[0].Anchors[0].NormalizedDigest = strings.Repeat("c", 64)
		}, "anchor_digest_mismatch", false},
		"foreign attempt document": {func(e *auditEvidence) {
			foreign := auditTestDocument(digest)
			foreign.Document.ID = "doc-foreign"
			e.Documents = append(e.Documents, foreign)
		}, "foreign_document", false},
		"document digest mismatch": {func(e *auditEvidence) {
			e.Documents[0].OriginalHash = strings.Repeat("d", 64)
			e.Documents[0].Document.Digest = strings.Repeat("d", 64)
		}, "document_digest_mismatch", false},
		"input hash mismatch": {func(e *auditEvidence) {
			e.InputHash = "other-hash"
		}, "attempt_input_hash_mismatch", false},
		"partial attempt": {func(e *auditEvidence) {
			e.AttemptState = "partial"
			e.PartialReason = "模型只完成部分条款"
		}, "attempt_not_completed", false},
		"model failure": {func(e *auditEvidence) {
			e.AttemptErrorCode = "model_timeout"
		}, "model_failure", true},
		"run failure": {func(e *auditEvidence) {
			e.RunState = "failed"
			e.RunFailure = "provider unavailable"
		}, "run_not_completed", true},
		"supporting material only": {func(e *auditEvidence) {
			e.Materials = auditTestMaterials(digest, "supporting")
		}, "submitted_material_missing", false},
		"attempt input absent": {func(e *auditEvidence) {
			e.HasInput = false
			e.Documents = nil
		}, "attempt_input_missing", false},
		"document not ready": {func(e *auditEvidence) {
			e.Documents[0].Ready = false
			e.Documents[0].ParseError = "ocr_failed"
		}, "attempt_document_not_ready", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := baseline
			candidate.Documents = append([]FrozenDocument(nil), baseline.Documents...)
			candidate.Findings = []auditFindingEvidence{{FindingID: "finding-1", Anchors: append([]Anchor(nil), baseline.Findings[0].Anchors...)}}
			candidate.Materials = append([]frozenVersionMaterial(nil), baseline.Materials...)
			tc.mutate(&candidate)
			result := assessAuditEvidence(candidate)
			if result.GateState != auditGateBlocked || !auditReasonPresent(result.Reasons, tc.reason) {
				t.Fatalf("gate = %+v, want blocked with %s", result, tc.reason)
			}
			if result.ModelFailure != tc.modelFail {
				t.Fatalf("model_failure = %t, want %t", result.ModelFailure, tc.modelFail)
			}
		})
	}
}

func TestCompletedAuditWithNoFindingsStillNeedsHuman(t *testing.T) {
	digest := strings.Repeat("e", 64)
	assessment := assessAuditEvidence(auditEvidence{
		RunState: "completed", AttemptState: "completed", HasInput: true,
		AttemptInputHash: "input-hash", InputHash: "input-hash",
		Documents: []FrozenDocument{auditTestDocument(digest)},
		Materials: auditTestMaterials(digest, "signed_contract"),
		Findings:  []auditFindingEvidence{},
	})
	if assessment.GateState != auditGateNeedsHuman || assessment.FindingCount != 0 ||
		assessment.VerifiedFindingCount != 0 || len(assessment.Reasons) != 0 {
		t.Fatalf("no-finding audit gate = %+v", assessment)
	}
	if assessment.GateState == "approved" || assessment.GateState == auditGateBlocked {
		t.Fatal("a clean model result must not become an approval")
	}
}

func TestContractAuditStateDoesNotInheritOldRevision(t *testing.T) {
	oldRevision := "11111111-1111-4111-8111-111111111111"
	newRevision := "22222222-2222-4222-8222-222222222222"
	oldBinding := &ContractAuditBinding{GateState: auditGateBlocked}

	if state, gate := contractAuditStateFor(newRevision, &newRevision, nil); state != auditStateUnbound || gate != auditGateNotApplicable {
		t.Fatalf("new revision without binding = %s/%s, want unbound/not_applicable", state, gate)
	}
	if state, gate := contractAuditStateFor(oldRevision, &newRevision, oldBinding); state != auditStateStale || gate != auditGateBlocked {
		t.Fatalf("old revision binding = %s/%s, want stale/blocked", state, gate)
	}
	if state, gate := contractAuditStateFor(newRevision, &newRevision, oldBinding); state != auditStateCurrent || gate != auditGateBlocked {
		t.Fatalf("current revision binding = %s/%s, want current/blocked", state, gate)
	}
	if state, _ := contractAuditStateFor(oldRevision, nil, oldBinding); state != auditStateStale {
		t.Fatalf("binding without a current revision = %s, want stale", state)
	}
}
