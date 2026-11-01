-- CLM-12 binds a completed audit to the exact frozen contract revision that
-- was submitted. The binding is a fact, not a copy of the review: it cites the
-- existing run, attempt, frozen attempt input, frozen revision seal and finding
-- evidence. It never rewrites a run, finding, decision, approval or revision.
--
-- A binding is append-only. A newer revision does not inherit an older
-- disposition: it starts unbound, and the old binding is reported as stale by
-- comparing its revision to clm_contracts.current_revision_id. A blocked gate
-- is recorded with its reasons instead of turning missing evidence or a model
-- failure into an approval.

CREATE TABLE clm_contract_audit_bindings (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    contract_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    binding_id uuid NOT NULL,
    review_run_id text NOT NULL CHECK (length(btrim(review_run_id)) > 0),
    review_attempt_id text NOT NULL CHECK (length(btrim(review_attempt_id)) > 0),
    case_org_id text NOT NULL CHECK (length(btrim(case_org_id)) > 0),
    case_id text NOT NULL CHECK (length(btrim(case_id)) > 0),
    attempt_input_hash text NOT NULL CHECK (length(btrim(attempt_input_hash)) > 0),
    snapshot_digest text NOT NULL CHECK (snapshot_digest ~ '^[a-f0-9]{64}$'),
    materials_digest text NOT NULL CHECK (materials_digest ~ '^[a-f0-9]{64}$'),
    run_state text NOT NULL CHECK (length(btrim(run_state)) > 0),
    attempt_state text NOT NULL CHECK (length(btrim(attempt_state)) > 0),
    gate_state text NOT NULL CHECK (gate_state IN ('blocked', 'needs_human')),
    gate_reasons jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(gate_reasons) = 'array'),
    finding_count integer NOT NULL CHECK (finding_count >= 0),
    verified_finding_count integer NOT NULL CHECK (verified_finding_count >= 0),
    model_failure boolean NOT NULL,
    contract_row_version bigint NOT NULL CHECK (contract_row_version > 0),
    actor_id text NOT NULL CHECK (length(btrim(actor_id)) > 0),
    actor_role text NOT NULL CHECK (length(btrim(actor_role)) > 0),
    bound_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, contract_id, binding_id),
    UNIQUE (tenant_id, contract_id, review_attempt_id),
    FOREIGN KEY (tenant_id, contract_id, contract_revision_id)
        REFERENCES clm_contract_version_seals (tenant_id, contract_id, contract_revision_id),
    FOREIGN KEY (review_run_id, review_attempt_id)
        REFERENCES review_attempt_inputs (run_id, attempt_id),
    FOREIGN KEY (tenant_id, case_org_id, case_id)
        REFERENCES cases (tenant_id, org_id, id)
);
CREATE INDEX clm_contract_audit_binding_latest
    ON clm_contract_audit_bindings (tenant_id, contract_id, contract_revision_id, bound_at DESC, binding_id DESC);
CREATE INDEX clm_contract_audit_binding_attempt
    ON clm_contract_audit_bindings (tenant_id, review_run_id, review_attempt_id);

CREATE TRIGGER clm_contract_audit_bindings_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_audit_bindings
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

DO $migration$
DECLARE table_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY['clm_contract_audit_bindings'] LOOP
        EXECUTE format('REVOKE ALL ON TABLE %I FROM PUBLIC', table_name);
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', table_name);
        EXECUTE format('CREATE POLICY tenant_boundary ON %I USING '
            || '(tenant_id=current_setting(''app.tenant_id'',true)) WITH CHECK '
            || '(tenant_id=current_setting(''app.tenant_id'',true))', table_name);
    END LOOP;
END $migration$;
