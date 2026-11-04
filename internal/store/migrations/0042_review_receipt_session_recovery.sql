-- An already accepted review may be replayed by its original actor after a
-- new login. The existing receipt stamp and insertion check remain unchanged.
GRANT USAGE,CREATE ON SCHEMA {{SCHEMA}} TO {{AUTH_FENCE_OWNER}};
SET LOCAL ROLE {{AUTH_FENCE_OWNER}};
CREATE FUNCTION {{SCHEMA}}.tenant_review_receipt_recovery_allowed(p_tenant text,p_org text,p_case text,p_actor text,p_route text,p_key text)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
 SELECT EXISTS (
  SELECT 1 FROM {{SCHEMA}}.tenant_context_info(NULLIF(current_setting('app.command_digest',true),'')) u
  JOIN {{SCHEMA}}.sessions s ON s.token=NULLIF(current_setting('app.command_digest',true),'')
  JOIN {{SCHEMA}}.cases c ON c.id=p_case AND c.tenant_id=p_tenant AND c.org_id=p_org
  WHERE (EXISTS (SELECT 1 FROM {{SCHEMA}}.service_database_roles sr WHERE sr.role_name=session_user AND sr.service='execution')
      OR session_user=pg_get_userbyid((SELECT relowner FROM pg_catalog.pg_class WHERE oid='{{SCHEMA}}.execution_write_receipts'::regclass)))
    AND p_route='POST /api/cases/'||p_case||'/reviews'
    AND NULLIF(current_setting('app.review_receipt_route',true),'')=p_route
    AND NULLIF(current_setting('app.review_receipt_key',true),'')=p_key
    AND s.audience='client' AND u.context_version::text=NULLIF(current_setting('app.command_version',true),'')
    AND u.tenant_id=p_tenant AND u.org_id=p_org AND u.actor_id=p_actor
    AND c.archived_at IS NULL AND c.business_status<>'archived'
    AND (
      (u.principal_id IS NULL AND c.member_ids::jsonb ? p_actor AND
       (p_tenant='local-demo' AND p_actor IN ('demo-admin','demo-clerk','demo-otherorg-clerk','demo-reviewer')
        OR p_tenant='local-demo-2' AND p_actor IN ('demo-tenant2-admin','demo-tenant2-clerk','demo-tenant2-reviewer')))
      OR (u.principal_id IS NOT NULL AND u.role_code IN ('admin','clerk','reviewer') AND EXISTS (
       SELECT 1 FROM jsonb_array_elements(u.grants) g
       WHERE g->>'role' IN ('case_owner','reviewer')
        AND (g->>'expires_at' IS NULL OR g->>'expires_at' IN ('','0001-01-01T00:00:00Z') OR (g->>'expires_at')::timestamptz>clock_timestamp())
        AND (g->>'scope'='tenant' OR g->>'scope'='self' AND c.owner_id=p_actor
          OR g->>'scope'='case' AND g->>'case_id'=p_case
          OR g->>'scope'='org' AND g->>'org_id'=p_org
          OR g->>'scope'='org_subtree' AND COALESCE(g->'resolved_org_ids','[]'::jsonb) ? p_org)
      ))
    )
 );
$fn$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_review_receipt_recovery_allowed(text,text,text,text,text,text) FROM PUBLIC;
-- The table owner is also the supported embedded login and may already alter
-- its policy. Other actual logins are checked against service_database_roles.
DO $grant_owner$
DECLARE relation_owner name;
BEGIN
 SELECT pg_get_userbyid(relowner) INTO relation_owner FROM pg_catalog.pg_class WHERE oid='{{SCHEMA}}.execution_write_receipts'::regclass;
 EXECUTE format('GRANT EXECUTE ON FUNCTION %I.tenant_review_receipt_recovery_allowed(text,text,text,text,text,text) TO %I',current_schema(),relation_owner);
END $grant_owner$;
RESET ROLE;
REVOKE CREATE ON SCHEMA {{SCHEMA}} FROM {{AUTH_FENCE_OWNER}};

ALTER POLICY command_receipt_context ON {{SCHEMA}}.execution_write_receipts
 USING (NULLIF(current_setting('app.command_digest',true),'') IS NULL
   OR command_session=current_setting('app.command_digest',true) AND command_version=NULLIF(current_setting('app.command_version',true),'')::bigint
   OR CASE WHEN NULLIF(current_setting('app.review_receipt_key',true),'') IS NOT NULL
     THEN {{SCHEMA}}.tenant_review_receipt_recovery_allowed(tenant_id,org_id,case_id,actor_id,route,idempotency_key) ELSE false END);
