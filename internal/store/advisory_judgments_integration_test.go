package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/advisory"
	"contractaudit/internal/agentflow"
	"contractaudit/internal/jev"
)

type countingAdvisoryProvider struct {
	calls         int
	probabilities map[string]float64
}

func (p *countingAdvisoryProvider) ProviderIdentity() string {
	return agentflow.AdvisoryProviderGateway
}

func (p *countingAdvisoryProvider) AskNouls(_ context.Context, request advisory.ProviderRequest) (advisory.ProviderResponse, error) {
	p.calls++
	answers := make(map[string]jev.NoulAnswer, len(request.Questions))
	for questionID := range request.Questions {
		alias := strings.TrimSuffix(questionID, "_relevance")
		answers[questionID] = jev.NoulAnswer{Noul: p.probabilities[alias], Model: request.Model}
	}
	return advisory.ProviderResponse{Model: request.Model, Answers: answers}, nil
}

func TestAdvisoryJudgmentIsPersistedBeforeReuseAndIsAppendOnly(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")) == "" {
		t.Skip("TEST_DATABASE_URL is required for advisory judgment persistence")
	}
	t.Setenv("APP_ENV", "test")
	t.Setenv("TYPESAFE_JEV_MODE", "off")
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatalf("open isolated store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	caseID, err := st.CreateCase(user, "advisory judgment persistence", "procurement_ict")
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	for _, item := range []struct{ role, text string }{
		{"procurement", "采购文件要求质保36个月。"},
		{"draft_contract", "拟签合同约定质保24个月。"},
	} {
		if _, err := st.AddDocument(ctx, user, caseID, item.role, item.role+".txt", "text/plain", []byte(item.text)); err != nil {
			t.Fatalf("add document: %v", err)
		}
	}
	if _, err := st.StartReviewAsync(ctx, user, caseID); err != nil {
		t.Fatalf("accept review: %v", err)
	}
	claim, err := st.ClaimNextJob(ctx, "advisory-judgment-test-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim review: %v", err)
	}

	configuration := agentflow.AdvisoryConfiguration{
		Mode: agentflow.AdvisoryModeAdvisory, Provider: agentflow.AdvisoryProviderGateway, Model: "jev-1.14.0",
		QuestionSetVersion: agentflow.AdvisoryQuestionSetVersion, Egress: agentflow.AdvisoryEgressMinimized,
		Features: []string{agentflow.AdvisoryFeatureKnowledgeRerank}, ThresholdHigh: 0.70, ThresholdLow: 0.30,
		MaxCandidates: 16, TimeoutMS: 3000,
	}
	request, err := advisory.BuildKnowledgeRequest(configuration,
		advisory.KnowledgeScope{BusinessLine: "telecom_enterprise", Scenario: "dedicated_line", ContractType: "procurement"},
		[]advisory.KnowledgeGap{{Domain: "compliance", Capability: "warranty", Reason: "missing_evidence"}},
		[]advisory.KnowledgeCandidate{
			{Kind: "guidance", Summary: "质保期限应与采购文件一致", CheckAction: "compare_warranty", MinimumEvidence: "signed_contract"},
			{Kind: "experience", Summary: "专线项目常见交付延期", CheckAction: "check_delivery", MinimumEvidence: "acceptance_record"},
		})
	if err != nil {
		t.Fatalf("build advisory request: %v", err)
	}
	provider := &countingAdvisoryProvider{probabilities: map[string]float64{"candidate_0": 0.82, "candidate_1": 0.12}}
	evaluator, err := newStoreAdvisoryEvaluator(st, claim, configuration,
		strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), provider)
	if err != nil {
		t.Fatalf("build evaluator: %v", err)
	}
	first, err := evaluator.Evaluate(ctx, request)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if provider.calls != 1 || first.Cached || first.JudgmentID == "" {
		t.Fatalf("first evaluation = %+v, provider calls=%d", first, provider.calls)
	}
	second, err := evaluator.Evaluate(ctx, request)
	if err != nil {
		t.Fatalf("reuse evaluation: %v", err)
	}
	if provider.calls != 1 || !second.Cached || second.JudgmentID != first.JudgmentID ||
		second.Result.Probabilities["candidate_0"] != 0.82 {
		t.Fatalf("reused evaluation = %+v, provider calls=%d", second, provider.calls)
	}

	count := func(scope dbScope) int {
		t.Helper()
		tx, err := st.pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin count: %v", err)
		}
		defer tx.Rollback(ctx)
		if err := setLocalScope(ctx, tx, scope); err != nil {
			t.Fatalf("set count scope: %v", err)
		}
		var total int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM advisory_judgments WHERE judgment_id=$1`, first.JudgmentID).Scan(&total); err != nil {
			t.Fatalf("count judgments: %v", err)
		}
		return total
	}
	if got := count(claim.scope()); got != 1 {
		t.Fatalf("persisted judgments = %d, want 1", got)
	}
	var rowSecurity, forceRowSecurity bool
	if err := st.pool.QueryRow(ctx, `SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE oid='advisory_judgments'::regclass`).Scan(&rowSecurity, &forceRowSecurity); err != nil {
		t.Fatalf("inspect advisory judgment RLS: %v", err)
	}
	if !rowSecurity || !forceRowSecurity {
		t.Fatalf("advisory_judgments RLS = enabled:%v forced:%v", rowSecurity, forceRowSecurity)
	}
	var policies int
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM pg_policies WHERE schemaname=current_schema() AND tablename='advisory_judgments' AND policyname='tenant_boundary'`).Scan(&policies); err != nil {
		t.Fatalf("inspect advisory judgment policy: %v", err)
	}
	if policies != 1 {
		t.Fatalf("advisory_judgments tenant policy count = %d", policies)
	}

	for _, statement := range []string{
		`UPDATE advisory_judgments SET error_code='tampered' WHERE judgment_id=$1`,
		`DELETE FROM advisory_judgments WHERE judgment_id=$1`,
	} {
		tx, err := st.pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin append-only check: %v", err)
		}
		if err := setLocalScope(ctx, tx, claim.scope()); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("set append-only scope: %v", err)
		}
		_, execErr := tx.Exec(ctx, statement, first.JudgmentID)
		_ = tx.Rollback(ctx)
		if execErr == nil {
			t.Fatalf("append-only statement succeeded: %s", statement)
		}
	}

}
