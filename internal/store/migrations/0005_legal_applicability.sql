-- Legal applicability follows subject version and binding creation.

CREATE UNIQUE INDEX IF NOT EXISTS case_subject_role_version_party_pin_unique
 ON case_subject_role_versions(tenant_id,org_id,case_id,binding_id,version,subject_id,subject_version);

CREATE TABLE IF NOT EXISTS case_legal_applicability_facts (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 case_id text NOT NULL,
 fact_id text NOT NULL,
 dimension text NOT NULL CHECK (dimension IN ('buyer_kind','seller_kind','buyer_enterprise_size','seller_enterprise_size','funding_kind','procurement_scope','project_kind','contract_clause_kind','transaction_kind')),
 current_version bigint NOT NULL CHECK (current_version > 0),
 created_by_user_id text NOT NULL,
 created_by_issuer text NOT NULL,
 created_by_subject text NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id,org_id,case_id,fact_id),
 UNIQUE (tenant_id,org_id,case_id,dimension),
 UNIQUE (tenant_id,org_id,case_id,fact_id,dimension),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases(tenant_id,org_id,id)
);

CREATE TABLE IF NOT EXISTS case_legal_applicability_fact_versions (
 tenant_id text NOT NULL,
 org_id text NOT NULL,
 case_id text NOT NULL,
 fact_id text NOT NULL,
 version bigint NOT NULL CHECK (version > 0),
 dimension text NOT NULL CHECK (dimension IN ('buyer_kind','seller_kind','buyer_enterprise_size','seller_enterprise_size','funding_kind','procurement_scope','project_kind','contract_clause_kind','transaction_kind')),
 status text NOT NULL CHECK (status IN ('established','unknown')),
 value text,
 unknown_reason text CHECK (unknown_reason IS NULL OR unknown_reason IN ('conflicting_evidence','uncertain_evidence','unsupported_basis','insufficient_evidence','expired_basis','stale_party_context','party_scope_ambiguous','historical_transition_basis_required')),
 temporal_basis text NOT NULL CHECK (temporal_basis IN ('current_state','historical_interval')),
 valid_from timestamptz NOT NULL,
 valid_until timestamptz,
 basis_note text NOT NULL CHECK (length(trim(basis_note)) > 0),
 basis_refs jsonb NOT NULL CHECK (jsonb_typeof(basis_refs)='array'),
 basis_sha256 text NOT NULL CHECK (basis_sha256 ~ '^[0-9a-f]{64}$'),
 basis_obtained_at timestamptz,
 party_side text CHECK (party_side IS NULL OR party_side IN ('buyer','seller')),
 party_binding_id text,
 party_binding_version bigint,
 party_subject_id text,
 party_subject_version bigint,
 party_scope_note text,
 request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
 idempotency_key text NOT NULL CHECK (idempotency_key ~ '^[!-~]{1,200}$'),
 actor_user_id text NOT NULL,
 actor_issuer text NOT NULL,
 actor_subject text NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id,org_id,case_id,fact_id,version),
 FOREIGN KEY (tenant_id,org_id,case_id,fact_id,dimension) REFERENCES case_legal_applicability_facts(tenant_id,org_id,case_id,fact_id,dimension),
 FOREIGN KEY (tenant_id,org_id,case_id,party_binding_id,party_binding_version,party_subject_id,party_subject_version) REFERENCES case_subject_role_versions(tenant_id,org_id,case_id,binding_id,version,subject_id,subject_version),
 UNIQUE (tenant_id,org_id,case_id,dimension,actor_user_id,idempotency_key),
 CHECK ((status='established' AND value IS NOT NULL AND unknown_reason IS NULL AND jsonb_array_length(basis_refs)>0) OR (status='unknown' AND value IS NULL AND unknown_reason IS NOT NULL AND (unknown_reason='insufficient_evidence' OR jsonb_array_length(basis_refs)>0))),
 CHECK (valid_until IS NULL OR valid_until>valid_from),
 CHECK (basis_obtained_at IS NULL OR basis_obtained_at<=created_at),
 CHECK ((temporal_basis='current_state' AND valid_from=created_at AND (basis_obtained_at IS NULL OR valid_from>=basis_obtained_at)) OR (temporal_basis='historical_interval' AND valid_until IS NOT NULL AND valid_until>valid_from AND jsonb_array_length(basis_refs)>0)),
 CHECK (status<>'established' OR CASE dimension
   WHEN 'buyer_kind' THEN value IN ('state_organ','institution','social_organization','enterprise','natural_person')
   WHEN 'seller_kind' THEN value IN ('state_organ','institution','social_organization','enterprise','natural_person')
   WHEN 'buyer_enterprise_size' THEN value IN ('large','sme','not_enterprise')
   WHEN 'seller_enterprise_size' THEN value IN ('large','sme','not_enterprise')
   WHEN 'funding_kind' THEN value IN ('fiscal','mixed_with_fiscal','state_owned_non_fiscal','private')
   WHEN 'procurement_scope' THEN value IN ('centralized_catalog','above_limit','catalog_and_above_limit','outside_catalog_and_below_limit')
   WHEN 'project_kind' THEN value IN ('construction','construction_related_goods','construction_related_services','standalone_goods','standalone_services')
   WHEN 'contract_clause_kind' THEN value IN ('standard_form','negotiated')
   WHEN 'transaction_kind' THEN value IN ('civil_contract','non_civil')
   ELSE false END),
 CONSTRAINT case_legal_applicability_party_pin_complete_check CHECK (
   (party_binding_id IS NULL AND party_binding_version IS NULL AND party_subject_id IS NULL AND party_subject_version IS NULL AND party_side IS NULL AND party_scope_note IS NULL)
   OR
   (party_side IS NOT NULL AND party_binding_id IS NOT NULL AND party_binding_version IS NOT NULL AND party_binding_version>0 AND party_subject_id IS NOT NULL AND party_subject_version IS NOT NULL AND party_subject_version>0 AND length(trim(COALESCE(party_scope_note,'')))>0 AND
     ((dimension IN ('buyer_kind','buyer_enterprise_size') AND party_side='buyer') OR (dimension IN ('seller_kind','seller_enterprise_size') AND party_side='seller')))
 ),
 CHECK (dimension IN ('buyer_kind','buyer_enterprise_size','seller_kind','seller_enterprise_size') OR
   (party_binding_id IS NULL AND party_binding_version IS NULL AND party_subject_id IS NULL AND party_subject_version IS NULL AND party_side IS NULL AND party_scope_note IS NULL)),
 CHECK (status<>'established' OR dimension NOT IN ('buyer_kind','buyer_enterprise_size','seller_kind','seller_enterprise_size') OR party_binding_id IS NOT NULL)
);

DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='case_legal_applicability_fact_versions'::regclass AND conname='case_legal_applicability_party_pin_complete_check') THEN
  ALTER TABLE case_legal_applicability_fact_versions ADD CONSTRAINT case_legal_applicability_party_pin_complete_check CHECK (
   (party_binding_id IS NULL AND party_binding_version IS NULL AND party_subject_id IS NULL AND party_subject_version IS NULL AND party_side IS NULL AND party_scope_note IS NULL)
   OR
   (party_side IS NOT NULL AND party_binding_id IS NOT NULL AND party_binding_version IS NOT NULL AND party_binding_version>0 AND party_subject_id IS NOT NULL AND party_subject_version IS NOT NULL AND party_subject_version>0 AND length(trim(COALESCE(party_scope_note,'')))>0 AND
     ((dimension IN ('buyer_kind','buyer_enterprise_size') AND party_side='buyer') OR (dimension IN ('seller_kind','seller_enterprise_size') AND party_side='seller')))
  );
 END IF;
END $$;

DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='case_legal_applicability_facts'::regclass AND conname='case_legal_applicability_current_version_fk') THEN
  ALTER TABLE case_legal_applicability_facts ADD CONSTRAINT case_legal_applicability_current_version_fk
   FOREIGN KEY (tenant_id,org_id,case_id,fact_id,current_version) REFERENCES case_legal_applicability_fact_versions(tenant_id,org_id,case_id,fact_id,version) DEFERRABLE INITIALLY DEFERRED;
 END IF;
END $$;

CREATE OR REPLACE FUNCTION forbid_legal_applicability_version_rewrite() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'append-only'; END $$;
DROP TRIGGER IF EXISTS case_legal_applicability_fact_versions_append_only ON case_legal_applicability_fact_versions;
CREATE TRIGGER case_legal_applicability_fact_versions_append_only BEFORE UPDATE OR DELETE ON case_legal_applicability_fact_versions FOR EACH ROW EXECUTE FUNCTION forbid_legal_applicability_version_rewrite();

CREATE OR REPLACE FUNCTION advance_legal_applicability_fact_head() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'append-only'; END IF;
 IF NEW.tenant_id<>OLD.tenant_id OR NEW.org_id<>OLD.org_id OR NEW.case_id<>OLD.case_id OR NEW.fact_id<>OLD.fact_id OR NEW.dimension<>OLD.dimension OR
    NEW.created_by_user_id<>OLD.created_by_user_id OR NEW.created_by_issuer<>OLD.created_by_issuer OR NEW.created_by_subject<>OLD.created_by_subject OR
    NEW.created_at<>OLD.created_at OR NEW.current_version<>OLD.current_version+1 THEN RAISE EXCEPTION 'legal applicability head must advance exactly one version'; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS case_legal_applicability_facts_head_advance ON case_legal_applicability_facts;
CREATE TRIGGER case_legal_applicability_facts_head_advance BEFORE UPDATE OR DELETE ON case_legal_applicability_facts FOR EACH ROW EXECUTE FUNCTION advance_legal_applicability_fact_head();

DO $migration$ DECLARE table_name text; BEGIN
 FOREACH table_name IN ARRAY ARRAY['case_legal_applicability_facts','case_legal_applicability_fact_versions'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',table_name);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',table_name);
  EXECUTE format('DROP POLICY IF EXISTS tenant_boundary ON %I',table_name);
  EXECUTE format('CREATE POLICY tenant_boundary ON %I USING (tenant_id = current_setting(''app.tenant_id'', true)) WITH CHECK (tenant_id = current_setting(''app.tenant_id'', true))',table_name);
 END LOOP;
END $migration$;
