package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/docir"
	"contractaudit/internal/rulepack"
	"contractaudit/internal/telecom"
)

const provenanceSLABasis = "；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项。"

type provenanceFixture struct {
	caseItem Case
	docs     []Interpreted
	request  rulepack.Request
	report   rulepack.Report
	snapshot []byte
}

func newProvenanceFixture(t *testing.T) provenanceFixture {
	t.Helper()
	item := Case{ID: "case-provenance", TenantID: "tenant-a", OwnerOrgID: "org-a",
		BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"}
	texts := []struct{ id, role, text string }{
		{"procurement", "procurement", "采购依据 📄\n电路 LINE-001：最低保障带宽 100 Mbit/s。\n电路 LINE-001：SLA 可用率 99.9%" + provenanceSLABasis + "\n电路 LINE-001：合同总额 100000 元，含税。"},
		{"award", "award", "电路 LINE-001：审批依据，预付款上限 30%，预付款以合同总额为基础，含税。"},
		{"draft", "draft_contract", "拟签合同 🧾\n电路 LINE-001：最低保障带宽 100 Mbit/s。\n电路 LINE-001：SLA 可用率 99.9%" + provenanceSLABasis + "\n电路 LINE-001：预付款比例 20%，预付款以合同总额为基础，含税。\n电路 LINE-001：合同总额 100000 元，含税。"},
		{"annex", "attachment", "电路 LINE-001：SLA 可用率 99.9%" + provenanceSLABasis},
	}
	fixture := provenanceFixture{caseItem: item}
	for i, input := range texts {
		doc := Document{ID: input.id, TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: item.ID,
			Role: input.role, Filename: input.id + ".txt", MediaType: "text/plain", Digest: digestBytes([]byte(input.text))}
		ir := docir.Build(input.text)
		parserID, parserVersion, configHash := parserConfigurationFor(doc, nil)
		selection := AttemptParseSelection{DocumentID: doc.ID, OriginalDigest: doc.Digest,
			ParsedDocumentID: "parsed-" + doc.ID, SelectionOrder: int64(i + 1), ParserID: parserID,
			ParserVersion: parserVersion, ConfigHash: configHash, NormalizationVersion: docir.NormalizationVersion}
		parsed, err := makeParsedWithSelection(doc, Interpreted{Document: doc, Ready: true, Text: ir.NormalizedText, IR: ir}, selection)
		if err != nil {
			t.Fatal(err)
		}
		fixture.docs = append(fixture.docs, parsedInterpreted(doc, parsed))
	}
	sources, err := telecomSources(fixture.docs)
	if err != nil {
		t.Fatal(err)
	}
	fixture.request = rulepack.Request{
		Principal: access.Principal{UserID: "alice", TenantID: item.TenantID, Enabled: true,
			Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}},
		Resource:     access.Resource{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: item.ID, OwnerID: "alice"},
		BusinessLine: item.BusinessLine, Scenario: item.Scenario, ContractType: item.ContractType,
		At: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC), Sources: sources,
	}
	fixture.request.Facts, err = telecom.Extract(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	fixture.report, err = telecom.Execute(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	fixture.snapshot, err = telecom.Snapshot(fixture.request)
	if err != nil || len(fixture.snapshot) == 0 {
		t.Fatalf("snapshot: %v", err)
	}
	return fixture
}

func (f provenanceFixture) guard() (telecomReviewOutput, error) {
	return guardTelecomReport(f.caseItem, "run-provenance", "attempt-provenance", f.docs, f.request, f.report, f.snapshot)
}

func TestCompatibleEvaluationsRetainExactFactAndAuthorityCitations(t *testing.T) {
	fixture := newProvenanceFixture(t)
	output, err := fixture.guard()
	if err != nil {
		t.Fatal(err)
	}
	if len(output.evaluations) != 3 || len(output.findings) != 0 {
		t.Fatalf("compatible rules should produce three evaluations without findings: %+v", output)
	}
	for _, evaluation := range output.evaluations {
		if evaluation.Relation != "compatible" || evaluation.FindingID != "" {
			t.Fatalf("unexpected result: %+v", evaluation)
		}
		var reportResult rulepack.Result
		for _, result := range fixture.report.Results {
			if result.RuleID == evaluation.RuleID {
				reportResult = result
			}
		}
		seenFact, seenAuthority := false, false
		for _, citation := range reportResult.Citations {
			matched := false
			for _, evidence := range evaluation.Evidence {
				ref := evidence.Ref
				if ref.DocumentID != citation.SourceID || ref.ParsedDocumentID != citation.SourceVersion ||
					ref.NormalizedDigest != citation.SourceDigest || ref.Quote != citation.Quote ||
					ref.Start != citation.Start || ref.End != citation.End {
					continue
				}
				matched = true
				for _, doc := range fixture.docs {
					if doc.Document.ID == ref.DocumentID {
						quote, ok := docir.Slice(doc.IR.NormalizedText, ref.Start, ref.End)
						if !ok || quote != ref.Quote {
							t.Fatalf("not a Unicode slice: %+v", ref)
						}
						if doc.Document.Role == "draft_contract" {
							seenFact = evidence.FactID != "" && contains(evaluation.FactIDs, evidence.FactID)
							if ref.Start == len("拟签合同 🧾\n") {
								t.Fatal("used byte offsets instead of code points")
							}
						} else if doc.Document.Role == "procurement" || doc.Document.Role == "award" {
							seenAuthority = evidence.FactID == ""
						}
					}
				}
			}
			if !matched {
				t.Fatalf("result citation was lost: %+v", citation)
			}
		}
		if !seenFact || !seenAuthority {
			t.Fatalf("missing direct fact or authority provenance: %+v", evaluation)
		}
	}
}

func TestEvaluationGuardRejectsForgedAndUnverifiedSources(t *testing.T) {
	for name, change := range map[string]func(*provenanceFixture){
		"wrong quote":        func(f *provenanceFixture) { f.report.Results[0].Citations[0].Quote = "伪造" },
		"wrong unicode span": func(f *provenanceFixture) { f.report.Results[0].Citations[0].Start++ },
		"wrong digest":       func(f *provenanceFixture) { f.report.Results[0].Citations[0].SourceDigest = strings.Repeat("0", 64) },
		"wrong parse":        func(f *provenanceFixture) { f.report.Results[0].Citations[0].SourceVersion = "another-parse" },
		"wrong tenant":       func(f *provenanceFixture) { f.report.Results[0].Citations[0].TenantID = "other-tenant" },
		"foreign fact":       func(f *provenanceFixture) { f.report.Results[0].FactIDs = []string{"foreign-fact"} },
		"forged duplicate span": func(f *provenanceFixture) {
			citation := f.report.Results[0].Citations[0]
			citation.Quote = "not the cited quote"
			f.report.Results[0].Citations = append(f.report.Results[0].Citations, citation)
		},
		"unverified artifact": func(f *provenanceFixture) {
			parsed := *f.docs[0].ParsedDocument
			parsed.ArtifactDigest = strings.Repeat("0", 64)
			f.docs[0].ParsedDocument = &parsed
		},
		"substituted source": func(f *provenanceFixture) {
			f.request.Sources[0].Text += "伪造"
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newProvenanceFixture(t)
			change(&fixture)
			if _, err := fixture.guard(); err == nil {
				t.Fatal("forged evidence was accepted")
			}
		})
	}
}

func TestMissingAuthorityStaysUnresolvedWithoutFabricatedEvidence(t *testing.T) {
	fixture := newProvenanceFixture(t)
	fixture.docs = fixture.docs[2:3]
	var err error
	fixture.request.Sources, err = telecomSources(fixture.docs)
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.Facts, err = telecom.Extract(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	fixture.report, err = telecom.Execute(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	fixture.snapshot, err = telecom.Snapshot(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	output, err := fixture.guard()
	if err != nil {
		t.Fatal(err)
	}
	for _, evaluation := range output.evaluations {
		if evaluation.Relation != "insufficient" || len(evaluation.Evidence) != 0 {
			t.Fatalf("missing authority became proven: %+v", evaluation)
		}
	}
	for _, finding := range output.findings {
		if finding.Relation != "insufficient" || len(finding.Anchors) != 0 {
			t.Fatalf("invented a located finding: %+v", finding)
		}
	}
}

func TestConflictEvidenceRetainsExistingFindingAnchors(t *testing.T) {
	fixture := newProvenanceFixture(t)
	old := fixture.docs[2]
	text := strings.Replace(old.Text, "最低保障带宽 100 Mbit/s", "最低保障带宽 50 Mbit/s", 1)
	doc := old.Document
	doc.Digest = digestBytes([]byte(text))
	ir := docir.Build(text)
	p := old.ParsedDocument
	selection := AttemptParseSelection{DocumentID: doc.ID, OriginalDigest: doc.Digest, ParsedDocumentID: p.ID,
		SelectionOrder: p.SelectionOrder, ParserID: p.ParserID, ParserVersion: p.ParserVersion,
		ConfigHash: p.ConfigHash, NormalizationVersion: p.NormalizationVersion}
	parsed, err := makeParsedWithSelection(doc, Interpreted{Document: doc, Ready: true, Text: ir.NormalizedText, IR: ir}, selection)
	if err != nil {
		t.Fatal(err)
	}
	fixture.docs[2] = parsedInterpreted(doc, parsed)
	fixture.request.Sources, err = telecomSources(fixture.docs)
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.Facts, err = telecom.Extract(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	fixture.report, err = telecom.Execute(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	fixture.snapshot, err = telecom.Snapshot(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	output, err := fixture.guard()
	if err != nil {
		t.Fatal(err)
	}
	for _, evaluation := range output.evaluations {
		if evaluation.RuleID != telecomBandwidthRule {
			continue
		}
		if evaluation.Relation != "conflict" || evaluation.FindingID == "" || len(evaluation.Evidence) < 2 {
			t.Fatalf("conflict evidence was not retained: %+v", evaluation)
		}
		for _, finding := range output.findings {
			if finding.ID != evaluation.FindingID {
				continue
			}
			if finding.Relation != "conflict" || len(finding.Anchors) < 2 {
				t.Fatalf("finding lost its original anchors: %+v", finding)
			}
			for _, anchor := range finding.Anchors {
				if anchor.Status != "located" {
					t.Fatalf("unlocated conflict anchor: %+v", anchor)
				}
				found := false
				for _, evidence := range evaluation.Evidence {
					ref := evidence.Ref
					if ref.DocumentID == anchor.DocumentID && ref.ParsedDocumentID == anchor.ParsedDocumentID &&
						ref.NormalizedDigest == anchor.NormalizedDigest && ref.Quote == anchor.Quote && ref.Start == anchor.Start && ref.End == anchor.End {
						found = true
					}
				}
				if !found {
					t.Fatalf("finding anchor not retained as result evidence: %+v", anchor)
				}
			}
			return
		}
		t.Fatal("conflict finding missing")
	}
	t.Fatal("bandwidth evaluation missing")
}

func provenanceState(t *testing.T, fixture provenanceFixture, output telecomReviewOutput) (state, Attempt) {
	t.Helper()
	inputHash := "frozen-input"
	attempt := Attempt{ID: "attempt-provenance", RunID: "run-provenance", HistoryKind: "frozen", InputHash: &inputHash}
	input := AttemptInput{AttemptID: attempt.ID, RunID: attempt.RunID, InputHash: inputHash}
	var st state
	for _, doc := range fixture.docs {
		parsed := *doc.ParsedDocument
		input.Documents = append(input.Documents, frozenFromParsed(doc.Document, parsed))
		st.Documents = append(st.Documents, doc.Document)
		st.ParsedDocuments = append(st.ParsedDocuments, parsed)
		st.AttemptParseSelections = append(st.AttemptParseSelections, AttemptParseSelection{
			AttemptID: attempt.ID, RunID: attempt.RunID, DocumentID: doc.Document.ID, OriginalDigest: doc.Document.Digest,
			ParsedDocumentID: parsed.ID, SelectionOrder: parsed.SelectionOrder, ParserID: parsed.ParserID,
			ParserVersion: parsed.ParserVersion, ConfigHash: parsed.ConfigHash, NormalizationVersion: parsed.NormalizationVersion,
			ArtifactDigest: parsed.ArtifactDigest,
		})
	}
	snapshot := RunSnapshot{RunID: attempt.RunID}
	for _, doc := range input.Documents {
		snapshot.ParsedDocumentIDs = append(snapshot.ParsedDocumentIDs, doc.ParsedDocumentID)
	}
	snapshot.ParseSelectionHash = selectionHash(input.Documents)
	snapshot.ParserConfigurationID = frozenConfigHash(input.Documents)
	st.Attempts, st.AttemptInputs, st.Snapshots = []Attempt{attempt}, []AttemptInput{input}, []RunSnapshot{snapshot}
	st.Evaluations, st.Findings = output.evaluations, output.findings
	return st, attempt
}

func TestEvaluationEvidenceBindsAttemptAndOutputHash(t *testing.T) {
	fixture := newProvenanceFixture(t)
	output, err := fixture.guard()
	if err != nil {
		t.Fatal(err)
	}
	st, attempt := provenanceState(t, fixture, output)
	if err := validateTelecomEvidenceInput(st, attempt, fixture.caseItem, output, fixture.docs); err != nil {
		t.Fatal(err)
	}
	original, err := attemptOutputHash(st, attempt.RunID, attempt.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := st.clone()
	if err != nil {
		t.Fatal(err)
	}
	changed.Evaluations[0].Evidence[0].Ref.Quote += "篡改"
	digest, err := attemptOutputHash(changed, attempt.RunID, attempt.ID, "")
	if err != nil || digest == original {
		t.Fatal("evaluation evidence was not committed in the output hash")
	}
	badOutput := output
	badOutput.evaluations = changed.Evaluations
	if err := validateTelecomEvidenceInput(st, attempt, fixture.caseItem, badOutput, fixture.docs); err == nil {
		t.Fatal("a forged span was accepted for the selected parse")
	}
	badAttempt := attempt
	badAttempt.InputHash = stringPointer("another-input")
	if err := validateTelecomEvidenceInput(st, badAttempt, fixture.caseItem, output, fixture.docs); err == nil {
		t.Fatal("citations were attached to another frozen attempt input")
	}
	changed, err = st.clone()
	if err != nil {
		t.Fatal(err)
	}
	changed.AttemptParseSelections[0].ParsedDocumentID = "different-parse"
	if err := validateTelecomEvidenceInput(changed, attempt, fixture.caseItem, output, fixture.docs); err == nil {
		t.Fatal("citations survived a different parse selection")
	}
}

func TestLegacyEvaluationJSONAndOutputHashStayUnchanged(t *testing.T) {
	legacy := ConstraintEval{ID: "ev-old", RunID: "run-old", AttemptID: "attempt-old", CaseID: "case-old",
		Domain: "technical", RuleVersion: "1", Relation: "compatible", Reason: "historical"}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":"ev-old","run_id":"run-old","attempt_id":"attempt-old","case_id":"case-old","domain":"technical","rule_version":"1","relation":"compatible","reason":"historical"}`
	if string(raw) != want {
		t.Fatalf("legacy evaluation changed: %s", raw)
	}
	st := state{Attempts: []Attempt{{ID: legacy.AttemptID, RunID: legacy.RunID}}, Evaluations: []ConstraintEval{legacy}}
	got, err := attemptOutputHash(st, legacy.RunID, legacy.AttemptID, "")
	if err != nil {
		t.Fatal(err)
	}
	oldOutput := fmt.Sprintf(`{"findings":null,"evaluations":[%s]}`, want)
	if got != digestBytes([]byte(oldOutput)) {
		t.Fatal("legacy output hash changed")
	}
}

func TestDurableEvaluationEvidenceSurvivesReloadAndScopedHash(t *testing.T) {
	st, admin := openExecutionStore(t)
	st.UseModels(&frozenOCRModel{})
	pack := publishSyntheticAcceptancePack(t, st, admin)
	publishSyntheticAcceptancePolicy(t, st, admin, pack)
	caseID, err := st.CreateCaseWithBusiness(admin, "冻结引用持久化", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ role, text string }{
		{"procurement", "采购依据 📄\n电路 LINE-001：最低保障带宽 100 Mbit/s。\n电路 LINE-001：SLA 可用率 99.9%" + provenanceSLABasis + "\n电路 LINE-001：合同总额 100000 元，含税。"},
		{"award", "电路 LINE-001：审批依据，预付款上限 30%，预付款以合同总额为基础，含税。"},
		{"draft_contract", "拟签合同 🧾\n电路 LINE-001：最低保障带宽 100 Mbit/s。\n电路 LINE-001：SLA 可用率 99.9%" + provenanceSLABasis + "\n电路 LINE-001：预付款比例 20%，预付款以合同总额为基础，含税。\n电路 LINE-001：合同总额 100000 元，含税。"},
		{"attachment", "电路 LINE-001：SLA 可用率 99.9%" + provenanceSLABasis},
	} {
		if _, err := st.AddDocument(context.Background(), admin, caseID, item.role, item.role+".txt", "text/plain", []byte(item.text)); err != nil {
			t.Fatal(err)
		}
	}
	accepted, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	runAcceptedKnowledgeJob(t, st, accepted)
	workspace := waitTerminalWorkspace(t, st, admin, caseID, accepted.RunID)
	if workspace.Execution == nil || workspace.Execution.Attempt == nil || workspace.Execution.Attempt.OutputHash == nil {
		t.Fatal("terminal output hash missing")
	}
	var compatible ConstraintEval
	for _, evaluation := range workspace.Evaluations {
		if evaluation.RuleID == telecomBandwidthRule {
			compatible = evaluation
		}
	}
	if compatible.Relation != "compatible" || len(compatible.Evidence) < 2 {
		t.Fatalf("compatible citations not stored: %+v", compatible)
	}
	ctx := context.Background()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scope := userScope(admin)
	scope.CaseID = caseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		t.Fatal(err)
	}
	narrow, err := loadAttemptOutputScope(ctx, tx, accepted.RunID, accepted.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(narrow.Evaluations, workspace.Evaluations) {
		t.Fatalf("scoped loader lost citations: %+v", narrow.Evaluations)
	}
	reviewDigest, err := agentReviewDigestForOutput(ctx, tx, scope.TenantID, scope.OrgID, accepted.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	calculated, err := attemptOutputHash(narrow, accepted.RunID, accepted.AttemptID, reviewDigest)
	if err != nil || calculated != *workspace.Execution.Attempt.OutputHash {
		t.Fatalf("scoped output hash changed: %s %v", calculated, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restored := readWorkspace(t, reopened, admin, caseID, accepted.RunID)
	if !reflect.DeepEqual(restored.Evaluations, workspace.Evaluations) {
		t.Fatalf("reopened evaluations changed: %+v", restored.Evaluations)
	}
}
