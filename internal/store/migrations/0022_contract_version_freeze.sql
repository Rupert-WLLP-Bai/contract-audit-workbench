-- Freezing a native draft appends one immutable contract revision together
-- with the exact material summaries and restricted-field fingerprints observed
-- at that moment. A later upload with the same filename creates a new document
-- and material version; it can never rewrite these rows.
--
-- The child insert guard follows the same rule as operator evaluation objects:
-- a version child row may only be inserted by the transaction that inserted
-- its parent revision. The append-only trigger alone would still allow a later
-- transaction to append goods, funding rows or attachments to an old version.

ALTER TABLE clm_contract_revisions
    ADD COLUMN amounts_reconciled boolean NOT NULL DEFAULT false;
ALTER TABLE clm_contract_revisions
    ADD CONSTRAINT clm_revision_reconciled_amount
    CHECK (NOT amounts_reconciled OR (amount IS NOT NULL AND currency IS NOT NULL));

CREATE TABLE clm_contract_version_materials (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    contract_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    material_id text NOT NULL CHECK (length(btrim(material_id)) > 0),
    material_version_id text NOT NULL CHECK (length(btrim(material_version_id)) > 0),
    case_org_id text NOT NULL CHECK (length(btrim(case_org_id)) > 0),
    case_id text NOT NULL CHECK (length(btrim(case_id)) > 0),
    material_version integer NOT NULL CHECK (material_version > 0),
    filename text NOT NULL CHECK (length(btrim(filename)) > 0),
    digest text NOT NULL CHECK (digest ~ '^[a-f0-9]{64}$'),
    purpose text NOT NULL CHECK (purpose IN
        ('draft', 'signed_contract', 'supporting', 'acceptance', 'payment', 'other')),
    PRIMARY KEY (tenant_id, contract_id, contract_revision_id, material_id),
    UNIQUE (tenant_id, contract_id, contract_revision_id, material_version_id),
    FOREIGN KEY (tenant_id, contract_id, contract_revision_id)
        REFERENCES clm_contract_revisions (tenant_id, contract_id, contract_revision_id),
    FOREIGN KEY (tenant_id, case_org_id, case_id, material_id, material_version)
        REFERENCES material_versions (tenant_id, org_id, case_id, material_id, version),
    FOREIGN KEY (tenant_id, case_org_id, case_id, material_version_id)
        REFERENCES documents (tenant_id, org_id, case_id, id)
);
CREATE INDEX clm_contract_version_materials_document
    ON clm_contract_version_materials (tenant_id, material_version_id);

CREATE TABLE clm_contract_version_seals (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    contract_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    snapshot_digest text NOT NULL CHECK (snapshot_digest ~ '^[a-f0-9]{64}$'),
    materials_digest text NOT NULL CHECK (materials_digest ~ '^[a-f0-9]{64}$'),
    content_fingerprint text NOT NULL CHECK (content_fingerprint ~ '^[a-f0-9]{64}$'),
    restricted_fingerprints jsonb NOT NULL CHECK (jsonb_typeof(restricted_fingerprints) = 'object'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, contract_id, contract_revision_id),
    FOREIGN KEY (tenant_id, contract_id, contract_revision_id)
        REFERENCES clm_contract_revisions (tenant_id, contract_id, contract_revision_id)
);

-- Version children may only be appended in the transaction that inserted the
-- revision. xmin is the inserting transaction id; the modulo matches the
-- existing operator evaluation guard for 64-bit xid wraparound.
CREATE OR REPLACE FUNCTION guard_contract_version_child_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM clm_contract_revisions parent
        WHERE parent.tenant_id = NEW.tenant_id
          AND parent.contract_id = NEW.contract_id
          AND parent.contract_revision_id = NEW.contract_revision_id
          AND parent.xmin::text::bigint =
              (pg_current_xact_id()::text::bigint % 4294967296)
    ) THEN
        RAISE EXCEPTION 'contract version children may only be inserted with their revision';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER clm_contract_version_materials_insert_guard
    BEFORE INSERT ON clm_contract_version_materials
    FOR EACH ROW EXECUTE FUNCTION guard_contract_version_child_insert();
CREATE TRIGGER clm_contract_version_materials_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_version_materials
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
CREATE TRIGGER clm_contract_version_seals_insert_guard
    BEFORE INSERT ON clm_contract_version_seals
    FOR EACH ROW EXECUTE FUNCTION guard_contract_version_child_insert();
CREATE TRIGGER clm_contract_version_seals_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_version_seals
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

DO $migration$
DECLARE table_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY['clm_contract_version_materials', 'clm_contract_version_seals'] LOOP
        EXECUTE format('REVOKE ALL ON TABLE %I FROM PUBLIC', table_name);
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', table_name);
        EXECUTE format('CREATE POLICY tenant_boundary ON %I USING '
            || '(tenant_id=current_setting(''app.tenant_id'',true)) WITH CHECK '
            || '(tenant_id=current_setting(''app.tenant_id'',true))', table_name);
    END LOOP;
END $migration$;
