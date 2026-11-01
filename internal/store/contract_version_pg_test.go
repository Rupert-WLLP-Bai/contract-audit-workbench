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

// Durable checks for #81: immutable revision rows, the same-transaction child
// insert guard, unconditional StageFinal, material summaries that survive a
// same-name replacement, restricted-field diff flags and tenant/role
// boundaries. They require TEST_DATABASE_URL and MIGRATOR_DATABASE_URL.

func freezeTestContract(t *testing.T, fixture durableCaseFixture) (string, FreezeContractVersionResult) {
	t.Helper()
	ctx := context.Background()
	detail, err := fixture.core.CreateContractDraft(ctx, fixture.admin, CreateContractDraftInput{
		ExpectedRowVersion: 0, AuthorityMode: "native",
		Draft: freezeCompleteDraft(), IdempotencyKey: "freeze-create-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		ContractID: detail.Summary.ContractID, ExpectedRowVersion: detail.Summary.RowVersion,
		IdempotencyKey: "freeze-version-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	return detail.Summary.ContractID, result
}

func TestContractVersionFreezeIsImmutableAndIdempotent(t *testing.T) {
	fixture, recovery := newContractDraftTestEnv(t)
	ctx := context.Background()
	contractID, result := freezeTestContract(t, fixture)
	if result.Version.RevisionNumber != 1 || !result.Version.AmountsReconciled ||
		result.ContractState != "draft" || result.CurrentRevisionID != result.Version.ContractRevisionID {
		t.Fatalf("unexpected freeze result %+v", result)
	}
	if result.RowVersion != 2 {
		t.Fatalf("freeze row version = %d", result.RowVersion)
	}
	replayed, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		ContractID: contractID, ExpectedRowVersion: 1, IdempotencyKey: "freeze-version-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Version.ContractRevisionID != result.Version.ContractRevisionID {
		t.Fatal("same idempotency key must replay the committed revision")
	}
	var revisions int
	if err := fixture.core.pool.QueryRow(ctx, `SELECT count(*) FROM clm_contract_revisions
		WHERE tenant_id=$1 AND contract_id=$2`, fixture.admin.TenantID, contractID).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if revisions != 1 {
		t.Fatalf("replay inserted %d revisions", revisions)
	}
	var snapshot []byte
	var digest, frozenBy string
	var reconciled bool
	if err := fixture.core.pool.QueryRow(ctx, `SELECT snapshot,snapshot_digest,frozen_by,amounts_reconciled
		FROM clm_contract_revisions WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`,
		fixture.admin.TenantID, contractID, result.Version.ContractRevisionID).Scan(
		&snapshot, &digest, &frozenBy, &reconciled); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(snapshot)
	if hex.EncodeToString(sum[:]) != digest || digest != result.Version.SnapshotDigest || !reconciled || frozenBy != fixture.admin.ID {
		t.Fatalf("revision row mismatch: digest=%s reconciled=%v frozen_by=%s", digest, reconciled, frozenBy)
	}
	if strings.Contains(string(snapshot), testRestrictedBankAccount) {
		t.Fatal("frozen revision snapshot contains restricted plaintext")
	}
	var sealCount, materialCount int
	if err := fixture.core.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM clm_contract_version_seals WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3),
		(SELECT count(*) FROM clm_contract_version_materials WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3)`,
		fixture.admin.TenantID, contractID, result.Version.ContractRevisionID).Scan(&sealCount, &materialCount); err != nil {
		t.Fatal(err)
	}
	if sealCount != 1 || materialCount != 0 {
		t.Fatalf("seal=%d materials=%d", sealCount, materialCount)
	}
	if _, err := fixture.core.pool.Exec(ctx, `UPDATE clm_contract_revisions SET snapshot='{}'::jsonb
		WHERE tenant_id=$1 AND contract_id=$2`, fixture.admin.TenantID, contractID); err == nil ||
		!strings.Contains(err.Error(), "append-only") {
		t.Fatalf("revision update must be rejected by append-only trigger: %v", err)
	}
	if _, err := fixture.core.pool.Exec(ctx, `INSERT INTO clm_contract_version_seals
		(tenant_id,contract_id,contract_revision_id,snapshot_digest,materials_digest,content_fingerprint,restricted_fingerprints)
		VALUES ($1,$2,$3,$4,$4,$4,'{}'::jsonb)`,
		fixture.admin.TenantID, contractID, result.Version.ContractRevisionID, digest); err == nil ||
		!strings.Contains(err.Error(), "only be inserted with their revision") {
		t.Fatalf("later seal insert must be rejected by the child guard: %v", err)
	}
	// The draft stays editable; a second freeze appends revision 2 without
	// touching revision 1.
	second, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		ContractID: contractID, ExpectedRowVersion: result.RowVersion, IdempotencyKey: "freeze-version-0002",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Version.RevisionNumber != 2 {
		t.Fatalf("second revision number = %d", second.Version.RevisionNumber)
	}
	detail, err := recovery.LoadContractDetail(ctx, fixture.admin, contractID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.CurrentRevisionID == nil || *detail.CurrentRevisionID != second.CurrentRevisionID {
		t.Fatalf("current revision = %+v, want %s", detail.CurrentRevisionID, second.CurrentRevisionID)
	}
	// A successful receipt must not replay to a caller who has since lost
	// visibility. The actor/tenant scope still matches, so the visibility
	// recheck is what refuses the replay.
	if _, err := fixture.core.pool.Exec(ctx, `UPDATE clm_contracts SET created_by='demo-viewer'
		WHERE tenant_id=$1 AND contract_id=$2`, fixture.admin.TenantID, contractID); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		ContractID: contractID, ExpectedRowVersion: 1, IdempotencyKey: "freeze-version-0001",
	})
	assertFaultStatus(t, err, 404, "replay after losing visibility")
}

func TestFreezeRunsStageFinalWithoutLegacyGate(t *testing.T) {
	fixture, _ := newContractDraftTestEnv(t)
	ctx := context.Background()
	incomplete := DraftContent{Contract: &ContractBasics{Name: "金额未知草稿"}}
	detail, err := fixture.core.CreateContractDraft(ctx, fixture.admin, CreateContractDraftInput{
		ExpectedRowVersion: 0, AuthorityMode: "native", Draft: incomplete,
		IdempotencyKey: "freeze-create-0002",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		ContractID: detail.Summary.ContractID, ExpectedRowVersion: 1, IdempotencyKey: "freeze-version-0003",
	})
	assertFaultStatus(t, err, 422, "amount-unknown freeze must fail StageFinal")
	var revisions int
	if err := fixture.core.pool.QueryRow(ctx, `SELECT count(*) FROM clm_contract_revisions
		WHERE tenant_id=$1 AND contract_id=$2`, fixture.admin.TenantID, detail.Summary.ContractID).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if revisions != 0 {
		t.Fatalf("rejected freeze inserted %d revisions", revisions)
	}
	var rowVersion int64
	if err := fixture.core.pool.QueryRow(ctx, `SELECT row_version FROM clm_contracts
		WHERE tenant_id=$1 AND contract_id=$2`, fixture.admin.TenantID, detail.Summary.ContractID).Scan(&rowVersion); err != nil {
		t.Fatal(err)
	}
	if rowVersion != 1 {
		t.Fatalf("rejected freeze changed row_version to %d", rowVersion)
	}
}

func TestFrozenMaterialSummarySurvivesSameNameReplacement(t *testing.T) {
	fixture, _ := newContractDraftTestEnv(t)
	ctx := context.Background()
	firstBody := []byte("第一版同名附件")
	upload, err := fixture.document.UploadMaterial(ctx, fixture.admin, fixture.caseID, "attachment",
		"same-name.txt", "text/plain", firstBody, MaterialUploadOptions{IdempotencyKey: "same-name-v1"})
	if err != nil {
		t.Fatal(err)
	}
	firstDigest := sha256.Sum256(firstBody)
	material := MaterialRef{
		MaterialID: upload.MaterialID, MaterialVersionID: upload.DocumentID,
		SHA256: hex.EncodeToString(firstDigest[:]), Purpose: "supporting",
	}
	draft := freezeCompleteDraft()
	draft.Materials = []MaterialRef{material}
	created, err := fixture.core.CreateContractDraft(ctx, fixture.admin, CreateContractDraftInput{
		ExpectedRowVersion: 0, AuthorityMode: "native", Draft: draft, IdempotencyKey: "freeze-create-0003",
	})
	if err != nil {
		t.Fatal(err)
	}
	contractID := created.Summary.ContractID
	// A same-name upload replaces the material while the draft still cites
	// version 1. Freeze must bind the cited version and its digest.
	secondBody := []byte("第二版同名附件，内容不同")
	replaced, err := fixture.document.UploadMaterial(ctx, fixture.admin, fixture.caseID, "attachment",
		"same-name.txt", "text/plain", secondBody, MaterialUploadOptions{
			Intent: "replace", MaterialID: upload.MaterialID, ReplacesDocumentID: upload.DocumentID,
			IdempotencyKey: "same-name-v2",
		})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Version != 2 || replaced.DocumentID == upload.DocumentID {
		t.Fatalf("same-name replacement did not create version 2: %+v", replaced)
	}
	first, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		ContractID: contractID, ExpectedRowVersion: created.Summary.RowVersion, IdempotencyKey: "freeze-version-0004",
	})
	if err != nil {
		t.Fatal(err)
	}
	var frozenDigest, filename string
	var frozenVersion int
	if err := fixture.core.pool.QueryRow(ctx, `SELECT digest,filename,material_version
		FROM clm_contract_version_materials WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND material_id=$4`,
		fixture.admin.TenantID, contractID, first.Version.ContractRevisionID, upload.MaterialID).Scan(
		&frozenDigest, &filename, &frozenVersion); err != nil {
		t.Fatal(err)
	}
	if frozenDigest != hex.EncodeToString(firstDigest[:]) || frozenVersion != 1 || filename != "same-name.txt" {
		t.Fatalf("frozen material summary was rewritten: digest=%s version=%d file=%s", frozenDigest, frozenVersion, filename)
	}
	if _, err := fixture.core.pool.Exec(ctx, `UPDATE clm_contract_version_materials SET filename='overwritten.txt'
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`,
		fixture.admin.TenantID, contractID, first.Version.ContractRevisionID); err == nil ||
		!strings.Contains(err.Error(), "append-only") {
		t.Fatalf("frozen material update must be rejected: %v", err)
	}

	// Point a new draft at version 2 and freeze revision 2, then diff.
	secondDigest := sha256.Sum256(secondBody)
	secondMaterial := material
	secondMaterial.MaterialVersionID = replaced.DocumentID
	secondMaterial.SHA256 = hex.EncodeToString(secondDigest[:])
	draft.Materials = []MaterialRef{secondMaterial}
	if _, err := fixture.core.ReplaceContractDraft(ctx, fixture.admin, ReplaceContractDraftInput{
		ContractID: contractID, ExpectedRowVersion: first.RowVersion, Draft: draft,
		IdempotencyKey: "freeze-replace-0001",
	}); err != nil {
		t.Fatal(err)
	}
	second, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		ContractID: contractID, ExpectedRowVersion: first.RowVersion + 1, IdempotencyKey: "freeze-version-0005",
	})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := fixture.core.DiffContractVersions(ctx, fixture.admin, contractID,
		first.Version.ContractRevisionID, second.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]ContractVersionFieldChange, len(diff.Fields))
	for _, change := range diff.Fields {
		byPath[change.Path] = change
	}
	prefix := "materials[material_id=" + upload.MaterialID + "]"
	digestChange, ok := byPath[prefix+".digest"]
	if !ok || digestChange.Before != hex.EncodeToString(firstDigest[:]) || digestChange.After != hex.EncodeToString(secondDigest[:]) {
		t.Fatalf("material digest change not located: %+v", digestChange)
	}
	versionChange, ok := byPath[prefix+".material_version"]
	if !ok || versionChange.Before != 1 || versionChange.After != 2 {
		t.Fatalf("material version change not located: %+v", versionChange)
	}
	if _, ok := byPath[prefix+".filename"]; ok {
		t.Fatal("same-name replacement must not report a filename change")
	}
}

func TestVersionDiffMasksRestrictedAndEnforcesVisibility(t *testing.T) {
	fixture, _ := newContractDraftTestEnv(t)
	ctx := context.Background()
	created, err := fixture.core.CreateContractDraft(ctx, fixture.admin, CreateContractDraftInput{
		ExpectedRowVersion: 0, AuthorityMode: "native", Draft: freezeCompleteDraft(), IdempotencyKey: "freeze-create-0004",
	})
	if err != nil {
		t.Fatal(err)
	}
	contractID := created.Summary.ContractID
	first, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		ContractID: contractID, ExpectedRowVersion: 1, IdempotencyKey: "freeze-version-0006",
	})
	if err != nil {
		t.Fatal(err)
	}
	changed := freezeCompleteDraft()
	changed.Contract.Amount = "105000.00"
	changed.Items[0].LineAmount = "105000.00"
	changed.Funding[0].Amount = "105000.00"
	changed.Payments[0].Amount = "105000.00"
	changed.Payments[0].UnitAmount = "105000.00"
	changed.Supplier.BankAccount = "6222000000000002"
	if _, err := fixture.core.ReplaceContractDraft(ctx, fixture.admin, ReplaceContractDraftInput{
		ContractID: contractID, ExpectedRowVersion: first.RowVersion, Draft: changed,
		IdempotencyKey: "freeze-replace-0002",
	}); err != nil {
		t.Fatal(err)
	}
	second, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		ContractID: contractID, ExpectedRowVersion: first.RowVersion + 1, IdempotencyKey: "freeze-version-0007",
	})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := fixture.core.DiffContractVersions(ctx, fixture.admin, contractID, first.CurrentRevisionID, second.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if diff.FromRevisionID == nil || diff.ToRevisionID != second.CurrentRevisionID || !diff.Changed {
		t.Fatalf("diff identity %+v", diff)
	}
	foundAmount := false
	for _, change := range diff.Fields {
		if change.Path == "contract.amount" {
			foundAmount = change.Before == "100000.00" && change.After == "105000.00" && change.Category == "amount"
		}
	}
	if !foundAmount {
		t.Fatalf("diff did not locate contract.amount: %+v", diff.Fields)
	}
	amountTerm := false
	for _, change := range diff.AmountTerms {
		amountTerm = amountTerm || change.Path == "contract.amount"
	}
	if !amountTerm {
		t.Fatalf("amount_terms missing contract.amount: %+v", diff.AmountTerms)
	}
	restricted := make(map[string]ContractVersionRestrictedChange, len(diff.RestrictedFields))
	for _, change := range diff.RestrictedFields {
		restricted[change.Path] = change
	}
	bank, ok := restricted["supplier.bank_account"]
	if !ok || bank.Changed == nil || !*bank.Changed {
		t.Fatalf("bank account change not reported: %+v", bank)
	}
	payee, ok := restricted["supplier.payee_name"]
	if !ok || payee.Changed == nil || *payee.Changed {
		t.Fatalf("unchanged payee must report changed=false: %+v", payee)
	}
	raw, err := json.Marshal(diff)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{testRestrictedBankAccount, "6222000000000002", "•••"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("diff leaked restricted material %q", forbidden)
		}
	}

	otherTenant, _ := accounts.ByID("demo-tenant2-admin")
	_, err = fixture.core.DiffContractVersions(ctx, otherTenant, contractID, first.CurrentRevisionID, second.CurrentRevisionID)
	assertFaultStatus(t, err, 404, "cross-tenant diff")
	nonOwner, _ := accounts.ByID("demo-clerk")
	_, err = fixture.core.DiffContractVersions(ctx, nonOwner, contractID, first.CurrentRevisionID, second.CurrentRevisionID)
	assertFaultStatus(t, err, 404, "non-creator diff")
	_, err = fixture.core.DiffContractVersions(ctx, fixture.admin, contractID, first.CurrentRevisionID, "c6052c81-8b05-49ce-89e9-f6e9c6403cff")
	assertFaultStatus(t, err, 404, "unknown revision")

	// The local demo binding binds an empty case grant to the creator. Reassign
	// this test contract to the viewer only to exercise read-only vs manage.
	if _, err := fixture.core.pool.Exec(ctx, `UPDATE clm_contracts SET created_by='demo-viewer'
		WHERE tenant_id=$1 AND contract_id=$2`, fixture.admin.TenantID, contractID); err != nil {
		t.Fatal(err)
	}
	viewer, _ := accounts.ByID("demo-viewer")
	if _, err := fixture.core.DiffContractVersions(ctx, viewer, contractID, first.CurrentRevisionID, second.CurrentRevisionID); err != nil {
		t.Fatalf("viewer with read authority must see the diff: %v", err)
	}
	_, err = fixture.core.FreezeContractVersion(ctx, viewer, FreezeContractVersionInput{
		ContractID: contractID, ExpectedRowVersion: second.RowVersion, IdempotencyKey: "freeze-version-0008",
	})
	assertFaultStatus(t, err, 403, "viewer cannot freeze")
}

func TestFreezeRejectsInvisibleContractsWith404(t *testing.T) {
	fixture, _ := newContractDraftTestEnv(t)
	ctx := context.Background()
	created, err := fixture.core.CreateContractDraft(ctx, fixture.admin, CreateContractDraftInput{
		ExpectedRowVersion: 0, AuthorityMode: "native", Draft: freezeCompleteDraft(), IdempotencyKey: "freeze-create-0005",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		user accounts.User
	}{
		{"cross-tenant", mustUser(t, "demo-tenant2-admin")},
		{"other-org", mustUser(t, "demo-otherorg-clerk")},
		{"non-creator", mustUser(t, "demo-clerk")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fixture.core.FreezeContractVersion(ctx, tc.user, FreezeContractVersionInput{
				ContractID: created.Summary.ContractID, ExpectedRowVersion: 1, IdempotencyKey: "freeze-denied-" + tc.name,
			})
			var fe *fault.Error
			if err == nil || !errorsAsFault(err, &fe) || fe.Status != 404 {
				t.Fatalf("expected 404, got %v", err)
			}
		})
	}
	if _, err := fixture.core.ListContractVersions(ctx, mustUser(t, "demo-tenant2-admin"), created.Summary.ContractID); err == nil {
		t.Fatal("cross-tenant revision list must be 404")
	}
}

func mustUser(t *testing.T, id string) accounts.User {
	t.Helper()
	user, ok := accounts.ByID(id)
	if !ok {
		t.Fatalf("unknown demo user %s", id)
	}
	return user
}

func errorsAsFault(err error, target **fault.Error) bool {
	return errors.As(err, target)
}
