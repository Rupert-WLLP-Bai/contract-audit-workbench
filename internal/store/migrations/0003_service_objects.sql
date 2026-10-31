-- Service objects require frozen material versions; mapping lineage follows them.

CREATE UNIQUE INDEX IF NOT EXISTS service_object_material_version_document_uq
 ON material_versions(tenant_id,org_id,case_id,material_id,version,document_id);
CREATE UNIQUE INDEX IF NOT EXISTS service_object_attempt_input_run_attempt_uq
 ON review_attempt_inputs(run_id,attempt_id);
CREATE UNIQUE INDEX IF NOT EXISTS service_object_parse_selection_tuple_uq
 ON review_attempt_parse_selections(run_id,attempt_id,document_id,parsed_document_id);

CREATE TABLE IF NOT EXISTS service_objects (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 case_id text NOT NULL,
 service_object_id text NOT NULL,
 current_version integer NOT NULL CHECK(current_version>0),
 current_association_version integer NULL CHECK(current_association_version IS NULL OR current_association_version>0),
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,org_id,case_id,service_object_id),
 FOREIGN KEY(tenant_id,org_id,case_id) REFERENCES cases(tenant_id,org_id,id)
);

CREATE TABLE IF NOT EXISTS service_object_versions (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 case_id text NOT NULL,
 service_object_id text NOT NULL,
 version integer NOT NULL CHECK(version>0),
 object_type text NOT NULL CHECK(object_type IN ('dedicated_line','service')),
 display_name text NOT NULL CHECK(length(display_name)>0 AND length(display_name)<=256),
 business_code text NULL,
 aliases jsonb NOT NULL CHECK(jsonb_typeof(aliases)='array'),
 state text NOT NULL CHECK(state IN ('pending_confirmation','confirmed','inactive')),
 effective_from timestamptz NOT NULL,
 change_decision_id text NULL,
 basis_refs jsonb NOT NULL CHECK(jsonb_typeof(basis_refs)='array'),
 basis_summary text NULL,
 payload_sha256 text NOT NULL CHECK(payload_sha256 ~ '^[0-9a-f]{64}$'),
 created_by text NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,org_id,case_id,service_object_id,version),
 FOREIGN KEY(tenant_id,org_id,case_id,service_object_id)
   REFERENCES service_objects(tenant_id,org_id,case_id,service_object_id),
 CHECK(state<>'inactive' OR change_decision_id IS NOT NULL)
);

CREATE TABLE IF NOT EXISTS service_object_contract_associations (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 case_id text NOT NULL,
 service_object_id text NOT NULL,
 association_version integer NOT NULL CHECK(association_version>0),
 object_version integer NOT NULL CHECK(object_version>0),
 material_id text NOT NULL,
 material_version integer NOT NULL CHECK(material_version>0),
 document_id text NOT NULL,
 parsed_document_id text NOT NULL,
 source_run_id text NOT NULL,
 source_attempt_id text NOT NULL,
 object_ref text NOT NULL,
 facts_canonical_json bytea NOT NULL CHECK(octet_length(facts_canonical_json)>0),
 facts_sha256 text NOT NULL CHECK(facts_sha256 ~ '^[0-9a-f]{64}$'),
 basis_refs jsonb NOT NULL CHECK(jsonb_typeof(basis_refs)='array'),
 identity_change_decision_id text NULL,
 payload_sha256 text NOT NULL CHECK(payload_sha256 ~ '^[0-9a-f]{64}$'),
 confirmed_by text NOT NULL,
 confirmed_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,org_id,case_id,service_object_id,association_version),
 UNIQUE(tenant_id,org_id,case_id,service_object_id,association_version,object_version),
 CONSTRAINT service_object_associations_object_version_fk
 FOREIGN KEY(tenant_id,org_id,case_id,service_object_id,object_version)
   REFERENCES service_object_versions(tenant_id,org_id,case_id,service_object_id,version),
 CONSTRAINT service_object_associations_material_version_document_fk
 FOREIGN KEY(tenant_id,org_id,case_id,material_id,material_version,document_id)
   REFERENCES material_versions(tenant_id,org_id,case_id,material_id,version,document_id),
 CONSTRAINT service_object_associations_source_run_case_fk
 FOREIGN KEY(tenant_id,org_id,case_id,source_run_id)
   REFERENCES review_runs(tenant_id,org_id,case_id,id),
 CONSTRAINT service_object_associations_source_attempt_run_fk
 FOREIGN KEY(tenant_id,org_id,source_run_id,source_attempt_id)
   REFERENCES review_attempts(tenant_id,org_id,run_id,id),
 CONSTRAINT service_object_associations_attempt_input_fk
 FOREIGN KEY(source_run_id,source_attempt_id)
   REFERENCES review_attempt_inputs(run_id,attempt_id),
 CONSTRAINT service_object_associations_parse_selection_fk
 FOREIGN KEY(source_run_id,source_attempt_id,document_id,parsed_document_id)
   REFERENCES review_attempt_parse_selections(run_id,attempt_id,document_id,parsed_document_id)
);

CREATE TABLE IF NOT EXISTS service_object_mutation_receipts (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 case_id text NOT NULL,
 actor_id text NOT NULL,
 operation text NOT NULL CHECK(operation IN ('create_object','confirm_association','identity_change')),
 idempotency_key text NOT NULL,
 request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
 response_status integer NOT NULL CHECK(response_status BETWEEN 200 AND 299),
 response_body bytea NOT NULL,
 entity_id text NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,org_id,case_id,actor_id,operation,idempotency_key),
 FOREIGN KEY(tenant_id,org_id,case_id) REFERENCES cases(tenant_id,org_id,id)
);

DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='service_objects_current_version_fk' AND conrelid='service_objects'::regclass) THEN
  ALTER TABLE service_objects ADD CONSTRAINT service_objects_current_version_fk
   FOREIGN KEY(tenant_id,org_id,case_id,service_object_id,current_version)
   REFERENCES service_object_versions(tenant_id,org_id,case_id,service_object_id,version)
   DEFERRABLE INITIALLY DEFERRED;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='service_objects_current_association_fk' AND conrelid='service_objects'::regclass) THEN
  ALTER TABLE service_objects ADD CONSTRAINT service_objects_current_association_fk
   FOREIGN KEY(tenant_id,org_id,case_id,service_object_id,current_association_version,current_version)
   REFERENCES service_object_contract_associations(tenant_id,org_id,case_id,service_object_id,association_version,object_version)
   DEFERRABLE INITIALLY DEFERRED;
 END IF;
END $$;

CREATE OR REPLACE FUNCTION guard_service_object_head() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.current_version<>1 OR NEW.current_association_version IS NOT NULL THEN
   RAISE EXCEPTION 'service object head must start at identity version 1 without an association';
  END IF;
  RETURN NEW;
 END IF;
 IF (NEW.tenant_id,NEW.org_id,NEW.case_id,NEW.service_object_id,NEW.created_by,NEW.created_at)
    IS DISTINCT FROM (OLD.tenant_id,OLD.org_id,OLD.case_id,OLD.service_object_id,OLD.created_by,OLD.created_at) THEN
  RAISE EXCEPTION 'service object identity is immutable';
 END IF;
 IF NEW.current_version<OLD.current_version OR NEW.current_version>OLD.current_version+1 THEN
  RAISE EXCEPTION 'invalid service object version advance';
 END IF;
 IF OLD.current_association_version IS NULL THEN
  IF NEW.current_association_version IS DISTINCT FROM 1 THEN
   RAISE EXCEPTION 'first association version must be 1';
  END IF;
 ELSIF NEW.current_association_version IS DISTINCT FROM OLD.current_association_version THEN
  IF NEW.current_association_version IS NULL OR NEW.current_association_version<>OLD.current_association_version+1 THEN
   RAISE EXCEPTION 'invalid service object association version advance';
  END IF;
 END IF;
	IF NEW.current_version<>OLD.current_version THEN
	 IF OLD.current_association_version IS NULL THEN
	  IF NEW.current_association_version IS DISTINCT FROM 1 THEN
	   RAISE EXCEPTION 'first identity confirmation requires association version 1';
	  END IF;
	 ELSIF NEW.current_association_version IS DISTINCT FROM OLD.current_association_version+1 THEN
	  RAISE EXCEPTION 'identity version advance requires a new pinned association';
	 END IF;
	END IF;
 IF NEW.current_association_version IS DISTINCT FROM OLD.current_association_version AND NOT EXISTS (
  SELECT 1 FROM service_object_contract_associations a
  WHERE (a.tenant_id,a.org_id,a.case_id,a.service_object_id,a.association_version,a.object_version)=
        (NEW.tenant_id,NEW.org_id,NEW.case_id,NEW.service_object_id,NEW.current_association_version,NEW.current_version)
 ) THEN
  RAISE EXCEPTION 'service object association head target missing';
 END IF;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION forbid_service_object_head_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'service object head cannot be deleted'; END $$;

DROP TRIGGER IF EXISTS service_objects_head_guard ON service_objects;
CREATE TRIGGER service_objects_head_guard BEFORE INSERT OR UPDATE ON service_objects
 FOR EACH ROW EXECUTE FUNCTION guard_service_object_head();
DROP TRIGGER IF EXISTS service_objects_no_delete ON service_objects;
CREATE TRIGGER service_objects_no_delete BEFORE DELETE ON service_objects
 FOR EACH ROW EXECUTE FUNCTION forbid_service_object_head_delete();

DROP TRIGGER IF EXISTS service_object_versions_append_only ON service_object_versions;
CREATE TRIGGER service_object_versions_append_only BEFORE UPDATE OR DELETE ON service_object_versions
 FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS service_object_associations_append_only ON service_object_contract_associations;
CREATE TRIGGER service_object_associations_append_only BEFORE UPDATE OR DELETE ON service_object_contract_associations
 FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS service_object_mutation_receipts_append_only ON service_object_mutation_receipts;
CREATE TRIGGER service_object_mutation_receipts_append_only BEFORE UPDATE OR DELETE ON service_object_mutation_receipts
 FOR EACH ROW EXECUTE FUNCTION forbid_append_only();

DO $$ DECLARE table_name text; BEGIN
 FOREACH table_name IN ARRAY ARRAY['service_objects','service_object_versions','service_object_contract_associations','service_object_mutation_receipts'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',table_name);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',table_name);
  EXECUTE format('DROP POLICY IF EXISTS tenant_boundary ON %I',table_name);
  EXECUTE format('CREATE POLICY tenant_boundary ON %I USING (tenant_id=current_setting(''app.tenant_id'',true)) WITH CHECK (tenant_id=current_setting(''app.tenant_id'',true))',table_name);
 END LOOP;
END $$;


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

