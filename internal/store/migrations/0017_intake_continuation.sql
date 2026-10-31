-- Original intake commands and personal archive facts are append-only. Existing
-- frozen operation identities and public completed receipt bytes are unchanged.
CREATE TABLE case_intake_requests (
    tenant_id text NOT NULL,
    operation_id text NOT NULL,
    schema_version smallint NOT NULL DEFAULT 1 CHECK (schema_version=1),
    request_json jsonb NOT NULL CHECK (jsonb_typeof(request_json)='object'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,operation_id),
    FOREIGN KEY (tenant_id,operation_id)
        REFERENCES case_intake_operations(tenant_id,operation_id)
);
CREATE TRIGGER case_intake_requests_append_only BEFORE UPDATE OR DELETE ON case_intake_requests
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
ALTER TABLE case_intake_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE case_intake_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON case_intake_requests
    USING (tenant_id=current_setting('app.tenant_id',true))
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true));

CREATE TABLE case_intake_archives (
    tenant_id text NOT NULL,
    operation_id text NOT NULL,
    archived_by text NOT NULL,
    archived_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,operation_id),
    FOREIGN KEY (tenant_id,operation_id)
        REFERENCES case_intake_operations(tenant_id,operation_id)
);
CREATE TRIGGER case_intake_archives_append_only BEFORE UPDATE OR DELETE ON case_intake_archives
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
ALTER TABLE case_intake_archives ENABLE ROW LEVEL SECURITY;
ALTER TABLE case_intake_archives FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON case_intake_archives
    USING (tenant_id=current_setting('app.tenant_id',true))
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true));

CREATE INDEX case_intake_pending_owner ON case_intake_operations
    (tenant_id,actor_id,created_at DESC,operation_id DESC) WHERE status='pending';
