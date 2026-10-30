package store

var schemaStatements = []string{
	`CREATE SEQUENCE IF NOT EXISTS parsed_selection_order_seq`,
	`CREATE TABLE IF NOT EXISTS sessions (
		seq bigserial PRIMARY KEY,
		token text NOT NULL UNIQUE,
		actor_id text NOT NULL,
		expires_at text NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS cases (
		seq bigserial PRIMARY KEY,
		id text NOT NULL UNIQUE,
		title text NOT NULL,
		contract_type text NOT NULL,
		owner_id text NOT NULL,
		member_ids text NOT NULL,
		created_at text NOT NULL,
		updated_at text NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS documents (
		seq bigserial PRIMARY KEY,
		id text NOT NULL UNIQUE,
		case_id text NOT NULL,
		role text NOT NULL,
		filename text NOT NULL,
		media_type text NOT NULL,
		digest text NOT NULL,
		size bigint NOT NULL,
		origin text NOT NULL,
		storage_backend text NOT NULL DEFAULT 'local' CHECK (storage_backend IN ('local','remote')),
		created_at text NOT NULL
	)`,
	`ALTER TABLE documents ADD COLUMN IF NOT EXISTS storage_backend text NOT NULL DEFAULT 'local'`,
	`CREATE TABLE IF NOT EXISTS parsed_documents (
		seq bigserial PRIMARY KEY,
		selection_order bigint NOT NULL DEFAULT nextval('parsed_selection_order_seq'),
		id text NOT NULL UNIQUE,
		document_id text NOT NULL,
		original_digest text NOT NULL,
		parser_id text NOT NULL,
		parser_version text NOT NULL,
		config_hash text NOT NULL,
		normalization_version text NOT NULL,
		normalized_digest text,
		ir_digest text,
		artifact_digest text NOT NULL,
		created_at text NOT NULL,
		status text NOT NULL CHECK (status IN ('ready','failed')),
		payload text NOT NULL
	)`,
	`ALTER TABLE parsed_documents ADD COLUMN IF NOT EXISTS selection_order bigint`,
	`UPDATE parsed_documents SET selection_order = seq WHERE selection_order IS NULL`,
	`ALTER TABLE parsed_documents ALTER COLUMN selection_order SET DEFAULT nextval('parsed_selection_order_seq')`,
	`ALTER TABLE parsed_documents ALTER COLUMN selection_order SET NOT NULL`,
	`SELECT setval('parsed_selection_order_seq', GREATEST((SELECT last_value FROM parsed_selection_order_seq), COALESCE((SELECT MAX(selection_order) FROM parsed_documents), 1)), true)`,
	`CREATE INDEX IF NOT EXISTS parsed_documents_selection ON parsed_documents (document_id, config_hash, selection_order)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS parsed_documents_selection_order_unique ON parsed_documents (selection_order)`,
	`CREATE TABLE IF NOT EXISTS review_runs (
		seq bigserial PRIMARY KEY,
		id text NOT NULL UNIQUE,
		case_id text NOT NULL,
		state text NOT NULL DEFAULT '',
		model_ref text NOT NULL DEFAULT '',
		partial_reason text NOT NULL DEFAULT '',
		failure_message text NOT NULL DEFAULT '',
		created_at text NOT NULL,
		updated_at text NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS review_attempts (
		id text PRIMARY KEY,
		run_id text NOT NULL REFERENCES review_runs(id),
		ordinal integer NOT NULL,
		retry_of_attempt_id text,
		state text NOT NULL,
		accepted_at text NOT NULL,
		started_at text,
		finished_at text,
		input_hash text,
		output_hash text,
		error_code text,
		history_kind text NOT NULL,
		rule_pack_snapshot bytea,
		rule_pack_hash text NOT NULL DEFAULT '',
		rule_pack_snapshot_digest text NOT NULL DEFAULT '',
		rule_pack_compiler_ref text NOT NULL DEFAULT '',
		UNIQUE (run_id, ordinal)
	)`,
	`ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS rule_pack_snapshot bytea`,
	`ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS rule_pack_hash text NOT NULL DEFAULT ''`,
	`ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS rule_pack_snapshot_digest text NOT NULL DEFAULT ''`,
	`ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS rule_pack_compiler_ref text NOT NULL DEFAULT ''`,
	`ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS actor_id text NOT NULL DEFAULT ''`,
	`CREATE TABLE IF NOT EXISTS review_attempt_parse_selections (
		seq bigserial PRIMARY KEY,
		attempt_id text NOT NULL REFERENCES review_attempts(id),
		run_id text NOT NULL,
		document_id text NOT NULL,
		original_digest text NOT NULL,
		parsed_document_id text NOT NULL,
		selection_order bigint NOT NULL,
		parser_id text NOT NULL,
		parser_version text NOT NULL,
		config_hash text NOT NULL,
		normalization_version text NOT NULL,
		artifact_digest text NOT NULL DEFAULT '',
		UNIQUE (attempt_id, document_id)
	)`,
	`CREATE TABLE IF NOT EXISTS review_attempt_inputs (
		attempt_id text PRIMARY KEY REFERENCES review_attempts(id),
		run_id text NOT NULL REFERENCES review_runs(id),
		input_hash text NOT NULL,
		frozen_at text NOT NULL,
		documents text NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS execution_events (
		attempt_id text NOT NULL REFERENCES review_attempts(id),
		event_seq integer NOT NULL,
		span_id text,
		parent_span_id text,
		node_kind text NOT NULL,
		phase text NOT NULL,
		event_type text NOT NULL,
		occurred_at text NOT NULL,
		duration_ms bigint,
		status text NOT NULL,
		error_code text,
		model_ref text,
		usage text,
		PRIMARY KEY (attempt_id, event_seq)
	)`,
	`ALTER TABLE execution_events ADD COLUMN IF NOT EXISTS recorded_at text`,
	`ALTER TABLE execution_events ADD COLUMN IF NOT EXISTS event_key text`,
	`ALTER TABLE execution_events ADD COLUMN IF NOT EXISTS generation bigint`,
	// Bounded physical model request/response record. This is diagnostic
	// observability only; it is never read as evidence for a finding.
	`ALTER TABLE execution_events ADD COLUMN IF NOT EXISTS trace jsonb`,
	`CREATE UNIQUE INDEX IF NOT EXISTS execution_events_logical_identity ON execution_events (attempt_id, generation, event_key) WHERE event_key IS NOT NULL`,
	`CREATE TABLE IF NOT EXISTS findings (
		seq bigserial PRIMARY KEY,
		id text NOT NULL UNIQUE,
		run_id text NOT NULL,
		case_id text NOT NULL,
		origin text NOT NULL,
		domain text NOT NULL,
		rule_id text NOT NULL DEFAULT '',
		capability_id text NOT NULL DEFAULT '',
		object_ref text NOT NULL DEFAULT '',
		claim text NOT NULL,
		relation text NOT NULL,
		disposition_suggestion text NOT NULL DEFAULT '',
		unverified_reason text NOT NULL DEFAULT '',
		version integer NOT NULL,
		severity text,
		severity_source jsonb,
		anchors text NOT NULL,
		created_at text NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS constraint_evaluations (
		seq bigserial PRIMARY KEY,
		id text NOT NULL UNIQUE,
		run_id text NOT NULL,
		case_id text NOT NULL,
		domain text NOT NULL,
		relation text NOT NULL,
		rule_version text NOT NULL,
		reason text NOT NULL,
		finding_id text NOT NULL DEFAULT '',
		rule_id text NOT NULL DEFAULT '',
		capability_id text NOT NULL DEFAULT '',
		reason_code text NOT NULL DEFAULT '',
		fact_ids text NOT NULL DEFAULT '[]',
		object_refs text NOT NULL DEFAULT '[]'
	)`,
	`ALTER TABLE findings ADD COLUMN IF NOT EXISTS attempt_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE findings ADD COLUMN IF NOT EXISTS rule_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE findings ADD COLUMN IF NOT EXISTS capability_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE findings ADD COLUMN IF NOT EXISTS object_ref text NOT NULL DEFAULT ''`,
	`ALTER TABLE findings ADD COLUMN IF NOT EXISTS severity text`,
	`ALTER TABLE findings ADD COLUMN IF NOT EXISTS severity_source jsonb`,
	`ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS attempt_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS rule_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS capability_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS reason_code text NOT NULL DEFAULT ''`,
	`ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS fact_ids text NOT NULL DEFAULT '[]'`,
	`ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS object_refs text NOT NULL DEFAULT '[]'`,
	`ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS mandatory boolean NOT NULL DEFAULT false`,
	`CREATE TABLE IF NOT EXISTS decisions (
		seq bigserial PRIMARY KEY,
		id text NOT NULL UNIQUE,
		finding_id text NOT NULL,
		run_id text NOT NULL,
		case_id text NOT NULL,
		actor_id text NOT NULL,
		actor_name text NOT NULL,
		action text NOT NULL,
		reason text NOT NULL,
		old_disposition text NOT NULL,
		new_disposition text NOT NULL,
		created_at text NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS run_events (
		seq bigserial PRIMARY KEY,
		run_id text NOT NULL,
		event_seq integer NOT NULL,
		event_type text NOT NULL,
		state text NOT NULL
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS run_events_identity ON run_events (run_id, event_seq)`,
	`CREATE TABLE IF NOT EXISTS activity (
		seq bigserial PRIMARY KEY,
		id text NOT NULL UNIQUE,
		case_id text NOT NULL,
		occurred_at text NOT NULL,
		summary text NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS idempotency (
		seq bigserial PRIMARY KEY,
		actor_id text NOT NULL,
		idem_key text NOT NULL,
		finding_id text NOT NULL,
		decision_id text NOT NULL,
		action text NOT NULL,
		reason text NOT NULL,
		expected_version integer NOT NULL,
		UNIQUE (actor_id, idem_key)
	)`,
	`CREATE TABLE IF NOT EXISTS review_run_snapshots (
		run_id text PRIMARY KEY,
		document_ids text NOT NULL,
		rule_set_hash text NOT NULL,
		playbook_hash text NOT NULL,
		model_ref text NOT NULL DEFAULT '',
		comparator_ref text NOT NULL
	)`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS model_configuration_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS parsed_document_ids text NOT NULL DEFAULT '[]'`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS parse_selection_hash text NOT NULL DEFAULT ''`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS parser_configuration_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS party_context bytea`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS party_context_hash text NOT NULL DEFAULT ''`,
	`CREATE TABLE IF NOT EXISTS audit_events (
		event_id text PRIMARY KEY,
		run_id text NOT NULL,
		actor text NOT NULL,
		seq integer NOT NULL,
		event_type text NOT NULL,
		prev_hash text NOT NULL DEFAULT '',
		hash text NOT NULL,
		correlation_ids text NOT NULL,
		UNIQUE (run_id, seq)
	)`,
	`CREATE OR REPLACE FUNCTION forbid_append_only() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'append-only';
		END;
		$$`,
	`DROP TRIGGER IF EXISTS review_attempt_inputs_append_only ON review_attempt_inputs`,
	`CREATE TRIGGER review_attempt_inputs_append_only
		BEFORE UPDATE OR DELETE ON review_attempt_inputs
		FOR EACH ROW EXECUTE FUNCTION forbid_append_only()`,
	`DROP TRIGGER IF EXISTS review_attempt_parse_selections_append_only ON review_attempt_parse_selections`,
	`CREATE TRIGGER review_attempt_parse_selections_append_only
		BEFORE UPDATE OR DELETE ON review_attempt_parse_selections
		FOR EACH ROW EXECUTE FUNCTION forbid_append_only()`,
	`DROP TRIGGER IF EXISTS execution_events_append_only ON execution_events`,
	`CREATE TRIGGER execution_events_append_only
		BEFORE UPDATE OR DELETE ON execution_events
		FOR EACH ROW EXECUTE FUNCTION forbid_append_only()`,
	`DROP TRIGGER IF EXISTS review_run_snapshots_append_only ON review_run_snapshots`,
	`CREATE TRIGGER review_run_snapshots_append_only
		BEFORE UPDATE OR DELETE ON review_run_snapshots
		FOR EACH ROW EXECUTE FUNCTION forbid_append_only()`,
	`DROP TRIGGER IF EXISTS audit_events_append_only ON audit_events`,
	`CREATE TRIGGER audit_events_append_only
		BEFORE UPDATE OR DELETE ON audit_events
		FOR EACH ROW EXECUTE FUNCTION forbid_append_only()`,
	`DROP TRIGGER IF EXISTS parsed_documents_append_only ON parsed_documents`,
	`CREATE TRIGGER parsed_documents_append_only
		BEFORE UPDATE OR DELETE ON parsed_documents
		FOR EACH ROW EXECUTE FUNCTION forbid_append_only()`,
	`UPDATE review_attempts a SET history_kind='legacy_unfrozen'
		WHERE a.history_kind NOT IN ('legacy_unfrozen','preparing')
		AND NOT EXISTS (
			SELECT 1 FROM review_attempt_parse_selections s WHERE s.attempt_id=a.id
		)
		AND NOT EXISTS (
			SELECT 1 FROM review_run_snapshots r WHERE r.run_id=a.run_id
			AND (COALESCE(r.parsed_document_ids,'[]')::jsonb NOT IN ('[]'::jsonb,'null'::jsonb)
				OR COALESCE(r.parse_selection_hash,'') <> ''
				OR COALESCE(r.parser_configuration_id,'') <> '')
		)
		AND NOT EXISTS (
			SELECT 1 FROM review_attempt_inputs i
			WHERE i.attempt_id=a.id
			AND CASE WHEN jsonb_typeof(i.documents::jsonb) = 'array' THEN jsonb_array_length(i.documents::jsonb) > 0 ELSE false END
			AND NOT EXISTS (
				SELECT 1 FROM jsonb_array_elements(i.documents::jsonb) AS d(value)
				WHERE COALESCE(d.value->>'parsed_document_id','') = ''
				OR COALESCE(d.value->>'parsed_artifact_digest','') = ''
				OR COALESCE(d.value->>'config_hash','') = ''
				OR COALESCE(d.value->>'parser_id','') = ''
				OR COALESCE(d.value->>'parser_version','') = ''
				OR COALESCE(d.value->>'normalization_version','') = ''
				OR COALESCE(d.value->>'parsed_status','') NOT IN ('ready','failed')
				OR COALESCE(d.value->>'selection_order','') !~ '^[1-9][0-9]*$'
			)
		)`,
	`CREATE TABLE IF NOT EXISTS worker_jobs (
		id text PRIMARY KEY,
		tenant_id text NOT NULL DEFAULT 'local-demo',
		org_id text NOT NULL DEFAULT 'local-demo',
		case_id text NOT NULL,
		run_id text NOT NULL,
		attempt_id text NOT NULL UNIQUE,
		actor_id text NOT NULL,
		kind text NOT NULL,
		state text NOT NULL CHECK (state IN ('queued','running','completed','partial','failed','cancelled')),
		generation bigint NOT NULL DEFAULT 0,
		worker_id text NOT NULL DEFAULT '',
		lease_until timestamptz,
		heartbeat_at timestamptz,
		created_at text NOT NULL,
		updated_at text NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS worker_jobs_claim ON worker_jobs (state, lease_until, created_at, id)`,
	`CREATE TABLE IF NOT EXISTS outbox_events (
		id text PRIMARY KEY,
		job_id text NOT NULL REFERENCES worker_jobs(id),
		tenant_id text NOT NULL DEFAULT 'local-demo',
		org_id text NOT NULL DEFAULT 'local-demo',
		case_id text NOT NULL,
		event_type text NOT NULL,
		payload text NOT NULL,
		state text NOT NULL CHECK (state IN ('pending','delivered')),
		created_at text NOT NULL,
		delivered_at text,
		UNIQUE (job_id, event_type)
	)`,
	`CREATE TABLE IF NOT EXISTS tool_call_ledger (
		id text PRIMARY KEY,
		tenant_id text NOT NULL,
		org_id text NOT NULL,
		case_id text NOT NULL,
		actor_id text NOT NULL,
		run_id text NOT NULL,
		attempt_id text NOT NULL,
		capability_id text NOT NULL CHECK (capability_id IN ('dedicated_line.minimum_bandwidth','dedicated_line.monthly_availability','payment.advance_ratio.ceiling')),
		tool_version text NOT NULL CHECK (tool_version = 'frozen-evidence/v1'),
		deadline timestamptz NOT NULL,
		state text NOT NULL CHECK (state IN ('reserved','finished')),
		status text NOT NULL CHECK (status IN ('reserved','completed','failed','cancelled')),
		output_bytes bigint NOT NULL DEFAULT 0 CHECK (output_bytes >= 0),
		reserved_at timestamptz NOT NULL DEFAULT clock_timestamp(),
		finished_at timestamptz,
		CHECK ((state='reserved' AND status='reserved' AND finished_at IS NULL) OR (state='finished' AND status IN ('completed','failed','cancelled') AND finished_at IS NOT NULL)),
		CHECK (deadline > reserved_at AND deadline <= reserved_at + interval '60 seconds')
	)`,
	`CREATE INDEX IF NOT EXISTS tool_call_ledger_tenant_window ON tool_call_ledger (tenant_id, reserved_at)`,
	`CREATE INDEX IF NOT EXISTS tool_call_ledger_attempt_budget ON tool_call_ledger (tenant_id, org_id, case_id, run_id, attempt_id)`,
	`DO $$
	DECLARE table_name text;
	BEGIN
		FOREACH table_name IN ARRAY ARRAY[
			'sessions','cases','documents','parsed_documents','review_runs','review_attempts',
			'review_attempt_parse_selections','review_attempt_inputs','execution_events','findings',
			'constraint_evaluations','decisions','run_events','activity','idempotency',
			'review_run_snapshots','audit_events','worker_jobs','outbox_events','tool_call_ledger'
		] LOOP
			EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT COALESCE(NULLIF(current_setting(''app.tenant_id'', true), ''''), ''local-demo'')', table_name);
			EXECUTE format('ALTER TABLE %I ALTER COLUMN tenant_id SET DEFAULT COALESCE(NULLIF(current_setting(''app.tenant_id'', true), ''''), ''local-demo'')', table_name);
			EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS org_id text NOT NULL DEFAULT COALESCE(NULLIF(current_setting(''app.org_id'', true), ''''), ''demo-org'')', table_name);
			EXECUTE format('ALTER TABLE %I ALTER COLUMN org_id SET DEFAULT COALESCE(NULLIF(current_setting(''app.org_id'', true), ''''), ''demo-org'')', table_name);
		END LOOP;
	END $$`,
	`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS issuer text NOT NULL DEFAULT ''`,
	`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS subject text NOT NULL DEFAULT ''`,
	`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS audience text NOT NULL DEFAULT 'client'`,
	`ALTER TABLE cases ADD COLUMN IF NOT EXISTS business_line text NOT NULL DEFAULT 'unspecified'`,
	`ALTER TABLE cases ADD COLUMN IF NOT EXISTS entity_version bigint NOT NULL DEFAULT 1`,
	`ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS entity_version bigint NOT NULL DEFAULT 1`,
	`ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0`,
	`ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS claim_owner text NOT NULL DEFAULT ''`,
	`ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS claim_until timestamptz`,
	`ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS last_error text NOT NULL DEFAULT ''`,
	`ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT to_timestamp(0)`,
	`CREATE INDEX IF NOT EXISTS outbox_events_pending_scan ON outbox_events (next_attempt_at, created_at, id) WHERE state='pending'`,
	`ALTER TABLE cases ADD COLUMN IF NOT EXISTS scenario text NOT NULL DEFAULT 'unspecified'`,
	`ALTER TABLE cases ADD COLUMN IF NOT EXISTS business_status text NOT NULL DEFAULT 'active'`,
	`ALTER TABLE cases ADD COLUMN IF NOT EXISTS archived_at text`,
	`CREATE UNIQUE INDEX IF NOT EXISTS sessions_token_identity ON sessions (tenant_id, org_id, token)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS cases_tenant_org_identity ON cases (tenant_id, org_id, id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS documents_tenant_org_identity ON documents (tenant_id, org_id, id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS documents_case_scoped_identity ON documents (tenant_id, org_id, case_id, id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS review_runs_tenant_org_identity ON review_runs (tenant_id, org_id, id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS review_attempts_tenant_org_identity ON review_attempts (tenant_id, org_id, id)`,
	`DO $$
	BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='documents_case_scope_fk' AND connamespace=current_schema()::regnamespace) THEN
			ALTER TABLE documents ADD CONSTRAINT documents_case_scope_fk FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id);
		END IF;
		IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='review_runs_case_scope_fk' AND connamespace=current_schema()::regnamespace) THEN
			ALTER TABLE review_runs ADD CONSTRAINT review_runs_case_scope_fk FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id);
		END IF;
		IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='review_attempts_run_scope_fk' AND connamespace=current_schema()::regnamespace) THEN
			ALTER TABLE review_attempts ADD CONSTRAINT review_attempts_run_scope_fk FOREIGN KEY (tenant_id, org_id, run_id) REFERENCES review_runs (tenant_id, org_id, id);
		END IF;
		IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='worker_jobs_case_scope_fk' AND connamespace=current_schema()::regnamespace) THEN
			ALTER TABLE worker_jobs ADD CONSTRAINT worker_jobs_case_scope_fk FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id);
		END IF;
		IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='tool_call_ledger_case_scope_fk' AND connamespace=current_schema()::regnamespace) THEN
			ALTER TABLE tool_call_ledger ADD CONSTRAINT tool_call_ledger_case_scope_fk FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id);
		END IF;
	END $$`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS rule_pack_compiler_ref text NOT NULL DEFAULT ''`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS rule_pack_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS rule_pack_version text NOT NULL DEFAULT ''`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS rule_pack_hash text NOT NULL DEFAULT ''`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS rule_pack_digest text NOT NULL DEFAULT ''`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS rule_pack_snapshot bytea`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS planner_configuration_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS rule_pack_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS rule_pack_version text NOT NULL DEFAULT ''`,
	`ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS rule_pack_pointer_revision bigint NOT NULL DEFAULT 0`,
	`ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS planner_configuration_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS planner_configuration bytea`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS planner_configuration bytea`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS cloud_policy_snapshot bytea`,
	`ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS cloud_policy_digest text NOT NULL DEFAULT ''`,
	businessSchemaSQL,
	governanceSchemaSQL,
	workbenchSchemaSQL,
	serviceProjectionSchemaSQL,
	serviceRoleSchemaSQL,
	modelBudgetSchemaSQL,
	queueSchedulerSchemaSQL,
	ExperienceFeedbackSchemaSQL,
	knowledgeGovernanceSchemaSQL,
	legalKnowledgeSchemaSQL,
	knowledgeAcceptanceSchemaSQL,
	EvidenceRequestSchemaSQL,
	EvaluationProvenanceSchemaSQL,
	`CREATE TABLE IF NOT EXISTS llm_endpoint_settings (
		id text PRIMARY KEY CHECK (id = 'active'),
		name text NOT NULL,
		protocol text NOT NULL CHECK (protocol IN ('openai-compatible')),
		base_url text NOT NULL,
		models jsonb NOT NULL DEFAULT '[]'::jsonb,
		review_model text NOT NULL DEFAULT '',
		ocr_model text NOT NULL DEFAULT '',
		secret_ciphertext bytea NOT NULL DEFAULT ''::bytea,
		revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
		updated_at text NOT NULL,
		updated_by text NOT NULL
	)`,
	experienceDistillSchemaSQL,
	advisoryJudgmentSchemaSQL,
	OperatorAuditSchemaSQL,
	`DO $$
	DECLARE table_name text;
	BEGIN
		FOREACH table_name IN ARRAY ARRAY[
			'sessions','cases','documents','parsed_documents','review_runs','review_attempts',
			'review_attempt_parse_selections','review_attempt_inputs','execution_events','findings',
			'constraint_evaluations','decisions','run_events','activity','idempotency',
			'review_run_snapshots','audit_events','worker_jobs','outbox_events','tool_call_ledger',
			'business_case_profiles','business_workbench_tasks','business_case_relations','business_knowledge','business_workbench_audit',
			'business_generations','business_remediations','business_remediation_revisions','business_reports','business_archives',
			'business_run_events','business_audit_events','document_activity','execution_activity',
			'model_call_leases','model_usage','execution_queue_policies','execution_queue_cursor','execution_queue_tenants',
			'rule_pack_versions','rule_pack_pointers','rule_pack_history','rule_pack_idempotency',
			'execution_agent_reviews','experience_feedback','experience_candidate_versions',
			'governed_knowledge_versions','governed_knowledge_state','governed_knowledge_pointers',
			'governed_knowledge_history','governed_knowledge_idempotency','governed_policy_rule_bindings',
			'experience_distill_inputs','experience_distill_results','experience_distill_failures',
			'advisory_judgments',
			'evidence_requests','evidence_receipts','evidence_request_events',
			'operator_object_bindings','operator_external_snapshot_sets','operator_external_observations',
			'operator_fulfillment_evaluations','operator_fulfillment_checks','operator_impact_plans',
			'legal_source_captures'
		] LOOP
			EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', table_name);
			EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', table_name);
			EXECUTE format('DROP POLICY IF EXISTS tenant_boundary ON %I', table_name);
			IF table_name = 'sessions' THEN
				EXECUTE format('CREATE POLICY tenant_boundary ON %I USING (tenant_id = current_setting(''app.tenant_id'', true) OR token = current_setting(''app.session_token'', true)) WITH CHECK (tenant_id = current_setting(''app.tenant_id'', true))', table_name);
			ELSIF table_name IN ('worker_jobs','outbox_events','execution_queue_policies','execution_queue_cursor','execution_queue_tenants') THEN
				EXECUTE format('CREATE POLICY tenant_boundary ON %I USING (tenant_id = current_setting(''app.tenant_id'', true) OR current_setting(''app.actor_role'', true) = ''worker'') WITH CHECK (tenant_id = current_setting(''app.tenant_id'', true) OR current_setting(''app.actor_role'', true) = ''worker'')', table_name);
			ELSE
				EXECUTE format('CREATE POLICY tenant_boundary ON %I USING (tenant_id = current_setting(''app.tenant_id'', true)) WITH CHECK (tenant_id = current_setting(''app.tenant_id'', true))', table_name);
			END IF;
		END LOOP;
	END $$`,
}
