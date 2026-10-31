package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/accounts"
)

// TestSessionNarrowWritesKeepAuthorityAndRevocation pins the session contract
// against the durable Core service role.
func TestSessionNarrowWritesKeepAuthorityAndRevocation(t *testing.T) {
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	st := environment.openRole(t, "core")
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo admin missing")
	}
	first, err := st.EstablishSession(admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.EstablishSession(admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two sessions shared one token")
	}
	for _, token := range []string{first, second} {
		user, _, err := st.AuthenticateSession(context.Background(), token)
		if err != nil || user.ID != admin.ID {
			t.Fatalf("session did not resolve its actor: user=%+v err=%v", user, err)
		}
	}
	// Revoking one token must not touch the other, and must be idempotent.
	if err := st.Logout(first); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AuthenticateSession(context.Background(), first); err == nil {
		t.Fatal("a revoked session still resolved")
	}
	if _, _, err := st.AuthenticateSession(context.Background(), second); err != nil {
		t.Fatalf("revoking one session revoked another: %v", err)
	}
	if err := st.Logout(first); err != nil {
		t.Fatalf("logout was not idempotent: %v", err)
	}
	// A whole-state write after the narrow writes must not delete the live
	// session: the in-memory projection has to know about it.
	if _, err := st.CreateCase(admin, "会话投影回归", "procurement_ict"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AuthenticateSession(context.Background(), second); err != nil {
		t.Fatalf("a whole-state write deleted a live session: %v", err)
	}
	// A foreign token cannot revoke a session it does not hold.
	stranger, err := st.EstablishSession(admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Logout("s_not_a_real_token"); err != nil {
		t.Fatalf("revoking an unknown token must be a no-op: %v", err)
	}
	if _, _, err := st.AuthenticateSession(context.Background(), stranger); err != nil {
		t.Fatalf("an unknown token revoked a live session: %v", err)
	}
	if _, _, err := st.AuthenticateSession(context.Background(), second); err != nil {
		t.Fatalf("an unknown token revoked a live session: %v", err)
	}
	// The narrow path keeps the actor identity exact.
	user, ok := accounts.ByID(admin.ID)
	if !ok {
		t.Fatal("demo admin missing")
	}
	other, err := st.EstablishSession(user, "invalid-audience")
	if err == nil {
		t.Fatalf("an unsupported audience was accepted: %q", other)
	}
	adminSession, err := st.EstablishSession(user, "admin")
	if err != nil {
		t.Fatalf("the trusted admin identity could not open an admin session: %v", err)
	}
	if audience, err := st.SessionAudience(adminSession); err != nil || audience != "admin" {
		t.Fatalf("admin session audience was not retained: %q err=%v", audience, err)
	}
}

// TestConcurrentLogoutCannotBeResurrectedByCaseWrite uses two Core stores on
// one database: one revokes a session while the other commits a generic case
// state write from a process opened before that session existed.
func TestConcurrentLogoutCannotBeResurrectedByCaseWrite(t *testing.T) {
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	revoker := environment.openRole(t, "core")
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo admin missing")
	}
	caseID, err := revoker.CreateCase(admin, "并发会话回归", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	writer := environment.openRole(t, "core")
	token, err := revoker.EstablishSession(admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := writer.AuthenticateSession(context.Background(), token); err != nil {
		t.Fatalf("the second store did not see the session: %v", err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var logoutErr, writeErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		logoutErr = revoker.Logout(token)
	}()
	go func() {
		defer wg.Done()
		<-start
		writeErr = writer.updateAs(admin, func(st *state) error {
			for i := range st.Cases {
				if st.Cases[i].ID == caseID {
					st.Cases[i].Title = "并发会话回归已更新"
					st.Cases[i].UpdatedAt = stamp()
					return nil
				}
			}
			return errors.New("test case is missing from the writer snapshot")
		})
	}()
	close(start)
	wg.Wait()
	if logoutErr != nil {
		t.Fatalf("concurrent logout failed: %v", logoutErr)
	}
	if writeErr != nil {
		t.Fatalf("concurrent generic case write failed: %v", writeErr)
	}
	for name, store := range map[string]*Store{"revoker": revoker, "writer": writer} {
		if _, _, err := store.AuthenticateSession(context.Background(), token); err == nil {
			t.Fatalf("the revoked token reappeared on the %s store", name)
		}
	}
}

func TestSessionDigestsKeepTenantAndAudienceAuthority(t *testing.T) {
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	issuer := environment.openRole(t, "core")
	reader := environment.openRole(t, "core")
	local, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("local administrator missing")
	}
	foreign, found := accounts.ByID("demo-tenant2-admin")
	if !found {
		t.Fatal("second tenant administrator missing")
	}
	type issuedSession struct {
		user     accounts.User
		audience string
		token    string
		digest   string
	}
	var issued []issuedSession
	for _, user := range []accounts.User{local, foreign} {
		for _, audience := range []string{"client", "admin"} {
			before := time.Now().UTC()
			token, err := issuer.EstablishSession(user, audience)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(token, "s_") {
				t.Fatal("session token lacks its expected opaque format")
			}
			entropy, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "s_"))
			if err != nil || len(entropy) != 32 {
				t.Fatalf("session token does not encode 32 random bytes: length=%d err=%v", len(entropy), err)
			}
			expected := sha256.Sum256([]byte(token))
			expectedDigest := "sha256:" + hex.EncodeToString(expected[:])

			tx, err := issuer.pool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := setLocalScope(context.Background(), tx, userScope(user)); err != nil {
				_ = tx.Rollback(context.Background())
				t.Fatal(err)
			}
			var stored, expiresAt, tenant, org string
			err = tx.QueryRow(context.Background(), `SELECT token, expires_at, tenant_id, org_id FROM sessions
				WHERE actor_id=$1 AND audience=$2`, user.ID, audience).Scan(&stored, &expiresAt, &tenant, &org)
			_ = tx.Rollback(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if stored != expectedDigest || stored == token || tenant != user.TenantID || org != user.OrgID {
				t.Fatal("persisted session is not the expected digest and tenant identity")
			}
			expires, err := time.Parse(timeLayout, expiresAt)
			if err != nil || expires.Before(before.Add(7*24*time.Hour-time.Second)) || expires.After(time.Now().UTC().Add(7*24*time.Hour)) {
				t.Fatalf("session expiry is not seven days: expires=%s err=%v", expiresAt, err)
			}
			if current, _, err := reader.AuthenticateSession(context.Background(), token); err != nil || current.ID != user.ID || current.TenantID != user.TenantID {
				t.Fatalf("another process could not resolve the session identity: user=%+v err=%v", current, err)
			}
			if got, err := reader.SessionAudience(token); err != nil || got != audience {
				t.Fatalf("another process returned the wrong session audience: audience=%q err=%v", got, err)
			}
			if _, _, err := reader.AuthenticateSession(context.Background(), stored); err == nil {
				t.Fatal("a leaked database digest authenticated as a bearer token")
			}
			if _, err := reader.SessionAudience(stored); err == nil {
				t.Fatal("a leaked database digest disclosed a session audience")
			}
			if err := reader.Logout(stored); err != nil {
				t.Fatalf("logout with an unknown digest should be idempotent: %v", err)
			}
			if _, _, err := reader.AuthenticateSession(context.Background(), token); err != nil {
				t.Fatalf("the leaked digest revoked its original session: %v", err)
			}
			issued = append(issued, issuedSession{user: user, audience: audience, token: token, digest: stored})
		}
	}

	issuer.mu.Lock()
	for _, projected := range issuer.st.Sessions {
		for _, item := range issued {
			if projected.Token == item.token {
				issuer.mu.Unlock()
				t.Fatal("session projection retained a bearer token")
			}
		}
		if !strings.HasPrefix(projected.Token, "sha256:") {
			issuer.mu.Unlock()
			t.Fatal("session projection retained a non-digest token")
		}
	}
	issuer.mu.Unlock()

	tx, err := issuer.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(context.Background(), tx, userScope(local)); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatal(err)
	}
	var foreignRows int
	err = tx.QueryRow(context.Background(), `SELECT count(*) FROM sessions WHERE token=$1`, issued[2].digest).Scan(&foreignRows)
	_ = tx.Rollback(context.Background())
	if err != nil || foreignRows != 0 {
		t.Fatalf("tenant scope exposed another tenant's session: count=%d err=%v", foreignRows, err)
	}
	caseID, err := issuer.CreateCase(local, "会话租户权限回归", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	foreignUser, _, err := reader.AuthenticateSession(context.Background(), issued[2].token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.GetCase(foreignUser, caseID); err == nil {
		t.Fatal("a second-tenant session accessed another tenant's case")
	}
	if err := reader.Logout(issued[2].token); err != nil {
		t.Fatal(err)
	}
	if _, _, err := issuer.AuthenticateSession(context.Background(), issued[2].token); err == nil {
		t.Fatal("revocation did not take effect in another process")
	}
	for _, item := range []issuedSession{issued[0], issued[1], issued[3]} {
		if _, _, err := issuer.AuthenticateSession(context.Background(), item.token); err != nil {
			t.Fatalf("revocation affected another audience or tenant: %v", err)
		}
	}
}

func TestSessionMigrationRemovesOnlyLegacyTokensAcrossTenants(t *testing.T) {
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	core := environment.openRole(t, "core")
	local, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("local administrator missing")
	}
	foreign, found := accounts.ByID("demo-tenant2-admin")
	if !found {
		t.Fatal("second tenant administrator missing")
	}
	fixtures := []struct {
		user   accounts.User
		legacy string
		decoy  string
		live   string
	}{
		{user: local, legacy: "s0123456789abcdef", decoy: "s0123456789abcdeF"},
		{user: foreign, legacy: "sfedcba9876543210", decoy: "sfedcba98765432100"},
	}
	ctx := context.Background()
	for i := range fixtures {
		fixture := &fixtures[i]
		var err error
		fixture.live, err = core.EstablishSession(fixture.user, "client")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := core.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := setLocalScope(ctx, tx, userScope(fixture.user)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		for _, token := range []string{fixture.legacy, fixture.decoy} {
			if _, err := tx.Exec(ctx, `INSERT INTO sessions (token, actor_id, expires_at, issuer, subject, tenant_id, org_id, audience)
				VALUES ($1,$2,$3,$4,$5,$6,$7,'client')`, token, fixture.user.ID,
				time.Now().UTC().Add(24*time.Hour).Format(timeLayout), fixture.user.Issuer, fixture.user.Subject,
				fixture.user.TenantID, fixture.user.OrgID); err != nil {
				_ = tx.Rollback(ctx)
				t.Fatal(err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if _, _, err := core.AuthenticateSession(context.Background(), fixture.legacy); err == nil {
			t.Fatal("legacy plaintext session authenticated before cleanup")
		}
		if _, err := core.SessionAudience(fixture.legacy); err == nil {
			t.Fatal("legacy plaintext session disclosed its audience")
		}
		if err := core.Logout(fixture.legacy); err != nil {
			t.Fatalf("legacy logout should be idempotent: %v", err)
		}
		snapshot, err := core.snapshotAs(fixture.user)
		if err != nil {
			t.Fatal(err)
		}
		for _, projected := range snapshot.Sessions {
			if projected.Token == fixture.legacy || projected.Token == fixture.decoy {
				t.Fatal("a plaintext session entered the process projection")
			}
		}
	}
	// Fail on the second matching deletion so the first deletion and the
	// temporary NO FORCE setting must both be rolled back.
	if _, err := environment.harness.pool.Exec(ctx, `CREATE FUNCTION session_test_reject_second_delete() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF current_setting('test.session_cleanup_deleted', true) = 'yes' THEN
				RAISE EXCEPTION 'injected session cleanup failure';
			END IF;
			PERFORM set_config('test.session_cleanup_deleted', 'yes', true);
			RETURN OLD;
		END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.harness.pool.Exec(ctx, `CREATE TRIGGER session_test_reject_second_delete BEFORE DELETE ON sessions
		FOR EACH ROW EXECUTE FUNCTION session_test_reject_second_delete()`); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.harness.pool.Exec(ctx, sessionLegacyCleanupSQL); err == nil {
		t.Fatal("session cleanup did not propagate a deletion failure")
	}
	var enabled, forced bool
	if err := environment.harness.pool.QueryRow(ctx, `SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE oid='sessions'::regclass`).Scan(&enabled, &forced); err != nil || !enabled || !forced {
		t.Fatalf("failed cleanup did not roll back row security: enabled=%t forced=%t err=%v", enabled, forced, err)
	}
	for _, fixture := range fixtures {
		tx, err := core.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := setLocalScope(ctx, tx, userScope(fixture.user)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		var legacyCount int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE token=$1`, fixture.legacy).Scan(&legacyCount)
		_ = tx.Rollback(ctx)
		if err != nil || legacyCount != 1 {
			t.Fatalf("failed cleanup did not roll back legacy deletion: count=%d err=%v", legacyCount, err)
		}
	}
	if _, err := environment.harness.pool.Exec(ctx, `DROP TRIGGER session_test_reject_second_delete ON sessions`); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.harness.pool.Exec(ctx, `DROP FUNCTION session_test_reject_second_delete()`); err != nil {
		t.Fatal(err)
	}
	// This fixture models the old, unversioned database. Already-applied
	// numbered migrations must remain no-ops on a current database.
	if _, err := environment.harness.pool.Exec(ctx, `DROP TABLE schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, environment.harness.pool, environment.harness.schema); err != nil {
		t.Fatalf("apply legacy session migration: %v", err)
	}
	if err := migrate(ctx, environment.harness.pool, environment.harness.schema); err != nil {
		t.Fatalf("repeat legacy session migration: %v", err)
	}
	// A successful cleanup also leaves row security enabled and forced.
	if err := environment.harness.pool.QueryRow(ctx, `SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE oid='sessions'::regclass`).Scan(&enabled, &forced); err != nil || !enabled || !forced {
		t.Fatalf("session row security was not restored: enabled=%t forced=%t err=%v", enabled, forced, err)
	}
	for _, fixture := range fixtures {
		tx, err := core.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := setLocalScope(ctx, tx, userScope(fixture.user)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		var legacyCount, decoyCount int
		err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE token=$1), count(*) FILTER (WHERE token=$2)
			FROM sessions`, fixture.legacy, fixture.decoy).Scan(&legacyCount, &decoyCount)
		_ = tx.Rollback(ctx)
		if err != nil || legacyCount != 0 || decoyCount != 1 {
			t.Fatalf("legacy cleanup affected the wrong rows: legacy=%d other=%d err=%v", legacyCount, decoyCount, err)
		}
		if _, _, err := core.AuthenticateSession(context.Background(), fixture.live); err != nil {
			t.Fatalf("legacy cleanup revoked a newly issued session: %v", err)
		}
	}
}
