package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"
)

// This focused durable scenario covers explicit pending ranges, append-only
// defects/rectification and the cumulative acceptance quantity contract.
func TestContractFulfillmentPartialAcceptanceAndLinearRetest(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	testCLMDataKey(t)
	ctx := context.Background()
	contractID, frozen := freezeTestContract(t, fixture)
	body := []byte("交付与验收所依据的案件材料")
	upload, err := fixture.document.UploadMaterial(ctx, fixture.admin, fixture.caseID, "attachment", "delivery-proof.txt", "text/plain", body,
		MaterialUploadOptions{IdempotencyKey: "fulfillment-evidence-0001"})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	baseMaterial := MaterialRef{MaterialID: upload.MaterialID, MaterialVersionID: upload.DocumentID, SHA256: hex.EncodeToString(sum[:]), Purpose: "supporting"}

	submitted, err := fixture.core.SubmitContractApproval(ctx, fixture.admin, SubmitContractApprovalInput{
		ContractID: contractID, ExpectedRowVersion: frozen.RowVersion, IdempotencyKey: "fulfillment-approval-submit",
	})
	if err != nil {
		t.Fatal(err)
	}
	var rowVersion int64 // Contract version; approval commands use the round version.
	for index, task := range submitted.Tasks {
		actor := approvalTestUser(t, "demo-reviewer")
		if task.Final {
			actor = fixture.admin
		}
		currentTask, err := fixture.core.GetApprovalTask(ctx, actor, task.ApprovalTaskID)
		if err != nil {
			t.Fatalf("refresh node %d: %v", task.NodeOrder, err)
		}
		if active := currentTask.RoundDetail.ActiveTaskID; active == nil || *active != task.ApprovalTaskID {
			t.Fatalf("node %d is not the active approval task", task.NodeOrder)
		}
		decision, err := fixture.core.DecideApprovalTask(ctx, actor, DecideApprovalTaskInput{
			TaskID: task.ApprovalTaskID, Decision: clmDecisionApprove, ExpectedRoundRowVersion: currentTask.RoundDetail.Round.RowVersion,
			IdempotencyKey: fmt.Sprintf("fulfillment-approval-%02d", index),
		})
		if err != nil {
			t.Fatalf("approve node %d: %v", task.NodeOrder, err)
		}
		rowVersion = decision.RowVersion
	}
	manual := ManualSigningComparison{Confirmed: true, SignedMaterialDigest: baseMaterial.SHA256,
		ContractRevisionID: frozen.Version.ContractRevisionID, Scope: clmSigningComparisonScope}
	signed := MaterialRef{MaterialID: baseMaterial.MaterialID, MaterialVersionID: baseMaterial.MaterialVersionID, SHA256: baseMaterial.SHA256, Purpose: "signed_contract"}
	registration, err := fixture.core.RegisterContractSigning(ctx, fixture.admin, ContractSigningRegistrationInput{
		ContractID: contractID, ExpectedRowVersion: rowVersion, BaselineRevisionID: frozen.Version.ContractRevisionID,
		SigningDate: time.Now().UTC().Format("2006-01-02"), SignedEvidence: signed, SignedContent: freezeCompleteDraft(), ManualComparison: manual,
		Parties: []ContractSigningParty{{Role: "buyer", Name: "采购方"}}, IdempotencyKey: "fulfillment-signing-register",
	})
	if err != nil {
		t.Fatal(err)
	}

	delivery, err := fixture.core.CreateContractDelivery(ctx, fixture.admin, ContractDeliveryInput{
		ContractID: contractID, ExpectedRowVersion: registration.RowVersion, ContractRevisionID: frozen.Version.ContractRevisionID,
		ItemID: "c6052c81-8b05-49ce-89e9-f6e9c6403c10", BatchReference: "batch-10", Quantity: "10.000000",
		ActualAt: "2026-11-02T10:00:00Z", Location: "采购单位仓库", Evidence: []MaterialRef{baseMaterial}, IdempotencyKey: "fulfillment-delivery-10",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertFaultStatus(t, func() error {
		_, err := fixture.core.CreateContractAcceptance(ctx, fixture.admin, ContractAcceptanceInput{
			ContractID: contractID, ExpectedRowVersion: deliveryRowVersion(t, fixture, contractID), ContractRevisionID: frozen.Version.ContractRevisionID,
			DeliveryID: delivery.DeliveryID, ScopeStart: "0", ScopeEnd: "10.000001", AcceptedQuantity: "1",
			ActualAt: "2026-11-02T11:00:00Z", Standard: "按冻结技术标准验收", Evidence: []MaterialRef{{MaterialID: baseMaterial.MaterialID, MaterialVersionID: baseMaterial.MaterialVersionID, SHA256: baseMaterial.SHA256, Purpose: "acceptance"}}, IdempotencyKey: "fulfillment-overrange",
		})
		return err
	}(), 409, "acceptance range must not exceed delivery")

	firstInput := ContractAcceptanceInput{ContractID: contractID, ExpectedRowVersion: deliveryRowVersion(t, fixture, contractID), ContractRevisionID: frozen.Version.ContractRevisionID,
		DeliveryID: delivery.DeliveryID, ScopeStart: "0", ScopeEnd: "10.000000", AcceptedQuantity: "4.000000",
		ActualAt: "2026-11-02T11:00:00Z", Standard: "按冻结技术标准验收", InspectorNote: "现场验收",
		Defects:  []ContractAcceptanceDefectInput{{Description: "设备接口不符", Responsible: "供应商", RemediationDue: "2026-11-10", FailedQuantity: "6.000000"}},
		Evidence: []MaterialRef{{MaterialID: baseMaterial.MaterialID, MaterialVersionID: baseMaterial.MaterialVersionID, SHA256: baseMaterial.SHA256, Purpose: "acceptance"}}, IdempotencyKey: "fulfillment-accept-4-fail-6"}
	first, err := fixture.core.CreateContractAcceptance(ctx, fixture.admin, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.Result != "partial" || first.AcceptedQuantity != "4.000000" || first.FailedQuantity != "6.000000" {
		t.Fatalf("unexpected partial result: %+v", first)
	}
	replayed, err := fixture.core.CreateContractAcceptance(ctx, fixture.admin, firstInput)
	if err != nil || replayed.AcceptanceID != first.AcceptanceID {
		t.Fatalf("same-key acceptance retry must replay original fact: %+v err=%v", replayed, err)
	}
	assertFaultStatus(t, func() error {
		input := firstInput
		input.ExpectedRowVersion = deliveryRowVersion(t, fixture, contractID)
		input.IdempotencyKey = "fulfillment-same-range-new-key"
		_, err := fixture.core.CreateContractAcceptance(ctx, fixture.admin, input)
		return err
	}(), 409, "a root scope cannot be allocated twice")

	rectification, err := fixture.core.RecordContractRectification(ctx, fixture.admin, ContractRectificationInput{
		ContractID: contractID, ExpectedRowVersion: deliveryRowVersion(t, fixture, contractID), ContractRevisionID: frozen.Version.ContractRevisionID,
		AcceptanceID: first.AcceptanceID, DefectID: first.Defects[0].DefectID, Description: "更换不符接口组件并复核",
		Evidence:       []MaterialRef{{MaterialID: baseMaterial.MaterialID, MaterialVersionID: baseMaterial.MaterialVersionID, SHA256: baseMaterial.SHA256, Purpose: "acceptance"}},
		IdempotencyKey: "fulfillment-rectification-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeRetest, err := fixture.core.ListContractAcceptances(ctx, fixture.admin, contractID)
	if err != nil {
		t.Fatal(err)
	}
	if beforeRetest.Summary[0].QualifiedQuantity != "4.000000" || beforeRetest.Summary[0].UnallocatedQuantity != "0.000000" || beforeRetest.Summary[0].PendingReinspectionQuantity != "6.000000" {
		t.Fatalf("partial quantity summary: %+v", beforeRetest.Summary)
	}
	retest, err := fixture.core.CreateContractAcceptance(ctx, fixture.admin, ContractAcceptanceInput{
		ContractID: contractID, ExpectedRowVersion: beforeRetest.RowVersion, ContractRevisionID: frozen.Version.ContractRevisionID,
		DeliveryID: delivery.DeliveryID, ScopeStart: "4.000000", ScopeEnd: "10.000000", AcceptedQuantity: "4.000000", ReinspectionOf: first.AcceptanceID,
		RectificationIDs: []string{rectification.RectificationID}, ActualAt: "2026-11-02T12:00:00Z", Standard: "按冻结技术标准复验",
		Defects:  []ContractAcceptanceDefectInput{{Description: "复验仍有一处接口松动", Responsible: "供应商", RemediationDue: "2026-11-12", FailedQuantity: "2.000000"}},
		Evidence: []MaterialRef{{MaterialID: baseMaterial.MaterialID, MaterialVersionID: baseMaterial.MaterialVersionID, SHA256: baseMaterial.SHA256, Purpose: "acceptance"}}, IdempotencyKey: "fulfillment-retest-6",
	})
	if err != nil {
		t.Fatal(err)
	}
	if retest.Result != "partial" || retest.AcceptedQuantity != "4.000000" || retest.FailedQuantity != "2.000000" {
		t.Fatalf("first linear retest did not preserve the new failed suffix: %+v", retest)
	}
	afterFirstRetest, err := fixture.core.ListContractAcceptances(ctx, fixture.admin, contractID)
	if err != nil {
		t.Fatal(err)
	}
	if afterFirstRetest.Summary[0].QualifiedQuantity != "8.000000" || afterFirstRetest.Summary[0].PendingReinspectionQuantity != "2.000000" {
		t.Fatalf("partial retest must leave only the new 2-unit suffix pending: %+v", afterFirstRetest.Summary)
	}
	secondRectification, err := fixture.core.RecordContractRectification(ctx, fixture.admin, ContractRectificationInput{
		ContractID: contractID, ExpectedRowVersion: afterFirstRetest.RowVersion, ContractRevisionID: frozen.Version.ContractRevisionID,
		AcceptanceID: retest.AcceptanceID, DefectID: retest.Defects[0].DefectID, Description: "重新紧固接口并复核",
		Evidence:       []MaterialRef{{MaterialID: baseMaterial.MaterialID, MaterialVersionID: baseMaterial.MaterialVersionID, SHA256: baseMaterial.SHA256, Purpose: "acceptance"}},
		IdempotencyKey: "fulfillment-rectification-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeSecondRetest, err := fixture.core.ListContractAcceptances(ctx, fixture.admin, contractID)
	if err != nil {
		t.Fatal(err)
	}
	finalRetest, err := fixture.core.CreateContractAcceptance(ctx, fixture.admin, ContractAcceptanceInput{
		ContractID: contractID, ExpectedRowVersion: beforeSecondRetest.RowVersion, ContractRevisionID: frozen.Version.ContractRevisionID,
		DeliveryID: delivery.DeliveryID, ScopeStart: "8.000000", ScopeEnd: "10.000000", AcceptedQuantity: "2.000000", ReinspectionOf: retest.AcceptanceID,
		RectificationIDs: []string{secondRectification.RectificationID}, ActualAt: "2026-11-02T13:00:00Z", Standard: "再次复验通过",
		Evidence: []MaterialRef{{MaterialID: baseMaterial.MaterialID, MaterialVersionID: baseMaterial.MaterialVersionID, SHA256: baseMaterial.SHA256, Purpose: "acceptance"}}, IdempotencyKey: "fulfillment-retest-2",
	})
	if err != nil || finalRetest.Result != "passed" {
		t.Fatalf("second retest should pass the final suffix: %+v err=%v", finalRetest, err)
	}
	afterRetest, err := fixture.core.ListContractAcceptances(ctx, fixture.admin, contractID)
	if err != nil {
		t.Fatal(err)
	}
	// An empty SQL aggregate may render zero without a fractional scale.
	pending, validPending := new(big.Rat).SetString(afterRetest.Summary[0].PendingReinspectionQuantity)
	if afterRetest.Summary[0].QualifiedQuantity != "10.000000" || !validPending || pending.Sign() != 0 {
		t.Fatalf("linear retests must bring cumulative qualified quantity to 10, not double-count: %+v", afterRetest.Summary)
	}
	assertFaultStatus(t, func() error {
		_, err := fixture.core.CreateContractAcceptance(ctx, fixture.admin, ContractAcceptanceInput{
			ContractID: contractID, ExpectedRowVersion: afterRetest.RowVersion, ContractRevisionID: frozen.Version.ContractRevisionID,
			DeliveryID: delivery.DeliveryID, ScopeStart: "4.000000", ScopeEnd: "10.000000", AcceptedQuantity: "4.000000", ReinspectionOf: first.AcceptanceID,
			RectificationIDs: []string{rectification.RectificationID}, ActualAt: "2026-11-02T14:00:00Z", Standard: "重复复验", Defects: []ContractAcceptanceDefectInput{{Description: "重复事实", Responsible: "供应商", RemediationDue: "2026-11-12", FailedQuantity: "2.000000"}}, Evidence: []MaterialRef{{MaterialID: baseMaterial.MaterialID, MaterialVersionID: baseMaterial.MaterialVersionID, SHA256: baseMaterial.SHA256, Purpose: "acceptance"}}, IdempotencyKey: "fulfillment-duplicate-retest-new-key",
		})
		return err
	}(), 409, "a failed fact has at most one direct retest")
}

func deliveryRowVersion(t *testing.T, fixture durableCaseFixture, contractID string) int64 {
	t.Helper()
	list, err := fixture.core.ListContractDeliveries(context.Background(), fixture.admin, contractID)
	if err != nil {
		t.Fatal(err)
	}
	return list.RowVersion
}

// The actual schema migration must tolerate malformed legacy plan JSON, and
// duplicate UUID rows must remain ambiguous rather than selecting the first.
func TestContractFulfillmentLegacyMigrationSkipsUnknownPlans(t *testing.T) {
	fixture, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for fulfillment migration checks")
	}
	ctx := context.Background()
	testCLMDataKey(t)

	duplicateDraft := freezeCompleteDraft()
	first := duplicateDraft.Items[0]
	first.Quantity, first.LineAmount = "5.000000", "50000.00"
	second := first
	second.Name = "同 UUID 的另一条旧合同明细"
	duplicateDraft.Items = []DraftItem{first, second}
	created, err := fixture.core.CreateContractDraft(ctx, fixture.admin, CreateContractDraftInput{
		ExpectedRowVersion: 0, AuthorityMode: "native", Draft: duplicateDraft, IdempotencyKey: "fulfillment-duplicate-line-draft",
	})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		CaseID: fixture.caseID, ContractID: created.Summary.ContractID, ExpectedRowVersion: created.Summary.RowVersion,
		IdempotencyKey: "fulfillment-duplicate-line-freeze",
	})
	if err != nil {
		t.Fatalf("duplicate item UUIDs must not add a new freeze gate: %v", err)
	}

	legacy, err := fixture.core.CreateContractDraft(ctx, fixture.admin, CreateContractDraftInput{
		ExpectedRowVersion: 0, AuthorityMode: "native", Draft: freezeCompleteDraft(), IdempotencyKey: "fulfillment-legacy-plan-draft",
	})
	if err != nil {
		t.Fatal(err)
	}
	const legacyRevisionID = "c6052c81-8b05-49ce-89e9-f6e9c6403c61"
	snapshot := `{"items":[
		{"item_id":"c6052c81-8b05-49ce-89e9-f6e9c6403c62","quantity":{"bad":true},"unit":null},
		{"item_id":"c6052c81-8b05-49ce-89e9-f6e9c6403c63","quantity":"1000000000000.000000","unit":"台"},
		{"item_id":"c6052c81-8b05-49ce-89e9-f6e9c6403c64","quantity":"-1.000000","unit":"台"},
		{"item_id":"c6052c81-8b05-49ce-89e9-f6e9c6403c65","quantity":"0.000000","unit":"台"},
		{"item_id":"c6052c81-8b05-49ce-89e9-f6e9c6403c66","quantity":"2.000000","unit":{"code":"台"}},
		{"item_id":"c6052c81-8b05-49ce-89e9-f6e9c6403c67","quantity":"2.000000","unit":"台"},
		{"item_id":"c6052c81-8b05-49ce-89e9-f6e9c6403c67","quantity":"3.000000","unit":"台"},
		{"item_id":{},"quantity":"4.000000","unit":"台"},
		"not-an-object"
	]}`
	if _, err := fixture.environment.harness.pool.Exec(ctx, `INSERT INTO clm_contract_revisions
		(tenant_id,contract_id,contract_revision_id,revision_number,amount,currency,snapshot,snapshot_digest,frozen_by)
		VALUES ($1,$2,$3,1,100000,'CNY',$4::jsonb,$5,$6)`, fixture.admin.TenantID, legacy.Summary.ContractID,
		legacyRevisionID, snapshot, strings.Repeat("a", 64), fixture.admin.ID); err != nil {
		t.Fatal(err)
	}

	pool := fixture.environment.harness.pool
	if _, err := pool.Exec(ctx, `DROP TRIGGER IF EXISTS clm_contract_revision_asset_plans_project ON clm_contract_revisions;
		DROP TABLE clm_contract_asset_receipts, clm_contract_asset_handovers, clm_contract_revision_asset_plans;
		DROP TABLE clm_contract_payment_eligibility_checks, clm_contract_payment_receipts;
		DROP TABLE clm_contract_acceptance_rectifications, clm_contract_rectifications,
		clm_contract_acceptance_defects, clm_contract_acceptances, clm_contract_deliveries, clm_contract_revision_items`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version>=28`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, pool, fixture.environment.harness.schema); err != nil {
		t.Fatalf("reapply migration 28 to malformed historical snapshots: %v", err)
	}

	var duplicateRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM clm_contract_revision_items WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`,
		fixture.admin.TenantID, created.Summary.ContractID, frozen.Version.ContractRevisionID).Scan(&duplicateRows); err != nil {
		t.Fatal(err)
	}
	if duplicateRows != 0 {
		t.Fatalf("ambiguous repeated item UUID created %d fulfillment plan rows", duplicateRows)
	}
	var total, knownQuantity, knownUnit int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(planned_quantity),count(unit) FROM clm_contract_revision_items
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`, fixture.admin.TenantID, legacy.Summary.ContractID, legacyRevisionID).
		Scan(&total, &knownQuantity, &knownUnit); err != nil {
		t.Fatal(err)
	}
	if total != 5 || knownQuantity != 1 || knownUnit != 3 {
		t.Fatalf("legacy fields should be preserved only when safely parsed: rows=%d quantities=%d units=%d", total, knownQuantity, knownUnit)
	}

	reader := &Store{pool: pool}
	list, err := reader.ListContractDeliveries(ctx, fixture.admin, legacy.Summary.ContractID)
	if err != nil {
		t.Fatalf("nullable unknown plan units must not break read projections: %v", err)
	}
	if len(list.Summary) != 5 {
		t.Fatalf("legacy summary rows=%d, want 5", len(list.Summary))
	}
	for _, item := range list.Summary {
		if item.ContractRevisionID == legacyRevisionID && item.ItemID == "c6052c81-8b05-49ce-89e9-f6e9c6403c66" && (item.PlannedQuantity != "2.000000" || item.Unit != "") {
			t.Fatalf("malformed legacy unit was inferred: %+v", item)
		}
	}
}
