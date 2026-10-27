package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func requireKnowledgeFault(t *testing.T, err error, status int) {
	t.Helper()
	var problem *fault.Error
	if !errors.As(err, &problem) || problem.Status != status {
		t.Fatalf("expected status %d, got %v", status, err)
	}
}

func knowledgeFixtureContent(pack RulePackDetail, id, version, kind string) GovernedKnowledgeContent {
	var manifest struct {
		Rules []struct {
			Comparator string `json:"comparator"`
			Threshold  struct {
				Value string `json:"value"`
				Unit  string `json:"unit"`
			} `json:"threshold"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(pack.Manifest, &manifest); err != nil {
		panic(err)
	}
	values, units := map[string]string{}, map[string]string{}
	for _, rule := range manifest.Rules {
		values[rule.Comparator], units[rule.Comparator] = rule.Threshold.Value, rule.Threshold.Unit
	}
	advance := values["advance_payment_ceiling"]
	if !strings.Contains(advance, "/") {
		advance += "/1"
	}
	quotes := []string{
		"带宽不低于" + values["minimum_bandwidth"] + " " + units["minimum_bandwidth"],
		"月可用率不低于" + values["monthly_sla"] + "%",
		"预付款比例不超过" + advance,
	}
	body := quotes[0] + "。" + quotes[1] + "，统计周期为自然月，时区Asia/Shanghai，公历gregorian，故障口径all_service_unavailability，无排除项。" +
		quotes[2] + "，预付款以合同总额为基础。"
	rules := []string{"ENT-TECH-BW-001", "ENT-TECH-SLA-001", "ENT-COM-ADV-001"}
	spans := make([]KnowledgeSourceSpan, len(rules))
	for i, id := range rules {
		index := strings.Index(body, quotes[i])
		start := utf8.RuneCountInString(body[:index])
		spans[i] = KnowledgeSourceSpan{RuleID: id, Start: start, End: start + utf8.RuneCountInString(quotes[i]), Quote: quotes[i]}
	}
	return GovernedKnowledgeContent{
		KnowledgeID: id, Version: version, Kind: kind, Title: "核验制度", Body: body,
		Summary: "核对付款及验收", SourceType: "reference", SourceReference: "制度来源记录",
		Scope:     KnowledgeScope{BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"},
		ValidFrom: "2020-01-01T00:00:00Z", ValidUntil: "2100-01-01T00:00:00Z",
		Binding: PolicyRuleBinding{
			PackID: pack.Pack.ID, PackVersion: pack.Pack.Version, PackContentHash: pack.Pack.ContentHash,
			PackSnapshotDigest: pack.Pack.SnapshotSHA256, CompilerRef: governedCompilerMarker,
			RuleIDs: rules, SourceSpans: spans, CandidateDiff: "与现有规则的候选差异",
			ValidationEvidence: "已通过规则包的校验及回归记录",
		},
	}
}

func createKnowledgeDraft(t *testing.T, st *Store, admin accounts.User, content GovernedKnowledgeContent, key string) GovernedKnowledgeVersion {
	t.Helper()
	v, err := st.CreateGovernedKnowledge(admin, CreateGovernedKnowledgeInput{Content: content, IdempotencyKey: key})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	return v
}

func knowledgeCommand(v GovernedKnowledgeVersion, action, key string) GovernedKnowledgeCommandInput {
	return GovernedKnowledgeCommandInput{
		KnowledgeID: v.Ref.KnowledgeID, VersionID: v.Ref.VersionID, Action: action,
		ExpectedRevision: v.Revision, ExpectedContentSHA256: v.Ref.ContentSHA256,
		ExpectedPointerRevision: v.PointerRevision, Reason: "审阅并确认适用范围和依据", IdempotencyKey: key,
	}
}

func advanceKnowledge(t *testing.T, st *Store, admin accounts.User, v GovernedKnowledgeVersion, action, key string) GovernedKnowledgeVersion {
	t.Helper()
	input := knowledgeCommand(v, action, key)
	input.CrossCaseReviewed = true
	out, err := st.GovernedKnowledgeCommand(admin, input)
	if err != nil {
		t.Fatalf("%s governance: %v", action, err)
	}
	return out
}

func publishKnowledge(t *testing.T, st *Store, admin accounts.User, v GovernedKnowledgeVersion, prefix string) GovernedKnowledgeVersion {
	t.Helper()
	for _, action := range []string{"submit", "validate", "approve", "publish"} {
		v = advanceKnowledge(t, st, admin, v, action, prefix+"-"+action)
	}
	return v
}

func governanceTestCase(t *testing.T, st *Store, user accounts.User) string {
	t.Helper()
	id, err := st.CreateCaseWithBusiness(user, "制度治理测试", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func governancePolicy(t *testing.T, st *Store, user accounts.User, pack RulePackDetail) GovernedKnowledgeVersion {
	t.Helper()
	content := knowledgeFixtureContent(pack, "operator-policy", "1", "policy")
	v := createKnowledgeDraft(t, st, user, content, "create-policy")
	return publishKnowledge(t, st, user, v, "policy")
}

func TestGovernedKnowledgeLifecycleDigestHistoryAndLegacyIsolation(t *testing.T) {
	st, admin := openExecutionStore(t)
	pack := publishGovernedPackStore(t, st, admin, "1", "100")
	caseID := governanceTestCase(t, st, admin)
	ctx := context.Background()
	content := knowledgeFixtureContent(pack, "operator-policy", "1", "policy")
	input := CreateGovernedKnowledgeInput{Content: content, IdempotencyKey: "draft-policy"}
	first, err := st.CreateGovernedKnowledge(admin, input)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := st.CreateGovernedKnowledge(admin, input)
	if err != nil || !reflect.DeepEqual(replayed, first) {
		t.Fatalf("draft replay: %+v %v", replayed, err)
	}
	changed := input
	changed.Content.Title = "另一标题"
	_, err = st.CreateGovernedKnowledge(admin, changed)
	requireKnowledgeFault(t, err, 409)
	if first.Ref.VersionID != knowledgeVersionID(admin.TenantID, content.KnowledgeID, content.Version) || first.Ref.ContentSHA256 == first.Ref.VersionID {
		t.Fatalf("version and content identities must be distinct: %+v", first.Ref)
	}
	_, err = st.ResolveGovernedKnowledge(ctx, admin, caseID, "2025-01-01T00:00:00Z", 10)
	requireKnowledgeFault(t, err, 409)
	first = advanceKnowledge(t, st, admin, first, "submit", "submit-policy")
	first = advanceKnowledge(t, st, admin, first, "validate", "validate-policy")
	badApproval := knowledgeCommand(first, "approve", "bad-approval")
	badApproval.ExpectedContentSHA256 = strings.Repeat("0", 64)
	_, err = st.GovernedKnowledgeCommand(admin, badApproval)
	requireKnowledgeFault(t, err, 409)
	first = advanceKnowledge(t, st, admin, first, "approve", "approve-policy")
	if first.ApprovedDigest != first.Ref.ContentSHA256 || first.ApprovalActorID != admin.ID {
		t.Fatalf("approval not bound: %+v", first)
	}
	_, err = st.ResolveGovernedKnowledge(ctx, admin, caseID, "2025-01-01T00:00:00Z", 10)
	requireKnowledgeFault(t, err, 409)
	publish := knowledgeCommand(first, "publish", "publish-policy")
	first, err = st.GovernedKnowledgeCommand(admin, publish)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err = st.GovernedKnowledgeCommand(admin, publish)
	if err != nil || !reflect.DeepEqual(replayed, first) {
		t.Fatalf("publish replay: %+v %v", replayed, err)
	}
	changedPublish := publish
	changedPublish.Reason = "不同的依据"
	_, err = st.GovernedKnowledgeCommand(admin, changedPublish)
	requireKnowledgeFault(t, err, 409)
	exact, err := st.GetGovernedKnowledgeVersion(admin, first.Ref.KnowledgeID, first.Ref.VersionID)
	if err != nil || !reflect.DeepEqual(exact, first) {
		t.Fatalf("exact version: %+v %v", exact, err)
	}
	wrongKind := knowledgeFixtureContent(pack, "operator-policy", "2", "guidance")
	_, err = st.CreateGovernedKnowledge(admin, CreateGovernedKnowledgeInput{Content: wrongKind, IdempotencyKey: "wrong-kind"})
	requireKnowledgeFault(t, err, 409)
	history, err := st.ListGovernedKnowledgeHistory(admin, first.Ref.KnowledgeID)
	if err != nil || len(history) != 5 || history[4].Action != "publish" || history[4].ActorID != admin.ID || history[4].Ref.ContentSHA256 != first.Ref.ContentSHA256 {
		t.Fatalf("history: %+v %v", history, err)
	}
	if _, err := st.pool.Exec(ctx, `UPDATE governed_knowledge_versions SET content=content WHERE tenant_id=$1 AND knowledge_id=$2`, admin.TenantID, first.Ref.KnowledgeID); err == nil {
		t.Fatal("immutable content update succeeded")
	}
	if _, err := st.pool.Exec(ctx, `DELETE FROM governed_knowledge_history WHERE tenant_id=$1 AND knowledge_id=$2`, admin.TenantID, first.Ref.KnowledgeID); err == nil {
		t.Fatal("audit history deletion succeeded")
	}
	if _, err := st.pool.Exec(ctx, `DELETE FROM governed_knowledge_pointers WHERE tenant_id=$1 AND knowledge_id=$2`, admin.TenantID, first.Ref.KnowledgeID); err == nil {
		t.Fatal("pointer tombstone deletion succeeded")
	}
	legacy, err := st.CreateKnowledge(admin, KnowledgeInput{ID: "legacy-only", Version: "1", Title: "旧制度", Body: "旧正文", SourceType: "reference"})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.VersionID == "" {
		t.Fatal("legacy version identity missing")
	}
	snapshot, err := st.ResolveGovernedKnowledge(ctx, admin, caseID, "2025-01-01T00:00:00Z", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.RequiredPolicies) != 1 || snapshot.RequiredPolicies[0].Ref != first.Ref || !snapshot.RequiredPolicies[0].Required {
		t.Fatalf("legacy content was promoted or mandatory policy was pruned: %+v", snapshot)
	}
	foreign, _ := accounts.ByID("demo-tenant2-admin")
	_, err = st.GetGovernedKnowledgeVersion(foreign, first.Ref.KnowledgeID, first.Ref.VersionID)
	requireKnowledgeFault(t, err, 404)
	_, err = st.ResolveGovernedKnowledge(ctx, foreign, caseID, "2025-01-01T00:00:00Z", 0)
	requireKnowledgeFault(t, err, 404)
}

func TestGovernedKnowledgeValidityBindingAndAmbiguityFailClosed(t *testing.T) {
	st, admin := openExecutionStore(t)
	pack := publishGovernedPackStore(t, st, admin, "1", "100")
	caseID := governanceTestCase(t, st, admin)
	ctx := context.Background()
	policyContent := knowledgeFixtureContent(pack, "operator-policy", "1", "policy")
	policyContent.ValidFrom = "2020-01-01T08:00:00+08:00"
	policyContent.ValidUntil = "2030-01-01T00:00:00Z"
	policy := publishKnowledge(t, st, admin, createKnowledgeDraft(t, st, admin, policyContent, "valid-policy"), "valid-policy")
	for _, at := range []string{"2020-01-01T00:00:00Z", "2029-12-31T23:59:59.999999999Z"} {
		got, err := st.ResolveGovernedKnowledge(ctx, admin, caseID, at, 0)
		if err != nil || len(got.RequiredPolicies) != 1 || got.RequiredPolicies[0].Ref != policy.Ref {
			t.Fatalf("valid at %s: %+v %v", at, got, err)
		}
	}
	for _, at := range []string{"2019-12-31T23:59:59Z", "2030-01-01T00:00:00Z"} {
		_, err := st.ResolveGovernedKnowledge(ctx, admin, caseID, at, 0)
		requireKnowledgeFault(t, err, 409)
	}
	_, err := st.ResolveGovernedKnowledge(ctx, admin, caseID, "", 0)
	requireKnowledgeFault(t, err, 422)
	wrong := knowledgeFixtureContent(pack, "wrong-binding", "1", "guidance")
	wrong.Binding.PackContentHash = strings.Repeat("0", 64)
	v := createKnowledgeDraft(t, st, admin, wrong, "wrong-binding")
	v = advanceKnowledge(t, st, admin, v, "submit", "wrong-binding-submit")
	_, err = st.GovernedKnowledgeCommand(admin, knowledgeCommand(v, "validate", "wrong-binding-validate"))
	requireKnowledgeFault(t, err, 409)
	badSpan := knowledgeFixtureContent(pack, "bad-span", "1", "guidance")
	badSpan.Binding.SourceSpans[0].Quote = "错误"
	_, err = st.CreateGovernedKnowledge(admin, CreateGovernedKnowledgeInput{Content: badSpan, IdempotencyKey: "bad-span"})
	requireKnowledgeFault(t, err, 422)
	global := knowledgeFixtureContent(pack, "scope-guide", "1", "guidance")
	local := knowledgeFixtureContent(pack, "scope-guide", "2", "guidance")
	local.Scope.OrgID = admin.OrgID
	publishKnowledge(t, st, admin, createKnowledgeDraft(t, st, admin, global, "global-guide"), "global-guide")
	publishKnowledge(t, st, admin, createKnowledgeDraft(t, st, admin, local, "local-guide"), "local-guide")
	_, err = st.ResolveGovernedKnowledge(ctx, admin, caseID, "2025-01-01T00:00:00Z", 10)
	requireKnowledgeFault(t, err, 409)
}

func TestGovernedKnowledgeWithdrawRollbackCASAndSnapshotRevalidation(t *testing.T) {
	st, admin := openExecutionStore(t)
	pack := publishGovernedPackStore(t, st, admin, "1", "100")
	caseID := governanceTestCase(t, st, admin)
	policy := governancePolicy(t, st, admin, pack)
	guide := knowledgeFixtureContent(pack, "payment-guide", "1", "guidance")
	first := publishKnowledge(t, st, admin, createKnowledgeDraft(t, st, admin, guide, "guide-one"), "guide-one")
	ctx := context.Background()
	snapshot, err := st.ResolveGovernedKnowledge(ctx, admin, caseID, "2025-01-01T00:00:00Z", 1)
	if err != nil || len(snapshot.RequiredPolicies) != 1 || snapshot.RequiredPolicies[0].Ref != policy.Ref || len(snapshot.OptionalCandidates) != 1 {
		t.Fatalf("snapshot: %+v %v", snapshot, err)
	}
	if err := st.RevalidateGovernedKnowledgeSnapshot(ctx, admin, caseID, snapshot); err != nil {
		t.Fatal(err)
	}
	body, err := st.ReadFrozenGovernedKnowledge(ctx, admin, caseID, snapshot, first.Ref)
	if err != nil || body.Body != guide.Body {
		t.Fatalf("frozen content: %+v %v", body, err)
	}
	modified := snapshot
	modified.OptionalCandidates = append([]GovernedKnowledgeSelection(nil), snapshot.OptionalCandidates...)
	modified.OptionalCandidates[0].Summary = "伪造摘要"
	modified, err = sealKnowledgeSnapshot(modified)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RevalidateGovernedKnowledgeSnapshot(ctx, admin, caseID, modified); err == nil {
		t.Fatal("rewritten selection with recomputed digest was accepted")
	}
	withdrawInput := knowledgeCommand(first, "withdraw", "withdraw-guide")
	withdrawn, err := st.GovernedKnowledgeCommand(admin, withdrawInput)
	if err != nil || withdrawn.Active || withdrawn.PointerRevision != first.PointerRevision+1 {
		t.Fatalf("withdraw: %+v %v", withdrawn, err)
	}
	replay, err := st.GovernedKnowledgeCommand(admin, withdrawInput)
	if err != nil || !reflect.DeepEqual(replay, withdrawn) {
		t.Fatalf("withdraw replay: %+v %v", replay, err)
	}
	if err := st.RevalidateGovernedKnowledgeSnapshot(ctx, admin, caseID, snapshot); err == nil {
		t.Fatal("withdrawn snapshot was reused")
	}
	if _, err := st.ReadFrozenGovernedKnowledge(ctx, admin, caseID, snapshot, first.Ref); err == nil {
		t.Fatal("withdrawn content was read")
	}
	stale := knowledgeCommand(withdrawn, "rollback", "rollback-stale")
	stale.ExpectedPointerRevision = first.PointerRevision
	_, err = st.GovernedKnowledgeCommand(admin, stale)
	requireKnowledgeFault(t, err, 409)
	rolled := advanceKnowledge(t, st, admin, withdrawn, "rollback", "rollback-guide")
	if !rolled.Active || rolled.PointerRevision != withdrawn.PointerRevision+1 {
		t.Fatalf("rollback: %+v", rolled)
	}
	if err := st.RevalidateGovernedKnowledgeSnapshot(ctx, admin, caseID, snapshot); err == nil {
		t.Fatal("ABA rollback reused a stale snapshot")
	}
	current, err := st.ResolveGovernedKnowledge(ctx, admin, caseID, "2025-01-01T00:00:00Z", 0)
	if err != nil || len(current.RequiredPolicies) != 1 || len(current.OptionalCandidates) != 0 || len(current.Excluded) != 1 || !current.OptionalTruncated {
		t.Fatalf("mandatory versus optional budget: %+v %v", current, err)
	}
	_, err = st.ReadFrozenGovernedKnowledge(ctx, admin, caseID, current, first.Ref)
	requireKnowledgeFault(t, err, 404)
	tampered := current
	tampered.SnapshotSHA256 = strings.Repeat("0", 64)
	if err := st.RevalidateGovernedKnowledgeSnapshot(ctx, admin, caseID, tampered); err == nil {
		t.Fatal("tampered snapshot was accepted")
	}
	if _, err := st.pool.Exec(ctx, `UPDATE cases SET member_ids='[]' WHERE tenant_id=$1 AND id=$2`, admin.TenantID, caseID); err != nil {
		t.Fatal(err)
	}
	err = st.RevalidateGovernedKnowledgeSnapshot(ctx, admin, caseID, current)
	requireKnowledgeFault(t, err, 404)
}

func TestGovernedKnowledgeConcurrentIdempotentPublish(t *testing.T) {
	st, admin := openExecutionStore(t)
	pack := publishGovernedPackStore(t, st, admin, "1", "100")
	v := createKnowledgeDraft(t, st, admin, knowledgeFixtureContent(pack, "operator-policy", "1", "policy"), "race-create")
	for _, action := range []string{"submit", "validate", "approve"} {
		v = advanceKnowledge(t, st, admin, v, action, "race-"+action)
	}
	input := knowledgeCommand(v, "publish", "race-publish")
	type result struct {
		version GovernedKnowledgeVersion
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := st.GovernedKnowledgeCommand(admin, input)
			results <- result{v, err}
		}()
	}
	wg.Wait()
	close(results)
	var got []GovernedKnowledgeVersion
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		got = append(got, result.version)
	}
	if len(got) != 2 || !reflect.DeepEqual(got[0], got[1]) || got[0].PointerRevision != 1 {
		t.Fatalf("concurrent publish: %+v", got)
	}
	history, err := st.ListGovernedKnowledgeHistory(admin, v.Ref.KnowledgeID)
	if err != nil || len(history) != 5 {
		t.Fatalf("publish replay duplicated audit history: %+v %v", history, err)
	}
}

func TestGovernedKnowledgeConcurrentPointerCAS(t *testing.T) {
	st, admin := openExecutionStore(t)
	pack := publishGovernedPackStore(t, st, admin, "1", "100")
	versions := make([]GovernedKnowledgeVersion, 2)
	for i, label := range []string{"1", "2"} {
		v := createKnowledgeDraft(t, st, admin, knowledgeFixtureContent(pack, "cas-guide", label, "guidance"), "cas-create-"+label)
		for _, action := range []string{"submit", "validate", "approve"} {
			v = advanceKnowledge(t, st, admin, v, action, "cas-"+label+"-"+action)
		}
		versions[i] = v
	}
	other, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	type result struct {
		version GovernedKnowledgeVersion
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i, instance := range []*Store{st, other} {
		input := knowledgeCommand(versions[i], "publish", "cas-publish-"+versions[i].Content.Version)
		wg.Add(1)
		go func(instance *Store, input GovernedKnowledgeCommandInput) {
			defer wg.Done()
			v, err := instance.GovernedKnowledgeCommand(admin, input)
			results <- result{v, err}
		}(instance, input)
	}
	wg.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for result := range results {
		if result.err == nil {
			succeeded++
			if result.version.PointerRevision != 1 {
				t.Fatalf("unexpected winning pointer: %+v", result.version)
			}
		} else {
			requireKnowledgeFault(t, result.err, 409)
			conflicted++
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("expected one CAS winner, got %d successes and %d conflicts", succeeded, conflicted)
	}
}

func TestGovernedExceptionRequiresBasisScopeAndExactPolicy(t *testing.T) {
	st, admin := openExecutionStore(t)
	pack := publishGovernedPackStore(t, st, admin, "1", "100")
	caseID := governanceTestCase(t, st, admin)
	policy := governancePolicy(t, st, admin, pack)
	content := knowledgeFixtureContent(pack, "approval-exception", "1", "exception")
	content.Scope.OrgID = admin.OrgID
	content.PolicyRefs = []KnowledgeRef{policy.Ref}
	_, err := st.CreateGovernedKnowledge(admin, CreateGovernedKnowledgeInput{Content: content, IdempotencyKey: "exception-missing-basis"})
	requireKnowledgeFault(t, err, 422)
	content.ExceptionBasis = "有权审批人的具体批准依据"
	content.Scope.OrgID = ""
	_, err = st.CreateGovernedKnowledge(admin, CreateGovernedKnowledgeInput{Content: content, IdempotencyKey: "exception-missing-scope"})
	requireKnowledgeFault(t, err, 422)
	content.Scope.OrgID = admin.OrgID
	content.ValidUntil = "2030-01-01T00:00:00Z"
	exception := publishKnowledge(t, st, admin, createKnowledgeDraft(t, st, admin, content, "exception"), "exception")
	got, err := st.ResolveGovernedKnowledge(context.Background(), admin, caseID, "2025-01-01T00:00:00Z", 5)
	if err != nil || len(got.RequiredPolicies) != 1 || got.RequiredPolicies[0].Ref != policy.Ref || len(got.OptionalCandidates) != 1 ||
		got.OptionalCandidates[0].Ref != exception.Ref || got.OptionalCandidates[0].ExceptionBasis != content.ExceptionBasis || !got.OptionalCandidates[0].RequiresFactEvaluation {
		t.Fatalf("exception removed policy or lost approval basis: %+v %v", got, err)
	}
}

func TestGovernedExperienceRequiresRealCandidateAndReviewedSource(t *testing.T) {
	st, admin, feedbackInput, _, pack, policy := feedbackGovernedFixture(t, "false_positive")
	feedback, err := st.SubmitExperienceFeedback(context.Background(), admin, feedbackInput)
	if err != nil {
		t.Fatal(err)
	}
	targetCase := governanceTestCase(t, st, admin)
	now := time.Now().UTC()
	candidateContent := candidateContentForFeedback(t, st, admin, feedback, now)
	delivery := createFeedbackDelivery(t, st, admin, feedback, feedbackDistillInput(t, st, admin, feedback, "real-candidate", ""), candidateContent, now.Add(time.Hour))
	candidate, err := acceptDelivery(st, admin, feedback, delivery)
	if err != nil {
		t.Fatal(err)
	}
	content := knowledgeFixtureContent(pack, "payment-experience", "1", "experience")
	content.SourceType, content.SourceCaseID = "human_feedback", feedback.CaseID
	content.CandidateID, content.CandidateVersionID, content.CandidateSHA256 = candidate.CandidateID, candidate.VersionID, candidate.ContentSHA256
	content.CandidateReviewDiff = "去除原案件身份信息并保留验收前付款反例"
	content.ReviewedApplicability = []string{"验收完成后支付"}
	content.ReviewedExclusions = []string{"验收前支付"}
	content.CheckAction, content.MinimumEvidence = "核对本案验收及付款日期", "验收记录及付款条款"
	content.Counterexamples = []string{"验收前付款仍需要提示风险"}
	content.PolicyRefs = []KnowledgeRef{policy.Ref}
	content.ValidFrom, content.ValidUntil = candidateContent.ValidFrom, candidateContent.ValidUntil
	forged := content
	forged.CandidateSHA256 = strings.Repeat("0", 64)
	_, err = st.CreateGovernedKnowledge(admin, CreateGovernedKnowledgeInput{Content: forged, IdempotencyKey: "forged-experience"})
	requireKnowledgeFault(t, err, 409)
	v := createKnowledgeDraft(t, st, admin, content, "real-experience")
	if v.Content.SourceFeedbackID != feedback.FeedbackID || v.Content.SourceOrgID != admin.OrgID {
		t.Fatalf("source was not server-derived: %+v", v.Content)
	}
	v = advanceKnowledge(t, st, admin, v, "submit", "experience-submit")
	v = advanceKnowledge(t, st, admin, v, "validate", "experience-validate")
	noReview := knowledgeCommand(v, "approve", "experience-unreviewed")
	_, err = st.GovernedKnowledgeCommand(admin, noReview)
	requireKnowledgeFault(t, err, 422)
	policy = advanceKnowledge(t, st, admin, policy, "withdraw", "source-policy-withdraw-before-approval")
	approval := knowledgeCommand(v, "approve", "experience-approve-withdrawn-policy")
	approval.CrossCaseReviewed = true
	_, err = st.GovernedKnowledgeCommand(admin, approval)
	requireKnowledgeFault(t, err, 409)
	policy = advanceKnowledge(t, st, admin, policy, "rollback", "source-policy-restore-before-approval")
	v = advanceKnowledge(t, st, admin, v, "approve", "experience-approve")
	policy = advanceKnowledge(t, st, admin, policy, "withdraw", "source-policy-withdraw-before-publish")
	_, err = st.GovernedKnowledgeCommand(admin, knowledgeCommand(v, "publish", "experience-publish-withdrawn-policy"))
	requireKnowledgeFault(t, err, 409)
	policy = advanceKnowledge(t, st, admin, policy, "rollback", "source-policy-restore-before-publish")
	v = advanceKnowledge(t, st, admin, v, "publish", "experience-publish")
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("reviewer missing")
	}
	if err := st.AddMember(admin, targetCase, reviewer.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.ResolveGovernedKnowledge(context.Background(), admin, targetCase, now.Format(time.RFC3339Nano), 10)
	if err != nil || len(snapshot.OptionalCandidates) != 1 || snapshot.OptionalCandidates[0].Ref != v.Ref ||
		!snapshot.OptionalCandidates[0].RequiresFactEvaluation || len(snapshot.OptionalCandidates[0].ReviewedExclusions) == 0 {
		t.Fatalf("experience selection lost verification boundary: %+v %v", snapshot, err)
	}
	if _, err := st.pool.Exec(context.Background(), `UPDATE cases SET member_ids='[]' WHERE tenant_id=$1 AND id=$2`, admin.TenantID, feedback.CaseID); err != nil {
		t.Fatal(err)
	}
	if err := st.RevalidateGovernedKnowledgeSnapshot(context.Background(), admin, targetCase, snapshot); err != nil {
		t.Fatalf("redacted experience lost target-case access when source access was revoked: %v", err)
	}
	reviewSnapshot, err := st.ResolveGovernedKnowledge(context.Background(), reviewer, targetCase, now.Format(time.RFC3339Nano), 10)
	if err != nil || len(reviewSnapshot.OptionalCandidates) != 1 || reviewSnapshot.OptionalCandidates[0].Ref != v.Ref {
		t.Fatalf("target reviewer could not resolve redacted experience: %+v %v", reviewSnapshot, err)
	}
	read, err := st.ReadFrozenGovernedKnowledge(context.Background(), reviewer, targetCase, reviewSnapshot, v.Ref)
	if err != nil || read.Body != content.Body {
		t.Fatalf("target reviewer could not read reviewed body: %+v %v", read, err)
	}
	if _, err := st.pool.Exec(context.Background(), `UPDATE cases SET member_ids='[]' WHERE tenant_id=$1 AND id=$2`, admin.TenantID, targetCase); err != nil {
		t.Fatal(err)
	}
	err = st.RevalidateGovernedKnowledgeSnapshot(context.Background(), reviewer, targetCase, reviewSnapshot)
	requireKnowledgeFault(t, err, 404)
	_, err = st.ReadFrozenGovernedKnowledge(context.Background(), reviewer, targetCase, reviewSnapshot, v.Ref)
	requireKnowledgeFault(t, err, 404)
	_, err = st.GetGovernedKnowledgeVersion(admin, v.Ref.KnowledgeID, v.Ref.VersionID)
	requireKnowledgeFault(t, err, 404)
	_, err = st.GovernedKnowledgeCommand(admin, knowledgeCommand(v, "withdraw", "lost-source-grant"))
	requireKnowledgeFault(t, err, 404)
}

func TestGovernedUnredactedExperienceRechecksAncestorGrants(t *testing.T) {
	st, owner, firstInput, _, pack, policy := feedbackGovernedFixture(t, "false_positive")
	ctx := context.Background()
	firstFeedback, err := st.SubmitExperienceFeedback(ctx, owner, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	secondInput := createGovernedFeedbackSource(t, st, owner, owner, "false_positive", "second-lineage-feedback")
	secondFeedback, err := st.SubmitExperienceFeedback(ctx, owner, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("reviewer missing")
	}
	if err := st.AddMember(owner, firstInput.CaseID, reviewer.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(owner, secondInput.CaseID, reviewer.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	firstContent := candidateContentForFeedback(t, st, owner, firstFeedback, now)
	firstDelivery := createFeedbackDelivery(t, st, owner, firstFeedback,
		feedbackDistillInput(t, st, owner, firstFeedback, "lineage-candidate", ""), firstContent, now.Add(time.Hour))
	firstCandidate, err := acceptDelivery(st, owner, firstFeedback, firstDelivery)
	if err != nil {
		t.Fatal(err)
	}
	secondContent := candidateContentForFeedback(t, st, owner, secondFeedback, now)
	secondContent.ReviewDiff = "保留来源案件中需要核验的条件"
	secondDelivery := createFeedbackDelivery(t, st, owner, secondFeedback,
		feedbackDistillInput(t, st, owner, secondFeedback, firstCandidate.CandidateID, firstCandidate.VersionID), secondContent, now.Add(time.Hour))
	secondCandidate, err := acceptDelivery(st, owner, secondFeedback, secondDelivery)
	if err != nil {
		t.Fatal(err)
	}
	content := knowledgeFixtureContent(pack, "lineage-experience", "1", "experience")
	content.SourceType, content.SourceCaseID = "human_feedback", secondFeedback.CaseID
	content.Scope.OrgID, content.Scope.CaseID = owner.OrgID, secondFeedback.CaseID
	content.CandidateID, content.CandidateVersionID, content.CandidateSHA256 = secondCandidate.CandidateID, secondCandidate.VersionID, secondCandidate.ContentSHA256
	content.CandidateReviewDiff = "保留来源范围，未进行跨案脱敏"
	content.ReviewedApplicability = []string{"验收完成后支付"}
	content.ReviewedExclusions = []string{"验收前支付"}
	content.CheckAction, content.MinimumEvidence = "核对本案验收及付款日期", "验收记录及付款条款"
	content.Counterexamples = []string{"验收前付款仍需要提示风险"}
	content.PolicyRefs = []KnowledgeRef{policy.Ref}
	content.ValidFrom, content.ValidUntil = secondContent.ValidFrom, secondContent.ValidUntil
	v := createKnowledgeDraft(t, st, owner, content, "lineage-experience-create")
	v = advanceKnowledge(t, st, owner, v, "submit", "lineage-experience-submit")
	v = advanceKnowledge(t, st, owner, v, "validate", "lineage-experience-validate")
	v, err = st.GovernedKnowledgeCommand(owner, knowledgeCommand(v, "approve", "lineage-experience-approve"))
	if err != nil {
		t.Fatal(err)
	}
	if v.CrossCaseReviewed {
		t.Fatal("case-restricted experience was unexpectedly marked redacted")
	}
	v = advanceKnowledge(t, st, owner, v, "publish", "lineage-experience-publish")
	snapshot, err := st.ResolveGovernedKnowledge(ctx, reviewer, secondFeedback.CaseID, now.Format(time.RFC3339Nano), 10)
	if err != nil || len(snapshot.OptionalCandidates) != 1 || snapshot.OptionalCandidates[0].Ref != v.Ref {
		t.Fatalf("authorized reviewer did not receive source-restricted experience: %+v %v", snapshot, err)
	}
	if _, err := st.ReadFrozenGovernedKnowledge(ctx, reviewer, secondFeedback.CaseID, snapshot, v.Ref); err != nil {
		t.Fatal(err)
	}
	members, err := json.Marshal([]string{owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(ctx, `UPDATE cases SET member_ids=$1 WHERE tenant_id=$2 AND id=$3`, string(members), owner.TenantID, firstFeedback.CaseID); err != nil {
		t.Fatal(err)
	}
	current, err := st.ResolveGovernedKnowledge(ctx, reviewer, secondFeedback.CaseID, now.Format(time.RFC3339Nano), 10)
	if err != nil || len(current.RequiredPolicies) == 0 || len(current.OptionalCandidates) != 0 || len(current.Excluded) != 0 {
		t.Fatalf("unauthorized ancestor content was disclosed in resolution: %+v %v", current, err)
	}
	err = st.RevalidateGovernedKnowledgeSnapshot(ctx, reviewer, secondFeedback.CaseID, snapshot)
	requireKnowledgeFault(t, err, 409)
	_, err = st.ReadFrozenGovernedKnowledge(ctx, reviewer, secondFeedback.CaseID, snapshot, v.Ref)
	requireKnowledgeFault(t, err, 409)
	if err := st.AddMember(owner, firstFeedback.CaseID, reviewer.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.RevalidateGovernedKnowledgeSnapshot(ctx, reviewer, secondFeedback.CaseID, snapshot); err != nil {
		t.Fatal(err)
	}
	read, err := st.ReadFrozenGovernedKnowledge(ctx, reviewer, secondFeedback.CaseID, snapshot, v.Ref)
	if err != nil || read.Body != content.Body {
		t.Fatalf("restored ancestor access: %+v %v", read, err)
	}
}

func refreshPolicyTestSpans(t *testing.T, content *GovernedKnowledgeContent) {
	t.Helper()
	for i := range content.Binding.SourceSpans {
		span := &content.Binding.SourceSpans[i]
		index := strings.Index(content.Body, span.Quote)
		if index < 0 {
			t.Fatalf("source quote %q missing from body %q", span.Quote, content.Body)
		}
		span.Start = utf8.RuneCountInString(content.Body[:index])
		span.End = span.Start + utf8.RuneCountInString(span.Quote)
	}
}

func TestPolicyThresholdExtractionRequiresProofAndExactValues(t *testing.T) {
	raw := governedPackManifest(t, "local-demo", "1", "100")
	parsed, err := parseGovernedManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	base := knowledgeFixtureContent(RulePackDetail{Manifest: raw}, "operator-policy", "1", "policy")
	checks := []struct {
		name   string
		change func(*GovernedKnowledgeContent)
		want   bool
		status string
	}{
		{"matching", func(*GovernedKnowledgeContent) {}, true, "match"},
		{"equivalent bandwidth unit", func(c *GovernedKnowledgeContent) {
			c.Body = strings.Replace(c.Body, c.Binding.SourceSpans[0].Quote, "带宽不低于0.1 Gbit/s", 1)
			c.Binding.SourceSpans[0].Quote = "带宽不低于0.1 Gbit/s"
		}, true, "match"},
		{"different bandwidth", func(c *GovernedKnowledgeContent) {
			c.Body = strings.Replace(c.Body, c.Binding.SourceSpans[0].Quote, "带宽不低于150 Mbit/s", 1)
			c.Binding.SourceSpans[0].Quote = "带宽不低于150 Mbit/s"
		}, false, "different"},
		{"ambiguous bandwidth", func(c *GovernedKnowledgeContent) {
			c.Body += "带宽不低于200 Mbit/s。"
		}, false, "ambiguous"},
		{"negated bandwidth", func(c *GovernedKnowledgeContent) {
			c.Body = strings.Replace(c.Body, c.Binding.SourceSpans[0].Quote, "并非"+c.Binding.SourceSpans[0].Quote, 1)
		}, false, "unexpressible"},
		{"unsupported bandwidth unit", func(c *GovernedKnowledgeContent) {
			c.Body = strings.Replace(c.Body, c.Binding.SourceSpans[0].Quote, "带宽不低于100 MiB/s", 1)
			c.Binding.SourceSpans[0].Quote = "带宽不低于100 MiB/s"
		}, false, "unexpressible"},
		{"wrong direction", func(c *GovernedKnowledgeContent) {
			c.Body = strings.Replace(c.Body, c.Binding.SourceSpans[0].Quote, "带宽不高于100 Mbit/s", 1)
			c.Binding.SourceSpans[0].Quote = "带宽不高于100 Mbit/s"
		}, false, "unexpressible"},
		{"unknown numeric clause", func(c *GovernedKnowledgeContent) { c.Body += "另须满足200元要求。" }, false, "match"},
		{"semantic check remains explicit", func(c *GovernedKnowledgeContent) { c.Body += "应明确服务中断赔偿责任。" }, true, "match"},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			content := base
			content.Binding.SourceSpans = append([]KnowledgeSourceSpan(nil), base.Binding.SourceSpans...)
			check.change(&content)
			refreshPolicyTestSpans(t, &content)
			preview := compareGovernedPolicyThresholds(content, parsed.Manifest())
			if preview.Approvable != check.want || len(preview.Comparisons) != 3 || preview.Comparisons[0].Status != check.status {
				t.Fatalf("preview: %+v", preview)
			}
			if check.name == "semantic check remains explicit" && (len(preview.SemanticOnly) != 1 || preview.SemanticOnly[0].Reason != "semantic_check_only") {
				t.Fatalf("semantic-only statement disappeared: %+v", preview)
			}
			if check.name == "matching" || check.name == "semantic check remains explicit" {
				selection := knowledgeSelection(GovernedKnowledgeVersion{Content: content})
				if selection.RequiresFactEvaluation != (check.name == "semantic check remains explicit") || len(selection.SemanticOnly) != len(preview.SemanticOnly) {
					t.Fatalf("frozen selection lost semantic-check requirement: %+v", selection)
				}
				encoded, err := json.Marshal(selection)
				if err != nil {
					t.Fatal(err)
				}
				var restored GovernedKnowledgeSelection
				if err := json.Unmarshal(encoded, &restored); err != nil || !reflect.DeepEqual(selection, restored) {
					t.Fatalf("selection changed after JSON round trip: %+v %+v %v", selection, restored, err)
				}
			}
			for _, comparison := range preview.Comparisons {
				if comparison.Candidate == nil {
					continue
				}
				candidate := comparison.Candidate
				if string([]rune(content.Body)[candidate.Start:candidate.End]) != candidate.Quote {
					t.Fatalf("incorrect Unicode span: %+v", candidate)
				}
			}
		})
	}
	for _, check := range []struct {
		name   string
		change func(string) string
	}{
		{"SLA timezone mismatch", func(body string) string { return strings.Replace(body, "Asia/Shanghai", "Asia/Tokyo", 1) }},
		{"missing payment basis", func(body string) string { return strings.Replace(body, "，预付款以合同总额为基础", "", 1) }},
		{"contradictory payment ratio", func(body string) string {
			return body + "预付款比例不超过1/4，预付款以合同总额为基础。"
		}},
	} {
		t.Run(check.name, func(t *testing.T) {
			content := base
			content.Binding.SourceSpans = append([]KnowledgeSourceSpan(nil), base.Binding.SourceSpans...)
			content.Body = check.change(content.Body)
			refreshPolicyTestSpans(t, &content)
			preview := compareGovernedPolicyThresholds(content, parsed.Manifest())
			if preview.Approvable {
				t.Fatalf("unsupported or contradictory policy was accepted: %+v", preview)
			}
		})
	}
	missingSpan := base
	missingSpan.Binding.SourceSpans = append([]KnowledgeSourceSpan(nil), base.Binding.SourceSpans...)
	missingSpan.Binding.SourceSpans[0].Quote = "带宽"
	refreshPolicyTestSpans(t, &missingSpan)
	preview := compareGovernedPolicyThresholds(missingSpan, parsed.Manifest())
	if preview.Approvable || preview.Comparisons[0].Reason != "source_span_missing_or_ambiguous" {
		t.Fatalf("partial source span was treated as threshold evidence: %+v", preview)
	}
	unbound := base
	unbound.Binding.RuleIDs = []string{base.Binding.RuleIDs[0]}
	unbound.Binding.SourceSpans = []KnowledgeSourceSpan{base.Binding.SourceSpans[0]}
	preview = compareGovernedPolicyThresholds(unbound, parsed.Manifest())
	if preview.Approvable || len(preview.Issues) != 2 {
		t.Fatalf("unbound policy thresholds were ignored: %+v", preview)
	}
}

func TestPolicyPreviewExposesMismatchAndValidationBlocksIt(t *testing.T) {
	st, admin := openExecutionStore(t)
	pack := publishGovernedPackStore(t, st, admin, "1", "100")
	content := knowledgeFixtureContent(pack, "operator-policy", "1", "policy")
	content.Body = strings.Replace(content.Body, content.Binding.SourceSpans[0].Quote, "带宽不低于150 Mbit/s", 1)
	content.Binding.SourceSpans[0].Quote = "带宽不低于150 Mbit/s"
	refreshPolicyTestSpans(t, &content)
	v := createKnowledgeDraft(t, st, admin, content, "different-threshold-create")
	preview, err := st.PreviewGovernedPolicy(admin, v.Ref.KnowledgeID, v.Ref.VersionID)
	if err != nil || preview.Approvable || len(preview.Comparisons) != 3 || preview.Comparisons[0].Status != "different" ||
		preview.Comparisons[0].Candidate == nil || preview.Comparisons[0].Candidate.Value != "150" || preview.Comparisons[0].RuleThreshold.Value != "100" {
		t.Fatalf("preview omitted the policy-to-rule difference: %+v %v", preview, err)
	}
	v = advanceKnowledge(t, st, admin, v, "submit", "different-threshold-submit")
	_, err = st.GovernedKnowledgeCommand(admin, knowledgeCommand(v, "validate", "different-threshold-validate"))
	requireKnowledgeFault(t, err, 409)
}
