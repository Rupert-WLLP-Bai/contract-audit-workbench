-- Editable drafts, command receipts and the provenance of imported signed
-- contracts are independent of the frozen audit case and document records.
CREATE TABLE clm_contract_drafts (
    tenant_id text NOT NULL,
    contract_id uuid NOT NULL,
    content jsonb NOT NULL CHECK (jsonb_typeof(content) = 'object'),
    updated_by text NOT NULL CHECK (length(btrim(updated_by)) > 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, contract_id),
    FOREIGN KEY (tenant_id, contract_id) REFERENCES clm_contracts (tenant_id, contract_id)
);

-- Existing signed contracts can have an unknown amount and currency. A native
-- submission must still validate its complete snapshot before freezing it.
ALTER TABLE clm_contract_revisions ALTER COLUMN amount DROP NOT NULL;
ALTER TABLE clm_contract_revisions ALTER COLUMN currency DROP NOT NULL;
ALTER TABLE clm_contract_revisions ADD CONSTRAINT clm_revision_amount_currency_pair
    CHECK ((amount IS NULL) = (currency IS NULL));

CREATE TABLE clm_contract_write_receipts (
    tenant_id text NOT NULL,
    actor_id text NOT NULL,
    route_scope text NOT NULL,
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    request_digest text NOT NULL CHECK (request_digest ~ '^[a-f0-9]{64}$'),
    contract_id uuid NOT NULL,
    response_status integer NOT NULL CHECK (response_status BETWEEN 200 AND 299),
    response_json jsonb NOT NULL CHECK (jsonb_typeof(response_json) = 'object'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, actor_id, route_scope, idempotency_key),
    FOREIGN KEY (tenant_id, contract_id) REFERENCES clm_contracts (tenant_id, contract_id)
);
CREATE TRIGGER clm_contract_write_receipts_append_only BEFORE UPDATE OR DELETE ON clm_contract_write_receipts
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

CREATE TABLE clm_legacy_imports (
    tenant_id text NOT NULL,
    contract_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    source_system text NOT NULL CHECK (length(btrim(source_system)) > 0),
    source_id text NOT NULL CHECK (length(btrim(source_id)) > 0),
    source_version text NOT NULL CHECK (length(btrim(source_version)) > 0),
    case_org_id text NOT NULL,
    case_id text NOT NULL,
    material_id text NOT NULL,
    material_version integer NOT NULL CHECK (material_version > 0),
    material_digest text NOT NULL CHECK (material_digest ~ '^[a-f0-9]{64}$'),
    approval_history_status text NOT NULL CHECK (approval_history_status IN ('unknown', 'evidence_attached')),
    historical_approval_materials jsonb NOT NULL CHECK (jsonb_typeof(historical_approval_materials) = 'array'),
    reason text NOT NULL CHECK (length(btrim(reason)) > 0),
    imported_by text NOT NULL CHECK (length(btrim(imported_by)) > 0),
    imported_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, contract_id),
    FOREIGN KEY (tenant_id, contract_id, contract_revision_id)
        REFERENCES clm_contract_revisions (tenant_id, contract_id, contract_revision_id),
    FOREIGN KEY (tenant_id, contract_id, case_org_id, case_id)
        REFERENCES clm_contract_cases (tenant_id, contract_id, case_org_id, case_id),
    FOREIGN KEY (tenant_id, case_org_id, case_id, material_id, material_version)
        REFERENCES material_versions (tenant_id, org_id, case_id, material_id, version),
    CHECK (approval_history_status <> 'evidence_attached' OR jsonb_array_length(historical_approval_materials) > 0)
);
CREATE TRIGGER clm_legacy_imports_append_only BEFORE UPDATE OR DELETE ON clm_legacy_imports
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

DO $migration$
DECLARE table_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY['clm_contract_drafts', 'clm_contract_write_receipts', 'clm_legacy_imports'] LOOP
        EXECUTE format('REVOKE ALL ON TABLE %I FROM PUBLIC', table_name);
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', table_name);
        EXECUTE format('CREATE POLICY tenant_boundary ON %I USING '
            || '(tenant_id=current_setting(''app.tenant_id'',true)) WITH CHECK '
            || '(tenant_id=current_setting(''app.tenant_id'',true))', table_name);
    END LOOP;
END $migration$;
