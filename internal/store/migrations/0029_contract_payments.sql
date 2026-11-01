-- Payment checks are immutable snapshots of frozen contractual conditions.
-- Payment and reversal receipts are append-only facts accepted only through
-- a server-configured authenticated source (never a browser claim).
CREATE TABLE clm_contract_payment_eligibility_checks (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    contract_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    check_id uuid NOT NULL,
    payment_sequence integer NOT NULL CHECK (payment_sequence > 0),
    status text NOT NULL CHECK (status IN ('eligible', 'blocked', 'unknown')),
    reasons jsonb NOT NULL CHECK (jsonb_typeof(reasons) = 'array'),
    evidence jsonb NOT NULL CHECK (jsonb_typeof(evidence) = 'array'),
    checked_by text NOT NULL CHECK (length(btrim(checked_by)) > 0),
    checked_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    contract_row_version bigint NOT NULL CHECK (contract_row_version > 0),
    PRIMARY KEY (tenant_id, contract_id, check_id),
    FOREIGN KEY (tenant_id, contract_id, contract_revision_id)
        REFERENCES clm_contract_revisions (tenant_id, contract_id, contract_revision_id)
);
CREATE TRIGGER clm_contract_payment_eligibility_checks_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_payment_eligibility_checks
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
CREATE INDEX clm_contract_payment_checks_latest
    ON clm_contract_payment_eligibility_checks
       (tenant_id, contract_id, contract_revision_id, payment_sequence, checked_at DESC);

CREATE TABLE clm_contract_payment_receipts (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    contract_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    receipt_id uuid NOT NULL,
    payment_sequence integer NOT NULL CHECK (payment_sequence > 0),
    source_id text NOT NULL CHECK (length(btrim(source_id)) BETWEEN 1 AND 200),
    source_key_id text NOT NULL CHECK (length(btrim(source_key_id)) BETWEEN 1 AND 200),
    source_event_id text NOT NULL CHECK (length(btrim(source_event_id)) BETWEEN 1 AND 300),
    event_type text NOT NULL CHECK (event_type IN ('payment', 'reversal')),
    original_source_event_id text,
    amount numeric(18,2) NOT NULL CHECK (amount > 0),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    occurred_at timestamptz NOT NULL,
    payload_digest text NOT NULL CHECK (payload_digest ~ '^[a-f0-9]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, receipt_id),
    UNIQUE (tenant_id, source_id, source_event_id),
    FOREIGN KEY (tenant_id, contract_id, contract_revision_id)
        REFERENCES clm_contract_revisions (tenant_id, contract_id, contract_revision_id),
    CHECK ((event_type = 'payment' AND original_source_event_id IS NULL)
        OR (event_type = 'reversal' AND original_source_event_id IS NOT NULL
            AND length(btrim(original_source_event_id)) BETWEEN 1 AND 300))
);
CREATE TRIGGER clm_contract_payment_receipts_append_only
    BEFORE UPDATE OR DELETE ON clm_contract_payment_receipts
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
CREATE INDEX clm_contract_payment_receipts_net
    ON clm_contract_payment_receipts
       (tenant_id, contract_id, contract_revision_id, payment_sequence, currency, occurred_at);

DO $migration$
DECLARE table_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'clm_contract_payment_eligibility_checks', 'clm_contract_payment_receipts'
    ] LOOP
        EXECUTE format('REVOKE ALL ON TABLE %I FROM PUBLIC', table_name);
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', table_name);
        EXECUTE format('CREATE POLICY tenant_boundary ON %I USING '
            || '(tenant_id=current_setting(''app.tenant_id'',true)) WITH CHECK '
            || '(tenant_id=current_setting(''app.tenant_id'',true))', table_name);
    END LOOP;
END $migration$;
