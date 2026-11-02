package store

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestOfflineMigratorFreshAndControlledLegacyBootstrap(t *testing.T) {
	adminDSN := os.Getenv("TEST_DATABASE_URL")
	if adminDSN == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "controlled_legacy"}[legacy], func(t *testing.T) {
			admin, err := OpenTest(t.TempDir(), adminDSN)
			if err != nil {
				t.Fatal(err)
			}

			schema := admin.schema
			// Fresh means the admin has created no application object in this namespace.
			if !legacy {
				if _, err = admin.pool.Exec(ctx, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE; CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
					t.Fatal(err)
				}
			}
			suffix := strings.TrimPrefix(schema, "test_")
			if len(suffix) > 18 {
				suffix = suffix[len(suffix)-18:]
			}
			migratorName := "offline_" + suffix
			password := strings.Repeat("x", 40) + suffix
			roleDSN := func(name string) string {
				u, e := url.Parse(adminDSN)
				if e != nil {
					t.Fatal(e)
				}
				u.User = url.UserPassword(name, password)
				return u.String()
			}
			defer func() {
				_ = admin.Close()
				cleanup, e := connectPoolDSN(context.Background(), adminDSN)
				if e != nil {
					return
				}
				defer cleanup.Close(context.Background())
				for _, name := range []string{migratorName, "off_core_" + suffix, "off_document_" + suffix, "off_execution_" + suffix, "off_model_" + suffix, "off_tool_" + suffix} {
					var exists bool
					_ = cleanup.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, name).Scan(&exists)
					if exists {
						_, _ = cleanup.Exec(context.Background(), `DROP OWNED BY `+pgx.Identifier{name}.Sanitize()+`; DROP ROLE `+pgx.Identifier{name}.Sanitize())
					}
				}
			}()
			migratorDSN := roleDSN(migratorName)
			t.Setenv("APP_ENV", "test")
			t.Setenv("DATABASE_SCHEMA", schema)
			t.Setenv("MIGRATOR_DATABASE_URL", migratorDSN)
			t.Setenv("AUTH_ADMIN_DATABASE_URL", adminDSN)
			for _, svc := range []string{"core", "document", "execution", "model", "tool"} {
				t.Setenv(strings.ToUpper(svc)+"_DATABASE_URL", roleDSN("off_"+svc+"_"+suffix))
			}
			if legacy {
				t.Setenv("MIGRATOR_DATABASE_URL", adminDSN)
				if err = MigrateServiceRoles(ctx); err != nil {
					t.Fatalf("legacy initial service roles: %v", err)
				}
				t.Setenv("MIGRATOR_DATABASE_URL", migratorDSN)
			}
			for round := 0; round < 2; round++ {
				if err = ProvisionOfflineMigrator(ctx, adminDSN, migratorDSN, schema, true); err != nil {
					t.Fatalf("round%d offline bootstrap: %v", round, err)
				}
				if err = ProvisionCLMMaintenanceRole(ctx, adminDSN, migratorDSN, schema); err != nil {
					t.Fatalf("round%d CLM owner: %v", round, err)
				}
				if err = ProvisionTenantIdentityOwner(ctx, adminDSN, migratorDSN, schema); err != nil {
					t.Fatalf("round%d AUTH owner: %v", round, err)
				}
				pool, e := newStorePool(ctx, migratorDSN, schema, true)
				if e != nil {
					t.Fatal(e)
				}
				var su, cu string
				var super, bypass, createRole, createDB, inherit bool
				e = pool.QueryRow(ctx, `SELECT session_user,current_user,rolsuper,rolbypassrls,rolcreaterole,rolcreatedb,rolinherit FROM pg_roles WHERE rolname=current_user`).Scan(&su, &cu, &super, &bypass, &createRole, &createDB, &inherit)
				if e != nil || su != migratorName || cu != su || super || bypass || !createRole || createDB || inherit {
					pool.Close()
					t.Fatal("offline migrator genuine nonSuper role proof failed")
				}
				if e = migrate(ctx, pool, schema); e != nil {
					pool.Close()
					t.Fatalf("round%d actual nonSuper migration: %v", round, e)
				}
				pool.Close()
				if err = MigrateServiceRoles(ctx); err != nil {
					t.Fatalf("round%d actual nonSuper service provisioning: %v", round, err)
				}
			}
			// Every actual runtime login remains unable to assume either owner or read identity SQL.
			var database string
			if err = admin.pool.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
				t.Fatal(err)
			}
			for _, svc := range []string{"core", "document", "execution", "model", "tool"} {
				role := "off_" + svc + "_" + suffix
				runtime, e := newStorePool(ctx, roleDSN(role), schema, true)
				if e != nil {
					t.Fatal(e)
				}
				var su, cu string
				var privileged, canSet bool
				e = runtime.QueryRow(ctx, `SELECT session_user,current_user,rolsuper OR rolbypassrls OR rolcreaterole OR rolcreatedb,pg_has_role(current_user,$1,'SET') OR pg_has_role(current_user,$2,'SET') FROM pg_roles WHERE rolname=current_user`, clmMaintenanceRoleName(database, schema), tenantIdentityOwnerName(database, schema)).Scan(&su, &cu, &privileged, &canSet)
				if e != nil || su != role || cu != su || privileged || canSet {
					runtime.Close()
					t.Fatalf("runtime %s actual role isolation failed: %v", svc, e)
				}
				_, e = runtime.Exec(ctx, `SELECT principal_id FROM identity_principals LIMIT 1`)
				runtime.Close()
				if e == nil {
					t.Fatalf("runtime %s can read identity SQL", svc)
				}
			}
			wrong := strings.Replace(migratorDSN, password, strings.Repeat("z", len(password)), 1)
			if err = ProvisionOfflineMigrator(ctx, adminDSN, wrong, schema, true); err == nil {
				t.Fatal("incorrect existing password was silently reset")
			}
			// Foreign objects must be rejected atomically; never normalize ownership.
			if _, err = admin.pool.Exec(ctx, `CREATE TABLE `+pgx.Identifier{schema, "foreign_unreviewed"}.Sanitize()+`(id int)`); err != nil {
				t.Fatal(err)
			}
			if err = ProvisionOfflineMigrator(ctx, adminDSN, migratorDSN, schema, true); err == nil {
				t.Fatal("foreign legacy relation accepted")
			}
			if _, err = admin.pool.Exec(ctx, `DROP TABLE `+pgx.Identifier{schema, "foreign_unreviewed"}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			if _, err = admin.pool.Exec(ctx, `GRANT pg_monitor TO `+pgx.Identifier{migratorName}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			if err = ProvisionOfflineMigrator(ctx, adminDSN, migratorDSN, schema, true); err == nil {
				t.Fatal("unrelated migrator membership accepted")
			}
			if _, err = admin.pool.Exec(ctx, `REVOKE pg_monitor FROM `+pgx.Identifier{migratorName}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			if err = ProvisionOfflineMigrator(ctx, adminDSN, adminDSN, schema, true); err == nil {
				t.Fatal("same admin/migrator accepted")
			}
		})
	}
}
