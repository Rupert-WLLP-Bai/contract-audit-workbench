// File metadata (manifest mirror):
// Purpose: 实现 `TestOperationsProjectionScopeAndRecoveryPG` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The projection and lifecycle must share the actual jurisdiction and preserve
// operator state while reconciling an unknown external identity action.
func TestOperationsProjectionScopeAndRecoveryPG(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	st, err := OpenTest(t.TempDir(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('app.tenant_id','local-demo',true),set_config('app.org_id','demo-org',true)`); err != nil {
		t.Fatal(err)
	}
	// No identity_tenants row is required for a legitimate local-demo case.
	if _, err = tx.Exec(ctx, `INSERT INTO cases(id,title,contract_type,owner_id,member_ids,created_at,updated_at,tenant_id,org_id) VALUES('ops-demo-case','demo','service','demo-admin','[]',$1,$1,'local-demo','demo-org')`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("unregistered demo case: %v", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO identity_jurisdictions(jurisdiction_id,display_name,status) VALUES('ops-j1','J1','active'),('ops-j2','J2','active')`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO identity_tenants(tenant_id,display_name,jurisdiction_id,status) VALUES('ops-actor-tenant','Actor','ops-j1','active'),('ops-target','Target','ops-j1','suspended')`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO identity_org_units(tenant_id,org_unit_id,display_name,status) VALUES('ops-actor-tenant','ops-org','Org','active')`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO identity_principals(principal_id,actor_id,issuer,subject,status) VALUES('11111111-1111-4111-8111-111111111111','ops-actor','https://example.invalid','subject','active')`); err != nil {
		t.Fatal(err)
	}
	grants := `[{"role":"jurisdiction_operator","scope":"jurisdiction","jurisdiction_id":"ops-j1","tenant_ids":["ops-target"],"metrics":["cases"]},{"role":"jurisdiction_operator","scope":"jurisdiction","jurisdiction_id":"ops-j2","tenant_ids":["ops-target"],"metrics":["money"]}]`
	if _, err = tx.Exec(ctx, `INSERT INTO identity_memberships(membership_id,principal_id,tenant_id,org_unit_id,role_code,grants,status) VALUES('22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111','ops-actor-tenant','ops-org','admin',$1::jsonb,'active')`, grants); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO tenant_provision_operations(operation_id,tenant_id,kind,status,current_step,payload,payload_sha256,idempotency_key,created_by) VALUES('44444444-4444-4444-8444-444444444444','ops-target','deactivate','pending','usage_reconcile','{}',repeat('a',64),'deactivation','ops-actor')`); err != nil {
		t.Fatal(err)
	}
	// Re-importing a stale active crosswalk may update directory metadata but may
	// not reactivate the target or change its original operational epoch.
	region := "ops-j1"
	if err = importIdentityDirectoryRows(ctx, tx, pgx.Identifier{st.schema}.Sanitize(), IdentityCrosswalk{Version: 1, Tenants: []IdentityTenantMap{{ID: "ops-target", DisplayName: "Target", JurisdictionID: &region, Status: "active"}}}); err != nil {
		t.Fatal(err)
	}
	var status string
	var epoch int64
	if err = tx.QueryRow(ctx, `SELECT status,live_epoch FROM identity_tenants WHERE tenant_id='ops-target'`).Scan(&status, &epoch); err != nil || status != "suspended" || epoch != 1 {
		t.Fatalf("crosswalk changed operational state: %s %d %v", status, epoch, err)
	}
	if _, err = tx.Exec(ctx, `UPDATE tenant_provision_operations SET status='completed',current_step='completed' WHERE operation_id='44444444-4444-4444-8444-444444444444'`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('app.actor_id','ops-actor',true),set_config('app.tenant_id','ops-actor-tenant',true),set_config('app.platform_operator_actor','ops-actor',true)`); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT tenant_jurisdiction_operations('ops-actor','11111111-1111-4111-8111-111111111111'::uuid,'22222222-2222-4222-8222-222222222222'::uuid,current_date,current_date)`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var view struct {
		Jurisdictions []struct {
			Tenants []struct {
				Metrics struct {
					Cases *string         `json:"cases_created"`
					Money json.RawMessage `json:"money"`
				} `json:"metrics"`
			} `json:"tenants"`
		} `json:"jurisdictions"`
	}
	if err = json.Unmarshal(raw, &view); err != nil || len(view.Jurisdictions) != 1 || len(view.Jurisdictions[0].Tenants) != 1 || view.Jurisdictions[0].Tenants[0].Metrics.Cases == nil || string(view.Jurisdictions[0].Tenants[0].Metrics.Money) != "null" {
		t.Fatalf("cross-jurisdiction grant leaked metric: %s %v", raw, err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO tenant_provision_operations(operation_id,tenant_id,kind,status,current_step,payload,payload_sha256,idempotency_key,created_by) VALUES('33333333-3333-4333-8333-333333333333','ops-target','provision','reconciliation_required','identity','{}',repeat('a',64),'ops-key','ops-actor')`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO tenant_provision_operation_steps(operation_id,step_name,state) VALUES('33333333-3333-4333-8333-333333333333','identity','external_unknown'),('33333333-3333-4333-8333-333333333333','tenant_config','pending')`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT before_blind_complete`); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT tenant_provision_operation_advance('ops-actor','33333333-3333-4333-8333-333333333333',1,'complete',repeat('b',64),'','ops-actor')`).Scan(&raw); err == nil {
		t.Fatal("unknown identity accepted an unconfirmed completion")
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT before_blind_complete`); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT tenant_provision_operation_advance('ops-actor','33333333-3333-4333-8333-333333333333',1,'reconciled',repeat('b',64),'confirmed_complete','ops-actor')`).Scan(&raw); err != nil {
		t.Fatalf("confirm already completed identity: %v", err)
	}
	var step, reconciliation string
	if err = tx.QueryRow(ctx, `SELECT state,reconciliation FROM tenant_provision_operation_steps WHERE operation_id='33333333-3333-4333-8333-333333333333' AND step_name='identity'`).Scan(&step, &reconciliation); err != nil || step != "complete" || reconciliation != "confirmed_complete" {
		t.Fatalf("identity reconciliation lost: %s %s %v", step, reconciliation, err)
	}
}
