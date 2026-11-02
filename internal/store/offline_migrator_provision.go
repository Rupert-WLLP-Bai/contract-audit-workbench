package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"contractaudit/internal/config"
	"github.com/jackc/pgx/v5"
)

// ProvisionOfflineMigrator is an offline, administrator-only installation step.
// It never converts an existing privileged identity or reassigns objects outside
// the selected application schema. Existing credentials are not rewritten.
func ProvisionOfflineMigrator(ctx context.Context, adminDSN, migratorDSN, schema string, allowCreate bool) error {
	admin, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		return errors.New("invalid offline administrator DSN")
	}
	migrator, err := pgx.ParseConfig(migratorDSN)
	if err != nil || !identRE.MatchString(migrator.User) || len(migrator.Password) < 32 || migrator.User == admin.User {
		return errors.New("offline migrator requires a distinct dedicated login and private password")
	}
	a, err := config.ParseDSN(adminDSN)
	if err != nil {
		return errors.New("invalid offline administrator target")
	}
	m, err := config.ParseDSN(migratorDSN)
	if err != nil || a != m {
		return errors.New("offline administrator and migrator must select the same database")
	}
	if _, err = quoteIdent(schema); err != nil {
		return errors.New("invalid offline migration schema")
	}
	roles := map[string]string{}
	for _, service := range []string{"core", "document", "execution", "model", "tool"} {
		raw := os.Getenv(strings.ToUpper(service) + "_DATABASE_URL")
		target, e := config.ParseDSN(raw)
		c, e2 := pgx.ParseConfig(raw)
		if e != nil || e2 != nil || target != a || !identRE.MatchString(c.User) || c.User == admin.User || c.User == migrator.User || len(c.Password) < 32 {
			return errors.New("five distinct service DSNs must select the offline migration database")
		}
		if _, ok := roles[c.User]; ok {
			return errors.New("offline migration service identities must be distinct")
		}
		roles[c.User] = service
	}
	conn, err := connectPoolDSN(ctx, adminDSN)
	if err != nil {
		return errors.New("cannot connect offline provisioning administrator")
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var database, session, current string
	var super bool
	if err = tx.QueryRow(ctx, `SELECT current_database(),session_user,current_user,rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&database, &session, &current, &super); err != nil || !super || database != admin.Database || current != admin.User || session != current {
		return errors.New("offline provisioning requires the actual selected superuser administrator")
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, schema); err != nil {
		return err
	}
	binding := "contractaudit offline migrator:" + database + ":" + schema
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, migrator.User).Scan(&exists); err != nil {
		return err
	}
	if exists {
		probe, e := connectPoolDSN(ctx, migratorDSN)
		if e != nil {
			return errors.New("existing offline migrator credential cannot authenticate")
		}
		var su, cu string
		e = probe.QueryRow(ctx, `SELECT session_user,current_user`).Scan(&su, &cu)
		probe.Close(ctx)
		if e != nil || su != migrator.User || cu != su {
			return errors.New("existing offline migrator DSN identity mismatch")
		}
	}
	if exists {
		var outside bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datdba=$1::regrole) OR EXISTS(SELECT 1 FROM pg_shdepend d WHERE d.refclassid='pg_authid'::regclass AND d.refobjid=$1::regrole AND (
            d.dbid<>0 AND d.dbid<>(SELECT oid FROM pg_database WHERE datname=current_database())
            OR d.classid='pg_database'::regclass AND d.objid<>(SELECT oid FROM pg_database WHERE datname=current_database())
            OR d.dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND (
              d.classid='pg_namespace'::regclass AND d.objid<>(SELECT oid FROM pg_namespace WHERE nspname=$2)
              OR d.classid='pg_class'::regclass AND EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=d.objid AND n.nspname<>$2)
              OR d.classid='pg_proc'::regclass AND EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid=d.objid AND n.nspname<>$2)
            )))`, migrator.User, schema).Scan(&outside); err != nil || outside {
			return errors.New("offline migrator has ownership or explicit privileges outside its selected database/schema")
		}
	}
	qm := pgx.Identifier{migrator.User}.Sanitize()
	qs := pgx.Identifier{schema}.Sanitize()
	if !exists {
		if !allowCreate {
			return errors.New("production offline migrator must be provisioned and audited by the operator")
		}
		password := "'" + strings.ReplaceAll(migrator.Password, "'", "''") + "'"
		if _, err = tx.Exec(ctx, `CREATE ROLE `+qm+` LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB CREATEROLE NOINHERIT NOREPLICATION PASSWORD `+password); err != nil {
			return errors.New("cannot create dedicated offline migrator")
		}
		if _, err = tx.Exec(ctx, `COMMENT ON ROLE `+qm+` IS '`+strings.ReplaceAll(binding, "'", "''")+`'`); err != nil {
			return err
		}
	} else {
		var safe bool
		var comment *string
		if err = tx.QueryRow(ctx, `SELECT rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb AND rolcreaterole AND NOT rolinherit AND NOT rolreplication,shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname=$1`, migrator.User).Scan(&safe, &comment); err != nil || !safe || comment == nil || *comment != binding {
			return errors.New("existing offline migrator lacks the exact safe attributes and database/schema binding; refusing takeover")
		}
	}
	var schemaExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, schema).Scan(&schemaExists); err != nil {
		return err
	}
	if !schemaExists {
		if _, err = tx.Exec(ctx, `CREATE SCHEMA `+qs+` AUTHORIZATION `+qm); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('search_path',$1,true)`, schema); err != nil {
		return err
	}
	if err = auditAndTransferOfflineObjects(ctx, tx, database, schema, current, migrator.User, roles); err != nil {
		return err
	}
	for _, owner := range []string{clmMaintenanceRoleName(database, schema), tenantIdentityOwnerName(database, schema)} {
		var present bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, owner).Scan(&present); err != nil {
			return err
		}
		if !present {
			continue
		}
		expected := clmMaintenanceRoleBinding(database, schema)
		if owner == tenantIdentityOwnerName(database, schema) {
			expected = tenantIdentityOwnerBinding(database, schema)
		}
		var bound string
		var safe bool
		if err = tx.QueryRow(ctx, `SELECT shobj_description(oid,'pg_authid'),NOT rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolinherit AND NOT rolreplication FROM pg_roles WHERE rolname=$1`, owner).Scan(&bound, &safe); err != nil || !safe || bound != expected {
			return errors.New("legacy authority owner has no exact safe binding")
		}
		if owner == tenantIdentityOwnerName(database, schema) {
			err = verifyTenantIdentityOwnerPrivileges(ctx, tx, database, schema, owner)
		} else {
			err = verifyCLMMaintenanceRolePrivileges(ctx, tx, database, schema, owner)
		}
		if err != nil {
			return err
		}
		var unsafe bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.member WHERE m.roleid=$1::regrole AND NOT (r.rolname=ANY($2::text[]) AND NOT m.admin_option AND NOT m.inherit_option AND m.set_option)) OR EXISTS(SELECT 1 FROM pg_auth_members WHERE member=$1::regrole)`, owner, []string{current, migrator.User}).Scan(&unsafe); err != nil || unsafe {
			return errors.New("legacy authority owner has unexpected memberships")
		}
		if _, err = tx.Exec(ctx, `REVOKE `+pgx.Identifier{owner}.Sanitize()+` FROM `+pgx.Identifier{current}.Sanitize()+`; GRANT `+pgx.Identifier{owner}.Sanitize()+` TO `+qm+` WITH ADMIN FALSE, INHERIT FALSE, SET TRUE`); err != nil {
			return err
		}
	}
	if err = verifyOfflineMigratorMemberships(ctx, tx, database, schema, migrator.User); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `GRANT USAGE,CREATE ON SCHEMA `+qs+` TO `+qm+` WITH GRANT OPTION`); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	// The DSN must genuinely authenticate as this login; no SET ROLE substitution.
	login, err := connectPoolDSN(ctx, migratorDSN)
	if err != nil {
		return errors.New("offline migrator credential cannot authenticate; no existing password was changed")
	}
	defer login.Close(ctx)
	if err = login.QueryRow(ctx, `SELECT session_user,current_user`).Scan(&session, &current); err != nil || session != migrator.User || current != session {
		return errors.New("offline migrator DSN does not authenticate as its dedicated login")
	}
	return nil
}

func auditAndTransferOfflineObjects(ctx context.Context, tx pgx.Tx, database, schema, admin, migrator string, roles map[string]string) error {
	catalog, err := migrationCatalog()
	if err != nil {
		return err
	}
	var ledger bool
	if err = tx.QueryRow(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&ledger); err != nil {
		return err
	}
	if ledger {
		if _, err = appliedMigrationVersion(ctx, tx, catalog); err != nil {
			return err
		}
	}
	tables := map[string]bool{"schema_migrations": true}
	functions := map[string]bool{}
	triggers := map[string]bool{
		"subject_versions_append_only": true, "case_subject_role_versions_append_only": true, "subject_change_requests_append_only": true, "subject_change_decisions_append_only": true, "subject_write_idempotency_append_only": true, "subject_source_snapshots_append_only": true,
	}
	policies := map[string]bool{}
	triggerRE := regexp.MustCompile(`(?i)CREATE\s+(?:CONSTRAINT\s+)?TRIGGER\s+([a-z_][a-z_0-9]*)`)
	policyRE := regexp.MustCompile(`(?i)CREATE\s+POLICY\s+([a-z_][a-z_0-9]*)`)
	sequences := map[string]bool{}
	sequenceRE := regexp.MustCompile(`(?i)CREATE\s+SEQUENCE\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:\{\{SCHEMA\}\}\.)?([a-z_][a-z_0-9]*)`)
	tableRE := regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:\{\{SCHEMA\}\}\.)?([a-z_][a-z_0-9]*)`)
	fnRE := regexp.MustCompile(`(?i)CREATE\s+(?:OR\s+REPLACE\s+)?FUNCTION\s+(?:\{\{SCHEMA\}\}\.)?([a-z_][a-z_0-9]*)`)
	for _, migration := range catalog {
		for _, v := range triggerRE.FindAllStringSubmatch(migration.sql, -1) {
			triggers[v[1]] = true
		}
		for _, v := range policyRE.FindAllStringSubmatch(migration.sql, -1) {
			policies[v[1]] = true
		}
		for _, v := range sequenceRE.FindAllStringSubmatch(migration.sql, -1) {
			sequences[v[1]] = true
		}
		for _, v := range tableRE.FindAllStringSubmatch(migration.sql, -1) {
			tables[v[1]] = true
		}
		for _, v := range fnRE.FindAllStringSubmatch(migration.sql, -1) {
			functions[v[1]] = true
		}
	}
	clm, auth := clmMaintenanceRoleName(database, schema), tenantIdentityOwnerName(database, schema)
	var schemaOwner string
	if err = tx.QueryRow(ctx, `SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname=$1`, schema).Scan(&schemaOwner); err != nil {
		return err
	}
	if schemaOwner != admin && schemaOwner != migrator && !(schema == "public" && schemaOwner == "pg_database_owner") {
		return errors.New("offline bootstrap refuses a schema owned by an unrelated identity")
	}
	// A nonempty legacy schema needs a verified immutable migration ledger.
	rows, err := tx.Query(ctx, `SELECT c.relname,c.relkind::text,pg_get_userbyid(c.relowner),COALESCE(parent.relname,'') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_depend d ON d.classid='pg_class'::regclass AND d.objid=c.oid AND d.deptype IN ('a','i') AND d.refclassid='pg_class'::regclass LEFT JOIN pg_class parent ON parent.oid=d.refobjid WHERE n.nspname=$1 AND c.relkind IN ('r','p','S','v','m','f') AND NOT EXISTS(SELECT 1 FROM pg_depend e WHERE e.classid='pg_class'::regclass AND e.objid=c.oid AND e.deptype='e')`, schema)
	if err != nil {
		return err
	}
	type object struct{ name, kind, owner, parent string }
	var objects []object
	for rows.Next() {
		var o object
		if err = rows.Scan(&o.name, &o.kind, &o.owner, &o.parent); err != nil {
			rows.Close()
			return err
		}
		objects = append(objects, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, o := range objects {
		known := tables[o.name] && (o.kind == "r" || o.kind == "p") || o.kind == "S" && (tables[o.parent] || sequences[o.name])
		if !ledger || !known {
			return fmt.Errorf("offline bootstrap refuses an unrecognized legacy relation %q", o.name)
		}
		if o.owner != admin && o.owner != migrator && o.owner != clm && o.owner != auth {
			return errors.New("offline bootstrap refuses foreign legacy relation ownership")
		}
	}
	var foreignType bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname=$1 AND (t.typrelid=0 OR EXISTS(SELECT 1 FROM pg_class c WHERE c.oid=t.typrelid AND c.relkind='c')) AND t.typelem=0 AND NOT EXISTS(SELECT 1 FROM pg_depend d WHERE d.deptype='e' AND (d.classid='pg_type'::regclass AND d.objid=t.oid OR d.classid='pg_class'::regclass AND d.objid=t.typrelid)))`, schema).Scan(&foreignType); err != nil || foreignType {
		return errors.New("offline bootstrap refuses unrecognized standalone types")
	}
	for _, kind := range []string{"trigger", "policy"} {
		query := `SELECT t.tgname FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND NOT t.tgisinternal`
		known := triggers
		if kind == "policy" {
			known = policies
			query = `SELECT p.polname FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1`
		}
		r, e := tx.Query(ctx, query, schema)
		if e != nil {
			return e
		}
		for r.Next() {
			var name string
			if e = r.Scan(&name); e != nil {
				r.Close()
				return e
			}
			if !known[name] {
				r.Close()
				return fmt.Errorf("offline bootstrap refuses an unrecognized legacy %s %q", kind, name)
			}
		}
		e = r.Err()
		r.Close()
		if e != nil {
			return e
		}
	}
	// Exact service binding is mandatory before any delegation to an existing role.
	for role, service := range roles {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, role).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			continue
		}
		if !ledger || !tables["service_database_roles"] {
			return errors.New("existing runtime role requires its application service binding")
		}
		var bound string
		var safe bool
		if err = tx.QueryRow(ctx, `SELECT service FROM service_database_roles WHERE role_name=$1`, role).Scan(&bound); err != nil || bound != service {
			return errors.New("existing runtime role has no exact application service binding")
		}
		if err = tx.QueryRow(ctx, `SELECT rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT EXISTS(SELECT 1 FROM pg_auth_members WHERE member=pg_roles.oid) FROM pg_roles WHERE rolname=$1`, role).Scan(&safe); err != nil || !safe {
			return errors.New("existing runtime role has unsafe attributes or memberships")
		}
		probe, e := connectPoolDSN(ctx, os.Getenv(strings.ToUpper(service)+"_DATABASE_URL"))
		if e != nil {
			return errors.New("existing runtime service credential cannot authenticate; no password was changed")
		}
		var su, cu string
		e = probe.QueryRow(ctx, `SELECT session_user,current_user`).Scan(&su, &cu)
		probe.Close(ctx)
		if e != nil || su != role || cu != su {
			return errors.New("existing runtime service DSN identity mismatch")
		}
		if _, err = tx.Exec(ctx, `GRANT `+pgx.Identifier{role}.Sanitize()+` TO `+pgx.Identifier{migrator}.Sanitize()+` WITH ADMIN TRUE, INHERIT FALSE, SET FALSE`); err != nil {
			return err
		}
	}
	allowed := []string{admin, migrator, clm, auth}
	for r := range roles {
		allowed = append(allowed, r)
	}
	var unsafeSchema bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace n CROSS JOIN LATERAL aclexplode(n.nspacl) a LEFT JOIN pg_roles r ON r.oid=a.grantee WHERE n.nspname=$1 AND NOT (COALESCE(r.rolname=ANY($2::text[]),false) OR COALESCE(r.rolname='pg_database_owner' AND $1='public',false) OR a.grantee=0 AND a.privilege_type='USAGE'))`, schema, allowed).Scan(&unsafeSchema); err != nil || unsafeSchema {
		return errors.New("legacy schema ACL contains an unreviewed grantee")
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace CROSS JOIN LATERAL aclexplode(a.attacl) acl LEFT JOIN pg_roles r ON r.oid=acl.grantee WHERE n.nspname=$1 AND NOT COALESCE(r.rolname=ANY($2::text[]),false))`, schema, allowed).Scan(&unsafeSchema); err != nil || unsafeSchema {
		return errors.New("legacy column ACL contains an unreviewed grantee")
	}
	var unsafe bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace CROSS JOIN LATERAL aclexplode(c.relacl) acl LEFT JOIN pg_roles r ON r.oid=acl.grantee WHERE n.nspname=$1 AND NOT (COALESCE(r.rolname,'PUBLIC')=ANY($2::text[])))`, schema, allowed).Scan(&unsafe); err != nil || unsafe {
		return errors.New("legacy relation ACL contains an unreviewed grantee")
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace CROSS JOIN LATERAL aclexplode(p.proacl) a LEFT JOIN pg_roles r ON r.oid=a.grantee WHERE n.nspname=$1 AND a.grantee<>0 AND NOT COALESCE(r.rolname=ANY($2::text[]),false) AND NOT EXISTS(SELECT 1 FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e'))`, schema, allowed).Scan(&unsafeSchema); err != nil || unsafeSchema {
		return errors.New("legacy function ACL contains an unreviewed grantee")
	}
	rows, err = tx.Query(ctx, `SELECT p.proname,pg_get_userbyid(p.proowner),pg_get_function_identity_arguments(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND NOT EXISTS(SELECT 1 FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e')`, schema)
	if err != nil {
		return err
	}
	var funcs []object
	for rows.Next() {
		var o object
		if err = rows.Scan(&o.name, &o.owner, &o.parent); err != nil {
			rows.Close()
			return err
		}
		funcs = append(funcs, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, o := range funcs {
		if !ledger || !functions[o.name] || o.owner != admin && o.owner != migrator && o.owner != clm && o.owner != auth {
			return errors.New("offline bootstrap refuses an unrecognized or foreign function")
		}
	}
	// Every audit completes before the first ownership transfer. Special NOLOGIN
	// owners retain their objects and their existing privilege verifiers.
	for _, o := range objects {
		if o.owner != admin {
			continue
		}
		kind := "TABLE"
		if o.kind == "S" {
			if o.parent != "" {
				continue
			}
			kind = "SEQUENCE"
		}
		if _, err = tx.Exec(ctx, `ALTER `+kind+` `+pgx.Identifier{schema, o.name}.Sanitize()+` OWNER TO `+pgx.Identifier{migrator}.Sanitize()); err != nil {
			return err
		}
	}
	for _, o := range funcs {
		if o.owner != admin {
			continue
		}
		if _, err = tx.Exec(ctx, `ALTER FUNCTION `+pgx.Identifier{schema, o.name}.Sanitize()+`(`+o.parent+`) OWNER TO `+pgx.Identifier{migrator}.Sanitize()); err != nil {
			return err
		}
	}
	return nil
}

// Only exact SET-only bound owners and ADMIN-only bound application logins may
// be members of this offline identity. Runtime roles never inherit or SET owners.
func verifyOfflineMigratorMemberships(ctx context.Context, tx clmRoleQueryer, database, schema, migrator string) error {
	var unsafe bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.roleid WHERE m.member=$1::regrole AND NOT (r.rolname=ANY($2::text[]) AND NOT m.admin_option AND NOT m.inherit_option AND m.set_option OR m.admin_option AND NOT m.inherit_option AND NOT m.set_option AND r.rolcanlogin AND NOT r.rolsuper AND NOT r.rolbypassrls AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication AND EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$3 AND c.relname='service_database_roles') AND r.rolname=ANY($4::text[])))`, migrator, []string{clmMaintenanceRoleName(database, schema), tenantIdentityOwnerName(database, schema)}, schema, offlineBoundServiceRoles(ctx, tx, schema)).Scan(&unsafe)
	if err != nil {
		return err
	}
	if unsafe {
		return errors.New("offline migrator has unexpected role membership or delegation options")
	}
	return nil
}
func offlineBoundServiceRoles(ctx context.Context, q clmRoleQueryer, schema string) []string {
	var exists bool
	if q.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, pgx.Identifier{schema, "service_database_roles"}.Sanitize()).Scan(&exists) != nil || !exists {
		return nil
	}
	var roles []string
	var database string
	if q.QueryRow(ctx, `SELECT current_database()`).Scan(&database) != nil {
		return nil
	}
	for _, service := range []string{"core", "document", "execution", "model", "tool"} {
		raw := os.Getenv(strings.ToUpper(service) + "_DATABASE_URL")
		target, e := config.ParseDSN(raw)
		cfg, e2 := pgx.ParseConfig(raw)
		if e != nil || e2 != nil || target.Name != database {
			return nil
		}
		var exact bool
		if q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+pgx.Identifier{schema, "service_database_roles"}.Sanitize()+` WHERE role_name=$1 AND service=$2)`, cfg.User, service).Scan(&exact) != nil {
			return nil
		}
		if exact {
			roles = append(roles, cfg.User)
		}
	}
	return roles
}
