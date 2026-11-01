-- CLM-93 introduces server-authenticated, append-only external contract source
-- observations. It preserves raw payload bytes as object-bound ciphertext; only
-- the typed, sanitized and separately sealed snapshot can become a revision.
ALTER TABLE clm_contracts DROP CONSTRAINT clm_contracts_lifecycle_state_check;
ALTER TABLE clm_contracts ADD CONSTRAINT clm_contracts_lifecycle_state_check
    CHECK (lifecycle_state IN ('draft','ready_to_sign','registered','performing','closed','terminated','external_observed'));
ALTER TABLE clm_contracts ADD CONSTRAINT clm_contract_external_observed_state
    CHECK (lifecycle_state <> 'external_observed' OR authority_mode = 'external');
ALTER TABLE clm_contracts DROP CONSTRAINT clm_contracts_check;
ALTER TABLE clm_contracts ADD CONSTRAINT clm_contract_external_identity_check
    CHECK (authority_mode <> 'external' OR
        (length(btrim(COALESCE(source_system, ''))) > 0
         AND length(btrim(COALESCE(external_contract_id, ''))) > 0
         AND (length(btrim(COALESCE(source_version, ''))) > 0 OR
              (source_version IS NULL AND lifecycle_state = 'external_observed' AND current_revision_id IS NULL))));

CREATE TABLE clm_contract_source_observations (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    observation_id uuid NOT NULL,
    contract_id uuid NOT NULL,
    source_id text NOT NULL CHECK (length(btrim(source_id)) BETWEEN 1 AND 200),
    source_kind text NOT NULL CHECK (source_kind IN ('mock','real')),
    source_key_id text NOT NULL CHECK (length(btrim(source_key_id)) BETWEEN 1 AND 200),
    external_contract_id text NOT NULL CHECK (length(btrim(external_contract_id)) BETWEEN 1 AND 200),
    source_event_id text NOT NULL CHECK (length(btrim(source_event_id)) BETWEEN 1 AND 300),
    source_version text NOT NULL CHECK (length(btrim(source_version)) BETWEEN 1 AND 200),
    current_source_version text CHECK (current_source_version IS NULL OR length(btrim(current_source_version)) BETWEEN 1 AND 200),
    source_sequence bigint CHECK (source_sequence IS NULL OR source_sequence > 0),
    payload_digest text NOT NULL CHECK (payload_digest ~ '^[a-f0-9]{64}$'),
    source_snapshot_digest text NOT NULL CHECK (source_snapshot_digest ~ '^[a-f0-9]{64}$'),
    raw_payload_digest text NOT NULL CHECK (raw_payload_digest ~ '^[a-f0-9]{64}$'),
    raw_payload_ciphertext text NOT NULL CHECK (length(raw_payload_ciphertext) > 0),
    source_observed_state text NOT NULL CHECK (length(btrim(source_observed_state)) BETWEEN 1 AND 200),
    disposition text NOT NULL CHECK (disposition IN ('applied','observation_only','unknown')),
    contract_revision_id uuid,
    case_org_id text NOT NULL CHECK (length(btrim(case_org_id)) > 0),
    case_id text NOT NULL CHECK (length(btrim(case_id)) > 0),
    source_occurred_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, observation_id),
    UNIQUE (tenant_id, source_id, source_event_id),
    FOREIGN KEY (tenant_id, contract_id) REFERENCES clm_contracts (tenant_id, contract_id),
    FOREIGN KEY (tenant_id, contract_id, contract_revision_id)
        REFERENCES clm_contract_revisions (tenant_id, contract_id, contract_revision_id),
    FOREIGN KEY (tenant_id, contract_id, case_org_id, case_id)
        REFERENCES clm_contract_cases (tenant_id, contract_id, case_org_id, case_id),
    CHECK ((disposition = 'applied' AND contract_revision_id IS NOT NULL)
        OR (disposition <> 'applied' AND contract_revision_id IS NULL))
);
CREATE TRIGGER clm_contract_source_observations_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_source_observations
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
CREATE INDEX clm_contract_source_observations_history
    ON clm_contract_source_observations (tenant_id, contract_id, recorded_at DESC, observation_id DESC);
CREATE INDEX clm_contract_source_observations_version
    ON clm_contract_source_observations (tenant_id, source_id, external_contract_id, source_version);
CREATE INDEX clm_contract_source_observations_sequence
    ON clm_contract_source_observations (tenant_id, source_id, external_contract_id, source_sequence DESC, recorded_at DESC);

REVOKE ALL ON TABLE clm_contract_source_observations FROM PUBLIC;
ALTER TABLE clm_contract_source_observations ENABLE ROW LEVEL SECURITY;
ALTER TABLE clm_contract_source_observations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_boundary ON clm_contract_source_observations
    USING (tenant_id=current_setting('app.tenant_id',true))
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true));
