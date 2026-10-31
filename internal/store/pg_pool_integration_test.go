package store

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type timeoutSettingsQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func poolTestDSN(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL pool integration checks")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("TEST_DATABASE_URL must be a PostgreSQL URL")
	}
	query := u.Query()
	for key := range query {
		if strings.HasPrefix(key, "pool_") || key == "options" || key == "statement_timeout" || key == "lock_timeout" || key == "idle_in_transaction_session_timeout" {
			query.Del(key)
		}
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func poolTestDSNWith(t *testing.T, raw string, settings map[string]string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal("invalid test database URL")
	}
	query := u.Query()
	for key, value := range settings {
		query.Set(key, value)
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func readPoolTimeouts(t *testing.T, ctx context.Context, queryer timeoutSettingsQueryer) [3]int64 {
	t.Helper()
	var result [3]int64
	err := queryer.QueryRow(ctx, `SELECT
		(SELECT setting::bigint FROM pg_settings WHERE name='statement_timeout'),
		(SELECT setting::bigint FROM pg_settings WHERE name='lock_timeout'),
		(SELECT setting::bigint FROM pg_settings WHERE name='idle_in_transaction_session_timeout')`).Scan(&result[0], &result[1], &result[2])
	if err != nil {
		t.Fatalf("read session timeout settings: %v", err)
	}
	return result
}

func TestRuntimePoolTimeoutsAndTransactionScope(t *testing.T) {
	dsn := poolTestDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := newPool(ctx, dsn, "public")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	first, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	for _, conn := range []timeoutSettingsQueryer{first, second} {
		if got := readPoolTimeouts(t, ctx, conn); got != [3]int64{60000, 10000, 300000} {
			t.Fatalf("runtime timeout settings (ms) = %v", got)
		}
	}
	assertScope := func(queryer timeoutSettingsQueryer, want [4]string) {
		t.Helper()
		var got [4]string
		err := queryer.QueryRow(ctx, `SELECT current_setting('app.tenant_id'), current_setting('app.org_id'),
			current_setting('app.actor_id'), current_setting('app.actor_role')`).Scan(&got[0], &got[1], &got[2], &got[3])
		if err != nil || got != want {
			t.Fatalf("session scope = %v, want %v: %v", got, want, err)
		}
	}
	baseline := [4]string{"local-demo", "demo-org", "system", "api"}
	assertScope(first, baseline)
	tx, err := first.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scope := dbScope{TenantID: "other-tenant", OrgID: "other-org", ActorID: "other-actor", Role: "worker"}
	if err := setLocalScope(ctx, tx, scope); err != nil {
		t.Fatal(err)
	}
	assertScope(tx, [4]string{scope.TenantID, scope.OrgID, scope.ActorID, scope.Role})
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	assertScope(first, baseline)
	assertScope(second, baseline)
}

func TestRuntimePoolExplicitTimeoutPrecedence(t *testing.T) {
	dsn := poolTestDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, tt := range []struct {
		name     string
		settings map[string]string
		want     [3]int64
	}{
		{
			name:     "direct startup parameters",
			settings: map[string]string{"statement_timeout": "2s", "lock_timeout": "0", "idle_in_transaction_session_timeout": "750ms"},
			want:     [3]int64{2000, 0, 750},
		},
		{
			name:     "options and direct parameter",
			settings: map[string]string{"options": "-c statement_timeout=1500ms --lock_timeout=1250ms", "idle_in_transaction_session_timeout": "0"},
			want:     [3]int64{1500, 1250, 0},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := newPool(ctx, poolTestDSNWith(t, dsn, tt.settings), "public")
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			if got := readPoolTimeouts(t, ctx, pool); got != tt.want {
				t.Fatalf("runtime timeout settings (ms) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRuntimePoolTimeoutsCancelStatementsLocksAndIdleTransactions(t *testing.T) {
	baseDSN := poolTestDSN(t)
	dsn := poolTestDSNWith(t, baseDSN, map[string]string{
		"pool_max_conns": "2", "statement_timeout": "200ms", "lock_timeout": "75ms", "idle_in_transaction_session_timeout": "30s",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := newPool(ctx, dsn, "public")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `SELECT pg_sleep(0.5)`); err == nil {
		t.Fatal("statement timeout did not cancel the query")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "57014" {
			t.Fatalf("statement cancellation error: %v", err)
		}
	}
	lockName, err := newID("it_pool_timeout_")
	if err != nil {
		t.Fatal(err)
	}
	first, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(ctx)
	if _, err := first.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockName); err != nil {
		t.Fatal(err)
	}
	second, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Rollback(ctx)
	if _, err := second.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockName); err == nil {
		t.Fatal("lock timeout did not cancel the blocked acquisition")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
			t.Fatalf("lock cancellation error: %v", err)
		}
	}
	if err := second.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := first.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	idleDSN := poolTestDSNWith(t, baseDSN, map[string]string{"idle_in_transaction_session_timeout": "500ms"})
	idlePool, err := newPool(ctx, idleDSN, "public")
	if err != nil {
		t.Fatal(err)
	}
	defer idlePool.Close()
	idle, err := idlePool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Rollback(ctx)
	time.Sleep(time.Second)
	if _, err := idle.Exec(ctx, `SELECT 1`); err == nil {
		t.Fatal("idle-in-transaction timeout did not close the connection")
	}
}

func TestMigrationPoolRetainsItsOwnTimeoutSettings(t *testing.T) {
	dsn := poolTestDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	baseline, err := connectPoolDSN(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer baseline.Close(ctx)
	want := readPoolTimeouts(t, ctx, baseline)
	migrationPool, err := newStorePool(ctx, dsn, "public", false)
	if err != nil {
		t.Fatal(err)
	}
	defer migrationPool.Close()
	if got := readPoolTimeouts(t, ctx, migrationPool); got != want {
		t.Fatalf("migration timeout settings (ms) = %v, database defaults = %v", got, want)
	}
	migrationDSN := poolTestDSNWith(t, dsn, map[string]string{
		"statement_timeout": "4min", "lock_timeout": "90s", "idle_in_transaction_session_timeout": "6min",
	})
	explicitPool, err := newStorePool(ctx, migrationDSN, "public", false)
	if err != nil {
		t.Fatal(err)
	}
	defer explicitPool.Close()
	if got := readPoolTimeouts(t, ctx, explicitPool); got != [3]int64{240000, 90000, 360000} {
		t.Fatalf("explicit migration timeout settings (ms) = %v", got)
	}
}

func TestDirectConnectionsAcceptPoolDSNSettings(t *testing.T) {
	dsn := poolTestDSNWith(t, poolTestDSN(t), map[string]string{
		"pool_max_conns": "2", "pool_min_conns": "1", "pool_max_conn_lifetime": "2h",
		"statement_timeout": "3s",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := connectPoolDSN(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if got := readPoolTimeouts(t, ctx, conn); got[0] != 3000 {
		t.Fatalf("direct connection discarded statement timeout: %v", got)
	}
	schema, err := newID("it_pool_")
	if err != nil {
		t.Fatal(err)
	}
	if err := createSchema(ctx, dsn, schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelCleanup()
		conn, err := connectPoolDSN(cleanup, dsn)
		if err != nil {
			t.Errorf("connect to drop isolated schema: %v", err)
			return
		}
		defer conn.Close(cleanup)
		if _, err := conn.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
	})
	var found bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, schema).Scan(&found); err != nil || !found {
		t.Fatalf("isolated schema not created: %v", err)
	}
}
