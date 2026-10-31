CREATE TABLE IF NOT EXISTS case_intake_operations (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 operation_id text NOT NULL,
 actor_id text NOT NULL,
 idempotency_key text NOT NULL,
 request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
 case_id text NOT NULL,
 upload_idempotency_key text NOT NULL,
 status text NOT NULL CHECK (status IN ('pending','completed')),
 response_status integer,
 response_body bytea,
 response_digest text,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id, operation_id),
 UNIQUE (tenant_id, actor_id, idempotency_key),
 UNIQUE (tenant_id, org_id, upload_idempotency_key),
 FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases(tenant_id,org_id,id) DEFERRABLE INITIALLY DEFERRED,
 CHECK (response_digest IS NULL OR response_digest ~ '^[0-9a-f]{64}$'),
 CHECK ((status='pending' AND response_status IS NULL AND response_body IS NULL AND response_digest IS NULL)
     OR (status='completed' AND response_status=201 AND response_body IS NOT NULL AND response_digest IS NOT NULL
         AND response_digest=encode(sha256(response_body),'hex')))
);
CREATE OR REPLACE FUNCTION guard_case_intake_operation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN
  RAISE EXCEPTION 'case intake operations are immutable' USING ERRCODE='P0001';
 END IF;
 IF ROW(NEW.tenant_id,NEW.org_id,NEW.operation_id,NEW.actor_id,NEW.idempotency_key,NEW.request_digest,NEW.case_id,NEW.upload_idempotency_key,NEW.created_at)
    IS DISTINCT FROM ROW(OLD.tenant_id,OLD.org_id,OLD.operation_id,OLD.actor_id,OLD.idempotency_key,OLD.request_digest,OLD.case_id,OLD.upload_idempotency_key,OLD.created_at) THEN
  RAISE EXCEPTION 'case intake operation identity is immutable' USING ERRCODE='P0001';
 END IF;
 IF OLD.status <> 'pending' OR NEW.status <> 'completed' OR OLD.response_status IS NOT NULL OR OLD.response_body IS NOT NULL OR OLD.response_digest IS NOT NULL THEN
  RAISE EXCEPTION 'case intake operation is terminal or transition is invalid' USING ERRCODE='P0001';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS case_intake_operation_guard ON case_intake_operations;
CREATE TRIGGER case_intake_operation_guard BEFORE UPDATE OR DELETE ON case_intake_operations
 FOR EACH ROW EXECUTE FUNCTION guard_case_intake_operation();
ALTER TABLE case_intake_operations ENABLE ROW LEVEL SECURITY;
ALTER TABLE case_intake_operations FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_boundary ON case_intake_operations;
CREATE POLICY tenant_boundary ON case_intake_operations
 USING (tenant_id = current_setting('app.tenant_id', true))
 WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
