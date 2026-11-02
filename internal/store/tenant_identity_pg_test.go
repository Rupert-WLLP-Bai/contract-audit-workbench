package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"

	"github.com/jackc/pgx/v5"
)

func TestTenantIdentityResolverAndPendingMembershipCoreRole(t *testing.T) {
	testDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if testDSN == "" {
		t.Skip("TEST_DATABASE_URL is required for isolated tenant identity PostgreSQL checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	st, err := OpenTest(t.TempDir(), testDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			t.Errorf("close tenant identity store: %v", err)
		}
	}()
	if err := migrate(ctx, st.pool, st.schema); err != nil {
		t.Fatalf("verify reapplication of the tenant identity migration prefix: %v", err)
	}

	issuer := "https://identity.example/tenant-test"
	directory := t.TempDir() + "/directory.json"
	users := []accounts.User{
		{ID: "identity-alice", DisplayName: "Alice", Role: "admin", TenantID: "directory-stale-a", OrgID: "directory-stale-a", Enabled: true, Issuer: issuer, Subject: "subject-alice",
			Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}},
		{ID: "identity-bob", DisplayName: "Bob", Role: "admin", TenantID: "directory-stale-b", OrgID: "directory-stale-b", Enabled: true, Issuer: issuer, Subject: "subject-bob",
			Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}},
		{ID: "identity-charlie", DisplayName: "Charlie", Role: "admin", TenantID: "directory-stale-c", OrgID: "directory-stale-c", Enabled: true, Issuer: issuer, Subject: "subject-charlie",
			Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}},
	}
	encoded, err := json.Marshal(map[string]any{"users": users})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directory, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = accounts.ConfigureDirectory("") })

	coreDSN, coreRole := tenantIdentityTestCore(t, ctx, st)
	corePool, err := newStorePool(ctx, coreDSN, st.schema, true)
	if err != nil {
		t.Fatal(err)
	}
	defer corePool.Close()
	var sessionUser, currentUser string
	var coreSuperuser, coreBypassRLS bool
	if err := corePool.QueryRow(ctx, `SELECT session_user,current_user,rolsuper,rolbypassrls
		FROM pg_roles WHERE rolname=current_user`).Scan(&sessionUser, &currentUser, &coreSuperuser, &coreBypassRLS); err != nil {
		t.Fatalf("verify isolated Core database session identity: %v", err)
	}
	if sessionUser != coreRole || currentUser != coreRole || coreSuperuser || coreBypassRLS {
		t.Fatalf("identity resolver test is not using the intended bounded Core role: session=%q current=%q superuser=%t bypassrls=%t", sessionUser, currentUser, coreSuperuser, coreBypassRLS)
	}
	core := &Store{pool: corePool, schema: st.schema, serviceOwner: "core"}
	assertAuthOwnerSessionScopeGuard(t, ctx, st)

	grantA := []access.Grant{{Role: "reviewer", Scope: "case", CaseID: "case-a"}}
	grantB := []access.Grant{{Role: "viewer", Scope: "case", CaseID: "case-b"}}
	document := IdentityCrosswalk{
		Version:       1,
		Jurisdictions: []IdentityJurisdictionMap{{ID: "region-test", DisplayName: "Test region", Status: "active"}},
		Tenants: []IdentityTenantMap{
			{ID: "tenant-a", DisplayName: "Tenant A", JurisdictionID: stringPtr("region-test"), Status: "active"},
			{ID: "tenant-b", DisplayName: "Tenant B", JurisdictionID: stringPtr("region-test"), Status: "active"},
			{ID: "tenant-single", DisplayName: "Tenant Single", JurisdictionID: stringPtr("region-test"), Status: "active"},
		},
		OrgUnits: []IdentityOrgUnitMap{
			{TenantID: "tenant-a", ID: "org-a", DisplayName: "Org A", Status: "active"},
			{TenantID: "tenant-b", ID: "org-b", DisplayName: "Org B", Status: "active"},
			{TenantID: "tenant-single", ID: "org-single", DisplayName: "Org Single", Status: "active"},
		},
		Principals: []IdentityPrincipalMap{
			{ID: "11111111-1111-4111-8111-111111111111", ActorID: users[0].ID, Issuer: issuer, Subject: users[0].Subject, Status: "active"},
			{ID: "44444444-4444-4444-8444-444444444444", ActorID: users[1].ID, Issuer: issuer, Subject: users[1].Subject, Status: "active"},
		},
		Memberships: []IdentityMembershipMap{
			{ID: "22222222-2222-4222-8222-222222222222", ActorID: users[0].ID, TenantID: "tenant-a", OrgUnitID: "org-a", RoleCode: "reviewer", Grants: grantA, Status: "active"},
			{ID: "33333333-3333-4333-8333-333333333333", ActorID: users[0].ID, TenantID: "tenant-b", OrgUnitID: "org-b", RoleCode: "viewer", Grants: grantB, Status: "active"},
			{ID: "55555555-5555-4555-8555-555555555555", ActorID: users[1].ID, TenantID: "tenant-single", OrgUnitID: "org-single", RoleCode: "reviewer", Grants: grantA, Status: "active"},
		},
	}
	adminConfig, err := pgx.ParseConfig(testDSN)
	if err != nil {
		t.Fatal(err)
	}
	adminDSN := adminConfig.ConnString()
	for _, apply := range []bool{false, true, true} {
		if _, err := ImportIdentityCrosswalk(ctx, adminDSN, st.schema, adminConfig.Database, document, apply); err != nil {
			t.Fatalf("import crosswalk apply=%t: %v", apply, err)
		}
	}
	conflict := document
	conflict.Principals = append([]IdentityPrincipalMap(nil), document.Principals...)
	conflict.Principals[0].ID = "66666666-6666-4666-8666-666666666666"
	if _, err := ImportIdentityCrosswalk(ctx, adminDSN, st.schema, adminConfig.Database, conflict, true); err == nil {
		t.Fatal("crosswalk that changed a stable principal ID was accepted")
	}
	profile, ok := accounts.BySubject(issuer, users[0].Subject)
	if !ok {
		t.Fatal("test directory did not resolve exact issuer+subject")
	}
	assertTenantIdentityLockOrdering(t, ctx, st, core, adminDSN, adminConfig.Database, profile, document, true)
	assertTenantIdentityLockOrdering(t, ctx, st, core, adminDSN, adminConfig.Database, profile, document, false)
	if _, err := ImportIdentityCrosswalk(ctx, adminDSN, st.schema, adminConfig.Database, document, true); err != nil {
		t.Fatalf("restore active crosswalk after lock-ordering cases: %v", err)
	}

	first, err := core.ResolveVerifiedOIDC(ctx, profile, "client")
	if err != nil || first.PendingToken == "" || len(first.Choices) != 2 {
		t.Fatalf("multi-membership login = pending %t choices %d err %v", first.PendingToken != "", len(first.Choices), err)
	}
	listed, err := core.ListPendingMemberships(ctx, first.PendingToken, "client")
	if err != nil || len(listed) != 2 {
		t.Fatalf("pending membership list = %d choices, err %v", len(listed), err)
	}
	assertPendingListWaitsForRevocation(t, ctx, st, core, first.PendingToken, document.Memberships[0].ID)
	if _, err := ImportIdentityCrosswalk(ctx, adminDSN, st.schema, adminConfig.Database, document, true); err != nil {
		t.Fatalf("restore active crosswalk after pending-list wait case: %v", err)
	}
	if _, err := core.ConsumePendingMembership(ctx, first.PendingToken, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "client"); err == nil {
		t.Fatal("membership outside the exact challenge was accepted")
	}
	selectedA, err := core.ConsumePendingMembership(ctx, first.PendingToken, document.Memberships[0].ID, "client")
	if err != nil {
		t.Fatalf("consume exact tenant A membership: %v", err)
	}
	aUser, audience, err := core.AuthenticateSession(ctx, selectedA.SessionToken)
	if err != nil || audience != "client" || aUser.TenantID != "tenant-a" || aUser.Role != "reviewer" || len(aUser.Grants) != 1 || aUser.Grants[0].CaseID != "case-a" {
		t.Fatalf("resolved tenant A session=%+v audience=%q err=%v", aUser, audience, err)
	}
	if authorizeCase(Case{ID: "case-a", TenantID: "tenant-a", OwnerOrgID: "org-a"}, aUser, access.Review) != true ||
		authorizeCase(Case{ID: "case-b", TenantID: "tenant-b", OwnerOrgID: "org-b"}, aUser, access.Read) {
		t.Fatal("tenant A membership did not retain exact case grant boundaries")
	}
	assertExecutionAdmissionBinding(t, ctx, st, aUser, selectedA.SessionToken, issuer, users[0].Subject)
	second, err := core.ResolveVerifiedOIDC(ctx, profile, "client")
	if err != nil || second.PendingToken == "" {
		t.Fatalf("second login pending=%t err=%v", second.PendingToken != "", err)
	}
	selectedB, err := core.ConsumePendingMembership(ctx, second.PendingToken, document.Memberships[1].ID, "client")
	if err != nil {
		t.Fatalf("consume exact tenant B membership: %v", err)
	}
	bUser, audience, err := core.AuthenticateSession(ctx, selectedB.SessionToken)
	if err != nil || audience != "client" || bUser.TenantID != "tenant-b" || bUser.Role != "viewer" || len(bUser.Grants) != 1 || bUser.Grants[0].CaseID != "case-b" {
		t.Fatalf("resolved tenant B session=%+v audience=%q err=%v", bUser, audience, err)
	}
	if authorizeCase(Case{ID: "case-b", TenantID: "tenant-b", OwnerOrgID: "org-b"}, bUser, access.Read) != true ||
		authorizeCase(Case{ID: "case-a", TenantID: "tenant-a", OwnerOrgID: "org-a"}, bUser, access.Review) {
		t.Fatal("tenant B membership did not retain exact case grant boundaries")
	}
	if _, _, err := core.AuthenticateSession(ctx, selectedA.SessionToken); err != nil {
		t.Fatalf("tenant A session changed after selecting B in a separate login: %v", err)
	}
	profile, ok = accounts.BySubject(issuer, users[1].Subject)
	if !ok {
		t.Fatal("test directory did not resolve the single-member identity")
	}
	single, err := core.ResolveVerifiedOIDC(ctx, profile, "client")
	if err != nil || single.SessionToken == "" || single.PendingToken != "" || single.User.TenantID != "tenant-single" || single.User.Role != "reviewer" {
		t.Fatalf("single-membership login=%+v err=%v", single, err)
	}
	profile, _ = accounts.BySubject(issuer, users[2].Subject)
	if _, err := core.ResolveVerifiedOIDC(ctx, profile, "client"); err == nil {
		t.Fatal("directory identity without a database membership was accepted")
	}
	profile, _ = accounts.BySubject(issuer, users[0].Subject)
	if _, err := core.ResolveVerifiedOIDC(ctx, profile, "admin"); err == nil {
		t.Fatal("directory admin role without a database admin membership was accepted")
	}
	expiredChallenge, err := core.ResolveVerifiedOIDC(ctx, profile, "client")
	if err != nil || expiredChallenge.PendingToken == "" {
		t.Fatalf("create expiring pending challenge: result=%+v err=%v", expiredChallenge, err)
	}
	if _, err := st.pool.Exec(ctx, `UPDATE `+pgx.Identifier{st.schema, "identity_pending_auth"}.Sanitize()+
		` SET created_at=clock_timestamp()-interval '10 minutes',expires_at=clock_timestamp()-interval '9 minutes' WHERE token_digest=$1`, identityTokenDigest(expiredChallenge.PendingToken)); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ListPendingMemberships(ctx, expiredChallenge.PendingToken, "client"); err == nil {
		t.Fatal("expired pending authentication challenge was listed")
	}
	if _, err := st.pool.Exec(ctx, `UPDATE `+pgx.Identifier{st.schema, "identity_memberships"}.Sanitize()+
		` SET effective_until=clock_timestamp()-interval '1 second',revision=revision+1 WHERE membership_id=$1::uuid`, document.Memberships[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := core.AuthenticateSession(ctx, selectedB.SessionToken); err == nil {
		t.Fatal("expired membership retained an authenticated session")
	}
	if _, _, err := core.AuthenticateSession(ctx, selectedA.SessionToken); err != nil {
		t.Fatalf("expiring tenant B membership invalidated tenant A session: %v", err)
	}
	assertSessionAuthWaitsForRevocation(t, ctx, st, core, selectedA.SessionToken, document.Memberships[0].ID)

	// The Core ticket path uses the ordinary Core service role. This preserves
	// the local demo session boundary. This final functional smoke uses the
	// administrator fixture; restricted-role evidence is asserted above and in
	// the separate formal distributed-service HTTP scenarios.
	if err := accounts.ConfigureDirectory(""); err != nil {
		t.Fatal(err)
	}
	demoAdmin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("local demonstration administrator is unavailable")
	}
	fileBytes := []byte("ticket-bound primary contract")
	operation, err := st.BeginCaseIntake(ctx, demoAdmin, CaseIntakeInput{
		Title: "Ticket authority", ContractType: "service", BusinessLine: "ENT", Scenario: "cloud_services",
		Filename: "ticket.txt", MediaType: "text/plain", Body: fileBytes, IdempotencyKey: "identity-ticket-authority",
	})
	if err != nil {
		t.Fatalf("begin ticket-bound intake: %v", err)
	}
	sessionToken, err := st.EstablishSession(demoAdmin, "client")
	if err != nil {
		t.Fatalf("establish ticket-bound session: %v", err)
	}
	ticket, err := st.CreateDocumentIntakeTicket(ctx, demoAdmin, sessionToken, operation.OperationID, operation.CaseID, operation.UploadKey, fileBytes)
	if err != nil {
		t.Fatalf("create Document intake ticket: %v", err)
	}
	authority, err := st.ResolveDocumentIntakeTicket(ctx, ticket, caseIntakeDigest(fileBytes))
	if err != nil || authority.ActorID != demoAdmin.ID || authority.CaseID != operation.CaseID ||
		authority.UploadKey != operation.UploadKey || authority.BodySHA256 != caseIntakeDigest(fileBytes) {
		t.Fatalf("resolve ticket authority=%+v err=%v", authority, err)
	}
	if _, err := st.ResolveDocumentIntakeTicket(ctx, ticket, caseIntakeDigest([]byte("changed"))); err == nil {
		t.Fatal("ticket was reusable for different upload bytes")
	}
	if err := st.Logout(sessionToken); err != nil {
		t.Fatalf("revoke ticket-bound session: %v", err)
	}
	if _, err := st.ResolveDocumentIntakeTicket(ctx, ticket, caseIntakeDigest(fileBytes)); err == nil {
		t.Fatal("revoked session still redeemed an intake ticket")
	}
}

func assertExecutionAdmissionBinding(t *testing.T, ctx context.Context, admin *Store, user accounts.User, sessionToken, issuer, subject string) {
	t.Helper()
	caseID, runID, attemptID, jobID := "case-a", "run-admission-a", "attempt-admission-a", "job-admission-a"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := admin.pool.Exec(ctx, `INSERT INTO cases(id,title,contract_type,owner_id,member_ids,created_at,updated_at,tenant_id,org_id)
		VALUES($1,'Admission case','service',$2,'[]',$3,$3,$4,$5)`, caseID, user.ID, now, user.TenantID, user.OrgID); err != nil {
		t.Fatalf("seed exact admission case: %v", err)
	}
	if _, err := admin.pool.Exec(ctx, `INSERT INTO review_runs(id,case_id,state,created_at,updated_at,tenant_id,org_id)
		VALUES($1,$2,'accepted',$3,$3,$4,$5)`, runID, caseID, now, user.TenantID, user.OrgID); err != nil {
		t.Fatalf("seed accepted admission run: %v", err)
	}
	if _, err := admin.pool.Exec(ctx, `INSERT INTO review_attempts(id,run_id,ordinal,state,accepted_at,history_kind,actor_id,tenant_id,org_id,input_hash)
		VALUES($1,$2,1,'accepted',$3,'frozen',$4,$5,$6,'frozen-input-digest')`, attemptID, runID, now, user.ID, user.TenantID, user.OrgID); err != nil {
		t.Fatalf("seed frozen admission attempt: %v", err)
	}
	executionDSN, executionRole := tenantIdentityTestExecution(t, ctx, admin)
	executionPool, err := newStorePool(ctx, executionDSN, admin.schema, true)
	if err != nil {
		t.Fatal(err)
	}
	defer executionPool.Close()
	var sessionUser, currentUser string
	var isSuper, bypass bool
	if err := executionPool.QueryRow(ctx, `SELECT session_user,current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&sessionUser, &currentUser, &isSuper, &bypass); err != nil {
		t.Fatal(err)
	}
	if sessionUser != executionRole || currentUser != executionRole || isSuper || bypass {
		t.Fatalf("admission binder was not called as bounded Execution: session=%q current=%q superuser=%t bypassrls=%t", sessionUser, currentUser, isSuper, bypass)
	}
	tx, err := executionPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(user)); err != nil {
		t.Fatal(err)
	}
	job := Job{ID: jobID, TenantID: user.TenantID, OrgID: user.OrgID, CaseID: caseID, RunID: runID, AttemptID: attemptID,
		ActorID: user.ID, PrincipalID: user.PrincipalID, MembershipID: user.MembershipID, IdentityCaptured: true,
		Kind: "review", State: "queued", CreatedAt: now, UpdatedAt: now}
	if err := syncJobs(WithAdmissionSessionToken(ctx, sessionToken), tx, []Job{job}); err != nil {
		t.Fatalf("Execution did not bind the admitted job to its live session: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var boundPrincipal, boundMembership string
	if err := admin.pool.QueryRow(ctx, `SELECT principal_id::text,membership_id::text FROM worker_claim_identities WHERE tenant_id=$1 AND job_id=$2`,
		user.TenantID, jobID).Scan(&boundPrincipal, &boundMembership); err != nil {
		t.Fatal(err)
	}
	if boundPrincipal != user.PrincipalID || boundMembership != user.MembershipID {
		t.Fatalf("admission binding principal/membership = %q/%q, want %q/%q", boundPrincipal, boundMembership, user.PrincipalID, user.MembershipID)
	}
	// A session from another tenant cannot replay the same immutable job bind.
	if _, err := executionPool.Exec(ctx, `SELECT tenant_identity_bind_admission($1,$2)`, sessionTokenDigest("not-the-admission-session"), jobID); err == nil {
		t.Fatal("an unrelated session digest bound an existing review job")
	}
	assertOperationAuthorityScopes(t, ctx, admin, user, sessionToken, jobID, runID, attemptID)
	_ = issuer
	_ = subject
}

func tenantIdentityTestExecution(t *testing.T, ctx context.Context, admin *Store) (string, string) {
	t.Helper()
	var name [12]byte
	if _, err := rand.Read(name[:]); err != nil {
		t.Fatal(err)
	}
	role := "caw_test_execution_" + hex.EncodeToString(name[:])
	password := "x" + hex.EncodeToString(name[:]) + hex.EncodeToString(name[:])
	quotedPassword := "'" + strings.ReplaceAll(password, "'", "''") + "'"
	if _, err := admin.pool.Exec(ctx, `CREATE ROLE `+pgx.Identifier{role}.Sanitize()+` LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT PASSWORD `+quotedPassword); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.pool.Exec(context.Background(), `DROP ROLE IF EXISTS `+pgx.Identifier{role}.Sanitize())
	})
	schema := pgx.Identifier{admin.schema}.Sanitize()
	quotedRole := pgx.Identifier{role}.Sanitize()
	if _, err := admin.pool.Exec(ctx, `GRANT USAGE ON SCHEMA `+schema+` TO `+quotedRole); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.pool.Exec(ctx, `GRANT INSERT,UPDATE,SELECT ON `+schema+`.worker_jobs TO `+quotedRole); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.pool.Exec(ctx, `GRANT EXECUTE ON FUNCTION `+schema+`.tenant_identity_bind_admission(text,text) TO `+quotedRole); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(admin.sel.DSN)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(role, password)
	return parsed.String(), role
}

func TestTenantIdentityNonSuperMigratorProvisioningAndMigration(t *testing.T) {
	testDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if testDSN == "" {
		t.Skip("TEST_DATABASE_URL is required for isolated tenant identity migration role checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	adminConfig, err := pgx.ParseConfig(testDSN)
	if err != nil {
		t.Fatal(err)
	}
	var suffix [10]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	suffixText := hex.EncodeToString(suffix[:])
	schema := "it_identity_" + suffixText
	migrator := "caw_mig_" + suffixText
	password := "x" + hex.EncodeToString(suffix[:]) + hex.EncodeToString(suffix[:])
	adminDSN := adminConfig.ConnString()
	admin, err := connectPoolDSN(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, `CREATE ROLE `+pgx.Identifier{migrator}.Sanitize()+` LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB CREATEROLE NOREPLICATION NOINHERIT PASSWORD '`+password+`'`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()+` AUTHORIZATION `+pgx.Identifier{migrator}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	roleName := tenantIdentityOwnerName(adminConfig.Database, schema)
	clmName := clmMaintenanceRoleName(adminConfig.Database, schema)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, `REVOKE `+pgx.Identifier{roleName}.Sanitize()+` FROM `+pgx.Identifier{migrator}.Sanitize())
		_, _ = admin.Exec(cleanupCtx, `REVOKE `+pgx.Identifier{clmName}.Sanitize()+` FROM `+pgx.Identifier{migrator}.Sanitize())
		_, _ = admin.Exec(cleanupCtx, `DROP SCHEMA IF EXISTS `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
		_, _ = admin.Exec(cleanupCtx, `DROP ROLE IF EXISTS `+pgx.Identifier{roleName}.Sanitize())
		_, _ = admin.Exec(cleanupCtx, `DROP ROLE IF EXISTS `+pgx.Identifier{clmName}.Sanitize())
		_, _ = admin.Exec(cleanupCtx, `DROP ROLE IF EXISTS `+pgx.Identifier{migrator}.Sanitize())
	})
	migratorURL, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	migratorURL.User = url.UserPassword(migrator, password)
	migratorDSN := migratorURL.String()
	var roleLogin, roleSuper, roleBypass, roleCreateDB, roleCreateRole, roleReplication bool
	if err := admin.QueryRow(ctx, `SELECT rolcanlogin,rolsuper,rolbypassrls,rolcreatedb,rolcreaterole,rolreplication FROM pg_roles WHERE rolname=$1`, migrator).
		Scan(&roleLogin, &roleSuper, &roleBypass, &roleCreateDB, &roleCreateRole, &roleReplication); err != nil {
		t.Fatalf("inspect isolated offline migrator role flags: %v", err)
	}
	if !roleLogin || roleSuper || roleBypass || roleCreateDB || !roleCreateRole || roleReplication {
		t.Fatalf("isolated offline migrator flags are unexpected: login=%t super=%t bypass=%t createdb=%t createrole=%t replication=%t", roleLogin, roleSuper, roleBypass, roleCreateDB, roleCreateRole, roleReplication)
	}
	for _, provision := range []func(context.Context, string, string, string) error{ProvisionCLMMaintenanceRole, ProvisionTenantIdentityOwner} {
		if err := provision(ctx, adminDSN, migratorDSN, schema); err != nil {
			t.Fatalf("pre-provision migration owner %T: %v", provision, err)
		}
		if err := provision(ctx, adminDSN, migratorDSN, schema); err != nil {
			t.Fatalf("repeat migration owner pre-provision %T: %v", provision, err)
		}
	}
	migrationPool, err := newStorePool(ctx, migratorDSN, schema, false)
	if err != nil {
		t.Fatal(err)
	}
	defer migrationPool.Close()
	if err := migrate(ctx, migrationPool, schema); err != nil {
		t.Fatalf("non-superuser offline migrator schema migration: %v", err)
	}
	if err := migrate(ctx, migrationPool, schema); err != nil {
		t.Fatalf("non-superuser offline migrator migration rerun: %v", err)
	}
	var currentUser, sessionUser string
	var isSuper, bypass bool
	if err := migrationPool.QueryRow(ctx, `SELECT current_user,session_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&currentUser, &sessionUser, &isSuper, &bypass); err != nil {
		t.Fatal(err)
	}
	if currentUser != migrator || sessionUser != migrator || isSuper || bypass {
		t.Fatalf("migration ran with unexpected authority: current_user=%q session_user=%q super=%t bypass=%t", currentUser, sessionUser, isSuper, bypass)
	}
	var adminOption, inheritOption, setOption bool
	if err := admin.QueryRow(ctx, `SELECT membership.admin_option,membership.inherit_option,membership.set_option
		FROM pg_auth_members membership JOIN pg_roles granted_role ON granted_role.oid=membership.roleid
		JOIN pg_roles member_role ON member_role.oid=membership.member
		WHERE granted_role.rolname=$1 AND member_role.rolname=$2`, roleName, migrator).Scan(&adminOption, &inheritOption, &setOption); err != nil {
		t.Fatal(err)
	}
	if adminOption || inheritOption || !setOption {
		t.Fatalf("AUTH_FENCE membership is not exact SET-only: admin=%t inherit=%t set=%t", adminOption, inheritOption, setOption)
	}
	var canAssumeAdmin bool
	if err := admin.QueryRow(ctx, `SELECT pg_has_role($1,$2,'MEMBER')`, migrator, adminConfig.User).Scan(&canAssumeAdmin); err != nil {
		t.Fatal(err)
	}
	if canAssumeAdmin {
		t.Fatal("offline migrator unexpectedly has administrator membership")
	}
}

func tenantIdentityTestCore(t *testing.T, ctx context.Context, st *Store) (string, string) {
	t.Helper()
	var name [12]byte
	if _, err := rand.Read(name[:]); err != nil {
		t.Fatal(err)
	}
	role := "caw_test_core_" + hex.EncodeToString(name[:])
	password := "x" + hex.EncodeToString(name[:]) + hex.EncodeToString(name[:])
	quotedPassword := "'" + strings.ReplaceAll(password, "'", "''") + "'"
	if _, err := st.pool.Exec(ctx, `CREATE ROLE `+pgx.Identifier{role}.Sanitize()+` LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT PASSWORD `+quotedPassword); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(context.Background(), `DROP ROLE IF EXISTS `+pgx.Identifier{role}.Sanitize())
	})
	schema := pgx.Identifier{st.schema}.Sanitize()
	quotedRole := pgx.Identifier{role}.Sanitize()
	if _, err := st.pool.Exec(ctx, `GRANT USAGE ON SCHEMA `+schema+` TO `+quotedRole); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(ctx, `GRANT SELECT (token,actor_id,issuer,subject,tenant_id,org_id,audience,expires_at,principal_id,membership_id)
		ON `+schema+`.sessions TO `+quotedRole); err != nil {
		t.Fatal(err)
	}
	for _, function := range []string{
		"tenant_identity_oidc_login(text,text,text,text,text,text)",
		"tenant_identity_list_pending(text,text)",
		"tenant_identity_consume_pending(text,uuid,text,text,text)",
		"tenant_identity_session_user(text)",
	} {
		if _, err := st.pool.Exec(ctx, `GRANT EXECUTE ON FUNCTION `+schema+`.`+function+` TO `+quotedRole); err != nil {
			t.Fatal(err)
		}
	}
	parsed, err := url.Parse(st.sel.DSN)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(role, password)
	return parsed.String(), role
}

func assertPendingListWaitsForRevocation(t *testing.T, ctx context.Context, st, core *Store, pendingToken, membershipID string) {
	t.Helper()
	lockTx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback(ctx)
	if _, err := lockTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, st.schema+":tenant-identity"); err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(ctx, `UPDATE `+pgx.Identifier{st.schema, "identity_memberships"}.Sanitize()+
		` SET status='revoked',revision=revision+1 WHERE membership_id=$1::uuid`, membershipID); err != nil {
		t.Fatal(err)
	}
	type listResult struct {
		choices []accounts.MembershipChoice
		err     error
	}
	listDone := make(chan listResult, 1)
	go func() {
		choices, err := core.ListPendingMemberships(ctx, pendingToken, "client")
		listDone <- listResult{choices: choices, err: err}
	}()
	waitForTenantIdentityLockWaiters(t, ctx, st, 1)
	if err := lockTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	result := <-listDone
	if result.err != nil {
		t.Fatalf("pending list failed after waiting for revocation: %v", result.err)
	}
	for _, choice := range result.choices {
		if choice.ID == membershipID {
			t.Fatal("pending membership list returned the revoked membership after waiting for the revocation")
		}
	}
}

func assertSessionAuthWaitsForRevocation(t *testing.T, ctx context.Context, st, core *Store, sessionToken, membershipID string) {
	t.Helper()
	lockTx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback(ctx)
	if _, err := lockTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, st.schema+":tenant-identity"); err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(ctx, `UPDATE `+pgx.Identifier{st.schema, "identity_memberships"}.Sanitize()+
		` SET status='suspended',revision=revision+1 WHERE membership_id=$1::uuid`, membershipID); err != nil {
		t.Fatal(err)
	}
	authDone := make(chan error, 1)
	go func() {
		_, _, err := core.AuthenticateSession(ctx, sessionToken)
		authDone <- err
	}()
	waitForTenantIdentityLockWaiters(t, ctx, st, 1)
	if err := lockTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-authDone; err == nil {
		t.Fatal("session authentication succeeded after waiting for membership revocation")
	}
}

func assertTenantIdentityLockOrdering(t *testing.T, ctx context.Context, st *Store, core *Store, adminDSN, database string, profile accounts.User, document IdentityCrosswalk, importerFirst bool) {
	t.Helper()
	active := document
	active.Memberships = append([]IdentityMembershipMap(nil), document.Memberships...)
	active.Memberships[0].Status = "active"
	if _, err := ImportIdentityCrosswalk(ctx, adminDSN, st.schema, database, active, true); err != nil {
		t.Fatalf("restore active membership before lock ordering case: %v", err)
	}
	pending, err := core.ResolveVerifiedOIDC(ctx, profile, "client")
	if err != nil || pending.PendingToken == "" {
		t.Fatalf("create pending challenge for lock ordering: result=%+v err=%v", pending, err)
	}
	lockTx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback(ctx)
	if _, err := lockTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, st.schema+":tenant-identity"); err != nil {
		t.Fatal(err)
	}
	type importResult struct{ err error }
	importDone := make(chan importResult, 1)
	consumeDone := make(chan accounts.OIDCLoginResult, 1)
	consumeErr := make(chan error, 1)
	applyRevocation := func() {
		changed := document
		changed.Memberships = append([]IdentityMembershipMap(nil), document.Memberships...)
		changed.Memberships[0].Status = "revoked"
		_, err := ImportIdentityCrosswalk(ctx, adminDSN, st.schema, database, changed, true)
		importDone <- importResult{err: err}
	}
	consume := func() {
		result, err := core.ConsumePendingMembership(ctx, pending.PendingToken, document.Memberships[0].ID, "client")
		consumeDone <- result
		consumeErr <- err
	}
	startImporter := func() { go applyRevocation() }
	startConsumer := func() { go consume() }
	if importerFirst {
		startImporter()
		waitForTenantIdentityLockWaiters(t, ctx, st, 1)
		startConsumer()
		waitForTenantIdentityLockWaiters(t, ctx, st, 2)
	} else {
		startConsumer()
		waitForTenantIdentityLockWaiters(t, ctx, st, 1)
		startImporter()
		waitForTenantIdentityLockWaiters(t, ctx, st, 2)
	}
	if err := lockTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	imported := <-importDone
	if imported.err != nil {
		t.Fatalf("concurrent membership revocation import failed: %v", imported.err)
	}
	result := <-consumeDone
	consumeFailure := <-consumeErr
	if importerFirst {
		if consumeFailure == nil || result.SessionToken != "" {
			t.Fatal("challenge consume passed after the revocation import acquired the shared identity lock first")
		}
		return
	}
	if consumeFailure != nil || result.SessionToken == "" {
		t.Fatalf("challenge consume that acquired the shared identity lock first failed: %v", consumeFailure)
	}
	if _, _, err := core.AuthenticateSession(ctx, result.SessionToken); err == nil {
		t.Fatal("session remained usable after a serialized later membership revocation")
	}
}

func waitForTenantIdentityLockWaiters(t *testing.T, ctx context.Context, st *Store, count int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		err := st.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname=current_database() AND wait_event_type='Lock' AND wait_event='advisory'
			AND pid<>pg_backend_pid()`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("identity advisory lock had fewer than %d blocked operations", count)
}

func stringPtr(value string) *string { return &value }

// Exercise the policy under its actual definer owner, not the superuser bypass.
func assertAuthOwnerSessionScopeGuard(t *testing.T, ctx context.Context, st *Store) {
	t.Helper()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var database string
	if err = tx.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	owner := tenantIdentityOwnerName(database, st.schema)
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE `+pgx.Identifier{owner}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	var current string
	if err = tx.QueryRow(ctx, `SELECT current_user`).Scan(&current); err != nil || current != owner {
		t.Fatal("AUTH owner scope guard role proof failed")
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('app.tenant_id','guard-tenant',true),set_config('app.org_id','guard-org',true),set_config('app.actor_id','guard-actor',true)`); err != nil {
		t.Fatal(err)
	}
	for _, values := range [][2]string{{"wrong-org", "guard-actor"}, {"guard-org", "wrong-actor"}, {"guard-org", "guard-actor"}} {
		attempt, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = attempt.Exec(ctx, `INSERT INTO sessions(token,actor_id,tenant_id,org_id,audience,expires_at) VALUES($1,$2,'guard-tenant',$3,'client','2099-01-01T00:00:00Z')`, "scope-guard-"+values[0]+values[1], values[1], values[0])
		valid := values[0] == "guard-org" && values[1] == "guard-actor"
		if valid && err != nil {
			t.Fatalf("exact AUTH owner session scope rejected: %v", err)
		}
		if !valid && (err == nil || !strings.Contains(err.Error(), "row-level security")) {
			t.Fatalf("wrong AUTH owner session scope was not denied by RLS: %v", err)
		}
		_ = attempt.Rollback(ctx)
	}
}
