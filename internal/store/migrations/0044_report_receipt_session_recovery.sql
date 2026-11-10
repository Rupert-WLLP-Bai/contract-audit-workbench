-- Report receipts may be recovered only by the original actor in the original
-- validated session organization, with current Export authority. Legacy rows
-- deliberately retain NULL origin because their source cannot be inferred.
ALTER TABLE {{SCHEMA}}.core_write_receipts
 ADD COLUMN command_origin_org text CHECK (command_origin_org IS NULL OR btrim(command_origin_org)<>'');
GRANT SELECT (tenant_id,org_id,case_id,actor_id,route,idempotency_key,command_session,command_version,command_origin_org)
 ON {{SCHEMA}}.core_write_receipts TO {{AUTH_FENCE_OWNER}};

GRANT USAGE,CREATE ON SCHEMA {{SCHEMA}} TO {{AUTH_FENCE_OWNER}};
SET LOCAL ROLE {{AUTH_FENCE_OWNER}};

CREATE FUNCTION {{SCHEMA}}.stamp_report_receipt_origin() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE digest text:=NULLIF(current_setting('app.command_digest',true),'');
        version text:=NULLIF(current_setting('app.command_version',true),''); actual record;
BEGIN
 IF NEW.route IS DISTINCT FROM 'POST /api/cases/'||NEW.case_id||'/reports' THEN
  NEW.command_origin_org:=NULL;
  RETURN NEW;
 END IF;
 IF digest IS NULL THEN
  NEW.command_origin_org:=NULL;
  RETURN NEW;
 END IF;
 SELECT u.actor_id,u.tenant_id,u.org_id,u.context_version,s.audience INTO actual
 FROM {{SCHEMA}}.tenant_context_info(digest) u JOIN {{SCHEMA}}.sessions s ON s.token=digest;
 IF NOT FOUND OR actual.audience<>'client' OR actual.context_version::text IS DISTINCT FROM version
   OR actual.actor_id IS DISTINCT FROM NEW.actor_id OR actual.tenant_id IS DISTINCT FROM NEW.tenant_id
   OR NEW.command_session IS DISTINCT FROM digest OR NEW.command_version::text IS DISTINCT FROM version
   OR COALESCE(actual.org_id,'')='' THEN
  RAISE EXCEPTION 'context_stale' USING ERRCODE='40001';
 END IF;
 NEW.command_origin_org:=actual.org_id;
 RETURN NEW;
END $fn$;

CREATE FUNCTION {{SCHEMA}}.tenant_report_receipt_recovery_allowed(
 p_tenant text,p_org text,p_case text,p_actor text,p_route text,p_key text,p_origin_org text)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
 SELECT EXISTS (
  SELECT 1 FROM {{SCHEMA}}.tenant_context_info(NULLIF(current_setting('app.command_digest',true),'')) u
  JOIN {{SCHEMA}}.sessions s ON s.token=NULLIF(current_setting('app.command_digest',true),'')
  JOIN {{SCHEMA}}.cases c ON c.id=p_case AND c.tenant_id=p_tenant AND c.org_id=p_org
  WHERE (EXISTS (SELECT 1 FROM {{SCHEMA}}.service_database_roles sr WHERE sr.role_name=session_user AND sr.service='core')
      OR session_user=pg_get_userbyid((SELECT relowner FROM pg_catalog.pg_class WHERE oid='{{SCHEMA}}.core_write_receipts'::regclass)))
    AND p_route='POST /api/cases/'||p_case||'/reports'
    AND NULLIF(current_setting('app.report_receipt_route',true),'')=p_route
    AND NULLIF(current_setting('app.report_receipt_key',true),'')=p_key
    AND s.audience='client' AND u.context_version::text=NULLIF(current_setting('app.command_version',true),'')
    AND u.tenant_id=p_tenant AND p_origin_org IS NOT NULL AND u.org_id=p_origin_org AND u.actor_id=p_actor
    AND c.archived_at IS NULL AND c.business_status<>'archived'
    AND (
      (u.principal_id IS NULL AND c.member_ids::jsonb ? p_actor AND
       (p_tenant='local-demo' AND p_actor IN ('demo-admin','demo-clerk','demo-otherorg-clerk')
        OR p_tenant='local-demo-2' AND p_actor IN ('demo-tenant2-admin','demo-tenant2-clerk')))
      OR (u.principal_id IS NOT NULL AND EXISTS (
       SELECT 1 FROM jsonb_array_elements(u.grants) g
       WHERE g->>'role' IN ('case_owner','exporter')
        AND (g->>'expires_at' IS NULL OR g->>'expires_at' IN ('','0001-01-01T00:00:00Z') OR (g->>'expires_at')::timestamptz>clock_timestamp())
        AND (g->>'scope'='tenant' OR g->>'scope'='self' AND c.owner_id=p_actor
          OR g->>'scope'='case' AND g->>'case_id'=p_case
          OR g->>'scope'='org' AND g->>'org_id'=p_org
          OR g->>'scope'='org_subtree' AND COALESCE(g->'resolved_org_ids','[]'::jsonb) ? p_org)
      ))
    )
 );
$fn$;

-- Do not expose receipt bodies for rows without a trusted origin. This boolean
-- reports only that the exact key is occupied by an unreplayable old origin.
CREATE FUNCTION {{SCHEMA}}.tenant_report_receipt_key_conflict(
 p_tenant text,p_org text,p_case text,p_actor text,p_route text,p_key text)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
 SELECT EXISTS (
  SELECT 1 FROM {{SCHEMA}}.tenant_context_info(NULLIF(current_setting('app.command_digest',true),'')) u
  WHERE {{SCHEMA}}.tenant_report_receipt_recovery_allowed(p_tenant,p_org,p_case,p_actor,p_route,p_key,u.org_id)
   AND EXISTS (SELECT 1 FROM {{SCHEMA}}.core_write_receipts w
    WHERE w.tenant_id=p_tenant AND w.org_id=p_org AND w.case_id=p_case AND w.actor_id=p_actor
     AND w.route=p_route AND w.idempotency_key=p_key
     AND NOT COALESCE(w.command_origin_org=u.org_id,false))
 );
$fn$;

REVOKE ALL ON FUNCTION {{SCHEMA}}.stamp_report_receipt_origin(),
 {{SCHEMA}}.tenant_report_receipt_recovery_allowed(text,text,text,text,text,text,text),
 {{SCHEMA}}.tenant_report_receipt_key_conflict(text,text,text,text,text,text) FROM PUBLIC;

DO $grant_roles$
DECLARE relation_owner name; runtime_role record;
BEGIN
 SELECT pg_get_userbyid(relowner) INTO relation_owner FROM pg_catalog.pg_class WHERE oid='{{SCHEMA}}.core_write_receipts'::regclass;
 EXECUTE format('GRANT EXECUTE ON FUNCTION %I.stamp_report_receipt_origin() TO %I',current_schema(),session_user);
 EXECUTE format('GRANT EXECUTE ON FUNCTION %I.tenant_report_receipt_recovery_allowed(text,text,text,text,text,text,text) TO %I',current_schema(),relation_owner);
 EXECUTE format('GRANT EXECUTE ON FUNCTION %I.tenant_report_receipt_key_conflict(text,text,text,text,text,text) TO %I',current_schema(),relation_owner);
 FOR runtime_role IN SELECT role_name,service FROM {{SCHEMA}}.service_database_roles LOOP
  EXECUTE format('GRANT EXECUTE ON FUNCTION %I.tenant_report_receipt_recovery_allowed(text,text,text,text,text,text,text) TO %I',current_schema(),runtime_role.role_name);
  IF runtime_role.service='core' THEN
   EXECUTE format('GRANT EXECUTE ON FUNCTION %I.tenant_report_receipt_key_conflict(text,text,text,text,text,text) TO %I',current_schema(),runtime_role.role_name);
  END IF;
 END LOOP;
END $grant_roles$;
RESET ROLE;
REVOKE CREATE ON SCHEMA {{SCHEMA}} FROM {{AUTH_FENCE_OWNER}};

CREATE TRIGGER report_receipt_origin BEFORE INSERT ON {{SCHEMA}}.core_write_receipts
 FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.stamp_report_receipt_origin();
ALTER POLICY command_receipt_context ON {{SCHEMA}}.core_write_receipts
 USING (NULLIF(current_setting('app.command_digest',true),'') IS NULL
   OR command_session=current_setting('app.command_digest',true) AND command_version=NULLIF(current_setting('app.command_version',true),'')::bigint
   OR current_user={{AUTH_FENCE_OWNER_LITERAL}} AND tenant_id=current_setting('app.tenant_id',true)
     AND actor_id=current_setting('app.actor_id',true) AND route='POST /api/cases/'||case_id||'/reports'
     AND route=current_setting('app.report_receipt_route',true) AND idempotency_key=current_setting('app.report_receipt_key',true)
   OR CASE WHEN NULLIF(current_setting('app.report_receipt_key',true),'') IS NOT NULL
     THEN {{SCHEMA}}.tenant_report_receipt_recovery_allowed(tenant_id,org_id,case_id,actor_id,route,idempotency_key,command_origin_org) ELSE false END);
SET LOCAL ROLE {{AUTH_FENCE_OWNER}};
DO $revoke_trigger_grant$
BEGIN
 EXECUTE format('REVOKE EXECUTE ON FUNCTION %I.stamp_report_receipt_origin() FROM %I',current_schema(),session_user);
END $revoke_trigger_grant$;
RESET ROLE;
