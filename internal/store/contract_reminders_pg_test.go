// File metadata (manifest mirror):
// Purpose: Exercises PostgreSQL fulfillment reminder lifecycle, authorization and worker delivery using service roles.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

// Exercises the actual frozen contract, Core and Execution roles; the broker
// is not a person-delivery signal and is deliberately not used here.
func TestContractReminderSingleHostLifecyclePostgres(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	testCLMDataKey(t)
	ctx := context.Background()
	draft := freezeCompleteDraft()
	future := time.Now().UTC().AddDate(0, 0, 2).Format("2006-01-02")
	past := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	draft.Payments = []DraftPaymentPlan{{Sequence: 1, PlannedDate: future, Amount: "50000.00", AdvanceAmount: "0.00", UnitAmount: "50000.00", FiscalAmount: "0.00"}, {Sequence: 2, PlannedDate: past, Amount: "50000.00", AdvanceAmount: "0.00", UnitAmount: "50000.00", FiscalAmount: "0.00"}}
	detail, err := fixture.core.CreateContractDraft(ctx, fixture.admin, CreateContractDraftInput{AuthorityMode: "native", Draft: draft, IdempotencyKey: "reminder-create-contract"})
	if err != nil {
		t.Fatal(err)
	}
	contractID := detail.Summary.ContractID
	frozen, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{CaseID: fixture.caseID, ContractID: contractID, ExpectedRowVersion: detail.Summary.RowVersion, IdempotencyKey: "reminder-freeze-contract"})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := fixture.core.SubmitContractApproval(ctx, fixture.admin, SubmitContractApprovalInput{ContractID: contractID, ExpectedRowVersion: frozen.RowVersion, IdempotencyKey: "reminder-submit-contract"})
	if err != nil {
		t.Fatal(err)
	}
	var version int64
	for i, task := range approval.Tasks {
		actor := approvalTestUser(t, "demo-reviewer", fixture.core)
		if task.Final {
			actor = fixture.admin
		}
		current, e := fixture.core.GetApprovalTask(ctx, actor, task.ApprovalTaskID)
		if e != nil {
			t.Fatal(e)
		}
		decision, e := fixture.core.DecideApprovalTask(ctx, actor, DecideApprovalTaskInput{TaskID: task.ApprovalTaskID, Decision: clmDecisionApprove, ExpectedRoundRowVersion: current.RoundDetail.Round.RowVersion, IdempotencyKey: fmt.Sprintf("reminder-approval-%02d", i)})
		if e != nil {
			t.Fatal(e)
		}
		version = decision.RowVersion
	}
	body := []byte("人工确认签署材料")
	upload, err := fixture.document.UploadMaterial(ctx, fixture.admin, fixture.caseID, "attachment", "reminder-signed.txt", "text/plain", body, MaterialUploadOptions{IdempotencyKey: "reminder-signed-material"})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	signed := MaterialRef{MaterialID: upload.MaterialID, MaterialVersionID: upload.DocumentID, SHA256: digest, Purpose: "signed_contract"}
	registered, err := fixture.core.RegisterContractSigning(ctx, fixture.admin, ContractSigningRegistrationInput{ContractID: contractID, ExpectedRowVersion: version, BaselineRevisionID: frozen.Version.ContractRevisionID, SigningDate: time.Now().UTC().Format("2006-01-02"), SignedEvidence: signed, SignedContent: draft, ManualComparison: ManualSigningComparison{Confirmed: true, SignedMaterialDigest: digest, ContractRevisionID: frozen.Version.ContractRevisionID, Scope: clmSigningComparisonScope}, Parties: []ContractSigningParty{{Role: "buyer", Name: "采购方"}}, IdempotencyKey: "reminder-register-signing"})
	if err != nil {
		t.Fatal(err)
	}
	input := ReminderCreateInput{ContractRevisionID: frozen.Version.ContractRevisionID, PlanKind: "payment", PlanSequence: 1, Timezone: "UTC", RecipientID: fixture.admin.ID, ExpectedContractRowVersion: registered.RowVersion}
	first, err := fixture.core.CreateContractReminder(ctx, fixture.admin, contractID, "reminder-future-create", input)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := fixture.core.CreateContractReminder(ctx, fixture.admin, contractID, "reminder-future-create", input)
	if err != nil || replay.ReminderID != first.ReminderID {
		t.Fatalf("idempotent replay: %+v %v", replay, err)
	}
	var jobID string
	var ready, deadline time.Time
	tx, err := fixture.environment.harness.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = setLocalScope(ctx, tx, userScope(fixture.admin)); err != nil {
		t.Fatal(err)
	}
	err = tx.QueryRow(ctx, `SELECT j.id,j.ready_at,j.deadline_at FROM worker_jobs j JOIN clm_fulfillment_reminders r ON r.job_id=j.id WHERE r.tenant_id=$1 AND r.reminder_id=$2`, fixture.admin.TenantID, first.ReminderID).Scan(&jobID, &ready, &deadline)
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	if !ready.Equal(first.DueAt) || !deadline.After(ready) || !ready.After(time.Now()) {
		t.Fatalf("future queue bounds ready=%v due=%v deadline=%v", ready, first.DueAt, deadline)
	}
	cancelled, err := fixture.core.CancelContractReminder(ctx, fixture.admin, contractID, first.ReminderID, "reminder-cancel-future", ReminderCancelInput{ExpectedVersion: first.Version})
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("cancel=%+v %v", cancelled, err)
	}
	tx, err = fixture.environment.harness.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = setLocalScope(ctx, tx, userScope(fixture.admin)); err != nil {
		t.Fatal(err)
	}
	var jobState string
	err = tx.QueryRow(ctx, `SELECT state FROM worker_jobs WHERE id=$1`, jobID).Scan(&jobState)
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	if jobState != "cancelled" {
		t.Fatalf("cancelled reminder job state=%s", jobState)
	}
	input.PlanSequence = 2
	due, err := fixture.core.CreateContractReminder(ctx, fixture.admin, contractID, "reminder-due-create", input)
	if err != nil {
		t.Fatal(err)
	}
	execution := fixture.environment.openRole(t, "execution")
	claim, err := execution.ClaimNextJob(ctx, "reminder-test-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Kind != "fulfillment_reminder" || claim.CaseID != "" || claim.AttemptID != "" {
		t.Fatalf("wrong claim %+v", claim)
	}
	wire := ReminderClaim{TenantID: claim.TenantID, OrgID: claim.OrgID, JobID: claim.JobID, WorkerID: claim.WorkerID, Generation: claim.Generation}
	if err = fixture.core.DeliverReminderClaim(ctx, wire); err != nil {
		t.Fatal(err)
	}
	if err = fixture.core.DeliverReminderClaim(ctx, wire); err == nil {
		t.Fatal("completed claim was reused")
	}
	inbox, err := fixture.core.ListReminderInbox(ctx, fixture.admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.Items) != 1 || inbox.Items[0].ReminderID != due.ReminderID {
		t.Fatalf("incorrect inbox %+v", inbox)
	}
	// Revoke the exact recipient membership after admission, before dispatch.
	_, err = fixture.core.CancelContractReminder(ctx, fixture.admin, contractID, due.ReminderID, "reminder-cancel-delivered", ReminderCancelInput{ExpectedVersion: due.Version})
	if err != nil {
		t.Fatal(err)
	}
	denied, err := fixture.core.CreateContractReminder(ctx, fixture.admin, contractID, "reminder-revoked-create", input)
	if err != nil {
		t.Fatal(err)
	}
	revoke := approvalIdentityCrosswalk()
	for i := range revoke.Memberships {
		if revoke.Memberships[i].ActorID == fixture.admin.ID {
			revoke.Memberships[i].Status = "revoked"
		}
	}
	importApprovalIdentity(t, fixture.core, revoke)
	claim, err = execution.ClaimNextJob(ctx, "reminder-revoked-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	wire = ReminderClaim{TenantID: claim.TenantID, OrgID: claim.OrgID, JobID: claim.JobID, WorkerID: claim.WorkerID, Generation: claim.Generation}
	if err = fixture.core.DeliverReminderClaim(ctx, wire); err != nil {
		t.Fatal(err)
	}
	tx, err = fixture.environment.harness.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = setLocalScope(ctx, tx, userScope(fixture.admin)); err != nil {
		t.Fatal(err)
	}
	var attempts, deliveries int
	err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM clm_fulfillment_reminder_attempts WHERE tenant_id=$1 AND reminder_id=$2 AND status='denied'),(SELECT count(*) FROM clm_fulfillment_reminder_deliveries WHERE tenant_id=$1 AND reminder_id=$2)`, fixture.admin.TenantID, denied.ReminderID).Scan(&attempts, &deliveries)
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	if attempts != 1 || deliveries != 0 {
		t.Fatalf("revoked delivery attempts=%d deliveries=%d", attempts, deliveries)
	}
}
