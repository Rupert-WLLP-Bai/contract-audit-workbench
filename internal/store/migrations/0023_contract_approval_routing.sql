-- CLM-07: versioned countersign templates, tenant role bindings, and the
-- routing snapshot frozen when a contract is submitted for approval.
--
-- A submitted round keeps its own template id/version, routing snapshot and
-- digest. Publishing a newer template or rebinding a role only changes what a
-- later submission freezes; it never rewrites an existing round or task.
--
-- The seven task rows are created together with their round in the submission
-- transaction. A task can only be inserted by that transaction, and its
-- identity columns never change. Approval decisions are appended by CLM-08.

ALTER TABLE clm_approval_rounds
    ADD COLUMN routing_digest text CHECK (routing_digest IS NULL OR routing_digest ~ '^[a-f0-9]{64}$');

CREATE TABLE clm_workflow_templates (
    tenant_id text NOT NULL CHECK (length(btrim(tenant_id)) > 0),
    template_id text NOT NULL CHECK (template_id ~ '^[a-z][a-z0-9._-]{2,63}$'),
    template_version text NOT NULL CHECK (template_version ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$'),
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    start_action text NOT NULL CHECK (start_action = 'submit_contract'),
    start_action_is_approval_node boolean NOT NULL CHECK (NOT start_action_is_approval_node),
    execution text NOT NULL CHECK (execution = 'sequential'),
    node_count integer NOT NULL CHECK (node_count = 7),
    template_digest text NOT NULL CHECK (template_digest ~ '^[a-f0-9]{64}$'),
    published_by text NOT NULL CHECK (length(btrim(published_by)) > 0),
    published_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, template_id, template_version)
);
CREATE TRIGGER clm_workflow_templates_append_only BEFORE UPDATE OR DELETE ON clm_workflow_templates
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

CREATE TABLE clm_workflow_template_nodes (
    tenant_id text NOT NULL,
    template_id text NOT NULL,
    template_version text NOT NULL,
    node_code text NOT NULL CHECK (length(btrim(node_code)) > 0),
    node_order integer NOT NULL CHECK (node_order BETWEEN 1 AND 7),
    title text NOT NULL CHECK (length(btrim(title)) > 0),
    role_code text NOT NULL CHECK (role_code ~ '^[a-z][a-z0-9_]{2,63}$'),
    is_final boolean NOT NULL,
    PRIMARY KEY (tenant_id, template_id, template_version, node_code),
    UNIQUE (tenant_id, template_id, template_version, node_order),
    UNIQUE (tenant_id, template_id, template_version, role_code),
    FOREIGN KEY (tenant_id, template_id, template_version)
        REFERENCES clm_workflow_templates (tenant_id, template_id, template_version),
    CHECK (is_final = (node_order = 7))
);
CREATE TRIGGER clm_workflow_template_nodes_append_only BEFORE UPDATE OR DELETE ON clm_workflow_template_nodes
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

-- The default pointer selects the template version used by new submissions.
-- Only one default pointer exists per tenant; older versions stay readable.
CREATE TABLE clm_workflow_template_pointers (
    tenant_id text NOT NULL,
    pointer_key text NOT NULL DEFAULT 'default' CHECK (pointer_key = 'default'),
    template_id text NOT NULL,
    template_version text NOT NULL,
    updated_by text NOT NULL CHECK (length(btrim(updated_by)) > 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, pointer_key),
    FOREIGN KEY (tenant_id, template_id, template_version)
        REFERENCES clm_workflow_templates (tenant_id, template_id, template_version)
);

-- Role bindings are administrative configuration, not approval evidence. A
-- binding with org_scope='' applies tenant-wide; any other value applies to the
-- exact owner organization only. Routing resolves both at submission time.
CREATE TABLE clm_workflow_roles (
    tenant_id text NOT NULL,
    role_code text NOT NULL CHECK (role_code ~ '^[a-z][a-z0-9_]{2,63}$'),
    row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
    updated_by text NOT NULL CHECK (length(btrim(updated_by)) > 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, role_code)
);

CREATE TABLE clm_workflow_role_bindings (
    tenant_id text NOT NULL,
    role_code text NOT NULL,
    org_scope text NOT NULL DEFAULT '',
    user_id text NOT NULL CHECK (length(btrim(user_id)) > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, role_code, org_scope, user_id),
    FOREIGN KEY (tenant_id, role_code) REFERENCES clm_workflow_roles (tenant_id, role_code) ON DELETE CASCADE
);
CREATE INDEX clm_workflow_role_bindings_user
    ON clm_workflow_role_bindings (tenant_id, user_id, role_code);

CREATE TABLE clm_approval_tasks (
    tenant_id text NOT NULL,
    contract_id uuid NOT NULL,
    approval_round_id uuid NOT NULL,
    approval_task_id uuid NOT NULL,
    node_code text NOT NULL CHECK (length(btrim(node_code)) > 0),
    node_order integer NOT NULL CHECK (node_order > 0),
    title text NOT NULL CHECK (length(btrim(title)) > 0),
    role_code text NOT NULL CHECK (role_code ~ '^[a-z][a-z0-9_]{2,63}$'),
    is_final boolean NOT NULL,
    state text NOT NULL CHECK (state IN ('pending', 'active', 'approved', 'returned', 'cancelled')),
    row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
    activated_at timestamptz,
    decided_at timestamptz,
    decided_by text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, contract_id, approval_round_id, approval_task_id),
    UNIQUE (tenant_id, approval_task_id),
    UNIQUE (tenant_id, contract_id, approval_round_id, node_order),
    UNIQUE (tenant_id, contract_id, approval_round_id, node_code),
    FOREIGN KEY (tenant_id, contract_id, approval_round_id)
        REFERENCES clm_approval_rounds (tenant_id, contract_id, approval_round_id),
    CHECK (
        (state = 'pending' AND activated_at IS NULL AND decided_at IS NULL AND decided_by IS NULL)
        OR (state = 'active' AND activated_at IS NOT NULL AND decided_at IS NULL AND decided_by IS NULL)
        OR (state IN ('approved', 'returned', 'cancelled') AND decided_at IS NOT NULL AND decided_by IS NOT NULL)
    )
);
CREATE UNIQUE INDEX clm_one_active_task_per_round
    ON clm_approval_tasks (tenant_id, contract_id, approval_round_id) WHERE state = 'active';
CREATE INDEX clm_approval_tasks_by_round
    ON clm_approval_tasks (tenant_id, contract_id, approval_round_id, node_order);

CREATE TABLE clm_approval_task_assignments (
    tenant_id text NOT NULL,
    contract_id uuid NOT NULL,
    approval_round_id uuid NOT NULL,
    approval_task_id uuid NOT NULL,
    user_id text NOT NULL CHECK (length(btrim(user_id)) > 0),
    display_name text NOT NULL CHECK (length(btrim(display_name)) > 0),
    role_code text NOT NULL CHECK (role_code ~ '^[a-z][a-z0-9_]{2,63}$'),
    binding_org_scope text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, contract_id, approval_round_id, approval_task_id, user_id),
    FOREIGN KEY (tenant_id, contract_id, approval_round_id, approval_task_id)
        REFERENCES clm_approval_tasks (tenant_id, contract_id, approval_round_id, approval_task_id)
);
CREATE TRIGGER clm_approval_task_assignments_append_only
    BEFORE UPDATE OR DELETE ON clm_approval_task_assignments
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

-- A template node belongs to the transaction that created its template
-- version. Role rebinding uses DELETE+INSERT on the binding table and never
-- touches the frozen routing snapshot.
CREATE OR REPLACE FUNCTION guard_clm_workflow_template_node_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM clm_workflow_templates parent
        WHERE parent.tenant_id = NEW.tenant_id
          AND parent.template_id = NEW.template_id
          AND parent.template_version = NEW.template_version
          AND parent.xmin::text::bigint =
              (pg_current_xact_id()::text::bigint % 4294967296)
    ) THEN
        RAISE EXCEPTION 'workflow template nodes may only be inserted with their template version';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER clm_workflow_template_nodes_insert_guard
    BEFORE INSERT ON clm_workflow_template_nodes
    FOR EACH ROW EXECUTE FUNCTION guard_clm_workflow_template_node_insert();

-- Tasks are the immutable routing skeleton of one round. Besides preventing a
-- later transaction from appending tasks, the update guard keeps node and role
-- identity fixed and allows only the sequential state transitions CLM-08 uses.
CREATE OR REPLACE FUNCTION guard_clm_approval_task_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM clm_approval_rounds parent
        WHERE parent.tenant_id = NEW.tenant_id
          AND parent.contract_id = NEW.contract_id
          AND parent.approval_round_id = NEW.approval_round_id
          AND parent.xmin::text::bigint =
              (pg_current_xact_id()::text::bigint % 4294967296)
    ) THEN
        RAISE EXCEPTION 'approval tasks may only be inserted with their round';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER clm_approval_tasks_insert_guard
    BEFORE INSERT ON clm_approval_tasks
    FOR EACH ROW EXECUTE FUNCTION guard_clm_approval_task_insert();

CREATE OR REPLACE FUNCTION guard_clm_approval_task_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id <> OLD.tenant_id OR NEW.contract_id <> OLD.contract_id
       OR NEW.approval_round_id <> OLD.approval_round_id
       OR NEW.approval_task_id <> OLD.approval_task_id
       OR NEW.node_code <> OLD.node_code OR NEW.node_order <> OLD.node_order
       OR NEW.title <> OLD.title OR NEW.role_code <> OLD.role_code
       OR NEW.is_final <> OLD.is_final OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'approval task identity is immutable';
    END IF;
    IF NEW.row_version <> OLD.row_version + 1 THEN
        RAISE EXCEPTION 'approval task row_version must advance by one';
    END IF;
    IF OLD.state = 'pending' AND NEW.state NOT IN ('active', 'cancelled') THEN
        RAISE EXCEPTION 'invalid pending approval task transition';
    END IF;
    IF OLD.state = 'active' AND NEW.state NOT IN ('approved', 'returned', 'cancelled') THEN
        RAISE EXCEPTION 'invalid active approval task transition';
    END IF;
    IF OLD.state IN ('approved', 'returned', 'cancelled') THEN
        RAISE EXCEPTION 'closed approval task is immutable';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER clm_approval_tasks_update_guard
    BEFORE UPDATE ON clm_approval_tasks
    FOR EACH ROW EXECUTE FUNCTION guard_clm_approval_task_update();

CREATE OR REPLACE FUNCTION guard_clm_approval_assignment_insert() RETURNS trigger
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
        RAISE EXCEPTION 'approval assignments may only be inserted with their task';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER clm_approval_task_assignments_insert_guard
    BEFORE INSERT ON clm_approval_task_assignments
    FOR EACH ROW EXECUTE FUNCTION guard_clm_approval_assignment_insert();

-- A round's template, routing snapshot and initiator are frozen at submission.
-- CLM-08 may only advance row_version or close the round.
CREATE OR REPLACE FUNCTION guard_clm_approval_round_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id <> OLD.tenant_id OR NEW.contract_id <> OLD.contract_id
       OR NEW.approval_round_id <> OLD.approval_round_id
       OR NEW.contract_revision_id <> OLD.contract_revision_id
       OR NEW.round_number <> OLD.round_number
       OR NEW.workflow_template_id <> OLD.workflow_template_id
       OR NEW.workflow_template_version <> OLD.workflow_template_version
       OR NEW.routing_snapshot <> OLD.routing_snapshot
       OR NEW.risk_disposition_snapshot <> OLD.risk_disposition_snapshot
       OR COALESCE(NEW.routing_digest, '') <> COALESCE(OLD.routing_digest, '')
       OR NEW.initiated_by <> OLD.initiated_by OR NEW.started_at <> OLD.started_at THEN
        RAISE EXCEPTION 'approval round snapshot is immutable';
    END IF;
    IF NEW.row_version <> OLD.row_version + 1 THEN
        RAISE EXCEPTION 'approval round row_version must advance by one';
    END IF;
    IF OLD.state = 'in_review' AND NEW.state = 'in_review' THEN
        IF NEW.ended_at IS NOT NULL THEN
            RAISE EXCEPTION 'open approval round cannot have ended_at';
        END IF;
    ELSIF OLD.state = 'in_review' AND NEW.state IN ('returned', 'withdrawn', 'approved', 'terminated') THEN
        IF NEW.ended_at IS NULL THEN
            RAISE EXCEPTION 'closed approval round requires ended_at';
        END IF;
    ELSE
        RAISE EXCEPTION 'invalid approval round transition';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER clm_approval_rounds_update_guard
    BEFORE UPDATE ON clm_approval_rounds
    FOR EACH ROW EXECUTE FUNCTION guard_clm_approval_round_update();

DO $migration$
DECLARE table_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'clm_workflow_templates', 'clm_workflow_template_nodes', 'clm_workflow_template_pointers',
        'clm_workflow_roles', 'clm_workflow_role_bindings',
        'clm_approval_tasks', 'clm_approval_task_assignments'
    ] LOOP
        EXECUTE format('REVOKE ALL ON TABLE %I FROM PUBLIC', table_name);
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', table_name);
        EXECUTE format('CREATE POLICY tenant_boundary ON %I USING '
            || '(tenant_id=current_setting(''app.tenant_id'',true)) WITH CHECK '
            || '(tenant_id=current_setting(''app.tenant_id'',true))', table_name);
    END LOOP;
END $migration$;
