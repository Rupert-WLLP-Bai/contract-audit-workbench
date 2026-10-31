-- Each row records an explicit reviewer reference before the first decision on
-- the finding. Existing candidate selection and retrieval are not backfilled.
CREATE TABLE experience_finding_usage (
    tenant_id text NOT NULL,
    org_id text NOT NULL,
    case_id text NOT NULL,
    run_id text NOT NULL,
    attempt_id text NOT NULL,
    finding_id text NOT NULL,
    knowledge_id text NOT NULL,
    version_id text NOT NULL CHECK (version_id ~ '^[0-9a-f]{64}$'),
    content_sha256 text NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    snapshot_sha256 text NOT NULL CHECK (snapshot_sha256 ~ '^[0-9a-f]{64}$'),
    input_sha256 text NOT NULL CHECK (input_sha256 ~ '^[0-9a-f]{64}$'),
    expected_action text NOT NULL CHECK (expected_action IN ('', 'confirm_risk', 'false_positive')),
    recorded_by text NOT NULL,
    usage_sha256 text NOT NULL CHECK (usage_sha256 ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, attempt_id, knowledge_id, version_id, finding_id),
    FOREIGN KEY (tenant_id,org_id,case_id,run_id,attempt_id,finding_id)
        REFERENCES findings (tenant_id,org_id,case_id,run_id,attempt_id,id),
    FOREIGN KEY (tenant_id,knowledge_id,version_id)
        REFERENCES governed_knowledge_versions (tenant_id,knowledge_id,version_id)
);
CREATE INDEX experience_finding_usage_version ON experience_finding_usage
    (tenant_id,knowledge_id,version_id,recorded_at DESC,attempt_id,finding_id);
CREATE TRIGGER experience_finding_usage_append_only BEFORE UPDATE OR DELETE ON experience_finding_usage
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
ALTER TABLE experience_finding_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE experience_finding_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON experience_finding_usage
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
