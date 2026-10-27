package store

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Distillation inputs and results belong to Execution. The source feedback and
// candidate remain Core-owned. Neither a task nor a model call borrows a review
// attempt identity.
const experienceDistillSchemaSQL = `
ALTER TABLE worker_jobs ALTER COLUMN run_id DROP NOT NULL;
ALTER TABLE worker_jobs ALTER COLUMN attempt_id DROP NOT NULL;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='worker_jobs_kind_identity' AND connamespace=current_schema()::regnamespace) THEN
    ALTER TABLE worker_jobs ADD CONSTRAINT worker_jobs_kind_identity CHECK (
      (kind='review' AND run_id IS NOT NULL AND run_id<>'' AND attempt_id IS NOT NULL AND attempt_id<>'') OR
      (kind='experience_distill' AND run_id IS NULL AND attempt_id IS NULL)
    );
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS experience_distill_inputs (
  job_id text PRIMARY KEY REFERENCES worker_jobs(id),
  tenant_id text NOT NULL,
  org_id text NOT NULL,
  case_id text NOT NULL,
  actor_id text NOT NULL,
  feedback_id text NOT NULL,
  idempotency_key text NOT NULL,
  request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
  input_sha256 text NOT NULL CHECK (input_sha256 ~ '^[0-9a-f]{64}$'),
  input_bytes bytea NOT NULL CHECK (octet_length(input_bytes) BETWEEN 1 AND 65536),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (tenant_id,org_id,case_id,job_id),
  UNIQUE (tenant_id,actor_id,idempotency_key),
  FOREIGN KEY (tenant_id,org_id,case_id,feedback_id)
    REFERENCES experience_feedback(tenant_id,org_id,case_id,feedback_id)
);
CREATE TABLE IF NOT EXISTS experience_distill_results (
  job_id text NOT NULL REFERENCES experience_distill_inputs(job_id),
  generation bigint NOT NULL CHECK (generation > 0),
  tenant_id text NOT NULL,
  org_id text NOT NULL,
  case_id text NOT NULL,
  input_sha256 text NOT NULL CHECK (input_sha256 ~ '^[0-9a-f]{64}$'),
  result_sha256 text NOT NULL CHECK (result_sha256 ~ '^[0-9a-f]{64}$'),
  result_bytes bytea NOT NULL CHECK (octet_length(result_bytes) BETWEEN 1 AND 65536),
  model_ref text NOT NULL CHECK (model_ref<>''),
  template_ref text NOT NULL CHECK (template_ref<>''),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (job_id,generation),
  UNIQUE (tenant_id,org_id,case_id,job_id,generation,input_sha256,result_sha256),
  FOREIGN KEY (tenant_id,org_id,case_id,job_id)
    REFERENCES experience_distill_inputs(tenant_id,org_id,case_id,job_id)
);
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='experience_candidate_result_scope_fk' AND connamespace=current_schema()::regnamespace) THEN
    ALTER TABLE experience_candidate_versions ADD CONSTRAINT experience_candidate_result_scope_fk
      FOREIGN KEY (tenant_id,org_id,case_id,job_id,generation,input_sha256,result_sha256)
      REFERENCES experience_distill_results (tenant_id,org_id,case_id,job_id,generation,input_sha256,result_sha256);
  END IF;
END $$;
CREATE TABLE IF NOT EXISTS experience_distill_failures (
  job_id text NOT NULL REFERENCES experience_distill_inputs(job_id),
  generation bigint NOT NULL CHECK (generation > 0),
  tenant_id text NOT NULL,
  org_id text NOT NULL,
  case_id text NOT NULL,
  error_code text NOT NULL CHECK (error_code<>''),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (job_id,generation),
  FOREIGN KEY (tenant_id,org_id,case_id,job_id)
    REFERENCES experience_distill_inputs(tenant_id,org_id,case_id,job_id)
);
DROP TRIGGER IF EXISTS experience_distill_failures_immutable ON experience_distill_failures;
CREATE TRIGGER experience_distill_failures_immutable BEFORE UPDATE OR DELETE ON experience_distill_failures
  FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS experience_distill_inputs_immutable ON experience_distill_inputs;
CREATE TRIGGER experience_distill_inputs_immutable BEFORE UPDATE OR DELETE ON experience_distill_inputs
  FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS experience_distill_results_immutable ON experience_distill_results;
CREATE TRIGGER experience_distill_results_immutable BEFORE UPDATE OR DELETE ON experience_distill_results
  FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
`

// The function is installed with a fixed, schema-qualified search path. Core
// may lock one live result but does not receive direct job write permissions.
const experienceDistillLockSQL = `
CREATE OR REPLACE FUNCTION {{SCHEMA}}.experience_distill_lock_result(
  p_job_id text, p_generation bigint, p_input_sha256 text, p_result_sha256 text
) RETURNS TABLE (tenant_id text, org_id text, case_id text, actor_id text, feedback_id text,
                 input_sha256 text, input_bytes bytea, result_sha256 text, result_bytes bytea,
                 model_ref text, template_ref text)
LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $function$
  SELECT i.tenant_id,i.org_id,i.case_id,i.actor_id,i.feedback_id,i.input_sha256,i.input_bytes,
         r.result_sha256,r.result_bytes,r.model_ref,r.template_ref
  FROM {{SCHEMA}}.worker_jobs j
  JOIN {{SCHEMA}}.experience_distill_inputs i ON i.job_id=j.id
  JOIN {{SCHEMA}}.experience_distill_results r ON r.job_id=j.id AND r.generation=j.generation
  WHERE j.id=p_job_id AND j.kind='experience_distill' AND j.generation=p_generation
    AND j.state='running' AND j.worker_id<>'' AND j.lease_until>clock_timestamp()
    AND j.tenant_id=current_setting('app.tenant_id',true) AND i.actor_id=current_setting('app.actor_id',true)
    AND i.tenant_id=j.tenant_id AND i.org_id=j.org_id AND i.case_id=j.case_id AND i.actor_id=j.actor_id
    AND r.tenant_id=i.tenant_id AND r.org_id=i.org_id AND r.case_id=i.case_id
    AND i.input_sha256=p_input_sha256 AND r.input_sha256=i.input_sha256 AND r.result_sha256=p_result_sha256
  FOR UPDATE OF j
$function$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.experience_distill_lock_result(text,bigint,text,text) FROM PUBLIC;
`

// Admission is the sole Core-to-Execution write boundary. It checks the
// transaction's tenant and actor, locks the same bounded queue as reviews and
// atomically records one input, job and notification.
const experienceDistillEnqueueSQL = `
CREATE OR REPLACE FUNCTION {{SCHEMA}}.experience_distill_enqueue(
  p_job text,p_outbox text,p_tenant text,p_org text,p_case text,p_actor text,p_feedback text,
  p_key text,p_request_sha text,p_input_sha text,p_input bytea,
  p_policy_hash text,p_policy_json text,p_global_pending integer,p_tenant_pending integer,
  p_max_claims integer,p_max_age integer
) RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $function$
DECLARE
  v_prior record;
  v_payload jsonb;
  v_policy text;
  v_global integer;
  v_tenant integer;
  v_role text := COALESCE(current_setting('app.actor_role',true),'');
  v_now text := clock_timestamp()::text;
BEGIN
  IF COALESCE(p_job,'')='' OR COALESCE(p_outbox,'')='' OR COALESCE(p_key,'')='' OR length(p_key)>200
     OR COALESCE(p_request_sha,'') !~ '^[0-9a-f]{64}$' OR COALESCE(p_input_sha,'') !~ '^[0-9a-f]{64}$'
     OR p_input IS NULL OR encode(sha256(p_input),'hex') IS DISTINCT FROM p_input_sha
     OR p_tenant IS DISTINCT FROM current_setting('app.tenant_id',true)
     OR p_actor IS DISTINCT FROM current_setting('app.actor_id',true)
     OR COALESCE(p_tenant,'')='' OR COALESCE(p_org,'')='' OR COALESCE(p_case,'')='' OR COALESCE(p_actor,'')=''
     OR COALESCE(p_feedback,'')='' OR COALESCE(p_policy_hash,'')='' OR COALESCE(p_policy_json,'')=''
     OR COALESCE(p_global_pending,0)<1 OR COALESCE(p_tenant_pending,0)<1
     OR COALESCE(p_max_claims,0)<1 OR COALESCE(p_max_age,0)<1 THEN
    RAISE EXCEPTION 'invalid experience task admission' USING ERRCODE='22023';
  END IF;
  v_payload := convert_from(p_input,'UTF8')::jsonb;
  IF v_payload->>'schema_version' IS DISTINCT FROM 'experience-distill-input/v1' OR v_payload->>'tenant_id' IS DISTINCT FROM p_tenant
     OR v_payload->>'org_id' IS DISTINCT FROM p_org OR v_payload->>'case_id' IS DISTINCT FROM p_case
     OR v_payload->>'feedback_id' IS DISTINCT FROM p_feedback OR v_payload->>'applicant_id' IS DISTINCT FROM p_actor
     OR COALESCE(v_payload->>'candidate_id','')='' THEN
    RAISE EXCEPTION 'experience task input identity mismatch' USING ERRCODE='22023';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM {{SCHEMA}}.experience_feedback f WHERE f.tenant_id=p_tenant AND f.org_id=p_org
      AND f.case_id=p_case AND f.feedback_id=p_feedback AND f.applicant_id=p_actor
      AND f.decision_id=v_payload->>'decision_id') THEN
    RAISE EXCEPTION 'experience feedback is unavailable' USING ERRCODE='42501';
  END IF;
  SELECT i.job_id,i.request_sha256 INTO v_prior FROM {{SCHEMA}}.experience_distill_inputs i
    WHERE i.tenant_id=p_tenant AND i.actor_id=p_actor AND i.idempotency_key=p_key;
  IF FOUND THEN
    IF v_prior.request_sha256<>p_request_sha THEN
      RAISE EXCEPTION 'idempotency key already used' USING ERRCODE='23505';
    END IF;
    RETURN v_prior.job_id;
  END IF;
  PERFORM set_config('app.actor_role','worker',true);
  PERFORM pg_advisory_xact_lock(hashtextextended('{{QUEUE_LOCK_KEY}}',0));
  SELECT i.job_id,i.request_sha256 INTO v_prior FROM {{SCHEMA}}.experience_distill_inputs i
    WHERE i.tenant_id=p_tenant AND i.actor_id=p_actor AND i.idempotency_key=p_key;
  IF FOUND THEN
    PERFORM set_config('app.actor_role',v_role,true);
    IF v_prior.request_sha256<>p_request_sha THEN
      RAISE EXCEPTION 'idempotency key already used' USING ERRCODE='23505';
    END IF;
    RETURN v_prior.job_id;
  END IF;
  INSERT INTO {{SCHEMA}}.execution_queue_policies(policy_hash,policy_json) VALUES(p_policy_hash,p_policy_json) ON CONFLICT DO NOTHING;
  INSERT INTO {{SCHEMA}}.execution_queue_cursor(tenant_id,policy_hash) VALUES('__scheduler__',p_policy_hash) ON CONFLICT DO NOTHING;
  SELECT policy_hash INTO v_policy FROM {{SCHEMA}}.execution_queue_cursor WHERE tenant_id='__scheduler__' FOR UPDATE;
  IF v_policy<>p_policy_hash THEN
    IF EXISTS (SELECT 1 FROM {{SCHEMA}}.worker_jobs WHERE state IN ('queued','running')) THEN
      RAISE EXCEPTION 'execution queue policy mismatch' USING ERRCODE='55000';
    END IF;
    UPDATE {{SCHEMA}}.execution_queue_cursor SET policy_hash=p_policy_hash,last_tenant='' WHERE tenant_id='__scheduler__';
    UPDATE {{SCHEMA}}.execution_queue_tenants SET deficit=0;
  END IF;
  SELECT count(*),count(*) FILTER (WHERE tenant_id=p_tenant) INTO v_global,v_tenant
    FROM {{SCHEMA}}.worker_jobs WHERE state IN ('queued','running');
  IF v_global>=p_global_pending OR v_tenant>=p_tenant_pending THEN
    RAISE EXCEPTION 'execution queue capacity exceeded' USING ERRCODE='53300';
  END IF;
  INSERT INTO {{SCHEMA}}.execution_queue_tenants(tenant_id) VALUES(p_tenant) ON CONFLICT DO NOTHING;
  INSERT INTO {{SCHEMA}}.worker_jobs(id,tenant_id,org_id,case_id,run_id,attempt_id,actor_id,kind,state,created_at,updated_at,
      policy_hash,max_claims,deadline_at)
    VALUES(p_job,p_tenant,p_org,p_case,NULL,NULL,p_actor,'experience_distill','queued',v_now,v_now,
      p_policy_hash,p_max_claims,clock_timestamp()+p_max_age*interval '1 second');
  INSERT INTO {{SCHEMA}}.experience_distill_inputs(job_id,tenant_id,org_id,case_id,actor_id,feedback_id,
      idempotency_key,request_sha256,input_sha256,input_bytes)
    VALUES(p_job,p_tenant,p_org,p_case,p_actor,p_feedback,p_key,p_request_sha,p_input_sha,p_input);
  INSERT INTO {{SCHEMA}}.outbox_events(id,job_id,tenant_id,org_id,case_id,event_type,payload,state,created_at)
    VALUES(p_outbox,p_job,p_tenant,p_org,p_case,'experience_distill.accepted',
      jsonb_build_object('job_id',p_job,'tenant_id',p_tenant,'case_id',p_case)::text,'pending',v_now);
  PERFORM set_config('app.actor_role',v_role,true);
  RETURN p_job;
END
$function$;
REVOKE ALL ON FUNCTION {{SCHEMA}}.experience_distill_enqueue(text,text,text,text,text,text,text,text,text,text,bytea,text,text,integer,integer,integer,integer) FROM PUBLIC;
`

func migrateExperienceDistillFunctions(ctx context.Context, pool *pgxpool.Pool) error {
	var schema string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return err
	}
	qualified := pgx.Identifier{schema}.Sanitize()
	statements := strings.ReplaceAll(experienceDistillLockSQL+experienceDistillEnqueueSQL, "{{SCHEMA}}", qualified)
	key := strings.ReplaceAll(schema+":execution-queue", "'", "''")
	statements = strings.ReplaceAll(statements, "{{QUEUE_LOCK_KEY}}", key)
	_, err := pool.Exec(ctx, statements)
	return err
}
