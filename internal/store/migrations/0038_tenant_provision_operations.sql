-- Durable, idempotent operator workflow for a tenant already present in the
-- trusted identity crosswalk. External actions are represented only by hashed
-- receipts; unknown results remain blocked until explicit reconciliation.
CREATE TABLE {{SCHEMA}}.tenant_provision_operations (
    operation_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id text NOT NULL REFERENCES {{SCHEMA}}.identity_tenants(tenant_id),
    kind text NOT NULL CHECK (kind IN ('provision','deactivate','reactivate')),
    status text NOT NULL CHECK (status IN ('pending','in_progress','reconciliation_required','provision_failed','completed')),
    current_step text NOT NULL CHECK (current_step IN ('identity','tenant_config','runtime_ready','usage_reconcile','completed')),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object'),
    payload_sha256 text NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 160),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(created_by,idempotency_key)
);
CREATE UNIQUE INDEX tenant_provision_one_open_per_tenant
    ON {{SCHEMA}}.tenant_provision_operations(tenant_id)
    WHERE status IN ('pending','in_progress','reconciliation_required');
CREATE INDEX tenant_provision_operations_latest
    ON {{SCHEMA}}.tenant_provision_operations(created_at DESC,operation_id DESC);

CREATE TABLE {{SCHEMA}}.tenant_provision_operation_steps (
    operation_id uuid NOT NULL REFERENCES {{SCHEMA}}.tenant_provision_operations(operation_id),
    step_name text NOT NULL CHECK (step_name IN ('identity','tenant_config','runtime_ready','usage_reconcile')),
    state text NOT NULL CHECK (state IN ('pending','complete','external_unknown','failed','reconciled')),
    receipt_sha256 text CHECK (receipt_sha256 ~ '^[0-9a-f]{64}$'),
    reconciliation text NOT NULL DEFAULT '' CHECK (reconciliation IN ('','confirmed_complete','confirmed_not_applied','requires_operator')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(operation_id,step_name),
    CHECK (state NOT IN ('complete','reconciled') OR receipt_sha256 IS NOT NULL)
);

ALTER TABLE {{SCHEMA}}.tenant_provision_operations ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.tenant_provision_operations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_provision_operations_owner ON {{SCHEMA}}.tenant_provision_operations
    TO {{AUTH_FENCE_OWNER}} USING (true) WITH CHECK (true);
ALTER TABLE {{SCHEMA}}.tenant_provision_operation_steps ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.tenant_provision_operation_steps FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_provision_steps_owner ON {{SCHEMA}}.tenant_provision_operation_steps
    TO {{AUTH_FENCE_OWNER}} USING (true) WITH CHECK (true);
REVOKE ALL ON {{SCHEMA}}.tenant_provision_operations,{{SCHEMA}}.tenant_provision_operation_steps FROM PUBLIC;

GRANT SELECT (tenant_id,status,live_epoch,revision,config_revision,config_version_id) ON {{SCHEMA}}.identity_tenants TO {{AUTH_FENCE_OWNER}};
GRANT UPDATE (status,live_epoch,revision,updated_at) ON {{SCHEMA}}.identity_tenants TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (principal_id,actor_id,status) ON {{SCHEMA}}.identity_principals TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (membership_id,principal_id,tenant_id,org_unit_id,role_code,status,effective_from,effective_until) ON {{SCHEMA}}.identity_memberships TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,org_unit_id,status) ON {{SCHEMA}}.identity_org_units TO {{AUTH_FENCE_OWNER}};
GRANT SELECT,INSERT,UPDATE ON {{SCHEMA}}.tenant_provision_operations,{{SCHEMA}}.tenant_provision_operation_steps TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,lease_id,money_cost_state) ON {{SCHEMA}}.model_usage TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,lease_id,event_kind,amount_minor) ON {{SCHEMA}}.model_money_ledger TO {{AUTH_FENCE_OWNER}};
CREATE POLICY tenant_lifecycle_cost_read ON {{SCHEMA}}.model_usage FOR SELECT TO {{AUTH_FENCE_OWNER}}
    USING (tenant_id=current_setting('app.lifecycle_cost_tenant',true));
CREATE POLICY tenant_lifecycle_cost_read ON {{SCHEMA}}.model_money_ledger FOR SELECT TO {{AUTH_FENCE_OWNER}}
    USING (tenant_id=current_setting('app.lifecycle_cost_tenant',true));
GRANT USAGE,CREATE ON SCHEMA {{SCHEMA}} TO {{AUTH_FENCE_OWNER}};
SET LOCAL ROLE {{AUTH_FENCE_OWNER}};

CREATE FUNCTION {{SCHEMA}}.tenant_provision_operation_json(p_operation_id uuid)
RETURNS jsonb LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
 SELECT jsonb_build_object(
   'operation_id',o.operation_id::text,'tenant_id',o.tenant_id,'kind',o.kind,'status',o.status,
   'current_step',o.current_step,'payload_sha256',o.payload_sha256,'revision',o.revision::text,
   'reconciliation_required',(o.status='reconciliation_required'),
   'steps',COALESCE((SELECT jsonb_agg(jsonb_build_object('name',s.step_name,'state',s.state,'receipt_sha256',s.receipt_sha256,'updated_at',s.updated_at) ORDER BY s.revision,s.step_name) FROM {{SCHEMA}}.tenant_provision_operation_steps s WHERE s.operation_id=o.operation_id),'[]'::jsonb),
   'created_at',o.created_at,'updated_at',o.updated_at)
 FROM {{SCHEMA}}.tenant_provision_operations o WHERE o.operation_id=p_operation_id
$fn$;

CREATE FUNCTION {{SCHEMA}}.tenant_provision_operation_start(p_actor text,p_idempotency_key text,p_payload jsonb,p_digest text,p_created_by text)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE v_id uuid; v_kind text; v_tenant text; v_status text; v_jurisdiction text; v_config_version text;
        v_old record; v_principal uuid; v_org text;
BEGIN
 IF p_actor IS NULL OR p_actor='' OR current_setting('app.platform_operator_actor',true) IS DISTINCT FROM p_actor OR
    p_created_by IS DISTINCT FROM p_actor OR p_idempotency_key IS NULL OR length(p_idempotency_key) NOT BETWEEN 1 AND 160 OR
    p_digest !~ '^[0-9a-f]{64}$' OR p_payload IS NULL OR jsonb_typeof(p_payload)<>'object' THEN
   RAISE EXCEPTION 'platform tenant operation authorization or payload invalid' USING ERRCODE='42501';
 END IF;
 v_kind:=p_payload->>'kind'; v_tenant:=p_payload->>'tenant_id'; v_jurisdiction:=p_payload->>'jurisdiction_id';
 v_config_version:=p_payload->>'tenant_config_version';
 IF v_kind NOT IN ('provision','deactivate','reactivate') OR v_tenant IS NULL OR v_tenant='' THEN
   RAISE EXCEPTION 'tenant provision operation fields invalid' USING ERRCODE='22023';
 END IF;
 SELECT * INTO v_old FROM {{SCHEMA}}.tenant_provision_operations WHERE created_by=p_actor AND idempotency_key=p_idempotency_key FOR UPDATE;
 IF FOUND THEN
   IF v_old.payload_sha256<>p_digest THEN RAISE EXCEPTION 'tenant operation idempotency conflict' USING ERRCODE='P7T01'; END IF;
   RETURN jsonb_build_object('schema_version','tenant-provision-operation/v1','operation',{{SCHEMA}}.tenant_provision_operation_json(v_old.operation_id));
 END IF;
 SELECT t.status,t.config_version_id INTO v_status,v_config_version FROM {{SCHEMA}}.identity_tenants t WHERE t.tenant_id=v_tenant FOR UPDATE;
 IF NOT FOUND OR v_status='closed' THEN RAISE EXCEPTION 'tenant is unavailable for lifecycle operation' USING ERRCODE='P7T02'; END IF;
 IF v_kind IN ('provision','reactivate') THEN
   IF v_status<>'suspended' OR v_jurisdiction IS NULL OR p_payload->>'admin_principal_id' IS NULL OR p_payload->>'admin_org_unit_id' IS NULL OR
      v_config_version IS NULL OR v_config_version='' OR v_config_version IS DISTINCT FROM p_payload->>'tenant_config_version' THEN
     RAISE EXCEPTION 'tenant needs a current config and active administrator before activation' USING ERRCODE='P7T03';
   END IF;
   BEGIN v_principal:=(p_payload->>'admin_principal_id')::uuid; EXCEPTION WHEN invalid_text_representation THEN
     RAISE EXCEPTION 'administrator principal is invalid' USING ERRCODE='22023'; END;
   v_org:=p_payload->>'admin_org_unit_id';
   IF NOT EXISTS(SELECT 1 FROM {{SCHEMA}}.identity_tenants t JOIN {{SCHEMA}}.identity_jurisdictions j ON j.jurisdiction_id=t.jurisdiction_id
      JOIN {{SCHEMA}}.identity_org_units u ON u.tenant_id=t.tenant_id AND u.org_unit_id=v_org AND u.status='active'
      JOIN {{SCHEMA}}.identity_memberships m ON m.tenant_id=t.tenant_id AND m.org_unit_id=u.org_unit_id AND m.principal_id=v_principal
        AND m.role_code='admin' AND m.status='active' AND (m.effective_from IS NULL OR m.effective_from<=clock_timestamp())
        AND (m.effective_until IS NULL OR m.effective_until>clock_timestamp())
      JOIN {{SCHEMA}}.identity_principals p ON p.principal_id=m.principal_id AND p.status='active'
     WHERE t.tenant_id=v_tenant AND t.display_name=p_payload->>'tenant_name' AND t.jurisdiction_id=v_jurisdiction AND j.status='active' AND t.config_revision>0
       AND u.display_name=p_payload->>'admin_org_unit_name') THEN
     RAISE EXCEPTION 'tenant administrator, jurisdiction, or adopted config is unavailable' USING ERRCODE='P7T03';
   END IF;
   IF v_kind='provision' THEN
     INSERT INTO {{SCHEMA}}.tenant_provision_operations(tenant_id,kind,status,current_step,payload,payload_sha256,idempotency_key,created_by)
       VALUES(v_tenant,v_kind,'pending','identity',p_payload,p_digest,p_idempotency_key,p_actor) RETURNING operation_id INTO v_id;
     INSERT INTO {{SCHEMA}}.tenant_provision_operation_steps(operation_id,step_name,state) VALUES(v_id,'identity','pending'),(v_id,'tenant_config','pending'),(v_id,'runtime_ready','pending');
   ELSE
     INSERT INTO {{SCHEMA}}.tenant_provision_operations(tenant_id,kind,status,current_step,payload,payload_sha256,idempotency_key,created_by)
       VALUES(v_tenant,v_kind,'pending','identity',p_payload,p_digest,p_idempotency_key,p_actor) RETURNING operation_id INTO v_id;
     INSERT INTO {{SCHEMA}}.tenant_provision_operation_steps(operation_id,step_name,state) VALUES(v_id,'identity','pending'),(v_id,'tenant_config','pending'),(v_id,'runtime_ready','pending');
   END IF;
 ELSE
   IF v_status<>'active' THEN RAISE EXCEPTION 'only active tenants can be deactivated' USING ERRCODE='P7T03'; END IF;
   UPDATE {{SCHEMA}}.identity_tenants SET status='suspended',live_epoch=live_epoch+1,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=v_tenant;
   INSERT INTO {{SCHEMA}}.tenant_provision_operations(tenant_id,kind,status,current_step,payload,payload_sha256,idempotency_key,created_by)
     VALUES(v_tenant,v_kind,'pending','usage_reconcile',p_payload,p_digest,p_idempotency_key,p_actor) RETURNING operation_id INTO v_id;
   INSERT INTO {{SCHEMA}}.tenant_provision_operation_steps(operation_id,step_name,state) VALUES(v_id,'usage_reconcile','pending');
 END IF;
 RETURN jsonb_build_object('schema_version','tenant-provision-operation/v1','operation',{{SCHEMA}}.tenant_provision_operation_json(v_id));
END
$fn$;

CREATE FUNCTION {{SCHEMA}}.tenant_provision_operations_list(p_actor text,p_cursor text,p_limit integer)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE v_ops jsonb; v_next text; v_cursor uuid;
BEGIN
 IF current_setting('app.platform_operator_actor',true) IS DISTINCT FROM p_actor OR p_actor IS NULL OR p_actor='' OR p_limit NOT BETWEEN 1 AND 100 THEN
   RAISE EXCEPTION 'platform tenant operation list scope invalid' USING ERRCODE='42501';
 END IF;
 IF coalesce(p_cursor,'')<>'' THEN
   BEGIN v_cursor:=p_cursor::uuid; EXCEPTION WHEN invalid_text_representation THEN RAISE EXCEPTION 'tenant operation cursor invalid' USING ERRCODE='22023'; END;
   IF NOT EXISTS(SELECT 1 FROM {{SCHEMA}}.tenant_provision_operations WHERE operation_id=v_cursor) THEN RAISE EXCEPTION 'tenant operation cursor not found' USING ERRCODE='P7T02'; END IF;
 END IF;
 WITH cursor_row AS (SELECT created_at,operation_id FROM {{SCHEMA}}.tenant_provision_operations WHERE operation_id=v_cursor),
 page AS (SELECT o.operation_id,o.created_at,row_number() OVER(ORDER BY o.created_at DESC,o.operation_id DESC) AS rn
   FROM {{SCHEMA}}.tenant_provision_operations o
   WHERE v_cursor IS NULL OR (o.created_at,o.operation_id)<(SELECT created_at,operation_id FROM cursor_row)
   ORDER BY o.created_at DESC,o.operation_id DESC LIMIT p_limit+1)
 SELECT COALESCE(jsonb_agg({{SCHEMA}}.tenant_provision_operation_json(page.operation_id) ORDER BY page.rn) FILTER(WHERE page.rn<=p_limit),'[]'::jsonb),
   (SELECT operation_id::text FROM page WHERE rn=p_limit)
 INTO v_ops,v_next FROM page;
 IF (SELECT count(*) FROM (SELECT 1 FROM {{SCHEMA}}.tenant_provision_operations o WHERE v_cursor IS NULL OR
   (o.created_at,o.operation_id)<(SELECT created_at,operation_id FROM {{SCHEMA}}.tenant_provision_operations WHERE operation_id=v_cursor) LIMIT p_limit+1) x)>p_limit THEN
   NULL;
 ELSE
   v_next:=NULL;
 END IF;
 RETURN jsonb_build_object('schema_version','tenant-provision-operations/v1','operations',v_ops,'next_cursor',v_next);
END
$fn$;

CREATE FUNCTION {{SCHEMA}}.tenant_provision_operation_read(p_actor text,p_operation_id text)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE v_id uuid; v_json jsonb;
BEGIN
 IF current_setting('app.platform_operator_actor',true) IS DISTINCT FROM p_actor OR p_actor IS NULL OR p_actor='' THEN RAISE EXCEPTION 'platform tenant operation authorization required' USING ERRCODE='42501'; END IF;
 BEGIN v_id:=p_operation_id::uuid; EXCEPTION WHEN invalid_text_representation THEN RAISE EXCEPTION 'operation not found' USING ERRCODE='P7T02'; END;
 v_json:={{SCHEMA}}.tenant_provision_operation_json(v_id);
 IF v_json IS NULL THEN RAISE EXCEPTION 'operation not found' USING ERRCODE='P7T02'; END IF;
 RETURN jsonb_build_object('schema_version','tenant-provision-operation/v1','operation',v_json);
END
$fn$;

CREATE FUNCTION {{SCHEMA}}.tenant_provision_operation_advance(p_actor text,p_operation_id text,p_expected_revision bigint,p_state text,p_receipt_sha256 text,p_reconciliation text,p_updated_by text)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE v_id uuid; v_op record; v_step text; v_next text; v_status text; v_tenant text; v_unknown bigint;
BEGIN
 IF current_setting('app.platform_operator_actor',true) IS DISTINCT FROM p_actor OR p_actor IS NULL OR p_actor='' OR p_updated_by IS DISTINCT FROM p_actor OR
    p_expected_revision<1 OR p_state NOT IN ('complete','external_unknown','failed','reconciled') OR
    p_reconciliation NOT IN ('','confirmed_complete','confirmed_not_applied','requires_operator') OR
    (p_state<>'reconciled' AND p_reconciliation<>'') OR
    (p_state IN ('complete','reconciled') AND (p_receipt_sha256 IS NULL OR p_receipt_sha256 !~ '^[0-9a-f]{64}$')) THEN
   RAISE EXCEPTION 'platform tenant operation step authorization or input invalid' USING ERRCODE='42501';
 END IF;
 BEGIN v_id:=p_operation_id::uuid; EXCEPTION WHEN invalid_text_representation THEN RAISE EXCEPTION 'operation not found' USING ERRCODE='P7T02'; END;
 SELECT * INTO v_op FROM {{SCHEMA}}.tenant_provision_operations WHERE operation_id=v_id FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'operation not found' USING ERRCODE='P7T02'; END IF;
 IF v_op.revision<>p_expected_revision THEN RAISE EXCEPTION 'tenant operation revision conflict' USING ERRCODE='P7T04'; END IF;
 IF v_op.status IN ('completed','provision_failed') THEN RAISE EXCEPTION 'tenant operation is terminal' USING ERRCODE='P7T05'; END IF;
 v_step:=v_op.current_step; v_next:=v_step; v_status:='in_progress'; v_tenant:=v_op.tenant_id;
 IF p_state='external_unknown' THEN
   UPDATE {{SCHEMA}}.tenant_provision_operation_steps SET state='external_unknown',receipt_sha256=NULL,reconciliation='',revision=revision+1,updated_at=clock_timestamp()
    WHERE operation_id=v_id AND step_name=v_step;
   v_status:='reconciliation_required';
 ELSIF p_state='failed' THEN
   UPDATE {{SCHEMA}}.tenant_provision_operation_steps SET state='failed',receipt_sha256=p_receipt_sha256,reconciliation='',revision=revision+1,updated_at=clock_timestamp()
    WHERE operation_id=v_id AND step_name=v_step;
   v_status:='provision_failed';
 ELSIF p_state='reconciled' THEN
   IF NOT EXISTS(SELECT 1 FROM {{SCHEMA}}.tenant_provision_operation_steps WHERE operation_id=v_id AND step_name=v_step AND state='external_unknown') THEN
     RAISE EXCEPTION 'only an unknown external result can be reconciled' USING ERRCODE='P7T05';
   END IF;
   IF p_reconciliation NOT IN ('confirmed_complete','confirmed_not_applied','requires_operator') THEN RAISE EXCEPTION 'reconciliation decision required' USING ERRCODE='22023'; END IF;
   IF p_reconciliation='requires_operator' THEN
     UPDATE {{SCHEMA}}.tenant_provision_operation_steps SET state='external_unknown',receipt_sha256=NULL,reconciliation=p_reconciliation,revision=revision+1,updated_at=clock_timestamp()
      WHERE operation_id=v_id AND step_name=v_step;
     v_status:='reconciliation_required';
   ELSIF p_reconciliation='confirmed_not_applied' THEN
     UPDATE {{SCHEMA}}.tenant_provision_operation_steps SET state='pending',receipt_sha256=NULL,reconciliation=p_reconciliation,revision=revision+1,updated_at=clock_timestamp()
      WHERE operation_id=v_id AND step_name=v_step;
     v_status:='in_progress';
   ELSE
     p_state:='complete';
   END IF;
 END IF;
 IF p_state='complete' THEN
   IF EXISTS(SELECT 1 FROM {{SCHEMA}}.tenant_provision_operation_steps WHERE operation_id=v_id AND step_name=v_step AND state='external_unknown')
      AND p_reconciliation<>'confirmed_complete' THEN
     RAISE EXCEPTION 'unknown external result requires reconciliation' USING ERRCODE='P7T05';
   END IF;
   IF v_step='identity' THEN
     IF NOT EXISTS(SELECT 1 FROM {{SCHEMA}}.tenant_provision_operation_steps WHERE operation_id=v_id AND step_name='identity' AND (state='pending' OR (state='external_unknown' AND p_reconciliation='confirmed_complete'))) THEN RAISE EXCEPTION 'identity step is not pending' USING ERRCODE='P7T05'; END IF;
     v_next:='tenant_config';
   ELSIF v_step='tenant_config' THEN
     IF NOT EXISTS(SELECT 1 FROM {{SCHEMA}}.tenant_provision_operation_steps WHERE operation_id=v_id AND step_name='identity' AND state='complete') THEN RAISE EXCEPTION 'identity step must complete first' USING ERRCODE='P7T05'; END IF;
     v_next:='runtime_ready';
     ELSIF v_step='runtime_ready' THEN
     IF NOT EXISTS(SELECT 1 FROM {{SCHEMA}}.tenant_provision_operation_steps WHERE operation_id=v_id AND step_name='tenant_config' AND state='complete') OR
        NOT EXISTS(SELECT 1 FROM {{SCHEMA}}.identity_tenants t
          JOIN {{SCHEMA}}.identity_jurisdictions j ON j.jurisdiction_id=t.jurisdiction_id AND j.status='active'
          JOIN {{SCHEMA}}.identity_org_units u ON u.tenant_id=t.tenant_id AND u.org_unit_id=v_op.payload->>'admin_org_unit_id'
            AND u.display_name=v_op.payload->>'admin_org_unit_name' AND u.status='active'
          JOIN {{SCHEMA}}.identity_memberships m ON m.tenant_id=t.tenant_id AND m.org_unit_id=u.org_unit_id
            AND m.principal_id=(v_op.payload->>'admin_principal_id')::uuid AND m.role_code='admin' AND m.status='active'
            AND (m.effective_from IS NULL OR m.effective_from<=clock_timestamp()) AND (m.effective_until IS NULL OR m.effective_until>clock_timestamp())
          JOIN {{SCHEMA}}.identity_principals p ON p.principal_id=m.principal_id AND p.status='active'
          WHERE t.tenant_id=v_tenant AND t.display_name=v_op.payload->>'tenant_name' AND t.jurisdiction_id=v_op.payload->>'jurisdiction_id'
            AND t.status='suspended' AND t.config_revision>0 AND t.config_version_id=v_op.payload->>'tenant_config_version') THEN
       RAISE EXCEPTION 'tenant is not ready for activation' USING ERRCODE='P7T03';
     END IF;
     UPDATE {{SCHEMA}}.identity_tenants SET status='active',live_epoch=live_epoch+1,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=v_tenant AND status='suspended';
     v_next:='completed'; v_status:='completed';
   ELSIF v_step='usage_reconcile' THEN
     PERFORM set_config('app.lifecycle_cost_tenant',v_tenant,true);
     SELECT count(*) INTO v_unknown FROM {{SCHEMA}}.model_usage u WHERE u.tenant_id=v_tenant AND u.money_cost_state='unknown_hold'
       AND COALESCE((SELECT sum(l.amount_minor) FROM {{SCHEMA}}.model_money_ledger l WHERE l.tenant_id=u.tenant_id AND l.lease_id=u.lease_id AND l.event_kind IN ('reserve','unknown_hold','release')),0)>0;
     IF v_unknown>0 THEN RAISE EXCEPTION 'unknown model cost holds still require reconciliation' USING ERRCODE='P7T06'; END IF;
     v_next:='completed'; v_status:='completed';
   ELSE
     RAISE EXCEPTION 'unknown tenant operation step' USING ERRCODE='P7T05';
   END IF;
   UPDATE {{SCHEMA}}.tenant_provision_operation_steps SET state='complete',receipt_sha256=p_receipt_sha256,reconciliation=p_reconciliation,revision=revision+1,updated_at=clock_timestamp()
     WHERE operation_id=v_id AND step_name=v_step;
 END IF;
 UPDATE {{SCHEMA}}.tenant_provision_operations SET status=v_status,current_step=v_next,revision=revision+1,updated_at=clock_timestamp() WHERE operation_id=v_id;
 RETURN jsonb_build_object('schema_version','tenant-provision-operation/v1','operation',{{SCHEMA}}.tenant_provision_operation_json(v_id));
END
$fn$;

REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_provision_operation_json(uuid),{{SCHEMA}}.tenant_provision_operation_start(text,text,jsonb,text,text),{{SCHEMA}}.tenant_provision_operations_list(text,text,integer),{{SCHEMA}}.tenant_provision_operation_read(text,text),{{SCHEMA}}.tenant_provision_operation_advance(text,text,bigint,text,text,text,text) FROM PUBLIC;
RESET ROLE;
REVOKE CREATE ON SCHEMA {{SCHEMA}} FROM {{AUTH_FENCE_OWNER}};
