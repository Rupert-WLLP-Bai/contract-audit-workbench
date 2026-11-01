package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
)

// This single database scenario covers source-bound human transcription,
// complete-field differences, approved/negative workflow history, idempotency,
// append-only registration and source-case revocation projection.
func TestContractSigningRequiresHumanSourceAttestationAndApprovedRevision(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	testCLMDataKey(t)
	ctx := context.Background()

	contractID, frozen := freezeTestContract(t, fixture)
	body := []byte("签署版原件：内容由授权人员逐项转录核对")
	upload, err := fixture.document.UploadMaterial(ctx, fixture.admin, fixture.caseID, "attachment",
		"signed-contract.txt", "text/plain", body, MaterialUploadOptions{IdempotencyKey: "signing-source-positive"})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	material := MaterialRef{MaterialID: upload.MaterialID, MaterialVersionID: upload.DocumentID,
		SHA256: hex.EncodeToString(digest[:]), Purpose: "signed_contract"}

	submitted, err := fixture.core.SubmitContractApproval(ctx, fixture.admin, SubmitContractApprovalInput{
		ContractID: contractID, ExpectedRowVersion: frozen.RowVersion, IdempotencyKey: "signing-approved-submit",
	})
	if err != nil {
		t.Fatal(err)
	}
	rowVersion := submitted.Round.RowVersion
	for index, task := range submitted.Tasks {
		actor := approvalTestUser(t, "demo-reviewer")
		if task.Final {
			actor = fixture.admin
		}
		decision, err := fixture.core.DecideApprovalTask(ctx, actor, DecideApprovalTaskInput{
			TaskID: task.ApprovalTaskID, Decision: clmDecisionApprove, ExpectedRoundRowVersion: rowVersion,
			IdempotencyKey: fmt.Sprintf("signing-approved-decision-%02d", index),
		})
		if err != nil {
			t.Fatalf("approve node %d: %v", task.NodeOrder, err)
		}
		rowVersion = decision.RowVersion
	}

	manual := ManualSigningComparison{Confirmed: true, SignedMaterialDigest: material.SHA256,
		ContractRevisionID: frozen.Version.ContractRevisionID, Scope: clmSigningComparisonScope}
	checkInput := ContractSigningCheckInput{ContractID: contractID, BaselineRevisionID: frozen.Version.ContractRevisionID,
		SignedEvidence: material, SignedContent: freezeCompleteDraft(), ManualComparison: manual}
	missingAttestation := checkInput
	missingAttestation.ManualComparison.Confirmed = false
	assertFaultStatus(t, func() error {
		_, err := fixture.core.CheckContractSigning(ctx, fixture.admin, missingAttestation)
		return err
	}(), 422, "missing manual source attestation")
	checked, err := fixture.core.CheckContractSigning(ctx, fixture.admin, checkInput)
	if err != nil || !checked.CanRegister || checked.ApprovalStatus != "approved" ||
		checked.VerificationStatus != "human_transcription_confirmed" {
		t.Fatalf("source-bound manual check: %+v err=%v", checked, err)
	}
	wrongDigest := checkInput
	wrongDigest.ManualComparison.SignedMaterialDigest = strings.Repeat("0", 64)
	_, err = fixture.core.CheckContractSigning(ctx, fixture.admin, wrongDigest)
	assertFaultStatus(t, err, 409, "manual confirmation must bind the authorized source digest")
	changed := freezeCompleteDraft()
	changed.Contract.PerformanceLocation = "未经批准的地点"
	changed.Contract.PerformanceBondRequired = new(false)
	checkInput.SignedContent = changed
	difference, err := fixture.core.CheckContractSigning(ctx, fixture.admin, checkInput)
	if err != nil || difference.CanRegister || difference.ComparisonResult != "material_difference" {
		t.Fatalf("performance changes must block registration: %+v err=%v", difference, err)
	}
	seen := map[string]bool{}
	for _, field := range difference.Fields {
		seen[field.Path] = true
	}
	if !seen["contract.performance_location"] || !seen["contract.performance_bond_required"] {
		t.Fatalf("business diff omitted performance fields: %+v", difference.Fields)
	}

	input := ContractSigningRegistrationInput{ContractID: contractID, ExpectedRowVersion: rowVersion,
		BaselineRevisionID: frozen.Version.ContractRevisionID, SigningDate: "2026-11-01", SignedEvidence: material,
		SignedContent: freezeCompleteDraft(), ManualComparison: manual,
		Parties: []ContractSigningParty{{Role: "buyer", Name: "采购方"}}, IdempotencyKey: "signing-registration-approved"}
	registered, err := fixture.core.RegisterContractSigning(ctx, fixture.admin, input)
	if err != nil || registered.VerificationStatus != "human_transcription_confirmed" ||
		registered.ComparisonConfirmedBy != fixture.admin.ID || registered.ComparisonScope != clmSigningComparisonScope {
		t.Fatalf("registration missing human confirmation record: %+v err=%v", registered, err)
	}
	replayed, err := fixture.core.RegisterContractSigning(ctx, fixture.admin, input)
	if err != nil || replayed.RegistrationID != registered.RegistrationID {
		t.Fatalf("idempotent replay: %+v err=%v", replayed, err)
	}
	var registrations int
	if err := fixture.core.pool.QueryRow(ctx, `SELECT count(*) FROM clm_contract_signing_registrations
		WHERE tenant_id=$1 AND contract_id=$2`, fixture.admin.TenantID, contractID).Scan(&registrations); err != nil || registrations != 1 {
		t.Fatalf("registration count=%d err=%v", registrations, err)
	}
	if _, err := fixture.environment.harness.pool.Exec(ctx, `UPDATE clm_contract_signing_registrations
		SET comparison_result='rewritten' WHERE tenant_id=$1 AND contract_id=$2`, fixture.admin.TenantID, contractID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("registration fact was mutable: %v", err)
	}

	// The contract-only reader can read this contract but has no grant to the
	// signed material's source case. Sensitive material facts must be omitted.
	dir := t.TempDir()
	directory := filepath.Join(dir, "directory.json")
	bodyJSON := `{"users":[{"id":"demo-admin","display_name":"合同管理员","role":"admin","tenant_id":"` + fixture.admin.TenantID + `","org_id":"` + fixture.admin.OrgID + `","enabled":true,"issuer":"signing-test","subject":"admin-revoked","grants":[{"role":"case_owner","scope":"case","case_id":"` + contractID + `"}]},{"id":"signing-reader","display_name":"合同只读","role":"reviewer","tenant_id":"` + fixture.admin.TenantID + `","org_id":"` + fixture.admin.OrgID + `","enabled":true,"issuer":"signing-test","subject":"reader","grants":[{"role":"case_owner","scope":"case","case_id":"` + contractID + `"}]}]}`
	if err := os.WriteFile(directory, []byte(bodyJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = accounts.ConfigureDirectory("") })
	_, err = fixture.core.RegisterContractSigning(ctx, fixture.admin, input)
	assertFaultStatus(t, err, 404, "receipt replay after source-case grant revocation")
	reader, ok := accounts.ByID("signing-reader")
	if !ok {
		t.Fatal("test reader missing")
	}
	detail, err := fixture.core.LoadContractDetail(ctx, reader, contractID)
	if err != nil || detail.SigningRegistration != nil {
		t.Fatalf("case-revoked detail leaked signed material: %+v err=%v", detail.SigningRegistration, err)
	}
	list, err := fixture.core.ListContractSigningRegistrations(ctx, reader, contractID)
	if err != nil || len(list.Items) != 0 {
		t.Fatalf("case-revoked registration list leaked material: %+v err=%v", list, err)
	}

	// A returned round on the exact revision remains a negative approval fact;
	// it cannot fall through to the legacy unknown-history branch.
	accounts.ConfigureDirectory("")
	negative, err := fixture.core.CreateContractDraft(ctx, fixture.admin, CreateContractDraftInput{
		ExpectedRowVersion: 0, AuthorityMode: "native", Draft: freezeCompleteDraft(), IdempotencyKey: "signing-negative-draft",
	})
	if err != nil {
		t.Fatal(err)
	}
	negativeFrozen, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		CaseID: fixture.caseID, ContractID: negative.Summary.ContractID, ExpectedRowVersion: negative.Summary.RowVersion,
		IdempotencyKey: "signing-negative-freeze",
	})
	if err != nil {
		t.Fatal(err)
	}
	negativeRound, err := fixture.core.SubmitContractApproval(ctx, fixture.admin, SubmitContractApprovalInput{
		ContractID: negative.Summary.ContractID, ExpectedRowVersion: negativeFrozen.RowVersion, IdempotencyKey: "signing-negative-submit",
	})
	if err != nil {
		t.Fatal(err)
	}
	returned, err := fixture.core.DecideApprovalTask(ctx, approvalTestUser(t, "demo-reviewer"), DecideApprovalTaskInput{
		TaskID: negativeRound.Tasks[0].ApprovalTaskID, Decision: clmDecisionReturn,
		ExpectedRoundRowVersion: negativeRound.Round.RowVersion, IdempotencyKey: "signing-negative-return", Comment: "审批不通过，需补充依据",
	})
	if err != nil {
		t.Fatal(err)
	}
	negativeInput := input
	negativeInput.ContractID = negative.Summary.ContractID
	negativeInput.ExpectedRowVersion = returned.RowVersion
	negativeInput.BaselineRevisionID = negativeFrozen.Version.ContractRevisionID
	negativeInput.ManualComparison.ContractRevisionID = negativeFrozen.Version.ContractRevisionID
	negativeInput.IdempotencyKey = "signing-negative-register"
	_, err = fixture.core.RegisterContractSigning(ctx, fixture.admin, negativeInput)
	assertFaultStatus(t, err, 409, "negative approval history")

	// No approval history on a new native revision is distinct and also denied.
	noHistory, err := fixture.core.CreateContractDraft(ctx, fixture.admin, CreateContractDraftInput{
		ExpectedRowVersion: 0, AuthorityMode: "native", Draft: freezeCompleteDraft(), IdempotencyKey: "signing-no-history-draft",
	})
	if err != nil {
		t.Fatal(err)
	}
	noHistoryFrozen, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		CaseID: fixture.caseID, ContractID: noHistory.Summary.ContractID, ExpectedRowVersion: noHistory.Summary.RowVersion,
		IdempotencyKey: "signing-no-history-freeze",
	})
	if err != nil {
		t.Fatal(err)
	}
	noHistoryInput := input
	noHistoryInput.ContractID, noHistoryInput.ExpectedRowVersion = noHistory.Summary.ContractID, noHistoryFrozen.RowVersion
	noHistoryInput.BaselineRevisionID = noHistoryFrozen.Version.ContractRevisionID
	noHistoryInput.ManualComparison.ContractRevisionID = noHistoryFrozen.Version.ContractRevisionID
	noHistoryInput.IdempotencyKey = "signing-no-history-register"
	_, err = fixture.core.RegisterContractSigning(ctx, fixture.admin, noHistoryInput)
	assertFaultStatus(t, err, 409, "no approval history")
}
