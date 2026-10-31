package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const subjectsSchemaSQL = `
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
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='subjects_current_version_fk') THEN
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
ALTER TABLE case_subject_roles DROP COLUMN IF EXISTS role;
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
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='case_subject_roles_current_version_fk') THEN
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
ALTER TABLE subject_change_requests DROP COLUMN IF EXISTS state;
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
`

func migrateSubjects(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, subjectsSchemaSQL); err != nil {
		return fmt.Errorf("create subject schema: %w", err)
	}
	tables := []string{
		"subjects", "subject_versions", "subject_current_identifier_claims", "case_subject_roles", "case_subject_role_versions",
		"subject_change_requests", "subject_change_decisions", "subject_write_idempotency", "subject_source_snapshots",
	}
	for _, table := range tables {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ENABLE ROW LEVEL SECURITY`, table)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s FORCE ROW LEVEL SECURITY`, table)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`DROP POLICY IF EXISTS tenant_boundary ON %s`, table)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE POLICY tenant_boundary ON %s USING (tenant_id=current_setting('app.tenant_id',true)) WITH CHECK (tenant_id=current_setting('app.tenant_id',true))`, table)); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

func loadSubjectState(ctx context.Context, q queryer) (state, error) {
	var out state
	rows, err := q.Query(ctx, `SELECT tenant_id,subject_id,current_version,status,created_by_user_id,created_by_issuer,created_by_subject,created_at FROM subjects ORDER BY created_at,subject_id`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item Subject
		if err := rows.Scan(&item.TenantID, &item.ID, &item.CurrentVersion, &item.Status, &item.CreatedByUser, &item.CreatedByIssuer, &item.CreatedBySubject, &item.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Subjects = append(out.Subjects, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	rows, err = q.Query(ctx, `SELECT payload FROM subject_versions ORDER BY subject_id,version`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var payload []byte
		var item SubjectVersion
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			return out, err
		}
		if err := json.Unmarshal(payload, &item); err != nil {
			rows.Close()
			return out, fmt.Errorf("decode subject version: %w", err)
		}
		out.SubjectVersions = append(out.SubjectVersions, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	rows, err = q.Query(ctx, `SELECT tenant_id,org_id,case_id,binding_id,current_version,created_by_user_id,created_at FROM case_subject_roles ORDER BY case_id,binding_id`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item CaseSubjectRole
		if err := rows.Scan(&item.TenantID, &item.OrgID, &item.CaseID, &item.BindingID, &item.CurrentVersion, &item.CreatedByUser, &item.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.CaseSubjectRoles = append(out.CaseSubjectRoles, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	rows, err = q.Query(ctx, `SELECT payload FROM case_subject_role_versions ORDER BY case_id,binding_id,version`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var payload []byte
		var item CaseSubjectRoleVersion
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			return out, err
		}
		if err := json.Unmarshal(payload, &item); err != nil {
			rows.Close()
			return out, fmt.Errorf("decode case subject role version: %w", err)
		}
		out.CaseSubjectRoleVersions = append(out.CaseSubjectRoleVersions, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	rows, err = q.Query(ctx, `SELECT payload FROM subject_change_requests ORDER BY created_at,request_id`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var payload []byte
		var item SubjectChangeRequest
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			return out, err
		}
		if err := json.Unmarshal(payload, &item); err != nil {
			rows.Close()
			return out, fmt.Errorf("decode subject change request: %w", err)
		}
		out.SubjectChangeRequests = append(out.SubjectChangeRequests, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	rows, err = q.Query(ctx, `SELECT payload FROM subject_change_decisions ORDER BY created_at,decision_id`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var payload []byte
		var item SubjectChangeDecision
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			return out, err
		}
		if err := json.Unmarshal(payload, &item); err != nil {
			rows.Close()
			return out, fmt.Errorf("decode subject change decision: %w", err)
		}
		out.SubjectChangeDecisions = append(out.SubjectChangeDecisions, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	rows, err = q.Query(ctx, `SELECT tenant_id,actor_user_id,operation,target_id,idempotency_key,payload_hash,response,http_status,created_at FROM subject_write_idempotency ORDER BY created_at`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item SubjectIdempotencyRecord
		if err := rows.Scan(&item.TenantID, &item.ActorUserID, &item.Operation, &item.TargetID, &item.Key, &item.PayloadHash, &item.Response, &item.HTTPStatus, &item.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.SubjectIdempotency = append(out.SubjectIdempotency, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	rows, err = q.Query(ctx, `SELECT tenant_id,org_id,case_id,snapshot_id,source_catalog_id,source_version,source_payload,source_digest,source_scope,captured_at,created_by_user_id,created_by_issuer,created_by_subject FROM subject_source_snapshots ORDER BY captured_at,snapshot_id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item SubjectSourceSnapshot
		if err := rows.Scan(&item.TenantID, &item.OrgID, &item.CaseID, &item.SnapshotID, &item.SourceCatalogID, &item.SourceVersion, &item.SourcePayload, &item.SourceDigest, &item.SourceScope, &item.CapturedAt, &item.CreatedByUser, &item.CreatedByIssuer, &item.CreatedBySubject); err != nil {
			return out, err
		}
		out.SubjectSourceSnapshots = append(out.SubjectSourceSnapshots, item)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	normalizeSubjectStateTimes(&out)
	return out, nil
}

func loadSubjectCaseState(ctx context.Context, q queryer, tenantID, orgID, caseID string) (state, error) {
	var out state
	rows, err := q.Query(ctx, `SELECT tenant_id,org_id,case_id,binding_id,current_version,created_by_user_id,created_at
		FROM case_subject_roles WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 ORDER BY binding_id`, tenantID, orgID, caseID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item CaseSubjectRole
		if err := rows.Scan(&item.TenantID, &item.OrgID, &item.CaseID, &item.BindingID, &item.CurrentVersion, &item.CreatedByUser, &item.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.CaseSubjectRoles = append(out.CaseSubjectRoles, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	rows, err = q.Query(ctx, `SELECT payload FROM case_subject_role_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 ORDER BY binding_id,version`, tenantID, orgID, caseID)
	if err != nil {
		return out, err
	}
	subjectIDs := make(map[string]struct{})
	for rows.Next() {
		var payload []byte
		var item CaseSubjectRoleVersion
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			return out, err
		}
		if err := json.Unmarshal(payload, &item); err != nil {
			rows.Close()
			return out, err
		}
		out.CaseSubjectRoleVersions = append(out.CaseSubjectRoleVersions, item)
		subjectIDs[item.SubjectID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	if len(subjectIDs) > 0 {
		ids := make([]string, 0, len(subjectIDs))
		for id := range subjectIDs {
			ids = append(ids, id)
		}
		rows, err = q.Query(ctx, `SELECT tenant_id,subject_id,current_version,status,created_by_user_id,created_by_issuer,created_by_subject,created_at
			FROM subjects WHERE tenant_id=$1 AND subject_id=ANY($2::text[]) ORDER BY subject_id`, tenantID, ids)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var item Subject
			if err := rows.Scan(&item.TenantID, &item.ID, &item.CurrentVersion, &item.Status, &item.CreatedByUser, &item.CreatedByIssuer, &item.CreatedBySubject, &item.CreatedAt); err != nil {
				rows.Close()
				return out, err
			}
			out.Subjects = append(out.Subjects, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return out, err
		}
		rows.Close()
		rows, err = q.Query(ctx, `SELECT payload FROM subject_versions WHERE tenant_id=$1 AND subject_id=ANY($2::text[]) ORDER BY subject_id,version`, tenantID, ids)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var payload []byte
			var item SubjectVersion
			if err := rows.Scan(&payload); err != nil {
				rows.Close()
				return out, err
			}
			if err := json.Unmarshal(payload, &item); err != nil {
				rows.Close()
				return out, err
			}
			out.SubjectVersions = append(out.SubjectVersions, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return out, err
		}
		rows.Close()
	}
	rows, err = q.Query(ctx, `SELECT payload FROM subject_change_requests WHERE tenant_id=$1 AND org_id=$2 AND source_case_id=$3 ORDER BY created_at,request_id`, tenantID, orgID, caseID)
	if err != nil {
		return out, err
	}
	requestIDs := make([]string, 0)
	for rows.Next() {
		var payload []byte
		var item SubjectChangeRequest
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			return out, err
		}
		if err := json.Unmarshal(payload, &item); err != nil {
			rows.Close()
			return out, err
		}
		out.SubjectChangeRequests = append(out.SubjectChangeRequests, item)
		requestIDs = append(requestIDs, item.RequestID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	if len(requestIDs) > 0 {
		rows, err = q.Query(ctx, `SELECT payload FROM subject_change_decisions WHERE tenant_id=$1 AND request_id=ANY($2::text[]) ORDER BY created_at,decision_id`, tenantID, requestIDs)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var payload []byte
			var item SubjectChangeDecision
			if err := rows.Scan(&payload); err != nil {
				rows.Close()
				return out, err
			}
			if err := json.Unmarshal(payload, &item); err != nil {
				rows.Close()
				return out, err
			}
			out.SubjectChangeDecisions = append(out.SubjectChangeDecisions, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return out, err
		}
		rows.Close()
	}
	rows, err = q.Query(ctx, `SELECT tenant_id,org_id,case_id,snapshot_id,source_catalog_id,source_version,source_payload,source_digest,source_scope,captured_at,created_by_user_id,created_by_issuer,created_by_subject
		FROM subject_source_snapshots WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 ORDER BY captured_at,snapshot_id`, tenantID, orgID, caseID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item SubjectSourceSnapshot
		if err := rows.Scan(&item.TenantID, &item.OrgID, &item.CaseID, &item.SnapshotID, &item.SourceCatalogID, &item.SourceVersion, &item.SourcePayload, &item.SourceDigest, &item.SourceScope, &item.CapturedAt, &item.CreatedByUser, &item.CreatedByIssuer, &item.CreatedBySubject); err != nil {
			return out, err
		}
		out.SubjectSourceSnapshots = append(out.SubjectSourceSnapshots, item)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows.Close()
	rows, err = q.Query(ctx, `SELECT tenant_id,actor_user_id,operation,target_id,idempotency_key,payload_hash,response,http_status,created_at
		FROM subject_write_idempotency WHERE tenant_id=$1 AND target_id=$2 ORDER BY created_at`, tenantID, caseID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item SubjectIdempotencyRecord
		if err := rows.Scan(&item.TenantID, &item.ActorUserID, &item.Operation, &item.TargetID, &item.Key, &item.PayloadHash, &item.Response, &item.HTTPStatus, &item.CreatedAt); err != nil {
			return out, err
		}
		out.SubjectIdempotency = append(out.SubjectIdempotency, item)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	normalizeSubjectStateTimes(&out)
	return out, nil
}

func normalizeSubjectStateTimes(st *state) {
	for i := range st.Subjects {
		st.Subjects[i].CreatedAt = st.Subjects[i].CreatedAt.UTC()
	}
	for i := range st.SubjectVersions {
		st.SubjectVersions[i].CreatedAt = st.SubjectVersions[i].CreatedAt.UTC()
	}
	for i := range st.CaseSubjectRoles {
		st.CaseSubjectRoles[i].CreatedAt = st.CaseSubjectRoles[i].CreatedAt.UTC()
	}
	for i := range st.CaseSubjectRoleVersions {
		st.CaseSubjectRoleVersions[i].CreatedAt = st.CaseSubjectRoleVersions[i].CreatedAt.UTC()
	}
	for i := range st.SubjectChangeRequests {
		st.SubjectChangeRequests[i].CreatedAt = st.SubjectChangeRequests[i].CreatedAt.UTC()
	}
	for i := range st.SubjectChangeDecisions {
		st.SubjectChangeDecisions[i].CreatedAt = st.SubjectChangeDecisions[i].CreatedAt.UTC()
	}
	for i := range st.SubjectIdempotency {
		st.SubjectIdempotency[i].CreatedAt = st.SubjectIdempotency[i].CreatedAt.UTC()
	}
	for i := range st.SubjectSourceSnapshots {
		st.SubjectSourceSnapshots[i].CapturedAt = st.SubjectSourceSnapshots[i].CapturedAt.UTC()
	}
}
