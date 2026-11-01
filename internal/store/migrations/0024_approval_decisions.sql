-- CLM-08: append-only approval decisions for sequential countersign.
--
-- A decision is evidence, not a projection. It is inserted while its task is
-- the single active task of an in_review round and the deciding user is one of
-- the frozen assignees; the task and round state changes happen in the same
-- transaction. Later rounds never update these rows, and every round keeps its
-- own task/decision history.

CREATE TABLE clm_approval_decisions (
    tenant_id text NOT NULL,
    decision_id uuid NOT NULL,
    contract_id uuid NOT NULL,
    approval_round_id uuid NOT NULL,
    approval_task_id uuid NOT NULL,
    contract_revision_id uuid NOT NULL,
    node_code text NOT NULL CHECK (length(btrim(node_code)) > 0),
    node_order integer NOT NULL CHECK (node_order > 0),
    role_code text NOT NULL CHECK (role_code ~ '^[a-z][a-z0-9_]{2,63}$'),
    decision text NOT NULL CHECK (decision IN ('approve', 'return')),
    comment text NOT NULL DEFAULT '' CHECK (char_length(comment) <= 2000),
    decided_by text NOT NULL CHECK (length(btrim(decided_by)) > 0),
    decided_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    round_row_version_after bigint NOT NULL CHECK (round_row_version_after > 0),
    workflow_state_after text NOT NULL CHECK (workflow_state_after IN ('in_review', 'approved', 'returned')),
    PRIMARY KEY (tenant_id, decision_id),
    UNIQUE (tenant_id, approval_task_id),
    FOREIGN KEY (tenant_id, contract_id, approval_round_id, approval_task_id)
        REFERENCES clm_approval_tasks (tenant_id, contract_id, approval_round_id, approval_task_id),
    FOREIGN KEY (tenant_id, contract_id, approval_round_id)
        REFERENCES clm_approval_rounds (tenant_id, contract_id, approval_round_id),
    CHECK ((decision = 'return' AND workflow_state_after = 'returned')
        OR (decision = 'approve' AND workflow_state_after IN ('in_review', 'approved'))),
    CHECK (decision <> 'return' OR length(btrim(comment)) > 0)
);

CREATE INDEX clm_approval_decisions_by_round
    ON clm_approval_decisions (tenant_id, contract_id, approval_round_id, node_order);

CREATE TRIGGER clm_approval_decisions_append_only
    BEFORE UPDATE OR DELETE ON clm_approval_decisions
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

-- The deciding transaction must be acting on the single active task, while the
-- round is still in review, with a frozen assignment for the caller. The
-- round_row_version_after must be the next version, so a stale command cannot
-- append a decision under an old round state.
CREATE OR REPLACE FUNCTION guard_clm_approval_decision_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM clm_approval_tasks task
        JOIN clm_approval_rounds round
          ON round.tenant_id = task.tenant_id
         AND round.contract_id = task.contract_id
         AND round.approval_round_id = task.approval_round_id
        JOIN clm_approval_task_assignments assignment
          ON assignment.tenant_id = task.tenant_id
         AND assignment.contract_id = task.contract_id
         AND assignment.approval_round_id = task.approval_round_id
         AND assignment.approval_task_id = task.approval_task_id
        WHERE task.tenant_id = NEW.tenant_id
          AND task.contract_id = NEW.contract_id
          AND task.approval_round_id = NEW.approval_round_id
          AND task.approval_task_id = NEW.approval_task_id
          AND task.state = 'active'
          AND task.node_code = NEW.node_code
          AND task.node_order = NEW.node_order
          AND task.role_code = NEW.role_code
          AND round.state = 'in_review'
          AND round.contract_revision_id = NEW.contract_revision_id
          AND round.row_version + 1 = NEW.round_row_version_after
          AND assignment.user_id = NEW.decided_by
    ) THEN
        RAISE EXCEPTION 'approval decision requires the active task, in-review round and frozen assignee';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER clm_approval_decisions_insert_guard
    BEFORE INSERT ON clm_approval_decisions
    FOR EACH ROW EXECUTE FUNCTION guard_clm_approval_decision_insert();

DO $migration$
BEGIN
    REVOKE ALL ON TABLE clm_approval_decisions FROM PUBLIC;
    ALTER TABLE clm_approval_decisions ENABLE ROW LEVEL SECURITY;
    ALTER TABLE clm_approval_decisions FORCE ROW LEVEL SECURITY;
    CREATE POLICY tenant_boundary ON clm_approval_decisions USING
        (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK
        (tenant_id = current_setting('app.tenant_id', true));
END $migration$;
