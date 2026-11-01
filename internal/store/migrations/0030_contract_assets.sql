-- CLM-16 adds immutable projections of frozen asset plans, manual handoff
-- facts, and authenticated ERP receipt facts. Acceptance quantity ranges are
-- logical accounting slots, not physical serial-number identities.

CREATE OR REPLACE FUNCTION clm_safe_asset_uuid(value jsonb) RETURNS uuid
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE candidate text;
BEGIN
    IF jsonb_typeof(value) <> 'string' THEN RETURN NULL; END IF;
    candidate := value #>> '{}';
    IF candidate !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN RETURN NULL; END IF;
    BEGIN RETURN candidate::uuid; EXCEPTION WHEN OTHERS THEN RETURN NULL; END;
END $$;

CREATE OR REPLACE FUNCTION clm_safe_asset_quantity(value jsonb) RETURNS numeric(18,6)
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE candidate text;
    parsed numeric(18,6);
BEGIN
    IF jsonb_typeof(value) <> 'string' THEN RETURN NULL; END IF;
    candidate := value #>> '{}';
    IF candidate !~ '^(0|[1-9][0-9]{0,11})\.[0-9]{6}$' THEN RETURN NULL; END IF;
    BEGIN parsed := candidate::numeric(18,6); EXCEPTION WHEN OTHERS THEN RETURN NULL; END;
    IF parsed <= 0 THEN RETURN NULL; END IF;
    RETURN parsed;
END $$;

CREATE OR REPLACE FUNCTION clm_safe_asset_label(value jsonb, max_length integer) RETURNS text
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE candidate text;
BEGIN
    IF jsonb_typeof(value) <> 'string' THEN RETURN NULL; END IF;
    candidate := btrim(value #>> '{}');
    IF candidate = '' OR length(candidate) > max_length THEN RETURN NULL; END IF;
    RETURN candidate;
END $$;

CREATE TABLE clm_contract_revision_asset_plans (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    contract_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    asset_plan_id uuid NOT NULL,
    item_id uuid NOT NULL,
    planned_quantity numeric(18,6) NOT NULL CHECK (planned_quantity > 0),
    unit text NOT NULL CHECK (length(btrim(unit)) BETWEEN 1 AND 40),
    department_code text NOT NULL DEFAULT '' CHECK (length(department_code) <= 100),
    department_name text NOT NULL DEFAULT '' CHECK (length(department_name) <= 200),
    custodian_user_id text NOT NULL DEFAULT '' CHECK (length(custodian_user_id) <= 100),
    PRIMARY KEY (tenant_id, contract_id, contract_revision_id, asset_plan_id),
    FOREIGN KEY (tenant_id, contract_id, contract_revision_id, item_id)
        REFERENCES clm_contract_revision_items (tenant_id, contract_id, contract_revision_id, item_id)
);
CREATE TRIGGER clm_contract_revision_asset_plans_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_revision_asset_plans
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

-- Extract only uniquely identified plans with exact item/unit/quantity. Invalid
-- legacy data remains in its immutable snapshot but produces no usable plan fact.
WITH candidates AS (
    SELECT revision.tenant_id, revision.contract_id, revision.contract_revision_id,
           clm_safe_asset_uuid(asset.value->'asset_plan_id') AS asset_plan_id,
           clm_safe_asset_uuid(asset.value->'item_id') AS item_id,
           clm_safe_asset_quantity(asset.value->'quantity') AS planned_quantity,
           COALESCE(clm_safe_asset_label(asset.value->'department_code', 100), '') AS department_code,
           COALESCE(clm_safe_asset_label(asset.value->'department_name', 200), '') AS department_name,
           COALESCE(clm_safe_asset_label(asset.value->'custodian_user_id', 100), '') AS custodian_user_id
    FROM clm_contract_revisions revision
    CROSS JOIN LATERAL jsonb_array_elements(
        CASE WHEN jsonb_typeof(revision.snapshot->'assets')='array'
             THEN revision.snapshot->'assets' ELSE '[]'::jsonb END
    ) AS asset(value)
), counted AS (
    SELECT candidates.*,
           count(asset_plan_id) OVER (PARTITION BY tenant_id,contract_id,contract_revision_id,asset_plan_id) AS plan_id_count
    FROM candidates
), resolvable AS (
    SELECT counted.*, item.unit, item.planned_quantity AS item_quantity
    FROM counted
    JOIN clm_contract_revision_items item USING (tenant_id,contract_id,contract_revision_id,item_id)
    WHERE counted.asset_plan_id IS NOT NULL AND counted.item_id IS NOT NULL
      AND counted.planned_quantity IS NOT NULL AND counted.plan_id_count=1
)
INSERT INTO clm_contract_revision_asset_plans
    (tenant_id,contract_id,contract_revision_id,asset_plan_id,item_id,planned_quantity,unit,department_code,department_name,custodian_user_id)
SELECT tenant_id,contract_id,contract_revision_id,asset_plan_id,item_id,planned_quantity,unit,department_code,department_name,custodian_user_id
FROM resolvable WHERE unit IS NOT NULL AND item_quantity IS NOT NULL AND planned_quantity <= item_quantity;

CREATE OR REPLACE FUNCTION project_clm_revision_asset_plans() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    WITH candidates AS (
        SELECT clm_safe_asset_uuid(asset.value->'asset_plan_id') AS asset_plan_id,
               clm_safe_asset_uuid(asset.value->'item_id') AS item_id,
               clm_safe_asset_quantity(asset.value->'quantity') AS planned_quantity,
               COALESCE(clm_safe_asset_label(asset.value->'department_code',100),'') AS department_code,
               COALESCE(clm_safe_asset_label(asset.value->'department_name',200),'') AS department_name,
               COALESCE(clm_safe_asset_label(asset.value->'custodian_user_id',100),'') AS custodian_user_id
        FROM jsonb_array_elements(CASE WHEN jsonb_typeof(NEW.snapshot->'assets')='array'
            THEN NEW.snapshot->'assets' ELSE '[]'::jsonb END) AS asset(value)
    ), counted AS (
        SELECT candidates.*, count(asset_plan_id) OVER (PARTITION BY asset_plan_id) AS plan_id_count
        FROM candidates
    )
    INSERT INTO clm_contract_revision_asset_plans
        (tenant_id,contract_id,contract_revision_id,asset_plan_id,item_id,planned_quantity,unit,department_code,department_name,custodian_user_id)
    SELECT NEW.tenant_id,NEW.contract_id,NEW.contract_revision_id,counted.asset_plan_id,counted.item_id,counted.planned_quantity,
           item.unit,counted.department_code,counted.department_name,counted.custodian_user_id
    FROM counted JOIN clm_contract_revision_items item
      ON item.tenant_id=NEW.tenant_id AND item.contract_id=NEW.contract_id
     AND item.contract_revision_id=NEW.contract_revision_id AND item.item_id=counted.item_id
    WHERE counted.asset_plan_id IS NOT NULL AND counted.item_id IS NOT NULL AND counted.planned_quantity IS NOT NULL
      AND counted.plan_id_count=1 AND item.unit IS NOT NULL AND item.planned_quantity IS NOT NULL
      AND counted.planned_quantity <= item.planned_quantity;
    RETURN NEW;
END $$;
CREATE CONSTRAINT TRIGGER clm_contract_revision_asset_plans_project
    AFTER INSERT ON clm_contract_revisions DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION project_clm_revision_asset_plans();

CREATE TABLE clm_contract_asset_handovers (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    contract_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    handover_id uuid NOT NULL,
    asset_plan_id uuid NOT NULL,
    delivery_id uuid NOT NULL,
    item_id uuid NOT NULL,
    acceptance_id uuid NOT NULL,
    scope_start numeric(18,6) NOT NULL CHECK (scope_start >= 0),
    scope_end numeric(18,6) NOT NULL CHECK (scope_end > scope_start),
    quantity numeric(18,6) GENERATED ALWAYS AS (scope_end-scope_start) STORED,
    unit text NOT NULL CHECK (length(btrim(unit)) BETWEEN 1 AND 40),
    handed_over_at timestamptz NOT NULL,
    location text NOT NULL CHECK (length(btrim(location)) BETWEEN 1 AND 1000),
    transfer_note text NOT NULL CHECK (length(btrim(transfer_note)) BETWEEN 1 AND 2000),
    evidence_snapshot jsonb NOT NULL CHECK (jsonb_typeof(evidence_snapshot)='array' AND jsonb_array_length(evidence_snapshot)>0),
    department_code_claim text NOT NULL DEFAULT '',
    department_name_claim text NOT NULL DEFAULT '',
    department_verification text NOT NULL CHECK (department_verification IN ('unknown','verified')),
    custodian_user_id text NOT NULL DEFAULT '',
    custodian_display_name_snapshot text NOT NULL DEFAULT '',
    custodian_tenant_id_snapshot text NOT NULL DEFAULT '',
    custodian_org_id_snapshot text NOT NULL DEFAULT '',
    custodian_directory_source text NOT NULL DEFAULT '',
    custodian_directory_revision text NOT NULL DEFAULT '',
    custodian_directory_checked_at timestamptz,
    custodian_verification text NOT NULL CHECK (custodian_verification IN ('unknown','verified')),
    recorded_by text NOT NULL CHECK (length(btrim(recorded_by))>0),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    contract_row_version bigint NOT NULL CHECK (contract_row_version>0),
    PRIMARY KEY (tenant_id,contract_id,handover_id),
    UNIQUE (tenant_id,contract_id,contract_revision_id,handover_id),
    FOREIGN KEY (tenant_id,contract_id,contract_revision_id)
        REFERENCES clm_contract_revisions (tenant_id,contract_id,contract_revision_id),
    FOREIGN KEY (tenant_id,contract_id,contract_revision_id,asset_plan_id)
        REFERENCES clm_contract_revision_asset_plans (tenant_id,contract_id,contract_revision_id,asset_plan_id),
    FOREIGN KEY (tenant_id,contract_id,contract_revision_id,delivery_id,item_id)
        REFERENCES clm_contract_deliveries (tenant_id,contract_id,contract_revision_id,delivery_id,item_id),
    FOREIGN KEY (tenant_id,contract_id,acceptance_id,contract_revision_id,delivery_id,item_id)
        REFERENCES clm_contract_acceptances (tenant_id,contract_id,acceptance_id,contract_revision_id,delivery_id,item_id)
);
CREATE TRIGGER clm_contract_asset_handovers_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_asset_handovers
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
CREATE INDEX clm_contract_asset_handovers_scope
    ON clm_contract_asset_handovers (tenant_id,contract_id,contract_revision_id,item_id,delivery_id,scope_start,scope_end);

CREATE TABLE clm_contract_asset_receipts (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id))>0),
    contract_id uuid NOT NULL,
    handover_id uuid NOT NULL,
    receipt_id uuid NOT NULL,
    source_id text NOT NULL CHECK (length(btrim(source_id)) BETWEEN 1 AND 200),
    source_key_id text NOT NULL CHECK (length(btrim(source_key_id)) BETWEEN 1 AND 200),
    source_event_id text NOT NULL CHECK (length(btrim(source_event_id)) BETWEEN 1 AND 300),
    event_type text NOT NULL CHECK (event_type IN ('receipt','reversal')),
    original_source_event_id text,
    asset_code text NOT NULL CHECK (length(btrim(asset_code)) BETWEEN 1 AND 200),
    quantity numeric(18,6) NOT NULL CHECK (quantity>0),
    unit text NOT NULL CHECK (length(btrim(unit)) BETWEEN 1 AND 40),
    occurred_at timestamptz NOT NULL,
    payload_digest text NOT NULL CHECK (payload_digest ~ '^[a-f0-9]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,receipt_id),
    UNIQUE (tenant_id,source_id,source_event_id),
    FOREIGN KEY (tenant_id,contract_id,handover_id)
        REFERENCES clm_contract_asset_handovers (tenant_id,contract_id,handover_id),
    CHECK ((event_type='receipt' AND original_source_event_id IS NULL)
        OR (event_type='reversal' AND original_source_event_id IS NOT NULL
            AND length(btrim(original_source_event_id)) BETWEEN 1 AND 300))
);
CREATE TRIGGER clm_contract_asset_receipts_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_asset_receipts
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
CREATE INDEX clm_contract_asset_receipts_handover
    ON clm_contract_asset_receipts (tenant_id,contract_id,handover_id,occurred_at);

DO $migration$
DECLARE table_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'clm_contract_revision_asset_plans','clm_contract_asset_handovers','clm_contract_asset_receipts'
    ] LOOP
        EXECUTE format('REVOKE ALL ON TABLE %I FROM PUBLIC',table_name);
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',table_name);
        EXECUTE format('CREATE POLICY tenant_boundary ON %I USING (tenant_id=current_setting(''app.tenant_id'',true)) WITH CHECK (tenant_id=current_setting(''app.tenant_id'',true))',table_name);
    END LOOP;
END $migration$;
