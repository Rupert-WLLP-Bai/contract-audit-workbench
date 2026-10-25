package store

import (
	"context"
	"errors"
	"testing"

	"contractaudit/internal/accounts"
)

// TestGovernanceServiceRoleBoundaries proves the real service-role contract with
// dedicated database roles: Core owns and writes the governance tables, Execution
// may only SELECT them (acceptance selection), and forced RLS hides another
// tenant's versions and pointers from every role.
func TestGovernanceServiceRoleBoundaries(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	core := env.openRole(t, "core")
	execution := env.openRole(t, "execution")
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo admin missing")
	}

	manifest := governedPackManifest(t, accounts.Tenant(admin), "1", "100")
	published := publishGovernedPackStore(t, core, admin, "1", "100", manifest)
	if published.Pack.Status != GovernancePublished || !published.Pack.IsActive {
		t.Fatalf("core did not publish the governed pack: %+v", published.Pack)
	}

	// Execution reads the active pointer for acceptance selection.
	selected, err := execution.SelectActiveRulePack(context.Background(), accounts.Tenant(admin),
		"ENT", "telecom_dedicated_line", "service", "2026-06-01T00:00:00Z")
	if err != nil {
		t.Fatalf("execution could not select the governed pack: %v", err)
	}
	if selected == nil || selected.PackID != "telecom-governed" || selected.Version != "1" {
		t.Fatalf("execution selected the wrong pack: %+v", selected)
	}
	if selected.ContentHash != published.Pack.ContentHash {
		t.Fatal("execution saw a different content hash than core published")
	}

	// Execution must not write governance tables.
	ctx := context.Background()
	tx, err := execution.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "worker", Role: "worker", TenantID: accounts.Tenant(admin), OrgID: admin.OrgID}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE rule_pack_pointers SET version=version WHERE tenant_id=$1`, accounts.Tenant(admin)); err == nil {
		t.Fatal("execution role was able to write the governance pointer table")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO rule_pack_versions (tenant_id, pack_id, version, org_id, status, revision, manifest, content_hash, snapshot_digest, effective_from, effective_until, created_at, updated_at)
		VALUES ($1,'x','1','o','draft',1,'{}','0','0','a','b','c','c')`, accounts.Tenant(admin)); err == nil {
		t.Fatal("execution role was able to insert a governance version")
	}
}

// TestGovernanceCrossTenantIsolation proves forced RLS hides another tenant's
// versions and pointers even from a role that can read the table.
func TestGovernanceCrossTenantIsolation(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	core := env.openRole(t, "core")
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo admin missing")
	}
	publishGovernedPackStore(t, core, admin, "1", "100", governedPackManifest(t, accounts.Tenant(admin), "1", "100"))

	// A different tenant sees nothing.
	ctx := context.Background()
	tx, err := core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "other-admin", Role: "admin", TenantID: "other-tenant", OrgID: "other-org"}); err != nil {
		t.Fatal(err)
	}
	var versions, pointers int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM rule_pack_versions`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM rule_pack_pointers`).Scan(&pointers); err != nil {
		t.Fatal(err)
	}
	if versions != 0 || pointers != 0 {
		t.Fatalf("cross-tenant RLS leaked %d versions and %d pointers", versions, pointers)
	}
	// The owning tenant still sees its own row.
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "owner", Role: "admin", TenantID: accounts.Tenant(admin), OrgID: admin.OrgID}); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM rule_pack_pointers`).Scan(&pointers); err != nil {
		t.Fatal(err)
	}
	if pointers != 1 {
		t.Fatalf("owning tenant saw %d pointers, want 1", pointers)
	}
}

// TestGovernancePointerIsVisibleAcrossInstances proves a publish committed by one
// Core process is immediately visible to a second Core process reading the same
// schema, and that a stale pointer revision from the first process is refused.
func TestGovernancePointerIsVisibleAcrossInstances(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	first := env.openRole(t, "core")
	second := env.openRole(t, "core")
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo admin missing")
	}
	published := publishGovernedPackStore(t, first, admin, "1", "100", governedPackManifest(t, accounts.Tenant(admin), "1", "100"))

	visible, err := second.GetRulePackVersion(admin, "telecom-governed", "1")
	if err != nil {
		t.Fatalf("second instance could not read the published version: %v", err)
	}
	if !visible.Pack.IsActive || visible.Pack.ActivePointerRevision != 1 {
		t.Fatalf("second instance did not see the active pointer: %+v", visible.Pack)
	}

	// The second instance observes the same pointer revision, so a stale
	// revision from the first process is refused rather than overwriting.
	selected, err := second.SelectActiveRulePack(context.Background(), accounts.Tenant(admin),
		"ENT", "telecom_dedicated_line", "service", "2026-06-01T00:00:00Z")
	if err != nil || selected == nil || selected.PointerRevision != published.Pack.ActivePointerRevision {
		t.Fatalf("cross-instance selection disagreed: %+v %v", selected, err)
	}
	created, err := first.CreateRulePack(admin, governedPackManifest(t, accounts.Tenant(admin), "2", "200"))
	if err != nil {
		t.Fatal(err)
	}
	validated, err := first.ValidateRulePackVersion(admin, GovernanceInput{
		PackID: "telecom-governed", Version: "2", ExpectedRevision: created.Pack.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := first.ApproveRulePackVersion(admin, GovernanceInput{
		PackID: "telecom-governed", Version: "2", ExpectedRevision: validated.Pack.Revision,
		ExpectedContentHash: validated.Pack.ContentHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Publishing with the pre-publish pointer revision (0) must be refused now
	// that another process has already advanced the pointer to 1.
	if _, err := first.PublishRulePackVersion(admin, GovernanceInput{
		PackID: "telecom-governed", Version: "2", ExpectedRevision: approved.Pack.Revision,
		ExpectedPointerRevision: 0,
	}); err == nil {
		t.Fatal("a stale cross-instance pointer revision published a new version")
	}
	if _, err := first.PublishRulePackVersion(admin, GovernanceInput{
		PackID: "telecom-governed", Version: "2", ExpectedRevision: approved.Pack.Revision,
		ExpectedPointerRevision: 1,
	}); err != nil {
		t.Fatalf("publish with the current cross-instance revision failed: %v", err)
	}
	moved, err := second.GetRulePackVersion(admin, "telecom-governed", "2")
	if err != nil || !moved.Pack.IsActive {
		t.Fatalf("second instance did not observe the moved pointer: %+v %v", moved.Pack, err)
	}
	_ = errors.Is
}
