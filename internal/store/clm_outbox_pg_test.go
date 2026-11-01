package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestCLMMaintenanceRoleNonSuperuserBootstrapAndRerun proves the first schema
// install and rerun work with a test-local non-superuser migrator. It also
// injects a table-level grant to the owner and requires migration to fail
// closed instead of silently normalizing that unexpected privilege.
func TestCLMMaintenanceRoleNonSuperuserBootstrapAndRerun(t *testing.T) {
	testDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if testDSN == "" {
		t.Skip("TEST_DATABASE_URL is required for the non-superuser CLM role migration check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	testConfig, err := pgx.ParseConfig(testDSN)
	if err != nil {
		t.Fatal(err)
	}
	id, err := newID("")
	if err != nil {
		t.Fatal(err)
	}
	schema := "it_clm_owner_" + id
	if len(schema) > 63 {
		schema = schema[:63]
	}
	migratorRole := "it_clm_migrator_" + id[:16]
	migratorPassword := uuid.NewString()
	t.Setenv("APP_ENV", config.ModeTest)
	t.Setenv("TEST_DATABASE_URL", testDSN)
	t.Setenv("DATABASE_SCHEMA", schema)

	admin, err := connectPoolDSN(ctx, testDSN)
	if err != nil {
		t.Fatalf("connect isolated database cleanup identity: %v", err)
	}
	ownerRole := clmMaintenanceRoleName(testConfig.Database, schema)
	defer func() {
		quotedSchema := pgx.Identifier{schema}.Sanitize()
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+quotedSchema+` CASCADE`)
		_, _ = admin.Exec(context.Background(), `REVOKE `+pgx.Identifier{ownerRole}.Sanitize()+` FROM `+pgx.Identifier{migratorRole}.Sanitize())
		_, _ = admin.Exec(context.Background(), `DROP ROLE IF EXISTS `+pgx.Identifier{ownerRole}.Sanitize())
		_, _ = admin.Exec(context.Background(), `DROP ROLE IF EXISTS `+pgx.Identifier{migratorRole}.Sanitize())
		admin.Close(context.Background())
	}()

	if _, err := admin.Exec(ctx, `CREATE ROLE `+pgx.Identifier{migratorRole}.Sanitize()+` LOGIN PASSWORD '`+migratorPassword+`' NOSUPERUSER NOBYPASSRLS NOCREATEDB CREATEROLE`); err != nil {
		t.Fatalf("create test-local non-superuser migrator: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatalf("create isolated migration schema: %v", err)
	}
	if _, err := admin.Exec(ctx, `GRANT USAGE,CREATE ON SCHEMA `+pgx.Identifier{schema}.Sanitize()+` TO `+pgx.Identifier{migratorRole}.Sanitize()); err != nil {
		t.Fatalf("grant temporary migration schema privileges: %v", err)
	}
	migrationPoolConfig, err := parsePoolConfig(testDSN, defaultMigratorPoolMaxConns)
	if err != nil {
		t.Fatal(err)
	}
	migrationPoolConfig.ConnConfig.User = migratorRole
	migrationPoolConfig.ConnConfig.Password = migratorPassword
	migrationPoolConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SELECT set_config('search_path',$1,false)`, schema)
		return err
	}
	migrator, err := pgxpool.NewWithConfig(ctx, migrationPoolConfig)
	if err != nil {
		t.Fatalf("open isolated non-superuser migration pool: %v", err)
	}
	defer migrator.Close()
	var sessionUser, currentUser string
	var superuser, bypassRLS, createDB, createRole, replication bool
	if err := migrator.QueryRow(ctx, `SELECT session_user,current_user,rolsuper,rolbypassrls,rolcreatedb,rolcreaterole,rolreplication
		FROM pg_roles WHERE rolname=current_user`).Scan(&sessionUser, &currentUser, &superuser, &bypassRLS, &createDB, &createRole, &replication); err != nil {
		t.Fatal(err)
	}
	if sessionUser != migratorRole || currentUser != migratorRole || superuser || bypassRLS || createDB || !createRole || replication {
		t.Fatalf("migration did not use the expected bounded non-superuser session identity: session=%q current=%q superuser=%v bypassrls=%v createdb=%v createrole=%v replication=%v", sessionUser, currentUser, superuser, bypassRLS, createDB, createRole, replication)
	}
	var inheritedAdmin bool
	if err := migrator.QueryRow(ctx, `SELECT pg_has_role(current_user,$1,'MEMBER')`, testConfig.User).Scan(&inheritedAdmin); err != nil || inheritedAdmin {
		t.Fatalf("isolated migrator unexpectedly inherits test administrator role: member=%v err=%v", inheritedAdmin, err)
	}
	if _, err := migrator.Exec(ctx, `SET ROLE `+pgx.Identifier{testConfig.User}.Sanitize()); err == nil {
		t.Fatal("isolated migrator unexpectedly assumed the test administrator role")
	}
	if err := migrate(ctx, migrator, schema); err != nil {
		t.Fatalf("first schema install with a non-superuser migrator: %v", err)
	}
	if err := migrate(ctx, migrator, schema); err != nil {
		t.Fatalf("schema migration rerun with the bounded owner: %v", err)
	}

	var roleSafe, ownerHasNoCreate, schemaUsage, bindingOK bool
	var members, ownedFunctions, ownedRelations, publicExec int
	if err := migrator.QueryRow(ctx, `SELECT current_user`).Scan(&currentUser); err != nil {
		t.Fatal(err)
	}
	if err := migrator.QueryRow(ctx, `SELECT NOT rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb
		AND NOT rolcreaterole AND NOT rolinherit AND NOT rolreplication FROM pg_roles WHERE rolname=$1`, ownerRole).Scan(&roleSafe); err != nil || !roleSafe {
		t.Fatalf("maintenance role attributes are unsafe: safe=%v err=%v", roleSafe, err)
	}
	if err := migrator.QueryRow(ctx, `SELECT shobj_description(oid,'pg_authid')=$2 FROM pg_roles WHERE rolname=$1`, ownerRole, clmMaintenanceRoleBinding(testConfig.Database, schema)).Scan(&bindingOK); err != nil || !bindingOK {
		t.Fatalf("maintenance role is not bound to database/schema: ok=%v err=%v", bindingOK, err)
	}
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM pg_auth_members m JOIN pg_roles target ON target.oid=m.roleid WHERE target.rolname=$1`, ownerRole).Scan(&members); err != nil || members != 1 {
		t.Fatalf("expected exactly one migration-only role member: count=%d err=%v", members, err)
	}
	var memberIsMigrator bool
	if err := migrator.QueryRow(ctx, `SELECT bool_and(member.rolname=$2 AND m.admin_option=false AND m.inherit_option=false AND m.set_option=true)
		FROM pg_auth_members m JOIN pg_roles target ON target.oid=m.roleid JOIN pg_roles member ON member.oid=m.member
		WHERE target.rolname=$1`, ownerRole, currentUser).Scan(&memberIsMigrator); err != nil || !memberIsMigrator {
		t.Fatalf("owner membership is not limited SET-only migrator access: ok=%v err=%v", memberIsMigrator, err)
	}
	if err := migrator.QueryRow(ctx, `SELECT has_schema_privilege($1,$2,'CREATE'),has_schema_privilege($1,$2,'USAGE')`, ownerRole, schema).Scan(&ownerHasNoCreate, &schemaUsage); err != nil || ownerHasNoCreate || !schemaUsage {
		t.Fatalf("schema privileges not reduced after owner transfer: create=%v usage=%v err=%v", ownerHasNoCreate, schemaUsage, err)
	}
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
		WHERE n.nspname=$1 AND p.proname IN ('clm_outbox_claim','clm_outbox_ack','clm_outbox_release','clm_metrics_snapshot') AND p.proowner=(SELECT oid FROM pg_roles WHERE rolname=$2)`, schema, ownerRole).Scan(&ownedFunctions); err != nil || ownedFunctions != 4 {
		t.Fatalf("maintenance owner does not own only the four CLM functions: count=%d err=%v", ownedFunctions, err)
	}
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relowner=(SELECT oid FROM pg_roles WHERE rolname=$2)`, schema, ownerRole).Scan(&ownedRelations); err != nil || ownedRelations != 0 {
		t.Fatalf("maintenance owner unexpectedly owns a relation: count=%d err=%v", ownedRelations, err)
	}
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
		CROSS JOIN LATERAL aclexplode(p.proacl) acl WHERE n.nspname=$1 AND p.proname IN
		('clm_outbox_claim','clm_outbox_ack','clm_outbox_release','clm_metrics_snapshot') AND acl.grantee=0 AND acl.privilege_type='EXECUTE'`, schema).Scan(&publicExec); err != nil || publicExec != 0 {
		t.Fatalf("CLM maintenance functions remain executable by PUBLIC: count=%d err=%v", publicExec, err)
	}

	// An unrelated table-level SELECT grant is a security regression even
	// though the grantee has the expected role name and owner attributes.
	if _, err := migrator.Exec(ctx, `GRANT SELECT ON TABLE `+pgx.Identifier{schema, "clm_contracts"}.Sanitize()+` TO `+pgx.Identifier{ownerRole}.Sanitize()); err != nil {
		t.Fatalf("inject unexpected owner grant: %v", err)
	}
	if err := migrate(ctx, migrator, schema); err == nil {
		t.Fatal("migration rerun accepted an unexpected sensitive table grant")
	}
	if _, err := migrator.Exec(ctx, `REVOKE SELECT ON TABLE `+pgx.Identifier{schema, "clm_contracts"}.Sanitize()+` FROM `+pgx.Identifier{ownerRole}.Sanitize()); err != nil {
		t.Fatalf("remove injected owner grant: %v", err)
	}
	if err := migrate(ctx, migrator, schema); err != nil {
		t.Fatalf("migration rerun should pass after removing the unexpected grant: %v", err)
	}
}

// TestCLMOutboxMaintenanceRoleClaimLeaseAndMetrics exercises cross-tenant
// maintenance only through the normal Core credential and fixed function API.
func TestCLMOutboxMaintenanceRoleClaimLeaseAndMetrics(t *testing.T) {
	fixture, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for CLM outbox role checks")
	}
	ctx := context.Background()
	core, admin := fixture.core, fixture.admin
	contractID, revisionID, err := seedCLMMetricContract(ctx, core, admin)
	if err != nil {
		t.Fatal(err)
	}
	if err := seedCLMMetricRows(ctx, core, admin, contractID, revisionID); err != nil {
		t.Fatal(err)
	}

	if err := core.RefreshCLMMetrics(ctx, nil); err != nil {
		t.Fatal(err)
	}
	snapshot := core.CLMMetricsSnapshot()
	if !snapshot.Available || snapshot.Pending != 3 || !snapshot.PendingAgeAvailable {
		t.Fatalf("pending snapshot is incomplete: %+v", snapshot)
	}
	if snapshot.Approved.Count != 1 || !snapshot.Approved.DurationAvailable || snapshot.Approved.P50MS <= 0 || snapshot.Approved.P95MS <= 0 {
		t.Fatalf("valid approved round duration was not measured: %+v", snapshot.Approved)
	}
	if snapshot.Returned.Count != 1 || snapshot.Returned.DurationAvailable || snapshot.Terminated.Count != 1 || snapshot.Terminated.DurationAvailable {
		t.Fatalf("bad/future timestamps must make only their duration percentiles unavailable: returned=%+v terminated=%+v", snapshot.Returned, snapshot.Terminated)
	}
	if snapshot.Withdrawn.Count != 0 || snapshot.Withdrawn.DurationAvailable || snapshot.AgedInReviewAvailable {
		t.Fatalf("empty outcome or missing threshold was reported as a fabricated value: %+v", snapshot)
	}
	threshold := 30 * time.Minute
	if err := core.RefreshCLMMetrics(ctx, &threshold); err != nil {
		t.Fatal(err)
	}
	if snapshot = core.CLMMetricsSnapshot(); !snapshot.AgedInReviewAvailable || snapshot.AgedInReview != 1 {
		t.Fatalf("configured threshold did not count the valid aged-in-review proxy: %+v", snapshot)
	}

	// An invalid in-review start makes only that proxy unknown. A pending event
	// with an infinite timestamp likewise preserves the pending count while
	// marking its age unavailable; neither may break ordinary metric samples.
	invalidContract, invalidRevision, err := seedCLMMetricContract(ctx, core, admin)
	if err != nil {
		t.Fatal(err)
	}
	if err := seedInvalidCLMMetricRows(ctx, core, admin, invalidContract, invalidRevision); err != nil {
		t.Fatal(err)
	}
	if err := core.RefreshCLMMetrics(ctx, &threshold); err != nil {
		t.Fatal(err)
	}
	snapshot = core.CLMMetricsSnapshot()
	if !snapshot.Available || snapshot.Pending != 4 || snapshot.PendingAgeAvailable || snapshot.AgedInReviewAvailable {
		t.Fatalf("invalid timestamps did not isolate their own availability: %+v", snapshot)
	}
	if snapshot.Approved.Count != 1 || !snapshot.Approved.DurationAvailable {
		t.Fatalf("invalid timestamps hid an independent valid outcome: %+v", snapshot.Approved)
	}

	// Core's direct table grant remains append-only for delivery state, while
	// another runtime role cannot invoke the cross-tenant function.
	tx, err := core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctx, tx, userScope(admin)); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	_, directUpdateErr := tx.Exec(ctx, `UPDATE clm_outbox_delivery SET attempt_count=attempt_count WHERE tenant_id=$1`, admin.TenantID)
	_ = tx.Rollback(ctx)
	if !isPostgresError(directUpdateErr, "42501", "permission denied") {
		t.Fatalf("Core still has direct outbox delivery mutation: %v", directUpdateErr)
	}
	execution := fixture.environment.openRole(t, "execution")
	if _, err := execution.ClaimCLMOutbox(ctx, 1, time.Second); err == nil {
		t.Fatal("Execution could invoke the Core-only CLM claim function")
	}

	// Two concurrent Core replicas receive disjoint durable claims.
	start := make(chan struct{})
	type claimResult struct {
		claims []CLMOutboxClaim
		err    error
	}
	results := make(chan claimResult, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claims, err := core.ClaimCLMOutbox(ctx, 1, time.Second)
			results <- claimResult{claims: claims, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var first, second CLMOutboxClaim
	for result := range results {
		if result.err != nil || len(result.claims) != 1 {
			t.Fatalf("concurrent Core claim failed: claims=%+v err=%v", result.claims, result.err)
		}
		if first.EventID == "" {
			first = result.claims[0]
		} else {
			second = result.claims[0]
		}
	}
	if first.EventID == second.EventID || first.MessageID == second.MessageID {
		t.Fatalf("concurrent claims overlapped: first=%+v second=%+v", first, second)
	}
	for _, claim := range []CLMOutboxClaim{first, second} {
		wantMessageID := hex.EncodeToString([]byte(claim.TenantID)) + ":" + claim.EventID
		if claim.MessageID != wantMessageID {
			t.Fatalf("message identity is not the stable full tenant/event identity: got=%q want=%q", claim.MessageID, wantMessageID)
		}
	}
	if err := core.AckCLMOutbox(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := core.AckCLMOutbox(ctx, first); err != nil {
		t.Fatalf("same-token ACK is not idempotent: %v", err)
	}
	if err := core.ReleaseCLMOutbox(ctx, second, "broker_unavailable", 0); err != nil {
		t.Fatal(err)
	}
	redeliveries, err := core.ClaimCLMOutbox(ctx, 4, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var redelivered *CLMOutboxClaim
	for i := range redeliveries {
		if redeliveries[i].EventID == second.EventID {
			redelivered = &redeliveries[i]
			break
		}
	}
	if redelivered == nil || redelivered.Token != second.Token+1 || redelivered.MessageID != second.MessageID {
		t.Fatalf("release did not preserve stable message identity and advance token: claims=%+v old=%+v", redeliveries, second)
	}
	if err := core.AckCLMOutbox(ctx, second); !errors.Is(err, ErrCLMOutboxClaimLost) {
		t.Fatalf("released old claim could ACK: %v", err)
	}
	if err := core.AckCLMOutbox(ctx, *redelivered); err != nil {
		t.Fatal(err)
	}

	// C and D remain held until expiry. After the DB clock lease expires, the
	// takeover advances tokens and rejects old ACKs.
	var remaining []CLMOutboxClaim
	for _, claim := range redeliveries {
		if claim.EventID != second.EventID {
			remaining = append(remaining, claim)
		}
	}
	if len(remaining) == 0 {
		t.Fatal("expected at least one held claim for lease expiry")
	}
	time.Sleep(1100 * time.Millisecond)
	takeovers, err := core.ClaimCLMOutbox(ctx, 4, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range remaining {
		var next *CLMOutboxClaim
		for i := range takeovers {
			if takeovers[i].EventID == old.EventID {
				next = &takeovers[i]
				break
			}
		}
		if next == nil || next.Token != old.Token+1 {
			t.Fatalf("expired claim was not fenced by a later token: old=%+v new=%+v", old, takeovers)
		}
		if err := core.AckCLMOutbox(ctx, old); !errors.Is(err, ErrCLMOutboxClaimLost) {
			t.Fatalf("expired claim could ACK: %v", err)
		}
		if err := core.AckCLMOutbox(ctx, *next); err != nil {
			t.Fatal(err)
		}
	}
	if err := core.RefreshCLMMetrics(ctx, &threshold); err != nil {
		t.Fatal(err)
	}
	if snapshot = core.CLMMetricsSnapshot(); !snapshot.Available || snapshot.Pending != 0 || snapshot.PendingAgeAvailable {
		t.Fatalf("empty outbox is not represented as available count zero/unknown age: %+v", snapshot)
	}
}

func seedCLMMetricContract(ctx context.Context, core *Store, admin accounts.User) (string, string, error) {
	contractID, revisionID := uuid.NewString(), uuid.NewString()
	tx, err := core.pool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(admin)); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_contracts(tenant_id,contract_id,owner_org_id,authority_mode,lifecycle_state,created_by)
		VALUES($1,$2,$3,'native','draft',$4)`, admin.TenantID, contractID, admin.OrgID, admin.ID); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_revisions(tenant_id,contract_id,contract_revision_id,revision_number,amount,currency,snapshot,snapshot_digest,frozen_by)
		VALUES($1,$2,$3,1,100,'CNY','{}',$4,$5)`, admin.TenantID, contractID, revisionID, strings.Repeat("a", 64), admin.ID); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE clm_contracts SET current_revision_id=$3 WHERE tenant_id=$1 AND contract_id=$2`, admin.TenantID, contractID, revisionID); err != nil {
		return "", "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return contractID, revisionID, nil
}

func seedCLMMetricRows(ctx context.Context, core *Store, admin accounts.User, contractID, revisionID string) error {
	tx, err := core.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(admin)); err != nil {
		return err
	}
	now := time.Now().UTC()
	for index, outcome := range []struct {
		state string
		start time.Time
		end   time.Time
	}{
		{"approved", now.Add(-5 * time.Minute), now.Add(-4 * time.Minute)},
		{"returned", now.Add(-2 * time.Second), now.Add(-4 * time.Second)},
		{"terminated", now.Add(-time.Minute), now.Add(time.Minute)},
	} {
		if _, err := tx.Exec(ctx, `INSERT INTO clm_approval_rounds(tenant_id,contract_id,approval_round_id,contract_revision_id,round_number,state,workflow_template_id,workflow_template_version,routing_snapshot,risk_disposition_snapshot,initiated_by,started_at,ended_at)
			VALUES($1,$2,$3,$4,$5,$6,'metrics-test','1','{}','{}',$7,$8,$9)`, admin.TenantID, contractID, uuid.NewString(), revisionID, index+1, outcome.state, admin.ID, outcome.start, outcome.end); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_approval_rounds(tenant_id,contract_id,approval_round_id,contract_revision_id,round_number,state,workflow_template_id,workflow_template_version,routing_snapshot,risk_disposition_snapshot,initiated_by,started_at)
		VALUES($1,$2,$3,$4,4,'in_review','metrics-test','1','{}','{}',$5,$6)`, admin.TenantID, contractID, uuid.NewString(), revisionID, admin.ID, now.Add(-time.Hour)); err != nil {
		return err
	}
	for index := 1; index <= 3; index++ {
		eventID := fmt.Sprintf("00000000-0000-4000-8000-%012d", index)
		if _, err := tx.Exec(ctx, `INSERT INTO clm_business_events(tenant_id,event_id,contract_id,aggregate_version,event_type,schema_version,payload)
			VALUES($1,$2,$3,$4,'contract.metrics_test',1,'{}')`, admin.TenantID, eventID, contractID, index); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO clm_outbox_delivery(tenant_id,event_id) VALUES($1,$2)`, admin.TenantID, eventID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func seedInvalidCLMMetricRows(ctx context.Context, core *Store, admin accounts.User, contractID, revisionID string) error {
	tx, err := core.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(admin)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_approval_rounds(tenant_id,contract_id,approval_round_id,contract_revision_id,round_number,state,workflow_template_id,workflow_template_version,routing_snapshot,risk_disposition_snapshot,initiated_by,started_at)
		VALUES($1,$2,$3,$4,1,'in_review','metrics-test','1','{}','{}',$5,'infinity')`, admin.TenantID, contractID, uuid.NewString(), revisionID, admin.ID); err != nil {
		return err
	}
	eventID := "00000000-0000-4000-8000-000000000004"
	if _, err := tx.Exec(ctx, `INSERT INTO clm_business_events(tenant_id,event_id,contract_id,aggregate_version,event_type,schema_version,payload,occurred_at)
		VALUES($1,$2,$3,4,'contract.metrics_test',1,'{}','infinity')`, admin.TenantID, eventID, contractID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_outbox_delivery(tenant_id,event_id) VALUES($1,$2)`, admin.TenantID, eventID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
