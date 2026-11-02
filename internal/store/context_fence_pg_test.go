package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/config"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// One bounded ordinary-role scenario covers context CAS, independent support
// approval, expiry after actual SQL blocking, and atomic zero-delta rejection.
func TestSessionContextSwitchSupportExpiryAndWriteFence(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := OpenTest(t.TempDir(), dsn)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) {
			t.Fatalf("%v SQL position=%d internal-position=%d internal-query=%s", err, pg.Position, pg.InternalPosition, pg.InternalQuery)
		}
		t.Fatal(err)
	}
	defer admin.Close()
	t.Setenv("APP_ENV", "test")
	t.Setenv("DATABASE_SCHEMA", admin.schema)
	t.Setenv("MIGRATOR_DATABASE_URL", dsn)
	secret, err := randomIdentityToken("private_test_")
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{"core", "document", "execution", "model", "tool"} {
		u, e := url.Parse(dsn)
		if e != nil {
			t.Fatal(e)
		}
		u.User = url.UserPassword("ctx_"+service+"_"+admin.schema, secret)
		t.Setenv(strings.ToUpper(service)+"_DATABASE_URL", u.String())
	}
	if err = MigrateServiceRoles(ctx); err != nil {
		t.Fatal(err)
	}
	cleanupRoles := []string{}
	for _, service := range []string{"core", "document", "execution", "model", "tool"} {
		cfg, e := pgx.ParseConfig(os.Getenv(strings.ToUpper(service) + "_DATABASE_URL"))
		if e != nil {
			t.Fatal(e)
		}
		cleanupRoles = append(cleanupRoles, cfg.User)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		conn, e := connectPoolDSN(cleanupCtx, dsn)
		if e != nil {
			return
		}
		defer conn.Close(cleanupCtx)
		for _, role := range cleanupRoles {
			_, _ = conn.Exec(cleanupCtx, `DROP OWNED BY `+pgx.Identifier{role}.Sanitize()+`; DROP ROLE `+pgx.Identifier{role}.Sanitize())
		}
	})
	coreDSN := os.Getenv("CORE_DATABASE_URL")
	coreCfg, err := pgx.ParseConfig(coreDSN)
	if err != nil {
		t.Fatal(err)
	}
	coreRole := coreCfg.User
	pool, err := newStorePool(ctx, coreDSN, admin.schema, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); pool.Close() }()
	var session, current string
	var super, bypass bool
	if err = pool.QueryRow(ctx, `SELECT session_user,current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&session, &current, &super, &bypass); err != nil || session != coreRole || current != coreRole || super || bypass {
		t.Fatal("genuine ordinary Core role assertion failed")
	}
	core := &Store{pool: pool, schema: admin.schema, serviceOwner: "core", sel: config.Selection{Mode: config.ModeTest, DSN: coreDSN}}
	if err = core.ValidateServiceDatabaseRole(ctx, "core"); err != nil {
		t.Fatal(err)
	}
	profiles := []accounts.User{}
	for _, name := range []string{"a", "b", "c"} {
		profiles = append(profiles, accounts.User{ID: "context-" + name, DisplayName: name, Role: "viewer", TenantID: "directory", OrgID: "directory", Enabled: true, Issuer: "https://context.invalid", Subject: name})
	}
	path := filepath.Join(t.TempDir(), "directory.json")
	raw, _ := json.Marshal(map[string]any{"users": profiles})
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = accounts.ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	defer accounts.ConfigureDirectory("")
	ids := []string{"a1111111-1111-4111-8111-111111111111", "b1111111-1111-4111-8111-111111111111", "c1111111-1111-4111-8111-111111111111"}
	member := []string{"a2222222-2222-4222-8222-222222222222", "b2222222-2222-4222-8222-222222222222", "c2222222-2222-4222-8222-222222222222", "a3333333-3333-4333-8333-333333333333"}
	doc := IdentityCrosswalk{Version: 1, Tenants: []IdentityTenantMap{{ID: "ctx-one", DisplayName: "one", Status: "active"}, {ID: "ctx-two", DisplayName: "two", Status: "active"}}, OrgUnits: []IdentityOrgUnitMap{{TenantID: "ctx-one", ID: "unit-one", DisplayName: "one", Status: "active"}, {TenantID: "ctx-two", ID: "unit-two", DisplayName: "two", Status: "active"}}}
	for i, p := range profiles {
		doc.Principals = append(doc.Principals, IdentityPrincipalMap{ID: ids[i], ActorID: p.ID, Issuer: p.Issuer, Subject: p.Subject, Status: "active"})
		tenant, org := "ctx-one", "unit-one"
		if i == 2 {
			tenant, org = "ctx-two", "unit-two"
		}
		doc.Memberships = append(doc.Memberships, IdentityMembershipMap{ID: member[i], ActorID: p.ID, TenantID: tenant, OrgUnitID: org, RoleCode: "clerk", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"})
	}
	doc.Memberships = append(doc.Memberships, IdentityMembershipMap{ID: member[3], ActorID: profiles[0].ID, TenantID: "ctx-two", OrgUnitID: "unit-two", RoleCode: "clerk", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"})
	var database string
	if err = admin.pool.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	if _, err = ImportIdentityCrosswalk(ctx, dsn, admin.schema, database, doc, true); err != nil {
		t.Fatal(err)
	}
	login := func(i int, choice string) (string, accounts.User) {
		t.Helper()
		r, e := core.ResolveVerifiedOIDC(ctx, profiles[i], "client")
		if e != nil {
			t.Fatal(e)
		}
		if r.PendingToken != "" {
			r, e = core.ConsumePendingMembership(ctx, r.PendingToken, choice, "client")
			if e != nil {
				t.Fatal(e)
			}
		}
		u, _, e := core.AuthenticateSession(ctx, r.SessionToken)
		if e != nil {
			t.Fatal(e)
		}
		return r.SessionToken, u
	}
	tokenA, a := login(0, member[0])
	_, b := login(1, member[1])
	_, c := login(2, member[2])
	caseID, err := core.CreateCase(a, "context authority", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if err = core.SwitchSessionContext(ctx, tokenA, "1", member[3]); err != nil {
		t.Fatal(err)
	}
	executionDSN := os.Getenv("EXECUTION_DATABASE_URL")
	executionCfg, e := pgx.ParseConfig(executionDSN)
	if e != nil {
		t.Fatal(e)
	}
	executionPool, e := newStorePool(ctx, executionDSN, admin.schema, true)
	if e != nil {
		t.Fatal(e)
	}
	defer executionPool.Close()
	if e = executionPool.QueryRow(ctx, `SELECT session_user,current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&session, &current, &super, &bypass); e != nil || session != executionCfg.User || current != session || super || bypass {
		t.Fatal("genuine ordinary Execution role assertion failed")
	}
	executionStore := &Store{pool: executionPool, schema: admin.schema, serviceOwner: "execution", sel: config.Selection{Mode: config.ModeTest, DSN: executionDSN}}
	if e = executionStore.ValidateServiceDatabaseRole(ctx, "execution"); e != nil {
		t.Fatal(e)
	}
	if tx, e := executionStore.beginContextTx(commandContext(ctx, a), pgx.TxOptions{IsoLevel: pgx.RepeatableRead}); e == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("cross-service Execution accepted old context")
	}
	if _, e = executionPool.Exec(ctx, `SELECT principal_id FROM identity_principals LIMIT 1`); e == nil {
		t.Fatal("Execution can directly resolve identity SQL")
	}
	if _, err = core.CreateCase(a, "stale command", "procurement_ict"); err == nil {
		t.Fatal("old context wrote after switch")
	}
	if err = core.SwitchSessionContext(ctx, tokenA, "1", member[0]); err == nil {
		t.Fatal("old switch CAS accepted")
	}
	if err = core.SwitchSessionContext(ctx, tokenA, "2", member[1]); err == nil {
		t.Fatal("cross-principal target accepted")
	}
	if err = core.SwitchSessionContext(ctx, tokenA, "2", member[0]); err != nil {
		t.Fatal(err)
	}
	a, _, err = core.AuthenticateSession(ctx, tokenA)
	if err != nil {
		t.Fatal(err)
	}
	request := SupportGrantRequest{PrincipalID: ids[2], Actions: []string{"case.read"}, Reason: "bounded case assistance", ExpiresAt: time.Now().Add(30 * time.Second)}
	grant, err := core.RequestSupportGrant(ctx, a, caseID, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = core.ChangeSupportGrant(ctx, a, grant.ID, "approve", grant.Revision); err == nil {
		t.Fatal("support self approval accepted")
	}
	grant, err = core.ChangeSupportGrant(ctx, b, grant.ID, "approve", grant.Revision)
	if err != nil {
		t.Fatal(err)
	}
	view, err := core.ReadSupportCase(ctx, c, grant.ID)
	if err != nil || view.CaseID != caseID || !view.ReadOnly || c.TenantID != "ctx-two" {
		t.Fatalf("exact support read/source identity: %+v %v", view, err)
	}
	grant, err = core.ChangeSupportGrant(ctx, b, grant.ID, "revoke", grant.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = core.ReadSupportCase(ctx, c, grant.ID); err == nil {
		t.Fatal("revoked support remained readable")
	}
	// Force a read to block specifically on the audit INSERT, after its first
	// authorization/expiry check, then prove Commit's final grant check rejects.
	request.ExpiresAt = time.Now().Add(1500 * time.Millisecond)
	late, err := core.RequestSupportGrant(ctx, a, caseID, request)
	if err != nil {
		t.Fatal(err)
	}
	late, err = core.ChangeSupportGrant(ctx, b, late.ID, "approve", late.Revision)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := admin.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, `LOCK TABLE `+pgx.Identifier{admin.schema, "identity_support_events"}.Sanitize()+` IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := core.ReadSupportCase(ctx, c, late.ID); done <- e }()
	waitContextSQLLock(t, ctx, admin, coreRole)
	time.Sleep(time.Until(late.ExpiresAt) + 50*time.Millisecond)
	if err = blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err == nil {
		t.Fatal("support expired during audit INSERT still returned sensitive view")
	}
	var leaked int
	if err = admin.pool.QueryRow(ctx, `SELECT count(*) FROM `+pgx.Identifier{admin.schema, "identity_support_events"}.Sanitize()+` WHERE grant_id=$1 AND action='read'`, late.ID).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("expired read committed an audit/event")
	}
	// Actual CreateCase blocks on its INSERT relation lock, not an artificial
	// timer/probe; expiry then rolls back the business row and activity atomically.
	if _, err = admin.pool.Exec(ctx, `UPDATE `+pgx.Identifier{admin.schema, "identity_memberships"}.Sanitize()+` SET effective_until=clock_timestamp()+interval '1500 milliseconds',revision=revision+1 WHERE membership_id=$1`, member[0]); err != nil {
		t.Fatal(err)
	}
	a, _, err = core.AuthenticateSession(ctx, tokenA)
	if err != nil {
		t.Fatal(err)
	}
	before := contextBusinessCounts(t, ctx, admin)
	blocker, err = admin.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, `LOCK TABLE `+pgx.Identifier{admin.schema, "cases"}.Sanitize()+` IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	go func() { _, e := core.CreateCase(a, "expired after SQL wait", "procurement_ict"); done <- e }()
	waitContextSQLLock(t, ctx, admin, coreRole)
	time.Sleep(1600 * time.Millisecond)
	_ = blocker.Rollback(ctx)
	if err = <-done; err == nil {
		t.Fatal("expired ordinary business write committed")
	}
	after := contextBusinessCounts(t, ctx, admin)
	if before != after {
		t.Fatalf("expired write changed business/receipt/outbox counts: %v -> %v", before, after)
	}
	var f *fault.Error
	if !errors.As(err, &f) && !strings.Contains(err.Error(), "session_invalid") {
		t.Fatalf("expiry did not return an authority error: %v", err)
	}
	// Principal-only and org-only imports touch the common tenant tuple before
	// changing authority. A genuine ordinary RR writer waiting behind that import
	// must serialize-fail rather than accept its older principal/org snapshot.
	if _, err = admin.pool.Exec(ctx, `UPDATE `+pgx.Identifier{admin.schema, "identity_memberships"}.Sanitize()+` SET effective_until=NULL,revision=revision+1 WHERE membership_id=$1`, member[0]); err != nil {
		t.Fatal(err)
	}
	barrier := pgx.Identifier{admin.schema, "context_import_barrier"}.Sanitize()
	if _, err = admin.pool.Exec(ctx, `CREATE FUNCTION `+barrier+`() RETURNS trigger LANGUAGE plpgsql AS $barrier$ BEGIN PERFORM pg_advisory_xact_lock(hashtextextended(TG_TABLE_SCHEMA||':context-import-barrier',0));RETURN NEW;END $barrier$;
 CREATE TRIGGER context_principal_barrier BEFORE UPDATE ON `+pgx.Identifier{admin.schema, "identity_principals"}.Sanitize()+` FOR EACH ROW WHEN (NEW.status='disabled') EXECUTE FUNCTION `+barrier+`();
 CREATE TRIGGER context_org_barrier BEFORE UPDATE ON `+pgx.Identifier{admin.schema, "identity_org_units"}.Sanitize()+` FOR EACH ROW WHEN (NEW.status='inactive') EXECUTE FUNCTION `+barrier+`()`); err != nil {
		t.Fatal(err)
	}
	adminConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"principal", "org"} {
		a, _, err = core.AuthenticateSession(ctx, tokenA)
		if err != nil {
			t.Fatal(err)
		}
		patch := IdentityCrosswalk{Version: 1}
		restore := IdentityCrosswalk{Version: 1}
		if kind == "principal" {
			item := doc.Principals[0]
			restore.Principals = []IdentityPrincipalMap{item}
			item.Status = "disabled"
			patch.Principals = []IdentityPrincipalMap{item}
		} else {
			item := doc.OrgUnits[0]
			restore.OrgUnits = []IdentityOrgUnitMap{item}
			item.Status = "inactive"
			patch.OrgUnits = []IdentityOrgUnitMap{item}
		}
		lock, e := admin.pool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = lock.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, admin.schema+":context-import-barrier"); e != nil {
			t.Fatal(e)
		}
		imported := make(chan error, 1)
		go func() { _, e := ImportIdentityCrosswalk(ctx, dsn, admin.schema, database, patch, true); imported <- e }()
		waitContextSQLLock(t, ctx, admin, adminConfig.User)
		observed := make(chan error, 1)
		go func() {
			tx, e := core.beginContextTx(commandContext(ctx, a), pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			if tx != nil {
				_ = tx.Rollback(ctx)
			}
			observed <- e
		}()
		waitContextSQLLock(t, ctx, admin, coreRole)
		_ = lock.Rollback(ctx)
		if e = <-imported; e != nil {
			t.Fatal(e)
		}
		e = <-observed
		var denied *fault.Error
		if !errors.As(e, &denied) || denied.Code != "context_stale" {
			t.Fatalf("%s import did not invalidate waiting ordinary RR snapshot: %v", kind, e)
		}
		if _, e = ImportIdentityCrosswalk(ctx, dsn, admin.schema, database, restore, true); e != nil {
			t.Fatal(e)
		}
	}

}
func waitContextSQLLock(t *testing.T, ctx context.Context, admin *Store, role string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := admin.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock'`, role).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("actual ordinary SQL lock wait was not observed")
}
func contextBusinessCounts(t *testing.T, ctx context.Context, s *Store) [4]int64 {
	t.Helper()
	var out [4]int64
	for i, table := range []string{"cases", activityTableFor("core"), "core_write_receipts", "outbox_events"} {
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM `+pgx.Identifier{s.schema, table}.Sanitize()).Scan(&out[i]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}
