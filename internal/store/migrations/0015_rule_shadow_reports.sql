-- Typed execution inputs belong to execution; reports belong to core. Neither
-- table grants the shadow runner authority over a review or a release pointer.
CREATE TABLE IF NOT EXISTS review_rulepack_inputs (
 tenant_id text NOT NULL, org_id text NOT NULL, case_id text NOT NULL,
 run_id text NOT NULL, attempt_id text NOT NULL,
 attempt_input_sha256 text NOT NULL CHECK (attempt_input_sha256 ~ '^[0-9a-f]{64}$'),
 pack_id text NOT NULL, pack_version text NOT NULL,
 pack_content_sha256 text NOT NULL CHECK (pack_content_sha256 ~ '^[0-9a-f]{64}$'),
 pack_snapshot_sha256 text NOT NULL CHECK (pack_snapshot_sha256 ~ '^[0-9a-f]{64}$'),
 extractor_ref text NOT NULL CHECK (extractor_ref <> ''),
 request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
 request_bytes bytea NOT NULL CHECK (octet_length(request_bytes) > 0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,attempt_id),
 FOREIGN KEY (tenant_id,org_id,case_id,run_id) REFERENCES review_runs (tenant_id,org_id,case_id,id),
 FOREIGN KEY (tenant_id,org_id,run_id,attempt_id) REFERENCES review_attempts (tenant_id,org_id,run_id,id)
);
CREATE INDEX IF NOT EXISTS review_rulepack_inputs_run ON review_rulepack_inputs (tenant_id,org_id,case_id,run_id,attempt_id);

CREATE TABLE IF NOT EXISTS rule_pack_shadow_reports (
 tenant_id text NOT NULL, org_id text NOT NULL, report_id text NOT NULL,
 pack_id text NOT NULL, pack_version text NOT NULL, pack_revision bigint NOT NULL CHECK (pack_revision > 0),
 pack_content_sha256 text NOT NULL CHECK (pack_content_sha256 ~ '^[0-9a-f]{64}$'),
 pack_snapshot_sha256 text NOT NULL CHECK (pack_snapshot_sha256 ~ '^[0-9a-f]{64}$'),
 pack_snapshot bytea NOT NULL CHECK (octet_length(pack_snapshot) > 0),
 report_sha256 text NOT NULL CHECK (report_sha256 ~ '^[0-9a-f]{64}$'),
 report_bytes bytea NOT NULL CHECK (octet_length(report_bytes) > 0),
 created_by text NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,report_id),
 FOREIGN KEY (tenant_id,pack_id,pack_version) REFERENCES rule_pack_versions (tenant_id,pack_id,version)
);
CREATE INDEX IF NOT EXISTS rule_pack_shadow_reports_version ON rule_pack_shadow_reports (tenant_id,pack_id,pack_version,created_at DESC,report_id);

CREATE TRIGGER review_rulepack_inputs_append_only BEFORE UPDATE OR DELETE ON review_rulepack_inputs FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
CREATE TRIGGER rule_pack_shadow_reports_append_only BEFORE UPDATE OR DELETE ON rule_pack_shadow_reports FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
ALTER TABLE review_rulepack_inputs ENABLE ROW LEVEL SECURITY;
ALTER TABLE review_rulepack_inputs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON review_rulepack_inputs USING (tenant_id=current_setting('app.tenant_id',true)) WITH CHECK (tenant_id=current_setting('app.tenant_id',true));
ALTER TABLE rule_pack_shadow_reports ENABLE ROW LEVEL SECURITY;
ALTER TABLE rule_pack_shadow_reports FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON rule_pack_shadow_reports USING (tenant_id=current_setting('app.tenant_id',true)) WITH CHECK (tenant_id=current_setting('app.tenant_id',true));
