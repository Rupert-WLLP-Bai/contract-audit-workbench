package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/clmsource"
	"github.com/jackc/pgx/v5"
)

func TestExternalContractSourceObservationOrderingAndAuthorization(t *testing.T) {
	fixture, core := newContractDraftTestEnv(t)
	ctx := context.Background()
	testCLMDataKey(t)
	admin := fixture.admin
	orgID := admin.OrgID
	authority := ContractSourceAuthority{
		TenantID: accounts.Tenant(admin), SourceID: "erp-readonly", KeyID: "source-key-1",
		Kind: string(clmsource.SourceMock), Ordering: clmsource.VersionMonotonicSequence,
		AllowedCases: []ContractSourceCaseScope{{OrgID: orgID, CaseID: fixture.caseID}},
	}
	makeInput := func(event, version, state, amount string, sequence uint64) ContractSourceObservationInput {
		snapshot := DraftContent{
			Contract: &ContractBasics{Name: "外部来源合同", Amount: amount, Currency: "CNY"},
			Supplier: &Supplier{Name: "来源供应方", BankAccount: testRestrictedBankAccount},
		}
		raw, err := json.Marshal(map[string]any{"event_id": event, "source_version": version, "snapshot": snapshot})
		if err != nil {
			t.Fatal(err)
		}
		return ContractSourceObservationInput{
			EventID: event, ExternalContractID: "erp-contract-7", SourceVersion: version,
			SourceSequence: &sequence, SourceState: state, OccurredAt: time.Now().UTC(),
			CaseOrgID: orgID, CaseID: fixture.caseID, Snapshot: snapshot, RawBody: raw,
		}
	}
	opaqueAuthority := authority
	opaqueAuthority.SourceID = "erp-opaque"
	opaqueAuthority.Ordering = clmsource.VersionOpaque
	opaqueInput := makeInput("opaque-first", "opaque-A", "received", "100.00", 0)
	opaqueInput.SourceSequence = nil
	opaqueInput.RawBody = []byte(`{"event_id":"opaque-first","source_version":"opaque-A"}`)
	opaque, err := core.RecordExternalContractObservation(ctx, opaqueAuthority, opaqueInput)
	if err != nil || opaque.Disposition != "observation_only" || opaque.CurrentSourceVersion != nil || opaque.ContractRevisionID != nil {
		t.Fatalf("first opaque observation must not become current: %+v err=%v", opaque, err)
	}
	opaqueDetail, err := core.LoadContractDetail(ctx, admin, opaque.ContractID)
	if err != nil || opaqueDetail.SourceID != opaqueAuthority.SourceID || opaqueDetail.SourceRef != nil || opaqueDetail.Summary.ContractState != "external_observed" {
		t.Fatalf("identity-only external contract projection: %+v err=%v", opaqueDetail, err)
	}
	unknownAuthority := authority
	unknownAuthority.SourceID = "erp-unknown"
	unknownFirst, err := core.RecordExternalContractObservation(ctx, unknownAuthority, makeInput("unknown-first", "seq-A", "received", "", 1))
	if err != nil || unknownFirst.Disposition != "unknown" || unknownFirst.CurrentSourceVersion != nil || unknownFirst.ContractRevisionID != nil {
		t.Fatalf("unknown first observation must not create a current revision: %+v err=%v", unknownFirst, err)
	}
	unknownNext, err := core.RecordExternalContractObservation(ctx, unknownAuthority, makeInput("unknown-next", "seq-B", "updated", "100.00", 2))
	if err != nil || unknownNext.Disposition != "applied" || unknownNext.CurrentSourceVersion == nil || *unknownNext.CurrentSourceVersion != "seq-B" || unknownNext.ContractRevisionID == nil {
		t.Fatalf("first valid monotonic version after unknown baseline: %+v err=%v", unknownNext, err)
	}

	firstInput := makeInput("event-v1", "opaque-A", "signed_at_source", "100.00", 1)
	first, err := core.RecordExternalContractObservation(ctx, authority, firstInput)
	if err != nil || first.Disposition != "applied" || first.ContractRevisionID == nil {
		t.Fatalf("initial external observation: %+v err=%v", first, err)
	}
	replay, err := core.RecordExternalContractObservation(ctx, authority, firstInput)
	if err != nil || !replay.Replayed || replay.ObservationID != first.ObservationID {
		t.Fatalf("same event replay: %+v err=%v", replay, err)
	}

	secondInput := makeInput("event-v2", "opaque-B", "changed_at_source", "200.00", 2)
	second, err := core.RecordExternalContractObservation(ctx, authority, secondInput)
	if err != nil || second.Disposition != "applied" || second.ContractRevisionID == nil || *second.ContractRevisionID == *first.ContractRevisionID {
		t.Fatalf("advancing sequence: %+v err=%v", second, err)
	}
	thirdInput := makeInput("event-v3", "opaque-C", "changed_again_at_source", "300.00", 3)
	third, err := core.RecordExternalContractObservation(ctx, authority, thirdInput)
	if err != nil || third.Disposition != "applied" || third.CurrentSourceVersion == nil || *third.CurrentSourceVersion != "opaque-C" {
		t.Fatalf("second sequence advance: %+v err=%v", third, err)
	}
	reusedHistoricalSequence := makeInput("event-v2-reuses-seq1", "opaque-B2", "late_conflicting", "150.00", 1)
	if _, err := core.RecordExternalContractObservation(ctx, authority, reusedHistoricalSequence); faultStatus(err) != 409 {
		t.Fatalf("a new version cannot reuse a historical sequence: %v", err)
	}

	staleInput := makeInput("event-v1-late", "opaque-A", "signed_at_source", "100.00", 1)
	stale, err := core.RecordExternalContractObservation(ctx, authority, staleInput)
	if err != nil || stale.Disposition != "observation_only" || stale.CurrentSourceVersion == nil || *stale.CurrentSourceVersion != "opaque-C" {
		t.Fatalf("late lower sequence must be archived: %+v err=%v", stale, err)
	}
	mutatedOldVersion := makeInput("event-v1-mutated", "opaque-A", "signed_at_source", "101.00", 1)
	if _, err := core.RecordExternalContractObservation(ctx, authority, mutatedOldVersion); faultStatus(err) != 409 {
		t.Fatalf("mutated historical source version should conflict, got %v", err)
	}

	unknownInput := makeInput("event-v4-unknown", "opaque-D", "pending_at_source", "", 4)
	unknownInput.Snapshot.Contract.Currency = ""
	unknownInput.RawBody = []byte(`{"event_id":"event-v4-unknown","source_version":"opaque-D","snapshot":{"contract":{"name":"外部来源合同"}}}`)
	unknown, err := core.RecordExternalContractObservation(ctx, authority, unknownInput)
	if err != nil || unknown.Disposition != "unknown" || unknown.CurrentSourceVersion == nil || *unknown.CurrentSourceVersion != "opaque-C" || unknown.ContractRevisionID != nil {
		t.Fatalf("unknown amount must stay an observation and preserve current: %+v err=%v", unknown, err)
	}

	detail, err := core.LoadContractDetail(ctx, admin, first.ContractID)
	if err != nil || detail.Summary.ContractState != "external_observed" || detail.SourceObservedState != "pending_at_source" || detail.SourceObservationDisposition != "unknown" || detail.CurrentRevisionID == nil || *detail.CurrentRevisionID != *third.ContractRevisionID {
		t.Fatalf("external detail must separate observed source state from local workflow: %+v err=%v", detail, err)
	}
	viewer, ok := accounts.ByID("demo-viewer")
	if !ok {
		t.Fatal("demo-viewer missing")
	}
	if _, err := core.LoadContractDetail(ctx, viewer, first.ContractID); faultStatus(err) != 404 {
		t.Fatalf("unlinked viewer must not see external contract metadata: %v", err)
	}

	tx, err := core.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "source-test", TenantID: authority.TenantID, OrgID: orgID, Role: "service"}); err != nil {
		t.Fatal(err)
	}
	var ciphertext, storedSnapshot string
	if err := tx.QueryRow(ctx, `SELECT o.raw_payload_ciphertext,r.snapshot::text
		FROM clm_contract_source_observations o JOIN clm_contract_revisions r
		ON r.tenant_id=o.tenant_id AND r.contract_id=o.contract_id AND r.contract_revision_id=o.contract_revision_id
		WHERE o.observation_id=$1`, first.ObservationID).Scan(&ciphertext, &storedSnapshot); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ciphertext, testRestrictedBankAccount) || strings.Contains(storedSnapshot, testRestrictedBankAccount) {
		t.Fatal("raw payload or restricted supplier field was stored in plaintext")
	}
	identityOnlyContractID := opaque.ContractID
	rejectInvalidIdentityUpdate := func(label, statement string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, `SAVEPOINT source_identity_constraint_case`); err != nil {
			t.Fatal(err)
		}
		_, updateErr := tx.Exec(ctx, statement, args...)
		if updateErr == nil || !strings.Contains(updateErr.Error(), "clm_contract_external_identity_check") {
			t.Fatalf("%s must fail the external identity constraint, got %v", label, updateErr)
		}
		if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT source_identity_constraint_case`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `RELEASE SAVEPOINT source_identity_constraint_case`); err != nil {
			t.Fatal(err)
		}
	}
	rejectInvalidIdentityUpdate("identity-only row changed to draft", `UPDATE clm_contracts SET lifecycle_state='draft'
		WHERE tenant_id=$1 AND contract_id=$2`, accounts.Tenant(admin), identityOnlyContractID)
	rejectInvalidIdentityUpdate("empty source version", `UPDATE clm_contracts SET lifecycle_state='draft',source_version=''
		WHERE tenant_id=$1 AND contract_id=$2`, accounts.Tenant(admin), identityOnlyContractID)
	rejectInvalidIdentityUpdate("whitespace-only source version", `UPDATE clm_contracts SET lifecycle_state='draft',source_version='   '
		WHERE tenant_id=$1 AND contract_id=$2`, accounts.Tenant(admin), identityOnlyContractID)
	revisionID, err := newContractUUID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT source_identity_revision_fixture`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_revisions
		(tenant_id,contract_id,contract_revision_id,revision_number,amount,currency,snapshot,snapshot_digest,frozen_by)
		VALUES($1,$2,$3,1,100,'CNY','{}',$4,'source-constraint-test')`, accounts.Tenant(admin), identityOnlyContractID, revisionID, strings.Repeat("a", 64)); err != nil {
		t.Fatalf("insert scoped revision fixture: %v", err)
	}
	rejectInvalidIdentityUpdate("null version with a current revision", `UPDATE clm_contracts SET current_revision_id=$3
		WHERE tenant_id=$1 AND contract_id=$2`, accounts.Tenant(admin), identityOnlyContractID, revisionID)
	rejectInvalidIdentityUpdate("empty version with a current revision", `UPDATE clm_contracts SET current_revision_id=$3,source_version=''
		WHERE tenant_id=$1 AND contract_id=$2`, accounts.Tenant(admin), identityOnlyContractID, revisionID)
	rejectInvalidIdentityUpdate("whitespace-only version with a current revision", `UPDATE clm_contracts SET current_revision_id=$3,source_version='   '
		WHERE tenant_id=$1 AND contract_id=$2`, accounts.Tenant(admin), identityOnlyContractID, revisionID)
	if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT source_identity_revision_fixture`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `RELEASE SAVEPOINT source_identity_revision_fixture`); err != nil {
		t.Fatal(err)
	}
}
