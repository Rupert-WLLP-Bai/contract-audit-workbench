-- Tenant live state fences asynchronous admission and every later job effect.
ALTER TABLE {{SCHEMA}}.identity_tenants
    ADD COLUMN live_epoch bigint NOT NULL DEFAULT 1 CHECK (live_epoch > 0);
ALTER TABLE {{SCHEMA}}.worker_jobs
    ADD COLUMN tenant_live_epoch bigint CHECK (tenant_live_epoch >= 0);
ALTER TABLE {{SCHEMA}}.review_attempts
    ADD COLUMN tenant_live_epoch bigint CHECK (tenant_live_epoch >= 0);
ALTER TABLE {{SCHEMA}}.operation_authority_tickets
    ADD COLUMN tenant_live_epoch bigint CHECK (tenant_live_epoch >= 0);
ALTER TABLE {{SCHEMA}}.intake_authority_tickets
    ADD COLUMN tenant_live_epoch bigint CHECK (tenant_live_epoch >= 0);

-- Existing work has no trustworthy admission epoch and remains read-only.
CREATE FUNCTION {{SCHEMA}}.tenant_epoch_immutable() RETURNS trigger
LANGUAGE plpgsql AS $fn$
BEGIN
 IF OLD.tenant_live_epoch IS DISTINCT FROM NEW.tenant_live_epoch THEN
  RAISE EXCEPTION 'admission epoch is immutable';
 END IF;
 RETURN NEW;
END $fn$;
CREATE TRIGGER worker_job_epoch_immutable BEFORE UPDATE ON {{SCHEMA}}.worker_jobs
 FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.tenant_epoch_immutable();
CREATE TRIGGER review_attempt_epoch_immutable BEFORE UPDATE ON {{SCHEMA}}.review_attempts
 FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.tenant_epoch_immutable();
CREATE TRIGGER operation_ticket_epoch_immutable BEFORE UPDATE ON {{SCHEMA}}.operation_authority_tickets
 FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.tenant_epoch_immutable();
CREATE TRIGGER intake_ticket_epoch_immutable BEFORE UPDATE ON {{SCHEMA}}.intake_authority_tickets
 FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.tenant_epoch_immutable();
CREATE INDEX worker_jobs_tenant_live_claim
    ON {{SCHEMA}}.worker_jobs (tenant_id, tenant_live_epoch, state, ready_at);

GRANT SELECT (tenant_id,status,live_epoch) ON {{SCHEMA}}.identity_tenants TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_live_epoch) ON {{SCHEMA}}.intake_authority_tickets, {{SCHEMA}}.operation_authority_tickets, {{SCHEMA}}.worker_jobs, {{SCHEMA}}.review_attempts TO {{AUTH_FENCE_OWNER}};
GRANT USAGE,CREATE ON SCHEMA {{SCHEMA}} TO {{AUTH_FENCE_OWNER}};
SET LOCAL ROLE {{AUTH_FENCE_OWNER}};

-- This function locks an existing tenant row for the caller's transaction so
-- status changes serialize with admission, claim, renewal, and completion.
CREATE FUNCTION {{SCHEMA}}.tenant_runtime_control(p_tenant_id text,p_allow_local_demo boolean)
RETURNS TABLE(status text,live_epoch bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
BEGIN
 IF p_tenant_id IS NULL OR btrim(p_tenant_id)='' THEN RETURN; END IF;
 RETURN QUERY SELECT t.status,t.live_epoch FROM {{SCHEMA}}.identity_tenants t
  WHERE t.tenant_id=p_tenant_id FOR SHARE;
 IF NOT FOUND AND p_allow_local_demo AND p_tenant_id IN ('local-demo','local-demo-2') THEN
  RETURN QUERY SELECT 'active'::text,0::bigint;
 END IF;
END $fn$;

CREATE FUNCTION {{SCHEMA}}.tenant_runtime_fence(p_tenant_id text,p_live_epoch bigint,p_allow_local_demo boolean)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE actual_status text; actual_epoch bigint;
BEGIN
 IF p_live_epoch IS NULL OR p_live_epoch<0 THEN RETURN false; END IF;
 SELECT control.status,control.live_epoch INTO actual_status,actual_epoch
  FROM {{SCHEMA}}.tenant_runtime_control(p_tenant_id,p_allow_local_demo) control;
 RETURN FOUND AND actual_status='active' AND actual_epoch=p_live_epoch;
END $fn$;
-- Queue discovery runs without a tenant GUC. Return only an equality result,
-- never a tenant row or attempt data, through the existing bounded owner.
CREATE FUNCTION {{SCHEMA}}.tenant_review_job_epoch_matches(p_job_id text)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
 SELECT EXISTS (
  SELECT 1 FROM {{SCHEMA}}.worker_jobs j
  JOIN {{SCHEMA}}.review_runs r ON r.tenant_id=j.tenant_id AND r.org_id=j.org_id AND r.case_id=j.case_id AND r.id=j.run_id
  JOIN {{SCHEMA}}.review_attempts a ON a.tenant_id=r.tenant_id AND a.org_id=r.org_id AND a.run_id=r.id AND a.id=j.attempt_id
  WHERE j.id=p_job_id AND j.kind='review' AND j.tenant_live_epoch IS NOT NULL AND a.tenant_live_epoch=j.tenant_live_epoch
 );
$fn$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_review_job_epoch_matches(text) FROM PUBLIC;

-- Read only the epoch bound to an exact unexpired operation ticket. Intake
-- tickets retain their original epoch when promoted to an operation ticket.
CREATE FUNCTION {{SCHEMA}}.tenant_ticket_epoch(p_ticket text,p_scope jsonb,p_allow_local_demo boolean)
RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE epoch bigint;
BEGIN
 SELECT t.tenant_live_epoch INTO epoch FROM {{SCHEMA}}.operation_authority_tickets t
 WHERE t.ticket_digest=p_ticket AND t.expires_at>clock_timestamp()
 AND t.scope=(p_scope-ARRAY['operation_ticket','configuration_id','provider_url']);
 IF NOT FOUND AND p_scope->>'kind'='document' AND p_scope->>'action'='intake_parse' THEN
  SELECT t.tenant_live_epoch INTO epoch FROM {{SCHEMA}}.intake_authority_tickets t
  WHERE t.ticket_digest=p_ticket AND t.expires_at>clock_timestamp() AND t.tenant_id=p_scope->>'tenant_id'
   AND t.org_id=p_scope->>'org_id' AND t.case_id=p_scope->>'case_id' AND t.actor_id=p_scope->>'actor_id'
   AND t.body_sha256=p_scope->>'input_sha256';
 END IF;
 IF NOT FOUND OR epoch IS NULL OR NOT {{SCHEMA}}.tenant_runtime_fence(p_scope->>'tenant_id',epoch,p_allow_local_demo) THEN RETURN NULL; END IF;
 RETURN epoch;
END $fn$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_ticket_epoch(text,jsonb,boolean) FROM PUBLIC;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_runtime_control(text,boolean),{{SCHEMA}}.tenant_runtime_fence(text,bigint,boolean) FROM PUBLIC;
RESET ROLE;
REVOKE CREATE ON SCHEMA {{SCHEMA}} FROM {{AUTH_FENCE_OWNER}};
