-- Tenant live state fences asynchronous admission and every later job effect.
ALTER TABLE {{SCHEMA}}.identity_tenants
    ADD COLUMN live_epoch bigint NOT NULL DEFAULT 1 CHECK (live_epoch > 0);
ALTER TABLE {{SCHEMA}}.worker_jobs
    ADD COLUMN tenant_live_epoch bigint NOT NULL DEFAULT 0 CHECK (tenant_live_epoch >= 0);

-- Preserve already queued jobs for tenants that are currently active. A later
-- suspension/reactivation advances the epoch and makes old work permanently
-- stale; absent legacy demo tenants keep the reserved epoch zero.
UPDATE {{SCHEMA}}.worker_jobs AS job
   SET tenant_live_epoch = tenant.live_epoch
  FROM {{SCHEMA}}.identity_tenants AS tenant
 WHERE tenant.tenant_id = job.tenant_id;
CREATE INDEX worker_jobs_tenant_live_claim
    ON {{SCHEMA}}.worker_jobs (tenant_id, tenant_live_epoch, state, ready_at);

GRANT SELECT (tenant_id,status,live_epoch) ON {{SCHEMA}}.identity_tenants TO {{AUTH_FENCE_OWNER}};
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
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_runtime_control(text,boolean),{{SCHEMA}}.tenant_runtime_fence(text,bigint,boolean) FROM PUBLIC;
REVOKE CREATE ON SCHEMA {{SCHEMA}} FROM {{AUTH_FENCE_OWNER}};
RESET ROLE;
