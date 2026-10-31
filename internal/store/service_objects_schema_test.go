package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
)

func TestServiceObjectSchemaHeadAndAppendOnlyBoundariesInPostgres(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("MIGRATOR_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for the isolated schema behavior test")
	}
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated PostgreSQL test environment is unavailable")
	}
	ctx := context.Background()
	if _, err := env.harness.pool.Exec(ctx, serviceObjectSchemaSQL); err != nil {
		t.Fatalf("apply service object schema: %v", err)
	}
	if _, err := env.harness.pool.Exec(ctx, serviceObjectSchemaSQL); err != nil {
		t.Fatalf("reapply service object schema idempotently: %v", err)
	}
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo administrator missing")
	}
	caseID, err := env.harness.CreateCase(admin, "服务对象独立schema", "procurement_ict")
	if err != nil {
		t.Fatalf("create scoped case: %v", err)
	}
	scope := userScope(admin)
	core := env.openRole(t, "core")
	const objectID = "service-object-schema-test"
	insertInitial := func(version int, association any) error {
		tx, err := env.harness.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if err := setLocalScope(ctx, tx, scope); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO service_objects
			(tenant_id,org_id,case_id,service_object_id,current_version,current_association_version,created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, accounts.Tenant(admin), admin.OrgID, caseID, objectID, version, association, admin.ID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO service_object_versions
			(tenant_id,org_id,case_id,service_object_id,version,object_type,display_name,aliases,state,effective_from,basis_refs,payload_sha256,created_by,created_at)
			VALUES ($1,$2,$3,$4,1,'dedicated_line','线路schema','[]'::jsonb,'pending_confirmation',clock_timestamp(),'[]'::jsonb,$5,$6,clock_timestamp())`,
			accounts.Tenant(admin), admin.OrgID, caseID, objectID, strings.Repeat("a", 64), admin.ID)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err := insertInitial(1, nil); err != nil {
		t.Fatalf("insert initial version/head: %v", err)
	}

	for name, test := range map[string]struct{ statement, message string }{
		"skipped initial version":              {`INSERT INTO service_objects(tenant_id,org_id,case_id,service_object_id,current_version,created_by) VALUES($1,$2,$3,'bad-initial-version',2,$4)`, "service object head"},
		"missing first association":            {`UPDATE service_objects SET current_association_version=1 WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4`, "service object association"},
		"identity advance without association": {`UPDATE service_objects SET current_version=2 WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4`, "first association version"},
		"immutable head identity":              {`UPDATE service_objects SET service_object_id='changed-service-object' WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4`, "service object identity is immutable"},
		"head cannot be deleted":               {`DELETE FROM service_objects WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4`, "service object head cannot be deleted"},
	} {
		tx, err := env.harness.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := setLocalScope(ctx, tx, scope); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		id := objectID
		if name == "skipped initial version" {
			id = "bad-initial-version"
		}
		_, err = tx.Exec(ctx, test.statement, accounts.Tenant(admin), admin.OrgID, caseID, id)
		if !isPostgresError(err, "P0001", test.message) {
			_ = tx.Rollback(ctx)
			t.Errorf("%s did not fail at the service object head guard: %v", name, err)
			continue
		}
		_ = tx.Rollback(ctx)
	}

	tx, err := env.harness.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctx, tx, scope); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `UPDATE service_object_versions SET display_name=display_name WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND version=1`, accounts.Tenant(admin), admin.OrgID, caseID, objectID)
	if !isPostgresError(err, "P0001", "append-only") {
		_ = tx.Rollback(ctx)
		t.Fatalf("identity version rewrite did not fail at append-only trigger: %v", err)
	}
	_ = tx.Rollback(ctx)
	receiptTx, err := env.harness.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctx, receiptTx, scope); err != nil {
		_ = receiptTx.Rollback(ctx)
		t.Fatal(err)
	}
	_, err = receiptTx.Exec(ctx, `INSERT INTO service_object_mutation_receipts
		(tenant_id,org_id,case_id,actor_id,operation,idempotency_key,request_sha256,response_status,response_body,entity_id,created_at)
		VALUES($1,$2,$3,$4,'create_object','schema-receipt',$5,201,'{}'::bytea,'schema-entity',clock_timestamp())`,
		accounts.Tenant(admin), admin.OrgID, caseID, admin.ID, strings.Repeat("b", 64))
	if err != nil {
		_ = receiptTx.Rollback(ctx)
		t.Fatalf("insert exact response receipt: %v", err)
	}
	_, err = receiptTx.Exec(ctx, `UPDATE service_object_mutation_receipts SET response_status=response_status
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND operation='create_object' AND idempotency_key='schema-receipt'`,
		accounts.Tenant(admin), admin.OrgID, caseID, admin.ID)
	if !isPostgresError(err, "P0001", "append-only") {
		_ = receiptTx.Rollback(ctx)
		t.Fatalf("response receipt rewrite did not fail at append-only trigger: %v", err)
	}
	_ = receiptTx.Rollback(ctx)

	execution := env.openRole(t, "execution")
	for table, want := range map[string][4]bool{
		"service_objects":                      {true, true, true, false},
		"service_object_versions":              {true, true, false, false},
		"service_object_contract_associations": {true, true, false, false},
		"service_object_mutation_receipts":     {true, true, false, false},
	} {
		var actual [4]bool
		if err := core.pool.QueryRow(ctx, `SELECT has_table_privilege(current_user,$1,'SELECT'),has_table_privilege(current_user,$1,'INSERT'),has_table_privilege(current_user,$1,'UPDATE'),has_table_privilege(current_user,$1,'DELETE')`, table).Scan(&actual[0], &actual[1], &actual[2], &actual[3]); err != nil {
			t.Fatal(err)
		}
		if actual != want {
			t.Fatalf("core role privileges for %s = %v, want %v", table, actual, want)
		}
	}
	coreWriteTx, err := core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctx, coreWriteTx, scope); err != nil {
		_ = coreWriteTx.Rollback(ctx)
		t.Fatal(err)
	}
	_, err = coreWriteTx.Exec(ctx, `UPDATE service_object_versions SET display_name=display_name
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND version=1`,
		accounts.Tenant(admin), admin.OrgID, caseID, objectID)
	if !isPostgresError(err, "42501", "permission denied") {
		_ = coreWriteTx.Rollback(ctx)
		t.Fatalf("core role could rewrite immutable versions before reaching the owner trigger: %v", err)
	}
	_ = coreWriteTx.Rollback(ctx)
	for _, table := range []string{"service_objects", "service_object_versions", "service_object_contract_associations"} {
		var canRead, canInsert, canUpdate, canDelete bool
		if err := execution.pool.QueryRow(ctx, `SELECT has_table_privilege(current_user,$1,'SELECT'),has_table_privilege(current_user,$1,'INSERT'),has_table_privilege(current_user,$1,'UPDATE'),has_table_privilege(current_user,$1,'DELETE')`, table).
			Scan(&canRead, &canInsert, &canUpdate, &canDelete); err != nil {
			t.Fatal(err)
		}
		if !canRead || canInsert || canUpdate || canDelete {
			t.Fatalf("execution role privileges for %s = read:%v insert:%v update:%v delete:%v", table, canRead, canInsert, canUpdate, canDelete)
		}
	}
	var receiptRead bool
	if err := execution.pool.QueryRow(ctx, `SELECT has_table_privilege(current_user,'service_object_mutation_receipts','SELECT')`).Scan(&receiptRead); err != nil {
		t.Fatal(err)
	}
	if receiptRead {
		t.Fatal("execution role can read core-only idempotency receipts")
	}
	readTx, err := execution.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctx, readTx, scope); err != nil {
		_ = readTx.Rollback(ctx)
		t.Fatal(err)
	}
	var ownRows int
	if err := readTx.QueryRow(ctx, `SELECT count(*) FROM service_objects WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3`, accounts.Tenant(admin), admin.OrgID, caseID).Scan(&ownRows); err != nil || ownRows != 1 {
		_ = readTx.Rollback(ctx)
		t.Fatalf("execution role could not read authorized tenant row: rows=%d err=%v", ownRows, err)
	}
	foreignScope := scope
	foreignScope.TenantID = "tenant-service-object-schema-foreign"
	if err := setLocalScope(ctx, readTx, foreignScope); err != nil {
		_ = readTx.Rollback(ctx)
		t.Fatal(err)
	}
	var foreignRows int
	if err := readTx.QueryRow(ctx, `SELECT count(*) FROM service_objects`).Scan(&foreignRows); err != nil || foreignRows != 0 {
		_ = readTx.Rollback(ctx)
		t.Fatalf("service object RLS exposed foreign rows: rows=%d err=%v", foreignRows, err)
	}
	_ = readTx.Rollback(ctx)

	foreignWrite, err := core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctx, foreignWrite, foreignScope); err != nil {
		_ = foreignWrite.Rollback(ctx)
		t.Fatal(err)
	}
	_, err = foreignWrite.Exec(ctx, `INSERT INTO service_objects(tenant_id,org_id,case_id,service_object_id,current_version,created_by)
		VALUES($1,$2,$3,'foreign-tenant-write',1,$4)`, accounts.Tenant(admin), admin.OrgID, caseID, admin.ID)
	if !isPostgresError(err, "42501", "row-level security") {
		_ = foreignWrite.Rollback(ctx)
		t.Fatalf("cross-tenant write did not fail at RLS: %v", err)
	}
	_ = foreignWrite.Rollback(ctx)
}
