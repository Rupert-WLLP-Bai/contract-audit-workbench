package store

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"contractaudit/internal/accounts"
	"contractaudit/internal/docir"
	"contractaudit/internal/model"
)

func TestCloudModelDraftsBindExactPolicyAndContractEvidence(t *testing.T) {
	st, admin := openExecutionStore(t)
	_, published := seedGovernedCloudPolicy(t, st, admin)
	quote := "服务中断的赔偿按照损失计算。"
	text := "第九条 服务中断时，" + quote
	ir := docir.Build(text)
	document := Interpreted{
		Document: Document{ID: "contract-1", Role: "draft_contract", Digest: "source-digest"},
		Ready:    true, Text: text, IR: ir,
		ParsedDocument: &ParsedDocument{
			ID: "parsed-contract-1", DocumentID: "contract-1", OriginalDigest: "source-digest",
			NormalizedDigest: ir.NormalizedDigest, ArtifactDigest: "artifact-digest", Status: "ready",
		},
	}
	policy := model.CloudPolicy{ID: published.Content.KnowledgeID, Version: published.Content.Version, Body: published.Content.Body}
	review := model.CloudReview{Findings: []model.CloudFinding{{
		Domain: "liability", PolicyClause: "应明确服务中断赔偿责任", Claim: "赔偿责任缺少累计上限。", DocumentID: document.Document.ID, Quote: quote,
	}}}
	drafts, err := cloudModelDrafts(review, []Interpreted{document}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].Origin != "model" || drafts[0].Relation != "insufficient" ||
		drafts[0].Disposition != "insufficient_evidence" || !strings.Contains(drafts[0].UnverifiedReason, policy.ID) ||
		!strings.Contains(drafts[0].UnverifiedReason, review.Findings[0].PolicyClause) || drafts[0].Claim != review.Findings[0].Claim ||
		len(drafts[0].Anchors) != 1 || drafts[0].Anchors[0].Quote != quote {
		t.Fatalf("unexpected unverified semantic draft: %+v", drafts)
	}

	invalid := review
	invalid.Findings = append([]model.CloudFinding(nil), review.Findings...)
	invalid.Findings[0].PolicyClause = "制度中不存在的条款"
	if _, err := cloudModelDrafts(invalid, []Interpreted{document}, policy); err == nil {
		t.Fatal("mismatched policy clause was accepted")
	}
	invalid = review
	invalid.Findings = append([]model.CloudFinding(nil), review.Findings...)
	invalid.Findings[0].Quote = "不存在于合同的引用"
	if _, err := cloudModelDrafts(invalid, []Interpreted{document}, policy); err == nil {
		t.Fatal("unmatched contract quote was accepted")
	}
	otherDocument := document
	otherDocument.Document.Role = "procurement"
	if _, err := cloudModelDrafts(review, []Interpreted{otherDocument}, policy); err == nil {
		t.Fatal("procurement evidence was accepted as a contract citation")
	}
}

func TestLegacyFindingSeveritySerializesAsNull(t *testing.T) {
	body, err := json.Marshal(Finding{ID: "legacy-finding"})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"severity", "severity_source"} {
		if value, exists := payload[field]; !exists || value != nil {
			t.Fatalf("legacy %s = %#v, want explicit null: %s", field, value, body)
		}
	}
}

// seedGovernedCloudPolicy exercises both publication lifecycles and binds each
// cloud comparator to an exact span in the approved policy body.
func seedGovernedCloudPolicy(t *testing.T, st *Store, admin accounts.User) (RulePackDetail, GovernedKnowledgeVersion) {
	t.Helper()
	pack, policies := seedGovernedCloudPolicies(t, st, admin, false)
	return pack, policies[0]
}

func seedGovernedCloudPolicies(t *testing.T, st *Store, admin accounts.User, split bool) (RulePackDetail, []GovernedKnowledgeVersion) {
	t.Helper()
	const policyID, secondID, version = "cloud-operator-standard", "cloud-payment-standard", "2026.2"
	var manifest map[string]any
	if err := json.Unmarshal(governedPackManifest(t, admin.TenantID, "1", "200"), &manifest); err != nil {
		t.Fatal(err)
	}
	definitions := []struct{ id, capability, threshold, quote, policy string }{
		{cloudBandwidthRule, "cloud_services.minimum_bandwidth", "200", "带宽下限为200 Mbit/s", policyID},
		{cloudSLARule, "cloud_services.monthly_availability", "99.8", "月可用率不低于99.8%", policyID},
		{cloudAdvanceRule, "cloud_services.advance_payment_ceiling", "1/4", "预付款不超过25%", policyID},
	}
	if split {
		definitions[2].policy = secondID
	}
	for i, raw := range manifest["rules"].([]any) {
		rule := raw.(map[string]any)
		rule["rule_id"], rule["capability_id"] = definitions[i].id, definitions[i].capability
		rule["business_scope"].(map[string]any)["scenario"] = "cloud_services"
		rule["threshold"].(map[string]any)["value"] = definitions[i].threshold
		rule["authority_sources"] = []any{map[string]any{"id": definitions[i].policy, "version": version, "kind": "internal_policy"}}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	pack := publishGovernedPackStore(t, st, admin, "1", "200", raw)
	ids := []string{policyID}
	if split {
		ids = append(ids, secondID)
	}
	var policies []GovernedKnowledgeVersion
	for _, id := range ids {
		content := knowledgeFixtureContent(pack, id, version, "policy")
		content.Scope.Scenario = "cloud_services"
		content.Body = ""
		content.Binding.RuleIDs = nil
		content.Binding.SourceSpans = nil
		for _, definition := range definitions {
			if definition.policy != id {
				continue
			}
			start := utf8.RuneCountInString(content.Body)
			content.Body += definition.quote
			switch definition.id {
			case cloudSLARule:
				content.Body += "，统计周期为自然月，时区Asia/Shanghai，公历gregorian，故障口径all_service_unavailability，无排除项"
			case cloudAdvanceRule:
				content.Body += "，预付款以合同总额为基础"
			}
			content.Body += "。"
			content.Binding.RuleIDs = append(content.Binding.RuleIDs, definition.id)
			content.Binding.SourceSpans = append(content.Binding.SourceSpans, KnowledgeSourceSpan{
				RuleID: definition.id, Start: start, End: start + utf8.RuneCountInString(definition.quote), Quote: definition.quote,
			})
		}
		content.Body += "应明确服务中断赔偿责任。"
		policy := publishKnowledge(t, st, admin, createKnowledgeDraft(t, st, admin, content, id+"-create"), id)
		policies = append(policies, policy)
	}
	return pack, policies
}

type governedCloudModel struct {
	frozenOCRModel
	entered  chan struct{}
	release  chan struct{}
	policies chan model.CloudPolicy
	once     sync.Once
}

func newGovernedCloudModel() *governedCloudModel {
	return &governedCloudModel{entered: make(chan struct{}, 4), release: make(chan struct{}), policies: make(chan model.CloudPolicy, 4)}
}

func (m *governedCloudModel) unblock() { m.once.Do(func() { close(m.release) }) }

func (m *governedCloudModel) CloudContract(ctx context.Context, policy model.CloudPolicy, docs []model.Material) (model.CloudReview, error) {
	m.policies <- policy
	m.entered <- struct{}{}
	select {
	case <-m.release:
	case <-ctx.Done():
		return model.CloudReview{}, ctx.Err()
	}
	return model.CloudReview{Findings: []model.CloudFinding{{
		Domain: "liability", PolicyClause: "应明确服务中断赔偿责任", Claim: "赔偿责任缺少累计上限。",
		DocumentID: docs[0].ID, Quote: "服务中断的赔偿按照损失计算。",
	}}}, nil
}

func startGovernedCloudReview(t *testing.T, st *Store, user accounts.User) (string, string) {
	t.Helper()
	caseID, err := st.CreateCaseWithBusiness(user, "云治理审查", "service", "ENT", "cloud_services")
	if err != nil {
		t.Fatal(err)
	}
	for role, text := range map[string]string{
		"procurement":    "云服务 CLOUD-001：最低保障带宽不低于200 Mbit/s。\n云服务 CLOUD-001：SLA 可用率不低于99.8%，自然月 Asia/Shanghai 公历 gregorian all_service_unavailability 无排除项。",
		"award":          "云服务 CLOUD-001：审批依据，预付款上限不得超过25%，预付款以合同总额为基础，合同总额100万元含税。",
		"draft_contract": "云服务 CLOUD-001：最低保障带宽200 Mbit/s。\n云服务 CLOUD-001：SLA 可用率99.8%，自然月 Asia/Shanghai 公历 gregorian all_service_unavailability 无排除项。\n云服务 CLOUD-001：预付款比例25%，预付款以合同总额为基础，合同总额100万元含税。\n服务中断的赔偿按照损失计算。",
		"attachment":     "云服务 CLOUD-001：SLA 可用率99.8%，自然月 Asia/Shanghai 公历 gregorian all_service_unavailability 无排除项。",
	} {
		if _, err := st.AddDocument(context.Background(), user, caseID, role, role+".txt", "text/plain", []byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	runID, err := st.StartReviewAt(context.Background(), user, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatalf("accept governed cloud review: %v", err)
	}
	return caseID, runID
}

func TestCloudReviewUsesPublishedGovernedPolicy(t *testing.T) {
	st, admin := openExecutionStore(t)
	_, published := seedGovernedCloudPolicy(t, st, admin)
	reviewer := newGovernedCloudModel()
	t.Cleanup(reviewer.unblock)
	st.UseModels(reviewer)
	caseID, runID := startGovernedCloudReview(t, st, admin)
	waitSignal(t, reviewer.entered, "cloud model policy")
	gotPolicy := <-reviewer.policies
	if gotPolicy.ID != published.Content.KnowledgeID || gotPolicy.Version != published.Content.Version || gotPolicy.Body != published.Content.Body {
		t.Fatalf("model received different policy: %+v", gotPolicy)
	}
	reviewer.unblock()
	workspace := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if workspace.Snapshot.GovernedKnowledge == nil || len(workspace.Snapshot.GovernedKnowledge.RequiredPolicies) != 1 ||
		workspace.Snapshot.GovernedKnowledge.RequiredPolicies[0].Ref != published.Ref || len(workspace.Snapshot.CloudPolicySnapshot) != 0 {
		t.Fatalf("cloud run did not freeze the governed policy: %+v", workspace.Snapshot)
	}
	if len(workspace.Evaluations) != 3 {
		t.Fatalf("mandatory deterministic results missing: %+v", workspace.Evaluations)
	}
	for _, evaluation := range workspace.Evaluations {
		if evaluation.Relation != "compatible" {
			t.Fatalf("published cloud threshold did not govern the comparison: %+v", evaluation)
		}
	}
	if workspace.Execution == nil || workspace.Execution.FirstVerifiedFindingMS != nil {
		t.Fatalf("a model suggestion was counted as a verified finding: %+v", workspace.Execution)
	}
	found := false
	for _, finding := range workspace.Findings {
		if finding.Severity != nil || finding.SeveritySource != nil {
			t.Fatalf("invented severity or source: %+v", finding)
		}
		if finding.Origin == "model" {
			found = true
			if finding.Relation != "insufficient" || finding.DispositionSuggestion != "insufficient_evidence" ||
				!strings.Contains(finding.UnverifiedReason, published.Content.KnowledgeID) {
				t.Fatalf("model suggestion was treated as verified: %+v", finding)
			}
		}
	}
	if !found {
		t.Fatalf("cloud suggestion missing: %+v", workspace.Findings)
	}
}

func TestCloudReviewRejectsWithdrawnPolicyBeforeModelAdoption(t *testing.T) {
	st, admin := openExecutionStore(t)
	_, published := seedGovernedCloudPolicy(t, st, admin)
	reviewer := newGovernedCloudModel()
	t.Cleanup(reviewer.unblock)
	st.UseModels(reviewer)
	caseID, runID := startGovernedCloudReview(t, st, admin)
	waitSignal(t, reviewer.entered, "in-flight cloud model")
	advanceKnowledge(t, st, admin, published, "withdraw", "cloud-policy-withdraw")
	reviewer.unblock()
	workspace := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if hasOrigin(workspace.Findings, "model") {
		t.Fatalf("withdrawn policy allowed an in-flight model suggestion: %+v", workspace.Findings)
	}
}

func TestCloudPolicyBindingRejectsMismatchedSources(t *testing.T) {
	st, admin := openExecutionStore(t)
	seedGovernedCloudPolicy(t, st, admin)
	reviewer := newGovernedCloudModel()
	t.Cleanup(reviewer.unblock)
	st.UseModels(reviewer)
	caseID, runID := startGovernedCloudReview(t, st, admin)
	waitSignal(t, reviewer.entered, "cloud policy binding")
	workspace := readWorkspace(t, st, admin, caseID, runID)
	frozen := workspace.Snapshot
	if frozen.GovernedKnowledge == nil {
		t.Fatal("governed policy snapshot missing")
	}
	if _, err := validateCloudPolicyBindings(frozen, *frozen.GovernedKnowledge, admin.TenantID); err != nil {
		t.Fatalf("published policy binding was rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ReviewKnowledgeSnapshot)
	}{
		{"different identity", func(s *ReviewKnowledgeSnapshot) { s.RequiredPolicies[0].Ref.KnowledgeID = "different-policy" }},
		{"different version", func(s *ReviewKnowledgeSnapshot) { s.RequiredPolicies[0].Ref.VersionID = strings.Repeat("0", 64) }},
		{"different source kind", func(s *ReviewKnowledgeSnapshot) { s.RequiredPolicies[0].SourceType = "synthetic_policy" }},
		{"missing rule binding", func(s *ReviewKnowledgeSnapshot) { s.RequiredPolicies[0].Binding.RuleIDs = []string{cloudBandwidthRule} }},
		{"missing policy spans", func(s *ReviewKnowledgeSnapshot) { s.RequiredPolicies[0].Binding.SourceSpans = nil }},
		{"different pack", func(s *ReviewKnowledgeSnapshot) {
			s.RequiredPolicies[0].Binding.PackContentHash = strings.Repeat("0", 64)
		}},
		{"missing policy", func(s *ReviewKnowledgeSnapshot) { s.RequiredPolicies = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(frozen.GovernedKnowledge)
			if err != nil {
				t.Fatal(err)
			}
			var changed ReviewKnowledgeSnapshot
			if err := json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&changed)
			changed, err = sealKnowledgeSnapshot(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := validateCloudPolicyBindings(frozen, changed, admin.TenantID); err == nil {
				t.Fatal("mismatched policy source was accepted")
			}
		})
	}
	reviewer.unblock()
	waitTerminalWorkspace(t, st, admin, caseID, runID)
}

func TestCloudReviewRejectsRevokedCaseAccessBeforeModelAdoption(t *testing.T) {
	st, admin := openExecutionStore(t)
	seedGovernedCloudPolicy(t, st, admin)
	reviewer := newGovernedCloudModel()
	t.Cleanup(reviewer.unblock)
	st.UseModels(reviewer)
	caseID, runID := startGovernedCloudReview(t, st, admin)
	waitSignal(t, reviewer.entered, "in-flight cloud model")
	if _, err := st.pool.Exec(context.Background(), `UPDATE cases SET member_ids='[]' WHERE tenant_id=$1 AND id=$2`, admin.TenantID, caseID); err != nil {
		t.Fatal(err)
	}
	reviewer.unblock()
	st.executionWG.Wait()
	var count int
	if err := st.pool.QueryRow(context.Background(), `SELECT count(*) FROM findings WHERE run_id=$1 AND origin='model'`, runID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("revoked case access allowed %d model suggestions", count)
	}
}

func TestCloudReviewCannotUseLegacyPolicyWithoutGovernance(t *testing.T) {
	st, admin := openExecutionStore(t)
	_, published := seedGovernedCloudPolicy(t, st, admin)
	advanceKnowledge(t, st, admin, published, "withdraw", "cloud-policy-withdraw-before-acceptance")
	if _, err := st.CreateKnowledge(admin, KnowledgeInput{
		ID: published.Content.KnowledgeID, Version: published.Content.Version, Title: "未治理制度",
		Body: published.Content.Body, SourceType: "reference",
	}); err != nil {
		t.Fatal(err)
	}
	caseID, err := st.CreateCaseWithBusiness(admin, "无已批准制度的云审查", "service", "ENT", "cloud_services")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"procurement", "draft_contract"} {
		if _, err := st.AddDocument(context.Background(), admin, caseID, role, role+".txt", "text/plain", []byte("云服务 CLOUD-001：最低保障带宽200 Mbit/s。")); err != nil {
			t.Fatal(err)
		}
	}
	_, err = st.StartReviewAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	requireKnowledgeFault(t, err, 409)
	if !strings.Contains(err.Error(), "强制规则缺少适用的已发布政策绑定") {
		t.Fatalf("legacy policy failed for an unrelated reason: %v", err)
	}
}

func TestCloudReviewUsesEachRuleBoundPolicy(t *testing.T) {
	st, admin := openExecutionStore(t)
	_, published := seedGovernedCloudPolicies(t, st, admin, true)
	reviewer := newGovernedCloudModel()
	t.Cleanup(reviewer.unblock)
	st.UseModels(reviewer)
	caseID, runID := startGovernedCloudReview(t, st, admin)
	waitSignal(t, reviewer.entered, "first cloud policy")
	reviewer.unblock()
	workspace := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if len(reviewer.policies) != len(published) {
		t.Fatalf("model reviewed %d policy sources, want %d; attempt=%+v", len(reviewer.policies), len(published), workspace.Execution)
	}
	want := make(map[string]model.CloudPolicy, len(published))
	for _, policy := range published {
		want[policy.Content.KnowledgeID] = model.CloudPolicy{ID: policy.Content.KnowledgeID, Version: policy.Content.Version, Body: policy.Content.Body}
	}
	for range published {
		got := <-reviewer.policies
		if expected, ok := want[got.ID]; !ok || got != expected {
			t.Fatalf("model received an unbound policy: %+v", got)
		}
		delete(want, got.ID)
	}
	if len(want) != 0 || workspace.Snapshot.GovernedKnowledge == nil || len(workspace.Snapshot.GovernedKnowledge.RequiredPolicies) != len(published) {
		t.Fatalf("published policy sources were omitted: missing=%v snapshot=%+v", want, workspace.Snapshot)
	}
	seen := make(map[string]bool, len(published))
	for _, finding := range workspace.Findings {
		if finding.Origin != "model" {
			continue
		}
		for _, policy := range published {
			if strings.Contains(finding.UnverifiedReason, policy.Content.KnowledgeID) {
				seen[policy.Content.KnowledgeID] = true
			}
		}
	}
	if len(seen) != len(published) {
		t.Fatalf("model suggestions lost their separate policy references: %+v", workspace.Findings)
	}
}
