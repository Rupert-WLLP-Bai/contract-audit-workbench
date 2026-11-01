-- CLM-13 records actual signing as an append-only fact and links a supplement
-- contract to the exact immutable revision of its parent. It does not create
-- or rewrite approval history, revisions, or signature verification claims.

CREATE TABLE clm_contract_signing_registrations (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    contract_id uuid NOT NULL,
    registration_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    signing_date date NOT NULL CHECK (signing_date >= DATE '1900-01-01'),
    signed_material_id text NOT NULL,
    signed_material_version_id text NOT NULL,
    signed_case_org_id text NOT NULL,
    signed_case_id text NOT NULL,
    signed_material_version integer NOT NULL CHECK (signed_material_version > 0),
    signed_filename text NOT NULL CHECK (length(btrim(signed_filename)) > 0),
    signed_digest text NOT NULL CHECK (signed_digest ~ '^[a-f0-9]{64}$'),
    parties jsonb NOT NULL CHECK (jsonb_typeof(parties) = 'array'
        AND jsonb_array_length(parties) BETWEEN 1 AND 20),
    signature_evidence_status text NOT NULL CHECK (signature_evidence_status IN
        ('unknown', 'document_only', 'external_evidence')),
    signature_evidence_ref text,
    approval_basis text NOT NULL CHECK (approval_basis IN ('approved_round', 'legacy_manual_basis')),
    approval_evidence_status text NOT NULL CHECK (approval_evidence_status IN
        ('approved_round', 'legacy_unknown_with_manual_basis')),
    approval_round_id uuid,
    comparison_method text NOT NULL CHECK (comparison_method = 'structured_signed_terms/v1'),
    comparison_result text NOT NULL CHECK (comparison_result = 'human_transcription_confirmed'),
    verification_status text NOT NULL CHECK (verification_status = 'human_transcription_confirmed'),
    comparison_confirmed_by text NOT NULL CHECK (length(btrim(comparison_confirmed_by)) > 0),
    comparison_confirmed_at timestamptz NOT NULL,
    comparison_scope text NOT NULL CHECK (comparison_scope = 'all_structured_business_fields_and_restricted_fields'),
    approval_basis_note text NOT NULL DEFAULT '' CHECK (length(approval_basis_note) <= 2000),
    variance jsonb NOT NULL CHECK (jsonb_typeof(variance) = 'object'),
    signed_terms jsonb NOT NULL CHECK (jsonb_typeof(signed_terms) = 'object'),
    request_digest text NOT NULL CHECK (request_digest ~ '^[a-f0-9]{64}$'),
    registered_by text NOT NULL CHECK (length(btrim(registered_by)) > 0),
    registered_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, contract_id, registration_id),
    UNIQUE (tenant_id, contract_id),
    FOREIGN KEY (tenant_id, contract_id)
        REFERENCES clm_contracts (tenant_id, contract_id),
    FOREIGN KEY (tenant_id, contract_id, contract_revision_id)
        REFERENCES clm_contract_revisions (tenant_id, contract_id, contract_revision_id),
    FOREIGN KEY (tenant_id, signed_case_org_id, signed_case_id,
        signed_material_id, signed_material_version)
        REFERENCES material_versions (tenant_id, org_id, case_id, material_id, version),
    FOREIGN KEY (tenant_id, signed_case_org_id, signed_case_id, signed_material_version_id)
        REFERENCES documents (tenant_id, org_id, case_id, id),
    FOREIGN KEY (tenant_id, contract_id, approval_round_id, contract_revision_id)
        REFERENCES clm_approval_rounds (tenant_id, contract_id, approval_round_id, contract_revision_id),
    CHECK ((signature_evidence_status = 'external_evidence'
            AND signature_evidence_ref IS NOT NULL AND length(btrim(signature_evidence_ref)) > 0)
        OR (signature_evidence_status <> 'external_evidence' AND signature_evidence_ref IS NULL)),
    CHECK ((approval_basis = 'approved_round' AND approval_evidence_status = 'approved_round'
            AND approval_round_id IS NOT NULL)
        OR (approval_basis = 'legacy_manual_basis' AND approval_evidence_status = 'legacy_unknown_with_manual_basis'
            AND approval_round_id IS NULL AND length(btrim(approval_basis_note)) > 0))
);

CREATE OR REPLACE FUNCTION guard_clm_signing_registration_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM clm_contracts contract
        WHERE contract.tenant_id = NEW.tenant_id
          AND contract.contract_id = NEW.contract_id
          AND contract.lifecycle_state = 'registered'
          AND contract.current_revision_id = NEW.contract_revision_id
          AND contract.xmin::text::bigint =
              (pg_current_xact_id()::text::bigint % 4294967296)
    ) THEN
        RAISE EXCEPTION 'signing registration must accompany the contract registration transition';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER clm_contract_signing_registration_insert_guard
    BEFORE INSERT ON clm_contract_signing_registrations
    FOR EACH ROW EXECUTE FUNCTION guard_clm_signing_registration_insert();
CREATE TRIGGER clm_contract_signing_registrations_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_signing_registrations
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
CREATE INDEX clm_contract_signing_registrations_date
    ON clm_contract_signing_registrations (tenant_id, signing_date, contract_id);

CREATE TABLE clm_contract_supplements (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    parent_contract_id uuid NOT NULL,
    supplement_contract_id uuid NOT NULL,
    parent_revision_id uuid NOT NULL,
    relation_type text NOT NULL CHECK (relation_type IN ('supplementary_agreement', 'change_order')),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 2000),
    linked_by text NOT NULL CHECK (length(btrim(linked_by)) > 0),
    linked_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, parent_contract_id, supplement_contract_id),
    UNIQUE (tenant_id, supplement_contract_id),
    CHECK (parent_contract_id <> supplement_contract_id),
    FOREIGN KEY (tenant_id, parent_contract_id)
        REFERENCES clm_contracts (tenant_id, contract_id),
    FOREIGN KEY (tenant_id, supplement_contract_id)
        REFERENCES clm_contracts (tenant_id, contract_id),
    FOREIGN KEY (tenant_id, parent_contract_id, parent_revision_id)
        REFERENCES clm_contract_revisions (tenant_id, contract_id, contract_revision_id)
);

CREATE OR REPLACE FUNCTION guard_clm_supplement_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM clm_contracts supplement
        WHERE supplement.tenant_id = NEW.tenant_id
          AND supplement.contract_id = NEW.supplement_contract_id
          AND supplement.authority_mode = 'native'
          AND supplement.lifecycle_state = 'draft'
          AND supplement.row_version = 1
          AND supplement.xmin::text::bigint =
              (pg_current_xact_id()::text::bigint % 4294967296)
    ) THEN
        RAISE EXCEPTION 'supplement relation must accompany supplement contract creation';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER clm_contract_supplements_insert_guard
    BEFORE INSERT ON clm_contract_supplements
    FOR EACH ROW EXECUTE FUNCTION guard_clm_supplement_insert();
CREATE TRIGGER clm_contract_supplements_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_supplements
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
CREATE INDEX clm_contract_supplements_parent
    ON clm_contract_supplements (tenant_id, parent_contract_id, linked_at DESC);

DO $migration$
DECLARE table_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'clm_contract_signing_registrations', 'clm_contract_supplements'
    ] LOOP
        EXECUTE format('REVOKE ALL ON TABLE %I FROM PUBLIC', table_name);
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', table_name);
        EXECUTE format('CREATE POLICY tenant_boundary ON %I USING '
            || '(tenant_id=current_setting(''app.tenant_id'',true)) WITH CHECK '
            || '(tenant_id=current_setting(''app.tenant_id'',true))', table_name);
    END LOOP;
END $migration$;
