package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
)

// CLM-08 durable checks. They require TEST_DATABASE_URL and
// MIGRATOR_DATABASE_URL; without a database the whole file is skipped and must
// be reported as NOT_RUN, never as passed.

// approvalDecisionTestDirectory installs a tenant-scoped trusted directory so
// both the initiator and independent approvers have server-resolved Decide
// grants. The built-in demo directory binds a case grant only to the contract
// creator, which intentionally makes an independent demo approver impossible;
// this fixture models an operator-managed enterprise directory instead.
func approvalDecisionTestDirectory(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "directory.json")
	// tenant scope is a server-resolved grant, not a browser claim.
	body := `{"users":[
		{"id":"demo-admin","display_name":"演示管理员","role":"admin","tenant_id":"local-demo","org_id":"demo-org","enabled":true,"issuer":"directory-demo","subject":"admin-demo","grants":[{"role":"case_owner","scope":"tenant"}]},
		{"id":"demo-reviewer","display_name":"演示审查员","role":"reviewer","tenant_id":"local-demo","org_id":"demo-org","enabled":true,"issuer":"directory-demo","subject":"reviewer-demo","grants":[{"role":"reviewer","scope":"tenant"}]},
		{"id":"demo-viewer","display_name":"演示只读","role":"viewer","tenant_id":"local-demo","org_id":"demo-org","enabled":true,"issuer":"directory-demo","subject":"viewer-demo","grants":[{"role":"viewer","scope":"tenant"}]}
	]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(path); err != nil {
		t.Fatalf("configure approval decision directory: %v", err)
	}
	t.Cleanup(func() { _ = accounts.ConfigureDirectory("") })
}

func newApprovalDecisionEnv(t *testing.T) (durableCaseFixture, PublishWorkflowTemplateInput) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")) == "" || strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL")) == "" {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for approval decision checks")
	}
	approvalDecisionTestDirectory(t)
	fixture, _ := newContractDraftTestEnv(t)
	template := validApprovalTemplate()
	if _, err := fixture.core.PublishWorkflowTemplate(context.Background(), fixture.admin, template); err != nil {
		t.Fatalf("publish template: %v", err)
	}
	for i, role := range approvalTestRoleCodes(template) {
		userIDs := []string{"demo-reviewer"}
		if i == len(template.Nodes)-1 {
			// The final check may be performed by the initiator.
			userIDs = []string{"demo-admin", "demo-reviewer"}
		}
		if _, err := fixture.core.ReplaceWorkflowRoleBindings(context.Background(), fixture.admin,
			ReplaceWorkflowRoleInput{RoleCode: role, Bindings: []WorkflowRoleBindingInput{{OrgScope: "", UserIDs: userIDs}}}); err != nil {
			t.Fatalf("bind role %s: %v", role, err)
		}
	}
	return fixture, template
}

func submitApprovalForDecisionTest(t *testing.T, fixture durableCaseFixture, key string) (string, SubmitContractApprovalResult) {
	t.Helper()
	contractID, frozen := freezeTestContract(t, fixture)
	result, err := fixture.core.SubmitContractApproval(context.Background(), fixture.admin, SubmitContractApprovalInput{
		ContractID: contractID, ExpectedRowVersion: frozen.RowVersion, IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) != clmApprovalNodeCount || result.Round.State != "in_review" {
		t.Fatalf("submission result %+v", result)
	}
	return contractID, result
}

func approvalTestUser(t *testing.T, id string) accounts.User {
	t.Helper()
	user, ok := accounts.ByID(id)
	if !ok {
		t.Fatalf("directory user %s missing", id)
	}
	return user
}

func TestSequentialApprovalDecisionsAdvanceAndCompleteRound(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	ctx := context.Background()
	contractID, submitted := submitApprovalForDecisionTest(t, fixture, "decision-submit-0001")
	roundVersion := submitted.Round.RowVersion
	for i, task := range submitted.Tasks {
		actor := approvalTestUser(t, "demo-reviewer")
		if task.Final {
			actor = fixture.admin
		}
		result, err := fixture.core.DecideApprovalTask(ctx, actor, DecideApprovalTaskInput{
			TaskID: task.ApprovalTaskID, Decision: clmDecisionApprove,
			ExpectedRoundRowVersion: roundVersion,
			IdempotencyKey:          fmt.Sprintf("decision-approve-%02d", i),
		})
		if err != nil {
			t.Fatalf("node %d decision: %v", task.NodeOrder, err)
		}
		if result.Task.State != "approved" || result.Round.RowVersion != roundVersion+1 {
			t.Fatalf("node %d result %+v", task.NodeOrder, result)
		}
		roundVersion = result.Round.RowVersion
		switch {
		case task.Final:
			if result.WorkflowState != "approved" || result.Round.State != "approved" ||
				result.ContractState != "ready_to_sign" || result.NextTask != nil {
				t.Fatalf("final decision %+v", result)
			}
		default:
			if result.WorkflowState != "in_review" || result.Round.State != "in_review" ||
				result.NextTask == nil || result.NextTask.ApprovalTaskID != submitted.Tasks[i+1].ApprovalTaskID {
				t.Fatalf("advance at node %d: %+v", task.NodeOrder, result)
			}
		}
	}
	var decisions, approvedTasks, sealedRevisions int
	if err := fixture.core.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM clm_approval_decisions WHERE tenant_id=$1 AND contract_id=$2),
		(SELECT count(*) FROM clm_approval_tasks WHERE tenant_id=$1 AND contract_id=$2 AND state='approved'),
		(SELECT count(*) FROM clm_contract_version_seals WHERE tenant_id=$1 AND contract_id=$2)`,
		fixture.admin.TenantID, contractID).Scan(&decisions, &approvedTasks, &sealedRevisions); err != nil {
		t.Fatal(err)
	}
	if decisions != clmApprovalNodeCount || approvedTasks != clmApprovalNodeCount || sealedRevisions != 1 {
		t.Fatalf("decisions=%d approved_tasks=%d seals=%d", decisions, approvedTasks, sealedRevisions)
	}
	var lifecycle, roundState string
	if err := fixture.core.pool.QueryRow(ctx, `SELECT contract.lifecycle_state, round.state
		FROM clm_contracts contract JOIN clm_approval_rounds round
		  ON round.tenant_id=contract.tenant_id AND round.contract_id=contract.contract_id
		WHERE contract.tenant_id=$1 AND contract.contract_id=$2`,
		fixture.admin.TenantID, contractID).Scan(&lifecycle, &roundState); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "ready_to_sign" || roundState != "approved" {
		t.Fatalf("lifecycle=%s round=%s", lifecycle, roundState)
	}
	// Decisions are evidence: the append-only trigger must refuse a rewrite.
	if _, err := fixture.core.pool.Exec(ctx, `UPDATE clm_approval_decisions SET comment='rewritten'
		WHERE tenant_id=$1 AND contract_id=$2`, fixture.admin.TenantID, contractID); err == nil {
		t.Fatal("approval decisions must be append-only")
	}
}

func TestApprovalDecisionRejectsStaleVersionAndWrongActor(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	ctx := context.Background()
	_, submitted := submitApprovalForDecisionTest(t, fixture, "decision-submit-0002")
	reviewer := approvalTestUser(t, "demo-reviewer")
	viewer := approvalTestUser(t, "demo-viewer")
	first, second, third := submitted.Tasks[0], submitted.Tasks[1], submitted.Tasks[2]

	// A future node cannot be decided before it becomes active.
	_, err := fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
		TaskID: third.ApprovalTaskID, Decision: clmDecisionApprove, ExpectedRoundRowVersion: submitted.Round.RowVersion,
		IdempotencyKey: "decision-future-0001",
	})
	assertFaultStatus(t, err, 409, "future node")

	// A viewer never receives Decide authority even with a frozen assignment
	// lookalike path; the assignment check also rejects unrelated users.
	_, err = fixture.core.DecideApprovalTask(ctx, viewer, DecideApprovalTaskInput{
		TaskID: first.ApprovalTaskID, Decision: clmDecisionApprove, ExpectedRoundRowVersion: submitted.Round.RowVersion,
		IdempotencyKey: "decision-viewer-0001",
	})
	assertFaultStatus(t, err, 403, "viewer")

	advanced, err := fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
		TaskID: first.ApprovalTaskID, Decision: clmDecisionApprove, ExpectedRoundRowVersion: submitted.Round.RowVersion,
		IdempotencyKey: "decision-stale-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
		TaskID: second.ApprovalTaskID, Decision: clmDecisionApprove,
		ExpectedRoundRowVersion: submitted.Round.RowVersion,
		IdempotencyKey:          "decision-stale-0002",
	})
	assertFaultStatus(t, err, 409, "stale round version")
	_, err = fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
		TaskID: second.ApprovalTaskID, Decision: clmDecisionApprove,
		ExpectedRoundRowVersion: advanced.Round.RowVersion,
		IdempotencyKey:          "decision-stale-0003",
	})
	if err != nil {
		t.Fatalf("current round version must be accepted: %v", err)
	}
}

func TestApprovalDecisionIdempotencyAndReturnCancelsRemainingTasks(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	ctx := context.Background()
	contractID, submitted := submitApprovalForDecisionTest(t, fixture, "decision-submit-0003")
	reviewer := approvalTestUser(t, "demo-reviewer")
	first, second := submitted.Tasks[0], submitted.Tasks[1]

	replayed, err := fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
		TaskID: first.ApprovalTaskID, Decision: clmDecisionApprove, ExpectedRoundRowVersion: submitted.Round.RowVersion,
		IdempotencyKey: "decision-replay-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
		TaskID: first.ApprovalTaskID, Decision: clmDecisionApprove, ExpectedRoundRowVersion: submitted.Round.RowVersion,
		IdempotencyKey: "decision-replay-0001",
	})
	if err != nil || again.DecisionID != replayed.DecisionID {
		t.Fatalf("same key and payload must replay: %+v err=%v", again, err)
	}
	_, err = fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
		TaskID: first.ApprovalTaskID, Decision: clmDecisionReturn, Comment: "不同意",
		ExpectedRoundRowVersion: submitted.Round.RowVersion,
		IdempotencyKey:          "decision-replay-0001",
	})
	assertFaultStatus(t, err, 409, "same key different payload")

	returned, err := fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
		TaskID: second.ApprovalTaskID, Decision: clmDecisionReturn, Comment: "金额条款需要复核",
		ExpectedRoundRowVersion: replayed.Round.RowVersion,
		IdempotencyKey:          "decision-return-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if returned.WorkflowState != "returned" || returned.Round.State != "returned" ||
		returned.Round.EndedAt == nil || returned.ContractState != "draft" {
		t.Fatalf("return result %+v", returned)
	}
	var cancelled int
	if err := fixture.core.pool.QueryRow(ctx, `SELECT count(*) FROM clm_approval_tasks
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND state='cancelled'`,
		fixture.admin.TenantID, contractID, submitted.Round.ApprovalRoundID).Scan(&cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled != clmApprovalNodeCount-2 {
		t.Fatalf("pending tasks cancelled=%d", cancelled)
	}
	// A returned round is history. A new submission creates round 2 and must
	// leave round 1 decisions intact.
	detail, err := fixture.core.LoadContractDetail(ctx, fixture.admin, contractID)
	if err != nil {
		t.Fatal(err)
	}
	resubmitted, err := fixture.core.SubmitContractApproval(ctx, fixture.admin, SubmitContractApprovalInput{
		ContractID: contractID, ExpectedRowVersion: detail.Summary.RowVersion,
		IdempotencyKey: "decision-submit-0004",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resubmitted.Round.RoundNumber != submitted.Round.RoundNumber+1 {
		t.Fatalf("new round number %d", resubmitted.Round.RoundNumber)
	}
	var firstRoundDecisions int
	if err := fixture.core.pool.QueryRow(ctx, `SELECT count(*) FROM clm_approval_decisions
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3`,
		fixture.admin.TenantID, contractID, submitted.Round.ApprovalRoundID).Scan(&firstRoundDecisions); err != nil {
		t.Fatal(err)
	}
	if firstRoundDecisions != 2 {
		t.Fatalf("old round decisions changed: %d", firstRoundDecisions)
	}
}

// A stale page and a double click are the same race: both carry the round
// row_version they saw. The contract advisory lock serializes them, so exactly
// one decision commits and the other receives 409; no ghost successor task is
// created.
func TestConcurrentApprovalDecisionsCommitExactlyOnce(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	ctx := context.Background()
	contractID, submitted := submitApprovalForDecisionTest(t, fixture, "decision-race-submit")
	reviewer := approvalTestUser(t, "demo-reviewer")
	type outcome struct {
		result ApprovalDecisionResult
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for index := 0; index < 2; index++ {
		go func(index int) {
			<-start
			result, err := fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
				TaskID: submitted.Tasks[0].ApprovalTaskID, Decision: clmDecisionApprove,
				Comment: "并发决定", ExpectedRoundRowVersion: submitted.Round.RowVersion,
				IdempotencyKey: fmt.Sprintf("decision-race-%02d", index),
			})
			results <- outcome{result: result, err: err}
		}(index)
	}
	close(start)
	successes, conflicts := 0, 0
	for index := 0; index < 2; index++ {
		select {
		case got := <-results:
			switch {
			case got.err == nil:
				successes++
			case faultStatus(got.err) == 409:
				conflicts++
			default:
				t.Fatalf("unexpected concurrent decision error: %v", got.err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	var decisions, approved, active int
	if err := fixture.core.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM clm_approval_decisions WHERE tenant_id=$1 AND contract_id=$2),
		(SELECT count(*) FROM clm_approval_tasks WHERE tenant_id=$1 AND contract_id=$2 AND state='approved'),
		(SELECT count(*) FROM clm_approval_tasks WHERE tenant_id=$1 AND contract_id=$2 AND state='active')`,
		fixture.admin.TenantID, contractID).Scan(&decisions, &approved, &active); err != nil {
		t.Fatal(err)
	}
	if decisions != 1 || approved != 1 || active != 1 {
		t.Fatalf("decisions=%d approved=%d active=%d", decisions, approved, active)
	}
}
