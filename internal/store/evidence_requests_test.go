package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/agentflow"
	"contractaudit/internal/fault"
)

type evidenceRequestFixture struct {
	store       *Store
	owner       accounts.User
	caseID      string
	sourceRun   string
	source      Workspace
	evaluation  ConstraintEval
	task        WorkbenchTask
	createInput EvidenceRequestInput
}

func makeEvidenceRequestFixture(t *testing.T) evidenceRequestFixture {
	t.Helper()
	st, owner := openExecutionStore(t)
	return evidenceFixtureForStore(t, st, owner)
}

func evidenceFixtureForStore(t *testing.T, st *Store, owner accounts.User) evidenceRequestFixture {
	t.Helper()
	caseID, err := st.CreateCase(owner, "补充材料请求", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range []struct{ role, text string }{
		{"procurement", "采购文件要求质保36个月。"},
		{"draft_contract", "拟签合同未写明质保期限。"},
	} {
		if _, err := st.AddDocument(context.Background(), owner, caseID, doc.role, doc.role+".txt", "text/plain", []byte(doc.text)); err != nil {
			t.Fatal(err)
		}
	}
	runID, err := st.StartReview(context.Background(), owner, caseID)
	if err != nil {
		t.Fatal(err)
	}
	workspace := waitTerminalWorkspace(t, st, owner, caseID, runID)
	if workspace.Execution == nil || workspace.Execution.Attempt == nil {
		t.Fatal("missing terminal source attempt")
	}
	var evaluation ConstraintEval
	for _, candidate := range workspace.Evaluations {
		if candidate.Domain == "warranty" && candidate.Relation == "insufficient" {
			evaluation = candidate
			break
		}
	}
	if evaluation.ID == "" {
		t.Fatalf("missing insufficient warranty check: %+v", workspace.Evaluations)
	}
	due := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	task, err := st.CreateWorkbenchTask(owner, caseID, WorkbenchTaskInput{
		Title: "补充质保期限", Kind: "review", AssigneeID: owner.ID,
		DueAt: &due, Status: "open", Priority: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	return evidenceRequestFixture{store: st, owner: owner, caseID: caseID, sourceRun: runID,
		source: workspace, evaluation: evaluation, task: task,
		createInput: EvidenceRequestInput{TaskID: task.ID, TaskVersion: task.Version, SourceRunID: runID,
			SourceAttemptID: workspace.Execution.Attempt.ID, SourceEvaluationID: evaluation.ID,
			Question: "请补充拟签合同的质保期限", MaterialRole: "draft_contract",
			VerificationCondition: "conflict", IdempotencyKey: "request-key"}}
}

func requireEvidenceFault(t *testing.T, err error, status int) {
	t.Helper()
	var apiErr *fault.Error
	if !errors.As(err, &apiErr) || apiErr.Status != status {
		t.Fatalf("error = %v, want HTTP %d", err, status)
	}
}

func (f evidenceRequestFixture) create(t *testing.T) EvidenceRequest {
	t.Helper()
	request, err := f.store.CreateEvidenceRequest(context.Background(), f.owner, f.caseID, f.createInput)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func (f evidenceRequestFixture) replaceDraft(t *testing.T, text string) string {
	t.Helper()
	id, err := replaceCurrentPrimaryForTest(t, f.store, f.owner, f.caseID, "draft.txt", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f evidenceRequestFixture) laterReview(t *testing.T, receipt EvidenceReceipt) Workspace {
	t.Helper()
	if receipt.ReviewWatermarkRunSequence < 1 {
		t.Fatal("receipt has no review acceptance fence")
	}
	runID, err := f.store.StartReview(context.Background(), f.owner, f.caseID)
	if err != nil {
		t.Fatal(err)
	}
	workspace := waitTerminalWorkspace(t, f.store, f.owner, f.caseID, runID)
	if workspace.Execution == nil || workspace.Execution.Attempt == nil || workspace.Execution.Attempt.ID == f.source.Execution.Attempt.ID {
		t.Fatalf("expected a later terminal review: %+v", workspace.Execution)
	}
	return workspace
}

func evidenceVerificationInput(t *testing.T, request EvidenceRequest, receipt EvidenceReceipt, workspace Workspace, documentID string) EvidenceRequestVerifyInput {
	t.Helper()
	var evaluation ConstraintEval
	for _, candidate := range workspace.Evaluations {
		if request.SourceEvaluation != nil && sameEvidenceCheck(*request.SourceEvaluation, candidate) {
			evaluation = candidate
			break
		}
	}
	if evaluation.ID == "" {
		t.Fatalf("later review lacks the selected check: %+v", workspace.Evaluations)
	}
	var ref ExperienceEvidenceRef
	for _, document := range workspace.Documents {
		if document.Document.ID != documentID || !document.Ready || document.ParsedDocument == nil {
			continue
		}
		ref = ExperienceEvidenceRef{DocumentID: documentID, ParsedDocumentID: document.ParsedDocument.ID,
			NormalizedDigest: document.ParsedDocument.NormalizedDigest, Quote: document.Text, Start: 0, End: len([]rune(document.Text))}
	}
	if ref.DocumentID == "" || ref.Quote == "" {
		t.Fatalf("later review lacks located receipt %s", documentID)
	}
	return EvidenceRequestVerifyInput{RunID: workspace.View.Run.ID, AttemptID: workspace.Execution.Attempt.ID,
		EvaluationID: evaluation.ID, ReceiptID: receipt.ID, Evidence: []ExperienceEvidenceRef{ref},
		Reason: "已检查提交材料中的质保期限和请求条件", ExpectedVersion: request.Version, IdempotencyKey: "verify-key"}
}

func TestEvidenceRequestReceiptAndAuthorizedVerificationPreserveHistory(t *testing.T) {
	f := makeEvidenceRequestFixture(t)
	ctx := context.Background()
	beforeState := f.store.snapshot()
	beforeInput, ok := beforeState.attemptInput(f.source.Execution.Attempt.ID)
	if !ok {
		t.Fatal("source frozen input missing")
	}
	beforeBytes, err := json.Marshal(beforeInput)
	if err != nil {
		t.Fatal(err)
	}
	request := f.create(t)
	if request.Status != "open" || request.Version != 1 || request.TaskID != f.task.ID || request.TaskVersion != f.task.Version ||
		request.AssigneeID != f.task.AssigneeID || request.DueAt != *f.task.DueAt || request.SourceEvaluation == nil || request.SourceEvaluation.ID != f.evaluation.ID ||
		request.SourceInputHash == "" || request.SourceOutputHash == "" {
		t.Fatalf("request omitted its frozen identity: %+v", request)
	}
	again, err := f.store.CreateEvidenceRequest(ctx, f.owner, f.caseID, f.createInput)
	if err != nil || !reflect.DeepEqual(request, again) {
		t.Fatalf("create replay changed result: %+v %v", again, err)
	}
	changed := f.createInput
	changed.Question = "另一个问题"
	_, err = f.store.CreateEvidenceRequest(ctx, f.owner, f.caseID, changed)
	requireEvidenceFault(t, err, 409)

	documentID := f.replaceDraft(t, "拟签合同约定质保24个月。")
	submit := EvidenceReceiptInput{DocumentID: documentID, ExpectedVersion: request.Version, IdempotencyKey: "receipt-key"}
	received, receipt, err := f.store.SubmitEvidenceReceipt(ctx, f.owner, f.caseID, request.ID, submit)
	if err != nil {
		t.Fatal(err)
	}
	if received.Status != "received" || received.Version != 2 || received.Verification != nil || receipt.DocumentID != documentID || receipt.DocumentDigest == "" {
		t.Fatalf("upload was misrepresented as a verified closure: request=%+v receipt=%+v", received, receipt)
	}
	replayed, replayReceipt, err := f.store.SubmitEvidenceReceipt(ctx, f.owner, f.caseID, request.ID, submit)
	if err != nil || !reflect.DeepEqual(replayed, received) || !reflect.DeepEqual(replayReceipt, receipt) {
		t.Fatalf("receipt replay changed result: %+v %+v %v", replayed, replayReceipt, err)
	}
	changedReceipt := submit
	changedReceipt.DocumentID = "another-document"
	_, _, err = f.store.SubmitEvidenceReceipt(ctx, f.owner, f.caseID, request.ID, changedReceipt)
	requireEvidenceFault(t, err, 409)

	later := f.laterReview(t, receipt)
	verification := evidenceVerificationInput(t, received, receipt, later, documentID)
	verification.Evidence[0].NormalizedDigest = ""
	closed, err := f.store.VerifyEvidenceRequest(ctx, f.owner, f.caseID, request.ID, verification)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != "verified" || closed.Version != 3 || closed.Verification == nil ||
		closed.Verification.AttemptID != later.Execution.Attempt.ID || closed.Verification.ReceiptID != receipt.ID ||
		closed.Verification.VerifiedBy != f.owner.ID || closed.Verification.EvaluationID != verification.EvaluationID {
		t.Fatalf("verification did not capture its authority: %+v", closed)
	}
	if closed.Verification.Evidence[0].NormalizedDigest == "" {
		t.Fatal("verification did not resolve the frozen parse digest")
	}
	actual, _ := evidenceEvaluation(state{Evaluations: later.Evaluations}, f.caseID, verification.RunID, verification.AttemptID, verification.EvaluationID)
	if closed.Verification.Relation != actual.Relation {
		t.Fatalf("human verification hid the observed check relation: %+v", closed.Verification)
	}
	verifiedReplay, err := f.store.VerifyEvidenceRequest(ctx, f.owner, f.caseID, request.ID, verification)
	if err != nil || !reflect.DeepEqual(verifiedReplay, closed) {
		t.Fatalf("verification replay changed result: %+v %v", verifiedReplay, err)
	}
	oldReplay, oldReceipt, err := f.store.SubmitEvidenceReceipt(ctx, f.owner, f.caseID, request.ID, submit)
	if err != nil || !reflect.DeepEqual(oldReplay, received) || !reflect.DeepEqual(oldReceipt, receipt) {
		t.Fatalf("later closure rewrote receipt replay: %+v %+v %v", oldReplay, oldReceipt, err)
	}
	history, err := f.store.ListEvidenceRequestHistory(ctx, f.owner, f.caseID, request.ID)
	if err != nil || len(history) != 3 || history[0].Request.Status != "open" || history[1].Request.Status != "received" ||
		history[1].Receipt == nil || *history[1].Receipt != receipt || history[2].Request.Status != "verified" {
		t.Fatalf("immutable request history is incomplete: %+v %v", history, err)
	}
	afterInput, ok := f.store.snapshot().attemptInput(f.source.Execution.Attempt.ID)
	if !ok {
		t.Fatal("source frozen input disappeared")
	}
	afterBytes, err := json.Marshal(afterInput)
	if err != nil || string(beforeBytes) != string(afterBytes) {
		t.Fatal("receipt or verification changed the source attempt")
	}
}

func TestEvidenceRequestRefusesUnobservedOrUnlocatedVerification(t *testing.T) {
	f := makeEvidenceRequestFixture(t)
	ctx := context.Background()
	request := f.create(t)
	documentID := f.replaceDraft(t, "拟签合同约定质保24个月。")
	received, receipt, err := f.store.SubmitEvidenceReceipt(ctx, f.owner, f.caseID, request.ID,
		EvidenceReceiptInput{DocumentID: documentID, ExpectedVersion: request.Version, IdempotencyKey: "receipt"})
	if err != nil {
		t.Fatal(err)
	}
	later := f.laterReview(t, receipt)
	valid := evidenceVerificationInput(t, received, receipt, later, documentID)
	for name, change := range map[string]func(*EvidenceRequestVerifyInput){
		"original attempt": func(in *EvidenceRequestVerifyInput) {
			in.RunID, in.AttemptID = f.sourceRun, f.source.Execution.Attempt.ID
		},
		"missing check":   func(in *EvidenceRequestVerifyInput) { in.EvaluationID = "not-an-evaluation" },
		"missing receipt": func(in *EvidenceRequestVerifyInput) { in.ReceiptID = "not-a-receipt" },
		"wrong parse":     func(in *EvidenceRequestVerifyInput) { in.Evidence[0].ParsedDocumentID = "another-parse" },
		"wrong digest": func(in *EvidenceRequestVerifyInput) {
			in.Evidence[0].NormalizedDigest = sha256Bytes([]byte("different"))
		},
		"forged quote":  func(in *EvidenceRequestVerifyInput) { in.Evidence[0].Quote = "原文不存在" },
		"stale version": func(in *EvidenceRequestVerifyInput) { in.ExpectedVersion-- },
	} {
		t.Run(name, func(t *testing.T) {
			input := valid
			input.IdempotencyKey = "invalid-" + name
			input.Evidence = append([]ExperienceEvidenceRef(nil), valid.Evidence...)
			change(&input)
			if _, err := f.store.VerifyEvidenceRequest(ctx, f.owner, f.caseID, request.ID, input); err == nil {
				t.Fatal("unverified evidence closed the request")
			}
			current, err := f.store.GetEvidenceRequest(ctx, f.owner, f.caseID, request.ID)
			if err != nil || current.Status != "received" || current.Version != received.Version || current.Verification != nil {
				t.Fatalf("failed verification changed the request: %+v %v", current, err)
			}
		})
	}
}

func TestEvidenceRequestAnswerRequiresLaterFrozenQuotation(t *testing.T) {
	f := makeEvidenceRequestFixture(t)
	ctx := context.Background()
	f.createInput.MaterialRole = ""
	request := f.create(t)
	answer := "质保期限为24个月"
	received, receipt, err := f.store.SubmitEvidenceReceipt(ctx, f.owner, f.caseID, request.ID,
		EvidenceReceiptInput{Answer: answer, ExpectedVersion: 1, IdempotencyKey: "answer"})
	if err != nil || received.Status != "received" || receipt.Answer != answer {
		t.Fatalf("answer receipt: %+v %+v %v", received, receipt, err)
	}
	documentID := f.replaceDraft(t, "拟签合同约定质保24个月。")
	later := f.laterReview(t, receipt)
	verify := evidenceVerificationInput(t, received, receipt, later, documentID)
	if _, err := f.store.VerifyEvidenceRequest(ctx, f.owner, f.caseID, request.ID, verify); err == nil {
		t.Fatal("answer absent from the later frozen quotation was accepted")
	}
	current, err := f.store.GetEvidenceRequest(ctx, f.owner, f.caseID, request.ID)
	if err != nil || current.Status != "received" {
		t.Fatalf("unmatched answer did not stay unresolved: %+v %v", current, err)
	}
	matchingDoc := f.replaceDraft(t, "拟签合同补充答复：质保期限为24个月。")
	later = f.laterReview(t, receipt)
	verify = evidenceVerificationInput(t, received, receipt, later, matchingDoc)
	verified, err := f.store.VerifyEvidenceRequest(ctx, f.owner, f.caseID, request.ID, verify)
	if err != nil || verified.Status != "verified" || verified.Verification.ReceiptID != receipt.ID {
		t.Fatalf("located answer was not verified: %+v %v", verified, err)
	}
}

func TestEvidenceRequestAuthorizationAndVersionConflicts(t *testing.T) {
	f := makeEvidenceRequestFixture(t)
	ctx := context.Background()
	viewer, ok := accounts.ByID("demo-viewer")
	if !ok {
		t.Fatal("viewer missing")
	}
	if err := f.store.AddMember(f.owner, f.caseID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.store.CreateEvidenceRequest(ctx, viewer, f.caseID, f.createInput)
	requireEvidenceFault(t, err, 403)
	input := f.createInput
	input.TaskVersion++
	_, err = f.store.CreateEvidenceRequest(ctx, f.owner, f.caseID, input)
	requireEvidenceFault(t, err, 409)
	input = f.createInput
	input.SourceEvaluationID = "not-the-frozen-check"
	_, err = f.store.CreateEvidenceRequest(ctx, f.owner, f.caseID, input)
	requireEvidenceFault(t, err, 422)
	input = f.createInput
	input.VerificationCondition = "unverifiable-free-text"
	_, err = f.store.CreateEvidenceRequest(ctx, f.owner, f.caseID, input)
	requireEvidenceFault(t, err, 422)
	input = f.createInput
	input.SourceGapID = "another-check"
	_, err = f.store.CreateEvidenceRequest(ctx, f.owner, f.caseID, input)
	requireEvidenceFault(t, err, 422)
	request := f.create(t)
	if _, err := f.store.GetEvidenceRequest(ctx, viewer, f.caseID, request.ID); err != nil {
		t.Fatalf("case viewer could not read: %v", err)
	}
	_, _, err = f.store.SubmitEvidenceReceipt(ctx, viewer, f.caseID, request.ID,
		EvidenceReceiptInput{Answer: "无权答复", ExpectedVersion: 1, IdempotencyKey: "viewer"})
	requireEvidenceFault(t, err, 403)
	_, err = f.store.VerifyEvidenceRequest(ctx, viewer, f.caseID, request.ID, EvidenceRequestVerifyInput{
		RunID: f.sourceRun, AttemptID: f.source.Execution.Attempt.ID, EvaluationID: f.evaluation.ID,
		ReceiptID: "missing-receipt", ExpectedVersion: 1, IdempotencyKey: "viewer-verify", Reason: "申请复核",
		Evidence: []ExperienceEvidenceRef{{DocumentID: "doc", ParsedDocumentID: "parse", Quote: "x", Start: 0, End: 1}},
	})
	requireEvidenceFault(t, err, 403)
	var foreign accounts.User
	for _, actor := range accounts.List() {
		if actor.TenantID != f.owner.TenantID && actor.Role == "admin" {
			foreign = actor
			break
		}
	}
	if foreign.ID == "" {
		t.Fatal("foreign tenant identity missing")
	}
	if _, err := f.store.GetEvidenceRequest(ctx, foreign, f.caseID, request.ID); err == nil {
		t.Fatal("cross-tenant request was visible")
	}
	otherCase, err := f.store.CreateCase(f.owner, "其他案件", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.GetEvidenceRequest(ctx, f.owner, otherCase, request.ID); err == nil {
		t.Fatal("request leaked through another case")
	}
	documentID := f.replaceDraft(t, "拟签合同约定质保36个月。")
	_, _, err = f.store.SubmitEvidenceReceipt(ctx, f.owner, f.caseID, request.ID,
		EvidenceReceiptInput{DocumentID: documentID, ExpectedVersion: 2, IdempotencyKey: "stale"})
	requireEvidenceFault(t, err, 409)
	foreignID, err := f.store.AddDocument(ctx, f.owner, otherCase, "draft_contract", "foreign.txt", "text/plain", []byte("其他案件质保36个月。"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = f.store.SubmitEvidenceReceipt(ctx, f.owner, f.caseID, request.ID,
		EvidenceReceiptInput{DocumentID: foreignID, ExpectedVersion: 1, IdempotencyKey: "foreign"})
	requireEvidenceFault(t, err, 422)
}

func TestEvidenceReceiptConcurrentReplayAndCAS(t *testing.T) {
	f := makeEvidenceRequestFixture(t)
	ctx := context.Background()
	request := f.create(t)
	input := EvidenceReceiptInput{Answer: "质保期限为36个月", ExpectedVersion: 1, IdempotencyKey: "same-key"}
	type result struct {
		request EvidenceRequest
		receipt EvidenceReceipt
		err     error
	}
	results := make(chan result, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			r, receipt, err := f.store.SubmitEvidenceReceipt(ctx, f.owner, f.caseID, request.ID, input)
			results <- result{r, receipt, err}
		}()
	}
	group.Wait()
	close(results)
	var first *result
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if first == nil {
			copy := result
			first = &copy
			continue
		}
		if !reflect.DeepEqual(*first, result) {
			t.Fatalf("concurrent replay produced multiple receipts: %+v %+v", *first, result)
		}
	}
	history, err := f.store.ListEvidenceRequestHistory(ctx, f.owner, f.caseID, request.ID)
	if err != nil || len(history) != 2 {
		t.Fatalf("replay appended more than one receipt: %+v %v", history, err)
	}
	_, _, err = f.store.SubmitEvidenceReceipt(ctx, f.owner, f.caseID, request.ID,
		EvidenceReceiptInput{Answer: "另一份答复", ExpectedVersion: 1, IdempotencyKey: "stale-writer"})
	requireEvidenceFault(t, err, 409)
}

func TestEvidenceRequestCheckIdentityNeedsUniqueActualEvaluation(t *testing.T) {
	source := ConstraintEval{ID: "old", Domain: "warranty", RuleVersion: "v1", Relation: "insufficient"}
	after := ConstraintEval{ID: "new", CaseID: "case", RunID: "run", AttemptID: "attempt", Domain: "warranty", RuleVersion: "v1", Relation: "insufficient"}
	st := state{Evaluations: []ConstraintEval{after}}
	if got, ok := matchingEvidenceEvaluation(st, source, "case", "run", "attempt", "new"); !ok || got.Relation != "insufficient" {
		t.Fatal("a real legacy check without an object identity was lost")
	}
	st.Evaluations = append(st.Evaluations, after)
	if _, ok := matchingEvidenceEvaluation(st, source, "case", "run", "attempt", "new"); ok {
		t.Fatal("ambiguous later checks were accepted")
	}
	source.RuleID, source.CapabilityID, source.ObjectRefs = "rule", "capability", []string{"object-a"}
	after.RuleID, after.CapabilityID, after.ObjectRefs = "rule", "capability", []string{"object-b"}
	if sameEvidenceCheck(source, after) {
		t.Fatal("different known objects matched")
	}
	after.ObjectRefs = []string{"object-a"}
	if !sameEvidenceCheck(source, after) {
		t.Fatal("same governed check was not matched")
	}
	after.RuleVersion = "v2"
	if sameEvidenceCheck(source, after) {
		t.Fatal("different rule versions matched")
	}
}

func TestEvidenceReceiptAndRequestEventsAreAppendOnly(t *testing.T) {
	f := makeEvidenceRequestFixture(t)
	ctx := context.Background()
	request := f.create(t)
	_, receipt, err := f.store.SubmitEvidenceReceipt(ctx, f.owner, f.caseID, request.ID,
		EvidenceReceiptInput{Answer: "质保期限为36个月", ExpectedVersion: 1, IdempotencyKey: "append-test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ query, id string }{
		{`UPDATE evidence_receipts SET record_json='{}' WHERE receipt_id=$1`, receipt.ID},
		{`DELETE FROM evidence_receipts WHERE receipt_id=$1`, receipt.ID},
		{`UPDATE evidence_request_events SET detail_json='{}' WHERE request_id=$1`, request.ID},
		{`DELETE FROM evidence_request_events WHERE request_id=$1`, request.ID},
	} {
		tx, err := f.store.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		scope := userScope(f.owner)
		scope.CaseID = f.caseID
		if err := setLocalScope(ctx, tx, scope); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, test.query, test.id)
		_ = tx.Rollback(ctx)
		if err == nil {
			t.Fatalf("append-only mutation succeeded: %s", test.query)
		}
	}
}

func TestEvidenceRequestInsufficientCheckCannotBeVerified(t *testing.T) {
	f := makeEvidenceRequestFixture(t)
	f.createInput.Question = "请提供含例外范围的质保条款"
	request := f.create(t)
	documentID := f.replaceDraft(t, "除电池外，质保36个月。")
	received, receipt, err := f.store.SubmitEvidenceReceipt(context.Background(), f.owner, f.caseID, request.ID,
		EvidenceReceiptInput{DocumentID: documentID, ExpectedVersion: request.Version, IdempotencyKey: "qualified-receipt"})
	if err != nil {
		t.Fatal(err)
	}
	later := f.laterReview(t, receipt)
	input := evidenceVerificationInput(t, received, receipt, later, documentID)
	input.Reason = "已人工核对所提交条款及电池除外的明确范围"
	var observed ConstraintEval
	for _, evaluation := range later.Evaluations {
		if evaluation.ID == input.EvaluationID {
			observed = evaluation
		}
	}
	if observed.Relation != "insufficient" {
		t.Fatalf("fixture requires a check the engine could not decide: %+v", observed)
	}
	_, err = f.store.VerifyEvidenceRequest(context.Background(), f.owner, f.caseID, request.ID, input)
	requireEvidenceFault(t, err, 409)
	current, err := f.store.GetEvidenceRequest(context.Background(), f.owner, f.caseID, request.ID)
	if err != nil || current.Status != "received" || current.Verification != nil {
		t.Fatalf("insufficient check closed the request: %+v %v", current, err)
	}
}

func TestEvidenceRequestCorePrivilegesAndTenantRLS(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	owner, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	f := evidenceFixtureForStore(t, env.harness, owner)
	core := env.openRole(t, "core")
	f.store = core
	request := f.create(t)
	document := env.openRole(t, "document")
	_, err := document.CreateEvidenceRequest(context.Background(), owner, f.caseID, f.createInput)
	requireEvidenceFault(t, err, 403)
	ctx := context.Background()
	tx, err := core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err := setLocalScope(ctx, tx, dbScope{ActorID: owner.ID, TenantID: "different-tenant", OrgID: "different-org", Role: "api"}); err != nil {
		t.Fatal(err)
	}
	var visible int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM evidence_requests WHERE request_id=$1`, request.ID).Scan(&visible); err != nil {
		t.Fatal(err)
	}
	if visible != 0 {
		t.Fatal("cross-tenant request was visible through PostgreSQL RLS")
	}
	var receiptUpdate, eventUpdate, projectionUpdate bool
	if err := tx.QueryRow(ctx, `SELECT has_table_privilege(current_user,'evidence_receipts','UPDATE'),
		has_table_privilege(current_user,'evidence_request_events','UPDATE'),
		has_table_privilege(current_user,'evidence_requests','UPDATE')`).Scan(&receiptUpdate, &eventUpdate, &projectionUpdate); err != nil {
		t.Fatal(err)
	}
	if receiptUpdate || eventUpdate || !projectionUpdate {
		t.Fatalf("core privileges do not preserve append-only history: receipts=%v events=%v projection=%v", receiptUpdate, eventUpdate, projectionUpdate)
	}
}

func TestEvidenceReceiptConcurrentDifferentWritersUseCAS(t *testing.T) {
	f := makeEvidenceRequestFixture(t)
	request := f.create(t)
	type result struct{ err error }
	results := make(chan result, 2)
	var group sync.WaitGroup
	for _, answer := range []string{"第一份答复", "第二份答复"} {
		group.Add(1)
		go func(answer string) {
			defer group.Done()
			_, _, err := f.store.SubmitEvidenceReceipt(context.Background(), f.owner, f.caseID, request.ID,
				EvidenceReceiptInput{Answer: answer, ExpectedVersion: 1, IdempotencyKey: answer})
			results <- result{err}
		}(answer)
	}
	group.Wait()
	close(results)
	success, conflicts := 0, 0
	for result := range results {
		if result.err == nil {
			success++
			continue
		}
		var problem *fault.Error
		if errors.As(result.err, &problem) && problem.Status == 409 {
			conflicts++
			continue
		}
		t.Fatalf("unexpected concurrent receipt failure: %v", result.err)
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("concurrent version fence: successes=%d conflicts=%d", success, conflicts)
	}
	history, err := f.store.ListEvidenceRequestHistory(context.Background(), f.owner, f.caseID, request.ID)
	if err != nil || len(history) != 2 {
		t.Fatalf("concurrent submissions changed history incorrectly: %+v %v", history, err)
	}
}

func TestEvidenceRequestUsesPersistedAgentGapAndLaterObservedCheck(t *testing.T) {
	t.Setenv("AGENT_PLANNER_MODE", "deterministic")
	st, claim, caseID, _ := agentReviewFixture(t)
	owner, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	ctx := context.Background()
	if err := st.RunClaimedJob(ctx, claim); err != nil {
		t.Fatal(err)
	}
	source := waitTerminalWorkspace(t, st, owner, caseID, claim.RunID)
	if source.Execution == nil {
		t.Fatal("missing source attempt")
	}
	var projection agentflow.AgentReview
	if err := json.Unmarshal(source.Execution.AgentReview, &projection); err != nil {
		t.Fatal(err)
	}
	var missing agentflow.GapReport
	for _, gap := range projection.Gaps {
		if gap.Domain == agentflow.Compliance && evidenceMissingGap(gap) {
			missing = gap
			break
		}
	}
	if missing.GapID == "" {
		t.Fatalf("fixture has no observed missing agent check: %+v", projection.Gaps)
	}
	due := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	task, err := st.CreateWorkbenchTask(owner, caseID, WorkbenchTaskInput{Title: "补充审查缺口", Kind: "review",
		AssigneeID: owner.ID, DueAt: &due, Status: "open", Priority: "high"})
	if err != nil {
		t.Fatal(err)
	}
	input := EvidenceRequestInput{TaskID: task.ID, TaskVersion: task.Version, SourceRunID: claim.RunID,
		SourceAttemptID: claim.AttemptID, SourceGapID: missing.GapID, Question: "请补充该缺口所需的合同资料",
		MaterialRole: "draft_contract", VerificationCondition: "resolved", IdempotencyKey: "agent-gap"}
	bad := input
	bad.SourceGapID = "unobserved-gap"
	if _, err := st.CreateEvidenceRequest(ctx, owner, caseID, bad); err == nil {
		t.Fatal("an absent gap became a request")
	}
	request, err := st.CreateEvidenceRequest(ctx, owner, caseID, input)
	if err != nil || request.SourceGap == nil || request.SourceGap.GapID != missing.GapID {
		t.Fatalf("gap was not captured: %+v %v", request, err)
	}
	documentID, err := replaceCurrentPrimaryForTest(t, st, owner, caseID, "updated.txt",
		[]byte("拟签合同：电路 LINE-001 最低保障带宽 100 Mbit/s。合同总价：100万元。质保36个月。"))
	if err != nil {
		t.Fatal(err)
	}
	received, receipt, err := st.SubmitEvidenceReceipt(ctx, owner, caseID, request.ID,
		EvidenceReceiptInput{DocumentID: documentID, ExpectedVersion: 1, IdempotencyKey: "agent-receipt"})
	if err != nil || received.Status != "received" {
		t.Fatalf("gap receipt: %+v %v", received, err)
	}
	accepted, err := st.StartReviewAsyncAt(ctx, owner, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	laterClaim, err := st.ClaimNextJob(ctx, "evidence-gap-worker", time.Minute)
	if err != nil || laterClaim.JobID != accepted.JobID {
		t.Fatalf("later review claim: %+v %v", laterClaim, err)
	}
	if err := st.RunClaimedJob(ctx, laterClaim); err != nil {
		t.Fatal(err)
	}
	later := waitTerminalWorkspace(t, st, owner, caseID, accepted.RunID)
	if later.Execution == nil {
		t.Fatal("missing later attempt")
	}
	var laterProjection agentflow.AgentReview
	if err := json.Unmarshal(later.Execution.AgentReview, &laterProjection); err != nil {
		t.Fatal(err)
	}
	observed, ok := evidenceGap(laterProjection, missing.GapID)
	if !ok || !sameEvidenceGap(missing, observed) {
		t.Fatalf("later projection lacks the matching check: %+v", laterProjection.Gaps)
	}
	if !evidenceMissingGap(observed) {
		t.Fatalf("fixture must retain an unresolved authority gap: %+v", observed)
	}
	var ref ExperienceEvidenceRef
	for _, doc := range later.Documents {
		if doc.Document.ID == documentID && doc.Ready && doc.ParsedDocument != nil {
			ref = ExperienceEvidenceRef{DocumentID: documentID, ParsedDocumentID: doc.ParsedDocument.ID,
				NormalizedDigest: doc.ParsedDocument.NormalizedDigest, Quote: doc.Text, End: len([]rune(doc.Text))}
		}
	}
	if ref.DocumentID == "" {
		t.Fatal("later attempt does not include the receipt")
	}
	verify := EvidenceRequestVerifyInput{RunID: accepted.RunID, AttemptID: laterClaim.AttemptID, GapID: missing.GapID,
		ReceiptID: receipt.ID, Evidence: []ExperienceEvidenceRef{ref}, Reason: "已核对提交的合同资料与对应审查缺口",
		ExpectedVersion: received.Version, IdempotencyKey: "agent-verify"}
	wrong := verify
	wrong.GapID = "different-gap"
	if _, err := st.VerifyEvidenceRequest(ctx, owner, caseID, request.ID, wrong); err == nil {
		t.Fatal("an absent later gap verified the request")
	}
	_, err = st.VerifyEvidenceRequest(ctx, owner, caseID, request.ID, verify)
	requireEvidenceFault(t, err, 409)
	current, err := st.GetEvidenceRequest(ctx, owner, caseID, request.ID)
	if err != nil || current.Status != "received" || current.Verification != nil {
		t.Fatalf("unresolved gap closed the request: %+v %v", current, err)
	}
}

func TestEvidenceRequestGapMatchingRejectsAbsentAmbiguousOrChangedChecks(t *testing.T) {
	missing := agentflow.GapReport{GapID: "gap", Domain: agentflow.Compliance, Capability: "authority", Status: "unknown"}
	if !evidenceMissingGap(missing) {
		t.Fatal("unknown gap was not recognized")
	}
	review := agentflow.AgentReview{Gaps: []agentflow.GapReport{missing}}
	if observed, ok := evidenceGap(review, "gap"); !ok || !sameEvidenceGap(missing, observed) {
		t.Fatal("actual matching gap was not found")
	}
	if _, ok := evidenceGap(review, "absent"); ok {
		t.Fatal("absent gap was treated as observed")
	}
	review.Gaps = append(review.Gaps, missing)
	if _, ok := evidenceGap(review, "gap"); ok {
		t.Fatal("ambiguous gaps were accepted")
	}
	changed := missing
	changed.Capability = "different"
	if sameEvidenceGap(missing, changed) {
		t.Fatal("different capabilities were treated as the same check")
	}
	missing.Status = "resolved"
	if evidenceMissingGap(missing) {
		t.Fatal("resolved check became a missing request")
	}
	relation := "compatible"
	review = agentflow.AgentReview{Domains: []agentflow.DomainCoverage{{Domain: missing.Domain,
		Capabilities: []agentflow.CapabilityResult{{CapabilityID: missing.Capability, Status: "completed", Relation: &relation}}}}}
	if !evidenceResolvedGap(review, missing) {
		t.Fatal("matching completed gap was not recognized")
	}
	review.UnresolvedGapIDs = []string{missing.GapID}
	if evidenceResolvedGap(review, missing) {
		t.Fatal("contradictory unresolved status was accepted")
	}
	review.UnresolvedGapIDs = nil
	review.Domains[0].Capabilities[0].Relation = nil
	if evidenceResolvedGap(review, missing) {
		t.Fatal("a completed label without an observed relation was accepted")
	}
}

func TestEvidenceRequestRejectsReviewAcceptedBeforeReceipt(t *testing.T) {
	f := makeEvidenceRequestFixture(t)
	ctx := context.Background()
	request := f.create(t)
	documentID := f.replaceDraft(t, "拟签合同约定质保36个月。")
	runID, err := f.store.StartReview(ctx, f.owner, f.caseID)
	if err != nil {
		t.Fatal(err)
	}
	priorReview := waitTerminalWorkspace(t, f.store, f.owner, f.caseID, runID)
	received, receipt, err := f.store.SubmitEvidenceReceipt(ctx, f.owner, f.caseID, request.ID,
		EvidenceReceiptInput{DocumentID: documentID, ExpectedVersion: 1, IdempotencyKey: "after-review"})
	if err != nil {
		t.Fatal(err)
	}
	verification := evidenceVerificationInput(t, received, receipt, priorReview, documentID)
	_, err = f.store.VerifyEvidenceRequest(ctx, f.owner, f.caseID, request.ID, verification)
	requireEvidenceFault(t, err, 409)
	current, err := f.store.GetEvidenceRequest(ctx, f.owner, f.caseID, request.ID)
	if err != nil || current.Status != "received" || current.Verification != nil {
		t.Fatalf("pre-receipt attempt closed request: %+v %v", current, err)
	}
}

func TestEvidenceRequestRequiresTheSpecifiedResultAndReceiptProvenance(t *testing.T) {
	t.Run("different result", func(t *testing.T) {
		f := makeEvidenceRequestFixture(t)
		f.createInput.VerificationCondition = "compatible"
		request := f.create(t)
		documentID := f.replaceDraft(t, "拟签合同约定质保24个月。")
		received, receipt, err := f.store.SubmitEvidenceReceipt(context.Background(), f.owner, f.caseID, request.ID,
			EvidenceReceiptInput{DocumentID: documentID, ExpectedVersion: 1, IdempotencyKey: "different-result"})
		if err != nil {
			t.Fatal(err)
		}
		later := f.laterReview(t, receipt)
		verification := evidenceVerificationInput(t, received, receipt, later, documentID)
		_, err = f.store.VerifyEvidenceRequest(context.Background(), f.owner, f.caseID, request.ID, verification)
		requireEvidenceFault(t, err, 409)
	})
	t.Run("receipt not used by check", func(t *testing.T) {
		f := makeEvidenceRequestFixture(t)
		f.createInput.MaterialRole = ""
		request := f.create(t)
		documentID, err := f.store.AddDocument(context.Background(), f.owner, f.caseID, "attachment", "receipt.txt", "text/plain", []byte("这是与本次审查无关的资料。"))
		if err != nil {
			t.Fatal(err)
		}
		received, receipt, err := f.store.SubmitEvidenceReceipt(context.Background(), f.owner, f.caseID, request.ID,
			EvidenceReceiptInput{DocumentID: documentID, ExpectedVersion: 1, IdempotencyKey: "unrelated"})
		if err != nil {
			t.Fatal(err)
		}
		f.replaceDraft(t, "拟签合同约定质保24个月。")
		later := f.laterReview(t, receipt)
		verification := evidenceVerificationInput(t, received, receipt, later, documentID)
		_, err = f.store.VerifyEvidenceRequest(context.Background(), f.owner, f.caseID, request.ID, verification)
		requireEvidenceFault(t, err, 409)
		current, err := f.store.GetEvidenceRequest(context.Background(), f.owner, f.caseID, request.ID)
		if err != nil || current.Status != "received" || current.Verification != nil {
			t.Fatalf("unrelated evidence closed request: %+v %v", current, err)
		}
	})
	t.Run("compatible without attributable evidence", func(t *testing.T) {
		f := makeEvidenceRequestFixture(t)
		f.createInput.VerificationCondition = "compatible"
		request := f.create(t)
		documentID := f.replaceDraft(t, "拟签合同约定质保36个月。")
		received, receipt, err := f.store.SubmitEvidenceReceipt(context.Background(), f.owner, f.caseID, request.ID,
			EvidenceReceiptInput{DocumentID: documentID, ExpectedVersion: 1, IdempotencyKey: "no-provenance"})
		if err != nil {
			t.Fatal(err)
		}
		later := f.laterReview(t, receipt)
		verification := evidenceVerificationInput(t, received, receipt, later, documentID)
		_, err = f.store.VerifyEvidenceRequest(context.Background(), f.owner, f.caseID, request.ID, verification)
		requireEvidenceFault(t, err, 409)
	})
}

func TestEvidenceGapReceiptRequiresObservedCheckEvidence(t *testing.T) {
	request := EvidenceRequest{MaterialRole: "draft_contract"}
	receipt := EvidenceReceipt{DocumentID: "doc", DocumentDigest: "original"}
	input := AttemptInput{Documents: []FrozenDocument{{Document: Document{ID: "doc", Role: "draft_contract", Digest: "original"}}}}
	ref := ExperienceEvidenceRef{DocumentID: "doc", ParsedDocumentID: "parse", NormalizedDigest: "digest", Quote: "verified", Start: 0, End: 8}
	gap := agentflow.GapReport{GapID: "gap", Status: "resolved", EvidenceIDs: []string{"other"}}
	if observedGapReceipt(request, receipt, input, gap, agentflow.AgentReview{}, []ExperienceEvidenceRef{ref}) {
		t.Fatal("unrelated gap evidence was accepted")
	}
	gap.EvidenceIDs = []string{"doc"}
	if !observedGapReceipt(request, receipt, input, gap, agentflow.AgentReview{}, []ExperienceEvidenceRef{ref}) {
		t.Fatal("cited receipt document was not matched")
	}
	gap.EvidenceIDs = []string{"fact"}
	review := agentflow.AgentReview{Evidence: []agentflow.VerifiedEvidence{{EvidenceID: "fact", Verification: "typed_semantic_and_frozen_quote_verified",
		Anchor: agentflow.EvidenceAnchor{DocumentID: "doc", ParsedDocumentID: "parse", NormalizedDigest: "digest", Quote: "verified", Start: 0, End: 8, Status: "located"}}}}
	if !observedGapReceipt(request, receipt, input, gap, review, []ExperienceEvidenceRef{ref}) {
		t.Fatal("cited typed fact was not matched")
	}
	review.Evidence[0].Anchor.Quote = "forged!!"
	if observedGapReceipt(request, receipt, input, gap, review, []ExperienceEvidenceRef{ref}) {
		t.Fatal("typed fact with a mismatched quote was accepted")
	}
}

func TestEvidenceRequestVerifiesCompatibleGovernedFactWithFrozenCitation(t *testing.T) {
	t.Setenv("AGENT_PLANNER_MODE", "deterministic")
	st, owner := openExecutionStore(t)
	st.UseModels(&frozenOCRModel{})
	pack := publishSyntheticAcceptancePack(t, st, owner)
	publishSyntheticAcceptancePolicy(t, st, owner, pack)
	caseID := governanceTestCase(t, st, owner)
	ctx := context.Background()
	for _, doc := range []struct{ role, text string }{
		{"procurement", "采购依据。电路 LINE-001：最低保障带宽 100 Mbit/s。"},
		{"draft_contract", "拟签合同尚未写明电路带宽。"},
	} {
		if _, err := st.AddDocument(ctx, owner, caseID, doc.role, doc.role+".txt", "text/plain", []byte(doc.text)); err != nil {
			t.Fatal(err)
		}
	}
	accepted, err := st.StartReviewAsyncAt(ctx, owner, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	runAcceptedKnowledgeJob(t, st, accepted)
	source := waitTerminalWorkspace(t, st, owner, caseID, accepted.RunID)
	var missing ConstraintEval
	for _, evaluation := range source.Evaluations {
		if evaluation.CapabilityID == "dedicated_line.minimum_bandwidth" && evaluation.Relation == "insufficient" {
			missing = evaluation
			break
		}
	}
	if missing.ID == "" || missing.ReasonCode != "required_fact_missing" {
		t.Fatalf("source did not record a missing draft bandwidth fact with contractual authority: %+v", source.Evaluations)
	}
	due := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	task, err := st.CreateWorkbenchTask(owner, caseID, WorkbenchTaskInput{Title: "补充带宽承诺", Kind: "review", AssigneeID: owner.ID,
		DueAt: &due, Status: "open", Priority: "high"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := st.CreateEvidenceRequest(ctx, owner, caseID, EvidenceRequestInput{TaskID: task.ID, TaskVersion: task.Version,
		SourceRunID: accepted.RunID, SourceAttemptID: accepted.AttemptID, SourceEvaluationID: missing.ID,
		Question: "请提供电路的最低保障带宽", MaterialRole: "draft_contract", VerificationCondition: "compatible", IdempotencyKey: "bandwidth-request"})
	if err != nil {
		t.Fatal(err)
	}
	documentID, err := replaceCurrentPrimaryForTest(t, st, owner, caseID, "bandwidth.txt",
		[]byte("拟签合同。电路 LINE-001：最低保障带宽 100 Mbit/s。"))
	if err != nil {
		t.Fatal(err)
	}
	received, receipt, err := st.SubmitEvidenceReceipt(ctx, owner, caseID, request.ID,
		EvidenceReceiptInput{DocumentID: documentID, ExpectedVersion: request.Version, IdempotencyKey: "bandwidth-receipt"})
	if err != nil {
		t.Fatal(err)
	}
	laterAccepted, err := st.StartReviewAsyncAt(ctx, owner, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	runAcceptedKnowledgeJob(t, st, laterAccepted)
	later := waitTerminalWorkspace(t, st, owner, caseID, laterAccepted.RunID)
	verification := evidenceVerificationInput(t, received, receipt, later, documentID)
	var observed ConstraintEval
	for _, evaluation := range later.Evaluations {
		if evaluation.ID == verification.EvaluationID {
			observed = evaluation
		}
	}
	if observed.Relation != "compatible" || len(observed.FactIDs) == 0 || len(observed.Evidence) == 0 {
		t.Fatalf("compatible result lacks its persisted fact and citation: %+v", observed)
	}
	tx, _, caseItem, err := st.beginExperienceCase(ctx, owner, caseID, "case.read")
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, frozen, err := st.evidenceAttempt(ctx, tx, caseItem, laterAccepted.RunID, laterAccepted.AttemptID)
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatal(err)
	}
	foreign := observed
	foreign.FactIDs = []string{"fact-from-another-check"}
	foreign.Evidence = nil
	for _, entry := range observed.Evidence {
		if entry.FactID != "" {
			foreign.Evidence = append(foreign.Evidence, entry)
		}
	}
	if len(foreign.Evidence) == 0 {
		_ = tx.Rollback(context.Background())
		t.Fatal("fixture requires a fact-linked citation")
	}
	matched, err := observedEvaluationReceipt(ctx, tx, caseItem, loaded, frozen, received, receipt, foreign, verification.Evidence)
	if err != nil || matched {
		_ = tx.Rollback(context.Background())
		t.Fatalf("citation with an unrelated fact identity was accepted: matched=%v err=%v", matched, err)
	}
	direct := observed
	direct.Evidence = nil
	for _, entry := range observed.Evidence {
		if entry.Ref.DocumentID == documentID {
			entry.FactID = ""
			direct.Evidence = append(direct.Evidence, entry)
		}
	}
	if len(direct.Evidence) == 0 {
		_ = tx.Rollback(context.Background())
		t.Fatal("fixture requires a receipt citation")
	}
	matched, err = observedEvaluationReceipt(ctx, tx, caseItem, loaded, frozen, received, receipt, direct, verification.Evidence)
	_ = tx.Rollback(context.Background())
	if err != nil || !matched {
		t.Fatalf("direct result citation was not matched: matched=%v err=%v", matched, err)
	}
	verification.Evidence[0].NormalizedDigest = ""
	verified, err := st.VerifyEvidenceRequest(ctx, owner, caseID, request.ID, verification)
	if err != nil || verified.Status != "verified" || verified.Verification == nil || verified.Verification.Relation != "compatible" {
		t.Fatalf("compatible result with attributed receipt could not be verified: %+v %v", verified, err)
	}
}

func TestEvidenceRequestVerifiesResolvedAgentGapWithCitedReceipt(t *testing.T) {
	_ = frozenPlannerTestConfiguration(t)
	st, owner := openExecutionStore(t)
	st.UseModels(&frozenOCRModel{})
	pack := publishSyntheticAcceptancePack(t, st, owner)
	publishSyntheticAcceptancePolicy(t, st, owner, pack)
	caseID := governanceTestCase(t, st, owner)
	ctx := context.Background()
	for _, doc := range []struct{ role, text string }{
		{"procurement", "采购依据。电路 LINE-001：最低保障带宽 100 Mbit/s。质保36个月。"},
		{"draft_contract", "拟签合同。电路 LINE-001：最低保障带宽 100 Mbit/s。未写明质保。"},
	} {
		if _, err := st.AddDocument(ctx, owner, caseID, doc.role, doc.role+".txt", "text/plain", []byte(doc.text)); err != nil {
			t.Fatal(err)
		}
	}
	accepted, err := st.StartReviewAsyncAt(ctx, owner, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	runAcceptedKnowledgeJob(t, st, accepted)
	source := waitTerminalWorkspace(t, st, owner, caseID, accepted.RunID)
	if source.Execution == nil {
		t.Fatal("source execution missing")
	}
	var sourceReview agentflow.AgentReview
	if err := json.Unmarshal(source.Execution.AgentReview, &sourceReview); err != nil {
		t.Fatal(err)
	}
	var missing agentflow.GapReport
	for _, gap := range sourceReview.Gaps {
		if gap.Domain == agentflow.Technical && gap.Capability == agentflow.BaselineCapability(agentflow.Technical) {
			missing = gap
			break
		}
	}
	if !evidenceMissingGap(missing) {
		t.Fatalf("source warranty gap is not unresolved: %+v", missing)
	}
	due := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	task, err := st.CreateWorkbenchTask(owner, caseID, WorkbenchTaskInput{Title: "补充质保", Kind: "review", AssigneeID: owner.ID,
		DueAt: &due, Status: "open", Priority: "high"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := st.CreateEvidenceRequest(ctx, owner, caseID, EvidenceRequestInput{TaskID: task.ID, TaskVersion: task.Version,
		SourceRunID: accepted.RunID, SourceAttemptID: accepted.AttemptID, SourceGapID: missing.GapID,
		Question: "请提供拟签合同的质保期限", MaterialRole: "draft_contract", VerificationCondition: "resolved", IdempotencyKey: "warranty-gap"})
	if err != nil {
		t.Fatal(err)
	}
	documentID, err := replaceCurrentPrimaryForTest(t, st, owner, caseID, "revised.txt",
		[]byte("拟签合同。电路 LINE-001：最低保障带宽 100 Mbit/s。质保24个月。"))
	if err != nil {
		t.Fatal(err)
	}
	received, receipt, err := st.SubmitEvidenceReceipt(ctx, owner, caseID, request.ID,
		EvidenceReceiptInput{DocumentID: documentID, ExpectedVersion: 1, IdempotencyKey: "gap-receipt"})
	if err != nil {
		t.Fatal(err)
	}
	laterAccepted, err := st.StartReviewAsyncAt(ctx, owner, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	runAcceptedKnowledgeJob(t, st, laterAccepted)
	later := waitTerminalWorkspace(t, st, owner, caseID, laterAccepted.RunID)
	if later.Execution == nil {
		t.Fatal("later execution missing")
	}
	var laterReview agentflow.AgentReview
	if err := json.Unmarshal(later.Execution.AgentReview, &laterReview); err != nil {
		t.Fatal(err)
	}
	observed, ok := evidenceGap(laterReview, missing.GapID)
	if !ok || !evidenceResolvedGap(laterReview, observed) {
		t.Fatalf("later warranty gap did not record a decisive result: %+v", observed)
	}
	var ref ExperienceEvidenceRef
	for _, doc := range later.Documents {
		if doc.Document.ID == documentID && doc.Ready && doc.ParsedDocument != nil {
			ref = ExperienceEvidenceRef{DocumentID: documentID, ParsedDocumentID: doc.ParsedDocument.ID,
				Quote: doc.Text, End: len([]rune(doc.Text))}
		}
	}
	if ref.DocumentID == "" {
		t.Fatal("receipt is absent from later frozen input")
	}
	verified, err := st.VerifyEvidenceRequest(ctx, owner, caseID, request.ID, EvidenceRequestVerifyInput{
		RunID: laterAccepted.RunID, AttemptID: laterAccepted.AttemptID, GapID: missing.GapID,
		ReceiptID: receipt.ID, Evidence: []ExperienceEvidenceRef{ref}, Reason: "已核对质保结果及提交的合同",
		ExpectedVersion: received.Version, IdempotencyKey: "gap-verified"})
	if err != nil || verified.Status != "verified" || verified.Verification == nil || verified.Verification.GapStatus != "resolved" {
		t.Fatalf("resolved cited gap was not verified: %+v %v", verified, err)
	}
}
