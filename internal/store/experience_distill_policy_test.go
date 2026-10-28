package store

import (
	"context"
	"contractaudit/internal/accounts"
	modelapi "contractaudit/internal/model"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type distillPolicyTestModel struct {
	*frozenOCRModel
	mu          sync.Mutex
	calls       int
	payload     experienceDistillWork
	instruction string
}

func (m *distillPolicyTestModel) Generate(_ context.Context, messages []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	if len(messages) != 2 {
		return nil, errors.New("missing distillation prompt")
	}
	var work experienceDistillWork
	if err := json.Unmarshal([]byte(messages[1].Content), &work); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.calls++
	m.payload = work
	m.instruction = messages[0].Content
	m.mu.Unlock()
	content := completeCandidateContent(time.Now().UTC())
	for _, policy := range work.Input.PolicyContext.RequiredPolicies {
		content.PolicyRefs = append(content.PolicyRefs, policy.Ref)
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}
	return &schema.Message{Role: schema.Assistant, Content: string(raw)}, nil
}

type uncertainExperienceDelivery struct {
	core   *Store
	commit bool
}

func (a uncertainExperienceDelivery) AcceptExperienceResult(ctx context.Context, delivery ExperienceResultDelivery) error {
	if a.commit {
		if err := a.core.AcceptExperienceResult(ctx, delivery); err != nil {
			return err
		}
	}
	return errors.New("delivery response was lost")
}

type canceledExperienceDelivery struct {
	core   *Store
	cancel context.CancelFunc
}

func (a canceledExperienceDelivery) AcceptExperienceResult(ctx context.Context, delivery ExperienceResultDelivery) error {
	if err := a.core.AcceptExperienceResult(ctx, delivery); err != nil {
		return err
	}
	a.cancel()
	return context.Canceled
}

func TestDistillationFreezesSourcePolicyAndReconcilesLostResponse(t *testing.T) {
	st, user, source, _, _, policy := feedbackGovernedFixture(t, "false_positive")
	ctx := context.Background()
	feedback, err := st.SubmitExperienceFeedback(ctx, user, source)
	if err != nil {
		t.Fatal(err)
	}
	model := &distillPolicyTestModel{frozenOCRModel: &frozenOCRModel{}}
	st.UseModels(model)
	st.UseExperienceResultAcceptor(uncertainExperienceDelivery{core: st, commit: true})
	request := ExperienceDistillRequest{CaseID: source.CaseID, FeedbackID: feedback.FeedbackID, IdempotencyKey: "lost-response"}
	receipt, err := st.RequestExperienceDistillation(ctx, user, request)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimNextJob(ctx, "distill-worker", time.Minute)
	if err != nil || claim.JobID != receipt.JobID {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	if err := st.RunClaimedJob(ctx, claim); err != nil {
		t.Fatal(err)
	}
	status, err := st.GetExperienceDistillation(ctx, user, source.CaseID, feedback.FeedbackID, receipt.JobID)
	if err != nil || status.State != "completed" || status.CandidateID == "" {
		t.Fatalf("lost acknowledgment hid accepted candidate: %+v %v", status, err)
	}
	candidate, err := st.GetExperienceCandidate(ctx, user, source.CaseID, status.CandidateID, status.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.ApprovalReady(time.Now().UTC()); err != nil {
		t.Fatalf("candidate incomplete: %v", err)
	}
	model.mu.Lock()
	calls, work, instruction := model.calls, model.payload, model.instruction
	model.mu.Unlock()
	if calls != 1 || work.Input.PolicyContext.RunID != source.RunID || work.Input.PolicyContext.AttemptID != source.AttemptID ||
		work.Input.PolicyContext.KnowledgeSnapshotSHA256 == "" || work.Input.PolicyContext.ReviewInputSHA256 == "" ||
		len(work.Input.PolicyContext.RequiredPolicies) != 1 {
		t.Fatalf("model did not receive the frozen source: calls=%d context=%+v", calls, work.Input.PolicyContext)
	}
	if !strings.Contains(instruction, "只能选择 input.policy_context.required_policies") {
		t.Fatal("model was not instructed to select only frozen policy references")
	}
	frozenPolicy := work.Input.PolicyContext.RequiredPolicies[0]
	if frozenPolicy.Ref != (ExperiencePolicyRef{KnowledgeID: policy.Ref.KnowledgeID, VersionID: policy.Ref.VersionID, ContentSHA256: policy.Ref.ContentSHA256}) ||
		frozenPolicy.Summary != policy.Content.Summary || frozenPolicy.ValidFrom != policy.Content.ValidFrom || frozenPolicy.ValidUntil != policy.Content.ValidUntil ||
		!reflect.DeepEqual(candidate.Content.PolicyRefs, []ExperiencePolicyRef{frozenPolicy.Ref}) {
		t.Fatalf("candidate did not retain the exact frozen policy: %+v %+v", frozenPolicy, candidate.Content.PolicyRefs)
	}
	if err := st.AcceptExperienceResult(ctx, ExperienceResultDelivery{TenantID: feedback.TenantID, JobID: claim.JobID,
		Generation: claim.Generation, InputSHA256: candidate.InputSHA256, ResultSHA256: candidate.ResultSHA256}); err != nil {
		t.Fatalf("accepted receipt could not be replayed: %v", err)
	}
	st.UseModels(nil)
	replayed, err := st.RequestExperienceDistillation(ctx, user, request)
	if err != nil || replayed != receipt {
		t.Fatalf("request did not replay its accepted receipt: %+v %v", replayed, err)
	}
}

func TestDistillationKeepsHistoricalPolicyAfterWithdrawal(t *testing.T) {
	st, user, source, _, _, policy := feedbackGovernedFixture(t, "false_positive")
	ctx := context.Background()
	feedback, err := st.SubmitExperienceFeedback(ctx, user, source)
	if err != nil {
		t.Fatal(err)
	}
	generator := &distillPolicyTestModel{frozenOCRModel: &frozenOCRModel{}}
	st.UseModels(generator)
	st.UseExperienceResultAcceptor(st)
	receipt, err := st.RequestExperienceDistillation(ctx, user, ExperienceDistillRequest{
		CaseID: source.CaseID, FeedbackID: feedback.FeedbackID, IdempotencyKey: "historical-policy",
	})
	if err != nil {
		t.Fatal(err)
	}
	advanceKnowledge(t, st, user, policy, "withdraw", "withdraw-after-distillation-request")
	claim, err := st.ClaimNextJob(ctx, "historical-policy-worker", time.Minute)
	if err != nil || claim.JobID != receipt.JobID {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	if err := st.RunClaimedJob(ctx, claim); err != nil {
		t.Fatal(err)
	}
	status, err := st.GetExperienceDistillation(ctx, user, source.CaseID, feedback.FeedbackID, receipt.JobID)
	if err != nil || status.State != "completed" {
		t.Fatalf("draft was not retained: %+v %v", status, err)
	}
	candidate, err := st.GetExperienceCandidate(ctx, user, source.CaseID, status.CandidateID, status.VersionID)
	if err != nil || len(candidate.Content.PolicyRefs) != 1 || candidate.Content.PolicyRefs[0].VersionID != policy.Ref.VersionID {
		t.Fatalf("historical policy was replaced by a current pointer: %+v %v", candidate, err)
	}
}

func expireDistillClaim(t *testing.T, st *Store, user accounts.User, feedback ExperienceFeedback, claim JobClaim) {
	t.Helper()
	ctx := context.Background()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	scope := userScope(user)
	scope.OrgID, scope.CaseID = feedback.OrgID, feedback.CaseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE worker_jobs SET lease_until=clock_timestamp()-interval '1 second',
		ready_at=clock_timestamp()-interval '1 second' WHERE id=$1 AND generation=$2`, claim.JobID, claim.Generation); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDistillationRedeliversPersistedResultWithoutAnotherModelCall(t *testing.T) {
	st, user, source, _ := feedbackFixture(t, "false_positive")
	ctx := context.Background()
	feedback, err := st.SubmitExperienceFeedback(ctx, user, source)
	if err != nil {
		t.Fatal(err)
	}
	model := &distillPolicyTestModel{frozenOCRModel: &frozenOCRModel{}}
	st.UseModels(model)
	st.UseExperienceResultAcceptor(uncertainExperienceDelivery{core: st})
	receipt, err := st.RequestExperienceDistillation(ctx, user, ExperienceDistillRequest{
		CaseID: source.CaseID, FeedbackID: feedback.FeedbackID, IdempotencyKey: "retry-delivery",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.ClaimNextJob(ctx, "first-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RunClaimedJob(ctx, first); err == nil {
		t.Fatal("ambiguous delivery unexpectedly succeeded")
	}
	status, err := st.GetExperienceDistillation(ctx, user, source.CaseID, feedback.FeedbackID, receipt.JobID)
	if err != nil || status.State != "running" || status.CandidateID != "" {
		t.Fatalf("unknown delivery was reported as a failure: %+v %v", status, err)
	}
	expireDistillClaim(t, st, user, feedback, first)
	st.UseExperienceResultAcceptor(st)
	second, err := st.ClaimNextJob(ctx, "second-worker", time.Minute)
	if err != nil || second.JobID != first.JobID || second.Generation <= first.Generation {
		t.Fatalf("new claim: %+v %v", second, err)
	}
	if err := st.RunClaimedJob(ctx, second); err != nil {
		t.Fatal(err)
	}
	status, err = st.GetExperienceDistillation(ctx, user, source.CaseID, feedback.FeedbackID, receipt.JobID)
	if err != nil || status.State != "completed" || status.CandidateID == "" {
		t.Fatalf("persisted result was not accepted: %+v %v", status, err)
	}
	model.mu.Lock()
	calls := model.calls
	model.mu.Unlock()
	if calls != 1 {
		t.Fatalf("redelivery made %d model calls", calls)
	}
	candidate, err := st.GetExperienceCandidate(ctx, user, source.CaseID, status.CandidateID, status.VersionID)
	if err != nil || candidate.Generation != second.Generation {
		t.Fatalf("wrong fenced candidate: %+v %v", candidate, err)
	}
}

func TestDistillationRecoversAcceptedPreviousGeneration(t *testing.T) {
	st, user, source, _ := feedbackFixture(t, "false_positive")
	ctx := context.Background()
	feedback, err := st.SubmitExperienceFeedback(ctx, user, source)
	if err != nil {
		t.Fatal(err)
	}
	generator := &distillPolicyTestModel{frozenOCRModel: &frozenOCRModel{}}
	st.UseModels(generator)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	st.UseExperienceResultAcceptor(canceledExperienceDelivery{core: st, cancel: cancel})
	receipt, err := st.RequestExperienceDistillation(ctx, user, ExperienceDistillRequest{
		CaseID: source.CaseID, FeedbackID: feedback.FeedbackID, IdempotencyKey: "accepted-before-crash",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.ClaimNextJob(ctx, "accepted-first", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RunClaimedJob(workerCtx, first); err == nil {
		t.Fatal("canceled acknowledgment unexpectedly succeeded")
	}
	initial, err := st.GetExperienceDistillation(ctx, user, source.CaseID, feedback.FeedbackID, receipt.JobID)
	if err != nil || initial.CandidateID == "" || initial.State != "running" {
		t.Fatalf("committed candidate missing: %+v %v", initial, err)
	}
	expireDistillClaim(t, st, user, feedback, first)
	st.UseExperienceResultAcceptor(st)
	second, err := st.ClaimNextJob(ctx, "accepted-second", time.Minute)
	if err != nil || second.JobID != first.JobID || second.Generation <= first.Generation {
		t.Fatalf("claim: %+v %v", second, err)
	}
	if err := st.RunClaimedJob(ctx, second); err != nil {
		t.Fatal(err)
	}
	status, err := st.GetExperienceDistillation(ctx, user, source.CaseID, feedback.FeedbackID, receipt.JobID)
	if err != nil || status.State != "completed" || status.CandidateID != initial.CandidateID || status.VersionID != initial.VersionID {
		t.Fatalf("recovery replaced or hid the accepted candidate: initial=%+v status=%+v err=%v", initial, status, err)
	}
	candidate, err := st.GetExperienceCandidate(ctx, user, source.CaseID, status.CandidateID, status.VersionID)
	if err != nil || candidate.Generation != first.Generation {
		t.Fatalf("recovery changed candidate generation: %+v %v", candidate, err)
	}
	generator.mu.Lock()
	calls := generator.calls
	generator.mu.Unlock()
	if calls != 1 {
		t.Fatalf("accepted receipt recovery made %d model calls", calls)
	}
}

func TestDistillationStopsAfterUncertainModelDispatch(t *testing.T) {
	for _, mode := range []string{"unsettled", "charged", "refunded"} {
		t.Run(mode, func(t *testing.T) {
			st, user, source, _ := feedbackFixture(t, "false_positive")
			ctx := context.Background()
			feedback, err := st.SubmitExperienceFeedback(ctx, user, source)
			if err != nil {
				t.Fatal(err)
			}
			generator := &distillPolicyTestModel{frozenOCRModel: &frozenOCRModel{}}
			st.UseModels(generator)
			st.UseExperienceResultAcceptor(st)
			receipt, err := st.RequestExperienceDistillation(ctx, user, ExperienceDistillRequest{
				CaseID: source.CaseID, FeedbackID: feedback.FeedbackID, IdempotencyKey: "ledger-recovery-" + mode,
			})
			if err != nil {
				t.Fatal(err)
			}
			first, err := st.ClaimNextJob(ctx, "ledger-first", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			work, err := st.loadExperienceDistillWork(ctx, first)
			if err != nil {
				t.Fatal(err)
			}
			scope := modelapi.DelegatedScope{Kind: "experience_distill", TenantID: first.TenantID, OrgID: first.OrgID,
				CaseID: first.CaseID, ActorID: first.ActorID, JobID: first.JobID, WorkerID: first.WorkerID,
				Generation: first.Generation, InputSHA256: work.InputSHA256}
			lease, err := st.ReserveModelCall(ctx, "worker", scope, generator.Name(), 1, 20, 20, modelBudgetTestLimits())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "charged" {
				if err := st.FinishModelCall(ctx, lease, ModelCallResult{Status: "completed"}); err != nil {
					t.Fatal(err)
				}
			} else if mode == "refunded" {
				settlePredispatch(t, ctx, st, lease)
			}
			expireDistillClaim(t, st, user, feedback, first)
			second, err := st.ClaimNextJob(ctx, "ledger-second", time.Minute)
			if err != nil || second.JobID != first.JobID || second.Generation <= first.Generation {
				t.Fatalf("claim: %+v %v", second, err)
			}
			runErr := st.RunClaimedJob(ctx, second)
			status, err := st.GetExperienceDistillation(ctx, user, source.CaseID, feedback.FeedbackID, receipt.JobID)
			if err != nil {
				t.Fatal(err)
			}
			generator.mu.Lock()
			calls := generator.calls
			generator.mu.Unlock()
			if mode == "refunded" {
				if runErr != nil || status.State != "completed" || status.CandidateID == "" || calls != 1 {
					t.Fatalf("proven pre-dispatch refund did not permit retry: status=%+v calls=%d err=%v", status, calls, runErr)
				}
			} else if runErr == nil || status.State != "failed" || status.FailureCode != "experience_model_outcome_unknown" || calls != 0 {
				t.Fatalf("uncertain model call was dispatched again: status=%+v calls=%d err=%v", status, calls, runErr)
			}
		})
	}
}

func TestDistillationRejectsForgedSourceAndPolicyReferences(t *testing.T) {
	st, user, source, _ := feedbackFixture(t, "false_positive")
	feedback, err := st.SubmitExperienceFeedback(context.Background(), user, source)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, test := range []struct {
		name   string
		change func(*ExperienceDistillInput, *ExperienceCandidateContent)
	}{
		{"different policy", func(_ *ExperienceDistillInput, c *ExperienceCandidateContent) {
			c.PolicyRefs[0].KnowledgeID = "invented-policy"
		}},
		{"invented version", func(_ *ExperienceDistillInput, c *ExperienceCandidateContent) {
			c.PolicyRefs[0].VersionID = sha256Bytes([]byte("invented-version"))
		}},
		{"invented digest", func(_ *ExperienceDistillInput, c *ExperienceCandidateContent) {
			c.PolicyRefs[0].ContentSHA256 = sha256Bytes([]byte("invented-content"))
		}},
		{"duplicate policy", func(_ *ExperienceDistillInput, c *ExperienceCandidateContent) {
			c.PolicyRefs = append(c.PolicyRefs, c.PolicyRefs[0])
		}},
		{"outside policy period", func(_ *ExperienceDistillInput, c *ExperienceCandidateContent) { c.ValidUntil = "2200-01-01T00:00:00Z" }},
		{"forged source digest", func(i *ExperienceDistillInput, _ *ExperienceCandidateContent) {
			i.PolicyContext.KnowledgeSnapshotSHA256 = sha256Bytes([]byte("different-snapshot"))
		}},
		{"forged review digest", func(i *ExperienceDistillInput, _ *ExperienceCandidateContent) {
			i.PolicyContext.ReviewInputSHA256 = sha256Bytes([]byte("different-review"))
		}},
		{"forged summary", func(i *ExperienceDistillInput, _ *ExperienceCandidateContent) {
			i.PolicyContext.RequiredPolicies[0].Summary = "伪造制度"
		}},
		{"missing governed source", func(i *ExperienceDistillInput, _ *ExperienceCandidateContent) {
			i.PolicyContext = ExperienceDistillPolicyContext{}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := feedbackDistillInput(t, st, user, feedback, "candidate-"+test.name, "")
			content := candidateContentForFeedback(t, st, user, feedback, now)
			test.change(&input, &content)
			delivery := createFeedbackDelivery(t, st, user, feedback, input, content, now.Add(time.Hour))
			_, err := acceptDelivery(st, user, feedback, delivery)
			requireFeedbackFault(t, err, 409)
		})
	}
}

func TestDistillationRejectsResealedSnapshotNotInAttemptInput(t *testing.T) {
	st, user, source, _ := feedbackFixture(t, "false_positive")
	ctx := context.Background()
	feedback, err := st.SubmitExperienceFeedback(ctx, user, source)
	if err != nil {
		t.Fatal(err)
	}
	ws := readWorkspace(t, st, user, source.CaseID, source.RunID)
	if ws.Snapshot.GovernedKnowledge == nil {
		t.Fatal("fixture lacks governed snapshot")
	}
	tampered := *ws.Snapshot.GovernedKnowledge
	tampered.RequiredPolicies = append([]GovernedKnowledgeSelection(nil), tampered.RequiredPolicies...)
	tampered.RequiredPolicies[0].Summary = "被替换的政策摘要"
	tampered, err = sealKnowledgeSnapshot(tampered)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(ctx, `ALTER TABLE review_run_snapshots DISABLE TRIGGER review_run_snapshots_append_only`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.pool.Exec(ctx, `ALTER TABLE review_run_snapshots ENABLE TRIGGER review_run_snapshots_append_only`)
	}()
	if _, err := st.pool.Exec(ctx, `UPDATE review_run_snapshots SET governed_knowledge=$2 WHERE run_id=$1`, source.RunID, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(ctx, `ALTER TABLE review_run_snapshots ENABLE TRIGGER review_run_snapshots_append_only`); err != nil {
		t.Fatal(err)
	}
	_, err = st.RequestExperienceDistillation(ctx, user, ExperienceDistillRequest{CaseID: source.CaseID, FeedbackID: feedback.FeedbackID, IdempotencyKey: "tampered-snapshot"})
	requireFeedbackFault(t, err, 409)
}

func TestLegacyFeedbackCannotInventPolicyContext(t *testing.T) {
	st, user := openExecutionStore(t)
	st.UseModels(&frozenOCRModel{})
	ctx := context.Background()
	caseID := createExecutionCase(t, st, user)
	runID, err := st.StartReview(ctx, user, caseID)
	if err != nil {
		t.Fatal(err)
	}
	ws := waitTerminalWorkspace(t, st, user, caseID, runID)
	if len(ws.Findings) == 0 || ws.Execution == nil || ws.Execution.Attempt == nil {
		t.Fatal("legacy review has no finding")
	}
	finding := ws.Findings[0]
	if err := st.Decide(user, caseID, runID, finding.ID, "legacy-decision", "false_positive", "按证据确认", finding.Version); err != nil {
		t.Fatal(err)
	}
	decisions := st.DecisionsFor(finding.ID)
	if len(decisions) != 1 {
		t.Fatal("legacy decision missing")
	}
	var ref ExperienceEvidenceRef
	for _, doc := range ws.Documents {
		if doc.Ready && doc.ParsedDocument != nil && doc.Text != "" {
			ref = ExperienceEvidenceRef{DocumentID: doc.Document.ID, ParsedDocumentID: doc.ParsedDocument.ID,
				NormalizedDigest: doc.ParsedDocument.NormalizedDigest, Quote: doc.Text, Start: 0, End: len([]rune(doc.Text))}
			break
		}
	}
	feedback, err := st.SubmitExperienceFeedback(ctx, user, ExperienceFeedbackInput{CaseID: caseID, RunID: runID,
		AttemptID: ws.Execution.Attempt.ID, FindingID: finding.ID, DecisionID: decisions[0].ID, Reason: "根据冻结材料核验",
		EvidenceRefs: []ExperienceEvidenceRef{ref}, IdempotencyKey: "legacy-feedback"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.RequestExperienceDistillation(ctx, user, ExperienceDistillRequest{CaseID: caseID, FeedbackID: feedback.FeedbackID, IdempotencyKey: "legacy-distill"})
	requireFeedbackFault(t, err, 409)
}

var _ ExperienceResultAcceptor = uncertainExperienceDelivery{}
