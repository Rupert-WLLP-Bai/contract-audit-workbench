package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
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
		actor := approvalTestUser(t, "demo-reviewer")
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
