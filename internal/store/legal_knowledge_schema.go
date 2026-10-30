package store

// legalKnowledgeSchemaSQL is added after the governed-knowledge tables. Legal
// captures and status observations share one immutable row so a refresh always
// appends a new snapshot identity. Core is the only writer; tenant RLS and
// service grants are registered by the main migration owner.
const legalKnowledgeSchemaSQL = `
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
`
