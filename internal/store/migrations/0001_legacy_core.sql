-- Bootstrap the previously unversioned schema. Temporarily let the owning
-- migrator see every tenant while historical backfills run. Table locks and
-- this transaction prevent another session from observing the RLS transition.
DO $migration$
DECLARE table_name text;
BEGIN
 FOREACH table_name IN ARRAY ARRAY[
  'parsed_documents','review_attempts','review_attempt_parse_selections',
  'review_attempt_inputs','review_run_snapshots','operator_external_collection_claims'
 ] LOOP
  IF EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
             WHERE n.nspname=current_schema() AND c.relname=table_name
               AND c.relkind IN ('r','p') AND c.relforcerowsecurity) THEN
   EXECUTE format('ALTER TABLE %I NO FORCE ROW LEVEL SECURITY',table_name);
  END IF;
 END LOOP;
END $migration$;

CREATE SEQUENCE IF NOT EXISTS parsed_selection_order_seq;

CREATE TABLE IF NOT EXISTS sessions (
		seq bigserial PRIMARY KEY,
		token text NOT NULL UNIQUE,
		actor_id text NOT NULL,
		expires_at text NOT NULL
	);

CREATE TABLE IF NOT EXISTS cases (
		seq bigserial PRIMARY KEY,
		id text NOT NULL UNIQUE,
		title text NOT NULL,
		contract_type text NOT NULL,
		owner_id text NOT NULL,
		member_ids text NOT NULL,
		created_at text NOT NULL,
		updated_at text NOT NULL
	);

CREATE TABLE IF NOT EXISTS documents (
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
	);

ALTER TABLE documents ADD COLUMN IF NOT EXISTS storage_backend text NOT NULL DEFAULT 'local';

CREATE TABLE IF NOT EXISTS parsed_documents (
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
	);

ALTER TABLE parsed_documents ADD COLUMN IF NOT EXISTS selection_order bigint;

UPDATE parsed_documents SET selection_order = seq WHERE selection_order IS NULL;

ALTER TABLE parsed_documents ALTER COLUMN selection_order SET DEFAULT nextval('parsed_selection_order_seq');

ALTER TABLE parsed_documents ALTER COLUMN selection_order SET NOT NULL;

DO $migration$
DECLARE next_selection bigint; sequence_increment bigint;
BEGIN
 SELECT seqincrement INTO STRICT sequence_increment FROM pg_sequence
 WHERE seqrelid='parsed_selection_order_seq'::regclass;
 -- ALTER acquires the sequence lock before reading its live value; unlike
 -- setval, both ALTER operations roll back with the migration transaction.
 EXECUTE format('ALTER SEQUENCE parsed_selection_order_seq INCREMENT BY %s',sequence_increment);
 SELECT GREATEST((SELECT last_value FROM parsed_selection_order_seq),
                 COALESCE((SELECT MAX(selection_order) FROM parsed_documents),1))+sequence_increment
 INTO next_selection;
 EXECUTE format('ALTER SEQUENCE parsed_selection_order_seq RESTART WITH %s',next_selection);
END $migration$;

CREATE INDEX IF NOT EXISTS parsed_documents_selection ON parsed_documents (document_id, config_hash, selection_order);

CREATE UNIQUE INDEX IF NOT EXISTS parsed_documents_selection_order_unique ON parsed_documents (selection_order);

CREATE TABLE IF NOT EXISTS review_runs (
		seq bigserial PRIMARY KEY,
		id text NOT NULL UNIQUE,
		case_id text NOT NULL,
		state text NOT NULL DEFAULT '',
		model_ref text NOT NULL DEFAULT '',
		partial_reason text NOT NULL DEFAULT '',
		failure_message text NOT NULL DEFAULT '',
		created_at text NOT NULL,
		updated_at text NOT NULL
	);

CREATE TABLE IF NOT EXISTS review_attempts (
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
	);

ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS rule_pack_snapshot bytea;

ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS rule_pack_hash text NOT NULL DEFAULT '';

ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS rule_pack_snapshot_digest text NOT NULL DEFAULT '';

ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS rule_pack_compiler_ref text NOT NULL DEFAULT '';

ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS actor_id text NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS review_attempt_parse_selections (
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
	);

CREATE TABLE IF NOT EXISTS review_attempt_inputs (
		attempt_id text PRIMARY KEY REFERENCES review_attempts(id),
		run_id text NOT NULL REFERENCES review_runs(id),
		input_hash text NOT NULL,
		frozen_at text NOT NULL,
		documents text NOT NULL
	);

CREATE TABLE IF NOT EXISTS execution_events (
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
	);

ALTER TABLE execution_events ADD COLUMN IF NOT EXISTS recorded_at text;

ALTER TABLE execution_events ADD COLUMN IF NOT EXISTS event_key text;

ALTER TABLE execution_events ADD COLUMN IF NOT EXISTS generation bigint;

ALTER TABLE execution_events ADD COLUMN IF NOT EXISTS trace jsonb;

CREATE UNIQUE INDEX IF NOT EXISTS execution_events_logical_identity ON execution_events (attempt_id, generation, event_key) WHERE event_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS findings (
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
	);

CREATE TABLE IF NOT EXISTS constraint_evaluations (
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
	);

ALTER TABLE findings ADD COLUMN IF NOT EXISTS attempt_id text NOT NULL DEFAULT '';

ALTER TABLE findings ADD COLUMN IF NOT EXISTS rule_id text NOT NULL DEFAULT '';

ALTER TABLE findings ADD COLUMN IF NOT EXISTS capability_id text NOT NULL DEFAULT '';

ALTER TABLE findings ADD COLUMN IF NOT EXISTS object_ref text NOT NULL DEFAULT '';

ALTER TABLE findings ADD COLUMN IF NOT EXISTS severity text;

ALTER TABLE findings ADD COLUMN IF NOT EXISTS severity_source jsonb;

ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS attempt_id text NOT NULL DEFAULT '';

ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS rule_id text NOT NULL DEFAULT '';

ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS capability_id text NOT NULL DEFAULT '';

ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS reason_code text NOT NULL DEFAULT '';

ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS fact_ids text NOT NULL DEFAULT '[]';

ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS object_refs text NOT NULL DEFAULT '[]';

ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS mandatory boolean NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS decisions (
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
	);

CREATE TABLE IF NOT EXISTS run_events (
		seq bigserial PRIMARY KEY,
		run_id text NOT NULL,
		event_seq integer NOT NULL,
		event_type text NOT NULL,
		state text NOT NULL
	);

CREATE UNIQUE INDEX IF NOT EXISTS run_events_identity ON run_events (run_id, event_seq);

CREATE TABLE IF NOT EXISTS activity (
		seq bigserial PRIMARY KEY,
		id text NOT NULL UNIQUE,
		case_id text NOT NULL,
		occurred_at text NOT NULL,
		summary text NOT NULL
	);

CREATE TABLE IF NOT EXISTS idempotency (
		seq bigserial PRIMARY KEY,
		actor_id text NOT NULL,
		idem_key text NOT NULL,
		finding_id text NOT NULL,
		decision_id text NOT NULL,
		action text NOT NULL,
		reason text NOT NULL,
		expected_version integer NOT NULL,
		UNIQUE (actor_id, idem_key)
	);

CREATE TABLE IF NOT EXISTS review_run_snapshots (
		run_id text PRIMARY KEY,
		document_ids text NOT NULL,
		rule_set_hash text NOT NULL,
		playbook_hash text NOT NULL,
		model_ref text NOT NULL DEFAULT '',
		comparator_ref text NOT NULL
	);

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS model_configuration_id text NOT NULL DEFAULT '';

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS parsed_document_ids text NOT NULL DEFAULT '[]';

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS parse_selection_hash text NOT NULL DEFAULT '';

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS parser_configuration_id text NOT NULL DEFAULT '';

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS party_context bytea;

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS party_context_hash text NOT NULL DEFAULT '';

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS service_object_context bytea;

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS service_object_context_hash text NOT NULL DEFAULT '';

DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='review_run_snapshots_service_object_context_pair' AND conrelid='review_run_snapshots'::regclass) THEN
			ALTER TABLE review_run_snapshots ADD CONSTRAINT review_run_snapshots_service_object_context_pair CHECK (
				(service_object_context IS NULL AND service_object_context_hash='') OR
				(service_object_context IS NOT NULL AND octet_length(service_object_context)>0 AND service_object_context_hash ~ '^[0-9a-f]{64}$'));
		END IF;
	END $$;

CREATE TABLE IF NOT EXISTS audit_events (
		event_id text PRIMARY KEY,
		run_id text NOT NULL,
		actor text NOT NULL,
		seq integer NOT NULL,
		event_type text NOT NULL,
		prev_hash text NOT NULL DEFAULT '',
		hash text NOT NULL,
		correlation_ids text NOT NULL,
		UNIQUE (run_id, seq)
	);

CREATE OR REPLACE FUNCTION forbid_append_only() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'append-only';
		END;
		$$;

DROP TRIGGER IF EXISTS review_attempt_inputs_append_only ON review_attempt_inputs;

CREATE TRIGGER review_attempt_inputs_append_only
		BEFORE UPDATE OR DELETE ON review_attempt_inputs
		FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

DROP TRIGGER IF EXISTS review_attempt_parse_selections_append_only ON review_attempt_parse_selections;

CREATE TRIGGER review_attempt_parse_selections_append_only
		BEFORE UPDATE OR DELETE ON review_attempt_parse_selections
		FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

DROP TRIGGER IF EXISTS execution_events_append_only ON execution_events;

CREATE TRIGGER execution_events_append_only
		BEFORE UPDATE OR DELETE ON execution_events
		FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

DROP TRIGGER IF EXISTS review_run_snapshots_append_only ON review_run_snapshots;

CREATE TRIGGER review_run_snapshots_append_only
		BEFORE UPDATE OR DELETE ON review_run_snapshots
		FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

DROP TRIGGER IF EXISTS audit_events_append_only ON audit_events;

CREATE TRIGGER audit_events_append_only
		BEFORE UPDATE OR DELETE ON audit_events
		FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

DROP TRIGGER IF EXISTS parsed_documents_append_only ON parsed_documents;

CREATE TRIGGER parsed_documents_append_only
		BEFORE UPDATE OR DELETE ON parsed_documents
		FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

UPDATE review_attempts a SET history_kind='legacy_unfrozen'
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
		);

CREATE TABLE IF NOT EXISTS worker_jobs (
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
	);

CREATE INDEX IF NOT EXISTS worker_jobs_claim ON worker_jobs (state, lease_until, created_at, id);

CREATE TABLE IF NOT EXISTS outbox_events (
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
	);

CREATE TABLE IF NOT EXISTS tool_call_ledger (
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
	);

CREATE INDEX IF NOT EXISTS tool_call_ledger_tenant_window ON tool_call_ledger (tenant_id, reserved_at);

CREATE INDEX IF NOT EXISTS tool_call_ledger_attempt_budget ON tool_call_ledger (tenant_id, org_id, case_id, run_id, attempt_id);

DO $$
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
	END $$;

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS issuer text NOT NULL DEFAULT '';

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS subject text NOT NULL DEFAULT '';

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS audience text NOT NULL DEFAULT 'client';

ALTER TABLE cases ADD COLUMN IF NOT EXISTS business_line text NOT NULL DEFAULT 'unspecified';

ALTER TABLE cases ADD COLUMN IF NOT EXISTS entity_version bigint NOT NULL DEFAULT 1;

ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS entity_version bigint NOT NULL DEFAULT 1;

ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0;

ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS claim_owner text NOT NULL DEFAULT '';

ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS claim_until timestamptz;

ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS last_error text NOT NULL DEFAULT '';

ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT to_timestamp(0);

CREATE INDEX IF NOT EXISTS outbox_events_pending_scan ON outbox_events (next_attempt_at, created_at, id) WHERE state='pending';

ALTER TABLE cases ADD COLUMN IF NOT EXISTS scenario text NOT NULL DEFAULT 'unspecified';

ALTER TABLE cases ADD COLUMN IF NOT EXISTS business_status text NOT NULL DEFAULT 'active';

ALTER TABLE cases ADD COLUMN IF NOT EXISTS archived_at text;

CREATE UNIQUE INDEX IF NOT EXISTS sessions_token_identity ON sessions (tenant_id, org_id, token);

CREATE UNIQUE INDEX IF NOT EXISTS cases_tenant_org_identity ON cases (tenant_id, org_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS documents_tenant_org_identity ON documents (tenant_id, org_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS documents_case_scoped_identity ON documents (tenant_id, org_id, case_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS review_runs_tenant_org_identity ON review_runs (tenant_id, org_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS review_attempts_tenant_org_identity ON review_attempts (tenant_id, org_id, id);

DO $$
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
	END $$;

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS rule_pack_compiler_ref text NOT NULL DEFAULT '';

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS rule_pack_id text NOT NULL DEFAULT '';

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS rule_pack_version text NOT NULL DEFAULT '';

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS rule_pack_hash text NOT NULL DEFAULT '';

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS rule_pack_digest text NOT NULL DEFAULT '';

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS rule_pack_snapshot bytea;

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS planner_configuration_id text NOT NULL DEFAULT '';

ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS rule_pack_id text NOT NULL DEFAULT '';

ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS rule_pack_version text NOT NULL DEFAULT '';

ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS rule_pack_pointer_revision bigint NOT NULL DEFAULT 0;

ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS planner_configuration_id text NOT NULL DEFAULT '';

ALTER TABLE review_attempts ADD COLUMN IF NOT EXISTS planner_configuration bytea;

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS planner_configuration bytea;

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS cloud_policy_snapshot bytea;

ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS cloud_policy_digest text NOT NULL DEFAULT '';


CREATE UNIQUE INDEX IF NOT EXISTS documents_tenant_org_case_identity ON documents (tenant_id, org_id, case_id, id);
CREATE UNIQUE INDEX IF NOT EXISTS review_runs_tenant_org_case_identity ON review_runs (tenant_id, org_id, case_id, id);
CREATE UNIQUE INDEX IF NOT EXISTS review_attempts_tenant_org_run_identity ON review_attempts (tenant_id, org_id, run_id, id);

CREATE TABLE IF NOT EXISTS business_generations (
	seq bigserial PRIMARY KEY,
	id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	document_id text NOT NULL,
	template_id text NOT NULL,
	template_version text NOT NULL,
	digest text NOT NULL,
	created_at text NOT NULL,
	UNIQUE (tenant_id, org_id, case_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id, document_id) REFERENCES documents (tenant_id, org_id, case_id, id)
);
CREATE INDEX IF NOT EXISTS business_generations_case_order ON business_generations (tenant_id, org_id, case_id, seq);

CREATE TABLE IF NOT EXISTS business_remediations (
	seq bigserial PRIMARY KEY,
	id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	run_id text NOT NULL,
	attempt_id text NOT NULL,
	finding_ids text[] NOT NULL DEFAULT '{}',
	assignee_id text NOT NULL DEFAULT '',
	due_at text NOT NULL DEFAULT '',
	measure text NOT NULL,
	status text NOT NULL,
	version integer NOT NULL CHECK (version > 0),
	document_ids text[] NOT NULL DEFAULT '{}',
	reason text NOT NULL,
	verification_run_id text,
	verification_attempt_id text,
	created_at text NOT NULL,
	updated_at text NOT NULL,
	UNIQUE (tenant_id, org_id, case_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id, run_id) REFERENCES review_runs (tenant_id, org_id, case_id, id),
	FOREIGN KEY (tenant_id, org_id, run_id, attempt_id) REFERENCES review_attempts (tenant_id, org_id, run_id, id),
    FOREIGN KEY (tenant_id, org_id, case_id, verification_run_id) REFERENCES review_runs (tenant_id, org_id, case_id, id),
    CONSTRAINT business_remediations_verification_scope_fkey FOREIGN KEY (tenant_id, org_id, verification_run_id, verification_attempt_id) REFERENCES review_attempts (tenant_id, org_id, run_id, id),
    CHECK ((verification_run_id IS NULL) = (verification_attempt_id IS NULL))
);
CREATE INDEX IF NOT EXISTS business_remediations_case_order ON business_remediations (tenant_id, org_id, case_id, seq);
CREATE INDEX IF NOT EXISTS business_remediations_attempt ON business_remediations (tenant_id, org_id, case_id, run_id, attempt_id);
DO $$
DECLARE
	legacy text;
BEGIN
	SELECT conname INTO legacy FROM pg_constraint
	WHERE conrelid = 'business_remediations'::regclass AND contype = 'f'
		AND pg_get_constraintdef(oid) LIKE '%(tenant_id, org_id, run_id, verification_attempt_id)%';
	IF legacy IS NOT NULL THEN
		EXECUTE format('ALTER TABLE business_remediations DROP CONSTRAINT %I', legacy);
	END IF;
	IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='business_remediations_verification_scope_fkey' AND connamespace=current_schema()::regnamespace) THEN
		ALTER TABLE business_remediations ADD CONSTRAINT business_remediations_verification_scope_fkey
			FOREIGN KEY (tenant_id, org_id, verification_run_id, verification_attempt_id)
			REFERENCES review_attempts (tenant_id, org_id, run_id, id);
	END IF;
END $$;

CREATE TABLE IF NOT EXISTS business_remediation_revisions (
	seq bigserial PRIMARY KEY,
	id text NOT NULL,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	remediation_id text NOT NULL,
	version integer NOT NULL CHECK (version > 0),
	recorded_at text NOT NULL,
	record_json jsonb NOT NULL,
	UNIQUE (tenant_id, org_id, case_id, remediation_id, version),
	FOREIGN KEY (tenant_id, org_id, case_id, remediation_id) REFERENCES business_remediations (tenant_id, org_id, case_id, id),
	CHECK (id = remediation_id AND record_json->>'id' = remediation_id AND (record_json->>'version')::integer = version)
);
CREATE INDEX IF NOT EXISTS business_remediation_revisions_case_order ON business_remediation_revisions (tenant_id, org_id, case_id, seq);

CREATE TABLE IF NOT EXISTS business_reports (
	seq bigserial PRIMARY KEY,
	id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	run_id text NOT NULL,
	attempt_id text NOT NULL,
	version integer NOT NULL CHECK (version > 0),
	input_hash text NOT NULL,
	output_hash text NOT NULL,
	decision_snapshot_hash text NOT NULL,
	digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
	created_at text NOT NULL,
	json_bytes bytea NOT NULL,
	html_bytes bytea NOT NULL,
	UNIQUE (tenant_id, org_id, case_id, id),
	UNIQUE (tenant_id, org_id, case_id, version),
	FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id, run_id) REFERENCES review_runs (tenant_id, org_id, case_id, id),
	FOREIGN KEY (tenant_id, org_id, run_id, attempt_id) REFERENCES review_attempts (tenant_id, org_id, run_id, id)
);
CREATE INDEX IF NOT EXISTS business_reports_case_order ON business_reports (tenant_id, org_id, case_id, seq);
CREATE INDEX IF NOT EXISTS business_reports_attempt ON business_reports (tenant_id, org_id, case_id, run_id, attempt_id);

CREATE TABLE IF NOT EXISTS business_archives (
	seq bigserial PRIMARY KEY,
	id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	report_id text NOT NULL,
	actor_id text NOT NULL,
	reason text NOT NULL,
	archived_at text NOT NULL,
	UNIQUE (tenant_id, org_id, case_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id, report_id) REFERENCES business_reports (tenant_id, org_id, case_id, id)
);
CREATE INDEX IF NOT EXISTS business_archives_case_order ON business_archives (tenant_id, org_id, case_id, seq);

CREATE OR REPLACE FUNCTION guard_business_remediation_update() RETURNS trigger
	LANGUAGE plpgsql AS $$
	BEGIN
		IF TG_OP = 'DELETE' THEN
			RAISE EXCEPTION 'append-only';
		END IF;
		IF NEW.id IS DISTINCT FROM OLD.id OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR
			NEW.org_id IS DISTINCT FROM OLD.org_id OR NEW.case_id IS DISTINCT FROM OLD.case_id OR
			NEW.run_id IS DISTINCT FROM OLD.run_id OR NEW.attempt_id IS DISTINCT FROM OLD.attempt_id OR
			NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.version <> OLD.version + 1 THEN
			RAISE EXCEPTION 'remediation identity or version is immutable';
		END IF;
		RETURN NEW;
	END;
	$$;
DROP TRIGGER IF EXISTS business_generations_append_only ON business_generations;
CREATE TRIGGER business_generations_append_only BEFORE UPDATE OR DELETE ON business_generations FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS business_remediations_update_guard ON business_remediations;
CREATE TRIGGER business_remediations_update_guard BEFORE UPDATE OR DELETE ON business_remediations FOR EACH ROW EXECUTE FUNCTION guard_business_remediation_update();
DROP TRIGGER IF EXISTS business_remediation_revisions_append_only ON business_remediation_revisions;
CREATE TRIGGER business_remediation_revisions_append_only BEFORE UPDATE OR DELETE ON business_remediation_revisions FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS business_reports_append_only ON business_reports;
CREATE TRIGGER business_reports_append_only BEFORE UPDATE OR DELETE ON business_reports FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS business_archives_append_only ON business_archives;
CREATE TRIGGER business_archives_append_only BEFORE UPDATE OR DELETE ON business_archives FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
;


CREATE TABLE IF NOT EXISTS rule_pack_versions (
	tenant_id text NOT NULL,
	pack_id text NOT NULL,
	version text NOT NULL,
	org_id text NOT NULL,
	status text NOT NULL CHECK (status IN ('draft','validated','approved','published')),
	revision bigint NOT NULL CHECK (revision > 0),
	manifest bytea NOT NULL CHECK (octet_length(manifest) > 0 AND octet_length(manifest) <= 1048576),
	content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
	snapshot_digest text NOT NULL CHECK (snapshot_digest ~ '^[0-9a-f]{64}$'),
	effective_from text NOT NULL,
	effective_until text NOT NULL,
	authority_sources text[] NOT NULL DEFAULT '{}',
	approval_actor_id text NOT NULL DEFAULT '',
	approved_at text NOT NULL DEFAULT '',
	published_at text NOT NULL DEFAULT '',
	published_by text NOT NULL DEFAULT '',
	created_at text NOT NULL,
	updated_at text NOT NULL,
	PRIMARY KEY (tenant_id, pack_id, version)
);
CREATE INDEX IF NOT EXISTS rule_pack_versions_tenant_order ON rule_pack_versions (tenant_id, pack_id, created_at, version);

-- The pointer row is never deleted: disable keeps a tombstone (active=false)
-- with a monotonically increasing pointer_revision, so a stale CAS from before
-- the disable can never match again (no ABA) and the revision keeps counting.
CREATE TABLE IF NOT EXISTS rule_pack_pointers (
	tenant_id text NOT NULL,
	pack_id text NOT NULL,
	version text NOT NULL CHECK (version <> ''),
	active boolean NOT NULL DEFAULT true,
	pointer_revision bigint NOT NULL CHECK (pointer_revision > 0),
	updated_at text NOT NULL,
	updated_by text NOT NULL,
	PRIMARY KEY (tenant_id, pack_id),
	FOREIGN KEY (tenant_id, pack_id, version) REFERENCES rule_pack_versions (tenant_id, pack_id, version)
);
ALTER TABLE rule_pack_pointers ADD COLUMN IF NOT EXISTS active boolean NOT NULL DEFAULT true;

CREATE TABLE IF NOT EXISTS rule_pack_history (
	seq bigserial PRIMARY KEY,
	tenant_id text NOT NULL,
	pack_id text NOT NULL,
	version text NOT NULL,
	action text NOT NULL CHECK (action IN ('create','edit','validate','approve','publish','disable','rollback','test')),
	actor_id text NOT NULL,
	content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
	pointer_revision bigint NOT NULL CHECK (pointer_revision >= 0),
	created_at text NOT NULL,
	previous_version text,
	previous_active boolean,
	resulting_version text,
	resulting_active boolean
);
ALTER TABLE rule_pack_history ADD COLUMN IF NOT EXISTS previous_version text;
ALTER TABLE rule_pack_history ADD COLUMN IF NOT EXISTS previous_active boolean;
ALTER TABLE rule_pack_history ADD COLUMN IF NOT EXISTS resulting_version text;
ALTER TABLE rule_pack_history ADD COLUMN IF NOT EXISTS resulting_active boolean;
-- Different packs may share an effective window. WITHOUT OVERLAPS is not
-- declared until a confirmed exclusion key exists: tenant, business line,
-- and rule slot. Overlapping packs are a supported configuration.
CREATE INDEX IF NOT EXISTS rule_pack_history_tenant_order ON rule_pack_history (tenant_id, pack_id, seq);

CREATE TABLE IF NOT EXISTS rule_pack_idempotency (
	tenant_id text NOT NULL,
	actor_id text NOT NULL,
	idem_key text NOT NULL,
	request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
	response_json jsonb NOT NULL,
	created_at text NOT NULL,
	PRIMARY KEY (tenant_id, actor_id, idem_key)
);

-- execution_agent_reviews holds the terminal agent-flow projection of one
-- attempt. It is Execution-owned: the durable worker writes it before the
-- terminal transition under a live claim generation fence, and reads verify its
-- hash. Historical attempts without the subsystem have no row and report null.
CREATE TABLE IF NOT EXISTS execution_agent_reviews (
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	run_id text NOT NULL,
	attempt_id text NOT NULL,
	generation bigint NOT NULL CHECK (generation > 0),
	attempt_input_sha256 text NOT NULL CHECK (attempt_input_sha256 ~ '^[0-9a-f]{64}$'),
	agent_review_sha256 text NOT NULL CHECK (agent_review_sha256 ~ '^[0-9a-f]{64}$'),
	-- bytea keeps the exact marshaled bytes the hash covers; a jsonb column would
	-- re-normalize whitespace and key order on readback and break the digest.
	agent_review bytea NOT NULL,
	created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
	PRIMARY KEY (tenant_id, org_id, attempt_id),
	FOREIGN KEY (tenant_id, org_id, case_id, run_id) REFERENCES review_runs (tenant_id, org_id, case_id, id),
	FOREIGN KEY (tenant_id, org_id, run_id, attempt_id) REFERENCES review_attempts (tenant_id, org_id, run_id, id)
);
CREATE INDEX IF NOT EXISTS execution_agent_reviews_attempt ON execution_agent_reviews (tenant_id, org_id, run_id, attempt_id);
DROP TRIGGER IF EXISTS execution_agent_reviews_terminal_guard ON execution_agent_reviews;
-- No append-only trigger: a newer live generation legitimately replaces an
-- unfinalized row. Terminal immutability is enforced by the attempt-state check
-- and the claim generation fence inside the writing transaction, not by the table.
;


CREATE TABLE IF NOT EXISTS business_case_profiles (
	seq bigserial PRIMARY KEY,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	number text NOT NULL,
	version integer NOT NULL CHECK (version > 0),
	record_json jsonb NOT NULL,
	UNIQUE (tenant_id, org_id, case_id),
	FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS business_case_profiles_number ON business_case_profiles (tenant_id, lower(number));
CREATE TABLE IF NOT EXISTS business_workbench_tasks (
	seq bigserial PRIMARY KEY,
	id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	version integer NOT NULL CHECK (version > 0),
	record_json jsonb NOT NULL,
	UNIQUE (tenant_id, org_id, case_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id)
);
CREATE INDEX IF NOT EXISTS business_workbench_tasks_case_order ON business_workbench_tasks (tenant_id, org_id, case_id, seq);
CREATE TABLE IF NOT EXISTS business_case_relations (
	seq bigserial PRIMARY KEY,
	id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	related_org_id text NOT NULL,
	related_case_id text NOT NULL,
	pair_key text NOT NULL,
	record_json jsonb NOT NULL,
	UNIQUE (tenant_id, pair_key),
	FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id),
	FOREIGN KEY (tenant_id, related_org_id, related_case_id) REFERENCES cases (tenant_id, org_id, id),
	CHECK ((org_id, case_id) <> (related_org_id, related_case_id))
);
CREATE INDEX IF NOT EXISTS business_case_relations_case_order ON business_case_relations (tenant_id, org_id, case_id, seq);
CREATE TABLE IF NOT EXISTS business_knowledge (
	seq bigserial PRIMARY KEY,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	id text NOT NULL,
	version text NOT NULL,
	source_type text NOT NULL CHECK (source_type IN ('synthetic_policy','reference')),
	record_json jsonb NOT NULL,
	UNIQUE (tenant_id, id, version)
);
CREATE INDEX IF NOT EXISTS business_knowledge_tenant_order ON business_knowledge (tenant_id, seq);
CREATE TABLE IF NOT EXISTS business_workbench_audit (
	seq bigserial PRIMARY KEY,
	id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text,
	occurred_at text NOT NULL,
	record_json jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS business_workbench_audit_tenant_order ON business_workbench_audit (tenant_id, seq);
CREATE INDEX IF NOT EXISTS business_workbench_audit_case_order ON business_workbench_audit (tenant_id, org_id, case_id, seq);

CREATE OR REPLACE FUNCTION guard_business_case_profile_update() RETURNS trigger
	LANGUAGE plpgsql AS $$
	BEGIN
		IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.org_id IS DISTINCT FROM OLD.org_id OR
			NEW.case_id IS DISTINCT FROM OLD.case_id OR NEW.version <> OLD.version + 1 THEN
			RAISE EXCEPTION 'case profile identity or version is immutable';
		END IF;
		RETURN NEW;
	END;
	$$;
CREATE OR REPLACE FUNCTION guard_business_workbench_task_update() RETURNS trigger
	LANGUAGE plpgsql AS $$
	BEGIN
		IF NEW.id IS DISTINCT FROM OLD.id OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR
			NEW.org_id IS DISTINCT FROM OLD.org_id OR NEW.case_id IS DISTINCT FROM OLD.case_id OR
			NEW.version <> OLD.version + 1 OR NEW.record_json->>'created_at' IS DISTINCT FROM OLD.record_json->>'created_at' OR
			NEW.record_json->>'created_by' IS DISTINCT FROM OLD.record_json->>'created_by' THEN
			RAISE EXCEPTION 'workbench task identity, creator, or version is immutable';
		END IF;
		RETURN NEW;
	END;
	$$;
CREATE OR REPLACE FUNCTION forbid_business_relation_update() RETURNS trigger
	LANGUAGE plpgsql AS $$
	BEGIN
		RAISE EXCEPTION 'case relations are immutable';
	END;
	$$;
DROP TRIGGER IF EXISTS business_case_profiles_update_guard ON business_case_profiles;
CREATE TRIGGER business_case_profiles_update_guard BEFORE UPDATE ON business_case_profiles FOR EACH ROW EXECUTE FUNCTION guard_business_case_profile_update();
DROP TRIGGER IF EXISTS business_workbench_tasks_update_guard ON business_workbench_tasks;
CREATE TRIGGER business_workbench_tasks_update_guard BEFORE UPDATE ON business_workbench_tasks FOR EACH ROW EXECUTE FUNCTION guard_business_workbench_task_update();
DROP TRIGGER IF EXISTS business_case_relations_update_guard ON business_case_relations;
CREATE TRIGGER business_case_relations_update_guard BEFORE UPDATE ON business_case_relations FOR EACH ROW EXECUTE FUNCTION forbid_business_relation_update();
DROP TRIGGER IF EXISTS business_knowledge_append_only ON business_knowledge;
CREATE TRIGGER business_knowledge_append_only BEFORE UPDATE OR DELETE ON business_knowledge FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS business_workbench_audit_append_only ON business_workbench_audit;
CREATE TRIGGER business_workbench_audit_append_only BEFORE UPDATE OR DELETE ON business_workbench_audit FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
;


CREATE TABLE IF NOT EXISTS business_run_events (
 seq bigserial PRIMARY KEY,
 run_id text NOT NULL,
 event_seq integer NOT NULL,
 event_type text NOT NULL,
 state text NOT NULL,
 tenant_id text NOT NULL DEFAULT current_setting('app.tenant_id',true),
 org_id text NOT NULL DEFAULT current_setting('app.org_id',true),
 UNIQUE (run_id,event_seq),
 FOREIGN KEY (tenant_id,org_id,run_id) REFERENCES review_runs(tenant_id,org_id,id)
);
CREATE TABLE IF NOT EXISTS business_audit_events (
 event_id text PRIMARY KEY,
 run_id text NOT NULL,
 actor text NOT NULL,
 seq integer NOT NULL,
 event_type text NOT NULL,
 prev_hash text NOT NULL DEFAULT '',
 hash text NOT NULL,
 correlation_ids text NOT NULL,
 tenant_id text NOT NULL DEFAULT current_setting('app.tenant_id',true),
 org_id text NOT NULL DEFAULT current_setting('app.org_id',true),
 UNIQUE (run_id,seq),
 FOREIGN KEY (tenant_id,org_id,run_id) REFERENCES review_runs(tenant_id,org_id,id)
);
CREATE TABLE IF NOT EXISTS document_activity (
 seq bigserial PRIMARY KEY,
 id text NOT NULL UNIQUE,
 case_id text NOT NULL,
 occurred_at text NOT NULL,
 summary text NOT NULL,
 tenant_id text NOT NULL DEFAULT current_setting('app.tenant_id',true),
 org_id text NOT NULL DEFAULT current_setting('app.org_id',true),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases(tenant_id,org_id,id)
);
CREATE TABLE IF NOT EXISTS execution_activity (
 seq bigserial PRIMARY KEY,
 id text NOT NULL UNIQUE,
 case_id text NOT NULL,
 occurred_at text NOT NULL,
 summary text NOT NULL,
 tenant_id text NOT NULL DEFAULT current_setting('app.tenant_id',true),
 org_id text NOT NULL DEFAULT current_setting('app.org_id',true),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases(tenant_id,org_id,id)
);
DROP TRIGGER IF EXISTS business_run_events_append_only ON business_run_events;
CREATE TRIGGER business_run_events_append_only BEFORE UPDATE OR DELETE ON business_run_events FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS business_audit_events_append_only ON business_audit_events;
CREATE TRIGGER business_audit_events_append_only BEFORE UPDATE OR DELETE ON business_audit_events FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS document_activity_append_only ON document_activity;
CREATE TRIGGER document_activity_append_only BEFORE UPDATE OR DELETE ON document_activity FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS execution_activity_append_only ON execution_activity;
CREATE TRIGGER execution_activity_append_only BEFORE UPDATE OR DELETE ON execution_activity FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
;

CREATE TABLE IF NOT EXISTS service_database_roles (
 role_name text PRIMARY KEY,
 service text NOT NULL CHECK (service IN ('core','document','execution','model','tool')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);


CREATE TABLE IF NOT EXISTS model_call_leases (
	lease_id text PRIMARY KEY,
	tenant_id text NOT NULL,
	actor_id text NOT NULL,
	case_id text NOT NULL,
	run_id text NOT NULL,
	attempt_id text NOT NULL,
	caller_identity text NOT NULL CHECK (caller_identity IN ('worker','document')),
	model_ref text NOT NULL,
	attempt_number integer NOT NULL CHECK (attempt_number > 0),
	reserved_input_tokens bigint NOT NULL CHECK (reserved_input_tokens >= 0),
	reserved_output_tokens bigint NOT NULL CHECK (reserved_output_tokens >= 0),
	acquired_at timestamptz NOT NULL,
	expires_at timestamptz NOT NULL
);
ALTER TABLE model_call_leases ADD COLUMN IF NOT EXISTS attempt_number integer NOT NULL DEFAULT 1;
ALTER TABLE model_call_leases ADD COLUMN IF NOT EXISTS reserved_input_tokens bigint NOT NULL DEFAULT 0;
ALTER TABLE model_call_leases ADD COLUMN IF NOT EXISTS purpose text NOT NULL DEFAULT 'review';
ALTER TABLE model_call_leases ADD COLUMN IF NOT EXISTS job_id text NOT NULL DEFAULT '';
ALTER TABLE model_call_leases ADD COLUMN IF NOT EXISTS job_generation bigint NOT NULL DEFAULT 0;
ALTER TABLE model_call_leases ADD COLUMN IF NOT EXISTS input_sha256 text NOT NULL DEFAULT '';
ALTER TABLE model_call_leases ADD COLUMN IF NOT EXISTS reserved_output_tokens bigint NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS model_call_leases_expiry ON model_call_leases (expires_at);
CREATE INDEX IF NOT EXISTS model_call_leases_tenant_expiry ON model_call_leases (tenant_id, expires_at);
CREATE INDEX IF NOT EXISTS model_call_leases_acquired ON model_call_leases (acquired_at);
CREATE TABLE IF NOT EXISTS model_usage (
	call_id text PRIMARY KEY,
	lease_id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	actor_id text NOT NULL,
	case_id text NOT NULL,
	run_id text NOT NULL,
	attempt_id text NOT NULL,
	caller_identity text NOT NULL CHECK (caller_identity IN ('worker','document')),
	model_ref text NOT NULL,
	attempt_number integer NOT NULL,
	call_status text NOT NULL,
	error_code text NOT NULL DEFAULT '',
	usage_source text,
	input_tokens bigint,
	output_tokens bigint,
	total_tokens bigint,
	occurred_at timestamptz NOT NULL,
	reserved_at timestamptz,
	reserved_input_tokens bigint,
	reserved_output_tokens bigint,
	charged_tokens bigint,
	budget_released boolean NOT NULL DEFAULT false,
	settlement_fingerprint text
);
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS reserved_at timestamptz;
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS reserved_input_tokens bigint;
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS reserved_output_tokens bigint;
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS charged_tokens bigint;
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS budget_released boolean NOT NULL DEFAULT false;
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS settlement_fingerprint text;
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS purpose text NOT NULL DEFAULT 'review';
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS job_id text NOT NULL DEFAULT '';
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS job_generation bigint NOT NULL DEFAULT 0;
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS input_sha256 text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS model_usage_occurred ON model_usage (occurred_at);
CREATE INDEX IF NOT EXISTS model_usage_tenant_occurred ON model_usage (tenant_id, occurred_at);
CREATE INDEX IF NOT EXISTS model_usage_reserved_at ON model_usage (reserved_at);
;


CREATE TABLE IF NOT EXISTS execution_queue_policies (
	tenant_id text NOT NULL DEFAULT '__scheduler__',
	policy_hash text PRIMARY KEY,
	policy_json text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS execution_queue_cursor (
	tenant_id text PRIMARY KEY CHECK (tenant_id='__scheduler__'),
	policy_hash text NOT NULL REFERENCES execution_queue_policies(policy_hash),
	last_tenant text NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS execution_queue_tenants (
	tenant_id text PRIMARY KEY,
	deficit integer NOT NULL DEFAULT 0 CHECK (deficit >= 0)
);
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS policy_hash text NOT NULL DEFAULT '';
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS claim_count integer NOT NULL DEFAULT 0;
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS max_claims integer NOT NULL DEFAULT 3;
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS deadline_at timestamptz NOT NULL DEFAULT (clock_timestamp()+interval '15 minutes');
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS ready_at timestamptz NOT NULL DEFAULT to_timestamp(0);
CREATE INDEX IF NOT EXISTS worker_jobs_tenant_ready ON worker_jobs (tenant_id,ready_at,created_at,id) WHERE state IN ('queued','running');
;


CREATE UNIQUE INDEX IF NOT EXISTS experience_run_scope_identity ON review_runs (tenant_id,org_id,case_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS experience_attempt_scope_identity ON review_attempts (tenant_id,org_id,run_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS experience_finding_scope_identity ON findings (tenant_id,org_id,case_id,run_id,attempt_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS experience_decision_scope_identity ON decisions (tenant_id,org_id,case_id,run_id,finding_id,id);
CREATE TABLE IF NOT EXISTS experience_feedback (
    tenant_id text NOT NULL,
    org_id text NOT NULL,
    case_id text NOT NULL,
    feedback_id text NOT NULL,
    run_id text NOT NULL,
    attempt_id text NOT NULL,
    finding_id text NOT NULL,
    decision_id text NOT NULL,
    decision_action text NOT NULL,
    decision_disposition text NOT NULL,
    reason text NOT NULL,
    evidence_refs jsonb NOT NULL,
    applicant_id text NOT NULL,
    idempotency_key text NOT NULL,
    request_sha256 text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,feedback_id),
    UNIQUE (tenant_id,org_id,case_id,feedback_id),
    UNIQUE (tenant_id,applicant_id,idempotency_key),
    FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases (tenant_id,org_id,id),
    FOREIGN KEY (tenant_id,org_id,case_id,run_id) REFERENCES review_runs (tenant_id,org_id,case_id,id),
    FOREIGN KEY (tenant_id,org_id,run_id,attempt_id) REFERENCES review_attempts (tenant_id,org_id,run_id,id),
    FOREIGN KEY (tenant_id,org_id,case_id,run_id,attempt_id,finding_id) REFERENCES findings (tenant_id,org_id,case_id,run_id,attempt_id,id),
    FOREIGN KEY (tenant_id,org_id,case_id,run_id,finding_id,decision_id) REFERENCES decisions (tenant_id,org_id,case_id,run_id,finding_id,id)
);
CREATE INDEX IF NOT EXISTS experience_feedback_case ON experience_feedback (tenant_id,org_id,case_id,created_at,feedback_id);
DROP TRIGGER IF EXISTS experience_feedback_append_only ON experience_feedback;
CREATE TRIGGER experience_feedback_append_only BEFORE UPDATE OR DELETE ON experience_feedback FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
CREATE TABLE IF NOT EXISTS experience_candidate_versions (
    tenant_id text NOT NULL,
    org_id text NOT NULL,
    case_id text NOT NULL,
    feedback_id text NOT NULL,
    candidate_id text NOT NULL,
    version_id text NOT NULL,
    base_version_id text,
    content_sha256 text NOT NULL,
    job_id text NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    input_sha256 text NOT NULL,
    result_sha256 text NOT NULL,
    model_ref text NOT NULL,
    template_ref text NOT NULL,
    content_bytes bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,candidate_id,version_id),
    UNIQUE (tenant_id,job_id,generation),
    UNIQUE (tenant_id,candidate_id,version_id,content_sha256),
    FOREIGN KEY (tenant_id,org_id,case_id,feedback_id) REFERENCES experience_feedback (tenant_id,org_id,case_id,feedback_id),
    FOREIGN KEY (tenant_id,candidate_id,base_version_id) REFERENCES experience_candidate_versions (tenant_id,candidate_id,version_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS experience_candidate_first_revision ON experience_candidate_versions (tenant_id,candidate_id) WHERE base_version_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS experience_candidate_successor ON experience_candidate_versions (tenant_id,candidate_id,base_version_id) WHERE base_version_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS experience_candidate_source ON experience_candidate_versions (tenant_id,org_id,case_id,feedback_id,created_at);
DROP TRIGGER IF EXISTS experience_candidate_append_only ON experience_candidate_versions;
CREATE TRIGGER experience_candidate_append_only BEFORE UPDATE OR DELETE ON experience_candidate_versions FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
;


CREATE TABLE IF NOT EXISTS governed_knowledge_versions (
 tenant_id text NOT NULL,
 knowledge_id text NOT NULL,
 version_id text NOT NULL CHECK (version_id ~ '^[0-9a-f]{64}$'),
 display_version text NOT NULL,
 scope_key text NOT NULL CHECK (scope_key ~ '^[0-9a-f]{64}$'),
 kind text NOT NULL CHECK (kind IN ('policy','guidance','exception','experience')),
 scope_org_id text,
 scope_case_id text,
 source_org_id text,
 source_case_id text,
 candidate_id text,
 candidate_version_id text,
 candidate_content_sha256 text,
 content_sha256 text NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
 content bytea NOT NULL,
 created_by text NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id, knowledge_id, version_id),
 UNIQUE (tenant_id, knowledge_id, display_version),
 UNIQUE (tenant_id, knowledge_id, version_id, scope_key),
 CHECK (scope_case_id IS NULL OR scope_org_id IS NOT NULL),
 CHECK ((source_org_id IS NULL) = (source_case_id IS NULL)),
 CHECK ((candidate_id IS NULL AND candidate_version_id IS NULL AND candidate_content_sha256 IS NULL) OR
        (kind='experience' AND candidate_id IS NOT NULL AND candidate_version_id IS NOT NULL AND candidate_content_sha256 IS NOT NULL)),
 FOREIGN KEY (tenant_id, scope_org_id, scope_case_id) REFERENCES cases (tenant_id, org_id, id),
 FOREIGN KEY (tenant_id, source_org_id, source_case_id) REFERENCES cases (tenant_id, org_id, id),
 FOREIGN KEY (tenant_id, candidate_id, candidate_version_id, candidate_content_sha256) REFERENCES experience_candidate_versions (tenant_id, candidate_id, version_id, content_sha256)
);
CREATE INDEX IF NOT EXISTS governed_knowledge_versions_scope ON governed_knowledge_versions (tenant_id, scope_key, knowledge_id, kind);
CREATE TABLE IF NOT EXISTS governed_knowledge_state (
 tenant_id text NOT NULL,
 knowledge_id text NOT NULL,
 version_id text NOT NULL,
 status text NOT NULL CHECK (status IN ('draft','submitted','validated','approved','published','withdrawn')),
 revision bigint NOT NULL CHECK (revision > 0),
 approved_digest text NOT NULL DEFAULT '',
 approval_actor_id text NOT NULL DEFAULT '',
 approval_reason text NOT NULL DEFAULT '',
 cross_case_reviewed boolean NOT NULL DEFAULT false,
 PRIMARY KEY (tenant_id, knowledge_id, version_id),
 FOREIGN KEY (tenant_id, knowledge_id, version_id) REFERENCES governed_knowledge_versions (tenant_id, knowledge_id, version_id)
);
CREATE TABLE IF NOT EXISTS governed_knowledge_pointers (
 tenant_id text NOT NULL,
 knowledge_id text NOT NULL,
 scope_key text NOT NULL,
 version_id text NOT NULL,
 active boolean NOT NULL,
 pointer_revision bigint NOT NULL CHECK (pointer_revision > 0),
 updated_by text NOT NULL,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id, knowledge_id, scope_key),
 FOREIGN KEY (tenant_id, knowledge_id, version_id, scope_key) REFERENCES governed_knowledge_versions (tenant_id, knowledge_id, version_id, scope_key)
);
CREATE TABLE IF NOT EXISTS governed_policy_rule_bindings (
 tenant_id text NOT NULL,
 knowledge_id text NOT NULL,
 version_id text NOT NULL,
 pack_id text NOT NULL,
 pack_version text NOT NULL,
 binding bytea NOT NULL,
 PRIMARY KEY (tenant_id, knowledge_id, version_id),
 FOREIGN KEY (tenant_id, knowledge_id, version_id) REFERENCES governed_knowledge_versions (tenant_id, knowledge_id, version_id),
 FOREIGN KEY (tenant_id, pack_id, pack_version) REFERENCES rule_pack_versions (tenant_id, pack_id, version)
);
CREATE TABLE IF NOT EXISTS governed_knowledge_history (
 seq bigserial PRIMARY KEY,
 tenant_id text NOT NULL,
 knowledge_id text NOT NULL,
 version_id text NOT NULL,
 action text NOT NULL CHECK (action IN ('create','submit','validate','approve','publish','withdraw','rollback')),
 revision bigint NOT NULL CHECK (revision > 0),
 pointer_revision bigint NOT NULL CHECK (pointer_revision >= 0),
 actor_id text NOT NULL,
 reason text NOT NULL,
 content_sha256 text NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
 previous_version_id text NOT NULL DEFAULT '',
 previous_active boolean NOT NULL DEFAULT false,
 resulting_version_id text NOT NULL DEFAULT '',
 resulting_active boolean NOT NULL DEFAULT false,
 occurred_at timestamptz NOT NULL,
 FOREIGN KEY (tenant_id, knowledge_id, version_id) REFERENCES governed_knowledge_versions (tenant_id, knowledge_id, version_id)
);
CREATE INDEX IF NOT EXISTS governed_knowledge_history_order ON governed_knowledge_history (tenant_id, knowledge_id, seq);
CREATE TABLE IF NOT EXISTS governed_knowledge_idempotency (
 tenant_id text NOT NULL,
 actor_id text NOT NULL,
 idem_key text NOT NULL,
 request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
 response bytea NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id, actor_id, idem_key)
);
CREATE OR REPLACE FUNCTION guard_governed_knowledge_pointer() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN RAISE EXCEPTION 'knowledge pointer tombstone must be retained'; END IF;
 IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.knowledge_id IS DISTINCT FROM OLD.knowledge_id OR
    NEW.scope_key IS DISTINCT FROM OLD.scope_key OR NEW.pointer_revision <> OLD.pointer_revision + 1 THEN
   RAISE EXCEPTION 'knowledge pointer revision or identity invalid';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS governed_knowledge_pointer_guard ON governed_knowledge_pointers;
CREATE TRIGGER governed_knowledge_pointer_guard BEFORE UPDATE OR DELETE ON governed_knowledge_pointers FOR EACH ROW EXECUTE FUNCTION guard_governed_knowledge_pointer();
CREATE OR REPLACE FUNCTION guard_governed_knowledge_state() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN RAISE EXCEPTION 'knowledge state cannot be deleted'; END IF;
 IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.knowledge_id IS DISTINCT FROM OLD.knowledge_id OR
    NEW.version_id IS DISTINCT FROM OLD.version_id OR NEW.revision <> OLD.revision + 1 THEN
   RAISE EXCEPTION 'knowledge revision or identity invalid';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS governed_knowledge_state_guard ON governed_knowledge_state;
CREATE TRIGGER governed_knowledge_state_guard BEFORE UPDATE OR DELETE ON governed_knowledge_state FOR EACH ROW EXECUTE FUNCTION guard_governed_knowledge_state();
DROP TRIGGER IF EXISTS governed_knowledge_versions_append_only ON governed_knowledge_versions;
CREATE TRIGGER governed_knowledge_versions_append_only BEFORE UPDATE OR DELETE ON governed_knowledge_versions FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS governed_policy_rule_bindings_append_only ON governed_policy_rule_bindings;
CREATE TRIGGER governed_policy_rule_bindings_append_only BEFORE UPDATE OR DELETE ON governed_policy_rule_bindings FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS governed_knowledge_history_append_only ON governed_knowledge_history;
CREATE TRIGGER governed_knowledge_history_append_only BEFORE UPDATE OR DELETE ON governed_knowledge_history FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS governed_knowledge_idempotency_append_only ON governed_knowledge_idempotency;
CREATE TRIGGER governed_knowledge_idempotency_append_only BEFORE UPDATE OR DELETE ON governed_knowledge_idempotency FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
;


ALTER TABLE governed_knowledge_versions DROP CONSTRAINT IF EXISTS governed_knowledge_versions_kind_check;
ALTER TABLE governed_knowledge_versions ADD CONSTRAINT governed_knowledge_versions_kind_check
 CHECK (kind IN ('policy','guidance','exception','experience','legal')) NOT VALID;
ALTER TABLE governed_knowledge_versions VALIDATE CONSTRAINT governed_knowledge_versions_kind_check;
CREATE TABLE IF NOT EXISTS legal_source_captures (
 tenant_id text NOT NULL,
 capture_snapshot_id text NOT NULL,
 source_id text NOT NULL,
 norm_version text NOT NULL,
	 norm_title text NOT NULL,
 document_type text NOT NULL CHECK (document_type IN ('law','administrative_regulation','judicial_interpretation','normative_notice','draft','legislative_plan')),
 publisher text NOT NULL,
 issuer text NOT NULL,
 official_url text NOT NULL,
 provenance text NOT NULL CHECK (provenance='verified_official_capture'),
 source_captured_at timestamptz NOT NULL,
	 promulgated_on date NOT NULL,
	 effective_from date NOT NULL,
	 effective_until date,
 response_status integer NOT NULL CHECK (response_status=200),
 media_type text NOT NULL,
 raw_response_bytes bytea NOT NULL CHECK (octet_length(raw_response_bytes)>0),
 raw_response_sha256 text NOT NULL CHECK (raw_response_sha256 ~ '^[0-9a-f]{64}$'),
 extracted_full_text text NOT NULL CHECK (length(extracted_full_text)>0),
 extracted_full_text_sha256 text NOT NULL CHECK (extracted_full_text_sha256 ~ '^[0-9a-f]{64}$'),
 extractor_id text NOT NULL,
 extractor_version text NOT NULL,
 canonicalization_version text NOT NULL,
 status_observation_id text NOT NULL,
 observed_status text NOT NULL CHECK (observed_status IN ('effective','repealed','not_yet_effective')),
 status_registry_url text NOT NULL,
 status_request_method text NOT NULL CHECK (status_request_method IN ('GET','POST')),
 status_request_body bytea NOT NULL DEFAULT ''::bytea,
 status_observed_at timestamptz NOT NULL,
 status_response_status integer NOT NULL CHECK (status_response_status=200),
 status_media_type text NOT NULL,
 status_response_bytes bytea NOT NULL CHECK (octet_length(status_response_bytes)>0),
 status_response_sha256 text NOT NULL CHECK (status_response_sha256 ~ '^[0-9a-f]{64}$'),
 status_mapping_evidence jsonb NOT NULL,
 article_manifest jsonb NOT NULL,
	 article_manifest_sha256 text NOT NULL CHECK (article_manifest_sha256 ~ '^[0-9a-f]{64}$'),
 request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
 idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
 imported_article_refs jsonb NOT NULL,
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,capture_snapshot_id),
 UNIQUE (tenant_id,created_by,idempotency_key)
);
CREATE INDEX IF NOT EXISTS legal_source_captures_source_time ON legal_source_captures (tenant_id,source_id,norm_version,source_captured_at DESC,capture_snapshot_id);
CREATE OR REPLACE FUNCTION guard_legal_source_capture() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'legal source captures are append-only';
END $$;
DROP TRIGGER IF EXISTS legal_source_captures_append_only ON legal_source_captures;
CREATE TRIGGER legal_source_captures_append_only BEFORE UPDATE OR DELETE ON legal_source_captures FOR EACH ROW EXECUTE FUNCTION guard_legal_source_capture();
ALTER TABLE governed_knowledge_versions ADD COLUMN IF NOT EXISTS legal_capture_snapshot_id text;
ALTER TABLE governed_knowledge_versions DROP CONSTRAINT IF EXISTS governed_knowledge_versions_legal_capture_fk;
ALTER TABLE governed_knowledge_versions ADD CONSTRAINT governed_knowledge_versions_legal_capture_fk
 FOREIGN KEY (tenant_id,legal_capture_snapshot_id) REFERENCES legal_source_captures (tenant_id,capture_snapshot_id) NOT VALID;
ALTER TABLE governed_knowledge_versions VALIDATE CONSTRAINT governed_knowledge_versions_legal_capture_fk;
ALTER TABLE governed_knowledge_versions DROP CONSTRAINT IF EXISTS governed_knowledge_versions_legal_capture_check;
ALTER TABLE governed_knowledge_versions ADD CONSTRAINT governed_knowledge_versions_legal_capture_check
 CHECK ((kind='legal' AND legal_capture_snapshot_id IS NOT NULL) OR (kind<>'legal' AND legal_capture_snapshot_id IS NULL)) NOT VALID;
ALTER TABLE governed_knowledge_versions VALIDATE CONSTRAINT governed_knowledge_versions_legal_capture_check;
;


ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS governed_knowledge bytea;
;


CREATE UNIQUE INDEX IF NOT EXISTS evidence_request_run_scope ON review_runs (tenant_id,org_id,case_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS evidence_request_attempt_scope ON review_attempts (tenant_id,org_id,run_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS evidence_request_evaluation_scope ON constraint_evaluations (tenant_id,org_id,case_id,run_id,attempt_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS evidence_request_document_scope ON documents (tenant_id,org_id,case_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS evidence_request_task_scope ON business_workbench_tasks (tenant_id,org_id,case_id,id);
CREATE TABLE IF NOT EXISTS evidence_requests (
 tenant_id text NOT NULL, org_id text NOT NULL, case_id text NOT NULL, request_id text NOT NULL,
 task_id text NOT NULL, source_run_id text NOT NULL, source_attempt_id text NOT NULL, source_evaluation_id text, source_gap_id text,
 status text NOT NULL CHECK (status IN ('open','received','verified')),
 version integer NOT NULL CHECK (version > 0), record_json jsonb NOT NULL,
 CHECK ((source_evaluation_id IS NULL) <> (source_gap_id IS NULL)),
 PRIMARY KEY (tenant_id,org_id,case_id,request_id),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases (tenant_id,org_id,id),
 FOREIGN KEY (tenant_id,org_id,case_id,task_id) REFERENCES business_workbench_tasks (tenant_id,org_id,case_id,id),
 FOREIGN KEY (tenant_id,org_id,case_id,source_run_id) REFERENCES review_runs (tenant_id,org_id,case_id,id),
 FOREIGN KEY (tenant_id,org_id,source_run_id,source_attempt_id) REFERENCES review_attempts (tenant_id,org_id,run_id,id),
 FOREIGN KEY (tenant_id,org_id,case_id,source_run_id,source_attempt_id,source_evaluation_id)
   REFERENCES constraint_evaluations (tenant_id,org_id,case_id,run_id,attempt_id,id)
);
CREATE INDEX IF NOT EXISTS evidence_requests_case ON evidence_requests (tenant_id,org_id,case_id,request_id);
CREATE TABLE IF NOT EXISTS evidence_receipts (
 tenant_id text NOT NULL, org_id text NOT NULL, case_id text NOT NULL, request_id text NOT NULL, receipt_id text NOT NULL,
 document_id text, record_json jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,request_id,receipt_id),
 FOREIGN KEY (tenant_id,org_id,case_id,request_id) REFERENCES evidence_requests (tenant_id,org_id,case_id,request_id),
 FOREIGN KEY (tenant_id,org_id,case_id,document_id) REFERENCES documents (tenant_id,org_id,case_id,id)
);
CREATE TABLE IF NOT EXISTS evidence_request_events (
 tenant_id text NOT NULL, org_id text NOT NULL, case_id text NOT NULL, request_id text NOT NULL,
 version integer NOT NULL CHECK (version > 0), event_type text NOT NULL CHECK (event_type IN ('created','received','verified')),
 actor_id text NOT NULL, idempotency_key text NOT NULL, request_sha256 text NOT NULL,
 record_json jsonb NOT NULL, detail_json jsonb NOT NULL, occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,request_id,version),
 UNIQUE (tenant_id,org_id,case_id,actor_id,event_type,idempotency_key),
 FOREIGN KEY (tenant_id,org_id,case_id,request_id) REFERENCES evidence_requests (tenant_id,org_id,case_id,request_id)
);
CREATE OR REPLACE FUNCTION guard_evidence_request_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN RAISE EXCEPTION 'evidence request cannot be deleted'; END IF;
 IF (NEW.tenant_id,NEW.org_id,NEW.case_id,NEW.request_id,NEW.task_id,NEW.source_run_id,NEW.source_attempt_id,NEW.source_evaluation_id,NEW.source_gap_id)
   IS DISTINCT FROM (OLD.tenant_id,OLD.org_id,OLD.case_id,OLD.request_id,OLD.task_id,OLD.source_run_id,OLD.source_attempt_id,OLD.source_evaluation_id,OLD.source_gap_id)
   OR NEW.version <> OLD.version + 1 OR OLD.status = 'verified'
   OR NEW.status NOT IN ('received','verified')
   OR (NEW.status = 'verified' AND OLD.status <> 'received')
   OR (NEW.status = 'verified' AND NOT (NEW.record_json ? 'verification'))
   OR (NEW.status = 'received' AND NEW.record_json ? 'verification')
   OR NEW.record_json->>'status' IS DISTINCT FROM NEW.status
   OR NEW.record_json->>'version' IS DISTINCT FROM NEW.version::text
   OR (NEW.record_json - ARRAY['status','version','updated_at','verification'])
      IS DISTINCT FROM (OLD.record_json - ARRAY['status','version','updated_at','verification']) THEN
  RAISE EXCEPTION 'invalid evidence request transition';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS evidence_requests_update_guard ON evidence_requests;
CREATE TRIGGER evidence_requests_update_guard BEFORE UPDATE OR DELETE ON evidence_requests FOR EACH ROW EXECUTE FUNCTION guard_evidence_request_update();
DROP TRIGGER IF EXISTS evidence_receipts_append_only ON evidence_receipts;
CREATE TRIGGER evidence_receipts_append_only BEFORE UPDATE OR DELETE ON evidence_receipts FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS evidence_request_events_append_only ON evidence_request_events;
CREATE TRIGGER evidence_request_events_append_only BEFORE UPDATE OR DELETE ON evidence_request_events FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
;


ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS evidence jsonb;
;

CREATE TABLE IF NOT EXISTS llm_endpoint_settings (
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
	);


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
;


CREATE TABLE IF NOT EXISTS advisory_judgments (
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	run_id text NOT NULL,
	attempt_id text NOT NULL,
	judgment_id text NOT NULL CHECK (judgment_id ~ '^[0-9a-f]{64}$'),
	feature text NOT NULL,
	question_version text NOT NULL,
	configuration_id text NOT NULL CHECK (configuration_id ~ '^[0-9a-f]{64}$'),
	provider text NOT NULL,
	model_requested text NOT NULL,
	model_observed text NOT NULL,
	frozen_input_sha256 text NOT NULL CHECK (frozen_input_sha256 ~ '^[0-9a-f]{64}$'),
	snapshot_sha256 text NOT NULL CHECK (snapshot_sha256 ~ '^[0-9a-f]{64}$'),
	request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
	state_sha256 text NOT NULL CHECK (state_sha256 ~ '^[0-9a-f]{64}$'),
	data_classification text NOT NULL,
	status text NOT NULL CHECK (status = 'completed'),
	error_code text NOT NULL DEFAULT '',
	response_json bytea NOT NULL,
	usage_json bytea NOT NULL,
	created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
	PRIMARY KEY (tenant_id, org_id, judgment_id),
	FOREIGN KEY (tenant_id, org_id, case_id, run_id) REFERENCES review_runs (tenant_id, org_id, case_id, id),
	FOREIGN KEY (tenant_id, org_id, run_id, attempt_id) REFERENCES review_attempts (tenant_id, org_id, run_id, id)
);
CREATE INDEX IF NOT EXISTS advisory_judgments_run ON advisory_judgments (tenant_id, org_id, run_id);
DROP TRIGGER IF EXISTS advisory_judgments_append_only ON advisory_judgments;
CREATE TRIGGER advisory_judgments_append_only BEFORE UPDATE OR DELETE ON advisory_judgments
	FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
;


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

-- Mutable fencing record for one external read request. Connector I/O happens
-- outside the owning transaction; only the current generation may publish a
-- durable snapshot result.
CREATE TABLE IF NOT EXISTS operator_external_collection_claims (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 case_id text NOT NULL,
 actor_id text NOT NULL,
 idempotency_key text NOT NULL,
	collection_id text NOT NULL,
 request_sha256 text NOT NULL,
 generation bigint NOT NULL CHECK (generation > 0),
 lease_owner text,
 lease_until timestamptz,
 status text NOT NULL CHECK (status IN ('in_progress','retryable','completed')),
 result_snapshot_set_id text,
 response_status integer,
 response_body bytea,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,actor_id,idempotency_key),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases (tenant_id,org_id,id),
 FOREIGN KEY (tenant_id,org_id,case_id,result_snapshot_set_id)
   REFERENCES operator_external_snapshot_sets (tenant_id,org_id,case_id,snapshot_set_id),
 CHECK ((status='in_progress' AND lease_owner IS NOT NULL AND lease_until IS NOT NULL
         AND result_snapshot_set_id IS NULL AND response_status IS NULL AND response_body IS NULL)
     OR (status='retryable' AND lease_owner IS NULL AND lease_until IS NULL
         AND result_snapshot_set_id IS NULL AND response_status IS NULL AND response_body IS NULL)
     OR (status='completed' AND lease_owner IS NULL AND lease_until IS NULL
         AND result_snapshot_set_id IS NOT NULL AND response_status IS NOT NULL AND response_body IS NOT NULL))
);
ALTER TABLE operator_external_collection_claims ADD COLUMN IF NOT EXISTS collection_id text;
UPDATE operator_external_collection_claims SET collection_id='col_' || md5(tenant_id || ':' || org_id || ':' || case_id || ':' || actor_id || ':' || idempotency_key)
 WHERE collection_id IS NULL OR collection_id='';
ALTER TABLE operator_external_collection_claims ALTER COLUMN collection_id SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS operator_external_collection_claims_collection_id_uq
 ON operator_external_collection_claims (tenant_id,org_id,case_id,collection_id);
CREATE INDEX IF NOT EXISTS operator_external_collection_claims_lease
 ON operator_external_collection_claims (tenant_id,org_id,case_id,status,lease_until);

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
;

DO $$
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
			'operator_object_bindings','operator_external_snapshot_sets','operator_external_collection_claims','operator_external_observations',
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
	END $$;
