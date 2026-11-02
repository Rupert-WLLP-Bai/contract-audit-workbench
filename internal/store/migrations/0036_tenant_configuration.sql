ALTER TABLE {{SCHEMA}}.identity_tenants
    ADD COLUMN config_revision bigint NOT NULL DEFAULT 0 CHECK (config_revision >= 0),
    ADD COLUMN config_version_id text,
    ADD COLUMN config_digest text NOT NULL DEFAULT '',
    ADD COLUMN config_adopted_at timestamptz,
    ADD COLUMN config_adopted_by text NOT NULL DEFAULT '',
    ADD COLUMN model_sends_enabled boolean NOT NULL DEFAULT true,
    ADD COLUMN model_sends_revision bigint NOT NULL DEFAULT 1 CHECK (model_sends_revision > 0),
    ADD COLUMN model_sends_updated_at timestamptz NOT NULL DEFAULT clock_timestamp();
ALTER TABLE {{SCHEMA}}.identity_tenants
    ADD CONSTRAINT identity_tenants_config_pair CHECK
      ((config_version_id IS NULL AND config_digest='') OR
       (config_version_id IS NOT NULL AND config_version_id<>'' AND config_digest ~ '^[0-9a-f]{64}$'));

CREATE TABLE {{SCHEMA}}.tenant_config_versions (
    tenant_id text NOT NULL REFERENCES {{SCHEMA}}.identity_tenants(tenant_id),
    version_id text NOT NULL CHECK (version_id ~ '^tcv_[a-zA-Z0-9_-]{8,120}$'),
    digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    config_json jsonb NOT NULL CHECK (jsonb_typeof(config_json)='object'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    created_by text NOT NULL,
    PRIMARY KEY (tenant_id,version_id)
);
ALTER TABLE {{SCHEMA}}.tenant_config_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.tenant_config_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON {{SCHEMA}}.tenant_config_versions
    USING (tenant_id=current_setting('app.tenant_id',true))
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true));
CREATE TRIGGER tenant_config_versions_append_only
    BEFORE UPDATE OR DELETE ON {{SCHEMA}}.tenant_config_versions
    FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.forbid_append_only();
CREATE INDEX tenant_config_versions_latest ON {{SCHEMA}}.tenant_config_versions(tenant_id,created_at DESC,version_id);

ALTER TABLE {{SCHEMA}}.review_run_snapshots
    ADD COLUMN adopted_tenant_config_version_id text NOT NULL DEFAULT '',
    ADD COLUMN adopted_tenant_config_digest text NOT NULL DEFAULT '',
    ADD COLUMN input_hash_version smallint NOT NULL DEFAULT 1 CHECK (input_hash_version IN (1,2));
ALTER TABLE {{SCHEMA}}.review_run_snapshots
    ADD CONSTRAINT review_run_snapshots_tenant_config_pair CHECK
      (((adopted_tenant_config_version_id='' AND adopted_tenant_config_digest='') OR
       (adopted_tenant_config_version_id<>'' AND adopted_tenant_config_digest ~ '^[0-9a-f]{64}$')) AND
      (input_hash_version=1 OR adopted_tenant_config_version_id<>''));

GRANT SELECT (tenant_id,status,live_epoch,config_revision,config_version_id,config_digest,config_adopted_at,config_adopted_by,model_sends_enabled,model_sends_revision,model_sends_updated_at)
    ON {{SCHEMA}}.identity_tenants TO {{AUTH_FENCE_OWNER}};
GRANT UPDATE (config_revision,config_version_id,config_digest,config_adopted_at,config_adopted_by,model_sends_enabled,model_sends_revision,model_sends_updated_at)
    ON {{SCHEMA}}.identity_tenants TO {{AUTH_FENCE_OWNER}};
GRANT USAGE,CREATE ON SCHEMA {{SCHEMA}} TO {{AUTH_FENCE_OWNER}};
SET LOCAL ROLE {{AUTH_FENCE_OWNER}};

CREATE FUNCTION {{SCHEMA}}.tenant_config_control(p_tenant_id text)
RETURNS TABLE(config_revision bigint,version_id text,digest text,adopted_at timestamptz,adopted_by text,model_sends_enabled boolean,model_sends_revision bigint,model_sends_updated_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
BEGIN
 RETURN QUERY SELECT t.config_revision,t.config_version_id,t.config_digest,t.config_adopted_at,t.config_adopted_by,t.model_sends_enabled,t.model_sends_revision,t.model_sends_updated_at
 FROM {{SCHEMA}}.identity_tenants t WHERE t.tenant_id=p_tenant_id FOR SHARE;
END
$fn$;

CREATE FUNCTION {{SCHEMA}}.tenant_config_adopt(p_tenant_id text,p_expected_revision bigint,p_version_id text,p_digest text,p_actor text)
RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE next_revision bigint;
BEGIN
 IF p_tenant_id IS NULL OR p_actor IS NULL OR p_version_id IS NULL OR p_digest IS NULL OR
    current_setting('app.tenant_id',true) IS DISTINCT FROM p_tenant_id OR
    current_setting('app.actor_id',true) IS DISTINCT FROM p_actor OR
    p_version_id !~ '^tcv_[a-zA-Z0-9_-]{8,120}$' OR p_digest !~ '^[0-9a-f]{64}$' THEN
  RAISE EXCEPTION 'tenant config scope invalid' USING ERRCODE='42501';
 END IF;
 UPDATE {{SCHEMA}}.identity_tenants SET config_revision=config_revision+1,config_version_id=p_version_id,
  config_digest=p_digest,config_adopted_at=clock_timestamp(),config_adopted_by=p_actor
  WHERE tenant_id=p_tenant_id AND status<>'closed' AND config_revision=p_expected_revision
  RETURNING config_revision INTO next_revision;
 IF NOT FOUND THEN RAISE EXCEPTION 'tenant config revision conflict' USING ERRCODE='P7C01'; END IF;
 RETURN next_revision;
END $fn$;

CREATE FUNCTION {{SCHEMA}}.tenant_model_send_set(p_tenant_id text,p_expected_revision bigint,p_enabled boolean,p_actor text)
RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE next_revision bigint;
BEGIN
 IF p_tenant_id IS NULL OR p_actor IS NULL OR
    current_setting('app.tenant_id',true) IS DISTINCT FROM p_tenant_id OR
    current_setting('app.actor_id',true) IS DISTINCT FROM p_actor THEN
  RAISE EXCEPTION 'tenant live control scope invalid' USING ERRCODE='42501';
 END IF;
 UPDATE {{SCHEMA}}.identity_tenants SET model_sends_enabled=p_enabled,model_sends_revision=model_sends_revision+1,model_sends_updated_at=clock_timestamp()
  WHERE tenant_id=p_tenant_id AND model_sends_revision=p_expected_revision
  RETURNING model_sends_revision INTO next_revision;
 IF NOT FOUND THEN RAISE EXCEPTION 'tenant live control revision conflict' USING ERRCODE='P7C02'; END IF;
 RETURN next_revision;
END $fn$;

CREATE FUNCTION {{SCHEMA}}.tenant_model_send_fence(p_tenant_id text,p_live_epoch bigint,p_allow_local_demo boolean)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE actual_status text; actual_epoch bigint; sends_enabled boolean;
BEGIN
 IF p_live_epoch IS NULL OR p_live_epoch<0 THEN RETURN false; END IF;
 SELECT control.status,control.live_epoch INTO actual_status,actual_epoch
  FROM {{SCHEMA}}.tenant_runtime_control(p_tenant_id,p_allow_local_demo) control;
 IF NOT FOUND OR actual_status<>'active' OR actual_epoch<>p_live_epoch THEN RETURN false; END IF;
 SELECT t.model_sends_enabled INTO sends_enabled FROM {{SCHEMA}}.identity_tenants t WHERE t.tenant_id=p_tenant_id FOR SHARE;
 IF NOT FOUND AND p_allow_local_demo AND p_tenant_id IN ('local-demo','local-demo-2') THEN RETURN true; END IF;
 RETURN FOUND AND sends_enabled;
END $fn$;

REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_config_control(text),{{SCHEMA}}.tenant_config_adopt(text,bigint,text,text,text),{{SCHEMA}}.tenant_model_send_set(text,bigint,boolean,text),{{SCHEMA}}.tenant_model_send_fence(text,bigint,boolean) FROM PUBLIC;
REVOKE CREATE ON SCHEMA {{SCHEMA}} FROM {{AUTH_FENCE_OWNER}};
RESET ROLE;
