package store

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type clmRoleQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Version 1 is the frozen, previously unversioned core schema. Versions 2–7
// preserve its dependent material, subject, legal and function migrations.
// Later schema changes must be new numbered files; never edit an applied file.
// The running application accepts only this explicitly reviewed version range.
const (
	schemaMinCompatibleVersion = 31
	schemaMaxCompatibleVersion = 33
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

var migrationFileName = regexp.MustCompile(`^([0-9]{4})_([a-z0-9]+(?:_[a-z0-9]+)*)\.sql$`)

type schemaMigration struct {
	version  int
	name     string
	checksum string
	sql      string
}

// migrationCatalog is the complete, ordered history, not a hash of a mutable
// current-schema projection. Checksums cover each file's exact source bytes.
func migrationCatalog() ([]schemaMigration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read schema migration catalog: %w", err)
	}
	catalog := make([]schemaMigration, 0, len(entries))
	for _, entry := range entries {
		match := migrationFileName.FindStringSubmatch(entry.Name())
		if entry.IsDir() || match == nil {
			return nil, fmt.Errorf("invalid schema migration filename %q", entry.Name())
		}
		version, err := strconv.Atoi(match[1])
		if err != nil || version != len(catalog)+1 {
			return nil, fmt.Errorf("schema migration versions must be consecutive at %q", entry.Name())
		}
		content, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read schema migration %q: %w", entry.Name(), err)
		}
		if len(strings.TrimSpace(string(content))) == 0 {
			return nil, fmt.Errorf("schema migration %q is empty", entry.Name())
		}
		digest := sha256.Sum256(content)
		catalog = append(catalog, schemaMigration{
			version: version, name: match[2], checksum: hex.EncodeToString(digest[:]), sql: string(content),
		})
	}
	if schemaMinCompatibleVersion < 1 || schemaMinCompatibleVersion > schemaMaxCompatibleVersion ||
		schemaMaxCompatibleVersion != len(catalog) {
		return nil, errors.New("schema compatibility range does not match the migration catalog")
	}
	return catalog, nil
}

type migrationQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// appliedMigrationVersion verifies the whole applied prefix, including names,
// checksums, missing versions and versions unknown to this binary.
func appliedMigrationVersion(ctx context.Context, q migrationQueryer, catalog []schemaMigration) (int, error) {
	rows, err := q.Query(ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return 0, fmt.Errorf("read schema migration ledger: %w", err)
	}
	defer rows.Close()
	applied := 0
	for rows.Next() {
		var version int
		var name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			return 0, fmt.Errorf("read schema migration ledger row: %w", err)
		}
		if applied >= len(catalog) {
			return 0, fmt.Errorf("database schema version %d is unknown to this application", version)
		}
		expected := catalog[applied]
		if version != expected.version {
			return 0, fmt.Errorf("schema migration ledger has version %d where version %d is required", version, expected.version)
		}
		if name != expected.name || checksum != expected.checksum {
			return 0, fmt.Errorf("schema migration %d (%s) does not match its recorded name or checksum", version, expected.name)
		}
		applied++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read schema migration ledger: %w", err)
	}
	return applied, nil
}

// checkSchemaCompatibility is used by application identities; they need only
// SELECT on the ledger and can never create it or run a migration at startup.
func checkSchemaCompatibility(ctx context.Context, pool *pgxpool.Pool) error {
	catalog, err := migrationCatalog()
	if err != nil {
		return err
	}
	version, err := appliedMigrationVersion(ctx, pool, catalog)
	if err != nil {
		return fmt.Errorf("schema compatibility check failed; run the migration job with the matching release: %w", err)
	}
	if version < schemaMinCompatibleVersion || version > schemaMaxCompatibleVersion {
		return fmt.Errorf("database schema version %d is outside application compatibility range [%d,%d]", version, schemaMinCompatibleVersion, schemaMaxCompatibleVersion)
	}
	return nil
}

const createMigrationLedgerSQL = `CREATE TABLE schema_migrations (
	version bigint PRIMARY KEY CHECK (version > 0),
	name text NOT NULL UNIQUE,
	checksum text NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
	applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
)`

// migrate holds the same schema-scoped advisory lock as schema-wide writers
// and service-role provisioning, on one dedicated session for the entire run.
// Each numbered step and its ledger record commit in one transaction.
func migrate(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	catalog, err := migrationCatalog()
	if err != nil {
		return err
	}
	if _, err := quoteIdent(schema); err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, schema); err != nil {
		// A cancelled lock request has an uncertain server-side outcome; do not
		// return that session to the pool with a possibly held advisory lock.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Hijack().Close(cleanupCtx)
		return fmt.Errorf("acquire schema migration lock: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(cleanupCtx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, schema).Scan(&unlocked); err != nil || !unlocked {
			_ = conn.Hijack().Close(cleanupCtx)
			return
		}
		conn.Release()
	}()

	// Serialize creation too: two migrators may target the same new test or
	// application schema. Existing schema migrations need only the schema's
	// CREATE privilege; database-level CREATE is needed only for a missing schema.
	if schema != "public" {
		var schemaExists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, schema).Scan(&schemaExists); err != nil {
			return fmt.Errorf("inspect migration schema: %w", err)
		}
		if !schemaExists {
			if _, err := conn.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
				return fmt.Errorf("create migration schema: %w", err)
			}
		}
	}
	var activeSchema *string
	if err := conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&activeSchema); err != nil || activeSchema == nil || *activeSchema != schema {
		return fmt.Errorf("migration connection cannot select schema %q", schema)
	}
	if err := ensureCLMMaintenanceRole(ctx, conn, schema); err != nil {
		return fmt.Errorf("pre-provision CLM maintenance owner: %w", err)
	}
	var exists bool
	ledger := pgx.Identifier{schema, "schema_migrations"}.Sanitize()
	if err := conn.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, ledger).Scan(&exists); err != nil {
		return fmt.Errorf("inspect schema migration ledger: %w", err)
	}
	applied := 0
	if exists {
		applied, err = appliedMigrationVersion(ctx, conn, catalog)
		if err != nil {
			return err
		}
	}
	for _, migration := range catalog[applied:] {
		if migration.version == 33 {
			if err := ensureTenantIdentityOwner(ctx, conn, schema); err != nil {
				return fmt.Errorf("pre-provision tenant identity owner: %w", err)
			}
		}
		if err := applyMigration(ctx, conn, schema, migration, !exists); err != nil {
			return fmt.Errorf("apply schema migration %04d_%s: %w", migration.version, migration.name, err)
		}
		exists = true
	}
	return nil
}

func tenantIdentityOwnerName(database, schema string) string {
	digest := md5.Sum([]byte(database + ":" + schema))
	return fmt.Sprintf("caw_auth_fence_%x", digest[:])
}

func tenantIdentityOwnerBinding(database, schema string) string {
	return "contractaudit:tenant-identity-owner:v1:" + database + ":" + schema
}

func ensureTenantIdentityOwner(ctx context.Context, conn *pgxpool.Conn, schema string) error {
	var database, migrator string
	if err := conn.QueryRow(ctx, `SELECT current_database(),current_user`).Scan(&database, &migrator); err != nil {
		return err
	}
	role := tenantIdentityOwnerName(database, schema)
	if !identRE.MatchString(migrator) || migrator == role {
		return errors.New("migration identity cannot be the tenant identity owner")
	}
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, role).Scan(&exists); err != nil {
		return err
	}
	quotedRole := pgx.Identifier{role}.Sanitize()
	if !exists {
		var isSuperuser bool
		if err := conn.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname=$1`, migrator).Scan(&isSuperuser); err != nil {
			return err
		}
		if !isSuperuser {
			return fmt.Errorf("tenant identity owner %q is missing for database %q schema %q; run `cmd/migrate provision-tenant-identity` with AUTH_ADMIN_DATABASE_URL and MIGRATOR_DATABASE_URL before migration", role, database, schema)
		}
		if _, err := conn.Exec(ctx, `CREATE ROLE `+quotedRole+` NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION`); err != nil {
			return fmt.Errorf("cannot create tenant identity owner: %w", err)
		}
		if _, err := conn.Exec(ctx, `REVOKE `+quotedRole+` FROM `+pgx.Identifier{migrator}.Sanitize()); err != nil {
			return fmt.Errorf("cannot remove implicit tenant identity owner membership: %w", err)
		}
		binding := tenantIdentityOwnerBinding(database, schema)
		if _, err := conn.Exec(ctx, `COMMENT ON ROLE `+quotedRole+` IS '`+strings.ReplaceAll(binding, "'", "''")+`'`); err != nil {
			return fmt.Errorf("cannot bind tenant identity owner to database and schema: %w", err)
		}
	} else {
		var binding string
		if err := conn.QueryRow(ctx, `SELECT shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname=$1`, role).Scan(&binding); err != nil || binding != tenantIdentityOwnerBinding(database, schema) {
			return errors.New("existing tenant identity owner is not bound to this database schema")
		}
	}
	var safe bool
	if err := conn.QueryRow(ctx, `SELECT NOT rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb
		AND NOT rolcreaterole AND NOT rolinherit AND NOT rolreplication FROM pg_roles WHERE rolname=$1`, role).Scan(&safe); err != nil || !safe {
		return errors.New("tenant identity owner has unsafe role attributes")
	}
	var memberCount int
	var migratorOnly, setOnly bool
	if err := conn.QueryRow(ctx, `SELECT count(*),
		COALESCE(bool_and(member_role.rolname=$2),false),
		COALESCE(bool_or(member_role.rolname=$2 AND m.admin_option=false AND m.inherit_option=false AND m.set_option=true),false)
		FROM pg_auth_members m JOIN pg_roles target ON target.oid=m.roleid
		JOIN pg_roles member_role ON member_role.oid=m.member WHERE target.rolname=$1`, role, migrator).Scan(&memberCount, &migratorOnly, &setOnly); err != nil {
		return fmt.Errorf("inspect tenant identity owner membership: %w", err)
	}
	if memberCount == 0 {
		var isSuperuser bool
		if err := conn.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname=$1`, migrator).Scan(&isSuperuser); err != nil {
			return err
		}
		if !isSuperuser {
			return errors.New("tenant identity owner is not pre-provisioned for this migrator")
		}
		if _, err := conn.Exec(ctx, `GRANT `+quotedRole+` TO `+pgx.Identifier{migrator}.Sanitize()+` WITH ADMIN FALSE, INHERIT FALSE, SET TRUE`); err != nil {
			return fmt.Errorf("cannot grant bounded tenant identity owner SET membership: %w", err)
		}
		memberCount, migratorOnly, setOnly = 1, true, true
	}
	if memberCount != 1 || !migratorOnly || !setOnly {
		return errors.New("tenant identity owner membership must be SET-only for this migrator")
	}
	var ownerHasMembership bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_auth_members WHERE member=$1::regrole)`, role).Scan(&ownerHasMembership); err != nil {
		return err
	}
	if ownerHasMembership {
		return errors.New("tenant identity owner must not inherit other roles")
	}
	if err := verifyTenantIdentityOwnerPrivileges(ctx, conn, database, schema, role); err != nil {
		return err
	}
	return nil
}

// clmMaintenanceRoleName is database-global but schema-specific. The stable
// suffix prevents one schema's maintenance owner from receiving another
// schema's policies or function privileges.
func clmMaintenanceRoleName(database, schema string) string {
	digest := md5.Sum([]byte(database + ":" + schema))
	return fmt.Sprintf("caw_clm_maint_%x", digest[:])
}

// ensureCLMMaintenanceRole runs before any numbered migration. It creates only
// a NOLOGIN, non-privileged owner and grants its narrowly scoped membership to
// the migrator so that migrations can transfer function ownership. Runtime
// identities are never members. Existing roles are accepted only when their
// attributes and complete direct membership are exactly the expected state.
func ensureCLMMaintenanceRole(ctx context.Context, conn *pgxpool.Conn, schema string) error {
	var database string
	if err := conn.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		return err
	}
	role := clmMaintenanceRoleName(database, schema)
	var migrator string
	if err := conn.QueryRow(ctx, `SELECT current_user`).Scan(&migrator); err != nil {
		return err
	}
	if !identRE.MatchString(migrator) || migrator == role {
		return errors.New("migration identity cannot be the CLM maintenance owner")
	}
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, role).Scan(&exists); err != nil {
		return err
	}
	quotedRole := pgx.Identifier{role}.Sanitize()
	if !exists {
		var migratorIsSuperuser bool
		if err := conn.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname=$1`, migrator).Scan(&migratorIsSuperuser); err != nil {
			return fmt.Errorf("inspect CLM maintenance owner bootstrap authority: %w", err)
		}
		if !migratorIsSuperuser {
			return fmt.Errorf("CLM maintenance owner %q is missing for database %q schema %q; run `cmd/migrate provision-clm-maintenance` with CLM_ADMIN_DATABASE_URL and a dedicated MIGRATOR_DATABASE_URL to pre-provision the bound NOLOGIN owner with ADMIN FALSE, INHERIT FALSE, SET TRUE membership (binding %q)", role, database, schema, clmMaintenanceRoleBinding(database, schema))
		}
		if _, err := conn.Exec(ctx, `CREATE ROLE `+quotedRole+` NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION`); err != nil {
			return fmt.Errorf("migration identity lacks authority to create the bounded maintenance role: %w", err)
		}
		// PostgreSQL gives a role creator an implicit ADMIN membership, whose
		// grantor may be the bootstrap superuser. Remove it before adding the
		// explicitly SET-only migrator membership below.
		if _, err := conn.Exec(ctx, `REVOKE `+quotedRole+` FROM `+pgx.Identifier{migrator}.Sanitize()); err != nil {
			return fmt.Errorf("cannot remove implicit creator membership from CLM maintenance role: %w", err)
		}
		binding := clmMaintenanceRoleBinding(database, schema)
		commentSQL := `COMMENT ON ROLE ` + quotedRole + ` IS '` + strings.ReplaceAll(binding, "'", "''") + `'`
		if _, err := conn.Exec(ctx, commentSQL); err != nil {
			return fmt.Errorf("cannot bind CLM maintenance role to database schema: %w", err)
		}
	} else {
		var binding string
		if err := conn.QueryRow(ctx, `SELECT shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname=$1`, role).Scan(&binding); err != nil || binding != clmMaintenanceRoleBinding(database, schema) {
			return errors.New("existing CLM maintenance role is not bound to this database schema")
		}
	}
	var safe bool
	if err := conn.QueryRow(ctx, `SELECT NOT rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb
		AND NOT rolcreaterole AND NOT rolinherit AND NOT rolreplication
		FROM pg_roles WHERE rolname=$1`, role).Scan(&safe); err != nil || !safe {
		return errors.New("existing CLM maintenance role has untrusted attributes")
	}
	var memberCount int
	var hasMigrator bool
	var onlyMigrator bool
	if err := conn.QueryRow(ctx, `SELECT count(*),COALESCE(bool_or(member_role.rolname=$2 AND m.admin_option=false
		AND m.inherit_option=false AND m.set_option=true),false),COALESCE(bool_and(member_role.rolname=$2),false)
		FROM pg_auth_members m JOIN pg_roles target ON target.oid=m.roleid
		JOIN pg_roles member_role ON member_role.oid=m.member WHERE target.rolname=$1`, role, migrator).Scan(&memberCount, &hasMigrator, &onlyMigrator); err != nil {
		return fmt.Errorf("inspect CLM maintenance role memberships: %w", err)
	}
	if memberCount == 0 {
		if _, err := conn.Exec(ctx, `GRANT `+quotedRole+` TO `+pgx.Identifier{migrator}.Sanitize()+` WITH ADMIN FALSE, INHERIT FALSE, SET TRUE`); err != nil {
			return fmt.Errorf("migration identity cannot receive limited CLM maintenance membership: %w", err)
		}
		memberCount, hasMigrator, onlyMigrator = 1, true, true
	} else if memberCount == 1 && onlyMigrator && !hasMigrator {
		// PostgreSQL grants a role creator membership with ADMIN but no SET
		// option. Replace that implicit grant with the explicitly limited
		// migration-only membership required for ownership transfer.
		if _, err := conn.Exec(ctx, `GRANT `+quotedRole+` TO `+pgx.Identifier{migrator}.Sanitize()+` WITH ADMIN FALSE, INHERIT FALSE, SET TRUE`); err != nil {
			return fmt.Errorf("cannot restrict creator membership on CLM maintenance role: %w", err)
		}
		hasMigrator = true
	}
	if memberCount != 1 || !hasMigrator || !onlyMigrator {
		return fmt.Errorf("CLM maintenance role membership is not limited to this migrator (members=%d has_migrator=%t only_migrator=%t current_user=%q)", memberCount, hasMigrator, onlyMigrator, migrator)
	}
	var ownerHasMembership bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_auth_members WHERE member=$1::regrole)`, role).Scan(&ownerHasMembership); err != nil {
		return err
	}
	if ownerHasMembership {
		return errors.New("CLM maintenance role must not inherit any role memberships")
	}
	if err := verifyCLMMaintenanceRolePrivileges(ctx, conn, database, schema, role); err != nil {
		return err
	}
	return nil
}

func clmMaintenanceRoleBinding(database, schema string) string {
	return "contractaudit:clm-maintenance:v1:" + database + ":" + schema
}

// verifyCLMMaintenanceRolePrivileges rejects an existing same-named role that
// owns anything outside this schema's four maintenance functions, has any
// unexpected ACL bits, or carries a dependency in another schema/database.
func verifyCLMMaintenanceRolePrivileges(ctx context.Context, q clmRoleQueryer, database, schema, role string) error {
	var unsafe bool
	err := q.QueryRow(ctx, `WITH target AS (
		SELECT role.oid AS role_oid, namespace.oid AS namespace_oid,
			(SELECT oid FROM pg_database WHERE datname=$1) AS database_oid
		FROM pg_roles role CROSS JOIN pg_namespace namespace
		WHERE role.rolname=$2 AND namespace.nspname=$3
	), allowed_functions AS (
		SELECT unnest(ARRAY[
			to_regprocedure(format('%I.clm_outbox_claim(integer,bigint)',$3))::oid,
			to_regprocedure(format('%I.clm_outbox_ack(text,uuid,bigint)',$3))::oid,
			to_regprocedure(format('%I.clm_outbox_release(text,uuid,bigint,bigint)',$3))::oid,
			to_regprocedure(format('%I.clm_metrics_snapshot(bigint)',$3))::oid
		]) AS oid
	), allowed_columns AS (
		SELECT c.oid AS relation_oid, a.attnum
		FROM target, pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
		WHERE n.oid=target.namespace_oid AND (
			(c.relname='clm_outbox_delivery' AND a.attname IN ('tenant_id','event_id','available_at','claim_token','claimed_until','broker_acked_at','attempt_count'))
			OR (c.relname='clm_business_events' AND a.attname IN ('tenant_id','event_id','event_type','occurred_at'))
			OR (c.relname='clm_approval_rounds' AND a.attname IN ('state','started_at','ended_at'))
		)
	), unexpected_dependencies AS (
		SELECT 1 FROM pg_shdepend dependency, target
		WHERE dependency.refclassid='pg_authid'::regclass AND dependency.refobjid=target.role_oid
		AND NOT (
			(dependency.deptype='o' AND dependency.dbid=target.database_oid
				AND dependency.classid='pg_proc'::regclass AND dependency.objid IN (SELECT oid FROM allowed_functions WHERE oid IS NOT NULL))
			OR (dependency.deptype='a' AND dependency.dbid=target.database_oid AND (
				(dependency.classid='pg_namespace'::regclass AND dependency.objid=target.namespace_oid AND dependency.objsubid=0)
				OR (dependency.classid='pg_class'::regclass AND EXISTS (SELECT 1 FROM allowed_columns column_acl
					WHERE column_acl.relation_oid=dependency.objid AND column_acl.attnum=dependency.objsubid))
			))
			OR (dependency.deptype='r' AND dependency.dbid=target.database_oid AND dependency.classid='pg_policy'::regclass
				AND dependency.objid IN (SELECT policy.oid FROM pg_policy policy JOIN pg_class relation ON relation.oid=policy.polrelid
					WHERE relation.relnamespace=target.namespace_oid AND policy.polname IN ('clm_maintenance_all','clm_maintenance_read')))
		)
	), bad_acls AS (
		SELECT 1 FROM pg_database database CROSS JOIN LATERAL aclexplode(database.datacl) acl, target
		WHERE acl.grantee=target.role_oid
		UNION ALL
		SELECT 1 FROM pg_namespace namespace CROSS JOIN LATERAL aclexplode(namespace.nspacl) acl, target
		WHERE acl.grantee=target.role_oid AND NOT (namespace.oid=target.namespace_oid AND acl.privilege_type='USAGE' AND NOT acl.is_grantable)
		UNION ALL
		SELECT 1 FROM pg_class relation CROSS JOIN LATERAL aclexplode(relation.relacl) acl, target
		WHERE acl.grantee=target.role_oid
		UNION ALL
		SELECT 1 FROM pg_attribute attribute CROSS JOIN LATERAL aclexplode(attribute.attacl) acl, target
		WHERE acl.grantee=target.role_oid AND NOT EXISTS (
			SELECT 1 FROM allowed_columns allowed JOIN pg_class relation ON relation.oid=allowed.relation_oid
			JOIN pg_attribute expected ON expected.attrelid=allowed.relation_oid AND expected.attnum=allowed.attnum
			WHERE allowed.relation_oid=attribute.attrelid AND allowed.attnum=attribute.attnum
			AND ((relation.relname='clm_outbox_delivery' AND
				((acl.privilege_type='SELECT' AND expected.attname IN ('tenant_id','event_id','available_at','claim_token','claimed_until','broker_acked_at','attempt_count')) OR
				 (acl.privilege_type='UPDATE' AND expected.attname IN ('available_at','claim_token','claimed_until','broker_acked_at','attempt_count')))) OR
				(relation.relname='clm_business_events' AND acl.privilege_type='SELECT' AND expected.attname IN ('tenant_id','event_id','event_type','occurred_at')) OR
				(relation.relname='clm_approval_rounds' AND acl.privilege_type='SELECT' AND expected.attname IN ('state','started_at','ended_at')))
			AND NOT acl.is_grantable
		)
		UNION ALL
		SELECT 1 FROM pg_proc function CROSS JOIN LATERAL aclexplode(function.proacl) acl, target
		WHERE acl.grantee=target.role_oid AND function.oid NOT IN (SELECT oid FROM allowed_functions WHERE oid IS NOT NULL)
		UNION ALL
		SELECT 1 FROM pg_default_acl defaults CROSS JOIN LATERAL aclexplode(defaults.defaclacl) acl, target
		WHERE acl.grantee=target.role_oid
	)
	SELECT EXISTS (SELECT 1 FROM unexpected_dependencies) OR EXISTS (SELECT 1 FROM bad_acls)
		OR EXISTS (SELECT 1 FROM pg_db_role_setting setting,target WHERE setting.setrole=target.role_oid)
		OR EXISTS (SELECT 1 FROM pg_shdepend dependency,target WHERE dependency.refclassid='pg_authid'::regclass
			AND dependency.refobjid=target.role_oid AND dependency.dbid<>target.database_oid AND dependency.dbid<>0)
		OR EXISTS (SELECT 1 FROM target WHERE has_schema_privilege(target.role_oid,target.namespace_oid,'CREATE'))
		OR EXISTS (SELECT 1 FROM target WHERE
			(SELECT count(*) FROM pg_proc function WHERE function.pronamespace=target.namespace_oid AND function.proname IN
				('clm_outbox_claim','clm_outbox_ack','clm_outbox_release','clm_metrics_snapshot')) NOT IN (0,4))
		OR EXISTS (SELECT 1 FROM target WHERE
			(SELECT count(*) FROM pg_proc function WHERE function.pronamespace=target.namespace_oid AND function.proname IN
				('clm_outbox_claim','clm_outbox_ack','clm_outbox_release','clm_metrics_snapshot') AND function.proowner=target.role_oid) NOT IN (0,4))
		OR EXISTS (SELECT 1 FROM target WHERE
			(SELECT count(*) FROM pg_policy policy JOIN pg_class relation ON relation.oid=policy.polrelid
				WHERE relation.relnamespace=target.namespace_oid AND policy.polname IN ('clm_maintenance_all','clm_maintenance_read')) NOT IN (0,3))
		OR EXISTS (SELECT 1 FROM target WHERE
			(SELECT count(*) FROM pg_proc function WHERE function.pronamespace=target.namespace_oid AND function.proname IN
				('clm_outbox_claim','clm_outbox_ack','clm_outbox_release','clm_metrics_snapshot'))=4
			AND ((SELECT count(*) FROM pg_policy policy JOIN pg_class relation ON relation.oid=policy.polrelid
				WHERE relation.relnamespace=target.namespace_oid AND (
					(relation.relname='clm_outbox_delivery' AND policy.polname='clm_maintenance_all' AND policy.polcmd='*'
						AND policy.polroles=ARRAY[target.role_oid] AND pg_get_expr(policy.polqual,policy.polrelid)='true'
						AND pg_get_expr(policy.polwithcheck,policy.polrelid)='true')
					OR (relation.relname IN ('clm_business_events','clm_approval_rounds') AND policy.polname='clm_maintenance_read'
						AND policy.polcmd='r' AND policy.polroles=ARRAY[target.role_oid]
						AND pg_get_expr(policy.polqual,policy.polrelid)='true' AND policy.polwithcheck IS NULL)))<>3
				OR EXISTS (SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
					WHERE namespace.oid=target.namespace_oid AND relation.relname IN ('clm_outbox_delivery','clm_business_events','clm_approval_rounds')
					AND (NOT relation.relrowsecurity OR NOT relation.relforcerowsecurity))))
		OR EXISTS (SELECT 1 FROM target WHERE
			(SELECT count(*) FROM pg_proc function WHERE function.pronamespace=target.namespace_oid AND function.proname IN
				('clm_outbox_claim','clm_outbox_ack','clm_outbox_release','clm_metrics_snapshot'))=4
			AND ((SELECT count(*) FROM pg_proc function WHERE function.pronamespace=target.namespace_oid AND function.proname IN
				('clm_outbox_claim','clm_outbox_ack','clm_outbox_release','clm_metrics_snapshot') AND function.proowner=target.role_oid
				AND function.prosecdef AND function.proconfig @> ARRAY['search_path=pg_catalog, pg_temp','statement_timeout=2s']::text[])=4
				AND NOT EXISTS (SELECT 1 FROM pg_proc function CROSS JOIN LATERAL aclexplode(COALESCE(function.proacl,acldefault('f',function.proowner))) acl
					WHERE function.pronamespace=target.namespace_oid AND function.proname IN
						('clm_outbox_claim','clm_outbox_ack','clm_outbox_release','clm_metrics_snapshot')
						AND acl.grantee=0 AND acl.privilege_type='EXECUTE')) IS FALSE)
		OR EXISTS (SELECT 1 FROM target WHERE
			(SELECT count(*) FROM pg_proc function WHERE function.pronamespace=target.namespace_oid AND function.proname IN
				('clm_outbox_claim','clm_outbox_ack','clm_outbox_release','clm_metrics_snapshot'))=4
			AND (
			NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_outbox_delivery',$3)),'tenant_id','SELECT')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_outbox_delivery',$3)),'event_id','SELECT')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_outbox_delivery',$3)),'available_at','SELECT')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_outbox_delivery',$3)),'claim_token','SELECT')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_outbox_delivery',$3)),'claimed_until','SELECT')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_outbox_delivery',$3)),'broker_acked_at','SELECT')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_outbox_delivery',$3)),'attempt_count','SELECT')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_outbox_delivery',$3)),'available_at','UPDATE')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_outbox_delivery',$3)),'claim_token','UPDATE')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_outbox_delivery',$3)),'claimed_until','UPDATE')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_outbox_delivery',$3)),'broker_acked_at','UPDATE')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_outbox_delivery',$3)),'attempt_count','UPDATE')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_business_events',$3)),'tenant_id','SELECT')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_business_events',$3)),'event_id','SELECT')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_business_events',$3)),'event_type','SELECT')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_business_events',$3)),'occurred_at','SELECT')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_approval_rounds',$3)),'state','SELECT')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_approval_rounds',$3)),'started_at','SELECT')
			OR NOT has_column_privilege(target.role_oid,to_regclass(format('%I.clm_approval_rounds',$3)),'ended_at','SELECT')))
		OR EXISTS (SELECT 1 FROM target WHERE
			(SELECT count(*) FROM pg_proc function WHERE function.pronamespace=target.namespace_oid AND function.proname IN
				('clm_outbox_claim','clm_outbox_ack','clm_outbox_release','clm_metrics_snapshot'))=4
			AND NOT has_schema_privilege(target.role_oid,target.namespace_oid,'USAGE'))`, database, role, schema).Scan(&unsafe)
	if err != nil {
		return fmt.Errorf("verify CLM maintenance role grants and dependencies: %w", err)
	}
	if unsafe {
		return errors.New("CLM maintenance role has extra privileges or cross-schema/database dependencies")
	}
	return nil
}

func applyMigration(ctx context.Context, conn *pgxpool.Conn, schema string, migration schemaMigration, createLedger bool) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanupCtx)
	}()
	if createLedger {
		if _, err := tx.Exec(ctx, createMigrationLedgerSQL); err != nil {
			return fmt.Errorf("create schema migration ledger: %w", err)
		}
	}
	if err := executeMigrationSQL(ctx, tx, schema, migration.sql); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version,name,checksum) VALUES($1,$2,$3)`,
		migration.version, migration.name, migration.checksum); err != nil {
		return fmt.Errorf("record schema migration: %w", err)
	}
	return tx.Commit(ctx)
}

// Schema identifiers and schema-specific advisory keys are the only dynamic
// parts in the frozen budget and experience SQL templates.
func executeMigrationSQL(ctx context.Context, tx pgx.Tx, schema, source string) error {
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	key := strings.ReplaceAll(schema, "'", "''")
	ownerRole := ""
	if strings.Contains(source, "{{CLM_OWNER}}") {
		var database string
		if err := tx.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
			return fmt.Errorf("resolve CLM migration owner: %w", err)
		}
		ownerRole = pgx.Identifier{clmMaintenanceRoleName(database, schema)}.Sanitize()
	}
	authFenceRole := ""
	authFenceLiteral := ""
	if strings.Contains(source, "{{AUTH_FENCE_OWNER}}") {
		var database string
		if err := tx.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
			return fmt.Errorf("resolve tenant identity owner: %w", err)
		}
		name := tenantIdentityOwnerName(database, schema)
		authFenceRole = pgx.Identifier{name}.Sanitize()
		authFenceLiteral = "'" + strings.ReplaceAll(name, "'", "''") + "'"
	}
	sql := strings.NewReplacer(
		"{{SCHEMA}}", quotedSchema,
		"{{CLM_OWNER}}", ownerRole,
		"{{AUTH_FENCE_OWNER}}", authFenceRole,
		"{{AUTH_FENCE_OWNER_LITERAL}}", authFenceLiteral,
		"{{IDENTITY_LOCK_KEY}}", strings.ReplaceAll(schema+":tenant-identity", "'", "''"),
		"{{LOCK_KEY}}", key+":model-budget:global",
		"{{QUEUE_LOCK_KEY}}", key+":execution-queue",
	).Replace(source)
	if _, err := tx.Exec(ctx, sql); err != nil {
		return fmt.Errorf("execute migration SQL: %w", err)
	}
	return nil
}
