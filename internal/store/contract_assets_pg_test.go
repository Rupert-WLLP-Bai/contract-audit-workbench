// File metadata (manifest mirror):
// Purpose: 实现 `TestContractAssetHandoverAndERPReceiptFacts`, `rewriteAssetRevisionSnapshotForTest` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-03
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// This focused aggregate case pins qualified-range allocation to one frozen
// asset plan, prevents overlap across plans, and separates manual handover from
// authenticated ERP booking.
func TestContractAssetHandoverAndERPReceiptFacts(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	testCLMDataKey(t)
	ctx := context.Background()
	itemID := "c6052c81-8b05-49ce-89e9-f6e9c6403c10"
	fixed := true
	draft := freezeCompleteDraft()
	draft.Contract.IsFixedAsset = &fixed
	draft.Assets = []DraftAssetPlan{
		{AssetPlanID: "70000000-0000-4000-8000-000000000001", ItemID: itemID, Name: "交换机子批次A", Quantity: "3.000000", UnitPrice: "10000.0000", DepartmentCode: "dept-unresolved", DepartmentName: "未核验使用部门", CustodianUser: "demo-admin"},
		{AssetPlanID: "70000000-0000-4000-8000-000000000002", ItemID: itemID, Name: "交换机子批次B", Quantity: "7.000000", UnitPrice: "10000.0000", DepartmentCode: "dept-unresolved", DepartmentName: "未核验使用部门", CustodianUser: "missing-directory-user"},
	}
	detail, err := fixture.core.CreateContractDraft(ctx, fixture.admin, CreateContractDraftInput{AuthorityMode: "native", Draft: draft, IdempotencyKey: "asset-create-contract-0001"})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{ContractID: detail.Summary.ContractID, CaseID: fixture.caseID, ExpectedRowVersion: detail.Summary.RowVersion, IdempotencyKey: "asset-freeze-contract-0001"})
	if err != nil {
		t.Fatal(err)
	}
	contractID, revisionID := detail.Summary.ContractID, frozen.Version.ContractRevisionID
	submitted, err := fixture.core.SubmitContractApproval(ctx, fixture.admin, SubmitContractApprovalInput{ContractID: contractID, ExpectedRowVersion: frozen.RowVersion, IdempotencyKey: "asset-approval-submit-0001"})
	if err != nil {
		t.Fatal(err)
	}
	var approvedVersion int64
	for index, task := range submitted.Tasks {
		actor := approvalTestUser(t, "demo-reviewer", fixture.core)
		if task.Final {
			actor = fixture.admin
		}
		current, e := fixture.core.GetApprovalTask(ctx, actor, task.ApprovalTaskID)
		if e != nil {
			t.Fatal(e)
		}
		decision, e := fixture.core.DecideApprovalTask(ctx, actor, DecideApprovalTaskInput{TaskID: task.ApprovalTaskID, Decision: clmDecisionApprove, ExpectedRoundRowVersion: current.RoundDetail.Round.RowVersion, IdempotencyKey: fmt.Sprintf("asset-approval-%02d", index)})
		if e != nil {
			t.Fatal(e)
		}
		approvedVersion = decision.RowVersion
	}
	proof := []byte("authorized asset handover evidence")
	upload, err := fixture.document.UploadMaterial(ctx, fixture.admin, fixture.caseID, "attachment", "asset-handover.txt", "text/plain", proof, MaterialUploadOptions{IdempotencyKey: "asset-evidence-upload-0001"})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(proof)
	material := MaterialRef{MaterialID: upload.MaterialID, MaterialVersionID: upload.DocumentID, SHA256: hex.EncodeToString(sum[:]), Purpose: "supporting"}
	registration, err := fixture.core.RegisterContractSigning(ctx, fixture.admin, ContractSigningRegistrationInput{ContractID: contractID, ExpectedRowVersion: approvedVersion, BaselineRevisionID: revisionID, SigningDate: time.Now().UTC().Format("2006-01-02"), SignedEvidence: MaterialRef{MaterialID: upload.MaterialID, MaterialVersionID: upload.DocumentID, SHA256: material.SHA256, Purpose: "signed_contract"}, SignedContent: draft, ManualComparison: ManualSigningComparison{Confirmed: true, SignedMaterialDigest: material.SHA256, ContractRevisionID: revisionID, Scope: clmSigningComparisonScope}, Parties: []ContractSigningParty{{Role: "buyer", Name: "采购方"}}, IdempotencyKey: "asset-signing-0001"})
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := fixture.core.CreateContractDelivery(ctx, fixture.admin, ContractDeliveryInput{ContractID: contractID, ExpectedRowVersion: registration.RowVersion, ContractRevisionID: revisionID, ItemID: itemID, BatchReference: "asset-delivery-10", Quantity: "10.000000", ActualAt: time.Now().UTC().Format(time.RFC3339Nano), Location: "合同指定交付地", Evidence: []MaterialRef{material}, IdempotencyKey: "asset-delivery-0001"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := fixture.core.CreateContractAcceptance(ctx, fixture.admin, ContractAcceptanceInput{ContractID: contractID, ExpectedRowVersion: deliveryRowVersion(t, fixture, contractID), ContractRevisionID: revisionID, DeliveryID: delivery.DeliveryID, ScopeStart: "0", ScopeEnd: "10", AcceptedQuantity: "4", ActualAt: time.Now().UTC().Format(time.RFC3339Nano), Standard: "冻结资产验收标准", Defects: []ContractAcceptanceDefectInput{{Description: "后六台需复验", Responsible: "供应商", RemediationDue: time.Now().UTC().AddDate(0, 0, 7).Format("2006-01-02"), FailedQuantity: "6"}}, Evidence: []MaterialRef{material}, IdempotencyKey: "asset-accept-4-fail-6"})
	if err != nil {
		t.Fatal(err)
	}
	rectification, err := fixture.core.RecordContractRectification(ctx, fixture.admin, ContractRectificationInput{ContractID: contractID, ExpectedRowVersion: deliveryRowVersion(t, fixture, contractID), ContractRevisionID: revisionID, AcceptanceID: root.AcceptanceID, DefectID: root.Defects[0].DefectID, Description: "完成后六台整改", Evidence: []MaterialRef{material}, IdempotencyKey: "asset-rectification-6"})
	if err != nil {
		t.Fatal(err)
	}
	retest, err := fixture.core.CreateContractAcceptance(ctx, fixture.admin, ContractAcceptanceInput{ContractID: contractID, ExpectedRowVersion: deliveryRowVersion(t, fixture, contractID), ContractRevisionID: revisionID, DeliveryID: delivery.DeliveryID, ScopeStart: "4", ScopeEnd: "10", AcceptedQuantity: "6", ReinspectionOf: root.AcceptanceID, RectificationIDs: []string{rectification.RectificationID}, ActualAt: time.Now().UTC().Format(time.RFC3339Nano), Standard: "冻结资产复验标准", Evidence: []MaterialRef{material}, IdempotencyKey: "asset-retest-6-pass"})
	if err != nil {
		t.Fatal(err)
	}

	firstInput := ContractAssetHandoverInput{ContractID: contractID, ExpectedRowVersion: deliveryRowVersion(t, fixture, contractID), ContractRevisionID: revisionID, AssetPlanID: draft.Assets[0].AssetPlanID, DeliveryID: delivery.DeliveryID, AcceptanceID: root.AcceptanceID, ScopeStart: "0", ScopeEnd: "3", HandedOverAt: time.Now().UTC().Format(time.RFC3339Nano), Location: "合同指定仓库", TransferNote: "首次移交三台", Evidence: []MaterialRef{material}, IdempotencyKey: "asset-handover-plan-a-0001"}
	assertAssetHandoverDeferredRollback(t, fixture, firstInput)
	first, err := fixture.core.CreateContractAssetHandover(ctx, fixture.admin, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.BookingStatus != "pending_booking" || first.AssignmentVerification != "unknown" || first.CustodianVerification != "verified" || first.DepartmentVerification != "unknown" {
		t.Fatalf("handover must preserve separate directory/booking status: %+v", first)
	}
	replayedHandover, err := fixture.core.CreateContractAssetHandover(ctx, fixture.admin, firstInput)
	if err != nil || replayedHandover.HandoverID != first.HandoverID {
		t.Fatalf("handover idempotency replay: %+v err=%v", replayedHandover, err)
	}
	assertFaultStatus(t, func() error {
		_, err := fixture.core.CreateContractAssetHandover(ctx, fixture.admin, ContractAssetHandoverInput{ContractID: contractID, ExpectedRowVersion: deliveryRowVersion(t, fixture, contractID), ContractRevisionID: revisionID, AssetPlanID: draft.Assets[1].AssetPlanID, DeliveryID: delivery.DeliveryID, AcceptanceID: root.AcceptanceID, ScopeStart: "2", ScopeEnd: "3", HandedOverAt: time.Now().UTC().Format(time.RFC3339Nano), Location: "合同指定仓库", TransferNote: "重复范围", Evidence: []MaterialRef{material}, IdempotencyKey: "asset-handover-overlap-0001"})
		return err
	}(), 409, "overlap across plans is rejected")
	assertFaultStatus(t, func() error {
		_, err := fixture.core.CreateContractAssetHandover(ctx, fixture.admin, ContractAssetHandoverInput{ContractID: contractID, ExpectedRowVersion: deliveryRowVersion(t, fixture, contractID), ContractRevisionID: revisionID, AssetPlanID: draft.Assets[0].AssetPlanID, DeliveryID: delivery.DeliveryID, AcceptanceID: root.AcceptanceID, ScopeStart: "3", ScopeEnd: "4", HandedOverAt: time.Now().UTC().Format(time.RFC3339Nano), Location: "合同指定仓库", TransferNote: "超出计划数量", Evidence: []MaterialRef{material}, IdempotencyKey: "asset-handover-plan-cap-0001"})
		return err
	}(), 409, "handover cannot exceed frozen plan quantity")
	second, err := fixture.core.CreateContractAssetHandover(ctx, fixture.admin, ContractAssetHandoverInput{ContractID: contractID, ExpectedRowVersion: deliveryRowVersion(t, fixture, contractID), ContractRevisionID: revisionID, AssetPlanID: draft.Assets[1].AssetPlanID, DeliveryID: delivery.DeliveryID, AcceptanceID: retest.AcceptanceID, ScopeStart: "4", ScopeEnd: "10", HandedOverAt: time.Now().UTC().Format(time.RFC3339Nano), Location: "合同指定仓库", TransferNote: "复验范围移交", Evidence: []MaterialRef{material}, IdempotencyKey: "asset-handover-plan-b-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if second.BookingStatus != "pending_booking" {
		t.Fatalf("handover alone must not assert ERP booking: %+v", second)
	}

	source := ContractAssetSource{TenantID: fixture.admin.TenantID, SourceID: "asset-system-test-fixture", KeyID: "asset-key-test-01"}
	assertAssetReceiptOutboxRollback(t, fixture, source, ContractAssetReceiptInput{
		ContractID: contractID, HandoverID: second.HandoverID, EventID: "asset-receipt-rollback-target", EventType: "receipt", AssetCode: "ERP-B-ROLLBACK", Quantity: "1", Unit: "台", OccurredAt: time.Now().UTC(),
	})
	input := ContractAssetReceiptInput{ContractID: contractID, HandoverID: first.HandoverID, EventID: "asset-receipt-event-1", EventType: "receipt", AssetCode: "ERP-A-0001", Quantity: "3.000000", Unit: "台", OccurredAt: time.Now().UTC()}
	receipt, replayed, err := fixture.core.RecordTrustedContractAssetReceipt(ctx, source, input)
	if err != nil || replayed || receipt.SourceEventID != input.EventID {
		t.Fatalf("trusted ERP fixture receipt: %+v replay=%v err=%v", receipt, replayed, err)
	}
	if _, replayed, err = fixture.core.RecordTrustedContractAssetReceipt(ctx, source, input); err != nil || !replayed {
		t.Fatalf("asset receipt replay: replay=%v err=%v", replayed, err)
	}
	changed := input
	changed.AssetCode = "ERP-A-OTHER"
	_, _, err = fixture.core.RecordTrustedContractAssetReceipt(ctx, source, changed)
	assertFaultStatus(t, err, 409, "same ERP event with changed payload conflicts")
	assets, err := fixture.core.ListContractAssets(ctx, fixture.admin, contractID)
	if err != nil || len(assets.Items) != 2 || len(assets.Items[0].Handovers) == 0 || assets.Items[0].Handovers[0].BookingStatus != "booked" {
		t.Fatalf("authorized asset projection after source receipt: %+v err=%v", assets, err)
	}
	t.Run("LifecycleOverviewAssetFacts", func(t *testing.T) {
		assertLifecycleMatchesExisting(t, fixture, contractID)
	})
	_, _, crossTenantErr := fixture.core.RecordTrustedContractAssetReceipt(ctx, ContractAssetSource{TenantID: "different-tenant", SourceID: "foreign-asset-source", KeyID: "foreign-asset-key"}, ContractAssetReceiptInput{ContractID: contractID, HandoverID: first.HandoverID, EventID: "foreign-event", EventType: "receipt", AssetCode: "ERP-FOREIGN", Quantity: "1", Unit: "台", OccurredAt: time.Now().UTC()})
	assertFaultStatus(t, crossTenantErr, 404, "cross-tenant asset source cannot resolve handover")

	notApplicableDraft := freezeCompleteDraft()
	notApplicable := false
	notApplicableDraft.Contract.IsFixedAsset = &notApplicable
	notApplicableDraft.Assets = nil
	other, err := fixture.core.CreateContractDraft(ctx, fixture.admin, CreateContractDraftInput{AuthorityMode: "native", Draft: notApplicableDraft, IdempotencyKey: "asset-not-applicable-create"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{ContractID: other.Summary.ContractID, CaseID: fixture.caseID, ExpectedRowVersion: other.Summary.RowVersion, IdempotencyKey: "asset-not-applicable-freeze"}); err != nil {
		t.Fatal(err)
	}
	noAssets, err := fixture.core.ListContractAssets(ctx, fixture.admin, other.Summary.ContractID)
	if err != nil || noAssets.FixedAssetStatus != "not_applicable" || len(noAssets.Items) != 0 {
		t.Fatalf("explicit service/non-capitalized contract must project not applicable: %+v err=%v", noAssets, err)
	}

	malformedQuantity := `{"contract":{"is_fixed_asset":true},"assets":[{"asset_plan_id":"70000000-0000-4000-8000-000000000001","item_id":"c6052c81-8b05-49ce-89e9-f6e9c6403c10","name":"子批次A","quantity":{"legacy":"unparseable"},"department_code":"dept-unresolved","department_name":"未核验使用部门","custodian_user_id":"demo-admin"},{"asset_plan_id":"70000000-0000-4000-8000-000000000002","item_id":"c6052c81-8b05-49ce-89e9-f6e9c6403c10","name":"子批次B","quantity":"7.000000","department_code":"dept-unresolved","department_name":"未核验使用部门","custodian_user_id":"missing-directory-user"}]}`
	rewriteAssetRevisionSnapshotForTest(t, fixture, contractID, revisionID, malformedQuantity)
	isolated, err := fixture.core.ListContractAssets(ctx, fixture.admin, contractID)
	if err != nil || len(isolated.Items) != 2 {
		t.Fatalf("one malformed legacy quantity must not fail the complete asset read: %+v err=%v", isolated, err)
	}
	if isolated.Items[0].PlanStatus != "unknown" || len(isolated.Items[0].Handovers) != 1 || isolated.Items[0].Handovers[0].BookingStatus != "booked" || isolated.Items[1].PlanStatus != "known" {
		t.Fatalf("malformed row should be isolated while preserving other plan and ERP history: %+v", isolated.Items)
	}

	malformedAssets := `{"contract":{"is_fixed_asset":true},"assets":{"legacy":"not-an-array"}}`
	rewriteAssetRevisionSnapshotForTest(t, fixture, contractID, revisionID, malformedAssets)
	wholeMalformed, err := fixture.core.ListContractAssets(ctx, fixture.admin, contractID)
	if err != nil || len(wholeMalformed.Items) != 2 || wholeMalformed.Items[0].PlanStatus != "unknown" || wholeMalformed.Items[1].PlanStatus != "unknown" {
		t.Fatalf("non-array legacy assets must return projected rows as unknown: %+v err=%v", wholeMalformed, err)
	}
	if len(wholeMalformed.Items[0].Handovers) != 1 || wholeMalformed.Items[0].Handovers[0].BookingStatus != "booked" || len(wholeMalformed.Items[1].Handovers) != 1 || wholeMalformed.Items[1].Handovers[0].BookingStatus != "pending_booking" {
		t.Fatalf("non-array snapshot must retain immutable handover and ERP receipt history: %+v", wholeMalformed.Items)
	}

	nullFixedAsset := `{"contract":{"is_fixed_asset":null},"assets":{"legacy":"not-an-array"}}`
	rewriteAssetRevisionSnapshotForTest(t, fixture, contractID, revisionID, nullFixedAsset)
	unknownFixedAsset, err := fixture.core.ListContractAssets(ctx, fixture.admin, contractID)
	if err != nil || unknownFixedAsset.FixedAssetStatus != "unknown" || len(unknownFixedAsset.Items) != 2 {
		t.Fatalf("null legacy fixed-asset flag must remain unknown and preserve plans: %+v err=%v", unknownFixedAsset, err)
	}
	if len(unknownFixedAsset.Items[0].Handovers) != 1 || unknownFixedAsset.Items[0].Handovers[0].BookingStatus != "booked" {
		t.Fatalf("null legacy fixed-asset flag must preserve handover and ERP history: %+v", unknownFixedAsset.Items[0])
	}
	t.Run("LifecycleOverviewMalformedAssetPlanUnknown", func(t *testing.T) {
		overview := assertLifecycleMatchesExisting(t, fixture, contractID)
		if overview.Completeness.Plans != "unknown" || overview.Plans != nil {
			t.Fatalf("malformed legacy plan must remain unknown: %+v", overview)
		}
	})
	t.Run("LifecycleOverviewRejectsUnidentifiedLegacyPlanEntries", func(t *testing.T) {
		for _, snapshot := range []string{
			`{"contract":{"currency":"CNY"},"delivery_plans":[null]}`,
			`{"contract":{"currency":"CNY"},"delivery_plans":[{}]}`,
			`{"contract":{"currency":"CNY"},"assets":[{}]}`,
			`{"contract":{"currency":"CNY"},"payments":[null]}`,
		} {
			rewriteAssetRevisionSnapshotForTest(t, fixture, contractID, revisionID, snapshot)
			overview, err := fixture.core.ContractLifecycleOverview(ctx, fixture.admin, contractID)
			if err != nil {
				t.Fatal(err)
			}
			if overview.Plans != nil || overview.Completeness.Plans != "unknown" {
				t.Fatalf("unidentified plan invented: %+v", overview)
			}
			if strings.Contains(snapshot, `"payments"`) && (overview.Payments != nil || overview.Completeness.Payments != "unknown") {
				t.Fatalf("unidentified payment plan invented a summary: %+v", overview)
			}
		}
	})
}

func rewriteAssetRevisionSnapshotForTest(t *testing.T, fixture durableCaseFixture, contractID, revisionID, snapshot string) {
	t.Helper()
	pool := fixture.environment.harness.pool
	if _, err := pool.Exec(context.Background(), `ALTER TABLE clm_contract_revisions DISABLE TRIGGER clm_contract_revisions_append_only`); err != nil {
		t.Fatalf("prepare malformed legacy snapshot fixture: %v", err)
	}
	defer func() {
		if _, err := pool.Exec(context.Background(), `ALTER TABLE clm_contract_revisions ENABLE TRIGGER clm_contract_revisions_append_only`); err != nil {
			t.Errorf("restore frozen snapshot append-only trigger: %v", err)
		}
	}()
	if _, err := pool.Exec(context.Background(), `UPDATE clm_contract_revisions SET snapshot=$1::jsonb WHERE tenant_id=$2 AND contract_id=$3 AND contract_revision_id=$4`, snapshot, fixture.admin.TenantID, contractID, revisionID); err != nil {
		t.Fatalf("install malformed legacy snapshot fixture: %v", err)
	}
}

type assetAtomicState struct {
	rowVersion                                              int64
	lifecycle                                               string
	handovers, assetReceipts, events, outbox, writeReceipts int64
}

func readAssetAtomicState(t *testing.T, fixture durableCaseFixture, contractID string) assetAtomicState {
	t.Helper()
	var state assetAtomicState
	err := fixture.environment.harness.pool.QueryRow(context.Background(), `SELECT c.row_version, c.lifecycle_state,
 (SELECT count(*) FROM clm_contract_asset_handovers h WHERE h.tenant_id=c.tenant_id AND h.contract_id=c.contract_id),
 (SELECT count(*) FROM clm_contract_asset_receipts r WHERE r.tenant_id=c.tenant_id AND r.contract_id=c.contract_id),
 (SELECT count(*) FROM clm_business_events e WHERE e.tenant_id=c.tenant_id AND e.contract_id=c.contract_id),
 (SELECT count(*) FROM clm_outbox_delivery o JOIN clm_business_events e ON e.tenant_id=o.tenant_id AND e.event_id=o.event_id WHERE e.tenant_id=c.tenant_id AND e.contract_id=c.contract_id),
 (SELECT count(*) FROM clm_contract_write_receipts w WHERE w.tenant_id=c.tenant_id AND w.contract_id=c.contract_id)
 FROM clm_contracts c WHERE c.tenant_id=$1 AND c.contract_id=$2`, fixture.admin.TenantID, contractID).Scan(&state.rowVersion, &state.lifecycle, &state.handovers, &state.assetReceipts, &state.events, &state.outbox, &state.writeReceipts)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func assertInjectedAssetFailure(t *testing.T, err error, message string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "P0001" || pgErr.Message != message {
		t.Fatalf("expected unique injected PostgreSQL failure %q, got %v", message, err)
	}
}

func assertAssetHandoverDeferredRollback(t *testing.T, fixture durableCaseFixture, input ContractAssetHandoverInput) {
	t.Helper()
	ctx := context.Background()
	pool := fixture.environment.harness.pool
	input.IdempotencyKey = "asset-deferred-rollback-target"
	before := readAssetAtomicState(t, fixture, input.ContractID)
	_, err := pool.Exec(ctx, `CREATE FUNCTION test_asset_deferred_receipt_failure() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER AS $$
 BEGIN
  IF NOT EXISTS (
   SELECT 1 FROM clm_contract_asset_handovers h
   JOIN clm_contracts c ON c.tenant_id=h.tenant_id AND c.contract_id=h.contract_id AND c.row_version=h.contract_row_version
   JOIN clm_business_events e ON e.tenant_id=h.tenant_id AND e.contract_id=h.contract_id AND e.aggregate_version=h.contract_row_version AND e.event_type='contract.asset_handover_recorded.v1' AND e.payload->>'handover_id'=h.handover_id::text
   JOIN clm_outbox_delivery o ON o.tenant_id=e.tenant_id AND o.event_id=e.event_id
   WHERE h.tenant_id=NEW.tenant_id AND h.contract_id=NEW.contract_id AND h.handover_id::text=NEW.response_json->>'handover_id'
  ) THEN RAISE EXCEPTION 'asset deferred precondition missing'; END IF;
  RAISE EXCEPTION 'asset deferred receipt injected failure';
 END $$;
 CREATE CONSTRAINT TRIGGER test_asset_deferred_receipt_failure AFTER INSERT ON clm_contract_write_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION test_asset_deferred_receipt_failure()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS test_asset_deferred_receipt_failure ON clm_contract_write_receipts; DROP FUNCTION IF EXISTS test_asset_deferred_receipt_failure()`)
	})
	_, err = fixture.core.CreateContractAssetHandover(ctx, fixture.admin, input)
	assertInjectedAssetFailure(t, err, "asset deferred receipt injected failure")
	if after := readAssetAtomicState(t, fixture, input.ContractID); after != before {
		t.Fatalf("deferred rollback changed asset facts/CAS/events/outbox/receipts: before=%+v after=%+v", before, after)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER test_asset_deferred_receipt_failure ON clm_contract_write_receipts; DROP FUNCTION test_asset_deferred_receipt_failure()`); err != nil {
		t.Fatal(err)
	}
}

func assertAssetReceiptOutboxRollback(t *testing.T, fixture durableCaseFixture, source ContractAssetSource, input ContractAssetReceiptInput) {
	t.Helper()
	ctx := context.Background()
	pool := fixture.environment.harness.pool
	before := readAssetAtomicState(t, fixture, input.ContractID)
	_, err := pool.Exec(ctx, `CREATE FUNCTION test_asset_outbox_receipt_failure() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER AS $$
 BEGIN
  IF NOT EXISTS (
   SELECT 1 FROM clm_contract_asset_receipts r
   JOIN clm_contracts c ON c.tenant_id=r.tenant_id AND c.contract_id=r.contract_id
   JOIN clm_business_events e ON e.tenant_id=r.tenant_id AND e.contract_id=r.contract_id AND e.aggregate_version=c.row_version AND e.event_type='contract.asset_erp_receipt_recorded.v1' AND e.payload->>'receipt_id'=r.receipt_id::text
   WHERE r.tenant_id=NEW.tenant_id AND e.event_id=NEW.event_id AND r.source_event_id='asset-receipt-rollback-target'
  ) THEN RAISE EXCEPTION 'asset outbox precondition missing'; END IF;
  RAISE EXCEPTION 'asset receipt outbox injected failure';
 END $$;
 CREATE TRIGGER test_asset_outbox_receipt_failure BEFORE INSERT ON clm_outbox_delivery FOR EACH ROW EXECUTE FUNCTION test_asset_outbox_receipt_failure()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS test_asset_outbox_receipt_failure ON clm_outbox_delivery; DROP FUNCTION IF EXISTS test_asset_outbox_receipt_failure()`)
	})
	_, _, err = fixture.core.RecordTrustedContractAssetReceipt(ctx, source, input)
	assertInjectedAssetFailure(t, err, "asset receipt outbox injected failure")
	if after := readAssetAtomicState(t, fixture, input.ContractID); after != before {
		t.Fatalf("outbox rollback changed asset facts/CAS/events/outbox/receipts: before=%+v after=%+v", before, after)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER test_asset_outbox_receipt_failure ON clm_outbox_delivery; DROP FUNCTION test_asset_outbox_receipt_failure()`); err != nil {
		t.Fatal(err)
	}
}
