package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func feedbackFixture(t *testing.T, action string) (*Store, accounts.User, ExperienceFeedbackInput, Decision) {
	t.Helper()
	st, user, input, decision, _, _ := feedbackGovernedFixture(t, action)
	return st, user, input, decision
}

// Every candidate fixture starts from an actual review accepted under an
// approved, published policy; no post-hoc snapshot or policy is invented.
func feedbackGovernedFixture(t *testing.T, action string) (*Store, accounts.User, ExperienceFeedbackInput, Decision, RulePackDetail, GovernedKnowledgeVersion) {
	t.Helper()
	st, owner := openExecutionStore(t)
	st.UseModels(&frozenOCRModel{})
	pack := publishSyntheticAcceptancePack(t, st, owner)
	policy := publishSyntheticAcceptancePolicy(t, st, owner, pack)
	input := createGovernedFeedbackSource(t, st, owner, owner, action, "feedback-key")
	decisions := st.DecisionsFor(input.FindingID)
	if len(decisions) != 1 {
		t.Fatalf("expected one decision, got %+v", decisions)
	}
	return st, owner, input, decisions[0], pack, policy
}

func createGovernedFeedbackSource(t *testing.T, st *Store, owner, applicant accounts.User, action, key string) ExperienceFeedbackInput {
	t.Helper()
	caseID := governanceTestCase(t, st, owner)
	for role, body := range map[string]string{
		"procurement":    "采购文件：电路 LINE-001：最低保障带宽 100 Mbit/s。合同总额 100万元。",
		"draft_contract": "拟签合同：电路 LINE-001：最低保障带宽 80 Mbit/s。合同总额 120万元。",
	} {
		if _, err := st.AddDocument(context.Background(), owner, caseID, role, role+".txt", "text/plain", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	runID, err := st.StartReviewAt(context.Background(), owner, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	ws := waitTerminalWorkspace(t, st, owner, caseID, runID)
	if len(ws.Findings) == 0 || ws.Execution == nil || ws.Execution.Attempt == nil {
		t.Fatal("fixture needs a finding and a frozen governed attempt")
	}
	finding := ws.Findings[0]
	if err := st.Decide(owner, caseID, runID, finding.ID, key+"-decision", action, "按证据确认人工决定", finding.Version); err != nil {
		t.Fatal(err)
	}
	decisions := st.DecisionsFor(finding.ID)
	if len(decisions) != 1 {
		t.Fatalf("expected one decision, got %+v", decisions)
	}
	var ref ExperienceEvidenceRef
	for _, document := range ws.Documents {
		if document.Ready && document.ParsedDocument != nil && document.Text != "" {
			ref = ExperienceEvidenceRef{
				DocumentID: document.Document.ID, ParsedDocumentID: document.ParsedDocument.ID,
				NormalizedDigest: document.ParsedDocument.NormalizedDigest, Quote: document.Text,
				Start: 0, End: len([]rune(document.Text)),
			}
			break
		}
	}
	if ref.DocumentID == "" {
		t.Fatal("fixture needs a located frozen document")
	}
	if applicant.ID != owner.ID {
		if err := st.AddMember(owner, caseID, applicant.ID); err != nil {
			t.Fatal(err)
		}
	}
	return ExperienceFeedbackInput{
		CaseID: caseID, RunID: runID, AttemptID: ws.Execution.Attempt.ID,
		FindingID: finding.ID, DecisionID: decisions[0].ID, Reason: "付款应结合已完成验收核验",
		EvidenceRefs: []ExperienceEvidenceRef{ref}, IdempotencyKey: key,
	}
}

func requireFeedbackFault(t *testing.T, err error, status int) {
	t.Helper()
	var problem *fault.Error
	if !errors.As(err, &problem) || problem.Status != status {
		t.Fatalf("expected HTTP status %d, got %v", status, err)
	}
}

func TestFeedbackPreservesDecisionAndReturnsSameReceiptOnReplay(t *testing.T) {
	st, user, input, decision := feedbackFixture(t, "accept_residual")
	ctx := context.Background()
	first, err := st.SubmitExperienceFeedback(ctx, user, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.SubmitExperienceFeedback(ctx, user, input)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("replay changed the receipt: first=%+v second=%+v err=%v", first, second, err)
	}
	if first.FeedbackID == "" || first.ApplicantID != user.ID || first.DecisionID != decision.ID ||
		!reflect.DeepEqual(first.EvidenceRefs, input.EvidenceRefs) {
		t.Fatalf("feedback lost its authoritative source: %+v", first)
	}
	read, err := st.GetExperienceFeedback(ctx, user, input.CaseID, first.FeedbackID)
	if err != nil || !reflect.DeepEqual(first, read) {
		t.Fatalf("exact feedback read: got=%+v err=%v", read, err)
	}
	if got := st.DecisionsFor(input.FindingID); len(got) != 1 || !reflect.DeepEqual(got[0], decision) || got[0].NewDisposition != "accepted_residual" {
		t.Fatalf("feedback altered the original residual-risk decision: %+v", got)
	}
	input.Reason = "另一条不同的纠正理由"
	_, err = st.SubmitExperienceFeedback(ctx, user, input)
	requireFeedbackFault(t, err, 409)
}

func TestFeedbackRejectsMismatchedIdentityAndUnfrozenEvidence(t *testing.T) {
	st, user, valid, _ := feedbackFixture(t, "false_positive")
	for name, change := range map[string]func(*ExperienceFeedbackInput){
		"run":      func(input *ExperienceFeedbackInput) { input.RunID = "other-run" },
		"attempt":  func(input *ExperienceFeedbackInput) { input.AttemptID = "other-attempt" },
		"finding":  func(input *ExperienceFeedbackInput) { input.FindingID = "other-finding" },
		"decision": func(input *ExperienceFeedbackInput) { input.DecisionID = "other-decision" },
	} {
		t.Run(name, func(t *testing.T) {
			input := valid
			change(&input)
			_, err := st.SubmitExperienceFeedback(context.Background(), user, input)
			requireFeedbackFault(t, err, 404)
		})
	}
	for name, change := range map[string]func(*ExperienceEvidenceRef){
		"document": func(ref *ExperienceEvidenceRef) { ref.DocumentID = "other-document" },
		"parse":    func(ref *ExperienceEvidenceRef) { ref.ParsedDocumentID = "other-parse" },
		"digest":   func(ref *ExperienceEvidenceRef) { ref.NormalizedDigest = "bad-digest" },
		"quote":    func(ref *ExperienceEvidenceRef) { ref.Quote = "并非正文" },
		"span":     func(ref *ExperienceEvidenceRef) { ref.End-- },
	} {
		t.Run(name, func(t *testing.T) {
			input := valid
			input.EvidenceRefs = append([]ExperienceEvidenceRef(nil), valid.EvidenceRefs...)
			change(&input.EvidenceRefs[0])
			_, err := st.SubmitExperienceFeedback(context.Background(), user, input)
			requireFeedbackFault(t, err, 422)
		})
	}
}

type feedbackDelivery struct {
	jobID      string
	generation int64
	inputSHA   string
	resultSHA  string
}

func completeCandidateContent(now time.Time) ExperienceCandidateContent {
	return ExperienceCandidateContent{
		SchemaVersion:   "experience-candidate/v1",
		Applicability:   []string{"付款发生在完成验收之后"},
		Exclusions:      []string{"付款发生在验收之前"},
		CheckAction:     "核对本案的验收日期和付款日期",
		MinimumEvidence: []string{"本案验收记录", "本案付款条款"},
		Counterexamples: []string{"验收前付款仍需提示风险"},
		ValidFrom:       now.Add(-time.Hour).UTC().Format(time.RFC3339),
		ValidUntil:      now.Add(24 * time.Hour).UTC().Format(time.RFC3339),
	}
}

func candidateContentForFeedback(t *testing.T, st *Store, user accounts.User, feedback ExperienceFeedback, now time.Time) ExperienceCandidateContent {
	t.Helper()
	input := feedbackDistillInput(t, st, user, feedback, "candidate-content", "")
	content := completeCandidateContent(now)
	for _, policy := range input.PolicyContext.RequiredPolicies {
		content.PolicyRefs = append(content.PolicyRefs, policy.Ref)
	}
	return content
}

// The test installs a real independently leased worker job and its immutable
// result. Acceptance goes through the production locked-result boundary.
func createFeedbackDelivery(t *testing.T, st *Store, user accounts.User, feedback ExperienceFeedback, input ExperienceDistillInput, content ExperienceCandidateContent, leaseUntil time.Time, overrides ...feedbackDelivery) feedbackDelivery {
	t.Helper()
	ctx := context.Background()
	jobID, err := newID("experience-test-job_")
	if err != nil {
		t.Fatal(err)
	}
	inputBytes, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	resultBytes, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	delivery := feedbackDelivery{jobID: jobID, generation: 3, inputSHA: sha256Bytes(inputBytes), resultSHA: sha256Bytes(resultBytes)}
	if len(overrides) > 0 {
		if overrides[0].inputSHA != "" {
			delivery.inputSHA = overrides[0].inputSHA
		}
		if overrides[0].resultSHA != "" {
			delivery.resultSHA = overrides[0].resultSHA
		}
	}
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	scope := userScope(user)
	scope.CaseID, scope.OrgID = feedback.CaseID, feedback.OrgID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO worker_jobs
		(id,tenant_id,org_id,case_id,run_id,attempt_id,actor_id,kind,state,generation,worker_id,lease_until,created_at,updated_at)
		VALUES ($1,$2,$3,$4,NULL,NULL,$5,'experience_distill','running',$6,'test-worker',$7,$8,$8)`,
		jobID, feedback.TenantID, feedback.OrgID, feedback.CaseID, user.ID, delivery.generation, leaseUntil, stamp()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO experience_distill_inputs
		(job_id,tenant_id,org_id,case_id,actor_id,feedback_id,idempotency_key,request_sha256,input_sha256,input_bytes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, jobID, feedback.TenantID, feedback.OrgID, feedback.CaseID,
		user.ID, feedback.FeedbackID, jobID, sha256Bytes([]byte(jobID)), delivery.inputSHA, inputBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO experience_distill_results
		(job_id,generation,tenant_id,org_id,case_id,input_sha256,result_sha256,result_bytes,model_ref,template_ref)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'model/test','template/test')`, jobID, delivery.generation,
		feedback.TenantID, feedback.OrgID, feedback.CaseID, delivery.inputSHA, delivery.resultSHA, resultBytes); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return delivery
}

func feedbackDistillInput(t *testing.T, st *Store, user accounts.User, feedback ExperienceFeedback, candidateID, baseVersionID string) ExperienceDistillInput {
	t.Helper()
	ctx := context.Background()
	tx, _, item, err := st.beginExperienceCase(ctx, user, feedback.CaseID, access.Review)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	policyContext, err := experiencePolicyContext(ctx, tx, item, feedback)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return ExperienceDistillInput{
		SchemaVersion: "experience-distill-input/v1", TenantID: feedback.TenantID, OrgID: feedback.OrgID,
		CaseID: feedback.CaseID, FeedbackID: feedback.FeedbackID, DecisionID: feedback.DecisionID,
		ApplicantID: feedback.ApplicantID, CandidateID: candidateID, BaseVersionID: baseVersionID,
		ModelConfigurationID: "model-config/test", TemplateRef: "template/test", PolicyContext: policyContext,
	}
}

func acceptDelivery(st *Store, user accounts.User, feedback ExperienceFeedback, delivery feedbackDelivery) (ExperienceCandidate, error) {
	return st.AcceptExperienceCandidate(context.Background(), user, feedback.CaseID, feedback.FeedbackID,
		delivery.jobID, delivery.generation, delivery.inputSHA, delivery.resultSHA)
}

func changeFeedbackJobState(t *testing.T, st *Store, user accounts.User, feedback ExperienceFeedback, jobID, state string) {
	t.Helper()
	ctx := context.Background()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	scope := userScope(user)
	scope.CaseID, scope.OrgID = feedback.CaseID, feedback.OrgID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE worker_jobs SET state=$1 WHERE id=$2 AND tenant_id=$3`, state, jobID, feedback.TenantID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCandidateUsesLeasedResultAndReplaysAfterJobCompletion(t *testing.T) {
	st, user, input, decision := feedbackFixture(t, "accept_residual")
	feedback, err := st.SubmitExperienceFeedback(context.Background(), user, input)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	content := candidateContentForFeedback(t, st, user, feedback, now)
	delivery := createFeedbackDelivery(t, st, user, feedback, feedbackDistillInput(t, st, user, feedback, "candidate-1", ""), content, now.Add(time.Hour))
	candidate, err := acceptDelivery(st, user, feedback, delivery)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.JobID != delivery.jobID || candidate.Generation != delivery.generation || candidate.FeedbackID != feedback.FeedbackID ||
		candidate.ContentSHA256 != delivery.resultSHA || candidate.ResultSHA256 != delivery.resultSHA ||
		candidate.ModelRef != "model/test" || candidate.TemplateRef != "template/test" || !reflect.DeepEqual(candidate.Content, content) {
		t.Fatalf("candidate did not retain the real result and provenance: %+v", candidate)
	}
	if err := candidate.ApprovalReady(now); err != nil {
		t.Fatalf("complete candidate could not be reviewed: %v", err)
	}
	changeFeedbackJobState(t, st, user, feedback, delivery.jobID, "completed")
	replay, err := acceptDelivery(st, user, feedback, delivery)
	if err != nil || !reflect.DeepEqual(candidate, replay) {
		t.Fatalf("completed delivery did not replay the receipt: candidate=%+v replay=%+v err=%v", candidate, replay, err)
	}
	wrong := delivery
	wrong.resultSHA = sha256Bytes([]byte("different result"))
	_, err = acceptDelivery(st, user, feedback, wrong)
	requireFeedbackFault(t, err, 409)
	read, err := st.GetExperienceCandidate(context.Background(), user, feedback.CaseID, candidate.CandidateID, candidate.VersionID)
	if err != nil || !reflect.DeepEqual(read, candidate) {
		t.Fatalf("exact candidate read: got=%+v err=%v", read, err)
	}
	foreign, ok := accounts.ByID("demo-tenant2-admin")
	if !ok {
		t.Fatal("second tenant missing")
	}
	_, err = st.GetExperienceCandidate(context.Background(), foreign, feedback.CaseID, candidate.CandidateID, candidate.VersionID)
	requireFeedbackFault(t, err, 404)
	reopened, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	persisted, err := reopened.GetExperienceCandidate(context.Background(), user, feedback.CaseID, candidate.CandidateID, candidate.VersionID)
	if err != nil || !reflect.DeepEqual(persisted, candidate) {
		t.Fatalf("candidate did not survive reopening: got=%+v err=%v", persisted, err)
	}
	if got := st.DecisionsFor(input.FindingID); len(got) != 1 || !reflect.DeepEqual(got[0], decision) || got[0].NewDisposition != "accepted_residual" {
		t.Fatalf("distillation converted or rewrote residual risk: %+v", got)
	}
}

func TestCandidateRejectsExpiredOrWrongJobAndSource(t *testing.T) {
	st, user, input, _ := feedbackFixture(t, "false_positive")
	feedback, err := st.SubmitExperienceFeedback(context.Background(), user, input)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	content := candidateContentForFeedback(t, st, user, feedback, now)
	for _, test := range []struct {
		name       string
		input      ExperienceDistillInput
		leaseUntil time.Time
		override   feedbackDelivery
		change     func(*feedbackDelivery)
	}{
		{name: "expired lease", input: feedbackDistillInput(t, st, user, feedback, "expired", ""), leaseUntil: now.Add(-time.Minute)},
		{name: "stale generation", input: feedbackDistillInput(t, st, user, feedback, "stale", ""), leaseUntil: now.Add(time.Hour), change: func(d *feedbackDelivery) { d.generation++ }},
		{name: "wrong result digest", input: feedbackDistillInput(t, st, user, feedback, "wrong-result", ""), leaseUntil: now.Add(time.Hour), change: func(d *feedbackDelivery) { d.resultSHA = sha256Bytes([]byte("other result")) }},
		{name: "wrong decision in task input", input: func() ExperienceDistillInput {
			value := feedbackDistillInput(t, st, user, feedback, "wrong-decision", "")
			value.DecisionID = "other-decision"
			return value
		}(), leaseUntil: now.Add(time.Hour)},
		{name: "missing model configuration", input: func() ExperienceDistillInput {
			value := feedbackDistillInput(t, st, user, feedback, "missing-model-config", "")
			value.ModelConfigurationID = ""
			return value
		}(), leaseUntil: now.Add(time.Hour)},
		{name: "missing template", input: func() ExperienceDistillInput {
			value := feedbackDistillInput(t, st, user, feedback, "missing-template", "")
			value.TemplateRef = ""
			return value
		}(), leaseUntil: now.Add(time.Hour)},
		{name: "different result template", input: func() ExperienceDistillInput {
			value := feedbackDistillInput(t, st, user, feedback, "different-template", "")
			value.TemplateRef = "other-template/v1"
			return value
		}(), leaseUntil: now.Add(time.Hour)},
		{name: "input bytes differ from stored digest", input: feedbackDistillInput(t, st, user, feedback, "bad-input-bytes", ""), leaseUntil: now.Add(time.Hour), override: feedbackDelivery{inputSHA: sha256Bytes([]byte("different input"))}},
		{name: "result bytes differ from stored digest", input: feedbackDistillInput(t, st, user, feedback, "bad-result-bytes", ""), leaseUntil: now.Add(time.Hour), override: feedbackDelivery{resultSHA: sha256Bytes([]byte("different result"))}},
	} {
		t.Run(test.name, func(t *testing.T) {
			delivery := createFeedbackDelivery(t, st, user, feedback, test.input, content, test.leaseUntil, test.override)
			if test.change != nil {
				test.change(&delivery)
			}
			_, err := acceptDelivery(st, user, feedback, delivery)
			requireFeedbackFault(t, err, 409)
		})
	}
}

func TestCandidateVersionsRemainImmutableAndRequireCompleteReviewFields(t *testing.T) {
	st, user, input, _ := feedbackFixture(t, "false_positive")
	feedback, err := st.SubmitExperienceFeedback(context.Background(), user, input)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	content := candidateContentForFeedback(t, st, user, feedback, now)
	firstDelivery := createFeedbackDelivery(t, st, user, feedback, feedbackDistillInput(t, st, user, feedback, "revision-candidate", ""), content, now.Add(time.Hour))
	first, err := acceptDelivery(st, user, feedback, firstDelivery)
	if err != nil {
		t.Fatal(err)
	}
	secondContent := content
	secondContent.CheckAction = "核对付款日期、验收日期和验收对象"
	secondContent.ReviewDiff = "新增验收对象的核验"
	secondDelivery := createFeedbackDelivery(t, st, user, feedback, feedbackDistillInput(t, st, user, feedback, first.CandidateID, first.VersionID), secondContent, now.Add(time.Hour))
	second, err := acceptDelivery(st, user, feedback, secondDelivery)
	if err != nil || second.VersionID == first.VersionID || second.BaseVersionID != first.VersionID || second.Content.CheckAction != secondContent.CheckAction {
		t.Fatalf("new result did not produce an immutable successor: first=%+v second=%+v err=%v", first, second, err)
	}
	read, err := st.GetExperienceCandidate(context.Background(), user, feedback.CaseID, first.CandidateID, first.VersionID)
	if err != nil || !reflect.DeepEqual(read, first) {
		t.Fatalf("older candidate changed after revision: got=%+v err=%v", read, err)
	}
	branch := createFeedbackDelivery(t, st, user, feedback, feedbackDistillInput(t, st, user, feedback, first.CandidateID, first.VersionID), secondContent, now.Add(time.Hour))
	_, err = acceptDelivery(st, user, feedback, branch)
	requireFeedbackFault(t, err, 409)
	for name, change := range map[string]func(*ExperienceCandidate){
		"applicability":    func(c *ExperienceCandidate) { c.Content.Applicability = nil },
		"exclusions":       func(c *ExperienceCandidate) { c.Content.Exclusions = nil },
		"check action":     func(c *ExperienceCandidate) { c.Content.CheckAction = "" },
		"minimum evidence": func(c *ExperienceCandidate) { c.Content.MinimumEvidence = nil },
		"counterexamples":  func(c *ExperienceCandidate) { c.Content.Counterexamples = nil },
		"expiry":           func(c *ExperienceCandidate) { c.Content.ValidUntil = "" },
		"expired":          func(c *ExperienceCandidate) { c.Content.ValidUntil = now.Add(-time.Minute).Format(time.RFC3339) },
		"policy":           func(c *ExperienceCandidate) { c.Content.PolicyRefs = nil },
		"policy digest": func(c *ExperienceCandidate) {
			c.Content.PolicyRefs = []ExperiencePolicyRef{{KnowledgeID: "policy", VersionID: sha256Bytes([]byte("version")), ContentSHA256: "bad"}}
		},
		"policy version": func(c *ExperienceCandidate) {
			c.Content.PolicyRefs = []ExperiencePolicyRef{{KnowledgeID: "policy", VersionID: "v1", ContentSHA256: sha256Bytes([]byte("policy"))}}
		},
		"revision diff": func(c *ExperienceCandidate) { c.Content.ReviewDiff = "" },
		"job":           func(c *ExperienceCandidate) { c.JobID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := second
			change(&candidate)
			if err := candidate.ApprovalReady(now); err == nil {
				t.Fatal("incomplete candidate was eligible for approval")
			}
		})
	}
}

func TestFeedbackChecksTrustedCaseAccessAndCurrentMembership(t *testing.T) {
	st, owner, input, _ := feedbackFixture(t, "confirm_risk")
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("reviewer missing")
	}
	foreign, ok := accounts.ByID("demo-tenant2-admin")
	if !ok {
		t.Fatal("second tenant missing")
	}
	_, err := st.SubmitExperienceFeedback(context.Background(), foreign, input)
	requireFeedbackFault(t, err, 404)
	forged := reviewer
	forged.Grants = owner.Grants
	_, err = st.SubmitExperienceFeedback(context.Background(), forged, input)
	requireFeedbackFault(t, err, 404)
	if err := st.AddMember(owner, input.CaseID, reviewer.ID); err != nil {
		t.Fatal(err)
	}
	feedback, err := st.SubmitExperienceFeedback(context.Background(), reviewer, input)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	delivery := createFeedbackDelivery(t, st, reviewer, feedback, feedbackDistillInput(t, st, reviewer, feedback, "revoked-candidate", ""), candidateContentForFeedback(t, st, reviewer, feedback, now), now.Add(time.Hour))
	candidate, err := acceptDelivery(st, reviewer, feedback, delivery)
	if err != nil {
		t.Fatal(err)
	}
	pending := createFeedbackDelivery(t, st, reviewer, feedback, feedbackDistillInput(t, st, reviewer, feedback, "pending-after-revocation", ""), candidateContentForFeedback(t, st, reviewer, feedback, now), now.Add(time.Hour))
	ctx := context.Background()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	scope := userScope(owner)
	scope.CaseID = input.CaseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		t.Fatal(err)
	}
	if err := scopedCaseLock(ctx, tx, input.CaseID); err != nil {
		t.Fatal(err)
	}
	members, err := json.Marshal([]string{owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE cases SET member_ids=$1 WHERE tenant_id=$2 AND id=$3`, string(members), feedback.TenantID, input.CaseID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = st.GetExperienceFeedback(ctx, reviewer, input.CaseID, feedback.FeedbackID)
	requireFeedbackFault(t, err, 404)
	_, err = st.GetExperienceCandidate(ctx, reviewer, input.CaseID, candidate.CandidateID, candidate.VersionID)
	requireFeedbackFault(t, err, 404)
	_, err = acceptDelivery(st, reviewer, feedback, delivery)
	requireFeedbackFault(t, err, 404)
	_, err = acceptDelivery(st, reviewer, feedback, pending)
	requireFeedbackFault(t, err, 404)
}

func TestConcurrentFeedbackSubmissionReturnsOneReceipt(t *testing.T) {
	st, user, input, _ := feedbackFixture(t, "false_positive")
	type outcome struct {
		feedback ExperienceFeedback
		err      error
	}
	results := make(chan outcome, 8)
	var ready sync.WaitGroup
	ready.Add(8)
	start := make(chan struct{})
	for range 8 {
		go func() {
			ready.Done()
			<-start
			feedback, err := st.SubmitExperienceFeedback(context.Background(), user, input)
			results <- outcome{feedback, err}
		}()
	}
	ready.Wait()
	close(start)
	var first ExperienceFeedback
	for i := range 8 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if i == 0 {
			first = result.feedback
		} else if !reflect.DeepEqual(first, result.feedback) {
			t.Fatalf("concurrent replay allocated different feedback: first=%+v next=%+v", first, result.feedback)
		}
	}
}

func TestFeedbackCannotCiteEvidenceAddedAfterItsAttempt(t *testing.T) {
	st, user, input, _ := feedbackFixture(t, "false_positive")
	documentID, err := st.AddDocument(context.Background(), user, input.CaseID, "draft_contract", "new-evidence.txt", "text/plain", []byte("之后新增的验收记录。"))
	if err != nil {
		t.Fatal(err)
	}
	runID, err := st.StartReviewAt(context.Background(), user, input.CaseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	workspace := waitTerminalWorkspace(t, st, user, input.CaseID, runID)
	if workspace.Execution == nil || workspace.Execution.Attempt == nil || len(workspace.Findings) == 0 {
		t.Fatal("new review did not produce its own attempt and finding")
	}
	newFinding := workspace.Findings[0]
	if err := st.Decide(user, input.CaseID, runID, newFinding.ID, "later-decision", "confirm_risk", "按新材料确认", newFinding.Version); err != nil {
		t.Fatal(err)
	}
	newDecisions := st.DecisionsFor(newFinding.ID)
	if len(newDecisions) != 1 {
		t.Fatal("new finding decision missing")
	}
	mismatched := input
	mismatched.DecisionID = newDecisions[0].ID
	_, err = st.SubmitExperienceFeedback(context.Background(), user, mismatched)
	requireFeedbackFault(t, err, 404)
	mismatched = input
	mismatched.AttemptID = workspace.Execution.Attempt.ID
	_, err = st.SubmitExperienceFeedback(context.Background(), user, mismatched)
	requireFeedbackFault(t, err, 404)
	var newer ExperienceEvidenceRef
	for _, doc := range workspace.Documents {
		if doc.Document.ID == documentID && doc.Ready && doc.ParsedDocument != nil {
			newer = ExperienceEvidenceRef{DocumentID: documentID, ParsedDocumentID: doc.ParsedDocument.ID,
				NormalizedDigest: doc.ParsedDocument.NormalizedDigest, Quote: doc.Text, Start: 0, End: len([]rune(doc.Text))}
		}
	}
	if newer.DocumentID == "" {
		t.Fatal("new review did not contain its new parsed material")
	}
	input.EvidenceRefs = []ExperienceEvidenceRef{newer}
	_, err = st.SubmitExperienceFeedback(context.Background(), user, input)
	requireFeedbackFault(t, err, 422)
}

func TestIncompleteResultIsRetainedButCannotBeApproved(t *testing.T) {
	st, user, input, _ := feedbackFixture(t, "false_positive")
	feedback, err := st.SubmitExperienceFeedback(context.Background(), user, input)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	content := candidateContentForFeedback(t, st, user, feedback, now)
	content.Counterexamples = nil
	delivery := createFeedbackDelivery(t, st, user, feedback, feedbackDistillInput(t, st, user, feedback, "incomplete-candidate", ""), content, now.Add(time.Hour))
	candidate, err := acceptDelivery(st, user, feedback, delivery)
	if err != nil {
		t.Fatal(err)
	}
	read, err := st.GetExperienceCandidate(context.Background(), user, feedback.CaseID, candidate.CandidateID, candidate.VersionID)
	if err != nil || !reflect.DeepEqual(read, candidate) || read.Content.Counterexamples != nil {
		t.Fatalf("incomplete result was not retained exactly: candidate=%+v read=%+v err=%v", candidate, read, err)
	}
	if err := read.ApprovalReady(now); err == nil {
		t.Fatal("candidate without counterexamples was eligible for approval")
	}
}

func TestFeedbackAndCandidateRowsAreAppendOnly(t *testing.T) {
	st, user, input, _ := feedbackFixture(t, "confirm_risk")
	feedback, err := st.SubmitExperienceFeedback(context.Background(), user, input)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	delivery := createFeedbackDelivery(t, st, user, feedback, feedbackDistillInput(t, st, user, feedback, "immutable-candidate", ""), candidateContentForFeedback(t, st, user, feedback, now), now.Add(time.Hour))
	candidate, err := acceptDelivery(st, user, feedback, delivery)
	if err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]string{
		"feedback update":  `UPDATE experience_feedback SET reason='rewritten' WHERE feedback_id=$1`,
		"feedback delete":  `DELETE FROM experience_feedback WHERE feedback_id=$1`,
		"candidate update": `UPDATE experience_candidate_versions SET model_ref='rewritten' WHERE candidate_id=$1`,
		"candidate delete": `DELETE FROM experience_candidate_versions WHERE candidate_id=$1`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			tx, err := st.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			scope := userScope(user)
			scope.CaseID = feedback.CaseID
			if err := setLocalScope(ctx, tx, scope); err != nil {
				t.Fatal(err)
			}
			id := feedback.FeedbackID
			if name == "candidate update" || name == "candidate delete" {
				id = candidate.CandidateID
			}
			if _, err := tx.Exec(ctx, query, id); err == nil {
				t.Fatal("immutable row accepted a rewrite or deletion")
			}
		})
	}
}

func TestCandidateRevisionRequiresAccessToEverySourceCase(t *testing.T) {
	st, owner, sourceInput, _ := feedbackFixture(t, "false_positive")
	ctx := context.Background()
	sourceFeedback, err := st.SubmitExperienceFeedback(ctx, owner, sourceInput)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	firstDelivery := createFeedbackDelivery(t, st, owner, sourceFeedback, feedbackDistillInput(t, st, owner, sourceFeedback, "shared-candidate", ""), candidateContentForFeedback(t, st, owner, sourceFeedback, now), now.Add(time.Hour))
	first, err := acceptDelivery(st, owner, sourceFeedback, firstDelivery)
	if err != nil {
		t.Fatal(err)
	}
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("reviewer missing")
	}
	secondInput := createGovernedFeedbackSource(t, st, owner, reviewer, "false_positive", "second-case-feedback")
	secondCase := secondInput.CaseID
	feedback, err := st.SubmitExperienceFeedback(ctx, reviewer, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	content := candidateContentForFeedback(t, st, reviewer, feedback, now)
	content.ReviewDiff = "根据第二案调整检查范围"
	delivery := createFeedbackDelivery(t, st, reviewer, feedback, feedbackDistillInput(t, st, reviewer, feedback, first.CandidateID, first.VersionID), content, now.Add(time.Hour))
	_, err = acceptDelivery(st, reviewer, feedback, delivery)
	requireFeedbackFault(t, err, 404)
	if err := st.AddMember(owner, sourceInput.CaseID, reviewer.ID); err != nil {
		t.Fatal(err)
	}
	second, err := acceptDelivery(st, reviewer, feedback, delivery)
	if err != nil {
		t.Fatal(err)
	}
	if second.BaseVersionID != first.VersionID {
		t.Fatalf("revision lost the source version: %+v", second)
	}
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	scope := userScope(owner)
	scope.CaseID = sourceInput.CaseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		t.Fatal(err)
	}
	if err := scopedCaseLock(ctx, tx, sourceInput.CaseID); err != nil {
		t.Fatal(err)
	}
	members, err := json.Marshal([]string{owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE cases SET member_ids=$1 WHERE tenant_id=$2 AND id=$3`, string(members), sourceFeedback.TenantID, sourceInput.CaseID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetExperienceFeedback(ctx, reviewer, secondCase, feedback.FeedbackID); err != nil {
		t.Fatalf("revoking the ancestor should not revoke the second case: %v", err)
	}
	_, err = st.GetExperienceCandidate(ctx, reviewer, secondCase, second.CandidateID, second.VersionID)
	requireFeedbackFault(t, err, 404)
}
