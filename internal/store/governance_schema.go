package store

// Governance tables are Core-owned. The core service writes the lifecycle and
// the active pointer; the execution and worker roles only SELECT the published
// version an acceptance transaction freezes, so governance write authority never
// leaves the core service role.
const governanceSchemaSQL = `
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
`
