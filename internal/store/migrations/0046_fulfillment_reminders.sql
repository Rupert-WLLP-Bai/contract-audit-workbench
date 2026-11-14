-- Reminder schedules are confirmed against a frozen Contract revision; delivery
-- and delivery attempts are separate facts. No external notification channel.
CREATE TABLE clm_fulfillment_reminders (
 tenant_id text NOT NULL,
 contract_id uuid NOT NULL,
 contract_revision_id uuid NOT NULL,
 reminder_id uuid NOT NULL,
 job_id text NOT NULL UNIQUE,
 plan_kind text NOT NULL CHECK (plan_kind IN ('delivery','acceptance','payment')),
 plan_sequence integer NOT NULL CHECK (plan_sequence > 0),
 planned_date date NOT NULL,
 timezone text NOT NULL CHECK (length(timezone) BETWEEN 1 AND 100),
 recipient_id text NOT NULL CHECK (length(btrim(recipient_id)) BETWEEN 1 AND 200),
 recipient_org_id text NOT NULL CHECK (length(btrim(recipient_org_id)) > 0),
 recipient_principal_id uuid,
 recipient_membership_id uuid,
 lead_days integer NOT NULL CHECK (lead_days BETWEEN 0 AND 365),
 due_at timestamptz NOT NULL,
 state text NOT NULL DEFAULT 'active' CHECK (state IN ('active','cancelled')),
 row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
 confirmed_by text NOT NULL,
 confirmed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,contract_id,reminder_id),
 UNIQUE (tenant_id,reminder_id),
 FOREIGN KEY (tenant_id,contract_id,contract_revision_id)
   REFERENCES clm_contract_revisions(tenant_id,contract_id,contract_revision_id),
 CHECK ((recipient_principal_id IS NULL) = (recipient_membership_id IS NULL))
);
CREATE UNIQUE INDEX clm_fulfillment_reminder_active_plan ON clm_fulfillment_reminders
 (tenant_id,contract_id,contract_revision_id,plan_kind,plan_sequence) WHERE state='active';

CREATE TABLE clm_fulfillment_reminder_history (
 tenant_id text NOT NULL, contract_id uuid NOT NULL, reminder_id uuid NOT NULL,
 version bigint NOT NULL CHECK (version > 0), action text NOT NULL CHECK (action IN ('confirmed','cancelled')),
 actor_id text NOT NULL, recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,contract_id,reminder_id,version),
 FOREIGN KEY (tenant_id,contract_id,reminder_id) REFERENCES clm_fulfillment_reminders(tenant_id,contract_id,reminder_id)
);
CREATE TRIGGER clm_reminder_history_append_only BEFORE UPDATE OR DELETE ON clm_fulfillment_reminder_history
 FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

CREATE TABLE clm_fulfillment_reminder_deliveries (
 tenant_id text NOT NULL, contract_id uuid NOT NULL, reminder_id uuid NOT NULL,
 recipient_id text NOT NULL, delivered_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,contract_id,reminder_id),
 FOREIGN KEY (tenant_id,contract_id,reminder_id) REFERENCES clm_fulfillment_reminders(tenant_id,contract_id,reminder_id)
);
CREATE TRIGGER clm_reminder_deliveries_append_only BEFORE UPDATE OR DELETE ON clm_fulfillment_reminder_deliveries
 FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

CREATE TABLE clm_fulfillment_reminder_attempts (
 tenant_id text NOT NULL, contract_id uuid NOT NULL, reminder_id uuid NOT NULL,
 generation bigint NOT NULL CHECK(generation > 0), status text NOT NULL CHECK(status IN ('delivered','denied','stale','failed')),
 attempted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,contract_id,reminder_id,generation),
 FOREIGN KEY (tenant_id,contract_id,reminder_id) REFERENCES clm_fulfillment_reminders(tenant_id,contract_id,reminder_id)
);
CREATE TRIGGER clm_reminder_attempts_append_only BEFORE UPDATE OR DELETE ON clm_fulfillment_reminder_attempts
 FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

ALTER TABLE worker_jobs ALTER COLUMN case_id DROP NOT NULL;
ALTER TABLE worker_jobs DROP CONSTRAINT worker_jobs_kind_identity;
ALTER TABLE worker_jobs ADD CONSTRAINT worker_jobs_kind_identity CHECK (
 (kind='review' AND case_id IS NOT NULL AND case_id<>'' AND run_id IS NOT NULL AND run_id<>'' AND attempt_id IS NOT NULL AND attempt_id<>'') OR
 (kind='experience_distill' AND case_id IS NOT NULL AND case_id<>'' AND run_id IS NULL AND attempt_id IS NULL) OR
 (kind='fulfillment_reminder' AND case_id IS NULL AND run_id IS NULL AND attempt_id IS NULL)
);

DO $migration$
DECLARE table_name text;
BEGIN
 FOREACH table_name IN ARRAY ARRAY['clm_fulfillment_reminders','clm_fulfillment_reminder_history','clm_fulfillment_reminder_deliveries','clm_fulfillment_reminder_attempts'] LOOP
  EXECUTE format('REVOKE ALL ON TABLE %I FROM PUBLIC',table_name);
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',table_name);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',table_name);
  EXECUTE format('CREATE POLICY tenant_boundary ON %I USING (tenant_id=current_setting(''app.tenant_id'',true)) WITH CHECK (tenant_id=current_setting(''app.tenant_id'',true))',table_name);
 END LOOP;
END $migration$;

-- Exact tenant+actor resolution only; no broad identity-table access is granted
-- to service roles. Active membership and organization are rechecked each use.
GRANT USAGE,CREATE ON SCHEMA {{SCHEMA}} TO {{AUTH_FENCE_OWNER}};
SET LOCAL ROLE {{AUTH_FENCE_OWNER}};
CREATE FUNCTION {{SCHEMA}}.tenant_reminder_member(p_tenant text,p_actor text)
RETURNS TABLE(principal_id uuid,membership_id uuid,actor_id text,issuer text,subject text,org_id text,role_code text,grants jsonb)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('{{IDENTITY_LOCK_KEY}}',0));
 RETURN QUERY SELECT p.principal_id,m.membership_id,p.actor_id,p.issuer,p.subject,m.org_unit_id,m.role_code,m.grants
 FROM {{SCHEMA}}.identity_principals p
 JOIN {{SCHEMA}}.identity_memberships m ON m.principal_id=p.principal_id
 JOIN {{SCHEMA}}.identity_tenants t ON t.tenant_id=m.tenant_id
 JOIN {{SCHEMA}}.identity_org_units o ON o.tenant_id=m.tenant_id AND o.org_unit_id=m.org_unit_id
 WHERE p_tenant=current_setting('app.tenant_id',true) AND p_tenant<>'' AND p_actor<>''
 AND EXISTS (SELECT 1 FROM {{SCHEMA}}.service_database_roles r WHERE r.role_name=session_user AND r.service='core')
 AND p.actor_id=p_actor AND m.tenant_id=p_tenant AND p.status='active' AND m.status='active' AND t.status='active' AND o.status='active'
 AND (m.effective_from IS NULL OR m.effective_from<=clock_timestamp())
 AND (m.effective_until IS NULL OR m.effective_until>clock_timestamp());
END
$fn$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_reminder_member(text,text) FROM PUBLIC;
RESET ROLE;
REVOKE CREATE ON SCHEMA {{SCHEMA}} FROM {{AUTH_FENCE_OWNER}};

CREATE FUNCTION {{SCHEMA}}.fulfillment_reminder_enqueue(
 p_tenant text,p_contract uuid,p_reminder uuid,p_job text,p_org text,p_actor text,p_epoch bigint,
 p_policy_hash text,p_policy_json text,p_global_pending integer,p_tenant_pending integer,p_max_claims integer,p_max_age integer,p_allow_demo boolean
) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE v_role text:=COALESCE(current_setting('app.actor_role',true),''); v_due timestamptz; v_policy text; v_total integer; v_tenant integer;
BEGIN
 IF p_tenant IS DISTINCT FROM current_setting('app.tenant_id',true) OR p_org IS DISTINCT FROM current_setting('app.org_id',true) OR p_actor IS DISTINCT FROM current_setting('app.actor_id',true)
    OR COALESCE(p_job,'')='' OR COALESCE(p_org,'')='' OR COALESCE(p_actor,'')='' OR COALESCE(p_policy_hash,'')='' OR COALESCE(p_policy_json,'')=''
    OR p_global_pending<1 OR p_tenant_pending<1 OR p_max_claims<1 OR p_max_age<1
    OR NOT {{SCHEMA}}.tenant_runtime_fence(p_tenant,p_epoch,p_allow_demo) THEN
  RAISE EXCEPTION 'invalid reminder admission' USING ERRCODE='42501';
 END IF;
 SELECT due_at INTO v_due FROM {{SCHEMA}}.clm_fulfillment_reminders
 WHERE tenant_id=p_tenant AND contract_id=p_contract AND reminder_id=p_reminder AND job_id=p_job AND state='active' AND confirmed_by=p_actor;
 IF NOT FOUND THEN RAISE EXCEPTION 'reminder unavailable' USING ERRCODE='42501'; END IF;
 PERFORM set_config('app.actor_role','worker',true);
 PERFORM pg_advisory_xact_lock(hashtextextended('{{QUEUE_LOCK_KEY}}',0));
 INSERT INTO {{SCHEMA}}.execution_queue_policies(policy_hash,policy_json) VALUES(p_policy_hash,p_policy_json) ON CONFLICT DO NOTHING;
 INSERT INTO {{SCHEMA}}.execution_queue_cursor(tenant_id,policy_hash) VALUES('__scheduler__',p_policy_hash) ON CONFLICT DO NOTHING;
 SELECT policy_hash INTO v_policy FROM {{SCHEMA}}.execution_queue_cursor WHERE tenant_id='__scheduler__' FOR UPDATE;
 IF v_policy<>p_policy_hash THEN RAISE EXCEPTION 'execution queue policy mismatch' USING ERRCODE='55000'; END IF;
 SELECT count(*),count(*) FILTER (WHERE tenant_id=p_tenant) INTO v_total,v_tenant FROM {{SCHEMA}}.worker_jobs WHERE state IN ('queued','running');
 IF v_total>=p_global_pending OR v_tenant>=p_tenant_pending THEN RAISE EXCEPTION 'execution queue capacity exceeded' USING ERRCODE='53300'; END IF;
 INSERT INTO {{SCHEMA}}.execution_queue_tenants(tenant_id) VALUES(p_tenant) ON CONFLICT DO NOTHING;
 INSERT INTO {{SCHEMA}}.worker_jobs(id,tenant_id,org_id,case_id,run_id,attempt_id,actor_id,kind,state,tenant_live_epoch,created_at,updated_at,policy_hash,max_claims,ready_at,deadline_at)
 VALUES(p_job,p_tenant,p_org,NULL,NULL,NULL,p_actor,'fulfillment_reminder','queued',p_epoch,clock_timestamp()::text,clock_timestamp()::text,p_policy_hash,p_max_claims,GREATEST(v_due,clock_timestamp()),GREATEST(v_due,clock_timestamp())+p_max_age*interval '1 second');
 PERFORM set_config('app.actor_role',v_role,true);
END $fn$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.fulfillment_reminder_enqueue(text,uuid,uuid,text,text,text,bigint,text,text,integer,integer,integer,integer,boolean) FROM PUBLIC;

CREATE FUNCTION {{SCHEMA}}.fulfillment_reminder_cancel_job(p_tenant text,p_contract uuid,p_reminder uuid)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE v_role text:=COALESCE(current_setting('app.actor_role',true),''); v_job text;
BEGIN
 IF p_tenant IS DISTINCT FROM current_setting('app.tenant_id',true) THEN RAISE EXCEPTION 'tenant mismatch' USING ERRCODE='42501'; END IF;
 SELECT job_id INTO v_job FROM {{SCHEMA}}.clm_fulfillment_reminders WHERE tenant_id=p_tenant AND contract_id=p_contract AND reminder_id=p_reminder AND state='cancelled';
 IF NOT FOUND THEN RAISE EXCEPTION 'reminder unavailable' USING ERRCODE='42501'; END IF;
 PERFORM set_config('app.actor_role','worker',true);
 UPDATE {{SCHEMA}}.worker_jobs SET state='cancelled',updated_at=clock_timestamp()::text WHERE id=v_job AND tenant_id=p_tenant AND kind='fulfillment_reminder' AND state='queued';
 PERFORM set_config('app.actor_role',v_role,true);
END $fn$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.fulfillment_reminder_cancel_job(text,uuid,uuid) FROM PUBLIC;

CREATE FUNCTION {{SCHEMA}}.fulfillment_reminder_lock_claim(p_tenant text,p_org text,p_job text,p_worker text,p_generation bigint,p_allow_demo boolean)
RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE v_role text:=COALESCE(current_setting('app.actor_role',true),''); v_found boolean; v_exhausted boolean;
BEGIN
 IF p_tenant IS DISTINCT FROM current_setting('app.tenant_id',true) OR p_tenant='' OR p_org='' OR p_job='' OR p_worker='' OR p_generation<=0 THEN RETURN 'unavailable'; END IF;
 PERFORM set_config('app.actor_role','worker',true);
 SELECT j.deadline_at<=clock_timestamp() OR j.claim_count>j.max_claims INTO v_exhausted FROM {{SCHEMA}}.worker_jobs j WHERE j.id=p_job AND j.tenant_id=p_tenant AND j.org_id=p_org AND j.worker_id=p_worker AND j.generation=p_generation AND j.kind='fulfillment_reminder'
 AND j.case_id IS NULL AND j.run_id IS NULL AND j.attempt_id IS NULL AND j.state='running' AND j.lease_until>clock_timestamp()
 AND EXISTS (SELECT 1 FROM {{SCHEMA}}.clm_fulfillment_reminders r WHERE r.tenant_id=j.tenant_id AND r.job_id=j.id AND r.confirmed_by=j.actor_id)
 AND {{SCHEMA}}.tenant_runtime_fence(j.tenant_id,j.tenant_live_epoch,p_allow_demo) FOR UPDATE;
 v_found:=FOUND; PERFORM set_config('app.actor_role',v_role,true); IF NOT v_found THEN RETURN 'unavailable'; END IF; IF v_exhausted THEN RETURN 'exhausted'; END IF; RETURN 'ready';
END $fn$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.fulfillment_reminder_lock_claim(text,text,text,text,bigint,boolean) FROM PUBLIC;

CREATE FUNCTION {{SCHEMA}}.fulfillment_reminder_finish_claim(p_tenant text,p_org text,p_job text,p_worker text,p_generation bigint,p_allow_demo boolean)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE v_role text:=COALESCE(current_setting('app.actor_role',true),''); v_count integer;
BEGIN
 IF p_tenant IS DISTINCT FROM current_setting('app.tenant_id',true) THEN RETURN false; END IF;
 IF NOT EXISTS(SELECT 1 FROM {{SCHEMA}}.clm_fulfillment_reminders r JOIN {{SCHEMA}}.clm_fulfillment_reminder_attempts a ON a.tenant_id=r.tenant_id AND a.contract_id=r.contract_id AND a.reminder_id=r.reminder_id WHERE r.tenant_id=p_tenant AND r.job_id=p_job AND a.generation=p_generation) THEN RETURN false; END IF;
 PERFORM set_config('app.actor_role','worker',true);
 UPDATE {{SCHEMA}}.worker_jobs j SET state=CASE WHEN EXISTS (SELECT 1 FROM {{SCHEMA}}.clm_fulfillment_reminders r JOIN {{SCHEMA}}.clm_fulfillment_reminder_attempts a ON a.tenant_id=r.tenant_id AND a.contract_id=r.contract_id AND a.reminder_id=r.reminder_id WHERE r.tenant_id=p_tenant AND r.job_id=p_job AND a.generation=p_generation AND a.status='failed') THEN 'failed' ELSE 'completed' END,lease_until=NULL,updated_at=clock_timestamp()::text
 WHERE j.id=p_job AND j.tenant_id=p_tenant AND j.org_id=p_org AND j.worker_id=p_worker AND j.generation=p_generation AND j.kind='fulfillment_reminder' AND j.state='running' AND j.lease_until>clock_timestamp()
 AND (j.deadline_at>clock_timestamp() AND j.claim_count<=j.max_claims OR EXISTS (SELECT 1 FROM {{SCHEMA}}.clm_fulfillment_reminders r JOIN {{SCHEMA}}.clm_fulfillment_reminder_attempts a ON a.tenant_id=r.tenant_id AND a.contract_id=r.contract_id AND a.reminder_id=r.reminder_id WHERE r.tenant_id=p_tenant AND r.job_id=p_job AND a.generation=p_generation AND a.status='failed'))
 AND {{SCHEMA}}.tenant_runtime_fence(j.tenant_id,j.tenant_live_epoch,p_allow_demo);
 GET DIAGNOSTICS v_count=ROW_COUNT; PERFORM set_config('app.actor_role',v_role,true); RETURN v_count=1;
END $fn$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.fulfillment_reminder_finish_claim(text,text,text,text,bigint,boolean) FROM PUBLIC;

CREATE FUNCTION guard_fulfillment_reminder() RETURNS trigger LANGUAGE plpgsql AS $fn$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'reminder history cannot be deleted' USING ERRCODE='23514'; END IF;
 IF OLD.state<>'active' OR NEW.state<>'cancelled' OR NEW.row_version<>OLD.row_version+1 OR
    (to_jsonb(NEW)-ARRAY['state','row_version']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['state','row_version']) THEN
  RAISE EXCEPTION 'reminder identities are immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $fn$;
CREATE TRIGGER clm_fulfillment_reminder_guard BEFORE UPDATE OR DELETE ON clm_fulfillment_reminders
 FOR EACH ROW EXECUTE FUNCTION guard_fulfillment_reminder();
