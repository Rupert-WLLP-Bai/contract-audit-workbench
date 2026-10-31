package httpapi

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"contractaudit/internal/apidocs"
	"contractaudit/internal/docir"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

const (
	citationCaseID     = "case-1"
	citationRunID      = "run-1"
	citationAttemptID  = "attempt-1"
	citationEvidenceID = "fact_0123456789abcdef01234567"
	citationOriginal   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	citationText       = "😀甲方应支付100万元。"
)

// citationFrozenDigest is the normalized digest of the fixture's frozen text.
func citationFrozenDigest() string { return docir.Build(citationText).NormalizedDigest }

// citationFixture is one accepted attempt whose frozen text carries an emoji
// before the quoted range, so a UTF-16 based offset would land one code point
// early and fail the reader.
func citationFixture(t *testing.T) (store.Workspace, int, int) {
	t.Helper()
	text, quote := citationText, "100万元"
	start, end := 6, 11
	if got, ok := docir.Slice(text, start, end); !ok || got != quote {
		t.Fatalf("fixture range is not the promised code-point slice: %q", got)
	}
	ir := docir.Build(text)
	parsed := &store.ParsedDocument{
		ID: "parsed-1", DocumentID: "doc-1", Status: "ready",
		OriginalDigest: citationOriginal, NormalizedDigest: ir.NormalizedDigest,
	}
	doc := store.Interpreted{
		Document: store.Document{ID: "doc-1", CaseID: citationCaseID, Filename: "合同.txt", Digest: citationOriginal},
		Ready:    true, Text: text, IR: ir, ParsedDocument: parsed,
	}
	attempt := store.Attempt{
		ID: citationAttemptID, RunID: citationRunID, Ordinal: 1, State: "completed",
		HistoryKind: "frozen", ParsedDocumentIDs: []string{"parsed-1"},
	}
	return store.Workspace{
		Case:      store.Case{ID: citationCaseID},
		Documents: []store.Interpreted{doc},
		Execution: &store.Execution{
			Attempt: &attempt, Attempts: []store.Attempt{attempt},
			AgentReview: citationReviewJSON(t, citationAttemptID, citationEvidenceID,
				citationAnchorJSON(start, end, quote, ir.NormalizedDigest), "typed_semantic_and_frozen_quote_verified"),
		},
	}, start, end
}

func citationAnchorJSON(start, end int, quote, digest string) map[string]any {
	return map[string]any{
		"id": "anchor-1", "document_id": "doc-1", "parsed_document_id": "parsed-1",
		"normalized_digest": digest, "quote": quote, "status": "located",
		"start": start, "end": end, "reason": nil, "page": nil, "element_id": nil,
	}
}

func citationReviewJSON(t *testing.T, attemptID, evidenceID string, anchor map[string]any, verification string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"attempt_id": attemptID,
		"status":     "completed",
		"evidence": []any{map[string]any{
			"evidence_id": evidenceID, "semantic_type": "warranty", "value": "36",
			"unit": "month", "currency": nil, "verification": verification, "anchor": anchor,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func cloneCitationWorkspace(workspace store.Workspace) store.Workspace {
	out := workspace
	out.Documents = make([]store.Interpreted, len(workspace.Documents))
	for i, doc := range workspace.Documents {
		parsed := *doc.ParsedDocument
		doc.ParsedDocument = &parsed
		out.Documents[i] = doc
	}
	execution := *workspace.Execution
	attempt := *workspace.Execution.Attempt
	execution.Attempt = &attempt
	execution.Attempts = append([]store.Attempt(nil), workspace.Execution.Attempts...)
	execution.AgentReview = append(json.RawMessage(nil), workspace.Execution.AgentReview...)
	out.Execution = &execution
	return out
}

func replaceCitationReview(t *testing.T, workspace *store.Workspace, anchor map[string]any, verification string) {
	t.Helper()
	workspace.Execution.AgentReview = citationReviewJSON(t, citationAttemptID, citationEvidenceID, anchor, verification)
}

func citationFault(t *testing.T, err error) *fault.Error {
	t.Helper()
	var target *fault.Error
	if !errors.As(err, &target) {
		t.Fatalf("expected an API fault, got %v", err)
	}
	return target
}

func TestContractCitationReadsExactFrozenCodePointRange(t *testing.T) {
	workspace, start, end := citationFixture(t)
	citation, err := contractCitationJSON(workspace, citationCaseID, citationRunID, citationAttemptID, citationEvidenceID)
	if err != nil {
		t.Fatalf("read the frozen citation: %v", err)
	}
	doc := workspace.Documents[0]
	if citation.CaseID != citationCaseID || citation.RunID != citationRunID || citation.AttemptID != citationAttemptID ||
		citation.EvidenceID != citationEvidenceID || citation.DocumentID != "doc-1" || citation.ParsedDocumentID != "parsed-1" ||
		citation.Filename != "合同.txt" || citation.OriginalDigest != citationOriginal ||
		citation.ParseDigest != workspace.Documents[0].ParsedDocument.NormalizedDigest {
		t.Fatalf("citation identity drifted: %+v", citation)
	}
	if citation.Start != start || citation.End != end || citation.Quote != "100万元" || citation.Text != doc.Text {
		t.Fatalf("citation position is not the frozen code-point slice: %+v", citation)
	}
	if citation.MaterialID != nil || citation.MaterialVersion != nil {
		t.Fatalf("unresolved material identity was invented: %+v", citation)
	}
}

func TestContractCitationNeverFallsBackToANewerParseOrAttempt(t *testing.T) {
	workspace, start, end := citationFixture(t)
	// A republished parse of the same document is present in the workspace, as
	// the previous-run comparison may append it. The frozen attempt parse stays
	// authoritative for its own citation.
	newerIR := docir.Build("拟签合同约定100万元。")
	reparsed := *workspace.Documents[0].ParsedDocument
	reparsed.ID, reparsed.Status, reparsed.NormalizedDigest = "parsed-2", "ready", newerIR.NormalizedDigest
	newer := workspace.Documents[0]
	newer.ParsedDocument = &reparsed
	newer.Text, newer.IR = "拟签合同约定100万元。", newerIR
	workspace.Documents = append(workspace.Documents, newer)
	citation, err := contractCitationJSON(workspace, citationCaseID, citationRunID, citationAttemptID, citationEvidenceID)
	if err != nil || citation.ParsedDocumentID != "parsed-1" || citation.Text != workspace.Documents[0].Text ||
		citation.ParseDigest != workspace.Documents[0].ParsedDocument.NormalizedDigest || citation.Start != start || citation.End != end {
		t.Fatalf("citation fell back to the newer parse: citation=%+v err=%v", citation, err)
	}

	// The attempt only froze parsed-1. A citation that names any other parse is
	// refused even when a document with that parse is present in the workspace.
	workspace = cloneCitationWorkspace(workspace)
	workspace.Execution.Attempt.ParsedDocumentIDs = []string{"parsed-2"}
	_, err = contractCitationJSON(workspace, citationCaseID, citationRunID, citationAttemptID, citationEvidenceID)
	if conflict := citationFault(t, err); conflict.Code != "conflict" {
		t.Fatalf("foreign parse was not refused: %+v", conflict)
	}

	// The exact apply goes the other way too: when the attempt's own parse is
	// missing from the workspace, the reader must not substitute current text.
	workspace, _, _ = citationFixture(t)
	workspace = cloneCitationWorkspace(workspace)
	workspace.Documents = []store.Interpreted{newer}
	_, err = contractCitationJSON(workspace, citationCaseID, citationRunID, citationAttemptID, citationEvidenceID)
	if conflict := citationFault(t, err); conflict.Code != "conflict" {
		t.Fatalf("missing frozen parse was not refused: %+v", conflict)
	}
}

func TestContractCitationRejectsUnprovenIdentityOrRange(t *testing.T) {
	for _, test := range []struct {
		name  string
		apply func(*testing.T, *store.Workspace)
	}{
		{name: "other attempt review", apply: func(t *testing.T, workspace *store.Workspace) {
			workspace.Execution.AgentReview = citationReviewJSON(t, "attempt-2", citationEvidenceID,
				citationAnchorJSON(6, 11, "100万元", citationFrozenDigest()), "typed_semantic_and_frozen_quote_verified")
		}},
		{name: "unverified evidence", apply: func(t *testing.T, workspace *store.Workspace) {
			replaceCitationReview(t, workspace, citationAnchorJSON(6, 11, "100万元", citationFrozenDigest()), "unknown")
		}},
		{name: "anchor not located", apply: func(t *testing.T, workspace *store.Workspace) {
			anchor := citationAnchorJSON(6, 11, "100万元", citationFrozenDigest())
			anchor["status"] = "unlocated"
			replaceCitationReview(t, workspace, anchor, "typed_semantic_and_frozen_quote_verified")
		}},
		{name: "missing parse identity", apply: func(t *testing.T, workspace *store.Workspace) {
			anchor := citationAnchorJSON(6, 11, "100万元", citationFrozenDigest())
			anchor["parsed_document_id"] = ""
			replaceCitationReview(t, workspace, anchor, "typed_semantic_and_frozen_quote_verified")
		}},
		{name: "missing digest", apply: func(t *testing.T, workspace *store.Workspace) {
			replaceCitationReview(t, workspace, citationAnchorJSON(6, 11, "100万元", ""), "typed_semantic_and_frozen_quote_verified")
		}},
		{name: "digest mismatch", apply: func(t *testing.T, workspace *store.Workspace) {
			replaceCitationReview(t, workspace, citationAnchorJSON(6, 11, "100万元", strings.Repeat("d", 64)), "typed_semantic_and_frozen_quote_verified")
		}},
		{name: "range mismatch", apply: func(t *testing.T, workspace *store.Workspace) {
			replaceCitationReview(t, workspace, citationAnchorJSON(5, 11, "100万元", citationFrozenDigest()), "typed_semantic_and_frozen_quote_verified")
		}},
		{name: "quote mismatch", apply: func(t *testing.T, workspace *store.Workspace) {
			replaceCitationReview(t, workspace, citationAnchorJSON(6, 11, "100万", citationFrozenDigest()), "typed_semantic_and_frozen_quote_verified")
		}},
		{name: "other case document", apply: func(t *testing.T, workspace *store.Workspace) {
			workspace.Documents[0].Document.CaseID = "other-case"
		}},
		{name: "document identity mismatch", apply: func(t *testing.T, workspace *store.Workspace) {
			workspace.Documents[0].Document.ID = "doc-2"
		}},
		{name: "not a frozen attempt", apply: func(t *testing.T, workspace *store.Workspace) {
			workspace.Execution.Attempt.HistoryKind = "legacy_unfrozen"
		}},
		{name: "no agent review", apply: func(t *testing.T, workspace *store.Workspace) {
			workspace.Execution.AgentReview = nil
		}},
		{name: "unreadable agent review", apply: func(t *testing.T, workspace *store.Workspace) {
			workspace.Execution.AgentReview = json.RawMessage(`{"attempt_id":`)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace, _, _ := citationFixture(t)
			workspace = cloneCitationWorkspace(workspace)
			test.apply(t, &workspace)
			citation, err := contractCitationJSON(workspace, citationCaseID, citationRunID, citationAttemptID, citationEvidenceID)
			if err == nil {
				t.Fatalf("unproven citation was read: %+v", citation)
			}
			if got := citationFault(t, err); got.Code != "conflict" {
				t.Fatalf("unexpected failure code %q: %s", got.Code, got.Message)
			}
		})
	}
}

func TestContractCitationRejectsUnknownEvidenceAndAttempt(t *testing.T) {
	workspace, _, _ := citationFixture(t)
	if _, err := contractCitationJSON(workspace, citationCaseID, citationRunID, citationAttemptID, "fact_ffffffffffffffffffffffff"); citationFault(t, err).Code != "not_found" {
		t.Fatalf("unknown evidence was not reported as not found: %v", err)
	}
	if _, err := contractCitationJSON(workspace, citationCaseID, citationRunID, "attempt-absent", citationEvidenceID); citationFault(t, err).Code != "not_found" {
		t.Fatalf("absent attempt was not reported as not found: %v", err)
	}
	withoutExecution := workspace
	withoutExecution.Execution = nil
	if _, err := contractCitationJSON(withoutExecution, citationCaseID, citationRunID, citationAttemptID, citationEvidenceID); citationFault(t, err).Code != "not_found" {
		t.Fatalf("attempt without execution was not reported as not found: %v", err)
	}
	if _, err := contractCitationJSON(workspace, citationCaseID, citationRunID, citationAttemptID, ""); citationFault(t, err).Code != "invalid" {
		t.Fatalf("empty evidence identity was not rejected: %v", err)
	}
}

func TestContractCitationWireFieldsMatchOpenAPISchema(t *testing.T) {
	var spec map[string]any
	if err := json.Unmarshal(apidocs.Document(), &spec); err != nil {
		t.Fatal(err)
	}
	schema := spec["components"].(map[string]any)["schemas"].(map[string]any)["ContractCitation"].(map[string]any)
	raw, err := json.Marshal(contractCitation{})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(body))
	for key := range body {
		got = append(got, key)
	}
	sort.Strings(got)
	properties := schema["properties"].(map[string]any)
	want := make([]string, 0, len(properties))
	for key := range properties {
		want = append(want, key)
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("citation wire fields drifted from the spec: Go %v, spec %v", got, want)
	}
	required := make([]string, 0, len(schema["required"].([]any)))
	for _, item := range schema["required"].([]any) {
		required = append(required, item.(string))
	}
	sort.Strings(required)
	if !reflect.DeepEqual(required, want) {
		t.Fatalf("citation spec required fields drifted: %v", required)
	}
}
