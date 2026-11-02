package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"contractaudit/internal/config"

	"github.com/jackc/pgx/v5"
)

// ProvisionTenantIdentityOwner prepares the schema-bound NOLOGIN AUTH_FENCE
// owner using a trusted administrator connection and grants SET-only access to
// the offline migrator. Runtime service roles are never members.
func ProvisionTenantIdentityOwner(ctx context.Context, adminDSN, migratorDSN, schema string) error {
	if ctx == nil || strings.TrimSpace(adminDSN) == "" || strings.TrimSpace(migratorDSN) == "" {
		return errors.New("AUTH_ADMIN_DATABASE_URL and MIGRATOR_DATABASE_URL are required")
	}
	if _, err := quoteIdent(schema); err != nil {
		return fmt.Errorf("invalid tenant identity schema: %w", err)
	}
	adminIdentity, err := config.ParseDSN(adminDSN)
	if err != nil {
		return errors.New("AUTH_ADMIN_DATABASE_URL is invalid")
	}
	migratorIdentity, err := config.ParseDSN(migratorDSN)
	if err != nil || adminIdentity != migratorIdentity {
		return errors.New("AUTH_ADMIN_DATABASE_URL and MIGRATOR_DATABASE_URL must select the same database")
	}
	adminConfig, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		return errors.New("AUTH_ADMIN_DATABASE_URL is invalid")
	}
	migratorConfig, err := pgx.ParseConfig(migratorDSN)
	if err != nil || !identRE.MatchString(migratorConfig.User) || migratorConfig.User == adminConfig.User {
		return errors.New("offline migrator must be a dedicated login distinct from the administrator")
	}
	conn, err := connectPoolDSN(ctx, adminDSN)
	if err != nil {
		return errors.New("cannot connect using the AUTH_FENCE administrator identity")
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("cannot begin AUTH_FENCE provisioning")
	}
	defer tx.Rollback(ctx)
	var database, adminRole string
	if err := tx.QueryRow(ctx, `SELECT current_database(),current_user`).Scan(&database, &adminRole); err != nil {
		return err
	}
	if database != adminConfig.Database {
		return errors.New("AUTH_FENCE administrator database identity changed after connection")
	}
	var adminSuper bool
	if err := tx.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname=$1`, adminRole).Scan(&adminSuper); err != nil || !adminSuper {
		return errors.New("AUTH_FENCE owner pre-provisioning requires a trusted superuser administrator connection")
	}
	var migratorOK bool
	if err := tx.QueryRow(ctx, `SELECT rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb
		AND rolcreaterole AND NOT rolinherit AND NOT rolreplication FROM pg_roles WHERE rolname=$1`, migratorConfig.User).Scan(&migratorOK); err != nil || !migratorOK {
		return errors.New("offline migrator must be a dedicated CREATEROLE login without superuser, BYPASSRLS, CREATEDB, or replication")
	}
	var schemaExists, schemaAccess bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1),
		CASE WHEN EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)
		THEN has_schema_privilege($2,$1,'USAGE') AND has_schema_privilege($2,$1,'CREATE') ELSE false END`, schema, migratorConfig.User).Scan(&schemaExists, &schemaAccess); err != nil {
		return err
	}
	if !schemaExists || !schemaAccess {
		return errors.New("application schema must exist and grant USAGE and CREATE to the offline migrator")
	}
	role := tenantIdentityOwnerName(database, schema)
	quotedRole := pgx.Identifier{role}.Sanitize()
	quotedMigrator := pgx.Identifier{migratorConfig.User}.Sanitize()
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, role).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := tx.Exec(ctx, `CREATE ROLE `+quotedRole+` NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION`); err != nil {
			return fmt.Errorf("cannot create bounded AUTH_FENCE owner: %w", err)
		}
		if _, err := tx.Exec(ctx, `REVOKE `+quotedRole+` FROM `+pgx.Identifier{adminRole}.Sanitize()); err != nil {
			return errors.New("cannot remove administrator membership from AUTH_FENCE owner")
		}
		binding := tenantIdentityOwnerBinding(database, schema)
		if _, err := tx.Exec(ctx, `COMMENT ON ROLE `+quotedRole+` IS '`+strings.ReplaceAll(binding, "'", "''")+`'`); err != nil {
			return errors.New("cannot bind AUTH_FENCE owner to this database and schema")
		}
	} else {
		var binding string
		if err := tx.QueryRow(ctx, `SELECT shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname=$1`, role).Scan(&binding); err != nil || binding != tenantIdentityOwnerBinding(database, schema) {
			return errors.New("existing AUTH_FENCE owner is not bound to this database and schema")
		}
	}
	var ownerSafe bool
	if err := tx.QueryRow(ctx, `SELECT NOT rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb
		AND NOT rolcreaterole AND NOT rolinherit AND NOT rolreplication FROM pg_roles WHERE rolname=$1`, role).Scan(&ownerSafe); err != nil || !ownerSafe {
		return errors.New("existing AUTH_FENCE owner has unsafe role attributes")
	}
	if err := verifyTenantIdentityOwnerPrivileges(ctx, tx, database, schema, role); err != nil {
		return fmt.Errorf("verify existing AUTH_FENCE owner before migrator membership: %w", err)
	}
	var ownerMemberCount int
	var exactMembership bool
	if err := tx.QueryRow(ctx, `SELECT count(*),COALESCE(bool_and(member.rolname=$2 AND NOT membership.admin_option
		AND NOT membership.inherit_option AND membership.set_option),false)
		FROM pg_auth_members membership JOIN pg_roles owner_role ON owner_role.oid=membership.roleid
		JOIN pg_roles member ON member.oid=membership.member WHERE owner_role.rolname=$1`, role, migratorConfig.User).Scan(&ownerMemberCount, &exactMembership); err != nil {
		return err
	}
	if err := verifyOfflineMigratorMemberships(ctx, tx, database, schema, migratorConfig.User); err != nil {
		return err
	}
	if ownerMemberCount == 0 {
		if _, err := tx.Exec(ctx, `GRANT `+quotedRole+` TO `+quotedMigrator+` WITH ADMIN FALSE, INHERIT FALSE, SET TRUE`); err != nil {
			return fmt.Errorf("cannot grant SET-only AUTH_FENCE membership to offline migrator: %w", err)
		}
	} else if ownerMemberCount != 1 || !exactMembership {
		return errors.New("existing AUTH_FENCE owner has additional or elevated memberships")
	}
	var ownerHasMembership bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_auth_members WHERE member=$1::regrole)`, role).Scan(&ownerHasMembership); err != nil {
		return err
	}
	if ownerHasMembership {
		return errors.New("AUTH_FENCE owner must not inherit or assume other roles")
	}
	if err := verifyTenantIdentityOwnerPrivileges(ctx, tx, database, schema, role); err != nil {
		return fmt.Errorf("verify AUTH_FENCE owner after migrator membership: %w", err)
	}
	return tx.Commit(ctx)
}

func verifyTenantIdentityOwnerPrivileges(ctx context.Context, q clmRoleQueryer, database, schema, role string) error {
	// Before 0033 the owner must have no owned objects or explicit object ACLs;
	// after 0033 it may own only its schema-scoped definer functions.
	var unsafe bool
	err := q.QueryRow(ctx, `WITH target AS (
		SELECT role.oid AS role_oid, namespace.oid AS namespace_oid,
			(SELECT oid FROM pg_database WHERE datname=$1) AS database_oid
		FROM pg_roles role CROSS JOIN pg_namespace namespace WHERE role.rolname=$2 AND namespace.nspname=$3
	), allowed_functions AS (
		SELECT unnest(ARRAY[
			to_regprocedure(format('%I.tenant_identity_oidc_login(text,text,text,text,text,text)',$3))::oid,
			to_regprocedure(format('%I.tenant_identity_list_pending(text,text)',$3))::oid,
			to_regprocedure(format('%I.tenant_identity_consume_pending(text,uuid,text,text,text)',$3))::oid,
			to_regprocedure(format('%I.tenant_identity_session_user(text)',$3))::oid,
			to_regprocedure(format('%I.tenant_identity_intake_ticket(text)',$3))::oid,
			to_regprocedure(format('%I.tenant_identity_claim_user(text,text,text,text,bigint,text,text,text,text)',$3))::oid,
			to_regprocedure(format('%I.tenant_identity_bind_admission(text,text)',$3))::oid
		]) AS oid
	), owner_objects AS (
		SELECT class.classid,class.objid,class.objsubid,namespace.nspname
		FROM pg_shdepend class LEFT JOIN pg_class relation ON class.classid='pg_class'::regclass AND class.objid=relation.oid
		LEFT JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
		WHERE class.refclassid='pg_authid'::regclass AND class.refobjid=(SELECT role_oid FROM target)
			AND class.deptype='o'
	)
	SELECT EXISTS(SELECT 1 FROM target WHERE has_schema_privilege(role_oid,namespace_oid,'CREATE'))
		OR EXISTS(SELECT 1 FROM owner_objects WHERE classid<>'pg_proc'::regclass OR nspname<>$3)
		OR EXISTS(SELECT 1 FROM owner_objects WHERE classid='pg_proc'::regclass AND objid NOT IN (SELECT oid FROM allowed_functions WHERE oid IS NOT NULL))
		OR EXISTS(SELECT 1 FROM pg_shdepend dependency WHERE dependency.refclassid='pg_authid'::regclass
			AND dependency.refobjid=(SELECT role_oid FROM target) AND dependency.dbid<>0
			AND dependency.dbid<>(SELECT database_oid FROM target) AND dependency.deptype IN ('o','a'))`, database, role, schema).Scan(&unsafe)
	if err != nil {
		return fmt.Errorf("inspect AUTH_FENCE owner dependencies and schema privileges: %w", err)
	}
	if unsafe {
		return errors.New("AUTH_FENCE owner has unexpected schema privileges, ownership, or cross-database dependencies")
	}
	var aclUnsafe bool
	err = q.QueryRow(ctx, `WITH target AS (
		SELECT oid AS role_oid FROM pg_roles WHERE rolname=$1
	), expected AS (
		SELECT * FROM (VALUES
			('identity_jurisdictions','jurisdiction_id','SELECT'),('identity_jurisdictions','display_name','SELECT'),('identity_jurisdictions','status','SELECT'),('identity_jurisdictions','revision','SELECT'),
			('identity_tenants','tenant_id','SELECT'),('identity_tenants','display_name','SELECT'),('identity_tenants','jurisdiction_id','SELECT'),('identity_tenants','status','SELECT'),('identity_tenants','revision','SELECT'),
			('identity_org_units','tenant_id','SELECT'),('identity_org_units','org_unit_id','SELECT'),('identity_org_units','display_name','SELECT'),('identity_org_units','status','SELECT'),('identity_org_units','revision','SELECT'),
			('identity_principals','principal_id','SELECT'),('identity_principals','actor_id','SELECT'),('identity_principals','issuer','SELECT'),('identity_principals','subject','SELECT'),('identity_principals','status','SELECT'),('identity_principals','revision','SELECT'),
			('identity_memberships','membership_id','SELECT'),('identity_memberships','principal_id','SELECT'),('identity_memberships','tenant_id','SELECT'),('identity_memberships','org_unit_id','SELECT'),('identity_memberships','role_code','SELECT'),('identity_memberships','grants','SELECT'),('identity_memberships','status','SELECT'),('identity_memberships','effective_from','SELECT'),('identity_memberships','effective_until','SELECT'),('identity_memberships','revision','SELECT'),
			('identity_pending_auth','token_digest','SELECT'),('identity_pending_auth','principal_id','SELECT'),('identity_pending_auth','audience','SELECT'),('identity_pending_auth','expires_at','SELECT'),('identity_pending_auth','consumed_at','SELECT'),
			('identity_pending_auth','token_digest','INSERT'),('identity_pending_auth','principal_id','INSERT'),('identity_pending_auth','audience','INSERT'),('identity_pending_auth','created_at','INSERT'),('identity_pending_auth','expires_at','INSERT'),('identity_pending_auth','consumed_at','UPDATE'),
			('sessions','token','SELECT'),('sessions','actor_id','SELECT'),('sessions','issuer','SELECT'),('sessions','subject','SELECT'),('sessions','tenant_id','SELECT'),('sessions','org_id','SELECT'),('sessions','audience','SELECT'),('sessions','expires_at','SELECT'),('sessions','principal_id','SELECT'),('sessions','membership_id','SELECT'),
			('sessions','token','INSERT'),('sessions','actor_id','INSERT'),('sessions','issuer','INSERT'),('sessions','subject','INSERT'),('sessions','tenant_id','INSERT'),('sessions','org_id','INSERT'),('sessions','audience','INSERT'),('sessions','expires_at','INSERT'),('sessions','principal_id','INSERT'),('sessions','membership_id','INSERT'),
			('sessions_seq_seq',NULL,'USAGE'),
			('intake_authority_tickets','ticket_digest','SELECT'),('intake_authority_tickets','tenant_id','SELECT'),
			('intake_authority_tickets','operation_id','SELECT'),('intake_authority_tickets','session_digest','SELECT'),
			('intake_authority_tickets','principal_id','SELECT'),('intake_authority_tickets','membership_id','SELECT'),
			('intake_authority_tickets','actor_id','SELECT'),('intake_authority_tickets','org_id','SELECT'),
			('intake_authority_tickets','case_id','SELECT'),('intake_authority_tickets','upload_key','SELECT'),
			('intake_authority_tickets','filename','SELECT'),('intake_authority_tickets','media_type','SELECT'),
			('intake_authority_tickets','body_sha256','SELECT'),('intake_authority_tickets','action','SELECT'),
			('intake_authority_tickets','expires_at','SELECT'),
			('worker_claim_identities','tenant_id','SELECT'),('worker_claim_identities','job_id','SELECT'),
			('worker_claim_identities','org_id','SELECT'),('worker_claim_identities','case_id','SELECT'),
			('worker_claim_identities','run_id','SELECT'),('worker_claim_identities','attempt_id','SELECT'),
			('worker_claim_identities','actor_id','SELECT'),('worker_claim_identities','principal_id','SELECT'),
			('worker_claim_identities','membership_id','SELECT'),('worker_claim_identities','membership_org_id','SELECT'),
			('worker_claim_identities','tenant_id','INSERT'),('worker_claim_identities','job_id','INSERT'),
			('worker_claim_identities','org_id','INSERT'),('worker_claim_identities','case_id','INSERT'),
			('worker_claim_identities','run_id','INSERT'),('worker_claim_identities','attempt_id','INSERT'),
			('worker_claim_identities','actor_id','INSERT'),('worker_claim_identities','principal_id','INSERT'),
			('worker_claim_identities','membership_id','INSERT'),('worker_claim_identities','membership_org_id','INSERT'),
			('worker_jobs','id','SELECT'),('worker_jobs','tenant_id','SELECT'),('worker_jobs','org_id','SELECT'),
			('worker_jobs','case_id','SELECT'),('worker_jobs','run_id','SELECT'),('worker_jobs','attempt_id','SELECT'),
			('worker_jobs','actor_id','SELECT'),('worker_jobs','worker_id','SELECT'),('worker_jobs','generation','SELECT'),
			('worker_jobs','state','SELECT'),('worker_jobs','lease_until','SELECT'),('worker_jobs','kind','SELECT'),
			('cases','tenant_id','SELECT'),('cases','org_id','SELECT'),('cases','id','SELECT'),
			('cases','owner_id','SELECT'),('cases','member_ids','SELECT'),('cases','archived_at','SELECT'),
			('review_runs','tenant_id','SELECT'),('review_runs','org_id','SELECT'),('review_runs','case_id','SELECT'),
			('review_runs','id','SELECT'),('review_runs','state','SELECT'),
			('review_attempts','tenant_id','SELECT'),('review_attempts','org_id','SELECT'),
			('review_attempts','run_id','SELECT'),('review_attempts','id','SELECT'),
			('review_attempts','history_kind','SELECT'),('review_attempts','state','SELECT'),('review_attempts','input_hash','SELECT')
		) AS item(table_name,column_name,privilege_type)
	), actual AS (
		SELECT namespace.nspname AS schema_name,relation.relname AS table_name,NULL::text AS column_name,acl.privilege_type,acl.is_grantable
		FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
		CROSS JOIN target CROSS JOIN LATERAL aclexplode(relation.relacl) acl
		WHERE relation.relacl IS NOT NULL AND acl.grantee=target.role_oid
		UNION ALL
		SELECT namespace.nspname,relation.relname,attribute.attname,acl.privilege_type,acl.is_grantable
		FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
		JOIN pg_attribute attribute ON attribute.attrelid=relation.oid AND attribute.attnum>0 AND NOT attribute.attisdropped
		CROSS JOIN target CROSS JOIN LATERAL aclexplode(attribute.attacl) acl
		WHERE attribute.attacl IS NOT NULL AND acl.grantee=target.role_oid
	)
	SELECT EXISTS(SELECT 1 FROM actual WHERE schema_name<>$2 OR is_grantable OR NOT EXISTS (
		SELECT 1 FROM expected WHERE expected.table_name=actual.table_name AND expected.column_name IS NOT DISTINCT FROM actual.column_name
		AND expected.privilege_type=actual.privilege_type))
		OR (to_regclass(format('%I.identity_principals',$2)) IS NOT NULL AND EXISTS(SELECT 1 FROM expected WHERE to_regclass(format('%I.%I',$2,expected.table_name)) IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM actual WHERE schema_name=$2 AND actual.table_name=expected.table_name
			AND actual.column_name IS NOT DISTINCT FROM expected.column_name AND actual.privilege_type=expected.privilege_type)))`, role, schema).Scan(&aclUnsafe)
	if err != nil {
		return fmt.Errorf("inspect AUTH_FENCE owner object grants: %w", err)
	}
	if aclUnsafe {
		return errors.New("AUTH_FENCE owner has unexpected or missing table/column privileges")
	}
	var policyUnsafe bool
	err = q.QueryRow(ctx, `WITH target AS (
		SELECT oid AS role_oid FROM pg_roles WHERE rolname=$1
	), expected AS (
		SELECT * FROM (VALUES
			('worker_claim_identities','worker_claim_identity_owner_read'),
			('worker_claim_identities','worker_claim_identity_owner_insert'),
			('worker_jobs','worker_identity_claim_owner_read'),
			('cases','case_identity_claim_owner_read'),
			('review_runs','run_identity_claim_owner_read'),
			('review_attempts','attempt_identity_claim_owner_read')
		) AS item(table_name,policy_name)
	), actual AS (
		SELECT relation.relname AS table_name,policy.polname AS policy_name,policy.polcmd,policy.polroles,
			pg_get_expr(policy.polqual,policy.polrelid) AS using_expr,pg_get_expr(policy.polwithcheck,policy.polrelid) AS check_expr
		FROM pg_policy policy JOIN pg_class relation ON relation.oid=policy.polrelid
		JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
		CROSS JOIN target
		WHERE namespace.nspname=$2 AND target.role_oid=ANY(policy.polroles)
			AND relation.relname IN ('worker_claim_identities','worker_jobs','cases','review_runs','review_attempts')
	)
	SELECT EXISTS(SELECT 1 FROM expected WHERE to_regclass(format('%I.worker_claim_identities',$2)) IS NOT NULL AND NOT EXISTS(
		SELECT 1 FROM actual,target WHERE actual.table_name=expected.table_name AND actual.policy_name=expected.policy_name
			AND actual.polroles=ARRAY[target.role_oid]
			AND ((expected.policy_name='worker_claim_identity_owner_read' AND actual.polcmd='r' AND actual.using_expr='true' AND actual.check_expr IS NULL)
			 OR (expected.policy_name='worker_claim_identity_owner_insert' AND actual.polcmd='a' AND actual.using_expr IS NULL AND actual.check_expr='true')
			 OR (expected.policy_name<>'worker_claim_identity_owner_read' AND expected.policy_name<>'worker_claim_identity_owner_insert' AND actual.polcmd='r' AND actual.using_expr='true' AND actual.check_expr IS NULL))))
		OR (SELECT count(*) FROM actual)<>CASE WHEN to_regclass(format('%I.worker_claim_identities',$2)) IS NULL THEN 0 ELSE 6 END
		OR EXISTS(SELECT 1 FROM (VALUES ('worker_claim_identities'),('worker_jobs'),('cases'),('review_runs'),('review_attempts')) AS tables(table_name)
			JOIN pg_class relation ON relation.relname=tables.table_name
			JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace AND namespace.nspname=$2
			WHERE NOT relation.relrowsecurity OR NOT relation.relforcerowsecurity)`, role, schema).Scan(&policyUnsafe)
	if err != nil {
		return fmt.Errorf("inspect AUTH_FENCE owner RLS policy boundary: %w", err)
	}
	if policyUnsafe {
		return errors.New("AUTH_FENCE owner has unexpected or missing claim-read policies or RLS state")
	}
	return nil
}
