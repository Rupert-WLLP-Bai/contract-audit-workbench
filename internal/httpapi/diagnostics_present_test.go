package httpapi

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"contractaudit/internal/docir"
	"contractaudit/internal/store"
)

func TestBusinessWorkspaceProjectsOnlyBusinessData(t *testing.T) {
	text := "质保36个月。"
	ir := docir.Build(text)
	parsed := &store.ParsedDocument{
		ID: "parse-1", DocumentID: "doc-1", Status: "ready", CreatedAt: "2026-01-01T00:00:00Z",
		OriginalDigest: "private-original-hash", NormalizedDigest: ir.NormalizedDigest,
		ConfigHash: "private-config-hash", ArtifactDigest: "private-artifact-hash",
	}
	doc := store.Interpreted{
		Document: store.Document{ID: "doc-1", Role: "draft_contract", Filename: "合同.txt", Digest: parsed.OriginalDigest},
		Ready:    true, Text: text, IR: ir, ParsedDocument: parsed,
	}
	anchor := store.Anchor{ID: "anchor-1", DocumentID: "doc-1", ParsedDocumentID: parsed.ID,
		NormalizedDigest: ir.NormalizedDigest, Quote: text, Status: "located", Start: 0, End: len([]rune(text))}
	privateHash := "private-attempt-hash"
	attempt := store.Attempt{ID: "attempt-2", RunID: "run-1", Ordinal: 2, State: "running", InputHash: &privateHash,
		HistoryKind: "frozen", ParsedDocumentIDs: []string{"parse-1"}}
	prior := store.Attempt{ID: "attempt-1", RunID: "run-1", Ordinal: 1, State: "failed", OutputHash: &privateHash}
	ws := store.Workspace{
		Case:        store.Case{ID: "case-1"},
		View:        store.RunView{Run: store.Run{ID: "run-1", State: "running", ModelRef: "private-model"}, State: "completed"},
		Documents:   []store.Interpreted{doc},
		Snapshot:    store.RunSnapshot{DocumentIDs: []string{"doc-1"}, RuleSetHash: "private-rule-hash", PlaybookHash: "private-playbook-hash"},
		Audit:       []store.AuditRecord{{EventID: "audit-1", RunID: "run-1", Actor: "reviewer", Seq: 1, EventType: "review_started", Hash: "private-audit-hash", CorrelationIDs: []string{"case-1"}}},
		Revisions:   []store.Revision{{Mark: "changed", Claim: "质保要求", Anchors: []store.Anchor{anchor}}},
		Evaluations: []store.ConstraintEval{{ID: "eval-1", Domain: "commercial", Relation: "conflict", Reason: "期限不一致"}},
		Execution: &store.Execution{
			SchemaVersion: "private-execution-schema", Attempt: &attempt, Attempts: []store.Attempt{prior, attempt},
			AgentReview: json.RawMessage(`{
				"schema_version":"private-agent-schema","attempt_id":"attempt-2","status":"partial",
				"mode":"private-mode","source_snapshot":{"parse_selection_hash":"private-source-hash"},
				"planner":{"prompt_sha256":"private-prompt-hash"},"events":[{"code":"private-trace"}],
				"plan":{"status":"completed"},"usage":{"planner_calls":1},
				"rulepack":{"id":"rules-1","version":"2","content_sha256":"private-pack-hash"},
				"scope":[{"domain":"commercial","capability_ids":["warranty"],"source_ids":["doc-1"],"trace":"private-scope-trace"}],
				"domains":[{"domain":"commercial","status":"partial","capabilities":[{"capability_id":"warranty","mandatory":true,"status":"insufficient","relation":"insufficient","reason_code":"missing_term","evidence_ids":["evidence-1"],"canonical_evaluation_ids":["eval-1"],"hash":"private-capability-hash"}]}],
				"gaps":[{"gap_id":"gap-1","domain":"commercial","capability_id":"warranty","mandatory":true,"status":"open","reason_code":"missing_term","evidence_ids":["evidence-1"]}],
				"evidence":[{"evidence_id":"evidence-1","semantic_type":"term","value":"36","unit":"month","currency":null,"verification":"verified","anchor":{"id":"anchor-1","document_id":"doc-1","parsed_document_id":"parse-1","normalized_digest":"private-anchor-hash","quote":"质保36个月。","status":"located","start":0,"end":7,"reason":null,"page":null,"element_id":null}}],
				"unresolved_gap_ids":["gap-1"]
			}`),
		},
	}

	body := diagnosticTestObject(t, workspaceJSON(nil, ws))
	assertNoDiagnosticKeys(t, body)
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-") {
		t.Fatalf("technical data reached the business response: %s", encoded)
	}
	if body["run"].(map[string]any)["state"] != "running" {
		t.Fatalf("missing execution events fabricated completion: %v", body["run"])
	}
	if body["execution"] != nil || len(body["audit_events"].([]any)) != 0 {
		t.Fatalf("legacy diagnostic fields were populated: %v", body)
	}
	businessAttempts := body["review_attempts"].(map[string]any)
	if businessAttempts["attempt"].(map[string]any)["id"] != "attempt-2" || len(businessAttempts["attempts"].([]any)) != 2 {
		t.Fatalf("attempt identity or history lost: %v", businessAttempts)
	}
	document := body["documents"].([]any)[0].(map[string]any)
	if document["text"] != text || document["parsed_document_id"] != "parse-1" || document["parsed_document"] != nil {
		t.Fatalf("business document lost its text or parse identity: %v", document)
	}
	revision := body["revisions"].([]any)[0].(map[string]any)
	citation := revision["evidence"].([]any)[0].(map[string]any)
	if citation["status"] != "located" || citation["parsed_document_id"] != "parse-1" || citation["text_offset"] == nil {
		t.Fatalf("verified citation lost its business location: %v", citation)
	}
	businessReview := body["agent_review"].(map[string]any)
	if businessReview["status"] != "partial" || businessReview["attempt_id"] != "attempt-2" || businessReview["rulepack"].(map[string]any)["version"] != "2" {
		t.Fatalf("review status or identity lost: %v", businessReview)
	}
	domain := businessReview["domains"].([]any)[0].(map[string]any)
	capability := domain["capabilities"].([]any)[0].(map[string]any)
	if domain["status"] != "partial" || capability["status"] != "insufficient" || capability["mandatory"] != true {
		t.Fatalf("business coverage lost: %v", domain)
	}
	gap := businessReview["gaps"].([]any)[0].(map[string]any)
	evidence := businessReview["evidence"].([]any)[0].(map[string]any)
	if gap["status"] != "open" || evidence["value"] != "36" || evidence["anchor"].(map[string]any)["quote"] != text {
		t.Fatalf("business gap or evidence lost: %v %v", gap, evidence)
	}
	businessEvent := body["business_events"].([]any)[0].(map[string]any)
	if businessEvent["event_type"] != "review_started" || businessEvent["actor"] != "reviewer" {
		t.Fatalf("business event lost: %v", businessEvent)
	}

	ws.Findings = []store.Finding{{ID: "finding-1", Anchors: []store.Anchor{anchor}}}
	diagnostics := diagnosticTestObject(t, reviewDiagnosticsJSON(ws))
	if diagnostics["execution"].(map[string]any)["schema_version"] != "private-execution-schema" ||
		diagnostics["snapshot"].(map[string]any)["rule_set_hash"] != "private-rule-hash" ||
		diagnostics["audit_events"].([]any)[0].(map[string]any)["hash"] != "private-audit-hash" ||
		diagnostics["documents"].([]any)[0].(map[string]any)["digest"] != "private-original-hash" ||
		diagnostics["evidence"].([]any)[0].(map[string]any)["anchors"].([]any)[0].(map[string]any)["normalized_digest"] != ir.NormalizedDigest {
		t.Fatalf("diagnostics lost the separately authorized technical data: %v", diagnostics)
	}
}

func TestDiagnosticsPreservesFrozenGovernedKnowledge(t *testing.T) {
	policy := store.KnowledgeRef{KnowledgeID: "policy", VersionID: "policy-version", ContentSHA256: "policy-hash"}
	optional := store.KnowledgeRef{KnowledgeID: "experience", VersionID: "experience-version", ContentSHA256: "experience-hash"}
	excluded := store.KnowledgeRef{KnowledgeID: "excluded", VersionID: "excluded-version", ContentSHA256: "excluded-hash"}
	frozen := &store.ReviewKnowledgeSnapshot{
		Encoding: "review-knowledge-snapshot/v1", BusinessAt: "2026-02-03T01:05:06Z",
		Scope:                 store.KnowledgeScope{OrgID: "org", CaseID: "case", BusinessLine: "ENT", Scenario: "telecom", ContractType: "service"},
		AuthorizationRevision: "authorization-revision", SnapshotSHA256: "snapshot-hash",
		RulePack: store.GovernedRulePackRef{PackID: "pack", Version: "1", ContentHash: "pack-hash", SnapshotDigest: "pack-snapshot", PointerRevision: 7},
		RequiredPolicies: []store.GovernedKnowledgeSelection{{
			Ref: policy, Kind: "policy", Required: true, Summary: "mandatory policy",
			Binding: store.PolicyRuleBinding{PackID: "pack", RuleIDs: []string{"rule-1"}, SourceSpans: []store.KnowledgeSourceSpan{{RuleID: "rule-1", Start: 0, End: 4, Quote: "期限"}}},
		}},
		OptionalCandidates: []store.GovernedKnowledgeSelection{{Ref: optional, Kind: "experience", Summary: "reviewed experience"}},
		Excluded:           []store.GovernedKnowledgeExclusion{{Ref: excluded, Reason: "optional_budget"}},
		OptionalTruncated:  true,
	}
	ws := store.Workspace{Snapshot: store.RunSnapshot{GovernedKnowledge: frozen}}
	business := diagnosticTestObject(t, workspaceJSON(nil, ws))
	if _, present := business["snapshot"].(map[string]any)["governed_knowledge"]; present {
		t.Fatal("frozen diagnostic knowledge leaked into the business workspace")
	}
	diagnostics := diagnosticTestObject(t, reviewDiagnosticsJSON(ws))
	got := diagnostics["snapshot"].(map[string]any)["governed_knowledge"]
	if want := diagnosticTestObject(t, frozen); !reflect.DeepEqual(got, want) {
		t.Fatalf("frozen knowledge changed in diagnostics: got %v want %v", got, want)
	}
	ws.Snapshot.GovernedKnowledge = nil
	diagnostics = diagnosticTestObject(t, reviewDiagnosticsJSON(ws))
	if got := diagnostics["snapshot"].(map[string]any)["governed_knowledge"]; got != nil {
		t.Fatalf("historical run fabricated a knowledge snapshot: %v", got)
	}
}

func TestBusinessWorkspaceNeverInfersSuccessFromMissingEvents(t *testing.T) {
	for _, state := range []string{"accepted", "queued", "running", "partial", "failed", "cancelled", "interrupted", "unknown", ""} {
		t.Run("state="+state, func(t *testing.T) {
			attempt := store.Attempt{ID: "attempt", State: state}
			ws := store.Workspace{View: store.RunView{Run: store.Run{ID: "run", State: state}, State: "completed"},
				Execution: &store.Execution{Attempt: &attempt}}
			body := workspaceJSON(nil, ws)
			if got := body["run"].(map[string]any)["state"]; got != state {
				t.Fatalf("unobserved events changed attempt state %q to %v", state, got)
			}
			if body["agent_review"] != nil {
				t.Fatalf("missing agent review became a result: %v", body["agent_review"])
			}
		})
	}
	attempt := store.Attempt{ID: "attempt", State: "completed"}
	ws := store.Workspace{View: store.RunView{Run: store.Run{State: "completed"}, State: "awaiting_human"},
		Execution: &store.Execution{Attempt: &attempt}}
	if got := workspaceJSON(nil, ws)["run"].(map[string]any)["state"]; got != "awaiting_human" {
		t.Fatalf("completed attempt lost pending human decisions: %v", got)
	}
	projected := businessAgentReviewJSON(json.RawMessage(`{"attempt_id":"attempt","events":[]}`)).(map[string]any)
	if projected["status"] != nil {
		t.Fatalf("missing review status was treated as success: %v", projected)
	}
	if businessAgentReviewJSON(json.RawMessage(`{"status":42}`)) != nil {
		t.Fatal("malformed persisted review produced a business result")
	}
}

func diagnosticTestObject(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertNoDiagnosticKeys(t *testing.T, value any) {
	t.Helper()
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if strings.Contains(key, "hash") || strings.Contains(key, "digest") || strings.Contains(key, "sha256") ||
				strings.Contains(key, "schema") || strings.Contains(key, "trace") || key == "planner" || key == "plan" || key == "usage" || key == "events" {
				t.Fatalf("diagnostic key %q in business response", key)
			}
			assertNoDiagnosticKeys(t, child)
		}
	case []any:
		for _, child := range item {
			assertNoDiagnosticKeys(t, child)
		}
	}
}
