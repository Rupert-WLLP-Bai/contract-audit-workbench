package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"contractaudit/internal/accounts"
)

func TestLegalCaptureTenantAndServiceRoleBoundaries(t *testing.T) {
	assetDir := os.Getenv("LEGALNORM_ASSET_DIR")
	if assetDir == "" {
		t.Fatal("LEGALNORM_ASSET_DIR must point to the external official legal-sources directory")
	}
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("MIGRATOR_DATABASE_URL") == "" {
		t.Fatal("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for legal capture boundary checks")
	}
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Fatal("isolated durable service-role environment could not be created")
	}
	core := env.openRole(t, "core")
	execution := env.openRole(t, "execution")
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo administrator missing")
	}
	input := legalSourceImportFixture(t, assetDir, "S1_civil_code_contract")
	input.IdempotencyKey = "legal-role-boundary"
	imported, err := core.ImportLegalSource(context.Background(), admin, input)
	if err != nil {
		t.Fatalf("core import verified legal capture: %v", err)
	}
	ctx := context.Background()

	// A different tenant sees neither the raw capture nor its legal versions.
	foreignTenant := "tenant-legal-boundary-foreign"
	foreignTx, err := core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foreignScope := userScope(admin)
	foreignScope.TenantID = foreignTenant
	if err := setLocalScope(ctx, foreignTx, foreignScope); err != nil {
		_ = foreignTx.Rollback(ctx)
		t.Fatal(err)
	}
	var captures, versions int
	if err := foreignTx.QueryRow(ctx, `SELECT count(*) FROM legal_source_captures`).Scan(&captures); err != nil {
		_ = foreignTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := foreignTx.QueryRow(ctx, `SELECT count(*) FROM governed_knowledge_versions WHERE kind='legal'`).Scan(&versions); err != nil {
		_ = foreignTx.Rollback(ctx)
		t.Fatal(err)
	}
	if captures != 0 || versions != 0 {
		_ = foreignTx.Rollback(ctx)
		t.Fatalf("foreign tenant RLS exposed captures/versions %d/%d", captures, versions)
	}
	if err := foreignTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	// Even an insert under another tenant cannot reference the owner's capture:
	// the composite (tenant_id,capture_snapshot_id) FK is enforced by PostgreSQL.
	ownerVersion, err := core.GetGovernedKnowledgeVersion(admin, imported.Articles[0].KnowledgeID, imported.Articles[0].VersionID)
	if err != nil {
		t.Fatalf("read imported version for FK boundary: %v", err)
	}
	contentBytes, err := json.Marshal(ownerVersion.Content)
	if err != nil {
		t.Fatal(err)
	}
	scopeKey, err := knowledgeScopeKey(ownerVersion.Content.Scope)
	if err != nil {
		t.Fatal(err)
	}
	foreignVersion := knowledgeVersionID(foreignTenant, ownerVersion.Content.KnowledgeID, ownerVersion.Content.Version)
	foreignInsert, err := core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctx, foreignInsert, foreignScope); err != nil {
		_ = foreignInsert.Rollback(ctx)
		t.Fatal(err)
	}
	_, err = foreignInsert.Exec(ctx, `INSERT INTO governed_knowledge_versions
		(tenant_id,knowledge_id,version_id,display_version,scope_key,kind,content_sha256,content,created_by,created_at,legal_capture_snapshot_id)
		VALUES ($1,$2,$3,$4,$5,'legal',$6,$7,$8,clock_timestamp(),$9)`, foreignTenant,
		ownerVersion.Content.KnowledgeID, foreignVersion, ownerVersion.Content.Version, scopeKey,
		sha256Bytes(contentBytes), contentBytes, admin.ID, imported.CaptureSnapshotID)
	if !isPostgresError(err, "23503", "governed_knowledge_versions_legal_capture_fk") {
		_ = foreignInsert.Rollback(ctx)
		t.Fatalf("cross-tenant capture reference did not fail composite FK: %v", err)
	}
	_ = foreignInsert.Rollback(ctx)

	// The durable core owner and execution role cannot rewrite or delete a
	// capture. The trigger is also exercised as schema owner below.
	for _, roleStore := range []*Store{core, execution} {
		var canUpdate, canDelete bool
		if err := roleStore.pool.QueryRow(ctx, `SELECT has_table_privilege(current_user,'legal_source_captures','UPDATE'), has_table_privilege(current_user,'legal_source_captures','DELETE')`).Scan(&canUpdate, &canDelete); err != nil {
			t.Fatal(err)
		}
		if canUpdate || canDelete {
			t.Fatalf("%s role can mutate append-only captures: UPDATE=%v DELETE=%v", roleStore.serviceOwner, canUpdate, canDelete)
		}
		for _, statement := range []string{
			`UPDATE legal_source_captures SET norm_title=norm_title WHERE tenant_id=$1 AND capture_snapshot_id=$2`,
			`DELETE FROM legal_source_captures WHERE tenant_id=$1 AND capture_snapshot_id=$2`,
		} {
			tx, err := roleStore.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := setLocalScope(ctx, tx, userScope(admin)); err != nil {
				_ = tx.Rollback(ctx)
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, statement, accounts.Tenant(admin), imported.CaptureSnapshotID)
			if !isPostgresError(err, "42501", "permission denied") {
				_ = tx.Rollback(ctx)
				t.Fatalf("%s role mutation did not fail at database privileges: %v", roleStore.serviceOwner, err)
			}
			_ = tx.Rollback(ctx)
		}
	}
	var canInsert, canUpdate, canDelete bool
	if err := execution.pool.QueryRow(ctx, `SELECT has_table_privilege(current_user,'legal_source_captures','INSERT'), has_table_privilege(current_user,'legal_source_captures','UPDATE'), has_table_privilege(current_user,'legal_source_captures','DELETE')`).Scan(&canInsert, &canUpdate, &canDelete); err != nil {
		t.Fatal(err)
	}
	if canInsert || canUpdate || canDelete {
		t.Fatalf("execution role can write legal captures: insert=%v update=%v delete=%v", canInsert, canUpdate, canDelete)
	}
	executionInsert, err := execution.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctx, executionInsert, userScope(admin)); err != nil {
		_ = executionInsert.Rollback(ctx)
		t.Fatal(err)
	}
	_, err = executionInsert.Exec(ctx, `INSERT INTO legal_source_captures SELECT * FROM legal_source_captures WHERE tenant_id=$1 AND capture_snapshot_id=$2`, accounts.Tenant(admin), imported.CaptureSnapshotID)
	if !isPostgresError(err, "42501", "permission denied") {
		_ = executionInsert.Rollback(ctx)
		t.Fatalf("execution INSERT did not fail at database privileges: %v", err)
	}
	_ = executionInsert.Rollback(ctx)

	readTx, err := execution.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctx, readTx, userScope(admin)); err != nil {
		_ = readTx.Rollback(ctx)
		t.Fatal(err)
	}
	var visible int
	if err := readTx.QueryRow(ctx, `SELECT count(*) FROM legal_source_captures WHERE capture_snapshot_id=$1`, imported.CaptureSnapshotID).Scan(&visible); err != nil {
		_ = readTx.Rollback(ctx)
		t.Fatalf("execution read-only legal capture lookup failed: %v", err)
	}
	if visible != 1 {
		_ = readTx.Rollback(ctx)
		t.Fatalf("execution role saw %d exact capture rows, want 1", visible)
	}
	_ = readTx.Rollback(ctx)

	for _, statement := range []string{
		`UPDATE legal_source_captures SET norm_title=norm_title WHERE tenant_id=$1 AND capture_snapshot_id=$2`,
		`DELETE FROM legal_source_captures WHERE tenant_id=$1 AND capture_snapshot_id=$2`,
	} {
		tx, err := env.harness.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := setLocalScope(ctx, tx, userScope(admin)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, statement, accounts.Tenant(admin), imported.CaptureSnapshotID)
		if !isPostgresError(err, "P0001", "append-only") {
			_ = tx.Rollback(ctx)
			t.Fatalf("schema owner mutation did not hit append-only trigger: %v", err)
		}
		_ = tx.Rollback(ctx)
	}
}
