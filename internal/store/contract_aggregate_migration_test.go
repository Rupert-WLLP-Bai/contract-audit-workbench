package store

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Exercise the numbered migration with real service roles and existing case
// materials. A uniqueness constraint alone cannot prove the later workflow.
func TestContractAggregateMigrationBoundaries(t *testing.T) {
	fixture, ok := newDurableCaseFixture(t)
	if !ok {
		t.Fatal("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required")
	}
	ctx := context.Background()
	env, admin := fixture.environment, fixture.admin
	core, document, caseID := fixture.core, fixture.document, fixture.caseID
	execution := env.openRole(t, "execution")
	upload, err := document.UploadMaterial(ctx, admin, caseID, "attachment", "contract.txt", "text/plain", []byte("原始合同材料"), MaterialUploadOptions{IdempotencyKey: "clm-migration-material"})
	if err != nil {
		t.Fatal(err)
	}
	const contractID = "c6052c81-8b05-49ce-89e9-f6e9c6403c01"
	const otherContractID = "c6052c81-8b05-49ce-89e9-f6e9c6403c02"
	const revisionID = "c6052c81-8b05-49ce-89e9-f6e9c6403c03"
	const roundID = "c6052c81-8b05-49ce-89e9-f6e9c6403c04"
	const eventID = "c6052c81-8b05-49ce-89e9-f6e9c6403c05"

	begin := func(st *Store, scope dbScope) pgx.Tx {
		t.Helper()
		tx, err := st.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := setLocalScope(ctx, tx, scope); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		return tx
	}
	scope := userScope(admin)
	tx := begin(core, scope)
	defer tx.Rollback(ctx)
	for _, id := range []string{contractID, otherContractID} {
		if _, err := tx.Exec(ctx, `INSERT INTO clm_contracts
			(tenant_id,contract_id,owner_org_id,authority_mode,lifecycle_state,created_by)
			VALUES ($1,$2,$3,'native','draft',$4)`, admin.TenantID, id, admin.OrgID, admin.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_cases
		(tenant_id,contract_id,case_org_id,case_id,linked_by) VALUES ($1,$2,$3,$4,$5)`,
		admin.TenantID, contractID, admin.OrgID, caseID, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_revisions
		(tenant_id,contract_id,contract_revision_id,revision_number,amount,currency,snapshot,snapshot_digest,frozen_by)
		VALUES ($1,$2,$3,1,100,'CNY','{}',$4,$5)`, admin.TenantID, contractID, revisionID, strings.Repeat("a", 64), admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE clm_contracts SET current_revision_id=$3 WHERE tenant_id=$1 AND contract_id=$2`, admin.TenantID, contractID, revisionID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_approval_rounds
		(tenant_id,contract_id,approval_round_id,contract_revision_id,round_number,state,workflow_template_id,workflow_template_version,routing_snapshot,risk_disposition_snapshot,initiated_by)
		VALUES ($1,$2,$3,$4,1,'in_review','workflow','1','{}','{}',$5)`, admin.TenantID, contractID, roundID, revisionID, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_business_events
		(tenant_id,event_id,contract_id,aggregate_version,event_type,schema_version,payload)
		VALUES ($1,$2,$3,1,'contract.created',1,'{}')`, admin.TenantID, eventID, contractID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_outbox_delivery(tenant_id,event_id) VALUES ($1,$2)`, admin.TenantID, eventID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if materialID, version, err := document.MaterialIdentity(ctx, admin, caseID, upload.DocumentID); err != nil || materialID != upload.MaterialID || version != 1 {
		t.Fatalf("original material chain changed: material=%q version=%d err=%v", materialID, version, err)
	}
	for _, tc := range []struct {
		name, query, code, message string
		args                       []any
	}{
		{"second active round", `INSERT INTO clm_approval_rounds (tenant_id,contract_id,approval_round_id,contract_revision_id,round_number,state,workflow_template_id,workflow_template_version,routing_snapshot,risk_disposition_snapshot,initiated_by) VALUES ($1,$2,'c6052c81-8b05-49ce-89e9-f6e9c6403c06',$3,2,'in_review','workflow','1','{}','{}',$4)`, "23505", "", []any{admin.TenantID, contractID, revisionID, admin.ID}},
		{"revision from another contract", `UPDATE clm_contracts SET current_revision_id=$3 WHERE tenant_id=$1 AND contract_id=$2`, "23503", "", []any{admin.TenantID, otherContractID, revisionID}},
		{"case in another organization", `INSERT INTO clm_contract_cases (tenant_id,contract_id,case_org_id,case_id,linked_by) VALUES ($1,$2,'wrong-org',$3,$4)`, "23503", "", []any{admin.TenantID, contractID, caseID, admin.ID}},
		{"external without source", `INSERT INTO clm_contracts (tenant_id,contract_id,owner_org_id,authority_mode,lifecycle_state,created_by) VALUES ($1,'c6052c81-8b05-49ce-89e9-f6e9c6403c07',$2,'external','draft',$3)`, "23514", "", []any{admin.TenantID, admin.OrgID, admin.ID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := begin(core, scope)
			defer tx.Rollback(ctx)
			_, err := tx.Exec(ctx, tc.query, tc.args...)
			if !isPostgresError(err, tc.code, tc.message) {
				t.Fatalf("expected PostgreSQL %s, got %v", tc.code, err)
			}
		})
	}

	t.Run("other tenant cannot read or write contract", func(t *testing.T) {
		foreign := scope
		foreign.TenantID = "other-tenant"
		tx := begin(core, foreign)
		defer tx.Rollback(ctx)
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM clm_contracts WHERE contract_id=$1`, contractID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("foreign tenant saw contract: count=%d err=%v", count, err)
		}
		_, err := tx.Exec(ctx, `INSERT INTO clm_contracts (tenant_id,contract_id,owner_org_id,authority_mode,lifecycle_state,created_by) VALUES ($1,'c6052c81-8b05-49ce-89e9-f6e9c6403c08',$2,'native','draft',$3)`, admin.TenantID, admin.OrgID, admin.ID)
		if !isPostgresError(err, "42501", "row-level security") {
			t.Fatalf("foreign tenant insert was not rejected: %v", err)
		}
	})
	t.Run("core cannot rewrite frozen revision", func(t *testing.T) {
		tx := begin(core, scope)
		defer tx.Rollback(ctx)
		_, err := tx.Exec(ctx, `UPDATE clm_contract_revisions SET amount=101 WHERE tenant_id=$1 AND contract_id=$2`, admin.TenantID, contractID)
		if !isPostgresError(err, "42501", "permission denied") {
			t.Fatalf("core could rewrite a frozen revision: %v", err)
		}
	})
	t.Run("execution cannot publish core event", func(t *testing.T) {
		tx := begin(execution, scope)
		defer tx.Rollback(ctx)
		_, err := tx.Exec(ctx, `INSERT INTO clm_business_events (tenant_id,event_id,contract_id,aggregate_version,event_type,schema_version,payload) VALUES ($1,'c6052c81-8b05-49ce-89e9-f6e9c6403c09',$2,1,'contract.created',1,'{}')`, admin.TenantID, contractID)
		if !isPostgresError(err, "42501", "permission denied") {
			t.Fatalf("execution wrote a core event: %v", err)
		}
	})
}
