-- Budget functions retain the schema-specific advisory lock and service isolation.
DROP FUNCTION IF EXISTS {{SCHEMA}}.model_reserve_call(text,text,text,text,text,text,text,text,integer,integer,integer,integer,integer,integer);
DROP FUNCTION IF EXISTS {{SCHEMA}}.model_settle_call(text,text,text);
DO $migration$
BEGIN
 IF to_regclass(format('%I.%I',current_schema(),'model_budget_ledger')) IS NOT NULL THEN
  EXECUTE format('ALTER TABLE %I.%I ENABLE ROW LEVEL SECURITY',current_schema(),'model_budget_ledger');
  EXECUTE format('ALTER TABLE %I.%I FORCE ROW LEVEL SECURITY',current_schema(),'model_budget_ledger');
  EXECUTE format('DROP POLICY IF EXISTS tenant_boundary ON %I.%I',current_schema(),'model_budget_ledger');
  EXECUTE format('CREATE POLICY tenant_boundary ON %I.%I USING (tenant_id=current_setting(%L,true)) WITH CHECK (tenant_id=current_setting(%L,true))',current_schema(),'model_budget_ledger','app.tenant_id','app.tenant_id');
  EXECUTE format('REVOKE ALL ON TABLE %I.%I FROM PUBLIC',current_schema(),'model_budget_ledger');
 END IF;
END $migration$;

CREATE OR REPLACE FUNCTION {{SCHEMA}}.model_budget_lock_experience_job(
	p_tenant text, p_org text, p_case text, p_actor text, p_job text, p_worker text,
	p_generation bigint, p_input_sha256 text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $function$
BEGIN
	PERFORM 1 FROM {{SCHEMA}}.worker_jobs j
	JOIN {{SCHEMA}}.experience_distill_inputs i ON i.job_id=j.id
	WHERE j.id=p_job AND j.kind='experience_distill' AND j.state='running'
		AND j.tenant_id=p_tenant AND j.org_id=p_org AND j.case_id=p_case AND j.actor_id=p_actor
		AND j.worker_id=p_worker AND j.worker_id<>'' AND j.generation=p_generation AND j.generation>0
		AND j.lease_until>pg_catalog.clock_timestamp()
		AND i.tenant_id=j.tenant_id AND i.org_id=j.org_id AND i.case_id=j.case_id AND i.actor_id=j.actor_id
		AND i.input_sha256=p_input_sha256
		AND p_tenant=pg_catalog.current_setting('app.tenant_id',true)
		AND p_org=pg_catalog.current_setting('app.org_id',true)
		AND pg_catalog.current_setting('app.actor_role',true)='model'
	FOR UPDATE OF j;
	IF NOT FOUND THEN
		RETURN false;
	END IF;
	-- A row can expire while this transaction waits for its lock.
	RETURN EXISTS (SELECT 1 FROM {{SCHEMA}}.worker_jobs j
		WHERE j.id=p_job AND j.lease_until>pg_catalog.clock_timestamp());
END
$function$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.model_budget_lock_experience_job(text,text,text,text,text,text,bigint,text) FROM PUBLIC;

CREATE OR REPLACE FUNCTION {{SCHEMA}}.model_budget_reserve(
	p_lease_id text, p_tenant text, p_actor text, p_case text, p_run text, p_attempt text,
	p_caller text, p_model_ref text, p_attempt_number integer,
	p_global_max integer, p_tenant_max integer, p_global_per_minute integer, p_tenant_per_minute integer,
	p_global_tokens bigint, p_tenant_tokens bigint, p_input_upper bigint, p_output_upper bigint,
	p_lease_milliseconds bigint) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $function$
DECLARE
	v_now timestamptz;
	v_global_inflight bigint;
	v_tenant_inflight bigint;
	v_global_calls bigint;
	v_tenant_calls bigint;
	v_global_tokens numeric;
	v_tenant_tokens numeric;
	v_purpose text := COALESCE(NULLIF(pg_catalog.current_setting('app.model_purpose',true),''),'review');
	v_job_id text := COALESCE(pg_catalog.current_setting('app.job_id',true),'');
	v_job_generation bigint := COALESCE(NULLIF(pg_catalog.current_setting('app.job_generation',true),''),'0')::bigint;
	v_input_sha256 text := COALESCE(pg_catalog.current_setting('app.model_input_sha256',true),'');
BEGIN
	IF p_lease_id IS NULL OR p_lease_id = '' OR p_tenant IS NULL OR p_tenant = '' OR p_actor IS NULL OR p_actor = '' OR
		p_case IS NULL OR p_case = '' OR p_run IS NULL OR p_attempt IS NULL OR p_caller IS NULL OR
		p_caller NOT IN ('worker','document') OR p_model_ref IS NULL OR p_model_ref = '' OR p_attempt_number IS NULL OR p_attempt_number < 1 OR
		p_global_max IS NULL OR p_tenant_max IS NULL OR p_global_max < 1 OR p_tenant_max < 1 OR
		p_global_per_minute IS NULL OR p_tenant_per_minute IS NULL OR p_global_per_minute < 1 OR p_tenant_per_minute < 1 OR
		p_global_tokens IS NULL OR p_tenant_tokens IS NULL OR p_global_tokens < 1 OR p_tenant_tokens < 1 OR
		p_input_upper IS NULL OR p_output_upper IS NULL OR p_input_upper < 1 OR p_output_upper < 1 OR
		p_input_upper::numeric + p_output_upper::numeric > 9223372036854775807 OR
		p_lease_milliseconds IS NULL OR p_lease_milliseconds < 1 THEN
		RAISE EXCEPTION 'invalid model budget reservation' USING ERRCODE = '22023';
	END IF;
	IF v_purpose = 'experience_distill' THEN
		IF p_caller <> 'worker' OR p_run <> '' OR p_attempt <> '' OR v_job_id = '' OR
			v_job_generation < 1 OR v_input_sha256 !~ '^[0-9a-f]{64}$' THEN
			RAISE EXCEPTION 'invalid experience model purpose' USING ERRCODE = '22023';
		END IF;
		-- Always take the job row before the global budget lock, including for
		-- direct SQL callers. The gateway already holds it through authorization.
		IF NOT {{SCHEMA}}.model_budget_lock_experience_job(p_tenant,
			pg_catalog.current_setting('app.org_id',true), p_case, p_actor, v_job_id,
			pg_catalog.current_setting('app.worker_id',true), v_job_generation, v_input_sha256) THEN
			RAISE EXCEPTION 'experience model execution scope is unavailable' USING ERRCODE = 'P7M01';
		END IF;
	END IF;
	PERFORM pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended('{{LOCK_KEY}}', 0));
	IF v_purpose = 'experience_distill' AND NOT {{SCHEMA}}.model_budget_lock_experience_job(p_tenant,
		pg_catalog.current_setting('app.org_id',true), p_case, p_actor, v_job_id,
		pg_catalog.current_setting('app.worker_id',true), v_job_generation, v_input_sha256) THEN
		RAISE EXCEPTION 'experience model execution scope is unavailable' USING ERRCODE = 'P7M01';
	END IF;
	v_now := pg_catalog.clock_timestamp();
	WITH expired AS (
		DELETE FROM {{SCHEMA}}.model_call_leases WHERE expires_at <= v_now RETURNING *
	)
	INSERT INTO {{SCHEMA}}.model_usage
		(call_id, lease_id, tenant_id, actor_id, case_id, run_id, attempt_id, caller_identity, model_ref,
		 attempt_number, call_status, error_code, usage_source, input_tokens, output_tokens, total_tokens, occurred_at,
		 reserved_at, reserved_input_tokens, reserved_output_tokens, charged_tokens, budget_released, settlement_fingerprint,
		 purpose, job_id, job_generation, input_sha256)
	SELECT 'mc_unknown_' || e.lease_id, e.lease_id, e.tenant_id, e.actor_id, e.case_id, e.run_id, e.attempt_id,
		e.caller_identity, e.model_ref, e.attempt_number, 'failed', 'model_call_outcome_unknown', NULL, NULL, NULL, NULL,
		e.expires_at, e.acquired_at, e.reserved_input_tokens, e.reserved_output_tokens,
		e.reserved_input_tokens + e.reserved_output_tokens, false, 'expired:' || e.lease_id,
		e.purpose, e.job_id, e.job_generation, e.input_sha256
	FROM expired e
	ON CONFLICT (lease_id) DO NOTHING;

	SELECT
		(SELECT count(*) FROM {{SCHEMA}}.model_call_leases l WHERE l.expires_at > v_now),
		(SELECT count(*) FROM {{SCHEMA}}.model_call_leases l WHERE l.tenant_id = p_tenant AND l.expires_at > v_now),
		(SELECT count(*) FROM {{SCHEMA}}.model_usage u WHERE COALESCE(u.reserved_at,u.occurred_at) >= v_now - interval '1 minute' AND NOT u.budget_released) +
			(SELECT count(*) FROM {{SCHEMA}}.model_call_leases l WHERE l.acquired_at >= v_now - interval '1 minute'),
		(SELECT count(*) FROM {{SCHEMA}}.model_usage u WHERE u.tenant_id = p_tenant AND COALESCE(u.reserved_at,u.occurred_at) >= v_now - interval '1 minute' AND NOT u.budget_released) +
			(SELECT count(*) FROM {{SCHEMA}}.model_call_leases l WHERE l.tenant_id = p_tenant AND l.acquired_at >= v_now - interval '1 minute'),
		(SELECT COALESCE(sum(COALESCE(u.charged_tokens,
			CASE WHEN u.total_tokens IS NOT NULL THEN u.total_tokens ELSE COALESCE(u.input_tokens,0) + COALESCE(u.output_tokens,0) END)),0)
			FROM {{SCHEMA}}.model_usage u WHERE COALESCE(u.reserved_at,u.occurred_at) >= v_now - interval '1 minute' AND NOT u.budget_released) +
			(SELECT COALESCE(sum(l.reserved_input_tokens + l.reserved_output_tokens),0)
			FROM {{SCHEMA}}.model_call_leases l WHERE l.acquired_at >= v_now - interval '1 minute'),
		(SELECT COALESCE(sum(COALESCE(u.charged_tokens,
			CASE WHEN u.total_tokens IS NOT NULL THEN u.total_tokens ELSE COALESCE(u.input_tokens,0) + COALESCE(u.output_tokens,0) END)),0)
			FROM {{SCHEMA}}.model_usage u WHERE u.tenant_id = p_tenant AND COALESCE(u.reserved_at,u.occurred_at) >= v_now - interval '1 minute' AND NOT u.budget_released) +
			(SELECT COALESCE(sum(l.reserved_input_tokens + l.reserved_output_tokens),0)
			FROM {{SCHEMA}}.model_call_leases l WHERE l.tenant_id = p_tenant AND l.acquired_at >= v_now - interval '1 minute')
	INTO v_global_inflight, v_tenant_inflight, v_global_calls, v_tenant_calls, v_global_tokens, v_tenant_tokens;

	IF v_global_inflight >= p_global_max OR v_tenant_inflight >= p_tenant_max OR
		v_global_calls >= p_global_per_minute OR v_tenant_calls >= p_tenant_per_minute OR
		v_global_tokens + p_input_upper::numeric + p_output_upper::numeric > p_global_tokens::numeric OR
		v_tenant_tokens + p_input_upper::numeric + p_output_upper::numeric > p_tenant_tokens::numeric THEN
		RETURN false;
	END IF;
	INSERT INTO {{SCHEMA}}.model_call_leases
		(lease_id, tenant_id, actor_id, case_id, run_id, attempt_id, caller_identity, model_ref, attempt_number,
		 reserved_input_tokens, reserved_output_tokens, acquired_at, expires_at, purpose, job_id, job_generation, input_sha256)
	VALUES (p_lease_id, p_tenant, p_actor, p_case, p_run, p_attempt, p_caller, p_model_ref, p_attempt_number,
		p_input_upper, p_output_upper, v_now, v_now + p_lease_milliseconds * interval '1 millisecond', v_purpose, v_job_id, v_job_generation, v_input_sha256);
	RETURN true;
END
$function$;

REVOKE ALL ON FUNCTION {{SCHEMA}}.model_budget_reserve(text,text,text,text,text,text,text,text,integer,integer,integer,integer,integer,bigint,bigint,bigint,bigint,bigint) FROM PUBLIC;

CREATE OR REPLACE FUNCTION {{SCHEMA}}.model_budget_finish(
	p_lease_id text, p_tenant text, p_actor text, p_case text, p_run text, p_attempt text,
	p_caller text, p_model_ref text, p_attempt_number integer,
	p_status text, p_error_code text, p_usage_source text, p_input bigint, p_output bigint, p_total bigint,
	p_occurred_at timestamptz, p_pre_dispatch_failure boolean, p_fingerprint text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $function$
DECLARE
	v_lease record;
	v_usage record;
	v_charged numeric;
	v_purpose text := COALESCE(NULLIF(pg_catalog.current_setting('app.model_purpose',true),''),'review');
	v_job_id text := COALESCE(pg_catalog.current_setting('app.job_id',true),'');
	v_job_generation bigint := COALESCE(NULLIF(pg_catalog.current_setting('app.job_generation',true),''),'0')::bigint;
	v_input_sha256 text := COALESCE(pg_catalog.current_setting('app.model_input_sha256',true),'');
BEGIN
	IF p_lease_id IS NULL OR p_lease_id = '' OR p_tenant IS NULL OR p_tenant = '' OR p_actor IS NULL OR p_actor = '' OR
		p_case IS NULL OR p_case = '' OR p_run IS NULL OR p_attempt IS NULL OR p_caller IS NULL OR
		p_caller NOT IN ('worker','document') OR p_model_ref IS NULL OR p_model_ref = '' OR p_attempt_number IS NULL OR p_attempt_number < 1 OR
		p_status IS NULL OR p_status NOT IN ('completed','failed') OR p_fingerprint IS NULL OR p_fingerprint = '' OR
		p_pre_dispatch_failure IS NULL OR p_input < 0 OR p_output < 0 OR p_total < 0 OR
		(p_pre_dispatch_failure AND (p_status <> 'failed' OR p_input IS NOT NULL OR p_output IS NOT NULL OR p_total IS NOT NULL)) THEN
		RAISE EXCEPTION 'invalid model call settlement' USING ERRCODE = '22023';
	END IF;
	PERFORM pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended('{{LOCK_KEY}}', 0));
	SELECT * INTO v_usage FROM {{SCHEMA}}.model_usage u WHERE u.lease_id = p_lease_id AND u.tenant_id = p_tenant;
	IF FOUND THEN
		RETURN COALESCE(v_usage.tenant_id = p_tenant AND v_usage.actor_id = p_actor AND v_usage.case_id = p_case AND
			v_usage.run_id = p_run AND v_usage.attempt_id = p_attempt AND v_usage.caller_identity = p_caller AND
			v_usage.model_ref = p_model_ref AND v_usage.attempt_number = p_attempt_number AND
			v_usage.purpose = v_purpose AND v_usage.job_id = v_job_id AND v_usage.job_generation = v_job_generation AND
			v_usage.input_sha256 = v_input_sha256 AND v_usage.settlement_fingerprint = p_fingerprint, false);
	END IF;
	SELECT * INTO v_lease FROM {{SCHEMA}}.model_call_leases l WHERE l.lease_id = p_lease_id AND l.tenant_id = p_tenant FOR UPDATE;
	IF NOT FOUND OR v_lease.actor_id <> p_actor OR v_lease.case_id <> p_case OR v_lease.run_id <> p_run OR
		v_lease.attempt_id <> p_attempt OR v_lease.caller_identity <> p_caller OR v_lease.model_ref <> p_model_ref OR
		v_lease.attempt_number <> p_attempt_number OR v_lease.purpose <> v_purpose OR v_lease.job_id <> v_job_id OR
		v_lease.job_generation <> v_job_generation OR v_lease.input_sha256 <> v_input_sha256 THEN
		RETURN false;
	END IF;
	IF p_pre_dispatch_failure THEN
		v_charged := 0;
	ELSIF p_input IS NULL AND p_output IS NULL AND p_total IS NULL THEN
		v_charged := v_lease.reserved_input_tokens::numeric + v_lease.reserved_output_tokens::numeric;
	ELSIF p_input IS NOT NULL AND p_output IS NOT NULL THEN
		v_charged := p_input::numeric + p_output::numeric;
	ELSIF p_input IS NOT NULL THEN
		v_charged := p_input::numeric + v_lease.reserved_output_tokens::numeric;
	ELSIF p_output IS NOT NULL THEN
		v_charged := v_lease.reserved_input_tokens::numeric + p_output::numeric;
	ELSE
		v_charged := p_total::numeric;
	END IF;
	IF p_total IS NOT NULL THEN
		v_charged := greatest(v_charged, p_total::numeric);
	END IF;
	v_charged := least(v_charged, 9223372036854775807::numeric);
	INSERT INTO {{SCHEMA}}.model_usage
		(call_id, lease_id, tenant_id, actor_id, case_id, run_id, attempt_id, caller_identity, model_ref,
		 attempt_number, call_status, error_code, usage_source, input_tokens, output_tokens, total_tokens, occurred_at,
		 reserved_at, reserved_input_tokens, reserved_output_tokens, charged_tokens, budget_released, settlement_fingerprint,
		 purpose, job_id, job_generation, input_sha256)
	VALUES ('mc_' || p_lease_id, p_lease_id, p_tenant, p_actor, p_case, p_run, p_attempt, p_caller, p_model_ref,
		p_attempt_number, p_status, COALESCE(p_error_code,''), p_usage_source, p_input, p_output, p_total,
		COALESCE(p_occurred_at,pg_catalog.clock_timestamp()), v_lease.acquired_at,
		v_lease.reserved_input_tokens, v_lease.reserved_output_tokens, v_charged::bigint, p_pre_dispatch_failure, p_fingerprint,
		v_lease.purpose, v_lease.job_id, v_lease.job_generation, v_lease.input_sha256);
	DELETE FROM {{SCHEMA}}.model_call_leases l WHERE l.lease_id = p_lease_id AND l.tenant_id = p_tenant;
	IF NOT FOUND THEN
		RAISE EXCEPTION 'model call lease disappeared during settlement' USING ERRCODE = '40001';
	END IF;
	RETURN true;
END
$function$;

REVOKE ALL ON FUNCTION {{SCHEMA}}.model_budget_finish(text,text,text,text,text,text,text,text,integer,text,text,text,bigint,bigint,bigint,timestamptz,boolean,text) FROM PUBLIC;

DO $migration$
DECLARE function_owner text; table_name text;
BEGIN
 SELECT r.rolname INTO function_owner FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 JOIN pg_roles r ON r.oid=p.proowner WHERE n.nspname=current_schema() AND p.proname='model_budget_reserve' LIMIT 1;
 IF function_owner IS NULL OR function_owner='' THEN RAISE EXCEPTION 'model budget function owner is unavailable'; END IF;
 FOREACH table_name IN ARRAY ARRAY['model_call_leases','model_usage'] LOOP
  EXECUTE format('DROP POLICY IF EXISTS model_budget_accounting_authority ON %I.%I',current_schema(),table_name);
  EXECUTE format('CREATE POLICY model_budget_accounting_authority ON %I.%I USING (current_user = %L) WITH CHECK (current_user = %L)',current_schema(),table_name,function_owner,function_owner);
 END LOOP;
END $migration$;
CREATE OR REPLACE FUNCTION {{SCHEMA}}.reject_model_usage_mutation()
 RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $function$
 BEGIN RAISE EXCEPTION 'model usage is immutable' USING ERRCODE = '55000'; END
 $function$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.reject_model_usage_mutation() FROM PUBLIC;
DROP TRIGGER IF EXISTS model_usage_immutable ON {{SCHEMA}}.model_usage;
CREATE TRIGGER model_usage_immutable BEFORE UPDATE OR DELETE ON {{SCHEMA}}.model_usage
 FOR EACH ROW EXECUTE FUNCTION {{SCHEMA}}.reject_model_usage_mutation();
