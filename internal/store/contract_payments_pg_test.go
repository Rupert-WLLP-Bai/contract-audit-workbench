package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

// This one focused PostgreSQL path proves prepayment eligibility, a tail
// payment block before acceptance, authenticated-source event replay/conflict,
// append-only partial reversals, and exact net payment projection.
func TestContractPaymentEligibilityAndTrustedReceiptFacts(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	testCLMDataKey(t)
	ctx := context.Background()
	draft := freezeCompleteDraft()
	advance, tail := true, false
	draft.Payments = []DraftPaymentPlan{
		{Sequence: 1, IsAdvance: &advance, Amount: "20000.00", AdvanceAmount: "20000.00", UnitAmount: "20000.00", FiscalAmount: "0.00",
			Condition: &PaymentCondition{Type: "signed_contract", DueRule: &DueRule{Trigger: "contract_signed"}}},
		{Sequence: 2, IsAdvance: &tail, Amount: "79999.99", AdvanceAmount: "0.00", UnitAmount: "79999.99", FiscalAmount: "0.00",
			Condition: &PaymentCondition{Type: "all_goods_accepted", DueRule: &DueRule{Trigger: "acceptance_passed"}}},
		{Sequence: 3, IsAdvance: &tail, Amount: "0.01", AdvanceAmount: "0.00", UnitAmount: "0.01", FiscalAmount: "0.00",
			Condition: &PaymentCondition{Type: "signed_contract", PolicyRef: "policy:requires-review", DueRule: &DueRule{Trigger: "contract_signed"}}},
	}
	detail, err := fixture.core.CreateContractDraft(ctx, fixture.admin, CreateContractDraftInput{AuthorityMode: "native", Draft: draft, IdempotencyKey: "payment-test-create"})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{ContractID: detail.Summary.ContractID, CaseID: fixture.caseID, ExpectedRowVersion: detail.Summary.RowVersion, IdempotencyKey: "payment-test-freeze"})
	if err != nil {
		t.Fatal(err)
	}
	contractID := detail.Summary.ContractID
	submitted, err := fixture.core.SubmitContractApproval(ctx, fixture.admin, SubmitContractApprovalInput{ContractID: contractID, ExpectedRowVersion: frozen.RowVersion, IdempotencyKey: "payment-test-approval-submit"})
	if err != nil {
		t.Fatal(err)
	}
	var contractVersion int64
	for index, task := range submitted.Tasks {
		actor := approvalTestUser(t, "demo-reviewer")
		if task.Final {
			actor = fixture.admin
		}
		current, e := fixture.core.GetApprovalTask(ctx, actor, task.ApprovalTaskID)
		if e != nil {
			t.Fatalf("refresh approval task: %v", e)
		}
		decision, e := fixture.core.DecideApprovalTask(ctx, actor, DecideApprovalTaskInput{TaskID: task.ApprovalTaskID, Decision: clmDecisionApprove, ExpectedRoundRowVersion: current.RoundDetail.Round.RowVersion, IdempotencyKey: fmt.Sprintf("payment-test-decision-%02d", index)})
		if e != nil {
			t.Fatalf("approve node %d: %v", task.NodeOrder, e)
		}
		contractVersion = decision.RowVersion
	}
	body := []byte("批准签署的付款测试材料")
	upload, err := fixture.document.UploadMaterial(ctx, fixture.admin, fixture.caseID, "attachment", "payment-signed.txt", "text/plain", body, MaterialUploadOptions{IdempotencyKey: "payment-test-signing-material"})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	material := MaterialRef{MaterialID: upload.MaterialID, MaterialVersionID: upload.DocumentID, SHA256: digest, Purpose: "signed_contract"}
	registered, err := fixture.core.RegisterContractSigning(ctx, fixture.admin, ContractSigningRegistrationInput{ContractID: contractID, ExpectedRowVersion: contractVersion, BaselineRevisionID: frozen.Version.ContractRevisionID, SigningDate: time.Now().UTC().Format("2006-01-02"), SignedEvidence: material, SignedContent: draft, ManualComparison: ManualSigningComparison{Confirmed: true, SignedMaterialDigest: digest, ContractRevisionID: frozen.Version.ContractRevisionID, Scope: clmSigningComparisonScope}, Parties: []ContractSigningParty{{Role: "buyer", Name: "采购方"}}, IdempotencyKey: "payment-test-register-signing"})
	if err != nil {
		t.Fatal(err)
	}
	policyCheck, err := fixture.core.CheckContractPaymentEligibility(ctx, fixture.admin, ContractPaymentEligibilityInput{ContractID: contractID, RevisionID: frozen.Version.ContractRevisionID, Sequence: 3, ExpectedRowVersion: registered.RowVersion, IdempotencyKey: "payment-test-policy-check"})
	if err != nil || policyCheck.Status != "unknown" {
		t.Fatalf("unresolved frozen policy reference must be unknown: %+v err=%v", policyCheck, err)
	}
	prepay, err := fixture.core.CheckContractPaymentEligibility(ctx, fixture.admin, ContractPaymentEligibilityInput{ContractID: contractID, RevisionID: frozen.Version.ContractRevisionID, Sequence: 1, ExpectedRowVersion: policyCheck.ContractRowVersion, IdempotencyKey: "payment-test-prepay-check"})
	if err != nil || prepay.Status != "eligible" || prepay.InitiatesPayment {
		t.Fatalf("signed prepayment condition: %+v err=%v", prepay, err)
	}
	replayedCheck, err := fixture.core.CheckContractPaymentEligibility(ctx, fixture.admin, ContractPaymentEligibilityInput{ContractID: contractID, RevisionID: frozen.Version.ContractRevisionID, Sequence: 1, ExpectedRowVersion: policyCheck.ContractRowVersion, IdempotencyKey: "payment-test-prepay-check"})
	if err != nil || replayedCheck.CheckID != prepay.CheckID {
		t.Fatalf("eligibility idempotency replay: %+v err=%v", replayedCheck, err)
	}
	tailResult, err := fixture.core.CheckContractPaymentEligibility(ctx, fixture.admin, ContractPaymentEligibilityInput{ContractID: contractID, RevisionID: frozen.Version.ContractRevisionID, Sequence: 2, ExpectedRowVersion: prepay.ContractRowVersion, IdempotencyKey: "payment-test-tail-check"})
	if err != nil || tailResult.Status != "blocked" || len(tailResult.Reasons) == 0 {
		t.Fatalf("tail without accepted quantity must block: %+v err=%v", tailResult, err)
	}
	noReceipts, err := fixture.core.ListContractPayments(ctx, fixture.admin, contractID)
	if err != nil || len(noReceipts) != 3 || noReceipts[0].State != "unknown" || noReceipts[0].NetPaid != "0.00" {
		t.Fatalf("no external receipt must remain unknown: %+v err=%v", noReceipts, err)
	}

	source := TrustedPaymentSource{TenantID: fixture.admin.TenantID, SourceID: "test-finance-fixture", KeyID: "test-key-01"}
	receiptInput := ContractPaymentReceiptInput{ContractID: contractID, RevisionID: frozen.Version.ContractRevisionID, Sequence: 1, EventID: "finance-event-001", EventType: "payment", Amount: "20000.00", Currency: "CNY", OccurredAt: time.Now().UTC()}
	_, _, crossTenantErr := fixture.core.RecordTrustedContractPaymentReceipt(ctx, TrustedPaymentSource{TenantID: "different-tenant", SourceID: "other-test-source", KeyID: "other-key-01"}, receiptInput)
	assertFaultStatus(t, crossTenantErr, 404, "cross-tenant source cannot resolve contract")
	receipt, replayed, err := fixture.core.RecordTrustedContractPaymentReceipt(ctx, source, receiptInput)
	if err != nil || replayed || receipt.SourceEventID != receiptInput.EventID || receipt.SourceKeyID != source.KeyID {
		t.Fatalf("authenticated fixture receipt: %+v replay=%v err=%v", receipt, replayed, err)
	}
	_, replayed, err = fixture.core.RecordTrustedContractPaymentReceipt(ctx, source, receiptInput)
	if err != nil || !replayed {
		t.Fatalf("source event replay: replay=%v err=%v", replayed, err)
	}
	rotatedSource := source
	rotatedSource.KeyID = "test-key-02"
	rotatedReceipt, rotationReplay, err := fixture.core.RecordTrustedContractPaymentReceipt(ctx, rotatedSource, receiptInput)
	if err != nil || !rotationReplay || rotatedReceipt.SourceKeyID != source.KeyID {
		t.Fatalf("event replay after credential rotation: %+v replay=%v err=%v", rotatedReceipt, rotationReplay, err)
	}
	changed := receiptInput
	changed.Amount = "20001.00"
	_, _, err = fixture.core.RecordTrustedContractPaymentReceipt(ctx, source, changed)
	assertFaultStatus(t, err, 409, "source event payload collision")
	partialReversal := ContractPaymentReceiptInput{ContractID: contractID, RevisionID: frozen.Version.ContractRevisionID, Sequence: 1, EventID: "finance-reversal-001", EventType: "reversal", OriginalEventID: receiptInput.EventID, Amount: "5000.00", Currency: "CNY", OccurredAt: time.Now().UTC()}
	if _, _, err = fixture.core.RecordTrustedContractPaymentReceipt(ctx, source, partialReversal); err != nil {
		t.Fatal(err)
	}
	summary, err := fixture.core.ListContractPayments(ctx, fixture.admin, contractID)
	if err != nil || len(summary) != 3 || summary[0].NetPaid != "15000.00" || summary[0].State != "partial" {
		t.Fatalf("partial reversal projection: %+v err=%v", summary, err)
	}
	partialReversal.EventID = "finance-reversal-002"
	partialReversal.Amount = "15000.00"
	if _, _, err = fixture.core.RecordTrustedContractPaymentReceipt(ctx, source, partialReversal); err != nil {
		t.Fatal(err)
	}
	partialReversal.EventID = "finance-reversal-overrun"
	partialReversal.Amount = "0.01"
	_, _, err = fixture.core.RecordTrustedContractPaymentReceipt(ctx, source, partialReversal)
	assertFaultStatus(t, err, 422, "over-reversal must be rejected")
	summary, err = fixture.core.ListContractPayments(ctx, fixture.admin, contractID)
	if err != nil || summary[0].NetPaid != "0.00" || summary[0].State != "unpaid" {
		t.Fatalf("fully reversed net projection: %+v err=%v", summary, err)
	}
	var facts, events, outbox int
	if err = fixture.core.pool.QueryRow(ctx, `SELECT count(*) FROM clm_contract_payment_receipts WHERE tenant_id=$1 AND contract_id=$2`, fixture.admin.TenantID, contractID).Scan(&facts); err != nil {
		t.Fatal(err)
	}
	if err = fixture.core.pool.QueryRow(ctx, `SELECT count(*) FROM clm_business_events WHERE tenant_id=$1 AND contract_id=$2 AND event_type=$3`, fixture.admin.TenantID, contractID, clmEventPaymentReceipt).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err = fixture.core.pool.QueryRow(ctx, `SELECT count(*) FROM clm_outbox_delivery WHERE tenant_id=$1 AND event_id IN (SELECT event_id FROM clm_business_events WHERE tenant_id=$1 AND contract_id=$2 AND event_type=$3)`, fixture.admin.TenantID, contractID, clmEventPaymentReceipt).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if facts != 3 || events != 3 || outbox != 3 {
		t.Fatalf("receipt/reversal and event/outbox count facts=%d events=%d outbox=%d", facts, events, outbox)
	}
	rowVersion := registered.RowVersion
	if err = fixture.core.pool.QueryRow(ctx, `SELECT row_version FROM clm_contracts WHERE tenant_id=$1 AND contract_id=$2`, fixture.admin.TenantID, contractID).Scan(&rowVersion); err != nil {
		t.Fatal(err)
	}
	beforeVersion := rowVersion
	if err = fixture.core.UseServiceOwner("worker"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = fixture.core.RecordTrustedContractPaymentReceipt(ctx, source, ContractPaymentReceiptInput{ContractID: contractID, RevisionID: frozen.Version.ContractRevisionID, Sequence: 1, EventID: "noncore-forbidden-event", EventType: "payment", Amount: "1.00", Currency: "CNY", OccurredAt: time.Now().UTC()}); err == nil {
		t.Fatal("non-core service owner accepted machine receipt write")
	}
	if err = fixture.core.UseServiceOwner("core"); err != nil {
		t.Fatal(err)
	}
	fixture.core.UseReadOnly(true)
	beforeFacts, beforeEvents, beforeOutbox := facts, events, outbox
	if _, _, err = fixture.core.RecordTrustedContractPaymentReceipt(ctx, source, ContractPaymentReceiptInput{ContractID: contractID, RevisionID: frozen.Version.ContractRevisionID, Sequence: 1, EventID: "readonly-forbidden-event", EventType: "payment", Amount: "1.00", Currency: "CNY", OccurredAt: time.Now().UTC()}); err == nil {
		t.Fatal("read-only store accepted machine receipt write")
	}
	readonlySummary, err := fixture.core.ListContractPayments(ctx, fixture.admin, contractID)
	if err != nil || len(readonlySummary) != 3 || readonlySummary[0].State != "unpaid" {
		t.Fatalf("read-only store must allow authorized history reads: %+v err=%v", readonlySummary, err)
	}
	if err = fixture.core.pool.QueryRow(ctx, `SELECT row_version FROM clm_contracts WHERE tenant_id=$1 AND contract_id=$2`, fixture.admin.TenantID, contractID).Scan(&rowVersion); err != nil {
		t.Fatal(err)
	}
	if rowVersion != beforeVersion {
		t.Fatalf("rejected readonly receipt changed contract version: %d -> %d", beforeVersion, rowVersion)
	}
	if err = fixture.core.pool.QueryRow(ctx, `SELECT count(*) FROM clm_contract_payment_receipts WHERE tenant_id=$1 AND contract_id=$2`, fixture.admin.TenantID, contractID).Scan(&facts); err != nil {
		t.Fatal(err)
	}
	if err = fixture.core.pool.QueryRow(ctx, `SELECT count(*) FROM clm_business_events WHERE tenant_id=$1 AND contract_id=$2 AND event_type=$3`, fixture.admin.TenantID, contractID, clmEventPaymentReceipt).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err = fixture.core.pool.QueryRow(ctx, `SELECT count(*) FROM clm_outbox_delivery WHERE tenant_id=$1 AND event_id IN (SELECT event_id FROM clm_business_events WHERE tenant_id=$1 AND contract_id=$2 AND event_type=$3)`, fixture.admin.TenantID, contractID, clmEventPaymentReceipt).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if facts != beforeFacts || events != beforeEvents || outbox != beforeOutbox {
		t.Fatalf("rejected readonly receipt changed facts/events/outbox: %d/%d/%d -> %d/%d/%d", beforeFacts, beforeEvents, beforeOutbox, facts, events, outbox)
	}
}
