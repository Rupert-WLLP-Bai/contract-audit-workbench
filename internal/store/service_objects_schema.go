package store

// serviceObjectSchemaSQL is additive and runs after material migrations so
// its tuple foreign keys can target the immutable material version records.
const serviceObjectSchemaSQL = `
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
`
