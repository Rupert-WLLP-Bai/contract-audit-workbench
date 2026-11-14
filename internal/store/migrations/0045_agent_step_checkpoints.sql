-- Agent planner/tool results are attempt-scoped recovery facts. A row starts
-- before an external call and may complete only through a same-generation CAS.
CREATE TABLE execution_agent_step_checkpoints (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 case_id text NOT NULL,
 run_id text NOT NULL,
 attempt_id text NOT NULL,
 step_id text NOT NULL CHECK (step_id ~ '^[A-Za-z0-9][A-Za-z0-9_./:-]{0,255}$'),
 generation bigint NOT NULL CHECK (generation > 0),
 attempt_input_sha256 text NOT NULL CHECK (attempt_input_sha256 ~ '^[0-9a-f]{64}$'),
 runtime_version text NOT NULL CHECK (btrim(runtime_version) <> ''),
 planner_id text NOT NULL CHECK (btrim(planner_id) <> ''),
 planner_configuration_id text NOT NULL CHECK (btrim(planner_configuration_id) <> ''),
 model_configuration_id text NOT NULL DEFAULT '',
 tool_catalog_version text NOT NULL CHECK (btrim(tool_catalog_version) <> ''),
 tool_catalog_sha256 text NOT NULL CHECK (tool_catalog_sha256 ~ '^[0-9a-f]{64}$'),
 step_kind text NOT NULL CHECK (step_kind IN ('planner','tool','knowledge_read')),
 round_number integer NOT NULL CHECK (round_number > 0),
 tool_id text NOT NULL DEFAULT '',
 tool_version text NOT NULL DEFAULT '',
 tool_configuration_sha256 text NOT NULL DEFAULT '',
 input_sha256 text NOT NULL CHECK (input_sha256 ~ '^[0-9a-f]{64}$'),
 status text NOT NULL CHECK (status IN ('started','succeeded','failed','ambiguous')),
 output_sha256 text NOT NULL DEFAULT '' CHECK (output_sha256 = '' OR output_sha256 ~ '^[0-9a-f]{64}$'),
 output_payload bytea,
 failure_code text NOT NULL DEFAULT '' CHECK (failure_code = '' OR failure_code ~ '^[a-z][a-z0-9_]{0,79}$'),
 started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 observed_at timestamptz,
 elapsed_ms bigint NOT NULL DEFAULT 0 CHECK (elapsed_ms >= 0),
 elapsed_known boolean NOT NULL DEFAULT false,
 calls_consumed integer NOT NULL DEFAULT 1 CHECK (calls_consumed > 0),
 usage_known boolean NOT NULL DEFAULT false,
 usage_status text NOT NULL DEFAULT 'unknown' CHECK (usage_status IN ('observed','unknown','not_applicable')),
 input_tokens bigint,
 output_tokens bigint,
 total_tokens bigint,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,attempt_id,step_id),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases (tenant_id,org_id,id),
 FOREIGN KEY (tenant_id,org_id,case_id,run_id) REFERENCES review_runs (tenant_id,org_id,case_id,id),
 FOREIGN KEY (tenant_id,org_id,run_id,attempt_id) REFERENCES review_attempts (tenant_id,org_id,run_id,id),
 CHECK ((step_kind IN ('planner','knowledge_read') AND tool_id='' AND tool_version='' AND tool_configuration_sha256='') OR
        (step_kind='tool' AND tool_id ~ '^[A-Za-z0-9][A-Za-z0-9_./:-]{0,255}$' AND btrim(tool_version)<>'' AND tool_configuration_sha256 ~ '^[0-9a-f]{64}$')),
 CHECK ((usage_status='observed' AND usage_known AND input_tokens IS NOT NULL AND input_tokens>=0
          AND output_tokens IS NOT NULL AND output_tokens>=0 AND total_tokens IS NOT NULL AND total_tokens>=0) OR
        (usage_status='unknown' AND NOT usage_known AND input_tokens IS NULL AND output_tokens IS NULL AND total_tokens IS NULL) OR
        (usage_status='not_applicable' AND usage_known AND input_tokens IS NULL AND output_tokens IS NULL AND total_tokens IS NULL)),
 CHECK ((status='started' AND output_sha256='' AND output_payload IS NULL AND failure_code=''
          AND observed_at IS NULL AND elapsed_ms=0 AND elapsed_known=false AND usage_status='unknown') OR
        (status='succeeded' AND output_sha256<>'' AND output_payload IS NOT NULL AND octet_length(output_payload)<=4194304
          AND failure_code='' AND observed_at IS NOT NULL AND elapsed_known) OR
        (status='failed' AND output_sha256='' AND output_payload IS NULL AND failure_code<>'' AND observed_at IS NOT NULL AND elapsed_known) OR
        (status='ambiguous' AND output_sha256='' AND output_payload IS NULL AND failure_code<>''
          AND observed_at IS NULL AND elapsed_known=false AND usage_status='unknown'))
);

CREATE INDEX execution_agent_step_checkpoints_attempt_recovery
 ON execution_agent_step_checkpoints (tenant_id,org_id,case_id,run_id,attempt_id,status);

ALTER TABLE execution_agent_step_checkpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE execution_agent_step_checkpoints FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON execution_agent_step_checkpoints
 USING (
  tenant_id=current_setting('app.tenant_id',true)
  AND org_id=current_setting('app.org_id',true)
  AND EXISTS (SELECT 1 FROM service_database_roles role_binding
    WHERE role_binding.role_name=session_user AND role_binding.service='execution')
  AND EXISTS (SELECT 1 FROM worker_jobs claim
    WHERE claim.id=NULLIF(current_setting('app.job_id',true),'')
      AND claim.tenant_id=execution_agent_step_checkpoints.tenant_id
      AND claim.org_id=execution_agent_step_checkpoints.org_id
      AND claim.case_id=execution_agent_step_checkpoints.case_id
      AND claim.run_id=execution_agent_step_checkpoints.run_id
      AND claim.attempt_id=execution_agent_step_checkpoints.attempt_id
      AND claim.worker_id=NULLIF(current_setting('app.worker_id',true),'')
      AND claim.generation=NULLIF(current_setting('app.job_generation',true),'')::bigint
      AND claim.state='running' AND claim.lease_until>clock_timestamp())
 )
 WITH CHECK (
  tenant_id=current_setting('app.tenant_id',true)
  AND org_id=current_setting('app.org_id',true)
  AND execution_agent_step_checkpoints.generation=NULLIF(current_setting('app.job_generation',true),'')::bigint
  AND EXISTS (SELECT 1 FROM service_database_roles role_binding
    WHERE role_binding.role_name=session_user AND role_binding.service='execution')
  AND EXISTS (SELECT 1 FROM worker_jobs claim
    WHERE claim.id=NULLIF(current_setting('app.job_id',true),'')
      AND claim.tenant_id=execution_agent_step_checkpoints.tenant_id
      AND claim.org_id=execution_agent_step_checkpoints.org_id
      AND claim.case_id=execution_agent_step_checkpoints.case_id
      AND claim.run_id=execution_agent_step_checkpoints.run_id
      AND claim.attempt_id=execution_agent_step_checkpoints.attempt_id
      AND claim.worker_id=NULLIF(current_setting('app.worker_id',true),'')
      AND claim.generation=NULLIF(current_setting('app.job_generation',true),'')::bigint
      AND claim.state='running' AND claim.lease_until>clock_timestamp())
 );

CREATE FUNCTION guard_execution_agent_step_checkpoint() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $fn$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.status<>'started' OR
     NEW.generation IS DISTINCT FROM NULLIF(current_setting('app.job_generation',true),'')::bigint OR
     NEW.output_payload IS NOT NULL OR NEW.output_sha256<>'' OR NEW.failure_code<>'' OR
     NEW.observed_at IS NOT NULL OR NEW.elapsed_ms<>0 OR NEW.elapsed_known OR NEW.usage_status<>'unknown' OR NEW.usage_known THEN
   RAISE EXCEPTION 'Agent step checkpoint must begin in started state' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  RAISE EXCEPTION 'Agent step checkpoints are immutable facts' USING ERRCODE='23514';
 END IF;
 IF OLD.status<>'started' THEN
  RAISE EXCEPTION 'finished Agent step checkpoints are immutable' USING ERRCODE='23514';
 END IF;
 IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.org_id IS DISTINCT FROM OLD.org_id OR
    NEW.case_id IS DISTINCT FROM OLD.case_id OR NEW.run_id IS DISTINCT FROM OLD.run_id OR
    NEW.attempt_id IS DISTINCT FROM OLD.attempt_id OR NEW.step_id IS DISTINCT FROM OLD.step_id OR
    NEW.attempt_input_sha256 IS DISTINCT FROM OLD.attempt_input_sha256 OR
    NEW.runtime_version IS DISTINCT FROM OLD.runtime_version OR NEW.planner_id IS DISTINCT FROM OLD.planner_id OR
    NEW.planner_configuration_id IS DISTINCT FROM OLD.planner_configuration_id OR
    NEW.model_configuration_id IS DISTINCT FROM OLD.model_configuration_id OR
    NEW.tool_catalog_version IS DISTINCT FROM OLD.tool_catalog_version OR
    NEW.tool_catalog_sha256 IS DISTINCT FROM OLD.tool_catalog_sha256 OR NEW.step_kind IS DISTINCT FROM OLD.step_kind OR
    NEW.round_number IS DISTINCT FROM OLD.round_number OR NEW.tool_id IS DISTINCT FROM OLD.tool_id OR
    NEW.tool_version IS DISTINCT FROM OLD.tool_version OR
    NEW.tool_configuration_sha256 IS DISTINCT FROM OLD.tool_configuration_sha256 OR
    NEW.input_sha256 IS DISTINCT FROM OLD.input_sha256 OR NEW.started_at IS DISTINCT FROM OLD.started_at OR
    NEW.calls_consumed IS DISTINCT FROM OLD.calls_consumed THEN
  RAISE EXCEPTION 'Agent step checkpoint identity is immutable' USING ERRCODE='23514';
 END IF;
 IF NEW.status='ambiguous' THEN
  IF NEW.generation<OLD.generation OR
      NEW.generation IS DISTINCT FROM NULLIF(current_setting('app.job_generation',true),'')::bigint OR
      NEW.output_payload IS NOT NULL OR NEW.output_sha256<>'' OR
      NEW.failure_code='' OR NEW.observed_at IS NOT NULL OR NEW.elapsed_ms<>OLD.elapsed_ms OR NEW.elapsed_known OR
     NEW.usage_status<>'unknown' OR NEW.usage_known OR NEW.input_tokens IS NOT NULL OR
     NEW.output_tokens IS NOT NULL OR NEW.total_tokens IS NOT NULL THEN
   RAISE EXCEPTION 'invalid Agent step ambiguity transition' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.status NOT IN ('succeeded','failed') OR NEW.generation<>OLD.generation OR
    NEW.generation IS DISTINCT FROM NULLIF(current_setting('app.job_generation',true),'')::bigint THEN
  RAISE EXCEPTION 'Agent step completion requires its starting generation' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END
$fn$;
REVOKE ALL ON FUNCTION guard_execution_agent_step_checkpoint() FROM PUBLIC;
CREATE TRIGGER execution_agent_step_checkpoint_guard
 BEFORE INSERT OR UPDATE OR DELETE ON execution_agent_step_checkpoints
 FOR EACH ROW EXECUTE FUNCTION guard_execution_agent_step_checkpoint();

REVOKE ALL ON TABLE execution_agent_step_checkpoints FROM PUBLIC;
