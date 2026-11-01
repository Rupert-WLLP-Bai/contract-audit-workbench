-- Contract management owns its aggregate separately from audit cases, review
-- attempts and the execution-job outbox. Existing evidence is not rewritten.
CREATE TABLE clm_contracts (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    contract_id uuid NOT NULL,
    owner_org_id text NOT NULL CHECK (length(btrim(owner_org_id)) > 0),
    authority_mode text NOT NULL CHECK (authority_mode IN ('native', 'external')),
    contract_number text CHECK (contract_number IS NULL OR length(btrim(contract_number)) > 0),
    source_system text,
    external_contract_id text,
    source_version text,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN
        ('draft', 'ready_to_sign', 'registered', 'performing', 'closed', 'terminated')),
    validity_state text NOT NULL DEFAULT 'active' CHECK (validity_state IN ('active', 'voided')),
    current_revision_id uuid,
    row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
    created_by text NOT NULL CHECK (length(btrim(created_by)) > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, contract_id),
    CHECK (authority_mode <> 'external' OR
        (length(btrim(COALESCE(source_system, ''))) > 0
         AND length(btrim(COALESCE(external_contract_id, ''))) > 0
         AND length(btrim(COALESCE(source_version, ''))) > 0))
);
CREATE UNIQUE INDEX clm_contract_number_per_tenant ON clm_contracts (tenant_id, contract_number)
    WHERE contract_number IS NOT NULL;
CREATE UNIQUE INDEX clm_external_contract_per_source
    ON clm_contracts (tenant_id, source_system, external_contract_id)
    WHERE external_contract_id IS NOT NULL;
CREATE INDEX clm_contract_seek_page ON clm_contracts (tenant_id, created_at DESC, contract_id DESC);

CREATE TABLE clm_contract_revisions (
    tenant_id text NOT NULL,
    contract_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    revision_number integer NOT NULL CHECK (revision_number > 0),
    amount numeric(18,2) NOT NULL CHECK (amount >= 0),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
    snapshot_digest text NOT NULL CHECK (snapshot_digest ~ '^[a-f0-9]{64}$'),
    frozen_by text NOT NULL CHECK (length(btrim(frozen_by)) > 0),
    frozen_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, contract_id, contract_revision_id),
    UNIQUE (tenant_id, contract_id, revision_number),
    FOREIGN KEY (tenant_id, contract_id) REFERENCES clm_contracts (tenant_id, contract_id)
);
ALTER TABLE clm_contracts ADD CONSTRAINT clm_contract_current_revision_fk
    FOREIGN KEY (tenant_id, contract_id, current_revision_id)
    REFERENCES clm_contract_revisions (tenant_id, contract_id, contract_revision_id);
CREATE TRIGGER clm_contract_revisions_append_only BEFORE UPDATE OR DELETE ON clm_contract_revisions
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

-- Cases and their materials retain their original organization and service
-- ownership. The composite case FK prevents a cross-tenant or cross-case link.
CREATE TABLE clm_contract_cases (
    tenant_id text NOT NULL,
    contract_id uuid NOT NULL,
    case_org_id text NOT NULL,
    case_id text NOT NULL,
    linked_by text NOT NULL CHECK (length(btrim(linked_by)) > 0),
    linked_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, contract_id, case_org_id, case_id),
    FOREIGN KEY (tenant_id, contract_id) REFERENCES clm_contracts (tenant_id, contract_id),
    FOREIGN KEY (tenant_id, case_org_id, case_id) REFERENCES cases (tenant_id, org_id, id)
);
CREATE TRIGGER clm_contract_cases_append_only BEFORE UPDATE OR DELETE ON clm_contract_cases
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

CREATE TABLE clm_approval_rounds (
    tenant_id text NOT NULL,
    contract_id uuid NOT NULL,
    approval_round_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    round_number integer NOT NULL CHECK (round_number > 0),
    state text NOT NULL CHECK (state IN ('in_review', 'returned', 'withdrawn', 'approved', 'terminated')),
    workflow_template_id text NOT NULL,
    workflow_template_version text NOT NULL,
    routing_snapshot jsonb NOT NULL CHECK (jsonb_typeof(routing_snapshot) = 'object'),
    risk_disposition_snapshot jsonb NOT NULL CHECK (jsonb_typeof(risk_disposition_snapshot) = 'object'),
    row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
    initiated_by text NOT NULL,
    started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ended_at timestamptz,
    PRIMARY KEY (tenant_id, contract_id, approval_round_id),
    UNIQUE (tenant_id, approval_round_id),
    UNIQUE (tenant_id, contract_id, round_number),
    UNIQUE (tenant_id, contract_id, approval_round_id, contract_revision_id),
    FOREIGN KEY (tenant_id, contract_id, contract_revision_id)
        REFERENCES clm_contract_revisions (tenant_id, contract_id, contract_revision_id),
    CHECK ((state = 'in_review' AND ended_at IS NULL) OR (state <> 'in_review' AND ended_at IS NOT NULL))
);
CREATE UNIQUE INDEX clm_one_in_review_round_per_contract
    ON clm_approval_rounds (tenant_id, contract_id) WHERE state = 'in_review';

-- Broker acknowledgement is not user delivery or business completion.
CREATE TABLE clm_business_events (
    tenant_id text NOT NULL,
    event_id uuid NOT NULL,
    contract_id uuid NOT NULL,
    aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
    event_type text NOT NULL CHECK (length(btrim(event_type)) > 0),
    schema_version integer NOT NULL CHECK (schema_version > 0),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, event_id),
    FOREIGN KEY (tenant_id, contract_id) REFERENCES clm_contracts (tenant_id, contract_id)
);
CREATE TRIGGER clm_business_events_append_only BEFORE UPDATE OR DELETE ON clm_business_events
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
CREATE TABLE clm_outbox_delivery (
    tenant_id text NOT NULL,
    event_id uuid NOT NULL,
    available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    claim_token bigint NOT NULL DEFAULT 0 CHECK (claim_token >= 0),
    claimed_until timestamptz,
    broker_acked_at timestamptz,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    PRIMARY KEY (tenant_id, event_id),
    FOREIGN KEY (tenant_id, event_id) REFERENCES clm_business_events (tenant_id, event_id)
);
CREATE INDEX clm_outbox_pending ON clm_outbox_delivery (tenant_id, available_at, event_id)
    WHERE broker_acked_at IS NULL;

DO $migration$
DECLARE table_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'clm_contracts', 'clm_contract_revisions', 'clm_contract_cases',
        'clm_approval_rounds', 'clm_business_events', 'clm_outbox_delivery'
    ] LOOP
        EXECUTE format('REVOKE ALL ON TABLE %I FROM PUBLIC', table_name);
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', table_name);
        EXECUTE format('CREATE POLICY tenant_boundary ON %I USING '
            || '(tenant_id=current_setting(''app.tenant_id'',true)) WITH CHECK '
            || '(tenant_id=current_setting(''app.tenant_id'',true))', table_name);
    END LOOP;
END $migration$;
