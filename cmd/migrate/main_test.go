package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestRunPreservesOptionalServiceRoleMigration(t *testing.T) {
	env := newMigrationCommandDatabase(t)

	t.Run("unset setting migrates schema without provisioning roles", func(t *testing.T) {
		t.Setenv("SERVICE_DATABASE_ROLES", "")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := run(ctx); err != nil {
			t.Fatalf("run() with the default role setting = %v", err)
		}
		env.requireRoles(t, false)
	})

	t.Run("false setting migrates schema without provisioning roles", func(t *testing.T) {
		t.Setenv("SERVICE_DATABASE_ROLES", "false")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := run(ctx); err != nil {
			t.Fatalf("run() with SERVICE_DATABASE_ROLES=false = %v", err)
		}
		env.requireRoles(t, false)
	})

	t.Run("true setting provisions the dedicated service roles", func(t *testing.T) {
		t.Setenv("SERVICE_DATABASE_ROLES", "true")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := run(ctx); err != nil {
			t.Fatalf("run() with SERVICE_DATABASE_ROLES=true = %v", err)
		}
		env.requireRoles(t, true)
	})

	t.Run("other settings keep the migration error", func(t *testing.T) {
		t.Setenv("SERVICE_DATABASE_ROLES", "enabled")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := run(ctx); err == nil || err.Error() != "SERVICE_DATABASE_ROLES must be true or false" {
			t.Fatalf("run() with invalid role setting = %v, want exact validation error", err)
		}
	})

	t.Run("canceled migration context is returned", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := run(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("run() with canceled context = %v, want context.Canceled", err)
		}
	})
}

func TestMigrateMainReportsStartupFailureAndExits(t *testing.T) {
	if os.Getenv("CAW_MIGRATE_MAIN_CHILD") == "1" {
		main()
		t.Fatal("main returned after migration startup failure")
	}

	command := exec.Command(os.Args[0], "-test.run=^TestMigrateMainReportsStartupFailureAndExits$")
	command.Env = replaceMigrationTestEnvironment(os.Environ(), map[string]string{
		"APP_DATABASE_MODE":      "application",
		"APP_ENV":                "test",
		"CAW_MIGRATE_MAIN_CHILD": "1",
		"MIGRATOR_DATABASE_URL":  "",
		"TEST_DATABASE_URL":      "postgres://migration-test:unused@127.0.0.1:5432/contract_audit_test?sslmode=disable",
	})
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("migrate main exit = %v, output=%s; want exit 1", err, output)
	}
	if !strings.Contains(string(output), "database migration failed: MIGRATOR_DATABASE_URL is required for migrations") {
		t.Fatalf("migrate main output lost the startup failure: %s", output)
	}
}

type migrationCommandDatabase struct {
	db     *pgx.Conn
	schema string
	roles  []string
}

func newMigrationCommandDatabase(t *testing.T) *migrationCommandDatabase {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	migratorDSN := strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL"))
	if baseDSN == "" || migratorDSN == "" {
		t.Fatal("migration command characterization requires TEST_DATABASE_URL and MIGRATOR_DATABASE_URL")
	}

	var nonce [10]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("create isolated migration database identity")
	}
	suffix := hex.EncodeToString(nonce[:])
	env := &migrationCommandDatabase{schema: "it_cmd_migrate_" + suffix}
	setupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := pgx.Connect(setupCtx, migratorDSN)
	if err != nil {
		t.Fatal("connect migration test database")
	}
	env.db = db

	t.Setenv("APP_DATABASE_MODE", "application")
	t.Setenv("APP_ENV", "test")
	t.Setenv("DATABASE_SCHEMA", env.schema)
	t.Setenv("MIGRATOR_DATABASE_URL", migratorDSN)
	t.Setenv("TEST_DATABASE_URL", baseDSN)
	t.Setenv("SERVICE_DATABASE_ROLES", "")
	for _, service := range []string{"core", "document", "execution", "model", "tool"} {
		role := "it_cmd_migrate_" + service + "_" + suffix
		parsed, err := url.Parse(baseDSN)
		if err != nil {
			t.Fatal("parse migration test database URL")
		}
		parsed.User = url.UserPassword(role, strings.Repeat(suffix, 4))
		t.Setenv(strings.ToUpper(service)+"_DATABASE_URL", parsed.String())
		env.roles = append(env.roles, role)
	}
	t.Cleanup(func() { env.close(t) })
	return env
}

func (env *migrationCommandDatabase) requireRoles(t *testing.T, present bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, role := range env.roles {
		var exists bool
		if err := env.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, role).Scan(&exists); err != nil {
			t.Fatalf("read isolated migration role state (%T)", err)
		}
		if exists != present {
			t.Fatalf("service role %q exists=%t, want %t", role, exists, present)
		}
	}
}

func (env *migrationCommandDatabase) close(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if env.db == nil {
		return
	}
	if _, err := env.db.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{env.schema}.Sanitize()+" CASCADE"); err != nil {
		t.Error("drop isolated migration test schema")
	}
	for _, role := range env.roles {
		if _, err := env.db.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Error("drop isolated migration test role")
		}
	}
	if err := env.db.Close(ctx); err != nil {
		t.Error("close migration test database connection")
	}
}

func replaceMigrationTestEnvironment(environment []string, replacements map[string]string) []string {
	filtered := make([]string, 0, len(environment)+len(replacements))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := replacements[name]; !replaced {
			filtered = append(filtered, entry)
		}
	}
	for name, value := range replacements {
		filtered = append(filtered, name+"="+value)
	}
	return filtered
}
