package store

// OperatorAuditSchemaSQL persists the operator-audit extension. All rows are
// tenant scoped and append-only: a new binding, snapshot set, evaluation or
// impact plan is a new row, never an overwrite. The manifests themselves are
// embedded in the binary; the database stores the human-confirmed mappings and
// the immutable evidence/results those manifests produced.
const OperatorAuditSchemaSQL = `
CREATE UNIQUE INDEX IF NOT EXISTS operator_binding_case_scope ON cases (tenant_id,org_id,id);

CREATE TABLE IF NOT EXISTS operator_object_bindings (
 tenant_id text NOT NULL DEFAULT COALESCE(NULLIF(current_setting('app.tenant_id', true), ''), 'local-demo'),
 org_id text NOT NULL DEFAULT COALESCE(NULLIF(current_setting('app.org_id', true), ''), 'demo-org'),
 case_id text NOT NULL,
 binding_id text NOT NULL,
 binding_version text NOT NULL,
 status text NOT NULL CHECK (status IN ('confirmed','missing','ambiguous')),
 contract_object jsonb NOT NULL,
 external_object jsonb NOT NULL,
 connector_id text NOT NULL,
 external_object_id text NOT NULL,
 source_system text NOT NULL,
 confirmed_by text NOT NULL,
 confirmed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 effective_from timestamptz NOT NULL,
 effective_until timestamptz NOT NULL,
 actor_id text NOT NULL,
 idempotency_key text NOT NULL,
 request_sha256 text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,binding_id),
 UNIQUE (tenant_id,org_id,case_id,binding_version),
 UNIQUE (tenant_id,org_id,case_id,actor_id,idempotency_key),
 CHECK (effective_from < effective_until),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases (tenant_id,org_id,id)
);
CREATE INDEX IF NOT EXISTS operator_object_bindings_case ON operator_object_bindings (tenant_id,org_id,case_id,created_at);

CREATE TABLE IF NOT EXISTS operator_external_snapshot_sets (
 tenant_id text NOT NULL DEFAULT COALESCE(NULLIF(current_setting('app.tenant_id', true), ''), 'local-demo'),
 org_id text NOT NULL DEFAULT COALESCE(NULLIF(current_setting('app.org_id', true), ''), 'demo-org'),
 case_id text NOT NULL,
 snapshot_set_id text NOT NULL,
 binding_version text NOT NULL,
 connector_id text NOT NULL,
 external_object_id text NOT NULL,
 source_system text NOT NULL,
 business_at timestamptz NOT NULL,
 status text NOT NULL,
 reason_code text NOT NULL,
 collected_by text NOT NULL,
 collected_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 observation_count integer NOT NULL CHECK (observation_count >= 0),
 set_sha256 text NOT NULL,
 actor_id text NOT NULL,
 idempotency_key text NOT NULL,
 request_sha256 text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,snapshot_set_id),
 UNIQUE (tenant_id,org_id,case_id,actor_id,idempotency_key),
 FOREIGN KEY (tenant_id,org_id,case_id,binding_version) REFERENCES operator_object_bindings (tenant_id,org_id,case_id,binding_version)
);
CREATE INDEX IF NOT EXISTS operator_snapshot_sets_case ON operator_external_snapshot_sets (tenant_id,org_id,case_id,created_at);

CREATE TABLE IF NOT EXISTS operator_external_observations (
 tenant_id text NOT NULL DEFAULT COALESCE(NULLIF(current_setting('app.tenant_id', true), ''), 'local-demo'),
 org_id text NOT NULL DEFAULT COALESCE(NULLIF(current_setting('app.org_id', true), ''), 'demo-org'),
 case_id text NOT NULL,
 snapshot_set_id text NOT NULL,
 snapshot_id text NOT NULL,
 record_id text NOT NULL,
 record_version text,
 observed_at timestamptz NOT NULL,
 source_updated_at timestamptz,
 effective_from timestamptz NOT NULL,
 effective_until timestamptz NOT NULL,
 availability text NOT NULL CHECK (availability IN ('available','unavailable','stale','rejected')),
 reason_code text NOT NULL,
 payload_ref text,
 fields jsonb NOT NULL,
 data_class text NOT NULL CHECK (data_class = 'synthetic'),
 observation_json jsonb NOT NULL,
 observation_sha256 text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,snapshot_set_id,snapshot_id),
 FOREIGN KEY (tenant_id,org_id,case_id,snapshot_set_id) REFERENCES operator_external_snapshot_sets (tenant_id,org_id,case_id,snapshot_set_id)
);
CREATE INDEX IF NOT EXISTS operator_observations_set ON operator_external_observations (tenant_id,org_id,case_id,snapshot_set_id);

CREATE TABLE IF NOT EXISTS operator_fulfillment_evaluations (
 tenant_id text NOT NULL DEFAULT COALESCE(NULLIF(current_setting('app.tenant_id', true), ''), 'local-demo'),
 org_id text NOT NULL DEFAULT COALESCE(NULLIF(current_setting('app.org_id', true), ''), 'demo-org'),
 case_id text NOT NULL,
 evaluation_id text NOT NULL,
 pack_id text NOT NULL,
 pack_version text NOT NULL,
 source_run_id text NOT NULL,
 source_attempt_id text NOT NULL,
 contract_version_id text NOT NULL,
 rule_pack_version text NOT NULL,
 binding_version text NOT NULL,
 snapshot_set_id text NOT NULL,
 business_at timestamptz NOT NULL,
 input_json jsonb NOT NULL,
 input_sha256 text NOT NULL,
 result_json jsonb NOT NULL,
 result_sha256 text NOT NULL,
 machine_status text NOT NULL CHECK (machine_status IN ('completed','partial','failed','cancelled')),
 created_by text NOT NULL,
 actor_id text NOT NULL,
 idempotency_key text NOT NULL,
 request_sha256 text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,evaluation_id),
 UNIQUE (tenant_id,org_id,case_id,actor_id,idempotency_key),
 FOREIGN KEY (tenant_id,org_id,case_id,source_run_id) REFERENCES review_runs (tenant_id,org_id,case_id,id),
 FOREIGN KEY (tenant_id,org_id,source_run_id,source_attempt_id) REFERENCES review_attempts (tenant_id,org_id,run_id,id),
 FOREIGN KEY (tenant_id,org_id,case_id,binding_version) REFERENCES operator_object_bindings (tenant_id,org_id,case_id,binding_version),
 FOREIGN KEY (tenant_id,org_id,case_id,snapshot_set_id) REFERENCES operator_external_snapshot_sets (tenant_id,org_id,case_id,snapshot_set_id)
);
CREATE INDEX IF NOT EXISTS operator_evaluations_case ON operator_fulfillment_evaluations (tenant_id,org_id,case_id,created_at);

CREATE TABLE IF NOT EXISTS operator_fulfillment_checks (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 case_id text NOT NULL,
 evaluation_id text NOT NULL,
 check_id text NOT NULL,
 relation text NOT NULL CHECK (relation IN ('compatible','conflict','insufficient','not_applicable')),
 reason_code text NOT NULL,
 machine_status text NOT NULL CHECK (machine_status IN ('completed','partial','failed','cancelled')),
 requires_human_review boolean NOT NULL,
 evidence jsonb NOT NULL,
 limitations jsonb NOT NULL,
 dependencies jsonb NOT NULL,
 result_json jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,evaluation_id,check_id),
 FOREIGN KEY (tenant_id,org_id,case_id,evaluation_id) REFERENCES operator_fulfillment_evaluations (tenant_id,org_id,case_id,evaluation_id)
);

CREATE TABLE IF NOT EXISTS operator_impact_plans (
 tenant_id text NOT NULL DEFAULT COALESCE(NULLIF(current_setting('app.tenant_id', true), ''), 'local-demo'),
 org_id text NOT NULL DEFAULT COALESCE(NULLIF(current_setting('app.org_id', true), ''), 'demo-org'),
 case_id text NOT NULL,
 base_evaluation_id text NOT NULL,
 target_evaluation_id text NOT NULL,
 mode text NOT NULL CHECK (mode IN ('explain','shadow')),
 plan_json jsonb NOT NULL,
 plan_sha256 text NOT NULL,
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,base_evaluation_id,target_evaluation_id,mode),
 FOREIGN KEY (tenant_id,org_id,case_id,target_evaluation_id) REFERENCES operator_fulfillment_evaluations (tenant_id,org_id,case_id,evaluation_id)
);
CREATE INDEX IF NOT EXISTS operator_impact_plans_target ON operator_impact_plans (tenant_id,org_id,case_id,target_evaluation_id);

DROP TRIGGER IF EXISTS operator_object_bindings_append_only ON operator_object_bindings;
CREATE TRIGGER operator_object_bindings_append_only BEFORE UPDATE OR DELETE ON operator_object_bindings FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS operator_snapshot_sets_append_only ON operator_external_snapshot_sets;
CREATE TRIGGER operator_snapshot_sets_append_only BEFORE UPDATE OR DELETE ON operator_external_snapshot_sets FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS operator_observations_append_only ON operator_external_observations;
CREATE TRIGGER operator_observations_append_only BEFORE UPDATE OR DELETE ON operator_external_observations FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS operator_evaluations_append_only ON operator_fulfillment_evaluations;
CREATE TRIGGER operator_evaluations_append_only BEFORE UPDATE OR DELETE ON operator_fulfillment_evaluations FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS operator_checks_append_only ON operator_fulfillment_checks;
CREATE TRIGGER operator_checks_append_only BEFORE UPDATE OR DELETE ON operator_fulfillment_checks FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS operator_impact_plans_append_only ON operator_impact_plans;
CREATE TRIGGER operator_impact_plans_append_only BEFORE UPDATE OR DELETE ON operator_impact_plans FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
`
