package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"contractaudit/internal/access"
	"contractaudit/internal/agentflow"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func plannerSelectionFixture(id, kind, summary string) GovernedKnowledgeSelection {
	return GovernedKnowledgeSelection{Ref: KnowledgeRef{KnowledgeID: id, VersionID: sha256Bytes([]byte("version:" + id)), ContentSHA256: sha256Bytes([]byte("content:" + id))},
		Kind: kind, Required: kind == "policy", Summary: summary, RequiresFactEvaluation: kind == "experience" || kind == "exception",
		Scope:     KnowledgeScope{BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"},
		ValidFrom: "2020-01-01T00:00:00Z", ValidUntil: "2100-01-01T00:00:00Z"}
}

func TestGovernedPlannerSelectionRanksAndRecordsEveryCandidate(t *testing.T) {
	snapshot := ReviewKnowledgeSnapshot{SnapshotSHA256: sha256Bytes([]byte("snapshot")), Scope: KnowledgeScope{BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"}}
	snapshot.RequiredPolicies = []GovernedKnowledgeSelection{plannerSelectionFixture("policy", "policy", "制度摘要")}
	for i := 34; i >= 0; i-- {
		snapshot.OptionalCandidates = append(snapshot.OptionalCandidates, plannerSelectionFixture(fmt.Sprintf("guide-%02d", i), "guidance", "最低带宽核验"))
	}
	snapshot.OptionalCandidates = append(snapshot.OptionalCandidates, plannerSelectionFixture("irrelevant", "guidance", "租赁物业"))
	snapshot.Excluded = []GovernedKnowledgeExclusion{{Ref: plannerSelectionFixture("acceptance-omitted", "guidance", "").Ref, Reason: "optional_budget"}}
	snapshot.OptionalTruncated = true
	gaps := []agentflow.Gap{{ID: "bandwidth", Domain: agentflow.Technical, Capability: "scope", Question: "核查最低带宽"}}
	evidence := []agentflow.Evidence{{ID: "contract", Role: "draft_contract", Text: "合同规定最低带宽为100兆。", SHA256: agentflow.TextHash("合同规定最低带宽为100兆。")}}
	refs, trace, err := governedPlannerKnowledge(&snapshot, gaps, evidence, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != agentflow.PlannerKnowledgeReferenceBudget || !refs[0].Required || refs[0].KnowledgeID != "policy" ||
		refs[1].KnowledgeID != "guide-00" || refs[len(refs)-1].KnowledgeID != "guide-30" {
		t.Fatalf("policy or stable relevance selection = %+v", refs)
	}
	if trace.PolicyStatus != "available" || trace.Budget.OptionalCandidates != 36 || trace.Budget.OptionalSelected != 31 ||
		trace.Budget.OptionalOmitted != 5 || trace.Budget.AcceptanceOmitted != 1 || len(trace.Candidates) != 38 {
		t.Fatalf("candidate or budget record = %+v", trace)
	}
	for _, candidate := range trace.Candidates {
		if candidate.KnowledgeID == "irrelevant" && (candidate.Reason != "no_relevance" || candidate.Score != 0) {
			t.Fatalf("irrelevant candidate = %+v", candidate)
		}
		if candidate.KnowledgeID == "guide-31" && candidate.Reason != "planner_budget" {
			t.Fatalf("budget omission = %+v", candidate)
		}
	}
	for left, right := 0, len(snapshot.OptionalCandidates)-1; left < right; left, right = left+1, right-1 {
		snapshot.OptionalCandidates[left], snapshot.OptionalCandidates[right] = snapshot.OptionalCandidates[right], snapshot.OptionalCandidates[left]
	}
	againRefs, againTrace, err := governedPlannerKnowledge(&snapshot, gaps, evidence, 2<<20)
	if err != nil || !reflect.DeepEqual(refs, againRefs) || !reflect.DeepEqual(trace, againTrace) {
		t.Fatalf("reordering the frozen candidates changed selection: %v", err)
	}
	if len(trace.PlannerRounds) != 0 {
		t.Fatal("retrieval was incorrectly recorded as observed planner use")
	}
}

func TestGovernedPlannerKeepsAllRequiredPoliciesAndMarksTruncation(t *testing.T) {
	snapshot := ReviewKnowledgeSnapshot{SnapshotSHA256: sha256Bytes([]byte("snapshot"))}
	for i := range 40 {
		snapshot.RequiredPolicies = append(snapshot.RequiredPolicies, plannerSelectionFixture(fmt.Sprintf("policy-%02d", i), "policy", strings.Repeat("制度", 180)))
	}
	snapshot.OptionalCandidates = []GovernedKnowledgeSelection{plannerSelectionFixture("guide", "guidance", "带宽")}
	gaps := []agentflow.Gap{{ID: "bandwidth", Domain: agentflow.Technical, Capability: "scope", Question: "检查带宽"}}
	refs, trace, err := governedPlannerKnowledge(&snapshot, gaps, nil, 1024)
	if err != nil || len(refs) != 40 || trace.PolicyStatus != "unknown" || trace.PolicyReason != "policy_context_truncated" || trace.Budget.ContentBytesUsed > 1024 {
		t.Fatalf("required policies or truncated state = %+v %+v %v", refs, trace, err)
	}
	for _, ref := range refs {
		if !ref.Required || !utf8.ValidString(ref.Summary) {
			t.Fatalf("required reference lost or invalid UTF-8: %+v", ref)
		}
	}
	if !trace.Candidates[0].Truncated || trace.Candidates[len(trace.Candidates)-1].Reason != "planner_budget" {
		t.Fatalf("truncation and omission were not explicit: %+v", trace.Candidates)
	}
	missingRefs, missing, err := governedPlannerKnowledge(nil, gaps, nil, 1024)
	if err != nil || len(missingRefs) != 0 || missing.PolicyStatus != "unknown" || missing.PolicyReason != "policy_snapshot_missing" {
		t.Fatalf("missing policy acquired a conclusion: %+v %v", missing, err)
	}
}

func TestGovernedPlannerCopiesDigestAndPackIdentity(t *testing.T) {
	knowledge := ReviewKnowledgeSnapshot{Encoding: "review-knowledge-snapshot/v1", AuthorizationRevision: sha256Bytes([]byte("auth")),
		RulePack:         GovernedRulePackRef{PackID: "pack", Version: "1", ContentHash: sha256Bytes([]byte("pack")), SnapshotDigest: sha256Bytes([]byte("pack-snapshot")), CompilerRef: "compiler", PointerRevision: 1},
		RequiredPolicies: []GovernedKnowledgeSelection{plannerSelectionFixture("policy", "policy", "原摘要")}}
	sealed, err := sealKnowledgeSnapshot(knowledge)
	if err != nil {
		t.Fatal(err)
	}
	frozen := RunSnapshot{RulePackID: sealed.RulePack.PackID, RulePackVersion: sealed.RulePack.Version, RulePackHash: sealed.RulePack.ContentHash,
		RulePackDigest: sealed.RulePack.SnapshotDigest, RulePackCompilerRef: sealed.RulePack.CompilerRef,
		RulePackSnapshot: []byte("pack-snapshot"), GovernedKnowledge: &sealed}
	copy, err := copyGovernedKnowledge(frozen)
	if err != nil {
		t.Fatal(err)
	}
	copy.RequiredPolicies[0].Summary = "changed"
	if frozen.GovernedKnowledge.RequiredPolicies[0].Summary != "原摘要" {
		t.Fatal("copy aliased the accepted snapshot")
	}
	tampered := frozen
	tampered.RulePackHash = sha256Bytes([]byte("another-pack"))
	if _, err := copyGovernedKnowledge(tampered); err == nil {
		t.Fatal("mismatched pack was accepted")
	}
	tampered = frozen
	tampered.RulePackSnapshot = []byte("modified-pack-snapshot")
	if _, err := copyGovernedKnowledge(tampered); err == nil {
		t.Fatal("mismatched frozen pack bytes were accepted")
	}
	tampered = frozen
	broken := sealed
	broken.RequiredPolicies = append([]GovernedKnowledgeSelection(nil), sealed.RequiredPolicies...)
	broken.RequiredPolicies[0].Summary = "rewritten"
	tampered.GovernedKnowledge = &broken
	if _, err := copyGovernedKnowledge(tampered); err == nil {
		t.Fatal("changed summary retained the old digest")
	}
}

type governedPlanningCapture struct {
	*frozenOCRModel
	inputs    []agentflow.PlanInput
	readFirst bool
}

func (capture *governedPlanningCapture) Generate(ctx context.Context, messages []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	if len(messages) != 2 {
		return nil, fmt.Errorf("expected planner messages")
	}
	var input agentflow.PlanInput
	if err := json.Unmarshal([]byte(messages[1].Content), &input); err != nil {
		return nil, err
	}
	capture.inputs = append(capture.inputs, input)
	var plan agentflow.Plan
	var err error
	if capture.readFirst && len(capture.inputs) == 1 {
		for _, ref := range input.Knowledge {
			if ref.Required {
				plan = agentflow.Plan{Calls: []agentflow.Call{}, KnowledgeReads: []agentflow.KnowledgeReadRequest{{
					KnowledgeID: ref.KnowledgeID, VersionID: ref.VersionID, ContentSHA256: ref.ContentSHA256, Start: 0, End: 2}}}
				break
			}
		}
		if plan.Calls == nil {
			return nil, fmt.Errorf("required policy was not supplied")
		}
	} else {
		plan, err = (agentflow.ScopePlanner{}).Plan(ctx, input)
		if err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	return schema.AssistantMessage(string(raw), nil), nil
}

func TestPersistedGovernedSnapshotReachesRealAgentPlanner(t *testing.T) {
	t.Setenv("AGENT_PLANNER_MODE", "fixture")
	t.Setenv("AGENT_PLANNER_MAX_ROUNDS", "2")
	t.Setenv("AGENT_PLANNER_MAX_CALLS", "4")
	st, admin := openExecutionStore(t)
	capture := &governedPlanningCapture{frozenOCRModel: &frozenOCRModel{}, readFirst: true}
	st.UseModels(capture)
	pack := publishSyntheticAcceptancePack(t, st, admin)
	policy := publishSyntheticAcceptancePolicy(t, st, admin, pack)
	content := knowledgeFixtureContent(pack, "bandwidth-guidance", "1", "guidance")
	content.Summary = "检查最低带宽和交付范围"
	content.ReviewedApplicability = []string{"政企专线适用"}
	content.ReviewedExclusions = []string{"不适用移动业务"}
	content.CheckAction = "比较合同与采购最低带宽"
	content.MinimumEvidence = "两份冻结材料"
	content.Counterexamples = []string{"口头承诺不能代替合同"}
	guide := publishKnowledge(t, st, admin, createKnowledgeDraft(t, st, admin, content, "planner-guide-create"), "planner-guide")
	caseID := acceptanceKnowledgeCase(t, st, st, admin)
	accepted, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	frozen := readWorkspace(t, st, admin, caseID, accepted.RunID).Snapshot.GovernedKnowledge
	if frozen == nil {
		t.Fatal("acceptance did not persist governance")
	}
	runAcceptedKnowledgeJob(t, st, accepted)
	if len(capture.inputs) != 2 || len(capture.inputs[1].KnowledgeObservations) != 1 || capture.inputs[1].KnowledgeObservations[0].Text != "带宽" {
		t.Fatalf("the real frozen policy span did not reach the next planning round: %+v", capture.inputs)
	}
	var gotPolicy, gotGuide *agentflow.KnowledgeReference
	for i := range capture.inputs[0].Knowledge {
		ref := &capture.inputs[0].Knowledge[i]
		switch ref.KnowledgeID {
		case policy.Ref.KnowledgeID:
			gotPolicy = ref
		case guide.Ref.KnowledgeID:
			gotGuide = ref
		}
	}
	if gotPolicy == nil || !gotPolicy.Required || gotPolicy.ContentSHA256 != policy.Ref.ContentSHA256 ||
		gotGuide == nil || gotGuide.VersionID != guide.Ref.VersionID || gotGuide.Summary != content.Summary ||
		!strings.Contains(gotGuide.Applicability, "政企专线适用") || !strings.Contains(gotGuide.Exclusions, "不适用移动业务") ||
		gotGuide.CheckAction != content.CheckAction || gotGuide.MinimumEvidence != content.MinimumEvidence || !strings.Contains(gotGuide.Counterexamples, content.Counterexamples[0]) {
		t.Fatalf("planner did not receive approved frozen content: %+v", capture.inputs[0].Knowledge)
	}
	raw, present, err := st.AgentReviewForAttempt(context.Background(), admin, caseID, accepted.RunID, accepted.AttemptID)
	if err != nil || !present {
		t.Fatalf("persisted review: present=%v err=%v", present, err)
	}
	var review agentflow.AgentReview
	if err := json.Unmarshal(raw, &review); err != nil {
		t.Fatal(err)
	}
	trace := review.KnowledgeSelection
	if trace == nil || trace.SnapshotSHA256 != frozen.SnapshotSHA256 || trace.PolicyStatus != "available" || len(trace.PlannerRounds) != 2 ||
		len(trace.PlannerRounds[0].ReferencesProvided) != len(capture.inputs[0].Knowledge) || len(trace.Reads) != 1 ||
		trace.Reads[0].Status != "completed" || trace.Reads[0].TextSHA256 != agentflow.TextHash("带宽") ||
		len(trace.PlannerRounds[1].ReadObservationsProvided) != 1 {
		t.Fatalf("persisted selection did not record observed delivery: %+v", trace)
	}
}

func TestSemanticPolicyStaysUnknownThroughRealPlanning(t *testing.T) {
	t.Setenv("AGENT_PLANNER_MODE", "fixture")
	t.Setenv("AGENT_PLANNER_MAX_ROUNDS", "2")
	t.Setenv("AGENT_PLANNER_MAX_CALLS", "4")
	st, admin := openExecutionStore(t)
	capture := &governedPlanningCapture{frozenOCRModel: &frozenOCRModel{}}
	st.UseModels(capture)
	pack := publishSyntheticAcceptancePack(t, st, admin)
	content := knowledgeFixtureContent(pack, "acceptance-demo-policy", "1", "policy")
	content.SourceType, content.SourceReference = "synthetic_policy", "approved synthetic acceptance fixture"
	content.Body += "应明确服务中断赔偿责任。"
	policy := publishKnowledge(t, st, admin, createKnowledgeDraft(t, st, admin, content, "semantic-policy-create"), "semantic-policy")
	caseID := acceptanceKnowledgeCase(t, st, st, admin)
	accepted, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	runAcceptedKnowledgeJob(t, st, accepted)
	if len(capture.inputs) == 0 || capture.inputs[0].KnowledgePolicyReason != "policy_semantic_verification_required" {
		t.Fatalf("planner did not receive semantic incompleteness: %+v", capture.inputs)
	}
	found := false
	for _, ref := range capture.inputs[0].Knowledge {
		if ref.KnowledgeID == policy.Ref.KnowledgeID {
			found = ref.RequiresFactEvaluation && strings.Contains(ref.SemanticChecks, "应明确服务中断赔偿责任")
		}
	}
	if !found {
		t.Fatalf("semantic-only source span was not supplied: %+v", capture.inputs[0].Knowledge)
	}
	checkFound := false
	for _, check := range capture.inputs[0].MandatoryChecks {
		if check.Capability == "governed_policy_semantics" && check.Status == "unknown" && check.Reason == "policy_semantic_verification_required" {
			checkFound = true
		}
	}
	if !checkFound {
		t.Fatal("semantic policy obligation disappeared from the mandatory checklist")
	}
	raw, present, err := st.AgentReviewForAttempt(context.Background(), admin, caseID, accepted.RunID, accepted.AttemptID)
	if err != nil || !present {
		t.Fatalf("persisted review: present=%v err=%v", present, err)
	}
	var review agentflow.AgentReview
	if err := json.Unmarshal(raw, &review); err != nil {
		t.Fatal(err)
	}
	if review.Status != "partial" || review.KnowledgeSelection == nil || review.KnowledgeSelection.PolicyStatus != "unknown" ||
		review.KnowledgeSelection.PolicyReason != "policy_semantic_verification_required" {
		t.Fatalf("numeric checks concealed an unverified semantic policy: %+v", review)
	}
	gapFound := false
	for _, gap := range review.Gaps {
		if gap.Capability == "governed_policy_semantics" && gap.Mandatory && gap.Status == "unknown" {
			gapFound = true
		}
	}
	if !gapFound {
		t.Fatal("semantic policy obligation disappeared from the durable review")
	}
}

func TestGovernedPlannerRechecksActorAndPolicyWithdrawal(t *testing.T) {
	st, admin := openExecutionStore(t)
	pack := publishSyntheticAcceptancePack(t, st, admin)
	policy := publishSyntheticAcceptancePolicy(t, st, admin, pack)
	caseID := acceptanceKnowledgeCase(t, st, st, admin)
	accepted, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimNextJob(context.Background(), "planner-auth-worker", time.Minute)
	if err != nil || claim.JobID != accepted.JobID {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	state, err := st.snapshotForAttemptContext(context.Background(), claim.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := state.caseByID(caseID)
	if !ok {
		t.Fatal("missing claimed case")
	}
	resource := access.Resource{TenantID: item.TenantID, CaseID: item.ID, OrgID: item.OwnerOrgID, OwnerID: item.OwnerID}
	frozen, err := copyGovernedKnowledge(state.snapshotOf(accepted.RunID))
	if err != nil || frozen == nil {
		t.Fatalf("frozen snapshot: %+v %v", frozen, err)
	}
	if err := st.revalidateAgentPlannerKnowledge(context.Background(), claim.RunID, claim.AttemptID, resource, admin.ID, frozen); err != nil {
		t.Fatal(err)
	}
	if err := st.revalidateAgentPlannerKnowledge(context.Background(), claim.RunID, claim.AttemptID, resource, "another-actor", frozen); err == nil {
		t.Fatal("caller selected a different actor")
	}
	selected := map[KnowledgeRef]bool{policy.Ref: true}
	read := agentflow.KnowledgeReadRequest{KnowledgeID: policy.Ref.KnowledgeID, VersionID: policy.Ref.VersionID, ContentSHA256: policy.Ref.ContentSHA256, Start: 0, End: 2}
	span, err := st.readAgentPlannerKnowledge(context.Background(), claim.RunID, claim.AttemptID, resource, admin.ID, frozen, selected, read)
	if err != nil || span != "带宽" {
		t.Fatalf("frozen policy span = %q, %v", span, err)
	}
	wrong := read
	wrong.ContentSHA256 = sha256Bytes([]byte("another version"))
	if _, err := st.readAgentPlannerKnowledge(context.Background(), claim.RunID, claim.AttemptID, resource, admin.ID, frozen, selected, wrong); err == nil {
		t.Fatal("unselected version was read")
	}
	wrong = read
	wrong.End = 999
	if _, err := st.readAgentPlannerKnowledge(context.Background(), claim.RunID, claim.AttemptID, resource, admin.ID, frozen, selected, wrong); err == nil {
		t.Fatal("oversized span was read")
	}
	advanceKnowledge(t, st, admin, policy, "withdraw", "planner-policy-withdraw")
	if err := st.revalidateAgentPlannerKnowledge(context.Background(), claim.RunID, claim.AttemptID, resource, admin.ID, frozen); err == nil {
		t.Fatal("withdrawn policy entered planning")
	}
	if _, err := st.readAgentPlannerKnowledge(context.Background(), claim.RunID, claim.AttemptID, resource, admin.ID, frozen, selected, read); err == nil {
		t.Fatal("withdrawn policy span was read")
	}
}
