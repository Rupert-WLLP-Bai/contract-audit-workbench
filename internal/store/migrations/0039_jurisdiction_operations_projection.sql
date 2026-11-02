-- Restricted aggregate projection. Source triggers only mark affected tenants
-- dirty; reads rebuild values from one PostgreSQL snapshot, replacing rather
-- than incrementally adding counters. Repeating the authorized GET is the
-- explicit rebuild path for the selected date range. No raw case or contract
-- rows are exposed.
CREATE TABLE {{SCHEMA}}.tenant_operations_projection_dirty (
    tenant_id text PRIMARY KEY REFERENCES {{SCHEMA}}.identity_tenants(tenant_id),
    generation bigint NOT NULL DEFAULT 1 CHECK (generation>0),
    projected_generation bigint NOT NULL DEFAULT 0 CHECK (projected_generation>=0),
    dirty_since timestamptz DEFAULT clock_timestamp()
);
CREATE TABLE {{SCHEMA}}.tenant_operations_projection_snapshots (
    tenant_id text NOT NULL REFERENCES {{SCHEMA}}.identity_tenants(tenant_id),
    from_date date NOT NULL,
    to_date date NOT NULL,
    source_generation bigint NOT NULL CHECK (source_generation>0),
    source_snapshot_token text NOT NULL,
    built_at timestamptz NOT NULL,
    cases_created bigint NOT NULL CHECK (cases_created>=0),
    runs_completed bigint NOT NULL CHECK (runs_completed>=0),
    attempts_completed bigint NOT NULL CHECK (attempts_completed>=0),
    money_known boolean NOT NULL,
    settled_minor numeric(60,0),
    unknown_hold_minor numeric(60,0),
    quota_calls bigint NOT NULL CHECK (quota_calls>=0),
    reserved_tokens numeric(60,0) NOT NULL CHECK (reserved_tokens>=0),
    unknown_usage_calls bigint NOT NULL CHECK (unknown_usage_calls>=0),
    PRIMARY KEY(tenant_id,from_date,to_date),
    CHECK(to_date>=from_date AND to_date<=from_date+30)
);
ALTER TABLE {{SCHEMA}}.tenant_operations_projection_dirty ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.tenant_operations_projection_dirty FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_ops_projection_owner ON {{SCHEMA}}.tenant_operations_projection_dirty TO {{AUTH_FENCE_OWNER}}
    USING (true) WITH CHECK (true);
ALTER TABLE {{SCHEMA}}.tenant_operations_projection_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{SCHEMA}}.tenant_operations_projection_snapshots FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_ops_projection_owner ON {{SCHEMA}}.tenant_operations_projection_snapshots TO {{AUTH_FENCE_OWNER}}
    USING (true) WITH CHECK (true);
REVOKE ALL ON {{SCHEMA}}.tenant_operations_projection_dirty,{{SCHEMA}}.tenant_operations_projection_snapshots FROM PUBLIC;

GRANT SELECT (tenant_id,display_name,jurisdiction_id,status) ON {{SCHEMA}}.identity_tenants TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,id,created_at,archived_at) ON {{SCHEMA}}.cases TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,id,case_id,state,created_at,updated_at) ON {{SCHEMA}}.review_runs TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,id,run_id,state,finished_at) ON {{SCHEMA}}.review_attempts TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,lease_id,event_kind,amount_minor,period_day) ON {{SCHEMA}}.model_money_ledger TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,lease_id,money_cost_state,money_cost_minor,money_period_day,reserved_at,occurred_at,reserved_input_tokens,reserved_output_tokens,input_tokens,output_tokens,total_tokens) ON {{SCHEMA}}.model_usage TO {{AUTH_FENCE_OWNER}};
GRANT SELECT (tenant_id,lease_id,reserved_input_tokens,reserved_output_tokens,acquired_at) ON {{SCHEMA}}.model_call_leases TO {{AUTH_FENCE_OWNER}};
CREATE POLICY tenant_ops_aggregate_read ON {{SCHEMA}}.cases FOR SELECT TO {{AUTH_FENCE_OWNER}}
    USING (tenant_id=current_setting('app.operations_tenant',true));
CREATE POLICY tenant_ops_aggregate_read ON {{SCHEMA}}.review_runs FOR SELECT TO {{AUTH_FENCE_OWNER}}
    USING (tenant_id=current_setting('app.operations_tenant',true));
CREATE POLICY tenant_ops_aggregate_read ON {{SCHEMA}}.review_attempts FOR SELECT TO {{AUTH_FENCE_OWNER}}
    USING (tenant_id=current_setting('app.operations_tenant',true));
CREATE POLICY tenant_ops_aggregate_read ON {{SCHEMA}}.model_call_leases FOR SELECT TO {{AUTH_FENCE_OWNER}}
    USING (tenant_id=current_setting('app.operations_tenant',true));
CREATE POLICY tenant_ops_aggregate_read ON {{SCHEMA}}.model_money_ledger FOR SELECT TO {{AUTH_FENCE_OWNER}}
    USING (tenant_id=current_setting('app.operations_tenant',true));
CREATE POLICY tenant_ops_aggregate_read ON {{SCHEMA}}.model_usage FOR SELECT TO {{AUTH_FENCE_OWNER}}
    USING (tenant_id=current_setting('app.operations_tenant',true));
GRANT SELECT,INSERT,UPDATE ON {{SCHEMA}}.tenant_operations_projection_dirty,{{SCHEMA}}.tenant_operations_projection_snapshots TO {{AUTH_FENCE_OWNER}};
GRANT USAGE,CREATE ON SCHEMA {{SCHEMA}} TO {{AUTH_FENCE_OWNER}};
SET LOCAL ROLE {{AUTH_FENCE_OWNER}};

CREATE FUNCTION {{SCHEMA}}.tenant_operations_mark_dirty() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE v_tenant text;
BEGIN
 IF TG_OP='DELETE' THEN v_tenant:=OLD.tenant_id; ELSE v_tenant:=NEW.tenant_id; END IF;
 IF v_tenant IN ('local-demo','local-demo-2') AND NOT EXISTS (SELECT 1 FROM {{SCHEMA}}.identity_tenants t WHERE t.tenant_id=v_tenant) THEN RETURN NULL; END IF;
 IF v_tenant IS NOT NULL AND v_tenant<>'' THEN
   INSERT INTO {{SCHEMA}}.tenant_operations_projection_dirty(tenant_id,generation,projected_generation,dirty_since)
     VALUES(v_tenant,1,0,clock_timestamp())
     ON CONFLICT(tenant_id) DO UPDATE SET generation={{SCHEMA}}.tenant_operations_projection_dirty.generation+1,
       dirty_since=CASE WHEN {{SCHEMA}}.tenant_operations_projection_dirty.generation={{SCHEMA}}.tenant_operations_projection_dirty.projected_generation
         THEN clock_timestamp() ELSE {{SCHEMA}}.tenant_operations_projection_dirty.dirty_since END;
 END IF;
 RETURN NULL;
END
$fn$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_operations_mark_dirty() FROM PUBLIC;
-- PostgreSQL checks function EXECUTE when the migrator creates each trigger.
-- The temporary grant is removed by the function owner after trigger creation.
GRANT EXECUTE ON FUNCTION {{SCHEMA}}.tenant_operations_mark_dirty() TO SESSION_USER;
RESET ROLE;

CREATE TRIGGER tenant_ops_projection_dirty AFTER INSERT OR UPDATE OR DELETE ON {{SCHEMA}}.cases FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.tenant_operations_mark_dirty();
CREATE TRIGGER tenant_ops_projection_dirty AFTER INSERT OR UPDATE OR DELETE ON {{SCHEMA}}.review_runs FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.tenant_operations_mark_dirty();
CREATE TRIGGER tenant_ops_projection_dirty AFTER INSERT OR UPDATE OR DELETE ON {{SCHEMA}}.review_attempts FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.tenant_operations_mark_dirty();
CREATE TRIGGER tenant_ops_projection_dirty AFTER INSERT OR UPDATE OR DELETE ON {{SCHEMA}}.model_call_leases FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.tenant_operations_mark_dirty();
CREATE TRIGGER tenant_ops_projection_dirty AFTER INSERT OR UPDATE OR DELETE ON {{SCHEMA}}.model_usage FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.tenant_operations_mark_dirty();
CREATE TRIGGER tenant_ops_projection_dirty AFTER INSERT OR UPDATE OR DELETE ON {{SCHEMA}}.model_money_ledger FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.tenant_operations_mark_dirty();
INSERT INTO {{SCHEMA}}.tenant_operations_projection_dirty(tenant_id,generation,projected_generation,dirty_since)
SELECT tenant_id,1,0,clock_timestamp() FROM {{SCHEMA}}.identity_tenants ON CONFLICT(tenant_id) DO NOTHING;
CREATE TRIGGER tenant_ops_projection_dirty AFTER INSERT OR UPDATE OR DELETE ON {{SCHEMA}}.identity_tenants FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.tenant_operations_mark_dirty();

SET LOCAL ROLE {{AUTH_FENCE_OWNER}};
REVOKE EXECUTE ON FUNCTION {{SCHEMA}}.tenant_operations_mark_dirty() FROM SESSION_USER;
CREATE FUNCTION {{SCHEMA}}.tenant_jurisdiction_operations(p_actor text,p_principal uuid,p_membership uuid,p_from date,p_to date)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $fn$
DECLARE v_scope_count integer; v_tenant record; v_observed timestamptz:=statement_timestamp(); v_snapshot text:=pg_current_snapshot()::text;
        v_jurisdictions jsonb; v_token text; v_projection text;
        v_dirty timestamptz; v_built timestamptz; v_age bigint; v_result jsonb;
BEGIN
 IF p_actor IS NULL OR p_actor='' OR p_actor<>current_setting('app.actor_id',true) OR
    p_principal IS NULL OR p_membership IS NULL OR p_from IS NULL OR p_to IS NULL OR p_to<p_from OR p_to>p_from+30 OR
    current_setting('app.tenant_id',true) IS NULL THEN
   RAISE EXCEPTION 'jurisdiction operations scope invalid' USING ERRCODE='42501';
 END IF;
 -- The only city/operator scope is an explicit grant in the caller's current
 -- database-resolved membership. Neither requested IDs nor role labels expand it.
 SELECT count(DISTINCT g->>'jurisdiction_id')::integer INTO v_scope_count
 FROM {{SCHEMA}}.identity_memberships m
 JOIN {{SCHEMA}}.identity_principals p ON p.principal_id=m.principal_id
 CROSS JOIN LATERAL jsonb_array_elements(m.grants) g
 WHERE m.membership_id=p_membership AND m.principal_id=p_principal AND m.tenant_id=current_setting('app.tenant_id',true)
   AND p.actor_id=p_actor AND p.status='active' AND m.status='active' AND m.role_code='admin'
   AND (m.effective_from IS NULL OR m.effective_from<=v_observed) AND (m.effective_until IS NULL OR m.effective_until>v_observed)
   AND g->>'role'='jurisdiction_operator' AND g->>'scope'='jurisdiction'
   AND jsonb_typeof(g->'tenant_ids')='array' AND jsonb_array_length(g->'tenant_ids')>0
   AND jsonb_typeof(g->'metrics')='array' AND jsonb_array_length(g->'metrics')>0;
 IF v_scope_count=0 THEN RAISE EXCEPTION 'jurisdiction metric grant required' USING ERRCODE='42501'; END IF;
 FOR v_tenant IN
   SELECT DISTINCT t.tenant_id,t.display_name,t.status,t.jurisdiction_id,
     ARRAY(SELECT DISTINCT metric.value FROM {{SCHEMA}}.identity_memberships m
       JOIN {{SCHEMA}}.identity_principals p ON p.principal_id=m.principal_id
       CROSS JOIN LATERAL jsonb_array_elements(m.grants) g
       CROSS JOIN LATERAL jsonb_array_elements_text(g->'metrics') AS metric(value)
       WHERE m.membership_id=p_membership AND m.principal_id=p_principal AND m.tenant_id=current_setting('app.tenant_id',true)
         AND p.actor_id=p_actor AND p.status='active' AND m.status='active' AND m.role_code='admin'
         AND g->>'role'='jurisdiction_operator' AND g->>'scope'='jurisdiction' AND g->>'jurisdiction_id'=t.jurisdiction_id
         AND (g->'tenant_ids') ? t.tenant_id)
       AS metrics
   FROM {{SCHEMA}}.identity_tenants t
   JOIN {{SCHEMA}}.identity_jurisdictions j ON j.jurisdiction_id=t.jurisdiction_id AND j.status='active'
   JOIN {{SCHEMA}}.identity_memberships m ON m.membership_id=p_membership AND m.principal_id=p_principal AND m.tenant_id=current_setting('app.tenant_id',true)
   JOIN {{SCHEMA}}.identity_principals p ON p.principal_id=m.principal_id AND p.actor_id=p_actor AND p.status='active'
   CROSS JOIN LATERAL jsonb_array_elements(m.grants) g
   WHERE m.status='active' AND m.role_code='admin' AND (m.effective_from IS NULL OR m.effective_from<=v_observed)
     AND (m.effective_until IS NULL OR m.effective_until>v_observed) AND g->>'role'='jurisdiction_operator'
     AND g->>'scope'='jurisdiction' AND g->>'jurisdiction_id'=t.jurisdiction_id AND (g->'tenant_ids') ? t.tenant_id
     AND jsonb_array_length(g->'metrics')>0
   ORDER BY t.jurisdiction_id,t.tenant_id
 LOOP
   PERFORM set_config('app.operations_tenant',v_tenant.tenant_id,true);
   INSERT INTO {{SCHEMA}}.tenant_operations_projection_snapshots(tenant_id,from_date,to_date,source_generation,source_snapshot_token,built_at,
     cases_created,runs_completed,attempts_completed,money_known,settled_minor,unknown_hold_minor,quota_calls,reserved_tokens,unknown_usage_calls)
   SELECT v_tenant.tenant_id,p_from,p_to,d.generation,v_snapshot||':'||d.generation::text,v_observed,
     (SELECT count(DISTINCT c.id) FROM {{SCHEMA}}.cases c WHERE c.tenant_id=v_tenant.tenant_id
       AND (c.created_at::timestamptz AT TIME ZONE 'UTC')::date BETWEEN p_from AND p_to),
     (SELECT count(DISTINCT r.id) FROM {{SCHEMA}}.review_runs r WHERE r.tenant_id=v_tenant.tenant_id AND r.state='completed'
       AND (r.updated_at::timestamptz AT TIME ZONE 'UTC')::date BETWEEN p_from AND p_to),
     (SELECT count(DISTINCT a.id) FROM {{SCHEMA}}.review_attempts a WHERE a.tenant_id=v_tenant.tenant_id AND a.state='completed' AND a.finished_at IS NOT NULL
       AND (a.finished_at::timestamptz AT TIME ZONE 'UTC')::date BETWEEN p_from AND p_to),
     NOT EXISTS(SELECT 1 FROM {{SCHEMA}}.model_usage u WHERE u.tenant_id=v_tenant.tenant_id AND u.money_period_day BETWEEN p_from AND p_to
       AND u.money_cost_state='unconfigured') AND NOT EXISTS(
       SELECT 1 FROM {{SCHEMA}}.model_usage u WHERE u.tenant_id=v_tenant.tenant_id AND u.money_period_day BETWEEN p_from AND p_to
         AND u.money_cost_state='unknown_hold' AND (
           COALESCE((SELECT sum(l.amount_minor) FROM {{SCHEMA}}.model_money_ledger l WHERE l.tenant_id=u.tenant_id AND l.lease_id=u.lease_id
             AND l.event_kind IN ('reserve','release')),0)>0 OR
           NOT EXISTS(SELECT 1 FROM {{SCHEMA}}.model_money_ledger l WHERE l.tenant_id=u.tenant_id AND l.lease_id=u.lease_id
             AND l.event_kind IN ('observed_cost','reconciliation')))),
     CASE WHEN EXISTS(SELECT 1 FROM {{SCHEMA}}.model_usage u WHERE u.tenant_id=v_tenant.tenant_id AND u.money_period_day BETWEEN p_from AND p_to
       AND u.money_cost_state='unknown_hold' AND NOT EXISTS(SELECT 1 FROM {{SCHEMA}}.model_money_ledger l
         WHERE l.tenant_id=u.tenant_id AND l.lease_id=u.lease_id AND l.event_kind='reserve')) THEN NULL ELSE
       COALESCE((SELECT sum(l.amount_minor) FROM {{SCHEMA}}.model_money_ledger l WHERE l.tenant_id=v_tenant.tenant_id AND l.period_day BETWEEN p_from AND p_to
         AND l.event_kind IN ('estimated_cost','observed_cost','adjustment','reconciliation')),0) END,
     (SELECT COALESCE(sum(holds.open_minor),0) FROM (
       SELECT u.lease_id,COALESCE(sum(l.amount_minor),0) AS open_minor FROM {{SCHEMA}}.model_usage u
       JOIN {{SCHEMA}}.model_money_ledger l ON l.tenant_id=u.tenant_id AND l.lease_id=u.lease_id
       WHERE u.tenant_id=v_tenant.tenant_id AND u.money_period_day BETWEEN p_from AND p_to AND u.money_cost_state='unknown_hold'
         AND l.event_kind IN ('reserve','unknown_hold','release') GROUP BY u.lease_id
     ) holds),
     (SELECT count(DISTINCT calls.lease_id) FROM (
       SELECT l.lease_id FROM {{SCHEMA}}.model_call_leases l WHERE l.tenant_id=v_tenant.tenant_id AND (l.acquired_at AT TIME ZONE 'UTC')::date BETWEEN p_from AND p_to
       UNION SELECT u.lease_id FROM {{SCHEMA}}.model_usage u WHERE u.tenant_id=v_tenant.tenant_id AND (COALESCE(u.reserved_at,u.occurred_at) AT TIME ZONE 'UTC')::date BETWEEN p_from AND p_to
     ) calls),
     (SELECT COALESCE(sum(r.tokens),0) FROM (
       SELECT l.lease_id,(l.reserved_input_tokens+l.reserved_output_tokens)::numeric AS tokens
       FROM {{SCHEMA}}.model_call_leases l WHERE l.tenant_id=v_tenant.tenant_id AND (l.acquired_at AT TIME ZONE 'UTC')::date BETWEEN p_from AND p_to
       UNION
       SELECT u.lease_id,COALESCE(u.reserved_input_tokens,0)::numeric+COALESCE(u.reserved_output_tokens,0)::numeric AS tokens
       FROM {{SCHEMA}}.model_usage u WHERE u.tenant_id=v_tenant.tenant_id AND (COALESCE(u.reserved_at,u.occurred_at) AT TIME ZONE 'UTC')::date BETWEEN p_from AND p_to
     ) r),
     (SELECT count(*) FROM {{SCHEMA}}.model_usage u WHERE u.tenant_id=v_tenant.tenant_id AND (COALESCE(u.reserved_at,u.occurred_at) AT TIME ZONE 'UTC')::date BETWEEN p_from AND p_to
       AND (u.input_tokens IS NULL OR u.output_tokens IS NULL OR u.total_tokens IS NULL))
   FROM {{SCHEMA}}.tenant_operations_projection_dirty d WHERE d.tenant_id=v_tenant.tenant_id
   ON CONFLICT(tenant_id,from_date,to_date) DO UPDATE SET source_generation=EXCLUDED.source_generation,source_snapshot_token=EXCLUDED.source_snapshot_token,
     built_at=EXCLUDED.built_at,cases_created=EXCLUDED.cases_created,runs_completed=EXCLUDED.runs_completed,attempts_completed=EXCLUDED.attempts_completed,
     money_known=EXCLUDED.money_known,settled_minor=EXCLUDED.settled_minor,unknown_hold_minor=EXCLUDED.unknown_hold_minor,
     quota_calls=EXCLUDED.quota_calls,reserved_tokens=EXCLUDED.reserved_tokens,unknown_usage_calls=EXCLUDED.unknown_usage_calls;
   UPDATE {{SCHEMA}}.tenant_operations_projection_dirty d SET projected_generation=s.source_generation,
     dirty_since=CASE WHEN d.generation=s.source_generation THEN NULL ELSE d.dirty_since END
   FROM {{SCHEMA}}.tenant_operations_projection_snapshots s WHERE s.tenant_id=v_tenant.tenant_id AND s.from_date=p_from AND s.to_date=p_to
     AND d.tenant_id=s.tenant_id AND d.generation>=s.source_generation;
 END LOOP;
 WITH scoped_grants AS (
   SELECT g->>'jurisdiction_id' AS jurisdiction_id,tenant_id.value AS tenant_id,
     ARRAY(SELECT jsonb_array_elements_text(g->'metrics')) AS metrics
   FROM {{SCHEMA}}.identity_memberships m
   JOIN {{SCHEMA}}.identity_principals p ON p.principal_id=m.principal_id
   CROSS JOIN LATERAL jsonb_array_elements(m.grants) g
   CROSS JOIN LATERAL jsonb_array_elements_text(g->'tenant_ids') tenant_id(value)
   WHERE m.membership_id=p_membership AND m.principal_id=p_principal AND m.tenant_id=current_setting('app.tenant_id',true)
     AND p.actor_id=p_actor AND p.status='active' AND m.status='active' AND m.role_code='admin'
     AND (m.effective_from IS NULL OR m.effective_from<=v_observed) AND (m.effective_until IS NULL OR m.effective_until>v_observed)
     AND g->>'role'='jurisdiction_operator' AND g->>'scope'='jurisdiction'
     AND jsonb_typeof(g->'tenant_ids')='array' AND jsonb_array_length(g->'tenant_ids')>0
     AND jsonb_typeof(g->'metrics')='array' AND jsonb_array_length(g->'metrics')>0
 ), allowed_tenants AS (
   SELECT t.tenant_id,t.display_name,t.status,t.jurisdiction_id,
     ARRAY(SELECT DISTINCT metric.value FROM scoped_grants sg CROSS JOIN LATERAL unnest(sg.metrics) AS metric(value) WHERE sg.tenant_id=t.tenant_id AND sg.jurisdiction_id=t.jurisdiction_id) AS metrics
   FROM {{SCHEMA}}.identity_tenants t
   JOIN {{SCHEMA}}.identity_jurisdictions j ON j.jurisdiction_id=t.jurisdiction_id AND j.status='active'
   WHERE EXISTS(SELECT 1 FROM scoped_grants sg WHERE sg.tenant_id=t.tenant_id AND sg.jurisdiction_id=t.jurisdiction_id)
 ), rows AS (
   SELECT a.*,s.source_snapshot_token,s.built_at,s.cases_created,s.runs_completed,s.attempts_completed,
     s.money_known,s.settled_minor,s.unknown_hold_minor,s.quota_calls,s.reserved_tokens,s.unknown_usage_calls,
     d.generation>d.projected_generation AS stale,d.dirty_since,
     extract(epoch FROM (v_observed-s.built_at))::bigint AS age_seconds
   FROM allowed_tenants a
   JOIN {{SCHEMA}}.tenant_operations_projection_snapshots s ON s.tenant_id=a.tenant_id AND s.from_date=p_from AND s.to_date=p_to
   JOIN {{SCHEMA}}.tenant_operations_projection_dirty d ON d.tenant_id=a.tenant_id
 ), jurisdiction_rows AS (
   SELECT jurisdiction_id,
     jsonb_agg(jsonb_build_object('tenant_id',tenant_id,'tenant_name',display_name,'status',status,
       'metrics',jsonb_build_object(
         'cases_created',CASE WHEN 'cases'=ANY(metrics) THEN cases_created::text ELSE NULL END,
         'runs_completed',CASE WHEN 'runs'=ANY(metrics) THEN runs_completed::text ELSE NULL END,
         'attempts_completed',CASE WHEN 'attempts'=ANY(metrics) THEN attempts_completed::text ELSE NULL END,
         'money',CASE WHEN 'money'=ANY(metrics) THEN jsonb_build_object('currency','USD','minor_unit_scale',6,'known',money_known,'settled_minor',settled_minor::text,'unknown_hold_minor',unknown_hold_minor::text) ELSE NULL END,
         'logical_quota',CASE WHEN 'quota'=ANY(metrics) THEN jsonb_build_object('period_basis','selected_utc_date_range','calls',quota_calls::text,'reserved_tokens',reserved_tokens::text,'unknown_usage_calls',unknown_usage_calls::text) ELSE NULL END,
         'shared_pool',CASE WHEN 'shared_pool'=ANY(metrics) THEN jsonb_build_object('attribution','unattributed_shared','capacity',NULL,'in_use',NULL) ELSE NULL END)) ORDER BY tenant_id) AS tenants,
     jsonb_build_object(
       'cases_created',CASE WHEN bool_and('cases'=ANY(metrics)) THEN sum(cases_created)::text ELSE NULL END,
       'runs_completed',CASE WHEN bool_and('runs'=ANY(metrics)) THEN sum(runs_completed)::text ELSE NULL END,
       'attempts_completed',CASE WHEN bool_and('attempts'=ANY(metrics)) THEN sum(attempts_completed)::text ELSE NULL END,
       'money',CASE WHEN bool_and('money'=ANY(metrics)) THEN jsonb_build_object('currency','USD','minor_unit_scale',6,'known',bool_and(money_known),
         'settled_minor',CASE WHEN bool_and(money_known) THEN sum(settled_minor)::text ELSE NULL END,'unknown_hold_minor',sum(unknown_hold_minor)::text) ELSE NULL END,
       'logical_quota',CASE WHEN bool_and('quota'=ANY(metrics)) THEN jsonb_build_object('period_basis','selected_utc_date_range','calls',sum(quota_calls)::text,'reserved_tokens',sum(reserved_tokens)::text,'unknown_usage_calls',sum(unknown_usage_calls)::text) ELSE NULL END,
       'shared_pool',CASE WHEN bool_and('shared_pool'=ANY(metrics)) THEN jsonb_build_object('attribution','unattributed_shared','capacity',NULL,'in_use',NULL) ELSE NULL END) AS totals,
     bool_or(stale) AS stale,min(dirty_since) AS dirty_since,min(built_at) AS built_at,
     max(age_seconds) AS age_seconds,string_agg(tenant_id||':'||source_snapshot_token,',' ORDER BY tenant_id) AS tokens
   FROM rows GROUP BY jurisdiction_id
 )
 SELECT COALESCE(jsonb_agg(jsonb_build_object('jurisdiction_id',j.jurisdiction_id,'tenants',j.tenants,'totals',j.totals) ORDER BY j.jurisdiction_id),'[]'::jsonb),
   CASE WHEN bool_or(j.stale) THEN 'stale' ELSE 'current' END,
   min(j.dirty_since),min(j.built_at),max(j.age_seconds),
   CASE WHEN count(*)=0 THEN NULL ELSE md5(string_agg(j.jurisdiction_id||':'||j.tokens,',' ORDER BY j.jurisdiction_id)) END
 INTO v_jurisdictions,v_projection,v_dirty,v_built,v_age,v_token FROM jurisdiction_rows j;
 v_result:=jsonb_build_object('schema_version','jurisdiction-operations/v1','observed_at',v_observed,'watermark_kind','pg_snapshot','watermark',NULL,
   'projection_state',COALESCE(v_projection,'current'),'source_snapshot_token',v_token,'built_at',v_built,'dirty_since',v_dirty,
   'projection_age_seconds',v_age,'from_date',p_from,'to_date',p_to,'jurisdictions',COALESCE(v_jurisdictions,'[]'::jsonb));
 RETURN v_result;
END
$fn$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.tenant_jurisdiction_operations(text,uuid,uuid,date,date) FROM PUBLIC;

RESET ROLE;
REVOKE CREATE ON SCHEMA {{SCHEMA}} FROM {{AUTH_FENCE_OWNER}};
