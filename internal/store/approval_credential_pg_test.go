package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"contractaudit/internal/approval"
	"contractaudit/internal/fault"
)

// CLM-11 durable checks: a decision may only be recorded with a credential
// bound to the same subject, task, revision, snapshot and row versions, and a
// transferred task belongs to its latest target only. Without a database these
// checks are skipped and must be reported as NOT_RUN.

func enableApprovalTestKeyring(t *testing.T, fixture durableCaseFixture) *approval.Keyring {
	t.Helper()
	key := bytes.Repeat([]byte("k"), 32)
	keyring, err := approval.NewKeyring("test-kid", map[string][]byte{"test-kid": key}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	fixture.core.approvalKeyring = keyring
	t.Cleanup(func() { fixture.core.approvalKeyring = nil })
	return keyring
}

func requireFaultStatus(t *testing.T, err error, status int) {
	t.Helper()
	var apiErr *fault.Error
	if !errors.As(err, &apiErr) || apiErr.Status != status {
		t.Fatalf("want HTTP %d, got %v", status, err)
	}
}

func TestApprovalDecisionRequiresCredentialBoundToTaskRevisionAndRowVersions(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	enableApprovalTestKeyring(t, fixture)
	ctx := context.Background()
	contractID, submitted := submitApprovalForDecisionTest(t, fixture, "credential-submit-0001")
	reviewer := approvalTestUser(t, "demo-reviewer")
	first := submitted.Tasks[0]
	roundVersion := submitted.Round.RowVersion

	issued, err := fixture.core.IssueApprovalCredential(ctx, reviewer, first.ApprovalTaskID)
	if err != nil {
		t.Fatalf("issue credential: %v", err)
	}
	if issued.Credential == "" || issued.Claims.TaskID != first.ApprovalTaskID ||
		issued.Claims.ContractID != contractID || issued.Claims.TaskRowVersion < 1 ||
		issued.Claims.SnapshotDigest == "" || issued.Claims.AmountLimit == "" ||
		issued.Claims.Role != reviewer.Role || issued.Claims.OrgID != reviewer.OrgID {
		t.Fatalf("issued credential %+v", issued)
	}

	_, err = fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
		TaskID: first.ApprovalTaskID, Decision: clmDecisionApprove,
		ExpectedRoundRowVersion: roundVersion, IdempotencyKey: "credential-missing-0001",
	})
	requireFaultStatus(t, err, 403)

	_, err = fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
		TaskID: first.ApprovalTaskID, Decision: clmDecisionApprove,
		ExpectedRoundRowVersion: roundVersion, Credential: issued.Credential + "x",
		IdempotencyKey: "credential-tampered-0001",
	})
	requireFaultStatus(t, err, 403)

	decided, err := fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
		TaskID: first.ApprovalTaskID, Decision: clmDecisionApprove,
		ExpectedRoundRowVersion: roundVersion, Credential: issued.Credential,
		IdempotencyKey: "credential-valid-0001",
	})
	if err != nil {
		t.Fatalf("decision with credential: %v", err)
	}
	if decided.Task.State != "approved" || decided.Round.RowVersion != roundVersion+1 {
		t.Fatalf("decision %+v", decided)
	}

	var credentialID, keyID, digest, snapshot, amount, currency, actorOrg string
	var contractRowVersion int64
	if err := fixture.core.pool.QueryRow(ctx, `SELECT credential_id,credential_key_id,credential_digest,
			snapshot_digest,amount_limit::text,currency,contract_row_version,actor_org_id
		FROM clm_approval_decisions WHERE tenant_id=$1 AND decision_id=$2`,
		fixture.admin.TenantID, decided.DecisionID).Scan(&credentialID, &keyID, &digest, &snapshot,
		&amount, &currency, &contractRowVersion, &actorOrg); err != nil {
		t.Fatal(err)
	}
	if credentialID == "" || keyID != "test-kid" || digest == "" || snapshot != issued.Claims.SnapshotDigest ||
		amount != issued.Claims.AmountLimit || currency != issued.Claims.Currency ||
		contractRowVersion != issued.Claims.ContractRowVersion || actorOrg != reviewer.OrgID {
		t.Fatalf("stored credential evidence %q %q %q %q %q %q %d %q", credentialID, keyID, digest,
			snapshot, amount, currency, contractRowVersion, actorOrg)
	}

	// The first token is bound to the first task and the old contract row
	// version; advancing the round must not let it approve the next node.
	next := submitted.Tasks[1]
	_, err = fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
		TaskID: next.ApprovalTaskID, Decision: clmDecisionApprove,
		ExpectedRoundRowVersion: decided.Round.RowVersion, Credential: issued.Credential,
		IdempotencyKey: "credential-replay-0001",
	})
	requireFaultStatus(t, err, 403)
}

func TestApprovalTaskMaterialsAreWhitelistedAndDefaultDeny(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	ctx := context.Background()
	var materialID, materialVersionID, materialDigest string
	if err := fixture.core.pool.QueryRow(ctx, `SELECT mv.material_id,d.id,d.digest
		FROM material_versions mv
		JOIN documents d ON d.tenant_id=mv.tenant_id AND d.org_id=mv.org_id
		  AND d.case_id=mv.case_id AND d.id=mv.document_id
		WHERE mv.tenant_id=$1 AND mv.org_id=$2 AND mv.case_id=$3 AND d.role='draft_contract'
		ORDER BY mv.version DESC LIMIT 1`,
		fixture.admin.TenantID, fixture.admin.OrgID, fixture.caseID).
		Scan(&materialID, &materialVersionID, &materialDigest); err != nil {
		t.Fatal(err)
	}
	draft := freezeCompleteDraft()
	draft.Materials = []MaterialRef{{
		MaterialID: materialID, MaterialVersionID: materialVersionID,
		SHA256: materialDigest, Purpose: "draft",
	}}
	created, err := fixture.core.CreateContractDraft(ctx, fixture.admin, CreateContractDraftInput{
		ExpectedRowVersion: 0, AuthorityMode: "native", Draft: draft,
		IdempotencyKey: "material-whitelist-create",
	})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		CaseID: fixture.caseID, ContractID: created.Summary.ContractID,
		ExpectedRowVersion: created.Summary.RowVersion, IdempotencyKey: "material-whitelist-freeze",
	})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := fixture.core.SubmitContractApproval(ctx, fixture.admin, SubmitContractApprovalInput{
		ContractID: created.Summary.ContractID, ExpectedRowVersion: frozen.RowVersion,
		IdempotencyKey: "material-whitelist-submit",
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewer := approvalTestUser(t, "demo-reviewer")
	detail, err := fixture.core.GetApprovalTask(ctx, reviewer, submitted.Tasks[0].ApprovalTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Materials) != 1 || detail.Materials[0].MaterialID != materialID ||
		detail.Materials[0].Digest != materialDigest {
		t.Fatalf("whitelisted materials %+v", detail.Materials)
	}

	// A frozen revision with no material reference produces an empty
	// whitelist, not a fallback to every material on the contract.
	plain, err := fixture.core.CreateContractDraft(ctx, fixture.admin, CreateContractDraftInput{
		ExpectedRowVersion: 0, AuthorityMode: "native", Draft: freezeCompleteDraft(),
		IdempotencyKey: "material-default-create",
	})
	if err != nil {
		t.Fatal(err)
	}
	plainFrozen, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		CaseID: fixture.caseID, ContractID: plain.Summary.ContractID,
		ExpectedRowVersion: plain.Summary.RowVersion, IdempotencyKey: "material-default-freeze",
	})
	if err != nil {
		t.Fatal(err)
	}
	plainSubmitted, err := fixture.core.SubmitContractApproval(ctx, fixture.admin, SubmitContractApprovalInput{
		ContractID: plain.Summary.ContractID, ExpectedRowVersion: plainFrozen.RowVersion,
		IdempotencyKey: "material-default-submit",
	})
	if err != nil {
		t.Fatal(err)
	}
	plainDetail, err := fixture.core.GetApprovalTask(ctx, reviewer, plainSubmitted.Tasks[0].ApprovalTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plainDetail.Materials) != 0 {
		t.Fatalf("empty whitelist exposed materials: %+v", plainDetail.Materials)
	}
}

func TestApprovalTaskTransferIsAppendOnlyAndMovesTheLiveRight(t *testing.T) {
	fixture, template := newApprovalDecisionEnv(t)
	ctx := context.Background()
	firstRole := template.Nodes[0].RoleCode
	if _, err := fixture.core.ReplaceWorkflowRoleBindings(ctx, fixture.admin, ReplaceWorkflowRoleInput{
		RoleCode: firstRole, Bindings: []WorkflowRoleBindingInput{{
			OrgScope: "", UserIDs: []string{"demo-reviewer", "demo-reviewer-2"},
		}},
	}); err != nil {
		t.Fatalf("bind second reviewer: %v", err)
	}
	contractID, submitted := submitApprovalForDecisionTest(t, fixture, "transfer-submit-0001")
	first := submitted.Tasks[0]
	reviewer := approvalTestUser(t, "demo-reviewer")
	replacement := approvalTestUser(t, "demo-reviewer-2")

	var taskRowVersion int64
	if err := fixture.core.pool.QueryRow(ctx, `SELECT row_version FROM clm_approval_tasks
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_task_id=$3`,
		fixture.admin.TenantID, contractID, first.ApprovalTaskID).Scan(&taskRowVersion); err != nil {
		t.Fatal(err)
	}

	_, err := fixture.core.TransferApprovalTask(ctx, reviewer, TransferApprovalTaskInput{
		TaskID: first.ApprovalTaskID, ToUserID: replacement.ID, Reason: "原审批人休假",
		ExpectedTaskRowVersion: taskRowVersion, IdempotencyKey: "transfer-by-reviewer-0001",
	})
	requireFaultStatus(t, err, 403)

	_, err = fixture.core.TransferApprovalTask(ctx, fixture.admin, TransferApprovalTaskInput{
		TaskID: first.ApprovalTaskID, ToUserID: replacement.ID, Reason: "原审批人休假",
		ExpectedTaskRowVersion: taskRowVersion + 1, IdempotencyKey: "transfer-stale-0001",
	})
	requireFaultStatus(t, err, 409)

	transferred, err := fixture.core.TransferApprovalTask(ctx, fixture.admin, TransferApprovalTaskInput{
		TaskID: first.ApprovalTaskID, ToUserID: replacement.ID, Reason: "原审批人休假",
		ExpectedTaskRowVersion: taskRowVersion, IdempotencyKey: "transfer-ok-0001",
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if transferred.FromUserID != reviewer.ID || transferred.ToUserID != replacement.ID ||
		transferred.ActorID != fixture.admin.ID || transferred.TransferID == "" {
		t.Fatalf("transfer result %+v", transferred)
	}

	_, err = fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
		TaskID: first.ApprovalTaskID, Decision: clmDecisionApprove,
		ExpectedRoundRowVersion: submitted.Round.RowVersion, IdempotencyKey: "transfer-old-decide-0001",
	})
	requireFaultStatus(t, err, 403)

	decided, err := fixture.core.DecideApprovalTask(ctx, replacement, DecideApprovalTaskInput{
		TaskID: first.ApprovalTaskID, Decision: clmDecisionApprove,
		ExpectedRoundRowVersion: submitted.Round.RowVersion, IdempotencyKey: "transfer-new-decide-0001",
	})
	if err != nil {
		t.Fatalf("replacement decision: %v", err)
	}
	if decided.Task.State != "approved" || decided.NextTask == nil {
		t.Fatalf("replacement decision %+v", decided)
	}

	var transfers, assignments int
	if err := fixture.core.pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM clm_approval_task_transfers WHERE tenant_id=$1 AND contract_id=$2),
			(SELECT count(*) FROM clm_approval_task_assignments
			 WHERE tenant_id=$1 AND contract_id=$2 AND approval_task_id=$3)`,
		fixture.admin.TenantID, contractID, first.ApprovalTaskID).Scan(&transfers, &assignments); err != nil {
		t.Fatal(err)
	}
	if transfers != 1 || assignments != 2 {
		t.Fatalf("transfers=%d assignments=%d", transfers, assignments)
	}

	_, err = fixture.core.TransferApprovalTask(ctx, fixture.admin, TransferApprovalTaskInput{
		TaskID: first.ApprovalTaskID, ToUserID: reviewer.ID, Reason: "再次转交",
		ExpectedTaskRowVersion: taskRowVersion, IdempotencyKey: "transfer-repeat-0001",
	})
	requireFaultStatus(t, err, 409)
	if _, err := fixture.core.pool.Exec(ctx, `UPDATE clm_approval_task_transfers SET reason='rewritten'
		WHERE tenant_id=$1 AND contract_id=$2`, fixture.admin.TenantID, contractID); err == nil {
		t.Fatal("transfer history accepted an update")
	}
}
