package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/rulepack"
	"contractaudit/internal/telecom"
)

type scriptedToolEvidenceReader struct {
	responses map[string]json.RawMessage
	failures  map[string]bool
	calls     []ToolEvidenceRequest
}

func (r *scriptedToolEvidenceReader) FrozenEvidence(_ context.Context, in ToolEvidenceRequest) (json.RawMessage, error) {
	r.calls = append(r.calls, in)
	if r.failures[in.CapabilityID] {
		return nil, errors.New("tool gateway unavailable")
	}
	raw, ok := r.responses[in.CapabilityID]
	if !ok {
		return nil, errors.New("no scripted response")
	}
	return raw, nil
}

// toolEvidenceFixture is a CPU-only frozen telecom attempt whose draft contains
// two different bandwidth statements (below and above the procurement
// requirement), so the deterministic comparison is a conflict or ambiguous
// outcome that no tool answer may silently remove.
type toolEvidenceFixture struct {
	request   rulepack.Request
	docs      []Interpreted
	canonical []rulepack.Fact
	claim     JobClaim
	caseItem  Case
	runID     string
	attemptID string
}

func newToolEvidenceFixture(t *testing.T) toolEvidenceFixture {
	t.Helper()
	const (
		caseID    = "case_tool_a"
		runID     = "run_tool_a"
		attemptID = "att_tool_a"
		docID     = "doc_tool_a"
		parsedID  = "parsed_tool_a"
	)
	clauses := "；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项。"
	draft := "拟签合同\n电路 LINE-001：最低保障带宽 50 Mbit/s。\n电路 LINE-001：SLA 可用率 99.9%" + clauses +
		"\n电路 LINE-001：最低保障带宽 150 Mbit/s。\n电路 LINE-001：预付款比例 20%，预付款以合同总额为基础，含税。"
	procurementText := "政企专线采购依据\n电路 LINE-001：最低保障带宽 100 Mbit/s。\n电路 LINE-001：SLA 可用率 99.9%" + clauses +
		"\n电路 LINE-001：合同总额 100000 元，含税。"
	approvalText := "电路 LINE-001：审批依据，预付款上限 30%，预付款以合同总额为基础，含税。"
	normalized := func(text string) string {
		sum := sha256.Sum256([]byte(text))
		return hex.EncodeToString(sum[:])
	}
	source := rulepack.Source{ID: docID, Version: parsedID, TenantID: "tenant-a", CaseID: caseID,
		Role: "draft_contract", Digest: normalized(draft), Text: draft}
	procurement := rulepack.Source{ID: "doc_proc", Version: "parsed_proc", TenantID: "tenant-a", CaseID: caseID,
		Role: "procurement", Digest: normalized(procurementText), Text: procurementText}
	approval := rulepack.Source{ID: "doc_appr", Version: "parsed_appr", TenantID: "tenant-a", CaseID: caseID,
		Role: "award", Digest: normalized(approvalText), Text: approvalText}
	request := rulepack.Request{
		Principal: access.Principal{UserID: "alice", TenantID: "tenant-a", Enabled: true,
			Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}},
		Resource:     access.Resource{TenantID: "tenant-a", CaseID: caseID, OrgID: "org-a", OwnerID: "alice"},
		BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service",
		At:      time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC),
		Sources: []rulepack.Source{procurement, approval, source},
	}
	facts, err := telecom.Extract(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Facts = facts
	parsed := &ParsedDocument{ID: parsedID, DocumentID: docID, OriginalDigest: "draft-digest", ParserID: "text", ParserVersion: "1",
		ConfigHash: "cfg", NormalizationVersion: "norm-v1", NormalizedDigest: source.Digest,
		ArtifactDigest: "artifact-digest", Status: "ready", Text: draft}
	document := Document{ID: docID, TenantID: "tenant-a", CaseID: caseID, Role: "draft_contract",
		Digest: "draft-digest", Filename: "draft.txt"}
	docs := []Interpreted{{Document: document, Ready: true, Text: draft, ParsedDocument: parsed}}
	return toolEvidenceFixture{
		request: request, docs: docs, canonical: facts,
		claim: JobClaim{JobID: "job_tool_a", TenantID: "tenant-a", OrgID: "org-a", CaseID: caseID, RunID: runID,
			AttemptID: attemptID, ActorID: "alice", WorkerID: "worker-1", Generation: 3},
		caseItem: Case{ID: caseID, TenantID: "tenant-a", OwnerOrgID: "org-a", BusinessLine: "ENT",
			Scenario: "telecom_dedicated_line", ContractType: "service"},
		runID: runID, attemptID: attemptID,
	}
}

// buildToolPayload turns canonical draft facts into the transport payload the
// tool gateway would return for them. With no facts it builds the honest
// "no supported statement" answer.
func buildToolPayload(t *testing.T, fixture toolEvidenceFixture, capability string, facts ...rulepack.Fact) json.RawMessage {
	t.Helper()
	body := toolEvidencePayload{
		SchemaVersion: toolEvidenceSchemaVersion, ToolVersion: toolEvidenceToolVersion,
		CaseID: fixture.claim.CaseID, RunID: fixture.runID, AttemptID: fixture.attemptID,
		CapabilityID: capability, Status: toolEvidenceStatusEmpty,
	}
	for _, fact := range facts {
		citation := fact.Citations[0]
		body.Facts = append(body.Facts, toolEvidenceFact{
			RuleID: fact.RuleID, ObjectRef: fact.ObjectRef, Value: fact.Value, Unit: fact.Unit,
			MeasurementWindow: fact.MeasurementWindow, TimeZone: fact.TimeZone, Calendar: fact.Calendar,
			OutageDefinition: fact.OutageDefinition, Exclusions: fact.Exclusions, Basis: fact.Basis,
			Citations: []toolEvidenceCitation{{
				DocumentID: citation.SourceID, DocumentRole: "draft_contract", OriginalDigest: "draft-digest",
				ParsedDocumentID: fixture.docs[0].ParsedDocument.ID, ParserID: "text", ParserVersion: "1",
				ConfigHash: "cfg", NormalizationVersion: "norm-v1", NormalizedDigest: fixture.docs[0].ParsedDocument.NormalizedDigest,
				ArtifactDigest: "artifact-digest", SourceVersion: citation.SourceVersion, SourceDigest: citation.SourceDigest,
				Start: citation.Start, End: citation.End, Quote: citation.Quote,
			}},
		})
	}
	if len(body.Facts) > 0 {
		body.Status = toolEvidenceStatusFound
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func bandwidthFacts(fixture toolEvidenceFixture) (lower, upper rulepack.Fact) {
	for _, fact := range fixture.canonical {
		if fact.RuleID != "ENT-TECH-BW-001" {
			continue
		}
		if fact.Value == "50" {
			lower = fact
		} else {
			upper = fact
		}
	}
	return lower, upper
}

func reportBytes(t *testing.T, request rulepack.Request) []byte {
	t.Helper()
	report, err := telecom.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestToolEvidenceConsumerCorroboratesCompleteDraftEvidenceOnly is the safety
// regression for the frozen-evidence consumer: a tool answer that is a subset,
// a duplicate, empty although statements exist, or mixed valid/ungrounded must
// leave the deterministic evidence and the RulePack outcome byte-identical, and
// only a complete equivalent set may be corroborated.
func TestToolEvidenceConsumerCorroboratesCompleteDraftEvidenceOnly(t *testing.T) {
	fixture := newToolEvidenceFixture(t)
	lower, upper := bandwidthFacts(fixture)
	if lower.ID == "" || upper.ID == "" || lower.ID == upper.ID || len(lower.Citations) != 1 || len(upper.Citations) != 1 ||
		lower.Citations[0].Start == upper.Citations[0].Start {
		t.Fatalf("test setup: expected two distinct bandwidth statements: %+v %+v", lower, upper)
	}
	bandwidth := mandatoryToolCapabilities[0]
	deterministicFacts := append([]rulepack.Fact(nil), fixture.request.Facts...)
	deterministic := reportBytes(t, fixture.request)
	detReport, err := telecom.Execute(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if len(detReport.Results) != len(mandatoryToolCapabilities) {
		t.Fatalf("test setup: the deterministic run did not evaluate every rule: %+v", detReport.Results)
	}

	emptyAnswers := func() map[string]json.RawMessage {
		responses := make(map[string]json.RawMessage, len(mandatoryToolCapabilities))
		for _, capability := range mandatoryToolCapabilities {
			responses[capability.CapabilityID] = buildToolPayload(t, fixture, capability.CapabilityID)
		}
		return responses
	}
	consume := func(name string, responses map[string]json.RawMessage, failures map[string]bool) (toolEvidenceOutcome, rulepack.Request) {
		t.Helper()
		reader := &scriptedToolEvidenceReader{responses: responses, failures: failures}
		st := &Store{}
		if err := st.UseToolEvidenceReader(reader); err != nil {
			t.Fatal(err)
		}
		outcome, err := st.consumeToolEvidence(context.Background(), fixture.runID, fixture.attemptID, fixture.claim, fixture.caseItem,
			fixture.request, fixture.docs)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(reader.calls) != len(mandatoryToolCapabilities) {
			t.Fatalf("%s: the consumer did not ask for every mandatory capability in order: %+v", name, reader.calls)
		}
		for i, capability := range mandatoryToolCapabilities {
			call := reader.calls[i]
			if call.CapabilityID != capability.CapabilityID || call.CaseID != fixture.claim.CaseID ||
				call.RunID != fixture.runID || call.AttemptID != fixture.attemptID || call.JobID != fixture.claim.JobID ||
				call.WorkerID != fixture.claim.WorkerID || call.Generation != fixture.claim.Generation ||
				call.TenantID != fixture.claim.TenantID || call.OrgID != fixture.claim.OrgID || call.Deadline.IsZero() {
				t.Fatalf("%s: tool call %d did not carry the frozen claim binding: %+v", name, i, call)
			}
		}
		evaluated := fixture.request
		evaluated.Facts = appendToolEvidenceFacts(fixture.request.Facts, outcome.Accepted)
		return outcome, evaluated
	}
	capabilityStatus := func(outcome toolEvidenceOutcome, capabilityID string) toolEvidenceCapability {
		t.Helper()
		for _, capability := range outcome.Capabilities {
			if capability.CapabilityID == capabilityID {
				return capability
			}
		}
		t.Fatalf("capability %s has no outcome: %+v", capabilityID, outcome.Capabilities)
		return toolEvidenceCapability{}
	}
	assertUntouched := func(name string, outcome toolEvidenceOutcome, evaluated rulepack.Request, status, reason string) {
		t.Helper()
		if !outcome.Incomplete {
			t.Fatalf("%s was reported complete: %+v", name, outcome.Capabilities)
		}
		if len(outcome.Accepted) != 0 {
			t.Fatalf("%s admitted facts: %+v", name, outcome.Accepted)
		}
		entry := capabilityStatus(outcome, bandwidth.CapabilityID)
		if entry.Status != status || entry.ReasonCode != reason {
			t.Fatalf("%s: unexpected capability outcome %+v", name, entry)
		}
		if len(evaluated.Facts) != len(deterministicFacts) {
			t.Fatalf("%s changed the evaluated fact set: %+v", name, evaluated.Facts)
		}
		if string(reportBytes(t, evaluated)) != string(deterministic) {
			t.Fatalf("%s changed the RulePack outcome", name)
		}
	}

	// 1. A subset of the draft statements: the conflict/ambiguity must survive.
	responses := emptyAnswers()
	responses[bandwidth.CapabilityID] = buildToolPayload(t, fixture, bandwidth.CapabilityID, upper)
	outcome, evaluated := consume("subset", responses, nil)
	assertUntouched("a subset of the extracted statements", outcome, evaluated, toolEvidenceRejected, "tool_evidence_incomplete_set")

	// 2. An empty answer although the attempt extracted statements.
	responses = emptyAnswers()
	outcome, evaluated = consume("empty with statements", responses, nil)
	assertUntouched("an empty answer with extracted statements", outcome, evaluated, toolEvidenceRejected, "tool_evidence_missing_canonical_set")

	// 3. A duplicated statement.
	responses = emptyAnswers()
	responses[bandwidth.CapabilityID] = buildToolPayload(t, fixture, bandwidth.CapabilityID, upper, upper)
	outcome, evaluated = consume("duplicate", responses, nil)
	assertUntouched("a duplicated statement", outcome, evaluated, toolEvidenceRejected, "tool_evidence_not_grounded")

	// 4. Mixed valid and ungrounded facts: the whole capability is refused.
	responses = emptyAnswers()
	responses[bandwidth.CapabilityID] = tamperPayloadValue(t, buildToolPayload(t, fixture, bandwidth.CapabilityID, lower, upper), `"value":"150"`, `"value":"999"`)
	outcome, evaluated = consume("mixed", responses, nil)
	assertUntouched("a mixed valid and ungrounded answer", outcome, evaluated, toolEvidenceRejected, "tool_evidence_not_grounded")

	// 5. A tool failure is reported per capability and keeps the evidence.
	responses = emptyAnswers()
	outcome, evaluated = consume("failed", responses, map[string]bool{bandwidth.CapabilityID: true})
	assertUntouched("a failed mandatory capability", outcome, evaluated, toolEvidenceFailed, "tool_evidence_call_failed")

	// 6. The complete equivalent set (any order, every mandatory capability) is
	// corroborated: same outcome, stable canonical IDs and no duplicated facts.
	responses = emptyAnswers()
	for _, capability := range mandatoryToolCapabilities {
		var facts []rulepack.Fact
		for _, fact := range fixture.canonical {
			if fact.RuleID == capability.RuleID {
				facts = append(facts, fact)
			}
		}
		if len(facts) == 0 {
			t.Fatalf("test setup: no canonical facts for %s", capability.RuleID)
		}
		if capability.CapabilityID == bandwidth.CapabilityID {
			facts[0], facts[len(facts)-1] = facts[len(facts)-1], facts[0]
		}
		responses[capability.CapabilityID] = buildToolPayload(t, fixture, capability.CapabilityID, facts...)
	}
	outcome, evaluated = consume("complete", responses, nil)
	if outcome.Incomplete {
		t.Fatalf("a complete equivalent set was reported incomplete: %+v", outcome.Capabilities)
	}
	admitted := map[string]rulepack.Fact{}
	for _, fact := range outcome.Accepted {
		admitted[fact.ID] = fact
	}
	expected := map[string]bool{}
	for _, fact := range fixture.canonical {
		if fact.SourceRole == "draft_contract" {
			expected[fact.ID] = true
		}
	}
	if len(expected) == 0 || len(admitted) != len(expected) {
		t.Fatalf("the complete set was not admitted: expected=%d admitted=%+v", len(expected), outcome.Accepted)
	}
	for id := range expected {
		if _, ok := admitted[id]; !ok {
			t.Fatalf("canonical fact %s lost its identity during corroboration: %+v", id, outcome.Accepted)
		}
	}
	if _, ok := admitted[lower.ID]; !ok {
		t.Fatalf("the lower statement lost its canonical identity: %+v", outcome.Accepted)
	}
	if _, ok := admitted[upper.ID]; !ok {
		t.Fatalf("the upper statement lost its canonical identity: %+v", outcome.Accepted)
	}
	if capabilityStatus(outcome, bandwidth.CapabilityID).Status != toolEvidenceAccepted || len(evaluated.Facts) != len(deterministicFacts) {
		t.Fatalf("corroboration changed the evaluated fact set: %+v", evaluated.Facts)
	}
	if string(reportBytes(t, evaluated)) != string(deterministic) {
		t.Fatal("the same frozen sources produced a different RulePack outcome after corroboration")
	}
	report, err := telecom.Execute(context.Background(), evaluated)
	if err != nil {
		t.Fatal(err)
	}
	ruled := map[string]bool{}
	for _, result := range report.Results {
		ruled[result.RuleID] = true
	}
	for _, capability := range mandatoryToolCapabilities {
		if !ruled[capability.RuleID] {
			t.Fatalf("rule %s stopped running: %+v", capability.RuleID, report.Results)
		}
	}
	// Every mandatory capability was corroborated by its own complete set.
	for _, capability := range mandatoryToolCapabilities {
		entry := capabilityStatus(outcome, capability.CapabilityID)
		if entry.Status != toolEvidenceAccepted || entry.Rejected != 0 {
			t.Fatalf("capability %s was not corroborated: %+v", capability.CapabilityID, entry)
		}
	}

	// 7. A local pipeline without a reader keeps its established behaviour.
	legacy := &Store{}
	legacyOutcome, err := legacy.consumeToolEvidence(context.Background(), fixture.runID, fixture.attemptID, fixture.claim, fixture.caseItem,
		fixture.request, fixture.docs)
	if err != nil || legacyOutcome.AdapterPresent || len(legacyOutcome.Accepted) != 0 || len(legacyOutcome.Capabilities) != 0 {
		t.Fatalf("reader-less pipeline consumed evidence: %+v err=%v", legacyOutcome, err)
	}
}

func tamperPayloadValue(t *testing.T, payload json.RawMessage, old, new string) json.RawMessage {
	t.Helper()
	tampered := strings.Replace(string(payload), old, new, 1)
	if tampered == string(payload) {
		t.Fatalf("test setup: %q not present in %s", old, payload)
	}
	return json.RawMessage(tampered)
}
