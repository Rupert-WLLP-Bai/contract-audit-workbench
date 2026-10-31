-- Receipts belong to the same service and transaction as their effects.
CREATE TABLE core_write_receipts (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 actor_id text NOT NULL,
 route text NOT NULL,
 idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 200),
 request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
 case_id text NOT NULL,
 response_json jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id, actor_id, route, idempotency_key),
 FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE execution_write_receipts (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 actor_id text NOT NULL,
 route text NOT NULL,
 idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 200),
 request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
 case_id text NOT NULL,
 response_json jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id, actor_id, route, idempotency_key),
 FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id) DEFERRABLE INITIALLY DEFERRED
);
ALTER TABLE core_write_receipts ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON TABLE core_write_receipts, execution_write_receipts FROM PUBLIC;
ALTER TABLE core_write_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON core_write_receipts
 USING (tenant_id = current_setting('app.tenant_id', true))
 WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
ALTER TABLE execution_write_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE execution_write_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON execution_write_receipts
 USING (tenant_id = current_setting('app.tenant_id', true))
 WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
CREATE TRIGGER core_write_receipts_append_only BEFORE UPDATE OR DELETE ON core_write_receipts
 FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
CREATE TRIGGER execution_write_receipts_append_only BEFORE UPDATE OR DELETE ON execution_write_receipts
 FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
