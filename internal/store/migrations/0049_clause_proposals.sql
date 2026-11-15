-- Proposal identity and evidence are immutable; decisions are append-only.
CREATE TABLE clm_clause_proposals (
 tenant_id text NOT NULL, org_id text NOT NULL, case_id text NOT NULL,
 id text NOT NULL, run_id text NOT NULL, attempt_id text NOT NULL,
 actor_id text NOT NULL, idempotency_key text NOT NULL, request_sha256 text NOT NULL,
 proposal_json jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,id), UNIQUE (tenant_id,actor_id,idempotency_key),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases(tenant_id,org_id,id),
 FOREIGN KEY (tenant_id,org_id,case_id,run_id) REFERENCES review_runs(tenant_id,org_id,case_id,id),
 FOREIGN KEY (tenant_id,org_id,run_id,attempt_id) REFERENCES review_attempts(tenant_id,org_id,run_id,id)
);
CREATE INDEX clm_clause_proposals_case ON clm_clause_proposals(tenant_id,org_id,case_id,created_at,id);
CREATE TABLE clm_clause_proposal_decisions (
 tenant_id text NOT NULL, org_id text NOT NULL, proposal_id text NOT NULL,
 version bigint NOT NULL CHECK (version > 1), decision_json jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,proposal_id,version),
 FOREIGN KEY (tenant_id,proposal_id) REFERENCES clm_clause_proposals(tenant_id,id)
);
CREATE TABLE clm_clause_proposal_receipts (
 tenant_id text NOT NULL, actor_id text NOT NULL, route_scope text NOT NULL,
 idempotency_key text NOT NULL, request_sha256 text NOT NULL, response_json jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,actor_id,route_scope,idempotency_key)
);
DO $$ DECLARE name text; BEGIN
 FOREACH name IN ARRAY ARRAY['clm_clause_proposals','clm_clause_proposal_decisions','clm_clause_proposal_receipts'] LOOP
  EXECUTE format('CREATE TRIGGER %I BEFORE UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION forbid_append_only()',name || '_append_only',name);
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',name);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',name);
  EXECUTE format('CREATE POLICY tenant_boundary ON %I USING (tenant_id=current_setting(''app.tenant_id'',true)) WITH CHECK (tenant_id=current_setting(''app.tenant_id'',true))',name);
 END LOOP;
END $$;
