-- Context CAS and exact-session CSRF do not change historical 0033 identities.
ALTER TABLE {{SCHEMA}}.sessions
 ADD COLUMN context_version bigint NOT NULL DEFAULT 1 CHECK(context_version>0),
 ADD COLUMN csrf_token text NOT NULL DEFAULT (replace(gen_random_uuid()::text,'-','')||replace(gen_random_uuid()::text,'-',''));
ALTER TABLE {{SCHEMA}}.worker_claim_identities ADD COLUMN session_digest text, ADD COLUMN context_version bigint;
ALTER TABLE {{SCHEMA}}.operation_authority_tickets ADD COLUMN context_version bigint;
ALTER TABLE {{SCHEMA}}.intake_authority_tickets ADD COLUMN context_version bigint;

CREATE TABLE {{SCHEMA}}.identity_support_grants (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),tenant_id text NOT NULL,org_id text NOT NULL,case_id text NOT NULL,
 principal_id uuid NOT NULL REFERENCES {{SCHEMA}}.identity_principals(principal_id),
 actions text[] NOT NULL CHECK(actions=ARRAY['case.read']::text[]),reason text NOT NULL CHECK(btrim(reason)<>''),
 expires_at timestamptz NOT NULL,created_by uuid NOT NULL REFERENCES {{SCHEMA}}.identity_principals(principal_id),
 approved_by uuid REFERENCES {{SCHEMA}}.identity_principals(principal_id),
 status text NOT NULL CHECK(status IN ('pending','approved','revoked')),revision bigint NOT NULL DEFAULT 1,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(tenant_id,org_id,case_id) REFERENCES {{SCHEMA}}.cases(tenant_id,org_id,id),
 CHECK(expires_at>created_at),CHECK(approved_by IS NULL OR approved_by<>created_by),CHECK(created_by<>principal_id),CHECK(approved_by IS NULL OR approved_by<>principal_id)
);
CREATE TABLE {{SCHEMA}}.identity_support_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),tenant_id text NOT NULL,grant_id uuid NOT NULL REFERENCES {{SCHEMA}}.identity_support_grants(id),
 revision bigint NOT NULL,actor_principal_id uuid NOT NULL,action text NOT NULL CHECK(action IN ('request','approve','revoke','read')),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),UNIQUE(grant_id,revision,action)
);
ALTER TABLE {{SCHEMA}}.identity_support_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.identity_support_grants FORCE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.identity_support_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.identity_support_events FORCE ROW LEVEL SECURITY;
CREATE POLICY support_owner ON {{SCHEMA}}.identity_support_grants TO {{AUTH_FENCE_OWNER}} USING(true) WITH CHECK(true);
CREATE POLICY support_owner ON {{SCHEMA}}.identity_support_events TO {{AUTH_FENCE_OWNER}} USING(true) WITH CHECK(true);
REVOKE ALL ON {{SCHEMA}}.identity_support_grants,{{SCHEMA}}.identity_support_events FROM PUBLIC;
GRANT SELECT,INSERT,UPDATE(status,approved_by,revision) ON {{SCHEMA}}.identity_support_grants TO {{AUTH_FENCE_OWNER}};
GRANT SELECT,INSERT ON {{SCHEMA}}.identity_support_events TO {{AUTH_FENCE_OWNER}};
GRANT UPDATE(revision) ON {{SCHEMA}}.identity_tenants,{{SCHEMA}}.identity_memberships TO {{AUTH_FENCE_OWNER}};
GRANT SELECT(context_version,csrf_token),UPDATE(context_version,tenant_id,org_id,membership_id) ON {{SCHEMA}}.sessions TO {{AUTH_FENCE_OWNER}};
GRANT SELECT(session_digest,context_version),UPDATE(session_digest,context_version) ON {{SCHEMA}}.worker_claim_identities TO {{AUTH_FENCE_OWNER}};
GRANT SELECT,UPDATE(context_version) ON {{SCHEMA}}.operation_authority_tickets TO {{AUTH_FENCE_OWNER}};
GRANT SELECT(context_version),UPDATE(context_version) ON {{SCHEMA}}.intake_authority_tickets TO {{AUTH_FENCE_OWNER}};
GRANT SELECT(title,contract_type,business_status,created_at,updated_at),UPDATE(id) ON {{SCHEMA}}.cases TO {{AUTH_FENCE_OWNER}};
CREATE POLICY case_context_owner_lock ON {{SCHEMA}}.cases FOR UPDATE TO {{AUTH_FENCE_OWNER}} USING(true) WITH CHECK(false);
GRANT USAGE,CREATE ON SCHEMA {{SCHEMA}} TO {{AUTH_FENCE_OWNER}};
CREATE POLICY operation_context_owner_read ON {{SCHEMA}}.operation_authority_tickets FOR SELECT TO {{AUTH_FENCE_OWNER}} USING(true);

-- Receipt context is an idempotency boundary, not an authorization source.
-- Only the validated definer fence populates it in normal application paths;
-- original tenant RLS and service privileges remain independently required.
CREATE FUNCTION {{SCHEMA}}.stamp_command_receipt() RETURNS trigger LANGUAGE plpgsql
SET search_path=pg_catalog,pg_temp AS $receipt$
DECLARE digest text:=NULLIF(current_setting('app.command_digest',true),'');version bigint:=NULLIF(current_setting('app.command_version',true),'')::bigint;
BEGIN
 IF TG_OP='UPDATE' AND OLD.command_session IS NOT NULL AND (OLD.command_session IS DISTINCT FROM digest OR OLD.command_version IS DISTINCT FROM version) THEN RAISE EXCEPTION 'context_stale' USING ERRCODE='40001';END IF;
 NEW.command_session:=digest;NEW.command_version:=version;RETURN NEW;
END $receipt$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.stamp_command_receipt() FROM PUBLIC;
DO $receipt_tables$
DECLARE table_name text;
BEGIN
 FOREACH table_name IN ARRAY ARRAY['idempotency','rule_pack_idempotency','governed_knowledge_idempotency','document_upload_idempotency','subject_write_idempotency','service_object_mutation_receipts','core_write_receipts','execution_write_receipts','clm_contract_write_receipts','case_intake_operations'] LOOP
  EXECUTE format('ALTER TABLE %I.%I ADD COLUMN command_session text,ADD COLUMN command_version bigint',current_schema(),table_name);
  EXECUTE format('CREATE TRIGGER command_receipt_context BEFORE INSERT OR UPDATE ON %I.%I FOR EACH ROW EXECUTE FUNCTION %I.stamp_command_receipt()',current_schema(),table_name,current_schema());
  EXECUTE format($policy$CREATE POLICY command_receipt_context ON %I.%I AS RESTRICTIVE USING (NULLIF(current_setting('app.command_digest',true),'') IS NULL OR command_session=current_setting('app.command_digest',true) AND command_version=NULLIF(current_setting('app.command_version',true),'')::bigint) WITH CHECK (NULLIF(current_setting('app.command_digest',true),'') IS NULL OR command_session=current_setting('app.command_digest',true) AND command_version=NULLIF(current_setting('app.command_version',true),'')::bigint)$policy$,current_schema(),table_name);
 END LOOP;
END $receipt_tables$;
SET LOCAL ROLE {{AUTH_FENCE_OWNER}};

-- Current projection and revision fingerprint are read in one PG statement.
CREATE FUNCTION {{SCHEMA}}.tenant_context_info(p_digest text)
RETURNS TABLE(actor_id text,principal_id uuid,membership_id uuid,tenant_id text,tenant_name text,org_id text,org_name text,role_code text,grants jsonb,context_version bigint,csrf_token text,authority_revision text)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
 SELECT s.actor_id,s.principal_id,s.membership_id,s.tenant_id,COALESCE(t.display_name,s.tenant_id),s.org_id,COALESCE(o.display_name,s.org_id),m.role_code,m.grants,s.context_version,s.csrf_token,
 md5(concat_ws(':',s.principal_id,m.revision,p.revision,t.revision,o.revision,(SELECT COALESCE(jsonb_agg(g.value ORDER BY g.value::text),'[]'::jsonb)::text FROM jsonb_array_elements(COALESCE(m.grants,'[]'::jsonb)) g WHERE g.value->>'expires_at' IS NULL OR g.value->>'expires_at' IN ('','0001-01-01T00:00:00Z') OR (g.value->>'expires_at')::timestamptz>clock_timestamp())))
 FROM {{SCHEMA}}.sessions s
 LEFT JOIN {{SCHEMA}}.identity_memberships m ON m.membership_id=s.membership_id AND m.principal_id=s.principal_id AND m.tenant_id=s.tenant_id AND m.org_unit_id=s.org_id
 LEFT JOIN {{SCHEMA}}.identity_principals p ON p.principal_id=s.principal_id
 LEFT JOIN {{SCHEMA}}.identity_tenants t ON t.tenant_id=m.tenant_id
 LEFT JOIN {{SCHEMA}}.identity_org_units o ON o.tenant_id=m.tenant_id AND o.org_unit_id=m.org_unit_id
 WHERE s.token=p_digest AND s.expires_at::timestamptz>clock_timestamp()
 AND (s.principal_id IS NULL AND s.issuer='' AND s.subject='' OR p.status='active' AND m.status='active' AND t.status='active' AND o.status='active'
 AND (m.effective_from IS NULL OR m.effective_from<=clock_timestamp()) AND (m.effective_until IS NULL OR m.effective_until>clock_timestamp()));
$fn$;

CREATE FUNCTION {{SCHEMA}}.tenant_context_fence(p_digest text,p_version bigint,p_revision text,p_target uuid,p_support uuid)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE hint record; actual record; target_tenant text;support_tenant text;
BEGIN
 PERFORM set_config('app.session_token',p_digest,true);
 SELECT s.token,s.tenant_id,s.membership_id,s.principal_id,s.context_version INTO hint FROM {{SCHEMA}}.sessions s WHERE s.token=p_digest;
 IF NOT FOUND OR p_version IS NULL OR p_version<1 THEN RAISE EXCEPTION 'session_invalid' USING ERRCODE='28000'; END IF;
 IF p_target IS NOT NULL THEN SELECT m.tenant_id INTO target_tenant FROM {{SCHEMA}}.identity_memberships m WHERE m.membership_id=p_target AND m.principal_id=hint.principal_id; IF NOT FOUND THEN RAISE EXCEPTION 'access_denied' USING ERRCODE='42501';END IF; END IF;
 IF p_support IS NOT NULL THEN SELECT g.tenant_id INTO support_tenant FROM {{SCHEMA}}.identity_support_grants g WHERE g.id=p_support; IF NOT FOUND THEN RAISE EXCEPTION 'access_denied' USING ERRCODE='42501';END IF;END IF;
 -- A changed row encountered after an RR snapshot raises 40001 rather than authorizing old state.
 PERFORM t.tenant_id FROM {{SCHEMA}}.identity_tenants t WHERE t.tenant_id IN(hint.tenant_id,target_tenant,support_tenant) ORDER BY t.tenant_id FOR UPDATE;
 PERFORM m.membership_id FROM {{SCHEMA}}.identity_memberships m WHERE m.membership_id IN(hint.membership_id,p_target) ORDER BY m.membership_id FOR UPDATE;
 IF p_support IS NOT NULL THEN PERFORM g.id FROM {{SCHEMA}}.identity_support_grants g WHERE g.id=p_support FOR UPDATE;END IF;
 PERFORM s.token FROM {{SCHEMA}}.sessions s WHERE s.token=p_digest FOR UPDATE;
 SELECT * INTO actual FROM {{SCHEMA}}.tenant_context_info(p_digest);
 IF NOT FOUND THEN RAISE EXCEPTION 'session_invalid' USING ERRCODE='28000';END IF;
 IF actual.context_version<>p_version OR p_revision IS NULL OR p_revision<>actual.authority_revision THEN RAISE EXCEPTION 'context_stale' USING ERRCODE='40001';END IF;
 PERFORM set_config('app.command_digest',p_digest,true);PERFORM set_config('app.command_version',p_version::text,true);
END $fn$;

CREATE FUNCTION {{SCHEMA}}.tenant_context_switch(p_digest text,p_version bigint,p_revision text,p_target uuid)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE current_identity record;target record;
BEGIN
 PERFORM {{SCHEMA}}.tenant_context_fence(p_digest,p_version,p_revision,p_target,NULL);
 SELECT s.principal_id,s.audience INTO current_identity FROM {{SCHEMA}}.sessions s WHERE s.token=p_digest;
 SELECT m.membership_id,m.tenant_id,m.org_unit_id,m.role_code INTO target FROM {{SCHEMA}}.identity_memberships m
 JOIN {{SCHEMA}}.identity_tenants t USING(tenant_id) JOIN {{SCHEMA}}.identity_org_units o USING(tenant_id,org_unit_id)
 WHERE m.membership_id=p_target AND m.principal_id=current_identity.principal_id AND m.status='active' AND t.status='active' AND o.status='active'
 AND (m.effective_from IS NULL OR m.effective_from<=clock_timestamp()) AND (m.effective_until IS NULL OR m.effective_until>clock_timestamp())
 AND (current_identity.audience='client' OR m.role_code='admin');
 IF NOT FOUND THEN RAISE EXCEPTION 'access_denied' USING ERRCODE='42501';END IF;
 PERFORM set_config('app.tenant_id',target.tenant_id,true);PERFORM set_config('app.org_id',target.org_unit_id,true);
 UPDATE {{SCHEMA}}.sessions SET tenant_id=target.tenant_id,org_id=target.org_unit_id,membership_id=target.membership_id,context_version=context_version+1 WHERE token=p_digest;
END $fn$;

CREATE FUNCTION {{SCHEMA}}.tenant_context_list(p_digest text)
RETURNS TABLE(id uuid,tenant_id text,tenant_name text,org_id text,org_name text,role text)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
 SELECT m.membership_id,m.tenant_id,t.display_name,m.org_unit_id,o.display_name,m.role_code
 FROM {{SCHEMA}}.sessions s JOIN {{SCHEMA}}.tenant_context_info(p_digest) info ON info.principal_id=s.principal_id
 JOIN {{SCHEMA}}.identity_memberships m ON m.principal_id=s.principal_id
 JOIN {{SCHEMA}}.identity_tenants t ON t.tenant_id=m.tenant_id JOIN {{SCHEMA}}.identity_org_units o ON o.tenant_id=m.tenant_id AND o.org_unit_id=m.org_unit_id
 WHERE s.token=p_digest AND m.status='active' AND t.status='active' AND o.status='active'
 AND (m.effective_from IS NULL OR m.effective_from<=clock_timestamp()) AND (m.effective_until IS NULL OR m.effective_until>clock_timestamp())
 AND (s.audience='client' OR m.role_code='admin') ORDER BY m.tenant_id,m.org_unit_id,m.membership_id;
$fn$;


CREATE FUNCTION {{SCHEMA}}.tenant_operation_info(p_scope jsonb,p_ticket text)
RETURNS TABLE(session_digest text,context_version bigint,authority_revision text,role_code text,grants jsonb)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE bound record;
BEGIN
 IF COALESCE(p_scope->>'job_id','')<>'' THEN
 SELECT i.session_digest,i.context_version INTO bound FROM {{SCHEMA}}.worker_claim_identities i JOIN {{SCHEMA}}.worker_jobs j ON j.id=i.job_id AND j.tenant_id=i.tenant_id
 WHERE j.id=p_scope->>'job_id' AND j.tenant_id=p_scope->>'tenant_id' AND j.org_id=p_scope->>'org_id' AND j.case_id=p_scope->>'case_id' AND j.actor_id=p_scope->>'actor_id'
 AND j.worker_id=p_scope->>'worker_id' AND j.generation=(p_scope->>'generation')::bigint AND j.state='running' AND j.lease_until>clock_timestamp()
 AND COALESCE(j.run_id,'')=COALESCE(p_scope->>'run_id','') AND COALESCE(j.attempt_id,'')=COALESCE(p_scope->>'attempt_id','');
 ELSE
 SELECT t.session_digest,t.context_version INTO bound FROM {{SCHEMA}}.operation_authority_tickets t WHERE t.ticket_digest=p_ticket AND t.expires_at>clock_timestamp() AND t.scope=(p_scope-ARRAY['operation_ticket','configuration_id','provider_url']);
 IF NOT FOUND AND p_scope->>'kind'='document' AND p_scope->>'action'='intake_parse' THEN
  SELECT t.session_digest,t.context_version INTO bound FROM {{SCHEMA}}.intake_authority_tickets t WHERE t.ticket_digest=p_ticket AND t.expires_at>clock_timestamp() AND t.tenant_id=p_scope->>'tenant_id' AND t.org_id=p_scope->>'org_id' AND t.case_id=p_scope->>'case_id' AND t.actor_id=p_scope->>'actor_id' AND t.body_sha256=p_scope->>'input_sha256';
 END IF;

 END IF;
 IF NOT FOUND OR bound.session_digest IS NULL OR bound.context_version IS NULL THEN RAISE EXCEPTION 'session_invalid' USING ERRCODE='28000';END IF;
 PERFORM set_config('app.session_token',bound.session_digest,true);
 RETURN QUERY SELECT bound.session_digest,info.context_version,info.authority_revision,info.role_code,info.grants FROM {{SCHEMA}}.tenant_context_info(bound.session_digest) info WHERE info.context_version=bound.context_version AND info.actor_id=p_scope->>'actor_id';
 IF NOT FOUND THEN RAISE EXCEPTION 'context_stale' USING ERRCODE='40001';END IF;
END $fn$;
CREATE FUNCTION {{SCHEMA}}.tenant_operation_fence(p_scope jsonb,p_ticket text,p_revision text)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE proof record;
BEGIN
 SELECT * INTO proof FROM {{SCHEMA}}.tenant_operation_info(p_scope,p_ticket);
 IF NOT FOUND OR proof.authority_revision<>p_revision THEN RAISE EXCEPTION 'context_stale' USING ERRCODE='40001';END IF;
 PERFORM {{SCHEMA}}.tenant_context_fence(proof.session_digest,proof.context_version,p_revision,NULL,NULL);
 -- A lease/ticket may naturally expire while a domain lock was blocked.
 PERFORM 1 FROM {{SCHEMA}}.tenant_operation_info(p_scope,p_ticket);
END $fn$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_operation_info(jsonb,text),{{SCHEMA}}.tenant_operation_fence(jsonb,text,text) FROM PUBLIC;
CREATE FUNCTION {{SCHEMA}}.tenant_support_can_manage(p_digest text,p_case text)
RETURNS boolean LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
 SELECT EXISTS(SELECT 1 FROM {{SCHEMA}}.tenant_context_info(p_digest) u JOIN {{SCHEMA}}.cases c ON c.tenant_id=u.tenant_id
 CROSS JOIN LATERAL jsonb_array_elements(u.grants) g
 WHERE c.id=p_case AND c.archived_at IS NULL AND u.role_code IN('admin','clerk') AND g->>'role'='case_owner'
 AND (g->>'expires_at' IS NULL OR g->>'expires_at' IN('','0001-01-01T00:00:00Z') OR (g->>'expires_at')::timestamptz>clock_timestamp())
 AND (g->>'scope'='tenant' OR g->>'scope'='case' AND g->>'case_id'=c.id OR g->>'scope'='org' AND g->>'org_id'=c.org_id OR g->>'scope'='self' AND c.owner_id=u.actor_id));
$fn$;
CREATE FUNCTION {{SCHEMA}}.tenant_support_json(p_grant uuid)
RETURNS jsonb LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
 SELECT jsonb_build_object('id',g.id,'tenant_id',g.tenant_id,'org_id',g.org_id,'case_id',g.case_id,'principal_id',g.principal_id,'actions',g.actions,'reason',g.reason,'expires_at',g.expires_at,'created_by',g.created_by,'approved_by',g.approved_by,'status',CASE WHEN g.expires_at<=clock_timestamp() AND g.status<>'revoked' THEN 'expired' ELSE g.status END,'revision',g.revision::text) FROM {{SCHEMA}}.identity_support_grants g WHERE g.id=p_grant;
$fn$;
CREATE FUNCTION {{SCHEMA}}.tenant_support_list(p_digest text)
RETURNS jsonb LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
 SELECT COALESCE(jsonb_agg({{SCHEMA}}.tenant_support_json(g.id) ORDER BY g.created_at,g.id),'[]'::jsonb)
 FROM {{SCHEMA}}.tenant_context_info(p_digest) u JOIN {{SCHEMA}}.identity_support_grants g ON g.principal_id=u.principal_id OR {{SCHEMA}}.tenant_support_can_manage(p_digest,g.case_id);
$fn$;
CREATE FUNCTION {{SCHEMA}}.tenant_support_command(p_digest text,p_version bigint,p_revision text,p_action text,p_grant uuid,p_case text,p_principal uuid,p_actions text[],p_reason text,p_expires timestamptz,p_expected bigint)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE u record;g record;c record;created uuid;
BEGIN
 PERFORM {{SCHEMA}}.tenant_context_fence(p_digest,p_version,p_revision,NULL,p_grant);
 SELECT * INTO u FROM {{SCHEMA}}.tenant_context_info(p_digest);
 IF u.principal_id IS NULL THEN RAISE EXCEPTION 'access_denied' USING ERRCODE='42501';END IF;
 IF p_action='request' THEN
  IF p_grant IS NOT NULL OR p_principal IS NULL OR p_principal=u.principal_id OR p_actions<>ARRAY['case.read']::text[] OR p_actions IS NULL OR p_reason IS NULL OR btrim(p_reason)='' OR length(p_reason)>1000 OR p_expires IS NULL OR p_expires<=clock_timestamp() OR p_expires>clock_timestamp()+interval '24 hours' THEN RAISE EXCEPTION 'invalid_support_request' USING ERRCODE='22023';END IF;
  IF NOT {{SCHEMA}}.tenant_support_can_manage(p_digest,p_case) OR NOT EXISTS(SELECT 1 FROM {{SCHEMA}}.identity_principals p WHERE p.principal_id=p_principal AND p.status='active') THEN RAISE EXCEPTION 'access_denied' USING ERRCODE='42501';END IF;
  SELECT id,tenant_id,org_id INTO c FROM {{SCHEMA}}.cases WHERE id=p_case AND tenant_id=u.tenant_id AND archived_at IS NULL FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'access_denied' USING ERRCODE='42501';END IF;
  PERFORM {{SCHEMA}}.tenant_context_fence(p_digest,p_version,p_revision,NULL,NULL);
  IF p_expires<=clock_timestamp() THEN RAISE EXCEPTION 'access_denied' USING ERRCODE='42501';END IF;
  INSERT INTO {{SCHEMA}}.identity_support_grants(tenant_id,org_id,case_id,principal_id,actions,reason,expires_at,created_by,status) VALUES(c.tenant_id,c.org_id,c.id,p_principal,p_actions,p_reason,p_expires,u.principal_id,'pending') RETURNING id INTO created;
  INSERT INTO {{SCHEMA}}.identity_support_events(tenant_id,grant_id,revision,actor_principal_id,action) VALUES(c.tenant_id,created,1,u.principal_id,'request');
  RETURN {{SCHEMA}}.tenant_support_json(created);
 END IF;
 SELECT * INTO g FROM {{SCHEMA}}.identity_support_grants WHERE id=p_grant;
 IF NOT FOUND OR NOT {{SCHEMA}}.tenant_support_can_manage(p_digest,g.case_id) OR g.tenant_id<>u.tenant_id THEN RAISE EXCEPTION 'access_denied' USING ERRCODE='42501';END IF;
 IF p_expected IS NULL OR p_expected<>g.revision THEN RAISE EXCEPTION 'context_stale' USING ERRCODE='40001';END IF;
 PERFORM case_row.id FROM {{SCHEMA}}.cases case_row WHERE case_row.id=g.case_id AND case_row.tenant_id=g.tenant_id AND case_row.archived_at IS NULL FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'access_denied' USING ERRCODE='42501';END IF;
 PERFORM {{SCHEMA}}.tenant_context_fence(p_digest,p_version,p_revision,NULL,p_grant);
 IF p_action='approve' THEN
  IF g.status<>'pending' OR g.expires_at<=clock_timestamp() OR u.principal_id IN(g.created_by,g.principal_id) THEN RAISE EXCEPTION 'access_denied' USING ERRCODE='42501';END IF;
  UPDATE {{SCHEMA}}.identity_support_grants SET status='approved',approved_by=u.principal_id,revision=revision+1 WHERE id=g.id;
 ELSIF p_action='revoke' THEN
  IF g.status='revoked' THEN RAISE EXCEPTION 'context_stale' USING ERRCODE='40001';END IF;
  UPDATE {{SCHEMA}}.identity_support_grants SET status='revoked',revision=revision+1 WHERE id=g.id;
 ELSE RAISE EXCEPTION 'invalid_support_action' USING ERRCODE='22023';END IF;
 INSERT INTO {{SCHEMA}}.identity_support_events(tenant_id,grant_id,revision,actor_principal_id,action) VALUES(g.tenant_id,g.id,g.revision+1,u.principal_id,p_action);
 RETURN {{SCHEMA}}.tenant_support_json(g.id);
END $fn$;
CREATE FUNCTION {{SCHEMA}}.tenant_support_validate(p_digest text,p_grant uuid)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM {{SCHEMA}}.tenant_context_info(p_digest) u JOIN {{SCHEMA}}.identity_support_grants g ON g.principal_id=u.principal_id JOIN {{SCHEMA}}.identity_tenants t ON t.tenant_id=g.tenant_id AND t.status='active' WHERE g.id=p_grant AND g.status='approved' AND g.actions=ARRAY['case.read']::text[] AND g.expires_at>clock_timestamp()) THEN RAISE EXCEPTION 'access_denied' USING ERRCODE='42501';END IF;
END $fn$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_support_validate(text,uuid) FROM PUBLIC;
CREATE FUNCTION {{SCHEMA}}.tenant_support_read(p_digest text,p_version bigint,p_revision text,p_grant uuid)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE u record;g record;c record;
BEGIN
 PERFORM {{SCHEMA}}.tenant_context_fence(p_digest,p_version,p_revision,NULL,p_grant);
 SELECT * INTO u FROM {{SCHEMA}}.tenant_context_info(p_digest);
 SELECT grant_row.* INTO g FROM {{SCHEMA}}.identity_support_grants grant_row JOIN {{SCHEMA}}.identity_tenants t ON t.tenant_id=grant_row.tenant_id AND t.status='active'
 WHERE grant_row.id=p_grant AND grant_row.principal_id=u.principal_id AND grant_row.status='approved' AND grant_row.expires_at>clock_timestamp() AND grant_row.actions=ARRAY['case.read']::text[];
 IF NOT FOUND THEN RAISE EXCEPTION 'access_denied' USING ERRCODE='42501';END IF;
 PERFORM set_config('app.tenant_id',g.tenant_id,true);PERFORM set_config('app.org_id',g.org_id,true);
 SELECT id,title,contract_type,business_status,created_at,updated_at INTO c FROM {{SCHEMA}}.cases WHERE id=g.case_id AND tenant_id=g.tenant_id AND org_id=g.org_id AND archived_at IS NULL FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'access_denied' USING ERRCODE='42501';END IF;
 PERFORM {{SCHEMA}}.tenant_context_fence(p_digest,p_version,p_revision,NULL,p_grant);
 IF g.expires_at<=clock_timestamp() THEN RAISE EXCEPTION 'access_denied' USING ERRCODE='42501';END IF;
 INSERT INTO {{SCHEMA}}.identity_support_events(tenant_id,grant_id,revision,actor_principal_id,action) VALUES(g.tenant_id,g.id,g.revision,u.principal_id,'read') ON CONFLICT(grant_id,revision,action) DO NOTHING;
 RETURN jsonb_build_object('grant_id',g.id,'case_id',g.case_id,'tenant_id',g.tenant_id,'org_id',g.org_id,'title',c.title,'contract_type',c.contract_type,'business_status',c.business_status,'created_at',c.created_at,'updated_at',c.updated_at,'read_only',true);
END $fn$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_support_can_manage(text,text),{{SCHEMA}}.tenant_support_json(uuid),{{SCHEMA}}.tenant_support_list(text),{{SCHEMA}}.tenant_support_command(text,bigint,text,text,uuid,text,uuid,text[],text,timestamptz,bigint),{{SCHEMA}}.tenant_support_read(text,bigint,text,uuid) FROM PUBLIC;
CREATE OR REPLACE FUNCTION {{SCHEMA}}.tenant_identity_bind_admission(p_session_digest text,p_job_id text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $function$
DECLARE
    bound record;
    existing record;
    now_at timestamptz;
BEGIN
    now_at := clock_timestamp();
    IF p_session_digest IS NULL OR p_session_digest !~ '^sha256:[0-9a-f]{64}$' OR p_job_id IS NULL OR p_job_id='' THEN
        RAISE EXCEPTION 'invalid admission binding' USING ERRCODE='22023';
    END IF;
    PERFORM set_config('app.session_token',p_session_digest,true);
    SELECT job.tenant_id,job.org_id,job.id,job.case_id,job.run_id,job.attempt_id,job.actor_id,
        session.principal_id,session.membership_id,session.issuer,session.subject,
        membership.org_unit_id AS membership_org_id,membership.role_code,membership.grants,principal.actor_id AS principal_actor,
        principal.issuer AS principal_issuer,principal.subject AS principal_subject,
        case_row.owner_id,case_row.member_ids
    INTO bound
    FROM {{SCHEMA}}.sessions AS session
    JOIN {{SCHEMA}}.identity_principals AS principal ON principal.principal_id=session.principal_id
    JOIN {{SCHEMA}}.identity_memberships AS membership ON membership.membership_id=session.membership_id
        AND membership.principal_id=principal.principal_id AND membership.tenant_id=session.tenant_id
        AND membership.org_unit_id=session.org_id
    JOIN {{SCHEMA}}.identity_tenants AS tenant ON tenant.tenant_id=membership.tenant_id
    JOIN {{SCHEMA}}.identity_org_units AS unit ON unit.tenant_id=membership.tenant_id AND unit.org_unit_id=membership.org_unit_id
    JOIN {{SCHEMA}}.worker_jobs AS job ON job.tenant_id=session.tenant_id
        AND job.actor_id=principal.actor_id AND job.id=p_job_id
    LEFT JOIN {{SCHEMA}}.review_runs AS run ON run.tenant_id=job.tenant_id AND run.org_id=job.org_id
        AND run.case_id=job.case_id AND run.id=job.run_id
    LEFT JOIN {{SCHEMA}}.review_attempts AS attempt ON attempt.tenant_id=job.tenant_id AND attempt.org_id=job.org_id
        AND attempt.run_id=job.run_id AND attempt.id=job.attempt_id
    JOIN {{SCHEMA}}.cases AS case_row ON case_row.tenant_id=job.tenant_id AND case_row.org_id=job.org_id AND case_row.id=job.case_id
    WHERE session.token=p_session_digest AND session.audience='client' AND session.expires_at::timestamptz>now_at
      AND principal.status='active' AND membership.status='active' AND tenant.status='active' AND unit.status='active'
      AND session.issuer=principal.issuer AND session.subject=principal.subject
      AND membership.role_code IN ('admin','clerk','reviewer','viewer')
      AND (membership.effective_from IS NULL OR membership.effective_from<=now_at)
      AND (membership.effective_until IS NULL OR membership.effective_until>now_at)
      AND ((job.kind='review' AND run.state IN ('accepted','running') AND attempt.state IN ('accepted','running')
        AND attempt.history_kind IN ('preparing','frozen') AND (attempt.history_kind='preparing' OR attempt.input_hash IS NOT NULL))
        OR (job.kind='experience_distill' AND job.run_id IS NULL AND job.attempt_id IS NULL))
      AND case_row.archived_at IS NULL
      AND EXISTS (
        SELECT 1 FROM jsonb_array_elements(membership.grants) AS grants(value)
        WHERE grants.value->>'role' IN ('case_owner','reviewer')
          AND (NULLIF(grants.value->>'expires_at','') IS NULL OR grants.value->>'expires_at'='0001-01-01T00:00:00Z' OR (grants.value->>'expires_at')::timestamptz>now_at)
          AND ((grants.value->>'scope'='self' AND case_row.owner_id=principal.actor_id)
            OR (grants.value->>'scope'='case' AND grants.value->>'case_id'=job.case_id)
            OR (grants.value->>'scope'='org' AND grants.value->>'org_id'=job.org_id)
            OR (grants.value->>'scope'='org_subtree' AND COALESCE(grants.value->'resolved_org_ids','[]'::jsonb) ? job.org_id)
            OR grants.value->>'scope'='tenant')
      );
    IF NOT FOUND THEN
        -- Explicit locally issued demonstration sessions retain their existing
        -- development-only path. Enterprise sessions can never enter it.
        SELECT job.tenant_id,job.org_id,job.id,job.case_id,job.run_id,job.attempt_id,job.actor_id,
            NULL::uuid AS principal_id,NULL::uuid AS membership_id,session.issuer,session.subject,
            NULL::text AS membership_org_id,NULL::text AS role_code,NULL::jsonb AS grants,job.actor_id AS principal_actor,
            ''::text AS principal_issuer,''::text AS principal_subject,case_row.owner_id,case_row.member_ids
        INTO bound
        FROM {{SCHEMA}}.sessions session JOIN {{SCHEMA}}.worker_jobs job
          ON job.actor_id=session.actor_id AND job.tenant_id=session.tenant_id AND job.org_id=session.org_id
        JOIN {{SCHEMA}}.cases case_row ON case_row.id=job.case_id AND case_row.tenant_id=job.tenant_id AND case_row.org_id=job.org_id
        LEFT JOIN {{SCHEMA}}.review_runs run ON run.id=job.run_id AND run.tenant_id=job.tenant_id AND run.org_id=job.org_id
        LEFT JOIN {{SCHEMA}}.review_attempts attempt ON attempt.id=job.attempt_id AND attempt.run_id=run.id AND attempt.tenant_id=job.tenant_id AND attempt.org_id=job.org_id
        WHERE session.token=p_session_digest AND session.audience='client' AND session.expires_at::timestamptz>now_at
          AND session.principal_id IS NULL AND session.membership_id IS NULL AND COALESCE(session.issuer,'')='' AND COALESCE(session.subject,'')=''
          AND job.tenant_id IN ('local-demo','local-demo-2') AND job.id=p_job_id AND case_row.archived_at IS NULL
          AND ((job.kind='review' AND run.state IN ('accepted','running') AND attempt.state IN ('accepted','running') AND attempt.history_kind IN ('preparing','frozen') AND (attempt.history_kind='preparing' OR attempt.input_hash IS NOT NULL))
            OR (job.kind='experience_distill' AND job.run_id IS NULL AND job.attempt_id IS NULL));
        IF NOT FOUND THEN RAISE EXCEPTION 'current session cannot admit this review job' USING ERRCODE='42501'; END IF;
    END IF;
    IF bound.principal_actor<>bound.actor_id OR bound.issuer<>bound.principal_issuer OR bound.subject<>bound.principal_subject THEN
        RAISE EXCEPTION 'current session cannot admit this review job' USING ERRCODE='42501';
    END IF;
    INSERT INTO {{SCHEMA}}.worker_claim_identities
        (tenant_id,job_id,org_id,case_id,run_id,attempt_id,actor_id,principal_id,membership_id,membership_org_id)
    VALUES (bound.tenant_id,bound.id,bound.org_id,bound.case_id,COALESCE(bound.run_id,''),COALESCE(bound.attempt_id,''),
        bound.actor_id,bound.principal_id,bound.membership_id,bound.membership_org_id)
    ON CONFLICT (tenant_id,job_id) DO NOTHING;
    SELECT tenant_id,job_id,org_id,case_id,run_id,attempt_id,actor_id,principal_id,membership_id,membership_org_id
    INTO existing FROM {{SCHEMA}}.worker_claim_identities
    WHERE tenant_id=bound.tenant_id AND job_id=bound.id;
    IF NOT FOUND OR existing.org_id IS DISTINCT FROM bound.org_id OR existing.case_id IS DISTINCT FROM bound.case_id
       OR existing.run_id IS DISTINCT FROM COALESCE(bound.run_id,'') OR existing.attempt_id IS DISTINCT FROM COALESCE(bound.attempt_id,'')
       OR existing.actor_id IS DISTINCT FROM bound.actor_id OR existing.principal_id IS DISTINCT FROM bound.principal_id
       OR existing.membership_id IS DISTINCT FROM bound.membership_id OR existing.membership_org_id IS DISTINCT FROM bound.membership_org_id THEN
        RAISE EXCEPTION 'review job identity binding conflicts with its immutable record' USING ERRCODE='23505';
    END IF;
    UPDATE {{SCHEMA}}.worker_claim_identities identity_row SET session_digest=p_session_digest,context_version=session_row.context_version FROM {{SCHEMA}}.sessions session_row
    WHERE identity_row.tenant_id=bound.tenant_id AND identity_row.job_id=bound.id AND session_row.token=p_session_digest
      AND (identity_row.session_digest IS NULL OR identity_row.session_digest=p_session_digest AND identity_row.context_version=session_row.context_version);
    IF NOT FOUND THEN RAISE EXCEPTION 'context_stale' USING ERRCODE='40001';END IF;
END
$function$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_context_info(text),{{SCHEMA}}.tenant_context_fence(text,bigint,text,uuid,uuid),{{SCHEMA}}.tenant_context_switch(text,bigint,text,uuid),{{SCHEMA}}.tenant_context_list(text) FROM PUBLIC;
RESET ROLE;
REVOKE CREATE ON SCHEMA {{SCHEMA}} FROM {{AUTH_FENCE_OWNER}};
