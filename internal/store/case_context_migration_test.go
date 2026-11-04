// File metadata (manifest mirror):
// Purpose: Verifies the case context fence migration upgrades an isolated schema and retains the intended service-role and public execution boundaries.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-03
// Updated: 2026-11-04
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestCaseContextFenceMigrationUpgradeAndRoleACL(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated PostgreSQL is required")
	}
	ctx := context.Background()
	core := env.openRole(t, "core")
	// Restore the relevant 0039 policy/trigger definitions in this isolated
	// fixture, then exercise both forward migrations and their role grants.
	if _, err := env.harness.pool.Exec(ctx, `
		DROP TRIGGER command_receipt_context ON case_intake_operations;
		CREATE TRIGGER command_receipt_context BEFORE INSERT OR UPDATE ON case_intake_operations FOR EACH ROW EXECUTE FUNCTION stamp_command_receipt();
		ALTER POLICY command_receipt_context ON case_intake_operations
		  USING (NULLIF(current_setting('app.command_digest',true),'') IS NULL OR command_session=current_setting('app.command_digest',true) AND command_version=NULLIF(current_setting('app.command_version',true),'')::bigint)
		  WITH CHECK (NULLIF(current_setting('app.command_digest',true),'') IS NULL OR command_session=current_setting('app.command_digest',true) AND command_version=NULLIF(current_setting('app.command_version',true),'')::bigint);
		ALTER POLICY command_receipt_context ON document_upload_idempotency
		  USING (NULLIF(current_setting('app.command_digest',true),'') IS NULL OR command_session=current_setting('app.command_digest',true) AND command_version=NULLIF(current_setting('app.command_version',true),'')::bigint);
		DROP POLICY intake_recovery_owner ON case_intake_operations;
		DROP TABLE case_intake_completion_events;
		DROP FUNCTION stamp_intake_completion();
		DROP FUNCTION tenant_intake_upload_recovery_allowed(text,text,text,text,text);
		DROP FUNCTION tenant_intake_read_allowed(text,text,text,text,text,text);
		DROP FUNCTION tenant_intake_recovery_allowed(text,text,text,text,text,text);
		DROP FUNCTION tenant_case_context_fence(text,bigint,text);
		DELETE FROM schema_migrations WHERE version IN (40,41)`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, env.harness.pool, env.harness.schema); err != nil {
		t.Fatalf("upgrade 0039 through 0041: %v", err)
	}
	if err := migrate(ctx, env.harness.pool, env.harness.schema); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	if err := MigrateServiceRoles(ctx); err != nil {
		t.Fatalf("upgrade service role grants: %v", err)
	}
	if err := core.ValidateServiceDatabaseRole(ctx, "core"); err != nil {
		t.Fatalf("upgraded core role ACL: %v", err)
	}
	var publicExecute bool
	if err := env.harness.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_proc p CROSS JOIN LATERAL aclexplode(COALESCE(p.proacl,acldefault('f',p.proowner))) a WHERE p.oid=$1::regprocedure AND a.grantee=0 AND a.privilege_type='EXECUTE')`, pgx.Identifier{env.harness.schema}.Sanitize()+`.tenant_case_context_fence(text,bigint,text)`).Scan(&publicExecute); err != nil || publicExecute {
		t.Fatalf("PUBLIC execute after upgrade: %t, %v", publicExecute, err)
	}
}
