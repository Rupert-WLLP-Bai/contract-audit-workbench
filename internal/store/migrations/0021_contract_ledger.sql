-- Contract ledger support: personal saved filters and frozen export
-- artifacts. Both tables are additive; no existing row or frozen revision is
-- rewritten. Exported rows are inserted once and never updated by the service
-- role, while saved views are mutable personal preferences (not audit facts).
CREATE TABLE clm_contract_saved_views (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    view_id uuid NOT NULL,
    owner_user_id text NOT NULL CHECK (length(btrim(owner_user_id)) > 0),
    name text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 60),
    filter jsonb NOT NULL CHECK (jsonb_typeof(filter) = 'object'),
    columns jsonb NOT NULL CHECK (jsonb_typeof(columns) = 'array'),
    row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, view_id),
    UNIQUE (tenant_id, owner_user_id, name)
);
CREATE INDEX clm_contract_saved_views_owner
    ON clm_contract_saved_views (tenant_id, owner_user_id, updated_at DESC, view_id DESC);

CREATE TABLE clm_contract_exports (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    export_id uuid NOT NULL,
    actor_id text NOT NULL CHECK (length(btrim(actor_id)) > 0),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    request_digest text NOT NULL CHECK (request_digest ~ '^[a-f0-9]{64}$'),
    filter jsonb NOT NULL CHECK (jsonb_typeof(filter) = 'object'),
    columns jsonb NOT NULL CHECK (jsonb_typeof(columns) = 'array'),
    format text NOT NULL CHECK (format IN ('csv')),
    contract_ids jsonb NOT NULL CHECK (jsonb_typeof(contract_ids) = 'array'),
    rows jsonb NOT NULL CHECK (jsonb_typeof(rows) = 'array'),
    row_count integer NOT NULL CHECK (row_count >= 0),
    policy_version text NOT NULL CHECK (length(btrim(policy_version)) > 0),
    grants_digest text NOT NULL CHECK (grants_digest ~ '^[a-f0-9]{64}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, export_id),
    UNIQUE (tenant_id, actor_id, idempotency_key),
    CHECK (expires_at > created_at),
    CHECK (jsonb_array_length(contract_ids) = row_count),
    CHECK (jsonb_array_length(rows) = row_count)
);
CREATE INDEX clm_contract_exports_owner
    ON clm_contract_exports (tenant_id, actor_id, created_at DESC, export_id DESC);

DO $migration$
DECLARE table_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY['clm_contract_saved_views', 'clm_contract_exports'] LOOP
        EXECUTE format('REVOKE ALL ON TABLE %I FROM PUBLIC', table_name);
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', table_name);
        EXECUTE format('CREATE POLICY tenant_boundary ON %I USING '
            || '(tenant_id=current_setting(''app.tenant_id'',true)) WITH CHECK '
            || '(tenant_id=current_setting(''app.tenant_id'',true))', table_name);
    END LOOP;
END $migration$;
