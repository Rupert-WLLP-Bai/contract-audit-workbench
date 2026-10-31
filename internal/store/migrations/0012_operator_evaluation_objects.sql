-- Immutable whole-case evaluation objects.
ALTER TABLE operator_fulfillment_evaluations
 ALTER COLUMN contract_version_id DROP NOT NULL,
 ALTER COLUMN binding_version DROP NOT NULL,
 ALTER COLUMN snapshot_set_id DROP NOT NULL;
DO $$ BEGIN
 ALTER TABLE operator_fulfillment_evaluations DROP CONSTRAINT IF EXISTS operator_fulfillment_evaluations_binding_version_fkey;
 ALTER TABLE operator_fulfillment_evaluations DROP CONSTRAINT IF EXISTS operator_fulfillment_evaluations_snapshot_set_id_fkey;
 ALTER TABLE operator_fulfillment_evaluations DROP CONSTRAINT IF EXISTS operator_fulfillment_evaluations_scope_tuple_ck;
 ALTER TABLE operator_fulfillment_evaluations ADD CONSTRAINT operator_fulfillment_evaluations_scope_tuple_ck CHECK (
  (contract_version_id IS NULL AND binding_version IS NULL AND snapshot_set_id IS NULL)
  OR (contract_version_id IS NOT NULL AND binding_version IS NOT NULL AND snapshot_set_id IS NOT NULL)
 );
END $$;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='operator_fulfillment_evaluations_binding_fk' AND conrelid='operator_fulfillment_evaluations'::regclass) THEN
  ALTER TABLE operator_fulfillment_evaluations ADD CONSTRAINT operator_fulfillment_evaluations_binding_fk
   FOREIGN KEY(tenant_id,org_id,case_id,binding_version)
   REFERENCES operator_object_bindings(tenant_id,org_id,case_id,binding_version);
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='operator_fulfillment_evaluations_snapshot_fk' AND conrelid='operator_fulfillment_evaluations'::regclass) THEN
  ALTER TABLE operator_fulfillment_evaluations ADD CONSTRAINT operator_fulfillment_evaluations_snapshot_fk
   FOREIGN KEY(tenant_id,org_id,case_id,snapshot_set_id)
   REFERENCES operator_external_snapshot_sets(tenant_id,org_id,case_id,snapshot_set_id);
 END IF;
END $$;
CREATE UNIQUE INDEX IF NOT EXISTS operator_snapshot_set_binding_tuple_uq
 ON operator_external_snapshot_sets(tenant_id,org_id,case_id,snapshot_set_id,binding_version);

CREATE TABLE IF NOT EXISTS operator_fulfillment_evaluation_objects (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 case_id text NOT NULL,
 evaluation_id text NOT NULL,
 ordinal integer NOT NULL CHECK(ordinal IS NOT NULL AND ordinal BETWEEN 1 AND 100),
 object_ref text NOT NULL CHECK(length(object_ref)>0 AND length(object_ref)<=256),
 source_run_id text NULL,
 source_attempt_id text NULL,
 material_id text NULL,
 material_version integer NULL,
 document_id text NULL,
 parsed_document_id text NULL,
 purpose text NULL,
 source_role text NULL,
 facts_canonical_json bytea NOT NULL CHECK(octet_length(facts_canonical_json)>0),
 facts_sha256 text NOT NULL CHECK(facts_sha256 ~ '^[0-9a-f]{64}$'),
 service_object_id text NULL,
 object_version integer NULL,
 association_version integer NULL,
 mapping_id text NULL,
 mapping_version bigint NULL,
 binding_version text NULL,
 snapshot_set_id text NULL,
 status text NOT NULL CHECK(status IN ('evaluated','unresolved','insufficient','not_applicable')),
 reason_code text NOT NULL,
 result_json bytea NOT NULL CHECK(octet_length(result_json)>0),
 result_sha256 text NOT NULL CHECK(result_sha256 ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,org_id,case_id,evaluation_id,ordinal),
 UNIQUE(tenant_id,org_id,case_id,evaluation_id,object_ref,document_id,parsed_document_id,service_object_id),
 FOREIGN KEY(tenant_id,org_id,case_id,evaluation_id)
  REFERENCES operator_fulfillment_evaluations(tenant_id,org_id,case_id,evaluation_id),
 FOREIGN KEY(tenant_id,org_id,case_id,source_run_id)
  REFERENCES review_runs(tenant_id,org_id,case_id,id),
 FOREIGN KEY(tenant_id,org_id,source_run_id,source_attempt_id)
  REFERENCES review_attempts(tenant_id,org_id,run_id,id),
 FOREIGN KEY(source_run_id,source_attempt_id,document_id,parsed_document_id)
  REFERENCES review_attempt_parse_selections(run_id,attempt_id,document_id,parsed_document_id),
 FOREIGN KEY(tenant_id,org_id,case_id,material_id,material_version,document_id)
  REFERENCES material_versions(tenant_id,org_id,case_id,material_id,version,document_id),
 FOREIGN KEY(tenant_id,org_id,case_id,service_object_id,object_version)
  REFERENCES service_object_versions(tenant_id,org_id,case_id,service_object_id,version),
 FOREIGN KEY(tenant_id,org_id,case_id,service_object_id,association_version,object_version)
  REFERENCES service_object_contract_associations(tenant_id,org_id,case_id,service_object_id,association_version,object_version),
 FOREIGN KEY(tenant_id,org_id,case_id,mapping_id,mapping_version,binding_version,service_object_id,object_version,association_version)
  REFERENCES operator_object_bindings(tenant_id,org_id,case_id,mapping_id,mapping_version,binding_version,service_object_id,object_version,association_version),
 FOREIGN KEY(tenant_id,org_id,case_id,snapshot_set_id,binding_version)
  REFERENCES operator_external_snapshot_sets(tenant_id,org_id,case_id,snapshot_set_id,binding_version),
 CHECK (
  (source_run_id IS NULL AND source_attempt_id IS NULL AND material_id IS NULL AND material_version IS NULL AND document_id IS NULL AND parsed_document_id IS NULL AND purpose IS NULL AND source_role IS NULL)
  OR (source_run_id IS NOT NULL AND source_attempt_id IS NOT NULL AND material_id IS NOT NULL AND material_version IS NOT NULL AND material_version>0 AND document_id IS NOT NULL AND parsed_document_id IS NOT NULL AND purpose IS NOT NULL AND source_role IS NOT NULL)
 ),
 CHECK (
  (service_object_id IS NULL AND object_version IS NULL AND association_version IS NULL)
  OR (service_object_id IS NOT NULL AND object_version IS NOT NULL AND object_version>0 AND (association_version IS NULL OR association_version>0))
 ),
 CHECK (
  (mapping_id IS NULL AND mapping_version IS NULL AND binding_version IS NULL)
  OR (mapping_id IS NOT NULL AND mapping_version IS NOT NULL AND mapping_version>0 AND binding_version IS NOT NULL AND service_object_id IS NOT NULL AND object_version IS NOT NULL AND association_version IS NOT NULL)
 ),
 CHECK (snapshot_set_id IS NULL OR (binding_version IS NOT NULL AND mapping_id IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS operator_evaluation_objects_ref
 ON operator_fulfillment_evaluation_objects(tenant_id,org_id,case_id,evaluation_id,object_ref,ordinal);
CREATE OR REPLACE FUNCTION guard_operator_evaluation_object_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM operator_fulfillment_evaluations parent
  WHERE parent.tenant_id=NEW.tenant_id AND parent.org_id=NEW.org_id AND parent.case_id=NEW.case_id
   AND parent.evaluation_id=NEW.evaluation_id
   AND parent.xmin::text::bigint=(pg_current_xact_id()::text::bigint % 4294967296)
 ) THEN
  RAISE EXCEPTION 'evaluation objects may only be inserted with their parent';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS operator_evaluation_objects_insert_guard ON operator_fulfillment_evaluation_objects;
CREATE TRIGGER operator_evaluation_objects_insert_guard BEFORE INSERT ON operator_fulfillment_evaluation_objects FOR EACH ROW EXECUTE FUNCTION guard_operator_evaluation_object_insert();
DROP TRIGGER IF EXISTS operator_evaluation_objects_append_only ON operator_fulfillment_evaluation_objects;
CREATE TRIGGER operator_evaluation_objects_append_only BEFORE UPDATE OR DELETE ON operator_fulfillment_evaluation_objects FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
ALTER TABLE operator_fulfillment_evaluation_objects ENABLE ROW LEVEL SECURITY;
ALTER TABLE operator_fulfillment_evaluation_objects FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_boundary ON operator_fulfillment_evaluation_objects;
CREATE POLICY tenant_boundary ON operator_fulfillment_evaluation_objects
 USING (tenant_id=current_setting('app.tenant_id',true)) WITH CHECK (tenant_id=current_setting('app.tenant_id',true));
