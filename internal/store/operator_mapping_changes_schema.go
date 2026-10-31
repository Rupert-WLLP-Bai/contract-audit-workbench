package store

// operatorMappingChangesSchemaSQL is a separate additive schema batch for the
// append-only service-object mapping lineage and its two-person correction
// records.
const operatorMappingChangesSchemaSQL = `
ALTER TABLE operator_object_bindings
 ADD COLUMN IF NOT EXISTS mapping_id text NULL,
 ADD COLUMN IF NOT EXISTS mapping_version bigint NULL,
 ADD COLUMN IF NOT EXISTS service_object_id text NULL,
 ADD COLUMN IF NOT EXISTS object_version integer NULL,
 ADD COLUMN IF NOT EXISTS association_version integer NULL,
 ADD COLUMN IF NOT EXISTS mapping_change_decision_id text NULL,
 ADD COLUMN IF NOT EXISTS mapping_change_decision text NULL,
 ADD COLUMN IF NOT EXISTS response_status integer NULL,
 ADD COLUMN IF NOT EXISTS response_body bytea NULL;
DO $$ BEGIN
 ALTER TABLE operator_object_bindings DROP CONSTRAINT IF EXISTS operator_object_bindings_status_check;
 ALTER TABLE operator_object_bindings ADD CONSTRAINT operator_object_bindings_status_check
  CHECK(status IN ('confirmed','missing','ambiguous','inactive'));
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS operator_object_bindings_mapping_version_uq
 ON operator_object_bindings(tenant_id,org_id,case_id,mapping_id,mapping_version,binding_version);
CREATE UNIQUE INDEX IF NOT EXISTS operator_object_bindings_mapping_version_head_uq
 ON operator_object_bindings(tenant_id,org_id,case_id,mapping_id,mapping_version,binding_version,service_object_id,object_version,association_version);
CREATE UNIQUE INDEX IF NOT EXISTS operator_object_bindings_mapping_service_version_uq
 ON operator_object_bindings(tenant_id,org_id,case_id,mapping_id,mapping_version,binding_version,service_object_id);
CREATE TABLE IF NOT EXISTS operator_mapping_lineages (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 case_id text NOT NULL,
 mapping_id text NOT NULL,
 service_object_id text NOT NULL,
	current_version bigint NOT NULL CHECK(current_version>0),
 current_binding_version text NOT NULL,
 created_by text NOT NULL,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,org_id,case_id,mapping_id),
 UNIQUE(tenant_id,org_id,case_id,service_object_id),
 UNIQUE(tenant_id,org_id,case_id,mapping_id,service_object_id),
 FOREIGN KEY(tenant_id,org_id,case_id,service_object_id)
  REFERENCES service_objects(tenant_id,org_id,case_id,service_object_id),
 CHECK(updated_at>=created_at)
);

DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='operator_mapping_head_binding_fk' AND conrelid='operator_mapping_lineages'::regclass) THEN
  ALTER TABLE operator_mapping_lineages ADD CONSTRAINT operator_mapping_head_binding_fk
   FOREIGN KEY(tenant_id,org_id,case_id,mapping_id,current_version,current_binding_version,service_object_id)
   REFERENCES operator_object_bindings(tenant_id,org_id,case_id,mapping_id,mapping_version,binding_version,service_object_id)
   DEFERRABLE INITIALLY DEFERRED;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='operator_binding_mapping_lineage_fk' AND conrelid='operator_object_bindings'::regclass) THEN
  ALTER TABLE operator_object_bindings ADD CONSTRAINT operator_binding_mapping_lineage_fk
   FOREIGN KEY(tenant_id,org_id,case_id,mapping_id,service_object_id)
   REFERENCES operator_mapping_lineages(tenant_id,org_id,case_id,mapping_id,service_object_id)
   DEFERRABLE INITIALLY DEFERRED;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='operator_binding_service_association_fk' AND conrelid='operator_object_bindings'::regclass) THEN
  ALTER TABLE operator_object_bindings ADD CONSTRAINT operator_binding_service_association_fk
   FOREIGN KEY(tenant_id,org_id,case_id,service_object_id,association_version,object_version)
   REFERENCES service_object_contract_associations(tenant_id,org_id,case_id,service_object_id,association_version,object_version);
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='operator_binding_mapping_pin_null_group' AND conrelid='operator_object_bindings'::regclass) THEN
  ALTER TABLE operator_object_bindings ADD CONSTRAINT operator_binding_mapping_pin_null_group CHECK (
   (mapping_id IS NULL AND mapping_version IS NULL AND service_object_id IS NULL AND object_version IS NULL AND association_version IS NULL AND mapping_change_decision_id IS NULL AND mapping_change_decision IS NULL)
   OR
   (mapping_id IS NOT NULL AND mapping_version IS NOT NULL AND mapping_version>0
    AND service_object_id IS NOT NULL AND object_version IS NOT NULL AND object_version>0
    AND association_version IS NOT NULL AND association_version>0
    AND ((mapping_version=1 AND mapping_change_decision_id IS NULL AND mapping_change_decision IS NULL)
      OR (mapping_version>1 AND mapping_change_decision_id IS NOT NULL AND mapping_change_decision='approved')))
  );
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='operator_binding_response_receipt_pair' AND conrelid='operator_object_bindings'::regclass) THEN
  ALTER TABLE operator_object_bindings ADD CONSTRAINT operator_binding_response_receipt_pair CHECK (
   (response_status IS NULL AND response_body IS NULL)
   OR (response_status IS NOT NULL AND response_status=201 AND response_body IS NOT NULL AND octet_length(response_body)>0)
  );
 END IF;
END $$;

CREATE TABLE IF NOT EXISTS operator_mapping_change_requests (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 case_id text NOT NULL,
 request_id text NOT NULL,
 mapping_id text NOT NULL,
	service_object_id text NOT NULL,
	change_kind text NOT NULL CHECK(change_kind IN ('replace_mapping','retire_mapping','reactivate_mapping','correct_identity','correct_identity_and_mapping','retire_service_object')),
 expected_mapping_version bigint NOT NULL CHECK(expected_mapping_version IS NOT NULL AND expected_mapping_version>0),
 expected_binding_version text NOT NULL,
 expected_object_version integer NOT NULL CHECK(expected_object_version IS NOT NULL AND expected_object_version>0),
 expected_association_version integer NOT NULL CHECK(expected_association_version IS NOT NULL AND expected_association_version>0),
 before_json jsonb NOT NULL CHECK(jsonb_typeof(before_json)='object'),
 before_sha256 text NOT NULL CHECK(before_sha256 ~ '^[0-9a-f]{64}$'),
 after_json jsonb NOT NULL CHECK(jsonb_typeof(after_json)='object'),
 after_sha256 text NOT NULL CHECK(after_sha256 ~ '^[0-9a-f]{64}$'),
 patch_json jsonb NOT NULL CHECK(jsonb_typeof(patch_json)='object'),
 patch_sha256 text NOT NULL CHECK(patch_sha256 ~ '^[0-9a-f]{64}$'),
 basis_refs jsonb NOT NULL CHECK(jsonb_typeof(basis_refs)='array' AND jsonb_array_length(basis_refs) BETWEEN 1 AND 100),
 impact_sha256 text NOT NULL CHECK(impact_sha256 ~ '^[0-9a-f]{64}$'),
 proposer_user_id text NOT NULL,
 proposer_issuer text NOT NULL,
 proposer_subject text NOT NULL,
 actor_id text NOT NULL,
 idempotency_key text NOT NULL,
 request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
	response_status integer NOT NULL CHECK(response_status=201),
	response_body bytea NOT NULL CHECK(octet_length(response_body)>0),
 created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,org_id,case_id,request_id),
 UNIQUE(tenant_id,org_id,case_id,actor_id,idempotency_key),
 UNIQUE(tenant_id,org_id,case_id,request_id,mapping_id,service_object_id,expected_mapping_version,expected_binding_version,expected_object_version,expected_association_version),
 FOREIGN KEY(tenant_id,org_id,case_id,mapping_id)
  REFERENCES operator_mapping_lineages(tenant_id,org_id,case_id,mapping_id),
 FOREIGN KEY(tenant_id,org_id,case_id,mapping_id,service_object_id)
  REFERENCES operator_mapping_lineages(tenant_id,org_id,case_id,mapping_id,service_object_id),
 FOREIGN KEY(tenant_id,org_id,case_id,mapping_id,expected_mapping_version,expected_binding_version,service_object_id,expected_object_version,expected_association_version)
  REFERENCES operator_object_bindings(tenant_id,org_id,case_id,mapping_id,mapping_version,binding_version,service_object_id,object_version,association_version)
);

CREATE TABLE IF NOT EXISTS operator_mapping_change_decisions (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 case_id text NOT NULL,
 request_id text NOT NULL,
 decision_id text NOT NULL,
 mapping_id text NOT NULL,
 service_object_id text NOT NULL,
 expected_mapping_version bigint NOT NULL CHECK(expected_mapping_version IS NOT NULL AND expected_mapping_version>0),
 expected_binding_version text NOT NULL,
 expected_object_version integer NOT NULL CHECK(expected_object_version IS NOT NULL AND expected_object_version>0),
 expected_association_version integer NOT NULL CHECK(expected_association_version IS NOT NULL AND expected_association_version>0),
 decision text NOT NULL CHECK(decision IN ('approved','rejected')),
	reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 2000),
 decider_user_id text NOT NULL,
 decider_issuer text NOT NULL,
 decider_subject text NOT NULL,
 request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
 actor_id text NOT NULL,
 idempotency_key text NOT NULL,
 decision_request_sha256 text NOT NULL CHECK(decision_request_sha256 ~ '^[0-9a-f]{64}$'),
	response_status integer NOT NULL CHECK(response_status=201),
	response_body bytea NOT NULL CHECK(octet_length(response_body)>0),
 created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,org_id,case_id,decision_id),
 UNIQUE(tenant_id,org_id,case_id,decision_id,decision),
 UNIQUE(tenant_id,org_id,case_id,request_id),
 UNIQUE(tenant_id,org_id,case_id,actor_id,idempotency_key),
 FOREIGN KEY(tenant_id,org_id,case_id,request_id,mapping_id,service_object_id,expected_mapping_version,expected_binding_version,expected_object_version,expected_association_version)
  REFERENCES operator_mapping_change_requests(tenant_id,org_id,case_id,request_id,mapping_id,service_object_id,expected_mapping_version,expected_binding_version,expected_object_version,expected_association_version)
);

ALTER TABLE service_object_versions ADD COLUMN IF NOT EXISTS change_decision_outcome text NULL;
ALTER TABLE service_object_contract_associations ADD COLUMN IF NOT EXISTS identity_change_decision_outcome text NULL;

DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='service_object_version_decision_outcome_pair' AND conrelid='service_object_versions'::regclass) THEN
  ALTER TABLE service_object_versions ADD CONSTRAINT service_object_version_decision_outcome_pair CHECK (
   (change_decision_id IS NULL AND change_decision_outcome IS NULL)
   OR (change_decision_id IS NOT NULL AND change_decision_outcome IS NOT NULL AND change_decision_outcome='approved')
  );
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='service_object_association_decision_outcome_pair' AND conrelid='service_object_contract_associations'::regclass) THEN
  ALTER TABLE service_object_contract_associations ADD CONSTRAINT service_object_association_decision_outcome_pair CHECK (
   (identity_change_decision_id IS NULL AND identity_change_decision_outcome IS NULL)
   OR (identity_change_decision_id IS NOT NULL AND identity_change_decision_outcome IS NOT NULL AND identity_change_decision_outcome='approved')
  );
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='operator_binding_decision_outcome_pair' AND conrelid='operator_object_bindings'::regclass) THEN
  ALTER TABLE operator_object_bindings ADD CONSTRAINT operator_binding_decision_outcome_pair CHECK (
   (mapping_change_decision_id IS NULL AND mapping_change_decision IS NULL)
   OR (mapping_change_decision_id IS NOT NULL AND mapping_change_decision IS NOT NULL AND mapping_change_decision='approved')
  );
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='service_object_version_mapping_decision_fk' AND conrelid='service_object_versions'::regclass) THEN
  ALTER TABLE service_object_versions ADD CONSTRAINT service_object_version_mapping_decision_fk
   FOREIGN KEY(tenant_id,org_id,case_id,change_decision_id,change_decision_outcome)
   REFERENCES operator_mapping_change_decisions(tenant_id,org_id,case_id,decision_id,decision);
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='service_object_association_mapping_decision_fk' AND conrelid='service_object_contract_associations'::regclass) THEN
  ALTER TABLE service_object_contract_associations ADD CONSTRAINT service_object_association_mapping_decision_fk
   FOREIGN KEY(tenant_id,org_id,case_id,identity_change_decision_id,identity_change_decision_outcome)
   REFERENCES operator_mapping_change_decisions(tenant_id,org_id,case_id,decision_id,decision);
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='operator_binding_mapping_decision_fk' AND conrelid='operator_object_bindings'::regclass) THEN
  ALTER TABLE operator_object_bindings ADD CONSTRAINT operator_binding_mapping_decision_fk
   FOREIGN KEY(tenant_id,org_id,case_id,mapping_change_decision_id,mapping_change_decision)
   REFERENCES operator_mapping_change_decisions(tenant_id,org_id,case_id,decision_id,decision);
 END IF;
END $$;

CREATE OR REPLACE FUNCTION guard_operator_mapping_lineage_head() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.current_version IS DISTINCT FROM 1 THEN
   RAISE EXCEPTION 'operator mapping lineage must start at version 1';
  END IF;
  RETURN NEW;
 END IF;
 IF (NEW.tenant_id,NEW.org_id,NEW.case_id,NEW.mapping_id,NEW.service_object_id,NEW.created_by,NEW.created_at)
    IS DISTINCT FROM (OLD.tenant_id,OLD.org_id,OLD.case_id,OLD.mapping_id,OLD.service_object_id,OLD.created_by,OLD.created_at) THEN
  RAISE EXCEPTION 'operator mapping lineage identity is immutable';
 END IF;
 IF NEW.current_version IS DISTINCT FROM OLD.current_version OR NEW.current_binding_version IS DISTINCT FROM OLD.current_binding_version THEN
  IF NEW.current_version IS DISTINCT FROM OLD.current_version+1 OR NEW.current_binding_version IS NOT DISTINCT FROM OLD.current_binding_version THEN
   RAISE EXCEPTION 'operator mapping head must advance exactly one binding version';
  END IF;
  IF NOT EXISTS (
   SELECT 1 FROM operator_object_bindings b
   WHERE (b.tenant_id,b.org_id,b.case_id,b.mapping_id,b.mapping_version,b.binding_version,b.service_object_id)=
         (NEW.tenant_id,NEW.org_id,NEW.case_id,NEW.mapping_id,NEW.current_version,NEW.current_binding_version,NEW.service_object_id)
  ) THEN RAISE EXCEPTION 'operator mapping head target missing'; END IF;
 END IF;
 IF NEW.updated_at<OLD.updated_at THEN RAISE EXCEPTION 'operator mapping head time cannot move backwards'; END IF;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION forbid_operator_mapping_lineage_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'operator mapping lineage cannot be deleted'; END $$;

DROP TRIGGER IF EXISTS operator_mapping_lineages_head_guard ON operator_mapping_lineages;
CREATE TRIGGER operator_mapping_lineages_head_guard BEFORE INSERT OR UPDATE ON operator_mapping_lineages
 FOR EACH ROW EXECUTE FUNCTION guard_operator_mapping_lineage_head();
DROP TRIGGER IF EXISTS operator_mapping_lineages_no_delete ON operator_mapping_lineages;
CREATE TRIGGER operator_mapping_lineages_no_delete BEFORE DELETE ON operator_mapping_lineages
 FOR EACH ROW EXECUTE FUNCTION forbid_operator_mapping_lineage_delete();
DROP TRIGGER IF EXISTS operator_mapping_change_requests_append_only ON operator_mapping_change_requests;
CREATE TRIGGER operator_mapping_change_requests_append_only BEFORE UPDATE OR DELETE ON operator_mapping_change_requests
 FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS operator_mapping_change_decisions_append_only ON operator_mapping_change_decisions;
CREATE TRIGGER operator_mapping_change_decisions_append_only BEFORE UPDATE OR DELETE ON operator_mapping_change_decisions
 FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

ALTER TABLE operator_mapping_lineages ENABLE ROW LEVEL SECURITY;
ALTER TABLE operator_mapping_lineages FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_boundary ON operator_mapping_lineages;
CREATE POLICY tenant_boundary ON operator_mapping_lineages
 USING (tenant_id=current_setting('app.tenant_id',true)) WITH CHECK (tenant_id=current_setting('app.tenant_id',true));
ALTER TABLE operator_mapping_change_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE operator_mapping_change_requests FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_boundary ON operator_mapping_change_requests;
CREATE POLICY tenant_boundary ON operator_mapping_change_requests
 USING (tenant_id=current_setting('app.tenant_id',true)) WITH CHECK (tenant_id=current_setting('app.tenant_id',true));
ALTER TABLE operator_mapping_change_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE operator_mapping_change_decisions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_boundary ON operator_mapping_change_decisions;
CREATE POLICY tenant_boundary ON operator_mapping_change_decisions
 USING (tenant_id=current_setting('app.tenant_id',true)) WITH CHECK (tenant_id=current_setting('app.tenant_id',true));
`
