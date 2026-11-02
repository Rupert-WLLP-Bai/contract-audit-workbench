package store

import (
	"context"
	"errors"
)

// Context functions are a second explicit authority surface; adding a function
// must not inherit PUBLIC execution or widen a non-Core service's identity read.
func verifyContextFunctionACL(ctx context.Context, q clmRoleQueryer, schema, owner, core string, roles []string) error {
	signatures := []string{"tenant_context_info(text)", "tenant_context_fence(text,bigint,text,uuid,uuid)", "tenant_context_switch(text,bigint,text,uuid)", "tenant_context_list(text)", "tenant_operation_info(jsonb,text)", "tenant_operation_fence(jsonb,text,text)", "tenant_support_can_manage(text,text)", "tenant_support_json(uuid)", "tenant_support_list(text)", "tenant_support_command(text,bigint,text,text,uuid,text,uuid,text[],text,timestamp with time zone,bigint)", "tenant_support_validate(text,uuid)", "tenant_support_read(text,bigint,text,uuid)"}
	scopes := []string{"core", "all", "core", "core", "core", "all", "owner", "owner", "core", "core", "core", "core"}
	var unsafe bool
	err := q.QueryRow(ctx, `WITH expected AS(SELECT to_regprocedure(format('%I.%s',$1::text,x.signature))::oid AS oid,x.scope FROM unnest($5::text[],$6::text[]) x(signature,scope)),actual AS(SELECT e.oid,e.scope,p.proowner,a.grantee,a.privilege_type FROM expected e JOIN pg_proc p ON p.oid=e.oid CROSS JOIN LATERAL aclexplode(COALESCE(p.proacl,acldefault('f',p.proowner))) a)
 SELECT (SELECT count(*) FROM expected WHERE oid IS NOT NULL)<>12
 OR EXISTS(SELECT 1 FROM actual WHERE proowner<>$2::regrole OR privilege_type<>'EXECUTE' OR grantee=0 OR NOT(grantee=$2::regrole OR scope='core' AND grantee=$3::regrole OR scope='all' AND grantee=ANY(ARRAY(SELECT rolname::regrole::oid FROM pg_roles WHERE rolname=ANY($4::text[])))))
 OR EXISTS(SELECT 1 FROM expected e WHERE e.scope='core' AND NOT has_function_privilege($3::text,e.oid,'EXECUTE'))
 OR EXISTS(SELECT 1 FROM expected e CROSS JOIN unnest($4::text[]) r(role) WHERE e.scope='all' AND NOT has_function_privilege(r.role,e.oid,'EXECUTE'))`, schema, owner, core, roles, signatures, scopes).Scan(&unsafe)
	if err != nil {
		return err
	}
	if unsafe {
		return errors.New("context authority functions have missing or unexpected execution privileges")
	}
	return nil
}
