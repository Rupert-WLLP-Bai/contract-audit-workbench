package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const workbenchSchemaSQL = `
CREATE TABLE IF NOT EXISTS business_case_profiles (
	seq bigserial PRIMARY KEY,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	number text NOT NULL,
	version integer NOT NULL CHECK (version > 0),
	record_json jsonb NOT NULL,
	UNIQUE (tenant_id, org_id, case_id),
	FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS business_case_profiles_number ON business_case_profiles (tenant_id, lower(number));
CREATE TABLE IF NOT EXISTS business_workbench_tasks (
	seq bigserial PRIMARY KEY,
	id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	version integer NOT NULL CHECK (version > 0),
	record_json jsonb NOT NULL,
	UNIQUE (tenant_id, org_id, case_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id)
);
CREATE INDEX IF NOT EXISTS business_workbench_tasks_case_order ON business_workbench_tasks (tenant_id, org_id, case_id, seq);
CREATE TABLE IF NOT EXISTS business_case_relations (
	seq bigserial PRIMARY KEY,
	id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	related_org_id text NOT NULL,
	related_case_id text NOT NULL,
	pair_key text NOT NULL,
	record_json jsonb NOT NULL,
	UNIQUE (tenant_id, pair_key),
	FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id),
	FOREIGN KEY (tenant_id, related_org_id, related_case_id) REFERENCES cases (tenant_id, org_id, id),
	CHECK ((org_id, case_id) <> (related_org_id, related_case_id))
);
CREATE INDEX IF NOT EXISTS business_case_relations_case_order ON business_case_relations (tenant_id, org_id, case_id, seq);
CREATE TABLE IF NOT EXISTS business_knowledge (
	seq bigserial PRIMARY KEY,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	id text NOT NULL,
	version text NOT NULL,
	source_type text NOT NULL CHECK (source_type IN ('synthetic_policy','reference')),
	record_json jsonb NOT NULL,
	UNIQUE (tenant_id, id, version)
);
CREATE INDEX IF NOT EXISTS business_knowledge_tenant_order ON business_knowledge (tenant_id, seq);
CREATE TABLE IF NOT EXISTS business_workbench_audit (
	seq bigserial PRIMARY KEY,
	id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text,
	occurred_at text NOT NULL,
	record_json jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS business_workbench_audit_tenant_order ON business_workbench_audit (tenant_id, seq);
CREATE INDEX IF NOT EXISTS business_workbench_audit_case_order ON business_workbench_audit (tenant_id, org_id, case_id, seq);

CREATE OR REPLACE FUNCTION guard_business_case_profile_update() RETURNS trigger
	LANGUAGE plpgsql AS $$
	BEGIN
		IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.org_id IS DISTINCT FROM OLD.org_id OR
			NEW.case_id IS DISTINCT FROM OLD.case_id OR NEW.version <> OLD.version + 1 THEN
			RAISE EXCEPTION 'case profile identity or version is immutable';
		END IF;
		RETURN NEW;
	END;
	$$;
CREATE OR REPLACE FUNCTION guard_business_workbench_task_update() RETURNS trigger
	LANGUAGE plpgsql AS $$
	BEGIN
		IF NEW.id IS DISTINCT FROM OLD.id OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR
			NEW.org_id IS DISTINCT FROM OLD.org_id OR NEW.case_id IS DISTINCT FROM OLD.case_id OR
			NEW.version <> OLD.version + 1 OR NEW.record_json->>'created_at' IS DISTINCT FROM OLD.record_json->>'created_at' OR
			NEW.record_json->>'created_by' IS DISTINCT FROM OLD.record_json->>'created_by' THEN
			RAISE EXCEPTION 'workbench task identity, creator, or version is immutable';
		END IF;
		RETURN NEW;
	END;
	$$;
CREATE OR REPLACE FUNCTION forbid_business_relation_update() RETURNS trigger
	LANGUAGE plpgsql AS $$
	BEGIN
		RAISE EXCEPTION 'case relations are immutable';
	END;
	$$;
DROP TRIGGER IF EXISTS business_case_profiles_update_guard ON business_case_profiles;
CREATE TRIGGER business_case_profiles_update_guard BEFORE UPDATE ON business_case_profiles FOR EACH ROW EXECUTE FUNCTION guard_business_case_profile_update();
DROP TRIGGER IF EXISTS business_workbench_tasks_update_guard ON business_workbench_tasks;
CREATE TRIGGER business_workbench_tasks_update_guard BEFORE UPDATE ON business_workbench_tasks FOR EACH ROW EXECUTE FUNCTION guard_business_workbench_task_update();
DROP TRIGGER IF EXISTS business_case_relations_update_guard ON business_case_relations;
CREATE TRIGGER business_case_relations_update_guard BEFORE UPDATE ON business_case_relations FOR EACH ROW EXECUTE FUNCTION forbid_business_relation_update();
DROP TRIGGER IF EXISTS business_knowledge_append_only ON business_knowledge;
CREATE TRIGGER business_knowledge_append_only BEFORE UPDATE OR DELETE ON business_knowledge FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS business_workbench_audit_append_only ON business_workbench_audit;
CREATE TRIGGER business_workbench_audit_append_only BEFORE UPDATE OR DELETE ON business_workbench_audit FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
`

func loadWorkbenchState(ctx context.Context, q queryer, st *state) error {
	var err error
	if st.Profiles, err = loadWorkbenchProfiles(ctx, q); err != nil {
		return err
	}
	if st.Tasks, err = loadWorkbenchTasks(ctx, q); err != nil {
		return err
	}
	if st.Relations, err = loadWorkbenchRelations(ctx, q); err != nil {
		return err
	}
	if st.Knowledge, err = loadKnowledge(ctx, q); err != nil {
		return err
	}
	if st.WorkbenchAudit, err = loadWorkbenchAudit(ctx, q); err != nil {
		return err
	}
	return nil
}

func loadWorkbenchProfiles(ctx context.Context, q queryer) ([]caseProfileRecord, error) {
	rows, err := q.Query(ctx, `SELECT tenant_id, org_id, case_id, number, version, record_json FROM business_case_profiles ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []caseProfileRecord{}
	for rows.Next() {
		var item caseProfileRecord
		var caseID, number string
		var version int
		var raw []byte
		if err := rows.Scan(&item.TenantID, &item.OrgID, &caseID, &number, &version, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item.Profile); err != nil {
			return nil, fmt.Errorf("decode case profile: %w", err)
		}
		if item.Profile.CaseID != caseID || item.Profile.Number != number || item.Profile.Version != version {
			return nil, errors.New("stored case profile identity does not match its row")
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadWorkbenchTasks(ctx context.Context, q queryer) ([]workbenchTaskRecord, error) {
	rows, err := q.Query(ctx, `SELECT tenant_id, org_id, id, case_id, version, record_json FROM business_workbench_tasks ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []workbenchTaskRecord{}
	for rows.Next() {
		var item workbenchTaskRecord
		var id, caseID string
		var version int
		var raw []byte
		if err := rows.Scan(&item.TenantID, &item.OrgID, &id, &caseID, &version, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item.Task); err != nil {
			return nil, fmt.Errorf("decode workbench task: %w", err)
		}
		if item.Task.ID != id || item.Task.CaseID != caseID || item.Task.Version != version {
			return nil, errors.New("stored workbench task identity does not match its row")
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadWorkbenchRelations(ctx context.Context, q queryer) ([]caseRelationRecord, error) {
	rows, err := q.Query(ctx, `SELECT tenant_id, org_id, case_id, related_org_id, related_case_id, pair_key, record_json FROM business_case_relations ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []caseRelationRecord{}
	for rows.Next() {
		var item caseRelationRecord
		var caseID, relatedCaseID string
		var raw []byte
		if err := rows.Scan(&item.TenantID, &item.OrgID, &caseID, &item.RelatedOrgID, &relatedCaseID, &item.PairKey, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item.Relation); err != nil {
			return nil, fmt.Errorf("decode case relation: %w", err)
		}
		if item.Relation.CaseID != caseID || item.Relation.RelatedCaseID != relatedCaseID {
			return nil, errors.New("stored case relation identity does not match its row")
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadKnowledge(ctx context.Context, q queryer) ([]knowledgeRecord, error) {
	rows, err := q.Query(ctx, `SELECT tenant_id, org_id, id, version, source_type, record_json FROM business_knowledge ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []knowledgeRecord{}
	for rows.Next() {
		var item knowledgeRecord
		var id, version, sourceType string
		var raw []byte
		if err := rows.Scan(&item.TenantID, &item.OrgID, &id, &version, &sourceType, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item.Item); err != nil {
			return nil, fmt.Errorf("decode knowledge record: %w", err)
		}
		if item.Item.ID != id || item.Item.Version != version || item.Item.SourceType != sourceType {
			return nil, errors.New("stored knowledge identity does not match its row")
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadWorkbenchAudit(ctx context.Context, q queryer) ([]workbenchAuditRecord, error) {
	rows, err := q.Query(ctx, `SELECT tenant_id, org_id, id, record_json FROM business_workbench_audit ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []workbenchAuditRecord{}
	for rows.Next() {
		var item workbenchAuditRecord
		var id string
		var raw []byte
		if err := rows.Scan(&item.TenantID, &item.OrgID, &id, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item.Item); err != nil {
			return nil, fmt.Errorf("decode workbench audit record: %w", err)
		}
		if item.Item.ID != id {
			return nil, errors.New("stored workbench audit identity does not match its row")
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func syncWorkbenchRows(ctx context.Context, tx pgx.Tx, previous, removed, changed state) error {
	if err := syncWorkbenchProfiles(ctx, tx, previous.Profiles, changed.Profiles); err != nil {
		return err
	}
	if err := syncWorkbenchTasks(ctx, tx, previous.Tasks, changed.Tasks); err != nil {
		return err
	}
	for _, item := range changed.Relations {
		raw, err := json.Marshal(item.Relation)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO business_case_relations (id,tenant_id,org_id,case_id,related_org_id,related_case_id,pair_key,record_json)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, item.Relation.ID, item.TenantID, item.OrgID, item.Relation.CaseID,
			item.RelatedOrgID, item.Relation.RelatedCaseID, item.PairKey, json.RawMessage(raw)); err != nil {
			return fmt.Errorf("insert case relation %q: %w", item.Relation.ID, err)
		}
	}
	for _, item := range removed.Relations {
		result, err := tx.Exec(ctx, `DELETE FROM business_case_relations WHERE id=$1 AND tenant_id=$2 AND org_id=$3 AND case_id=$4`,
			item.Relation.ID, item.TenantID, item.OrgID, item.Relation.CaseID)
		if err != nil {
			return fmt.Errorf("delete case relation %q: %w", item.Relation.ID, err)
		}
		if result.RowsAffected() != 1 {
			return errors.New("case relation disappeared before deletion")
		}
	}
	for _, item := range changed.Knowledge {
		raw, err := json.Marshal(item.Item)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO business_knowledge (tenant_id,org_id,id,version,source_type,record_json)
			VALUES ($1,$2,$3,$4,$5,$6)`, item.TenantID, item.OrgID, item.Item.ID, item.Item.Version, item.Item.SourceType, json.RawMessage(raw)); err != nil {
			return fmt.Errorf("insert knowledge %q/%q: %w", item.Item.ID, item.Item.Version, err)
		}
	}
	for _, item := range changed.WorkbenchAudit {
		raw, err := json.Marshal(item.Item)
		if err != nil {
			return err
		}
		var caseID any
		if item.Item.CaseID != nil {
			caseID = *item.Item.CaseID
		}
		if _, err := tx.Exec(ctx, `INSERT INTO business_workbench_audit (id,tenant_id,org_id,case_id,occurred_at,record_json)
			VALUES ($1,$2,$3,$4,$5,$6)`, item.Item.ID, item.TenantID, item.OrgID, caseID, item.Item.At, json.RawMessage(raw)); err != nil {
			return fmt.Errorf("insert workbench audit %q: %w", item.Item.ID, err)
		}
	}
	return nil
}

func syncWorkbenchProfiles(ctx context.Context, tx pgx.Tx, previous, changed []caseProfileRecord) error {
	previousByCase := make(map[string]caseProfileRecord, len(previous))
	for _, item := range previous {
		previousByCase[item.Profile.CaseID] = item
	}
	for _, item := range changed {
		profile := item.Profile
		raw, err := json.Marshal(profile)
		if err != nil {
			return err
		}
		prior, exists := previousByCase[profile.CaseID]
		if exists {
			if item.TenantID != prior.TenantID || item.OrgID != prior.OrgID || profile.Version != prior.Profile.Version+1 {
				return errors.New("case profile changed scope or skipped a version")
			}
			result, err := tx.Exec(ctx, `UPDATE business_case_profiles SET number=$1,version=$2,record_json=$3
				WHERE tenant_id=$4 AND org_id=$5 AND case_id=$6 AND version=$7`, profile.Number, profile.Version,
				json.RawMessage(raw), item.TenantID, item.OrgID, profile.CaseID, prior.Profile.Version)
			if err != nil {
				return fmt.Errorf("update case profile %q: %w", profile.CaseID, err)
			}
			if result.RowsAffected() != 1 {
				return errors.New("case profile version conflict")
			}
			continue
		}
		if profile.Version != 1 {
			return errors.New("new case profile must begin at version one")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO business_case_profiles (tenant_id,org_id,case_id,number,version,record_json)
			VALUES ($1,$2,$3,$4,$5,$6)`, item.TenantID, item.OrgID, profile.CaseID, profile.Number, profile.Version, json.RawMessage(raw)); err != nil {
			return fmt.Errorf("insert case profile %q: %w", profile.CaseID, err)
		}
	}
	return nil
}

func syncWorkbenchTasks(ctx context.Context, tx pgx.Tx, previous, changed []workbenchTaskRecord) error {
	previousByID := make(map[string]workbenchTaskRecord, len(previous))
	for _, item := range previous {
		previousByID[item.Task.ID] = item
	}
	for _, item := range changed {
		task := item.Task
		raw, err := json.Marshal(task)
		if err != nil {
			return err
		}
		prior, exists := previousByID[task.ID]
		if exists {
			if item.TenantID != prior.TenantID || item.OrgID != prior.OrgID || task.CaseID != prior.Task.CaseID ||
				task.CreatedAt != prior.Task.CreatedAt || task.Version != prior.Task.Version+1 {
				return errors.New("workbench task changed scope, creation time, or version")
			}
			result, err := tx.Exec(ctx, `UPDATE business_workbench_tasks SET version=$1,record_json=$2
				WHERE id=$3 AND tenant_id=$4 AND org_id=$5 AND case_id=$6 AND version=$7`, task.Version,
				json.RawMessage(raw), task.ID, item.TenantID, item.OrgID, task.CaseID, prior.Task.Version)
			if err != nil {
				return fmt.Errorf("update workbench task %q: %w", task.ID, err)
			}
			if result.RowsAffected() != 1 {
				return errors.New("workbench task version conflict")
			}
			continue
		}
		if task.Version != 1 {
			return errors.New("new workbench task must begin at version one")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO business_workbench_tasks (id,tenant_id,org_id,case_id,version,record_json)
			VALUES ($1,$2,$3,$4,$5,$6)`, task.ID, item.TenantID, item.OrgID, task.CaseID, task.Version, json.RawMessage(raw)); err != nil {
			return fmt.Errorf("insert workbench task %q: %w", task.ID, err)
		}
	}
	return nil
}
