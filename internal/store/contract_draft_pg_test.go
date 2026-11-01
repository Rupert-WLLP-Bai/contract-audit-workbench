package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

// The durable checks below cover the schema-19 acceptance facts that a unit
// test cannot observe: draft recovery across connections, receipt replay and
// payload conflicts, tenant and authorization boundaries, sealed restricted
// fields at rest, and legacy imports that keep unknown history unknown.

func newContractDraftTestEnv(t *testing.T) (durableCaseFixture, *Store) {
	t.Helper()
	fixture, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for durable contract draft checks")
	}
	testCLMDataKey(t)
	recovery := fixture.environment.openRole(t, "core")
	return fixture, recovery
}

func sampleCreateInput(key string) CreateContractDraftInput {
	return CreateContractDraftInput{
		ExpectedRowVersion: 0, AuthorityMode: "native",
		Draft: sampleDraftContent(), IdempotencyKey: key,
	}
}

func assertFaultStatus(t *testing.T, err error, status int, context string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected failure", context)
	}
	var fe *fault.Error
	if !errors.As(err, &fe) || fe.Status != status {
		t.Fatalf("%s: expected status %d, got %v", context, status, err)
	}
}

func TestContractDraftCreateRecoverReplayAndReplace(t *testing.T) {
	fixture, recovery := newContractDraftTestEnv(t)
	ctx := context.Background()
	admin := fixture.admin

	detail, err := fixture.core.CreateContractDraft(ctx, admin, sampleCreateInput("create-recover-0001"))
	if err != nil {
		t.Fatal(err)
	}
	if detail.Summary.ContractState != "draft" || detail.Summary.RowVersion != 1 {
		t.Fatalf("unexpected summary %+v", detail.Summary)
	}
	if len(detail.AllowedCommands) != 1 || detail.AllowedCommands[0] != "replace_draft" {
		t.Fatalf("allowed commands %v", detail.AllowedCommands)
	}
	if detail.Draft == nil || detail.Draft.Supplier.BankAccount != testRestrictedBankAccount {
		t.Fatalf("creator must read back plaintext restricted fields: %+v", detail.Draft)
	}

	restored, err := recovery.LoadContractDetail(ctx, admin, detail.Summary.ContractID)
	if err != nil {
		t.Fatalf("draft recovery through another connection failed: %v", err)
	}
	if restored.Draft == nil || restored.Draft.Contract.Name != "测试设备采购合同" ||
		restored.Draft.Supplier.BankAccount != testRestrictedBankAccount {
		t.Fatalf("restored draft mismatch: %+v", restored.Draft)
	}
	if restored.Summary.Amount == nil || *restored.Summary.Amount != "100000.00" {
		t.Fatalf("summary facts %+v", restored.Summary)
	}

	drafts, err := recovery.ListMyContractDrafts(ctx, admin, 25)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("own draft list: %v %+v", err, drafts)
	}
	if drafts[0].ContractID != detail.Summary.ContractID || drafts[0].Name != "测试设备采购合同" {
		t.Fatalf("list item %+v", drafts[0])
	}

	replayed, err := fixture.core.CreateContractDraft(ctx, admin, sampleCreateInput("create-recover-0001"))
	if err != nil {
		t.Fatalf("same-key same-payload replay failed: %v", err)
	}
	if replayed.Summary.ContractID != detail.Summary.ContractID {
		t.Fatalf("replay created a second contract: %s", replayed.Summary.ContractID)
	}

	conflict := sampleCreateInput("create-recover-0001")
	conflict.Draft.Contract.Name = "另一个合同"
	_, err = fixture.core.CreateContractDraft(ctx, admin, conflict)
	assertFaultStatus(t, err, 409, "same-key different-payload")

	replacement := sampleDraftContent()
	replacement.Contract.Name = "替换后的合同"
	replacement.Supplier.BankAccount = "6222020200007654321"
	_, err = fixture.core.ReplaceContractDraft(ctx, admin, ReplaceContractDraftInput{
		ContractID: detail.Summary.ContractID, ExpectedRowVersion: 99,
		Draft: replacement, IdempotencyKey: "replace-conflict-0001",
	})
	assertFaultStatus(t, err, 409, "stale expected_row_version")

	replaced, err := fixture.core.ReplaceContractDraft(ctx, admin, ReplaceContractDraftInput{
		ContractID: detail.Summary.ContractID, ExpectedRowVersion: 1,
		Draft: replacement, IdempotencyKey: "replace-recover-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Summary.RowVersion != 2 || replaced.Draft.Contract.Name != "替换后的合同" {
		t.Fatalf("replace result %+v", replaced)
	}
	replayedReplace, err := fixture.core.ReplaceContractDraft(ctx, admin, ReplaceContractDraftInput{
		ContractID: detail.Summary.ContractID, ExpectedRowVersion: 1,
		Draft: replacement, IdempotencyKey: "replace-recover-0001",
	})
	if err != nil || replayedReplace.Summary.RowVersion != 2 {
		t.Fatalf("replace replay must return the committed result: %v %+v", err, replayedReplace)
	}

	// Restricted field values stay sealed in every stored representation.
	var storedContent, storedReceipt string
	if err := fixture.core.pool.QueryRow(ctx,
		`SELECT content::text FROM clm_contract_drafts WHERE tenant_id=$1 AND contract_id=$2`,
		admin.TenantID, detail.Summary.ContractID).Scan(&storedContent); err != nil {
		t.Fatal(err)
	}
	if err := fixture.core.pool.QueryRow(ctx,
		`SELECT response_json::text FROM clm_contract_write_receipts
		 WHERE tenant_id=$1 AND actor_id=$2 AND route_scope=$3 AND idempotency_key=$4`,
		admin.TenantID, admin.ID, clmRouteCreateDraft, "create-recover-0001").Scan(&storedReceipt); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{testRestrictedBankAccount, "6222020200007654321"} {
		if strings.Contains(storedContent, forbidden) || strings.Contains(storedReceipt, forbidden) {
			t.Fatal("plaintext bank account reached persistent storage")
		}
	}
	if !strings.Contains(storedContent, "clmv1:") {
		t.Fatal("restricted field was not sealed at rest")
	}
}

func TestContractDraftTenantAndGrantBoundaries(t *testing.T) {
	fixture, recovery := newContractDraftTestEnv(t)
	ctx := context.Background()
	admin := fixture.admin

	detail, err := fixture.core.CreateContractDraft(ctx, admin, sampleCreateInput("boundary-create-0001"))
	if err != nil {
		t.Fatal(err)
	}
	contractID := detail.Summary.ContractID

	otherTenant, _ := accounts.ByID("demo-tenant2-admin")
	_, err = recovery.LoadContractDetail(ctx, otherTenant, contractID)
	assertFaultStatus(t, err, 404, "cross-tenant read")
	_, err = fixture.core.ReplaceContractDraft(ctx, otherTenant, ReplaceContractDraftInput{
		ContractID: contractID, ExpectedRowVersion: 1,
		Draft: sampleDraftContent(), IdempotencyKey: "boundary-other-tenant-01",
	})
	assertFaultStatus(t, err, 404, "cross-tenant replace")

	otherOrg, _ := accounts.ByID("demo-otherorg-clerk")
	_, err = fixture.core.LoadContractDetail(ctx, otherOrg, contractID)
	assertFaultStatus(t, err, 404, "same-tenant other-org read")

	nonOwner, _ := accounts.ByID("demo-clerk")
	_, err = fixture.core.LoadContractDetail(ctx, nonOwner, contractID)
	assertFaultStatus(t, err, 404, "same-tenant non-creator read")
	_, err = fixture.core.ReplaceContractDraft(ctx, nonOwner, ReplaceContractDraftInput{
		ContractID: contractID, ExpectedRowVersion: 1,
		Draft: sampleDraftContent(), IdempotencyKey: "boundary-non-owner-001",
	})
	assertFaultStatus(t, err, 404, "non-creator replace")

	viewer, _ := accounts.ByID("demo-viewer")
	_, err = fixture.core.CreateContractDraft(ctx, viewer, sampleCreateInput("boundary-viewer-0001"))
	assertFaultStatus(t, err, 403, "viewer role cannot create contracts")

	nonOwnerList, err := fixture.core.ListMyContractDrafts(ctx, nonOwner, 25)
	if err != nil || len(nonOwnerList) != 0 {
		t.Fatalf("non-creator draft list must be empty: %v %+v", err, nonOwnerList)
	}
}

func TestLegacyImportKeepsHistoryUnknownAndVerifiesMaterial(t *testing.T) {
	fixture, recovery := newContractDraftTestEnv(t)
	ctx := context.Background()
	admin := fixture.admin

	sourceBody := []byte("存量已签合同原件：设备采购，总价100万元。")
	upload, err := fixture.document.UploadMaterial(ctx, admin, fixture.caseID, "attachment",
		"legacy-contract.txt", "text/plain", sourceBody, MaterialUploadOptions{IdempotencyKey: "legacy-source-material"})
	if err != nil {
		t.Fatal(err)
	}
	sourceDigest := sha256.Sum256(sourceBody)
	sourceRef := MaterialRef{
		MaterialID: upload.MaterialID, MaterialVersionID: upload.DocumentID,
		SHA256: hex.EncodeToString(sourceDigest[:]), Purpose: "signed_contract",
	}

	historyBody := []byte("历史会签扫描件。")
	historyUpload, err := fixture.document.UploadMaterial(ctx, admin, fixture.caseID, "attachment",
		"legacy-approval.txt", "text/plain", historyBody, MaterialUploadOptions{IdempotencyKey: "legacy-history-material"})
	if err != nil {
		t.Fatal(err)
	}
	historyDigest := sha256.Sum256(historyBody)
	historyRef := MaterialRef{
		MaterialID: historyUpload.MaterialID, MaterialVersionID: historyUpload.DocumentID,
		SHA256: hex.EncodeToString(historyDigest[:]), Purpose: "supporting",
	}

	input := LegacyImportInput{
		ExpectedRowVersion: 0, AuthorityMode: "native",
		SourceRef:             SourceRef{System: "legacy-erp", ID: "HT-2019-077", SourceVersion: "1"},
		SourceEvidence:        sourceRef,
		Snapshot:              sampleDraftContent(),
		ApprovalHistoryStatus: "evidence_attached",
		Reason:                "2019年签订的存量合同补录台账",
		IdempotencyKey:        "legacy-import-0001",
	}
	_, err = fixture.core.ImportLegacySignedContract(ctx, admin, input)
	assertFaultStatus(t, err, 422, "evidence_attached requires materials")

	input.ApprovalHistoryStatus = "unknown"
	input.HistoricalApprovalMaterials = []MaterialRef{historyRef}
	_, err = fixture.core.ImportLegacySignedContract(ctx, admin, input)
	assertFaultStatus(t, err, 422, "unknown history must not cite materials")

	input.HistoricalApprovalMaterials = nil
	detail, err := fixture.core.ImportLegacySignedContract(ctx, admin, input)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Summary.ContractState != "registered" || detail.Summary.RowVersion != 1 {
		t.Fatalf("imported summary %+v", detail.Summary)
	}
	if detail.SourceRef == nil || detail.SourceRef.System != "legacy-erp" || detail.SourceObservedAt == nil {
		t.Fatalf("import provenance %+v", detail)
	}
	foundWarning := false
	for _, warning := range detail.Warnings {
		foundWarning = foundWarning || warning == "approval_history_unknown"
	}
	if !foundWarning {
		t.Fatalf("unknown history must warn: %v", detail.Warnings)
	}
	if len(detail.AllowedCommands) != 0 {
		t.Fatalf("imported contract commands %v", detail.AllowedCommands)
	}
	if detail.Draft == nil || detail.Draft.Supplier.BankAccount != testRestrictedBankAccount {
		t.Fatalf("importer must read back the sealed snapshot: %+v", detail.Draft)
	}

	restored, err := recovery.LoadContractDetail(ctx, admin, detail.Summary.ContractID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.CurrentRevisionID == nil {
		t.Fatal("import must freeze one revision")
	}

	var historyStatus string
	var historicalRaw string
	var rounds int
	if err := fixture.core.pool.QueryRow(ctx,
		`SELECT approval_history_status, historical_approval_materials::text FROM clm_legacy_imports
		 WHERE tenant_id=$1 AND contract_id=$2`, admin.TenantID, detail.Summary.ContractID).
		Scan(&historyStatus, &historicalRaw); err != nil {
		t.Fatal(err)
	}
	if historyStatus != "unknown" || historicalRaw != "[]" {
		t.Fatalf("unknown history mutated: %s %s", historyStatus, historicalRaw)
	}
	if err := fixture.core.pool.QueryRow(ctx,
		`SELECT count(*) FROM clm_approval_rounds WHERE tenant_id=$1 AND contract_id=$2`,
		admin.TenantID, detail.Summary.ContractID).Scan(&rounds); err != nil {
		t.Fatal(err)
	}
	if rounds != 0 {
		t.Fatalf("legacy import fabricated %d approval rounds", rounds)
	}
	var caseLinks, events int
	if err := fixture.core.pool.QueryRow(ctx,
		`SELECT count(*) FROM clm_contract_cases WHERE tenant_id=$1 AND contract_id=$2 AND case_id=$3`,
		admin.TenantID, detail.Summary.ContractID, fixture.caseID).Scan(&caseLinks); err != nil {
		t.Fatal(err)
	}
	if caseLinks != 1 {
		t.Fatalf("source case link count %d", caseLinks)
	}
	if err := fixture.core.pool.QueryRow(ctx,
		`SELECT count(*) FROM clm_business_events e JOIN clm_outbox_delivery o
		 ON o.tenant_id=e.tenant_id AND o.event_id=e.event_id
		 WHERE e.tenant_id=$1 AND e.contract_id=$2 AND e.event_type=$3`,
		admin.TenantID, detail.Summary.ContractID, clmEventLegacyImported).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("legacy import event/outbox rows %d", events)
	}

	replayed, err := fixture.core.ImportLegacySignedContract(ctx, admin, input)
	if err != nil || replayed.Summary.ContractID != detail.Summary.ContractID {
		t.Fatalf("legacy replay: %v %+v", err, replayed)
	}
	input.Reason = "不同载荷的重复导入"
	_, err = fixture.core.ImportLegacySignedContract(ctx, admin, input)
	assertFaultStatus(t, err, 409, "legacy replay with different payload")

	digestMismatch := input
	digestMismatch.IdempotencyKey = "legacy-import-0002"
	digestMismatch.SourceEvidence.SHA256 = strings.Repeat("b", 64)
	_, err = fixture.core.ImportLegacySignedContract(ctx, admin, digestMismatch)
	assertFaultStatus(t, err, 422, "digest mismatch")

	unknownMaterial := input
	unknownMaterial.IdempotencyKey = "legacy-import-0003"
	unknownMaterial.SourceEvidence.MaterialVersionID = "doc_does_not_exist"
	_, err = fixture.core.ImportLegacySignedContract(ctx, admin, unknownMaterial)
	assertFaultStatus(t, err, 404, "unresolvable material")

	otherTenant, _ := accounts.ByID("demo-tenant2-admin")
	_, err = fixture.core.ImportLegacySignedContract(ctx, otherTenant, input)
	assertFaultStatus(t, err, 404, "another tenant cannot resolve the source material")

	// evidence_attached keeps only verified materials, enriched with the
	// resolved case identity.
	withHistory := input
	withHistory.IdempotencyKey = "legacy-import-0004"
	withHistory.SourceRef = SourceRef{System: "legacy-erp", ID: "HT-2019-078", SourceVersion: "1"}
	withHistory.ApprovalHistoryStatus = "evidence_attached"
	withHistory.HistoricalApprovalMaterials = []MaterialRef{historyRef}
	withHistoryDetail, err := fixture.core.ImportLegacySignedContract(ctx, admin, withHistory)
	if err != nil {
		t.Fatal(err)
	}
	if len(withHistoryDetail.Warnings) != 0 {
		t.Fatalf("evidence_attached must not warn: %v", withHistoryDetail.Warnings)
	}
	var historicalCount int
	if err := fixture.core.pool.QueryRow(ctx,
		`SELECT jsonb_array_length(historical_approval_materials) FROM clm_legacy_imports
		 WHERE tenant_id=$1 AND contract_id=$2`, admin.TenantID, withHistoryDetail.Summary.ContractID).
		Scan(&historicalCount); err != nil {
		t.Fatal(err)
	}
	if historicalCount != 1 {
		t.Fatalf("historical materials stored: %d", historicalCount)
	}

	var snapshotText string
	if err := fixture.core.pool.QueryRow(ctx,
		`SELECT r.snapshot::text FROM clm_contract_revisions r
		 WHERE r.tenant_id=$1 AND r.contract_id=$2`,
		admin.TenantID, detail.Summary.ContractID).Scan(&snapshotText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snapshotText, testRestrictedBankAccount) {
		t.Fatal("frozen legacy snapshot contains the plaintext bank account")
	}
}

func TestContractDraftRestrictedFieldFailsWithoutKey(t *testing.T) {
	fixture, _ := newContractDraftTestEnv(t)
	ctx := context.Background()
	admin := fixture.admin

	t.Setenv("CLM_DATA_KEY", "")

	_, err := fixture.core.CreateContractDraft(ctx, admin, sampleCreateInput("no-key-restricted-01"))
	assertFaultStatus(t, err, 503, "restricted draft without configured key")

	plain := sampleCreateInput("no-key-plain-0001")
	plain.Draft.Supplier = &Supplier{Name: "无账户供应商"}
	detail, err := fixture.core.CreateContractDraft(ctx, admin, plain)
	if err != nil {
		t.Fatalf("draft without restricted fields must not need the key: %v", err)
	}
	if _, err := fixture.core.LoadContractDetail(ctx, admin, detail.Summary.ContractID); err != nil {
		t.Fatalf("unrestricted detail read must not need the key: %v", err)
	}
}

// TestContractDraftReceiptScopeIsolation covers the receipt namespace: the
// key is scoped to tenant and actor, so another tenant's same key creates its
// own contract instead of replaying or blocking the first one.
func TestContractDraftReceiptScopeIsolation(t *testing.T) {
	fixture, _ := newContractDraftTestEnv(t)
	ctx := context.Background()
	admin := fixture.admin

	detail, err := fixture.core.CreateContractDraft(ctx, admin, sampleCreateInput("replay-scope-0001"))
	if err != nil {
		t.Fatal(err)
	}
	otherTenant, _ := accounts.ByID("demo-tenant2-admin")
	otherDetail, err := fixture.core.CreateContractDraft(ctx, otherTenant, sampleCreateInput("replay-scope-0001"))
	if err != nil {
		t.Fatalf("another tenant's same key must stay isolated: %v", err)
	}
	if otherDetail.Summary.ContractID == detail.Summary.ContractID {
		t.Fatal("receipt keys leaked across tenants")
	}
	var responseStatus int
	if err := fixture.core.pool.QueryRow(ctx,
		`SELECT response_status FROM clm_contract_write_receipts
		 WHERE tenant_id=$1 AND actor_id=$2 AND route_scope=$3 AND idempotency_key=$4`,
		admin.TenantID, admin.ID, clmRouteCreateDraft, "replay-scope-0001").Scan(&responseStatus); err != nil {
		t.Fatal(err)
	}
	if responseStatus != 201 {
		t.Fatalf("receipt status %d", responseStatus)
	}
}

func TestLegacyImportPinsReferenceChainAndCheckReusesOriginalEndpoint(t *testing.T) {
	fixture, recovery := newContractDraftTestEnv(t)
	ctx := context.Background()
	admin := fixture.admin

	subject, _, err := fixture.core.CreateUnboundSubject(ctx, admin,
		SubjectCreateInput{DisplayName: "示例采购方"}, "reference-subject-0001")
	if err != nil {
		t.Fatal(err)
	}
	bound, _, err := fixture.core.BindExistingSubject(ctx, admin, fixture.caseID, BindExistingSubjectInput{
		Role: SubjectRoleCustomer, SubjectID: subject.SubjectID, ExpectedSubjectVersion: subject.CurrentVersion,
	}, "reference-subject-bind-0001")
	if err != nil {
		t.Fatal(err)
	}

	awardBody := []byte("中标通知书：包件 PKG-01。")
	award, err := fixture.document.UploadMaterial(ctx, admin, fixture.caseID, "award",
		"award.txt", "text/plain", awardBody, MaterialUploadOptions{IdempotencyKey: "reference-award-0001"})
	if err != nil {
		t.Fatal(err)
	}
	awardDigest := sha256.Sum256(awardBody)

	sourceBody := []byte("存量已签合同原件。")
	source, err := fixture.document.UploadMaterial(ctx, admin, fixture.caseID, "attachment",
		"legacy-source.txt", "text/plain", sourceBody, MaterialUploadOptions{IdempotencyKey: "reference-source-0001"})
	if err != nil {
		t.Fatal(err)
	}
	sourceDigest := sha256.Sum256(sourceBody)
	sourceRef := MaterialRef{
		MaterialID: source.MaterialID, MaterialVersionID: source.DocumentID,
		SHA256: hex.EncodeToString(sourceDigest[:]), Purpose: "signed_contract",
	}

	snapshot := sampleDraftContent()
	snapshot.Contract.RelatedParties = []PartyRef{{
		PartyID: subject.SubjectID, Role: "buyer", NameSnapshot: bound.DisplayName, BindingID: bound.BindingID,
	}}
	snapshot.Materials = []MaterialRef{{
		MaterialID: award.MaterialID, MaterialVersionID: award.DocumentID,
		SHA256: hex.EncodeToString(awardDigest[:]), Purpose: "supporting",
	}}
	snapshot.Contract.ProcurementResultPackage = &ProcurementRef{
		System: "erp-procurement", ResultID: "PR-2026-0001", PackageID: "PKG-01",
		SnapshotRef: award.DocumentID,
	}

	input := LegacyImportInput{
		ExpectedRowVersion: 0, AuthorityMode: "native",
		SourceRef:             SourceRef{System: "legacy-erp", ID: "HT-2026-REF-01", SourceVersion: "1"},
		SourceEvidence:        sourceRef,
		Snapshot:              snapshot,
		ApprovalHistoryStatus: "unknown",
		Reason:                "核验主体材料链与采购结果来源快照",
		IdempotencyKey:        "reference-import-0001",
	}
	detail, err := fixture.core.ImportLegacySignedContract(ctx, admin, input)
	if err != nil {
		t.Fatal(err)
	}

	var storedSnapshot string
	if err := fixture.core.pool.QueryRow(ctx,
		`SELECT snapshot::text FROM clm_contract_revisions
		 WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`,
		admin.TenantID, detail.Summary.ContractID, *detail.CurrentRevisionID).Scan(&storedSnapshot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(storedSnapshot, `"reference_snapshot"`) ||
		!strings.Contains(storedSnapshot, `"source_snapshot"`) ||
		!strings.Contains(storedSnapshot, bound.BindingID) ||
		!strings.Contains(storedSnapshot, award.DocumentID) {
		t.Fatal("frozen revision does not pin the resolved reference chain")
	}

	check, err := recovery.CheckContractReferences(ctx, admin, detail.Summary.ContractID, "")
	if err != nil {
		t.Fatal(err)
	}
	if check.Source != "revision" || check.ContractRevisionID == nil || *check.ContractRevisionID != *detail.CurrentRevisionID {
		t.Fatalf("check source %+v", check)
	}
	if len(check.Subjects) != 1 || check.Subjects[0].BindingID != bound.BindingID ||
		check.Subjects[0].BindingVersion != bound.BindingVersion || !check.Subjects[0].Frozen {
		t.Fatalf("subject reference %+v", check.Subjects)
	}
	if len(check.Materials) != 1 || check.Materials[0].OriginalPath !=
		"/api/cases/"+fixture.caseID+"/documents/"+award.DocumentID+"/original" {
		t.Fatalf("material reference %+v", check.Materials)
	}
	if check.ProcurementResult == nil || check.ProcurementResult.MaterialRole != "award" ||
		check.ProcurementResult.MaterialVersionID != award.DocumentID {
		t.Fatalf("procurement reference %+v", check.ProcurementResult)
	}

	// Replacing the source material after freeze must not drift the frozen
	// revision: the pin keeps pointing at the document that was verified.
	replacement, err := fixture.document.UploadMaterial(ctx, admin, fixture.caseID, "award",
		"award-v2.txt", "text/plain", []byte("中标通知书修订版：包件 PKG-01。"),
		MaterialUploadOptions{Intent: "replace", MaterialID: award.MaterialID,
			ReplacesDocumentID: award.DocumentID, IdempotencyKey: "reference-award-0002"})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.DocumentID == award.DocumentID || replacement.Version != award.Version+1 {
		t.Fatalf("replacement material version %+v", replacement)
	}
	afterReplacement, err := recovery.CheckContractReferences(ctx, admin, detail.Summary.ContractID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(afterReplacement.Materials) != 1 || afterReplacement.Materials[0].MaterialVersionID != award.DocumentID ||
		afterReplacement.Materials[0].SHA256 != hex.EncodeToString(awardDigest[:]) ||
		afterReplacement.Materials[0].MaterialVersion != award.Version || !afterReplacement.Materials[0].Frozen {
		t.Fatalf("frozen material drifted after replacement: %+v", afterReplacement.Materials)
	}
	if afterReplacement.ProcurementResult == nil ||
		afterReplacement.ProcurementResult.MaterialVersionID != award.DocumentID ||
		afterReplacement.ProcurementResult.SHA256 != hex.EncodeToString(awardDigest[:]) {
		t.Fatalf("procurement snapshot drifted after replacement: %+v", afterReplacement.ProcurementResult)
	}

	otherTenant, _ := accounts.ByID("demo-tenant2-admin")
	_, err = recovery.CheckContractReferences(ctx, otherTenant, detail.Summary.ContractID, "")
	assertFaultStatus(t, err, 404, "cross-tenant reference check")

	otherCaseID, err := fixture.core.CreateCase(admin, "引用链其他案件", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	foreignBody := []byte("其他案件的中标材料。")
	foreign, err := fixture.document.UploadMaterial(ctx, admin, otherCaseID, "award",
		"foreign-award.txt", "text/plain", foreignBody, MaterialUploadOptions{IdempotencyKey: "reference-foreign-award-01"})
	if err != nil {
		t.Fatal(err)
	}
	foreignDigest := sha256.Sum256(foreignBody)
	foreignSnapshot := sampleDraftContent()
	foreignSnapshot.Materials = []MaterialRef{{
		MaterialID: foreign.MaterialID, MaterialVersionID: foreign.DocumentID,
		SHA256: hex.EncodeToString(foreignDigest[:]), Purpose: "supporting",
	}}
	foreignInput := input
	foreignInput.IdempotencyKey = "reference-import-0002"
	foreignInput.SourceRef = SourceRef{System: "legacy-erp", ID: "HT-2026-REF-02", SourceVersion: "1"}
	foreignInput.Snapshot = foreignSnapshot
	_, err = fixture.core.ImportLegacySignedContract(ctx, admin, foreignInput)
	assertFaultStatus(t, err, 422, "cross-case material reference")

	var pinned DraftContent
	if err := json.Unmarshal([]byte(storedSnapshot), &pinned); err != nil {
		t.Fatal(err)
	}
	tx, err := fixture.core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(admin)); err != nil {
		t.Fatal(err)
	}
	anchor := Case{ID: fixture.caseID, TenantID: admin.TenantID, OwnerOrgID: admin.OrgID,
		OwnerID: admin.ID, MemberIDs: []string{admin.ID}, BusinessStatus: "active"}
	tamperedProcurement := pinned
	tamperedContract := *pinned.Contract
	tamperedResult := *pinned.Contract.ProcurementResultPackage
	tamperedSource := *tamperedResult.SourceSnapshot
	tamperedSource.SHA256 = strings.Repeat("d", 64)
	tamperedResult.SourceSnapshot = &tamperedSource
	tamperedContract.ProcurementResultPackage = &tamperedResult
	tamperedProcurement.Contract = &tamperedContract
	tamperedProcurement.Materials = []MaterialRef{pinned.Materials[0]}
	_, err = resolveContractReferences(ctx, tx, admin, anchor, &tamperedProcurement, referenceModeFrozen)
	assertFaultStatus(t, err, 409, "tampered procurement source snapshot")

	tamperedSubject := pinned
	tamperedSubject.Contract.RelatedParties = []PartyRef{pinned.Contract.RelatedParties[0]}
	tamperedSubject.Contract.RelatedParties[0].ReferenceSnapshot = new(PartyReferenceSnapshot)
	*tamperedSubject.Contract.RelatedParties[0].ReferenceSnapshot = *pinned.Contract.RelatedParties[0].ReferenceSnapshot
	tamperedSubject.Contract.RelatedParties[0].ReferenceSnapshot.SubjectPayloadHash = strings.Repeat("f", 64)
	_, err = resolveContractReferences(ctx, tx, admin, anchor, &tamperedSubject, referenceModeFrozen)
	assertFaultStatus(t, err, 409, "tampered frozen subject pin")

	tamperedMaterial := pinned
	tamperedMaterial.Materials = []MaterialRef{pinned.Materials[0]}
	tamperedMaterial.Materials[0].SHA256 = strings.Repeat("e", 64)
	tamperedMaterial.Contract.ProcurementResultPackage = nil
	_, err = resolveContractReferences(ctx, tx, admin, anchor, &tamperedMaterial, referenceModeFrozen)
	assertFaultStatus(t, err, 409, "tampered frozen material digest")

	if _, err := fixture.core.pool.Exec(ctx,
		`UPDATE clm_contract_revisions SET snapshot=snapshot WHERE tenant_id=$1 AND contract_id=$2`,
		admin.TenantID, detail.Summary.ContractID); err == nil {
		t.Fatal("frozen revision must reject rewrite")
	}
}
