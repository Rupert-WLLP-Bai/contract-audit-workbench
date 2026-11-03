// File metadata (manifest mirror):
// Purpose: Verifies the case context fence migration upgrades an isolated schema and retains the intended service-role and public execution boundaries.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-03
// Updated: 2026-11-03
// Revision: r1
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
	// 0040 adds only this function, so removing it and its ledger entry restores
	// the 0039 schema while retaining the pre-existing service roles and ACLs.
	if _, err := env.harness.pool.Exec(ctx, `DROP FUNCTION tenant_case_context_fence(text,bigint,text); DELETE FROM schema_migrations WHERE version=40`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, env.harness.pool, env.harness.schema); err != nil {
		t.Fatalf("upgrade 0039 to 0040: %v", err)
	}
	if err := migrate(ctx, env.harness.pool, env.harness.schema); err != nil {
		t.Fatalf("repeat 0040 migration: %v", err)
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
