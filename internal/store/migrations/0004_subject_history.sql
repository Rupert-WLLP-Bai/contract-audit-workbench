-- Subject history and tenant protections.

CREATE TABLE IF NOT EXISTS subjects (
 tenant_id text NOT NULL, subject_id text NOT NULL, current_version bigint NOT NULL CHECK (current_version > 0),
 status text NOT NULL CHECK (status IN ('pending_verification','verified','inactive')),
 created_by_user_id text NOT NULL, created_by_issuer text NOT NULL, created_by_subject text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,subject_id)
);
CREATE TABLE IF NOT EXISTS subject_versions (
 tenant_id text NOT NULL, subject_id text NOT NULL, version bigint NOT NULL CHECK (version > 0),
 payload jsonb NOT NULL, payload_hash text NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,subject_id,version),
 FOREIGN KEY (tenant_id,subject_id) REFERENCES subjects(tenant_id,subject_id)
);
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='subjects_current_version_fk' AND conrelid='subjects'::regclass) THEN
  ALTER TABLE subjects ADD CONSTRAINT subjects_current_version_fk
   FOREIGN KEY (tenant_id,subject_id,current_version) REFERENCES subject_versions(tenant_id,subject_id,version)
   DEFERRABLE INITIALLY DEFERRED;
 END IF;
END $$;
CREATE TABLE IF NOT EXISTS subject_current_identifier_claims (
 tenant_id text NOT NULL, subject_id text NOT NULL, namespace text NOT NULL, identifier_type text NOT NULL,
 value text NOT NULL, verification_state text NOT NULL CHECK (verification_state IN ('pending_verification','verified')),
 verification_scope text NOT NULL DEFAULT '', source_catalog_id text NOT NULL DEFAULT '', source_snapshot_id text NOT NULL DEFAULT '',
 source_digest text NOT NULL DEFAULT '', source_time timestamptz, source_valid_from timestamptz, source_valid_until timestamptz,
 PRIMARY KEY (tenant_id,subject_id,namespace,identifier_type),
 FOREIGN KEY (tenant_id,subject_id) REFERENCES subjects(tenant_id,subject_id),
 CHECK ((verification_scope='synthetic_controlled' AND source_catalog_id<>'') OR verification_scope<>'synthetic_controlled')
);
CREATE UNIQUE INDEX IF NOT EXISTS subject_synthetic_identifier_unique ON subject_current_identifier_claims
 (tenant_id,source_catalog_id,namespace,identifier_type,value)
 WHERE verification_state='verified' AND verification_scope='synthetic_controlled';
CREATE TABLE IF NOT EXISTS case_subject_roles (
 tenant_id text NOT NULL, org_id text NOT NULL, case_id text NOT NULL, binding_id text NOT NULL,
 current_version bigint NOT NULL CHECK (current_version > 0),
 created_by_user_id text NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,binding_id),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases(tenant_id,org_id,id)
);
-- Preserve any historical role column in an older unversioned database.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='case_subject_roles'::regclass AND attname='role' AND attnum>0 AND NOT attisdropped) THEN
  ALTER TABLE case_subject_roles ALTER COLUMN role DROP NOT NULL;
 END IF;
END $$;
CREATE TABLE IF NOT EXISTS case_subject_role_versions (
 tenant_id text NOT NULL, org_id text NOT NULL, case_id text NOT NULL, binding_id text NOT NULL,
 version bigint NOT NULL CHECK (version > 0), subject_id text NOT NULL, subject_version bigint NOT NULL CHECK (subject_version > 0),
 role text NOT NULL CHECK (role IN ('customer','supplier','contractor','service_provider','other')),
 binding_state text NOT NULL CHECK (binding_state IN ('active','inactive','pending_confirmation')),
 source_kind text NOT NULL, payload jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,binding_id,version),
 FOREIGN KEY (tenant_id,org_id,case_id,binding_id) REFERENCES case_subject_roles(tenant_id,org_id,case_id,binding_id),
 FOREIGN KEY (tenant_id,subject_id,subject_version) REFERENCES subject_versions(tenant_id,subject_id,version)
);
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='case_subject_roles_current_version_fk' AND conrelid='case_subject_roles'::regclass) THEN
  ALTER TABLE case_subject_roles ADD CONSTRAINT case_subject_roles_current_version_fk
   FOREIGN KEY (tenant_id,org_id,case_id,binding_id,current_version)
   REFERENCES case_subject_role_versions(tenant_id,org_id,case_id,binding_id,version)
   DEFERRABLE INITIALLY DEFERRED;
 END IF;
END $$;
CREATE INDEX IF NOT EXISTS case_subject_role_current_subject ON case_subject_role_versions(tenant_id,subject_id,subject_version)
 WHERE binding_state <> 'inactive';
CREATE TABLE IF NOT EXISTS subject_change_requests (
 tenant_id text NOT NULL, org_id text NOT NULL, source_case_id text NOT NULL, request_id text NOT NULL,
 target_kind text NOT NULL CHECK (target_kind IN ('subject','case_subject_role')),
 target_subject_id text NOT NULL, target_binding_id text, expected_subject_version bigint NOT NULL,
 expected_binding_version bigint, payload jsonb NOT NULL, payload_hash text NOT NULL,
 affected_bindings_hash text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,request_id),
 FOREIGN KEY (tenant_id,org_id,source_case_id) REFERENCES cases(tenant_id,org_id,id),
 CHECK ((target_kind='subject' AND target_binding_id IS NULL AND expected_binding_version IS NULL) OR
        (target_kind='case_subject_role' AND target_binding_id IS NOT NULL AND expected_binding_version IS NOT NULL))
);
-- Preserve any historical state column in an older unversioned database.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='subject_change_requests'::regclass AND attname='state' AND attnum>0 AND NOT attisdropped) THEN
  ALTER TABLE subject_change_requests ALTER COLUMN state DROP NOT NULL;
 END IF;
END $$;
CREATE TABLE IF NOT EXISTS subject_change_decisions (
 tenant_id text NOT NULL, request_id text NOT NULL, decision_id text NOT NULL,
 decision text NOT NULL CHECK (decision IN ('approve','reject')), actor_user_id text NOT NULL,
 actor_issuer text NOT NULL, actor_subject text NOT NULL, payload jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,decision_id), UNIQUE (tenant_id,request_id),
 FOREIGN KEY (tenant_id,request_id) REFERENCES subject_change_requests(tenant_id,request_id)
);
CREATE TABLE IF NOT EXISTS subject_write_idempotency (
 tenant_id text NOT NULL, actor_user_id text NOT NULL, operation text NOT NULL,
 target_id text NOT NULL, idempotency_key text NOT NULL, payload_hash text NOT NULL,
 http_status integer NOT NULL, response jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,actor_user_id,operation,target_id,idempotency_key)
);
CREATE TABLE IF NOT EXISTS subject_source_snapshots (
 tenant_id text NOT NULL, org_id text NOT NULL, case_id text NOT NULL, snapshot_id text NOT NULL,
 source_catalog_id text NOT NULL, source_version integer NOT NULL CHECK (source_version > 0),
 source_payload text NOT NULL, source_digest text NOT NULL, source_scope text NOT NULL CHECK (source_scope='synthetic_controlled'),
 captured_at timestamptz NOT NULL DEFAULT clock_timestamp(), created_by_user_id text NOT NULL,
 created_by_issuer text NOT NULL, created_by_subject text NOT NULL,
 PRIMARY KEY (tenant_id,org_id,case_id,snapshot_id),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases(tenant_id,org_id,id)
);
CREATE OR REPLACE FUNCTION forbid_subject_append_only_rewrite() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'append-only'; END $$;
DO $$ DECLARE table_name text; trigger_name text; BEGIN
 FOREACH table_name IN ARRAY ARRAY['subject_versions','case_subject_role_versions','subject_change_requests','subject_change_decisions','subject_write_idempotency','subject_source_snapshots'] LOOP
  trigger_name := table_name || '_append_only';
  EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I', trigger_name, table_name);
  EXECUTE format('CREATE TRIGGER %I BEFORE UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION forbid_subject_append_only_rewrite()', trigger_name, table_name);
 END LOOP;
END $$;

DO $migration$ DECLARE table_name text; BEGIN
 FOREACH table_name IN ARRAY ARRAY['subjects','subject_versions','subject_current_identifier_claims','case_subject_roles','case_subject_role_versions','subject_change_requests','subject_change_decisions','subject_write_idempotency','subject_source_snapshots'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',table_name);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',table_name);
  EXECUTE format('DROP POLICY IF EXISTS tenant_boundary ON %I',table_name);
  EXECUTE format('CREATE POLICY tenant_boundary ON %I USING (tenant_id=current_setting(''app.tenant_id'',true)) WITH CHECK (tenant_id=current_setting(''app.tenant_id'',true))',table_name);
 END LOOP;
END $migration$;
