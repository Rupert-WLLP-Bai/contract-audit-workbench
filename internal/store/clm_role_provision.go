package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"contractaudit/internal/config"
	"github.com/jackc/pgx/v5"
)

// ProvisionCLMMaintenanceRole uses a trusted administrator connection to
// create and bind the NOLOGIN CLM owner, then grants it only to the offline
// migrator with SET TRUE, INHERIT FALSE, ADMIN FALSE. It never grants this role
// to runtime service identities.
func ProvisionCLMMaintenanceRole(ctx context.Context, adminDSN, migratorDSN, schema string) error {
	if ctx == nil {
		return errors.New("CLM maintenance provisioning context is required")
	}
	adminDSN = strings.TrimSpace(adminDSN)
	migratorDSN = strings.TrimSpace(migratorDSN)
	if adminDSN == "" || migratorDSN == "" {
		return errors.New("CLM_ADMIN_DATABASE_URL and MIGRATOR_DATABASE_URL are required for CLM owner provisioning")
	}
	if _, err := quoteIdent(schema); err != nil {
		return fmt.Errorf("invalid CLM maintenance schema: %w", err)
	}
	adminIdentity, err := config.ParseDSN(adminDSN)
	if err != nil {
		return errors.New("CLM administrator URL is invalid")
	}
	migratorIdentity, err := config.ParseDSN(migratorDSN)
	if err != nil || adminIdentity != migratorIdentity {
		return errors.New("CLM administrator and offline migrator must select the same database")
	}
	adminConfig, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		return errors.New("CLM administrator URL is invalid")
	}
	migratorConfig, err := pgx.ParseConfig(migratorDSN)
	if err != nil || !identRE.MatchString(migratorConfig.User) {
		return errors.New("CLM offline migrator identity is invalid")
	}
	for _, key := range [...]string{"CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL", "EXECUTION_DATABASE_URL", "MODEL_DATABASE_URL", "TOOL_DATABASE_URL"} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		runtimeConfig, parseErr := pgx.ParseConfig(raw)
		if parseErr != nil {
			return fmt.Errorf("%s is invalid for CLM role safety validation", key)
		}
		if runtimeConfig.User == migratorConfig.User {
			return fmt.Errorf("CLM offline migrator identity must be distinct from %s", key)
		}
	}

	conn, err := connectPoolDSN(ctx, adminDSN)
	if err != nil {
		return errors.New("cannot connect using the CLM administrator identity")
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("cannot begin CLM owner provisioning")
	}
	defer tx.Rollback(ctx)
	var database, adminRole string
	if err := tx.QueryRow(ctx, `SELECT current_database(),current_user`).Scan(&database, &adminRole); err != nil {
		return err
	}
	if database != adminConfig.Database {
		return errors.New("CLM administrator database identity changed after connection")
	}
	role := clmMaintenanceRoleName(database, schema)
	var isSuperuser bool
	if err := tx.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname=$1`, adminRole).Scan(&isSuperuser); err != nil || !isSuperuser {
		return errors.New("CLM owner provisioning requires a trusted superuser administrator connection")
	}
	var migratorOK bool
	if err := tx.QueryRow(ctx, `SELECT role.rolcanlogin AND NOT role.rolsuper AND NOT role.rolbypassrls
		AND NOT role.rolcreatedb AND role.rolcreaterole AND NOT role.rolreplication
		FROM pg_roles role WHERE role.rolname=$1`, migratorConfig.User).Scan(&migratorOK); err != nil || !migratorOK {
		return errors.New("CLM offline migrator must be a dedicated login without superuser, bypass-RLS, create-database, replication, or inherited role membership")
	}
	var schemaExists, schemaAccess bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1),
		CASE WHEN EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)
		THEN has_schema_privilege($2,$1,'USAGE') AND has_schema_privilege($2,$1,'CREATE') ELSE false END`, schema, migratorConfig.User).Scan(&schemaExists, &schemaAccess); err != nil {
		return err
	}
	if !schemaExists || !schemaAccess {
		return errors.New("CLM schema must exist and grant USAGE and CREATE to the offline migrator before owner provisioning")
	}
	quotedRole := pgx.Identifier{role}.Sanitize()
	quotedMigrator := pgx.Identifier{migratorConfig.User}.Sanitize()
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, role).Scan(&exists); err != nil {
		return err
	}
	binding := clmMaintenanceRoleBinding(database, schema)
	if !exists {
		if _, err := tx.Exec(ctx, `CREATE ROLE `+quotedRole+` NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION`); err != nil {
			return fmt.Errorf("trusted administrator cannot create CLM maintenance owner: %w", err)
		}
		if _, err := tx.Exec(ctx, `REVOKE `+quotedRole+` FROM `+pgx.Identifier{adminRole}.Sanitize()); err != nil {
			return fmt.Errorf("cannot remove bootstrap administrator membership from CLM owner: %w", err)
		}
		commentSQL := `COMMENT ON ROLE ` + quotedRole + ` IS '` + strings.ReplaceAll(binding, "'", "''") + `'`
		if _, err := tx.Exec(ctx, commentSQL); err != nil {
			return fmt.Errorf("cannot bind CLM maintenance owner to database and schema: %w", err)
		}
	} else {
		var actualBinding string
		if err := tx.QueryRow(ctx, `SELECT shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname=$1`, role).Scan(&actualBinding); err != nil || actualBinding != binding {
			return errors.New("existing CLM maintenance owner is not bound to this database schema")
		}
	}
	var ownerSafe bool
	if err := tx.QueryRow(ctx, `SELECT NOT rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb
		AND NOT rolcreaterole AND NOT rolinherit AND NOT rolreplication FROM pg_roles WHERE rolname=$1`, role).Scan(&ownerSafe); err != nil || !ownerSafe {
		return errors.New("existing CLM maintenance owner has untrusted role attributes")
	}
	if err := verifyCLMMaintenanceRolePrivileges(ctx, tx, database, schema, role); err != nil {
		return fmt.Errorf("verify existing CLM maintenance owner privileges before membership grant: %w", err)
	}
	var memberCount int
	var exactMembership bool
	var ownerIsMember bool
	var migratorHasOtherMemberships bool
	if err := tx.QueryRow(ctx, `SELECT count(*),COALESCE(bool_and(member.rolname=$2 AND NOT membership.admin_option
		AND NOT membership.inherit_option AND membership.set_option),false)
		FROM pg_auth_members membership JOIN pg_roles owner_role ON owner_role.oid=membership.roleid
		JOIN pg_roles member ON member.oid=membership.member WHERE owner_role.rolname=$1`, role, migratorConfig.User).Scan(&memberCount, &exactMembership); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_auth_members membership
		JOIN pg_roles member ON member.oid=membership.member JOIN pg_roles granted_role ON granted_role.oid=membership.roleid
		WHERE member.rolname=$1 AND NOT (granted_role.rolname=$2 AND NOT membership.admin_option
		AND NOT membership.inherit_option AND membership.set_option))`, migratorConfig.User, role).Scan(&migratorHasOtherMemberships); err != nil {
		return err
	}
	if migratorHasOtherMemberships {
		return errors.New("CLM offline migrator has extra role memberships; pre-provisioning refuses to remove or normalize them")
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_auth_members WHERE member=$1::regrole)`, role).Scan(&ownerIsMember); err != nil {
		return err
	}
	if memberCount == 0 {
		if _, err := tx.Exec(ctx, `GRANT `+quotedRole+` TO `+quotedMigrator+` WITH ADMIN FALSE, INHERIT FALSE, SET TRUE`); err != nil {
			return fmt.Errorf("cannot grant limited migrator membership on CLM maintenance owner: %w", err)
		}
	} else if memberCount != 1 || !exactMembership {
		return errors.New("existing CLM maintenance owner has additional or elevated role memberships; refusing to normalize them")
	}
	if ownerIsMember {
		return errors.New("CLM maintenance owner must not inherit or assume other roles")
	}
	if err := verifyCLMMaintenanceRolePrivileges(ctx, tx, database, schema, role); err != nil {
		return fmt.Errorf("verify CLM maintenance owner privileges after membership grant: %w", err)
	}
	return tx.Commit(ctx)
}
