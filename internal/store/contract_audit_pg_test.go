package store

import (
	"context"
	"testing"
)

// Durable checks for CLM-12 audit binding. They require TEST_DATABASE_URL and
// MIGRATOR_DATABASE_URL; without a database they are skipped and reported as
// NOT_RUN, never as passed.

func seedAuditAttempt(t *testing.T, fixture durableCaseFixture, execution *Store, runID, attemptID, inputHash string) {
	t.Helper()
	ctx := context.Background()
	if _, err := execution.pool.Exec(ctx, `INSERT INTO review_runs
		(id,tenant_id,org_id,case_id,state,created_at,updated_at)
		VALUES ($1,$2,$3,$4,'completed','2026-11-01T00:00:00Z','2026-11-01T00:00:00Z')`,
		runID, fixture.admin.TenantID, fixture.admin.OrgID, fixture.caseID); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.pool.Exec(ctx, `INSERT INTO review_attempts
		(id,tenant_id,org_id,run_id,ordinal,state,accepted_at,started_at,finished_at,history_kind,actor_id)
		VALUES ($1,$2,$3,$4,1,'completed','2026-11-01T00:00:00Z','2026-11-01T00:00:00Z','2026-11-01T00:00:01Z','legacy_unfrozen',$5)`,
		attemptID, fixture.admin.TenantID, fixture.admin.OrgID, runID, fixture.admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.pool.Exec(ctx, `INSERT INTO review_attempt_inputs
		(attempt_id,run_id,input_hash,frozen_at,documents)
		VALUES ($1,$2,$3,'2026-11-01T00:00:00Z','[]')`, attemptID, runID, inputHash); err != nil {
		t.Fatal(err)
	}
}

func TestContractAuditBindingIsBlockedWithoutCitationsAndGoesStaleOnNewRevision(t *testing.T) {
	fixture, _ := newContractDraftTestEnv(t)
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
		IdempotencyKey: "audit-binding-create",
	})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		CaseID: fixture.caseID, ContractID: created.Summary.ContractID,
		ExpectedRowVersion: created.Summary.RowVersion, IdempotencyKey: "audit-binding-freeze",
	})
	if err != nil {
		t.Fatal(err)
	}
	if frozen.Version.AuditState != auditStateUnbound || frozen.Version.AuditGateState != auditGateNotApplicable {
		t.Fatalf("new revision audit state %+v", frozen.Version)
	}
	seedAuditAttempt(t, fixture, fixture.environment.openRole(t, "execution"),
		"audit-run-1", "audit-attempt-1", "attempt-input-hash-1")
	binding, err := fixture.core.BindContractAudit(ctx, fixture.admin, BindContractAuditInput{
		ContractID: created.Summary.ContractID, ContractRevisionID: frozen.Version.ContractRevisionID,
		ReviewRunID: "audit-run-1", ReviewAttemptID: "audit-attempt-1",
		ExpectedRowVersion: frozen.RowVersion, IdempotencyKey: "audit-binding-bind",
	})
	if err != nil {
		t.Fatal(err)
	}
	if binding.GateState != auditGateBlocked || binding.ModelFailure {
		t.Fatalf("binding gate %+v", binding)
	}
	blocked := false
	for _, reason := range binding.GateReasons {
		if reason.Code == "attempt_input_missing" {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("binding reasons %+v", binding.GateReasons)
	}
	detail, err := fixture.core.LoadContractVersion(ctx, fixture.admin,
		created.Summary.ContractID, frozen.Version.ContractRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Version.AuditState != auditStateCurrent || detail.Version.AuditGateState != auditGateBlocked {
		t.Fatalf("current revision audit state %+v", detail.Version)
	}

	second, err := fixture.core.FreezeContractVersion(ctx, fixture.admin, FreezeContractVersionInput{
		CaseID: fixture.caseID, ContractID: created.Summary.ContractID,
		ExpectedRowVersion: frozen.RowVersion + 1, IdempotencyKey: "audit-binding-freeze-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := fixture.core.ListContractVersions(ctx, fixture.admin, created.Summary.ContractID)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range list.Items {
		switch version.ContractRevisionID {
		case second.Version.ContractRevisionID:
			if version.AuditState != auditStateUnbound || version.AuditGateState != auditGateNotApplicable {
				t.Fatalf("new revision inherited audit: %+v", version)
			}
		case frozen.Version.ContractRevisionID:
			if version.AuditState != auditStateStale || version.AuditGateState != auditGateBlocked {
				t.Fatalf("old revision audit state: %+v", version)
			}
		}
	}
}
