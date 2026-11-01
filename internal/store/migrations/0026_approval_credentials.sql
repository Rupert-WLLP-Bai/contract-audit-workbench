-- CLM-11 approval identity and scoped transfer history.
--
-- A decision may carry a short-lived server-signed credential. The raw token
-- never reaches the database; only its identifier, key id and digest are kept
-- together with the frozen snapshot digest and the row versions the credential
-- was bound to. Columns stay nullable so deployments that have not enabled the
-- credential switch keep their existing idempotent decisions readable.
--
-- A transfer is an append-only reassignment fact. The original task row is not
-- rewritten; the new assignee must still be a live directory account holding
-- the frozen role, and the decision command refuses the old assignee after the
-- transfer. First-version delegation stays off: this table records an
-- operator-managed transfer, never a standing delegation.

ALTER TABLE clm_approval_decisions
    ADD COLUMN IF NOT EXISTS actor_org_id text,
    ADD COLUMN IF NOT EXISTS credential_id text,
    ADD COLUMN IF NOT EXISTS credential_key_id text,
    ADD COLUMN IF NOT EXISTS credential_digest text,
    ADD COLUMN IF NOT EXISTS credential_issued_at timestamptz,
    ADD COLUMN IF NOT EXISTS credential_expires_at timestamptz,
    ADD COLUMN IF NOT EXISTS snapshot_digest text,
    ADD COLUMN IF NOT EXISTS amount_limit numeric(18,2),
    ADD COLUMN IF NOT EXISTS currency text,
    ADD COLUMN IF NOT EXISTS contract_row_version bigint;

ALTER TABLE clm_approval_decisions
    ADD CONSTRAINT clm_approval_decision_credential_complete CHECK (
        (credential_id IS NULL AND credential_key_id IS NULL AND credential_digest IS NULL
         AND credential_issued_at IS NULL AND credential_expires_at IS NULL
         AND snapshot_digest IS NULL AND amount_limit IS NULL AND currency IS NULL
         AND contract_row_version IS NULL)
        OR (credential_id IS NOT NULL AND credential_key_id IS NOT NULL
            AND credential_digest ~ '^[a-f0-9]{64}$'
            AND credential_issued_at IS NOT NULL AND credential_expires_at IS NOT NULL
            AND credential_expires_at > credential_issued_at
            AND snapshot_digest ~ '^[a-f0-9]{64}$'
            AND amount_limit >= 0 AND currency ~ '^[A-Z]{3}$'
            AND contract_row_version > 0)
    );

CREATE TABLE clm_approval_task_transfers (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    contract_id uuid NOT NULL,
    approval_round_id uuid NOT NULL,
    transfer_id uuid NOT NULL,
    approval_task_id uuid NOT NULL,
    from_user_id text NOT NULL CHECK (length(btrim(from_user_id)) > 0),
    to_user_id text NOT NULL CHECK (length(btrim(to_user_id)) > 0),
    to_org_id text NOT NULL CHECK (length(btrim(to_org_id)) > 0),
    reason text NOT NULL CHECK (length(btrim(reason)) > 0 AND char_length(reason) <= 1000),
    actor_id text NOT NULL CHECK (length(btrim(actor_id)) > 0),
    from_task_row_version bigint NOT NULL CHECK (from_task_row_version > 0),
    transferred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, contract_id, transfer_id),
    UNIQUE (tenant_id, approval_task_id),
    FOREIGN KEY (tenant_id, contract_id, approval_round_id, approval_task_id)
        REFERENCES clm_approval_tasks (tenant_id, contract_id, approval_round_id, approval_task_id),
    FOREIGN KEY (tenant_id, contract_id, approval_round_id)
        REFERENCES clm_approval_rounds (tenant_id, contract_id, approval_round_id)
);
CREATE INDEX clm_approval_task_transfers_by_round
    ON clm_approval_task_transfers (tenant_id, contract_id, approval_round_id, transferred_at);

-- Task material whitelist. Submission writes the tasks and their material
-- subset in one transaction; there is no later expansion path, so an external
-- lawyer mapped to a reviewer account only sees the intersection of the frozen
-- revision materials and this task's whitelist. An empty whitelist means no
-- materials, never "all materials".
CREATE TABLE clm_approval_task_materials (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    contract_id uuid NOT NULL,
    approval_round_id uuid NOT NULL,
    approval_task_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    material_id text NOT NULL CHECK (length(btrim(material_id)) > 0),
    material_version_id text NOT NULL CHECK (length(btrim(material_version_id)) > 0),
    PRIMARY KEY (tenant_id, contract_id, approval_round_id, approval_task_id, material_id),
    FOREIGN KEY (tenant_id, contract_id, approval_round_id, approval_task_id)
        REFERENCES clm_approval_tasks (tenant_id, contract_id, approval_round_id, approval_task_id),
    FOREIGN KEY (tenant_id, contract_id, contract_revision_id, material_id)
        REFERENCES clm_contract_version_materials (tenant_id, contract_id, contract_revision_id, material_id)
);
CREATE INDEX clm_approval_task_materials_version
    ON clm_approval_task_materials (tenant_id, contract_id, contract_revision_id, material_version_id);

CREATE OR REPLACE FUNCTION guard_clm_approval_task_material_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM clm_approval_tasks parent
        WHERE parent.tenant_id = NEW.tenant_id
          AND parent.contract_id = NEW.contract_id
          AND parent.approval_round_id = NEW.approval_round_id
          AND parent.approval_task_id = NEW.approval_task_id
          AND parent.xmin::text::bigint =
              (pg_current_xact_id()::text::bigint % 4294967296)
    ) THEN
        RAISE EXCEPTION 'approval task materials may only be inserted with their task';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER clm_approval_task_materials_insert_guard
    BEFORE INSERT ON clm_approval_task_materials
    FOR EACH ROW EXECUTE FUNCTION guard_clm_approval_task_material_insert();
CREATE TRIGGER clm_approval_task_materials_append_only
    BEFORE UPDATE OR DELETE ON clm_approval_task_materials
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

DO $migration$
BEGIN
    REVOKE ALL ON TABLE clm_approval_task_materials FROM PUBLIC;
    ALTER TABLE clm_approval_task_materials ENABLE ROW LEVEL SECURITY;
    ALTER TABLE clm_approval_task_materials FORCE ROW LEVEL SECURITY;
    CREATE POLICY tenant_boundary ON clm_approval_task_materials USING
        (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK
        (tenant_id = current_setting('app.tenant_id', true));
END $migration$;

-- A transfer is the only later source of a live assignment. The routing
-- snapshot stays immutable: the new row is admitted only when the append-only
-- transfer fact for this task and target was inserted by the same transaction.
CREATE OR REPLACE FUNCTION guard_clm_approval_assignment_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM clm_approval_tasks parent
        WHERE parent.tenant_id = NEW.tenant_id
          AND parent.contract_id = NEW.contract_id
          AND parent.approval_round_id = NEW.approval_round_id
          AND parent.approval_task_id = NEW.approval_task_id
          AND parent.xmin::text::bigint =
              (pg_current_xact_id()::text::bigint % 4294967296)
    ) OR EXISTS (
        SELECT 1 FROM clm_approval_task_transfers transfer
        WHERE transfer.tenant_id = NEW.tenant_id
          AND transfer.contract_id = NEW.contract_id
          AND transfer.approval_round_id = NEW.approval_round_id
          AND transfer.approval_task_id = NEW.approval_task_id
          AND transfer.to_user_id = NEW.user_id
          AND transfer.xmin::text::bigint =
              (pg_current_xact_id()::text::bigint % 4294967296)
    ) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'approval assignments may only be inserted with their task or a transfer';
END $$;

CREATE TRIGGER clm_approval_task_transfers_append_only
    BEFORE UPDATE OR DELETE ON clm_approval_task_transfers
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

DO $migration$
BEGIN
    REVOKE ALL ON TABLE clm_approval_task_transfers FROM PUBLIC;
    ALTER TABLE clm_approval_task_transfers ENABLE ROW LEVEL SECURITY;
    ALTER TABLE clm_approval_task_transfers FORCE ROW LEVEL SECURITY;
    CREATE POLICY tenant_boundary ON clm_approval_task_transfers USING
        (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK
        (tenant_id = current_setting('app.tenant_id', true));
END $migration$;
