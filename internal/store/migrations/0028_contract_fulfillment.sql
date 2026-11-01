-- CLM-14 keeps the frozen plan in each immutable revision and records actual
-- deliveries, accepted quantity ranges, defects and remediation as append-only
-- facts. A quantity range is a logical accounting slot, not a serial-number
-- assertion about a physical object.

CREATE TABLE clm_contract_revision_items (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    contract_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    item_id uuid NOT NULL,
    planned_quantity numeric(18,6) CHECK (planned_quantity IS NULL OR planned_quantity >= 0),
    unit text CHECK (unit IS NULL OR length(btrim(unit)) > 0),
    PRIMARY KEY (tenant_id, contract_id, contract_revision_id, item_id),
    FOREIGN KEY (tenant_id, contract_id, contract_revision_id)
        REFERENCES clm_contract_revisions (tenant_id, contract_id, contract_revision_id)
);

CREATE OR REPLACE FUNCTION clm_safe_revision_item_uuid(value jsonb) RETURNS uuid
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE candidate text;
BEGIN
    IF jsonb_typeof(value) <> 'string' THEN RETURN NULL; END IF;
    candidate := value #>> '{}';
    IF candidate !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN
        RETURN NULL;
    END IF;
    BEGIN
        RETURN candidate::uuid;
    EXCEPTION WHEN OTHERS THEN
        RETURN NULL;
    END;
END $$;

CREATE OR REPLACE FUNCTION clm_safe_revision_item_quantity(value jsonb) RETURNS numeric(18,6)
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE candidate text;
    parsed numeric(18,6);
BEGIN
    IF jsonb_typeof(value) <> 'string' THEN RETURN NULL; END IF;
    candidate := value #>> '{}';
    IF candidate !~ '^(0|[1-9][0-9]{0,11})\.[0-9]{6}$' THEN RETURN NULL; END IF;
    BEGIN
        parsed := candidate::numeric(18,6);
    EXCEPTION WHEN OTHERS THEN
        RETURN NULL;
    END;
    IF parsed <= 0 THEN RETURN NULL; END IF;
    RETURN parsed;
END $$;

CREATE OR REPLACE FUNCTION clm_safe_revision_item_unit(value jsonb) RETURNS text
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE candidate text;
BEGIN
    IF jsonb_typeof(value) <> 'string' THEN RETURN NULL; END IF;
    candidate := btrim(value #>> '{}');
    IF length(candidate) = 0 OR length(candidate) > 40 THEN RETURN NULL; END IF;
    RETURN candidate;
END $$;

-- Backfill only facts already frozen in historical revision snapshots. Actual
-- delivery data is never inferred from a plan. Safe parsers turn malformed
-- legacy fields into unknown; duplicate item IDs make every duplicate row
-- ambiguous and are all skipped. No cast depends on WHERE evaluation order.
WITH revision_items AS (
    SELECT revision.tenant_id, revision.contract_id, revision.contract_revision_id,
           clm_safe_revision_item_uuid(item.value->'item_id') AS item_id,
           clm_safe_revision_item_quantity(item.value->'quantity') AS planned_quantity,
           clm_safe_revision_item_unit(item.value->'unit') AS unit
    FROM clm_contract_revisions AS revision
    CROSS JOIN LATERAL jsonb_array_elements(
        CASE WHEN jsonb_typeof(revision.snapshot->'items') = 'array'
             THEN revision.snapshot->'items' ELSE '[]'::jsonb END
    ) AS item(value)
), counted AS (
    SELECT revision_items.*,
           count(item_id) OVER (PARTITION BY tenant_id, contract_id, contract_revision_id, item_id) AS id_count
    FROM revision_items
)
INSERT INTO clm_contract_revision_items
    (tenant_id, contract_id, contract_revision_id, item_id, planned_quantity, unit)
SELECT tenant_id, contract_id, contract_revision_id, item_id, planned_quantity, unit
FROM counted WHERE item_id IS NOT NULL AND id_count = 1;

CREATE TRIGGER clm_contract_revision_items_insert_guard
    BEFORE INSERT ON clm_contract_revision_items
    FOR EACH ROW EXECUTE FUNCTION guard_contract_version_child_insert();
CREATE TRIGGER clm_contract_revision_items_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_revision_items
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

CREATE TABLE clm_contract_deliveries (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    contract_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    delivery_id uuid NOT NULL,
    delivery_plan_sequence integer,
    item_id uuid NOT NULL,
    batch_reference text NOT NULL CHECK (length(btrim(batch_reference)) BETWEEN 1 AND 200),
    quantity numeric(18,6) NOT NULL CHECK (quantity > 0),
    unit text NOT NULL CHECK (length(btrim(unit)) > 0),
    actual_at timestamptz NOT NULL,
    location text NOT NULL CHECK (length(btrim(location)) BETWEEN 1 AND 1000),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 4000),
    evidence_snapshot jsonb NOT NULL CHECK (jsonb_typeof(evidence_snapshot) = 'array'
        AND jsonb_array_length(evidence_snapshot) > 0),
    recorded_by text NOT NULL CHECK (length(btrim(recorded_by)) > 0),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, contract_id, delivery_id),
    UNIQUE (tenant_id, contract_id, contract_revision_id, item_id, batch_reference),
    UNIQUE (tenant_id, contract_id, contract_revision_id, delivery_id, item_id),
    FOREIGN KEY (tenant_id, contract_id, contract_revision_id, item_id)
        REFERENCES clm_contract_revision_items (tenant_id, contract_id, contract_revision_id, item_id)
);
CREATE TRIGGER clm_contract_deliveries_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_deliveries
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
CREATE INDEX clm_contract_deliveries_item
    ON clm_contract_deliveries (tenant_id, contract_id, contract_revision_id, item_id, actual_at);

CREATE TABLE clm_contract_acceptances (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    contract_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    acceptance_id uuid NOT NULL,
    delivery_id uuid NOT NULL,
    item_id uuid NOT NULL,
    acceptance_plan_sequence integer,
    scope_start numeric(18,6) NOT NULL CHECK (scope_start >= 0),
    scope_end numeric(18,6) NOT NULL CHECK (scope_end > scope_start),
    quantity numeric(18,6) GENERATED ALWAYS AS (scope_end - scope_start) STORED,
    accepted_quantity numeric(18,6) NOT NULL CHECK (accepted_quantity >= 0 AND accepted_quantity <= scope_end - scope_start),
    result text NOT NULL CHECK (result IN ('passed', 'partial', 'failed')),
    reinspection_of uuid,
    actual_at timestamptz NOT NULL,
    standard text NOT NULL CHECK (length(btrim(standard)) BETWEEN 1 AND 4000),
    inspector_note text NOT NULL DEFAULT '' CHECK (length(inspector_note) <= 4000),
    evidence_snapshot jsonb NOT NULL CHECK (jsonb_typeof(evidence_snapshot) = 'array'
        AND jsonb_array_length(evidence_snapshot) > 0),
    recorded_by text NOT NULL CHECK (length(btrim(recorded_by)) > 0),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, contract_id, acceptance_id),
    UNIQUE (tenant_id, contract_id, acceptance_id, contract_revision_id, delivery_id, item_id),
    UNIQUE (tenant_id, contract_id, acceptance_id, contract_revision_id),
    UNIQUE (tenant_id, contract_id, reinspection_of),
    FOREIGN KEY (tenant_id, contract_id, contract_revision_id, delivery_id, item_id)
        REFERENCES clm_contract_deliveries (tenant_id, contract_id, contract_revision_id, delivery_id, item_id),
    FOREIGN KEY (tenant_id, contract_id, reinspection_of, contract_revision_id, delivery_id, item_id)
        REFERENCES clm_contract_acceptances (tenant_id, contract_id, acceptance_id, contract_revision_id, delivery_id, item_id),
    CHECK ((result = 'passed' AND accepted_quantity = scope_end - scope_start)
        OR (result = 'failed' AND accepted_quantity = 0)
        OR (result = 'partial' AND accepted_quantity > 0 AND accepted_quantity < scope_end - scope_start)),
    CHECK (reinspection_of IS NULL OR reinspection_of <> acceptance_id)
);

CREATE OR REPLACE FUNCTION guard_clm_acceptance_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    delivery_quantity numeric(18,6);
    parent_start numeric(18,6);
    parent_end numeric(18,6);
    parent_accepted numeric(18,6);
    parent_result text;
BEGIN
    SELECT delivery.quantity INTO delivery_quantity
    FROM clm_contract_deliveries delivery
    WHERE delivery.tenant_id = NEW.tenant_id AND delivery.contract_id = NEW.contract_id
      AND delivery.contract_revision_id = NEW.contract_revision_id
      AND delivery.delivery_id = NEW.delivery_id AND delivery.item_id = NEW.item_id;
    IF delivery_quantity IS NULL OR NEW.scope_end > delivery_quantity THEN
        RAISE EXCEPTION 'acceptance range exceeds delivered quantity';
    END IF;
    IF NEW.reinspection_of IS NULL THEN
        IF EXISTS (
            SELECT 1 FROM clm_contract_acceptances existing
            WHERE existing.tenant_id = NEW.tenant_id AND existing.contract_id = NEW.contract_id
              AND existing.delivery_id = NEW.delivery_id AND existing.reinspection_of IS NULL
              AND numrange(existing.scope_start, existing.scope_end, '[)')
                  && numrange(NEW.scope_start, NEW.scope_end, '[)')
        ) THEN
            RAISE EXCEPTION 'acceptance range overlaps an existing root range';
        END IF;
    ELSE
        SELECT parent.scope_start, parent.scope_end, parent.accepted_quantity, parent.result
        INTO parent_start, parent_end, parent_accepted, parent_result
        FROM clm_contract_acceptances parent
        WHERE parent.tenant_id = NEW.tenant_id AND parent.contract_id = NEW.contract_id
          AND parent.acceptance_id = NEW.reinspection_of
          AND parent.contract_revision_id = NEW.contract_revision_id
          AND parent.delivery_id = NEW.delivery_id AND parent.item_id = NEW.item_id;
        IF parent_start IS NULL OR parent_result = 'passed'
           OR NEW.scope_start <> parent_start + parent_accepted OR NEW.scope_end <> parent_end THEN
            RAISE EXCEPTION 'reinspection must exactly cover the parent failed suffix';
        END IF;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER clm_contract_acceptance_insert_guard
    BEFORE INSERT ON clm_contract_acceptances
    FOR EACH ROW EXECUTE FUNCTION guard_clm_acceptance_insert();
CREATE TRIGGER clm_contract_acceptances_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_acceptances
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
CREATE INDEX clm_contract_acceptances_delivery
    ON clm_contract_acceptances (tenant_id, contract_id, delivery_id, recorded_at);

CREATE TABLE clm_contract_acceptance_defects (
    tenant_id text NOT NULL,
    contract_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    acceptance_id uuid NOT NULL,
    defect_id uuid NOT NULL,
    prior_acceptance_id uuid,
    prior_defect_id uuid,
    description text NOT NULL CHECK (length(btrim(description)) BETWEEN 1 AND 4000),
    responsible text NOT NULL CHECK (length(btrim(responsible)) BETWEEN 1 AND 300),
    remediation_due date NOT NULL,
    failed_quantity numeric(18,6) NOT NULL CHECK (failed_quantity > 0),
    recorded_by text NOT NULL CHECK (length(btrim(recorded_by)) > 0),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, contract_id, acceptance_id, defect_id),
    CHECK ((prior_acceptance_id IS NULL) = (prior_defect_id IS NULL)),
    FOREIGN KEY (tenant_id, contract_id, prior_acceptance_id, prior_defect_id)
        REFERENCES clm_contract_acceptance_defects (tenant_id, contract_id, acceptance_id, defect_id),
    FOREIGN KEY (tenant_id, contract_id, acceptance_id, contract_revision_id)
        REFERENCES clm_contract_acceptances (tenant_id, contract_id, acceptance_id, contract_revision_id)
);
CREATE TRIGGER clm_contract_acceptance_defects_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_acceptance_defects
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

CREATE OR REPLACE FUNCTION guard_clm_acceptance_defect_lineage() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE parent_id uuid;
BEGIN
    IF NEW.prior_acceptance_id IS NOT NULL THEN
        SELECT acceptance.reinspection_of INTO parent_id
        FROM clm_contract_acceptances acceptance
        WHERE acceptance.tenant_id=NEW.tenant_id AND acceptance.contract_id=NEW.contract_id
          AND acceptance.acceptance_id=NEW.acceptance_id
          AND acceptance.contract_revision_id=NEW.contract_revision_id;
        IF parent_id IS NULL OR parent_id <> NEW.prior_acceptance_id THEN
            RAISE EXCEPTION 'prior defect must belong to the exact reinspection parent';
        END IF;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER clm_contract_acceptance_defect_lineage
    BEFORE INSERT ON clm_contract_acceptance_defects
    FOR EACH ROW EXECUTE FUNCTION guard_clm_acceptance_defect_lineage();

CREATE OR REPLACE FUNCTION validate_clm_acceptance_defects() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE failed numeric(18,6);
    declared numeric(18,6);
    defect_count integer;
BEGIN
    failed := NEW.scope_end - NEW.scope_start - NEW.accepted_quantity;
    SELECT COALESCE(sum(defect.failed_quantity),0), count(*)
    INTO declared, defect_count
    FROM clm_contract_acceptance_defects defect
    WHERE defect.tenant_id=NEW.tenant_id AND defect.contract_id=NEW.contract_id
      AND defect.acceptance_id=NEW.acceptance_id;
    IF failed = 0 AND defect_count <> 0 THEN
        RAISE EXCEPTION 'passed acceptance cannot include defects';
    END IF;
    IF failed > 0 AND (defect_count = 0 OR declared <> failed) THEN
        RAISE EXCEPTION 'defect quantities must exactly cover the failed suffix';
    END IF;
    RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER clm_contract_acceptance_defects_complete
    AFTER INSERT ON clm_contract_acceptances
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION validate_clm_acceptance_defects();

CREATE TABLE clm_contract_rectifications (
    tenant_id text NOT NULL,
    contract_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    rectification_id uuid NOT NULL,
    acceptance_id uuid NOT NULL,
    defect_id uuid NOT NULL,
    description text NOT NULL CHECK (length(btrim(description)) BETWEEN 1 AND 4000),
    evidence_snapshot jsonb NOT NULL CHECK (jsonb_typeof(evidence_snapshot) = 'array'
        AND jsonb_array_length(evidence_snapshot) > 0),
    recorded_by text NOT NULL CHECK (length(btrim(recorded_by)) > 0),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, contract_id, rectification_id),
    UNIQUE (tenant_id, contract_id, rectification_id, acceptance_id, defect_id),
    FOREIGN KEY (tenant_id, contract_id, acceptance_id, defect_id)
        REFERENCES clm_contract_acceptance_defects (tenant_id, contract_id, acceptance_id, defect_id)
);
CREATE TRIGGER clm_contract_rectifications_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_rectifications
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

CREATE TABLE clm_contract_acceptance_rectifications (
    tenant_id text NOT NULL,
    contract_id uuid NOT NULL,
    acceptance_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    delivery_id uuid NOT NULL,
    item_id uuid NOT NULL,
    rectification_id uuid NOT NULL,
    defect_acceptance_id uuid NOT NULL,
    defect_id uuid NOT NULL,
    PRIMARY KEY (tenant_id, contract_id, acceptance_id, rectification_id),
    FOREIGN KEY (tenant_id, contract_id, acceptance_id, contract_revision_id, delivery_id, item_id)
        REFERENCES clm_contract_acceptances (tenant_id, contract_id, acceptance_id, contract_revision_id, delivery_id, item_id),
    FOREIGN KEY (tenant_id, contract_id, rectification_id, defect_acceptance_id, defect_id)
        REFERENCES clm_contract_rectifications (tenant_id, contract_id, rectification_id, acceptance_id, defect_id)
);
CREATE TRIGGER clm_contract_acceptance_rectifications_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_acceptance_rectifications
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

CREATE OR REPLACE FUNCTION guard_clm_acceptance_rectification_lineage() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE parent_id uuid;
BEGIN
    SELECT acceptance.reinspection_of INTO parent_id
    FROM clm_contract_acceptances acceptance
    WHERE acceptance.tenant_id=NEW.tenant_id AND acceptance.contract_id=NEW.contract_id
      AND acceptance.acceptance_id=NEW.acceptance_id
      AND acceptance.contract_revision_id=NEW.contract_revision_id
      AND acceptance.delivery_id=NEW.delivery_id AND acceptance.item_id=NEW.item_id;
    IF parent_id IS NULL OR parent_id <> NEW.defect_acceptance_id THEN
        RAISE EXCEPTION 'reinspection may cite only rectification facts from its exact parent';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER clm_contract_acceptance_rectification_lineage
    BEFORE INSERT ON clm_contract_acceptance_rectifications
    FOR EACH ROW EXECUTE FUNCTION guard_clm_acceptance_rectification_lineage();

DO $migration$
DECLARE table_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'clm_contract_revision_items', 'clm_contract_deliveries', 'clm_contract_acceptances',
        'clm_contract_acceptance_defects', 'clm_contract_rectifications',
        'clm_contract_acceptance_rectifications'
    ] LOOP
        EXECUTE format('REVOKE ALL ON TABLE %I FROM PUBLIC', table_name);
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', table_name);
        EXECUTE format('CREATE POLICY tenant_boundary ON %I USING '
            || '(tenant_id=current_setting(''app.tenant_id'',true)) WITH CHECK '
            || '(tenant_id=current_setting(''app.tenant_id'',true))', table_name);
    END LOOP;
END $migration$;
