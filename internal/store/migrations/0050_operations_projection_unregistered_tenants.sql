-- Projection bookkeeping is not an admission boundary. A tenant that has no
-- identity_tenants row has no aggregate projection to refresh, so marking it
-- dirty must be skipped instead of rejecting the source write with a foreign
-- key error. The aggregate reader already joins identity_tenants and therefore
-- cannot observe these tenants.
GRANT USAGE,CREATE ON SCHEMA {{SCHEMA}} TO {{AUTH_FENCE_OWNER}};
SET LOCAL ROLE {{AUTH_FENCE_OWNER}};

CREATE OR REPLACE FUNCTION {{SCHEMA}}.tenant_operations_mark_dirty() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE v_tenant text;
BEGIN
 IF TG_OP='DELETE' THEN v_tenant:=OLD.tenant_id; ELSE v_tenant:=NEW.tenant_id; END IF;
 IF v_tenant IS NOT NULL AND v_tenant<>'' AND EXISTS (
   SELECT 1 FROM {{SCHEMA}}.identity_tenants t WHERE t.tenant_id=v_tenant
 ) THEN
   INSERT INTO {{SCHEMA}}.tenant_operations_projection_dirty(tenant_id,generation,projected_generation,dirty_since)
     VALUES(v_tenant,1,0,clock_timestamp())
     ON CONFLICT(tenant_id) DO UPDATE SET generation={{SCHEMA}}.tenant_operations_projection_dirty.generation+1,
       dirty_since=CASE WHEN {{SCHEMA}}.tenant_operations_projection_dirty.generation={{SCHEMA}}.tenant_operations_projection_dirty.projected_generation
         THEN clock_timestamp() ELSE {{SCHEMA}}.tenant_operations_projection_dirty.dirty_since END;
 END IF;
 RETURN NULL;
END
$fn$;

RESET ROLE;
REVOKE CREATE ON SCHEMA {{SCHEMA}} FROM {{AUTH_FENCE_OWNER}};
