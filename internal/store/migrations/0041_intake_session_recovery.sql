-- A continuation is an exact, current-session operation. The existing command
-- stamps remain immutable; only the selected original owner's intake and its
-- Document upload receipt may be observed across sessions.
CREATE TABLE {{SCHEMA}}.case_intake_completion_events (
 tenant_id text NOT NULL,
 operation_id text NOT NULL,
 actor_id text NOT NULL,
 request_digest text NOT NULL,
 response_digest text NOT NULL,
 session_digest text,
 context_version bigint,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,operation_id),
 FOREIGN KEY (tenant_id,operation_id) REFERENCES {{SCHEMA}}.case_intake_operations(tenant_id,operation_id),
 CHECK ((session_digest IS NULL AND context_version IS NULL) OR
        (session_digest ~ '^sha256:[0-9a-f]{64}$' AND context_version > 0))
);
CREATE TRIGGER intake_completion_append_only BEFORE UPDATE OR DELETE ON {{SCHEMA}}.case_intake_completion_events
 FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.forbid_append_only();
ALTER TABLE {{SCHEMA}}.case_intake_completion_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.case_intake_completion_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON {{SCHEMA}}.case_intake_completion_events
 USING (tenant_id=current_setting('app.tenant_id',true))
 WITH CHECK (tenant_id=current_setting('app.tenant_id',true));
CREATE POLICY intake_completion_owner ON {{SCHEMA}}.case_intake_completion_events TO {{AUTH_FENCE_OWNER}}
 USING (true) WITH CHECK (true);
GRANT SELECT,INSERT ON {{SCHEMA}}.case_intake_completion_events TO {{AUTH_FENCE_OWNER}};
GRANT SELECT ON {{SCHEMA}}.case_intake_operations TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (role_name,service) ON {{SCHEMA}}.service_database_roles TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,operation_id,schema_version,request_json) ON {{SCHEMA}}.case_intake_requests TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,org_id,case_id,id,role,filename,media_type,digest,size,origin) ON {{SCHEMA}}.documents TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,org_id,case_id,material_id,role,purpose) ON {{SCHEMA}}.material_entities TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,org_id,case_id,material_id,version,document_id) ON {{SCHEMA}}.material_versions TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,org_id,case_id,actor_id,idempotency_key,document_id,material_id,command_session,command_version) ON {{SCHEMA}}.document_upload_idempotency TO {{AUTH_FENCE_OWNER}};
CREATE POLICY intake_recovery_owner ON {{SCHEMA}}.case_intake_operations FOR SELECT TO {{AUTH_FENCE_OWNER}} USING (true);
GRANT USAGE,CREATE ON SCHEMA {{SCHEMA}} TO {{AUTH_FENCE_OWNER}};
SET LOCAL ROLE {{AUTH_FENCE_OWNER}};

CREATE FUNCTION {{SCHEMA}}.tenant_intake_recovery_allowed(p_tenant text,p_org text,p_case text,p_actor text,p_operation text,p_key text)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
 SELECT EXISTS (
  SELECT 1 FROM {{SCHEMA}}.tenant_context_info(NULLIF(current_setting('app.command_digest',true),'')) u
  JOIN {{SCHEMA}}.sessions s ON s.token=NULLIF(current_setting('app.command_digest',true),'')
  JOIN {{SCHEMA}}.cases c ON c.id=p_case AND c.tenant_id=p_tenant AND c.org_id=p_org
  WHERE s.audience='client' AND u.context_version::text=NULLIF(current_setting('app.command_version',true),'')
    AND u.tenant_id=p_tenant AND u.org_id=p_org AND u.actor_id=p_actor
    AND (NULLIF(current_setting('app.intake_operation',true),'')=p_operation
      OR NULLIF(current_setting('app.intake_key',true),'')=p_key)
    AND c.archived_at IS NULL AND c.business_status<>'archived'
    AND (
      (u.principal_id IS NULL AND c.member_ids::jsonb ? p_actor AND
       (p_tenant='local-demo' AND p_actor IN ('demo-admin','demo-clerk','demo-otherorg-clerk')
        OR p_tenant='local-demo-2' AND p_actor IN ('demo-tenant2-admin','demo-tenant2-clerk')))
      OR (u.principal_id IS NOT NULL AND u.role_code IN ('admin','clerk') AND EXISTS (
       SELECT 1 FROM jsonb_array_elements(u.grants) g
       WHERE g->>'role'='case_owner'
        AND (g->>'expires_at' IS NULL OR g->>'expires_at' IN ('','0001-01-01T00:00:00Z') OR (g->>'expires_at')::timestamptz>clock_timestamp())
        AND (g->>'scope'='tenant' OR g->>'scope'='self' AND c.owner_id=p_actor
          OR g->>'scope'='case' AND g->>'case_id'=p_case
          OR g->>'scope'='org' AND g->>'org_id'=p_org
          OR g->>'scope'='org_subtree' AND COALESCE(g->'resolved_org_ids','[]'::jsonb) ? p_org)
      ))
    )
 );
$fn$;

-- Ordinary receipt reads retain case Read access; personal list queries are
-- additionally restricted to the originating actor and current organization.
CREATE FUNCTION {{SCHEMA}}.tenant_intake_read_allowed(p_tenant text,p_org text,p_case text,p_actor text,p_operation text,p_key text)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
 SELECT EXISTS (
  SELECT 1 FROM {{SCHEMA}}.tenant_context_info(NULLIF(current_setting('app.command_digest',true),'')) u
  JOIN {{SCHEMA}}.sessions s ON s.token=NULLIF(current_setting('app.command_digest',true),'')
  JOIN {{SCHEMA}}.cases c ON c.id=p_case AND c.tenant_id=p_tenant AND c.org_id=p_org
  WHERE s.audience='client' AND u.context_version::text=NULLIF(current_setting('app.command_version',true),'') AND u.tenant_id=p_tenant
    AND (NULLIF(current_setting('app.intake_operation',true),'')=p_operation
      OR NULLIF(current_setting('app.intake_key',true),'')=p_key
      OR NULLIF(current_setting('app.intake_list_actor',true),'')=u.actor_id AND p_actor=u.actor_id AND u.org_id=p_org)
    AND (
      (u.principal_id IS NULL AND c.member_ids::jsonb ? u.actor_id AND
       (p_tenant='local-demo' AND u.actor_id IN ('demo-admin','demo-clerk','demo-otherorg-clerk','demo-reviewer','demo-viewer')
        OR p_tenant='local-demo-2' AND u.actor_id IN ('demo-tenant2-admin','demo-tenant2-clerk','demo-tenant2-reviewer','demo-tenant2-viewer')))
      OR (u.principal_id IS NOT NULL AND EXISTS (
       SELECT 1 FROM jsonb_array_elements(u.grants) g
       WHERE g->>'role' IN ('case_owner','reviewer','viewer','exporter')
        AND (g->>'expires_at' IS NULL OR g->>'expires_at' IN ('','0001-01-01T00:00:00Z') OR (g->>'expires_at')::timestamptz>clock_timestamp())
        AND (g->>'scope'='tenant' OR g->>'scope'='self' AND c.owner_id=u.actor_id
          OR g->>'scope'='case' AND g->>'case_id'=p_case
          OR g->>'scope'='org' AND g->>'org_id'=p_org
          OR g->>'scope'='org_subtree' AND COALESCE(g->'resolved_org_ids','[]'::jsonb) ? p_org)
      ))
    )
 );
$fn$;

CREATE FUNCTION {{SCHEMA}}.tenant_intake_upload_recovery_allowed(p_tenant text,p_org text,p_case text,p_actor text,p_key text)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
 SELECT EXISTS (
  SELECT 1 FROM {{SCHEMA}}.case_intake_operations o
  -- Embedded non-production deployments use the actual table owner as their
  -- single database login. That role can already alter this policy; a merely
  -- privileged or unregistered login does not become an embedded identity.
  WHERE (EXISTS (SELECT 1 FROM {{SCHEMA}}.service_database_roles sr WHERE sr.role_name=session_user AND sr.service IN ('core','document'))
      OR session_user=pg_get_userbyid((SELECT c.relowner FROM pg_catalog.pg_class c WHERE c.oid='{{SCHEMA}}.case_intake_operations'::regclass)))
    AND o.tenant_id=p_tenant AND o.org_id=p_org AND o.case_id=p_case AND o.actor_id=p_actor
    AND o.upload_idempotency_key=p_key AND NULLIF(current_setting('app.intake_upload_key',true),'')=p_key
    AND {{SCHEMA}}.tenant_intake_recovery_allowed(o.tenant_id,o.org_id,o.case_id,o.actor_id,o.operation_id,o.idempotency_key)
 );
$fn$;

-- This trigger does not impersonate the originating session: its stamp stays
-- unchanged. The actual completing session is appended in the same transaction.
CREATE FUNCTION {{SCHEMA}}.stamp_intake_completion() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE digest text:=NULLIF(current_setting('app.command_digest',true),''); version bigint:=NULLIF(current_setting('app.command_version',true),'')::bigint; receipt jsonb;
BEGIN
 IF TG_OP='INSERT' THEN NEW.command_session:=digest; NEW.command_version:=version; RETURN NEW; END IF;
 IF OLD.status<>'pending' OR NEW.status<>'completed' OR NEW.response_status<>201
   OR ROW(NEW.tenant_id,NEW.org_id,NEW.operation_id,NEW.actor_id,NEW.idempotency_key,NEW.request_digest,NEW.case_id,NEW.upload_idempotency_key,NEW.created_at,NEW.command_session,NEW.command_version)
     IS DISTINCT FROM ROW(OLD.tenant_id,OLD.org_id,OLD.operation_id,OLD.actor_id,OLD.idempotency_key,OLD.request_digest,OLD.case_id,OLD.upload_idempotency_key,OLD.created_at,OLD.command_session,OLD.command_version)
 THEN RAISE EXCEPTION 'invalid intake completion' USING ERRCODE='23514'; END IF;
 IF digest IS NOT NULL AND NOT {{SCHEMA}}.tenant_intake_recovery_allowed(OLD.tenant_id,OLD.org_id,OLD.case_id,OLD.actor_id,OLD.operation_id,OLD.idempotency_key) THEN
  RAISE EXCEPTION 'context_stale' USING ERRCODE='40001';
 END IF;
 IF digest IS NULL AND (OLD.command_session IS NOT NULL OR OLD.tenant_id NOT IN ('local-demo','local-demo-2')) THEN
  RAISE EXCEPTION 'context_stale' USING ERRCODE='40001';
 END IF;
 receipt:=convert_from(NEW.response_body,'UTF8')::jsonb;
 IF jsonb_typeof(receipt)<>'object' OR (SELECT count(*) FROM jsonb_object_keys(receipt))<>7
  OR receipt->>'operation_id' IS DISTINCT FROM OLD.operation_id OR receipt->>'case_id' IS DISTINCT FROM OLD.case_id
  OR receipt->>'state' IS DISTINCT FROM 'completed' OR receipt->'material_version' IS DISTINCT FROM '1'::jsonb
  OR receipt->'retryable' IS DISTINCT FROM 'false'::jsonb OR NOT EXISTS (
   SELECT 1 FROM {{SCHEMA}}.case_intake_requests r
   JOIN {{SCHEMA}}.document_upload_idempotency i ON i.tenant_id=OLD.tenant_id AND i.org_id=OLD.org_id AND i.case_id=OLD.case_id
      AND i.actor_id=OLD.actor_id AND i.idempotency_key=OLD.upload_idempotency_key
   JOIN {{SCHEMA}}.documents d ON d.tenant_id=i.tenant_id AND d.org_id=i.org_id AND d.case_id=i.case_id AND d.id=i.document_id
   JOIN {{SCHEMA}}.material_entities m ON m.tenant_id=i.tenant_id AND m.org_id=i.org_id AND m.case_id=i.case_id AND m.material_id=i.material_id
   JOIN {{SCHEMA}}.material_versions v ON v.tenant_id=i.tenant_id AND v.org_id=i.org_id AND v.case_id=i.case_id
      AND v.material_id=i.material_id AND v.document_id=i.document_id AND v.version=1
   WHERE r.tenant_id=OLD.tenant_id AND r.operation_id=OLD.operation_id AND r.schema_version=1
    AND d.id=receipt->>'document_id' AND m.material_id=receipt->>'material_id'
    AND d.role='draft_contract' AND d.origin='upload' AND m.role='draft_contract' AND m.purpose='primary_contract'
    AND d.filename=r.request_json->>'file_name' AND d.media_type=r.request_json->>'media_type'
    AND d.digest=r.request_json->>'file_sha256' AND to_jsonb(d.size)=r.request_json->'file_size'
  ) THEN RAISE EXCEPTION 'intake completion does not match original Document fact' USING ERRCODE='23514'; END IF;
 NEW.command_session:=OLD.command_session; NEW.command_version:=OLD.command_version;
 INSERT INTO {{SCHEMA}}.case_intake_completion_events(tenant_id,operation_id,actor_id,request_digest,response_digest,session_digest,context_version)
 VALUES(OLD.tenant_id,OLD.operation_id,OLD.actor_id,OLD.request_digest,NEW.response_digest,digest,version);
 RETURN NEW;
END $fn$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_intake_recovery_allowed(text,text,text,text,text,text),{{SCHEMA}}.tenant_intake_read_allowed(text,text,text,text,text,text),
 {{SCHEMA}}.tenant_intake_upload_recovery_allowed(text,text,text,text,text),{{SCHEMA}}.stamp_intake_completion() FROM PUBLIC;
-- The offline migrator owns the table but not this bounded function. Grant it
-- EXECUTE only while it creates the trigger; runtime roles never receive it.
DO $grant_migrator$
DECLARE relation_owner name;
BEGIN
 SELECT pg_get_userbyid(c.relowner) INTO relation_owner FROM pg_catalog.pg_class c WHERE c.oid='{{SCHEMA}}.case_intake_operations'::regclass;
 -- The table owner is also the supported embedded non-production login. It
 -- already has authority to alter the relation and its policies.
 EXECUTE format('GRANT EXECUTE ON FUNCTION %I.tenant_intake_recovery_allowed(text,text,text,text,text,text), %I.tenant_intake_read_allowed(text,text,text,text,text,text), %I.tenant_intake_upload_recovery_allowed(text,text,text,text,text) TO %I',current_schema(),current_schema(),current_schema(),relation_owner);
 EXECUTE format('GRANT EXECUTE ON FUNCTION %I.stamp_intake_completion() TO %I',current_schema(),session_user);
END $grant_migrator$;
RESET ROLE;
REVOKE CREATE ON SCHEMA {{SCHEMA}} FROM {{AUTH_FENCE_OWNER}};

ALTER POLICY command_receipt_context ON {{SCHEMA}}.case_intake_operations
 USING (NULLIF(current_setting('app.command_digest',true),'') IS NULL
   OR command_session=current_setting('app.command_digest',true) AND command_version=NULLIF(current_setting('app.command_version',true),'')::bigint
   OR current_user={{AUTH_FENCE_OWNER_LITERAL}}
   OR {{SCHEMA}}.tenant_intake_recovery_allowed(tenant_id,org_id,case_id,actor_id,operation_id,idempotency_key)
   OR {{SCHEMA}}.tenant_intake_read_allowed(tenant_id,org_id,case_id,actor_id,operation_id,idempotency_key))
 WITH CHECK (NULLIF(current_setting('app.command_digest',true),'') IS NULL
   OR command_session=current_setting('app.command_digest',true) AND command_version=NULLIF(current_setting('app.command_version',true),'')::bigint
   OR {{SCHEMA}}.tenant_intake_recovery_allowed(tenant_id,org_id,case_id,actor_id,operation_id,idempotency_key));
ALTER POLICY command_receipt_context ON {{SCHEMA}}.document_upload_idempotency
 USING (NULLIF(current_setting('app.command_digest',true),'') IS NULL
   OR command_session=current_setting('app.command_digest',true) AND command_version=NULLIF(current_setting('app.command_version',true),'')::bigint
   OR CASE WHEN NULLIF(current_setting('app.intake_upload_key',true),'') IS NOT NULL
     THEN {{SCHEMA}}.tenant_intake_upload_recovery_allowed(tenant_id,org_id,case_id,actor_id,idempotency_key) ELSE false END);
DROP TRIGGER command_receipt_context ON {{SCHEMA}}.case_intake_operations;
CREATE TRIGGER command_receipt_context BEFORE INSERT OR UPDATE ON {{SCHEMA}}.case_intake_operations
 FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.stamp_intake_completion();
SET LOCAL ROLE {{AUTH_FENCE_OWNER}};
DO $revoke_migrator$
BEGIN
 EXECUTE format('REVOKE EXECUTE ON FUNCTION %I.stamp_intake_completion() FROM %I',current_schema(),session_user);
END $revoke_migrator$;
RESET ROLE;
