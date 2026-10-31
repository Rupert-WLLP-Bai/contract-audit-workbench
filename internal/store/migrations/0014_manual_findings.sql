-- A manual finding is an append-only human record on an existing frozen attempt.
-- It never changes the attempt output, machine findings, or already saved reports.
CREATE UNIQUE INDEX IF NOT EXISTS manual_finding_parse_selection_scope_uq
 ON review_attempt_parse_selections(tenant_id,org_id,run_id,attempt_id,document_id,parsed_document_id);
CREATE UNIQUE INDEX IF NOT EXISTS manual_finding_parsed_document_scope_uq
 ON parsed_documents(tenant_id,org_id,document_id,id);

CREATE TABLE manual_finding_records (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 case_id text NOT NULL,
 run_id text NOT NULL,
 attempt_id text NOT NULL,
 id text NOT NULL,
 actor_id text NOT NULL CHECK (actor_id <> ''),
 actor_name text NOT NULL CHECK (actor_name <> ''),
 claim text NOT NULL CHECK (char_length(claim) BETWEEN 2 AND 1000),
 severity text NOT NULL CHECK (severity IN ('high','medium','low','info')),
 document_id text NOT NULL,
 parsed_document_id text NOT NULL,
 normalized_digest text NOT NULL CHECK (normalized_digest ~ '^[0-9a-f]{64}$'),
 start_offset integer NOT NULL CHECK (start_offset >= 0),
 end_offset integer NOT NULL CHECK (end_offset > start_offset),
 quote text NOT NULL CHECK (char_length(quote) BETWEEN 1 AND 4096 AND char_length(quote) = end_offset - start_offset),
 idempotency_key text NOT NULL CHECK (char_length(idempotency_key) BETWEEN 1 AND 80),
 request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,id),
 UNIQUE (tenant_id,actor_id,idempotency_key),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases(tenant_id,org_id,id),
 FOREIGN KEY (tenant_id,org_id,case_id,run_id) REFERENCES review_runs(tenant_id,org_id,case_id,id),
 FOREIGN KEY (tenant_id,org_id,run_id,attempt_id) REFERENCES review_attempts(tenant_id,org_id,run_id,id),
 FOREIGN KEY (run_id,attempt_id) REFERENCES review_attempt_inputs(run_id,attempt_id),
 FOREIGN KEY (tenant_id,org_id,case_id,document_id) REFERENCES documents(tenant_id,org_id,case_id,id),
 FOREIGN KEY (tenant_id,org_id,document_id,parsed_document_id) REFERENCES parsed_documents(tenant_id,org_id,document_id,id),
 FOREIGN KEY (tenant_id,org_id,run_id,attempt_id,document_id,parsed_document_id)
   REFERENCES review_attempt_parse_selections(tenant_id,org_id,run_id,attempt_id,document_id,parsed_document_id)
);
CREATE INDEX manual_finding_records_attempt ON manual_finding_records
 (tenant_id,org_id,case_id,run_id,attempt_id,created_at,id);
CREATE TRIGGER manual_finding_records_append_only BEFORE UPDATE OR DELETE ON manual_finding_records
 FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
ALTER TABLE manual_finding_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE manual_finding_records FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON manual_finding_records
 USING (tenant_id=current_setting('app.tenant_id',true))
 WITH CHECK (tenant_id=current_setting('app.tenant_id',true));
