package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const materialSchemaSQL = `
CREATE TABLE IF NOT EXISTS case_contract_chains (
 tenant_id text NOT NULL, org_id text NOT NULL, case_id text NOT NULL,
 primary_material_id text, legacy_selected_document_id text,
 legacy_selection_status text NOT NULL DEFAULT 'not_applicable' CHECK (legacy_selection_status IN ('not_applicable','pending_confirmation','confirmed')),
 selection_version bigint NOT NULL DEFAULT 1 CHECK (selection_version > 0),
 selected_document_ids text NOT NULL DEFAULT '[]',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases (tenant_id,org_id,id)
);
CREATE TABLE IF NOT EXISTS material_entities (
 tenant_id text NOT NULL, org_id text NOT NULL, case_id text NOT NULL,
 material_id text NOT NULL, role text NOT NULL CHECK (role IN ('procurement','award','draft_contract','attachment')),
 purpose text NOT NULL CHECK (purpose IN ('primary_contract','supplement','procurement','award','acceptance','invoice','correspondence','other')),
 legacy_status text NOT NULL CHECK (legacy_status IN ('native','pending_confirmation','confirmed')),
 legacy_selected boolean NOT NULL DEFAULT false,
 current_document_id text NOT NULL, current_version integer NOT NULL CHECK (current_version > 0),
 created_by text NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,material_id),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases (tenant_id,org_id,id),
 FOREIGN KEY (tenant_id,org_id,case_id,current_document_id) REFERENCES documents (tenant_id,org_id,case_id,id)
);
CREATE UNIQUE INDEX IF NOT EXISTS material_entities_one_native_primary ON material_entities (tenant_id,org_id,case_id)
 WHERE purpose='primary_contract' AND legacy_status='native';
CREATE TABLE IF NOT EXISTS material_versions (
 tenant_id text NOT NULL, org_id text NOT NULL, case_id text NOT NULL,
 material_id text NOT NULL, version integer NOT NULL CHECK (version > 0),
 document_id text NOT NULL, previous_document_id text,
 created_by text NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,material_id,version),
 UNIQUE (tenant_id,org_id,case_id,document_id),
 FOREIGN KEY (tenant_id,org_id,case_id,material_id) REFERENCES material_entities (tenant_id,org_id,case_id,material_id),
 FOREIGN KEY (tenant_id,org_id,case_id,document_id) REFERENCES documents (tenant_id,org_id,case_id,id)
);
CREATE TABLE IF NOT EXISTS document_upload_idempotency (
 tenant_id text NOT NULL, org_id text NOT NULL, case_id text NOT NULL, actor_id text NOT NULL,
 idempotency_key text NOT NULL, request_hash bytea NOT NULL, document_id text NOT NULL,
 material_id text NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,org_id,case_id,actor_id,idempotency_key),
 FOREIGN KEY (tenant_id,org_id,case_id,document_id) REFERENCES documents (tenant_id,org_id,case_id,id),
 FOREIGN KEY (tenant_id,org_id,case_id,material_id) REFERENCES material_entities (tenant_id,org_id,case_id,material_id)
);
CREATE OR REPLACE FUNCTION forbid_material_version_rewrite() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'append-only'; END $$;
DROP TRIGGER IF EXISTS material_versions_append_only ON material_versions;
CREATE TRIGGER material_versions_append_only BEFORE UPDATE OR DELETE ON material_versions
FOR EACH ROW EXECUTE FUNCTION forbid_material_version_rewrite();
`

func migrateMaterials(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, materialSchemaSQL); err != nil {
		return fmt.Errorf("create material schema: %w", err)
	}
	// Every legacy document receives an isolated entity and version. The old
	// current-by-role projection is recorded, never interpreted as versioning.
	const backfill = `
INSERT INTO material_entities (tenant_id,org_id,case_id,material_id,role,purpose,legacy_status,legacy_selected,current_document_id,current_version,created_by,created_at)
SELECT d.tenant_id,d.org_id,d.case_id,'legacy_'||d.id,d.role,
 CASE d.role WHEN 'draft_contract' THEN 'primary_contract' WHEN 'attachment' THEN 'other' ELSE d.role END,
 'pending_confirmation', (row_number() OVER (PARTITION BY d.tenant_id,d.org_id,d.case_id,d.role ORDER BY d.seq DESC)=1),
 d.id,1,'legacy-migration',d.created_at::timestamptz
FROM documents d WHERE NOT EXISTS (SELECT 1 FROM material_versions mv WHERE mv.tenant_id=d.tenant_id AND mv.org_id=d.org_id AND mv.case_id=d.case_id AND mv.document_id=d.id)
ON CONFLICT (tenant_id,org_id,case_id,material_id) DO NOTHING;
INSERT INTO material_versions (tenant_id,org_id,case_id,material_id,version,document_id,created_by,created_at)
SELECT me.tenant_id,me.org_id,me.case_id,me.material_id,1,me.current_document_id,'legacy-migration',me.created_at
FROM material_entities me WHERE me.legacy_status='pending_confirmation' AND me.current_version=1
AND NOT EXISTS (SELECT 1 FROM material_versions mv WHERE mv.tenant_id=me.tenant_id AND mv.org_id=me.org_id AND mv.case_id=me.case_id AND mv.material_id=me.material_id)
ON CONFLICT (tenant_id,org_id,case_id,material_id,version) DO NOTHING;
INSERT INTO case_contract_chains (tenant_id,org_id,case_id,legacy_selected_document_id,legacy_selection_status)
SELECT c.tenant_id,c.org_id,c.id,d.id,CASE WHEN d.id IS NULL THEN 'not_applicable' ELSE 'pending_confirmation' END
FROM cases c LEFT JOIN LATERAL (
 SELECT id FROM documents WHERE case_id=c.id AND tenant_id=c.tenant_id AND org_id=c.org_id AND role='draft_contract' ORDER BY seq DESC LIMIT 1
) d ON true
ON CONFLICT (tenant_id,org_id,case_id) DO NOTHING;
UPDATE case_contract_chains ch SET selected_document_ids = COALESCE((SELECT json_agg(m.current_document_id ORDER BY m.role)::text FROM material_entities m WHERE m.tenant_id=ch.tenant_id AND m.org_id=ch.org_id AND m.case_id=ch.case_id AND m.legacy_selected),'[]')
WHERE ch.legacy_selection_status IN ('pending_confirmation','not_applicable') AND ch.selected_document_ids='[]';

`
	if _, err := tx.Exec(ctx, backfill); err != nil {
		return fmt.Errorf("backfill legacy materials: %w", err)
	}
	for _, table := range []string{"case_contract_chains", "material_entities", "material_versions", "document_upload_idempotency"} {
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

func loadMaterialState(ctx context.Context, q queryer) ([]MaterialEntity, []MaterialVersion, []CaseMaterialChain, error) {
	entities, err := q.Query(ctx, `SELECT tenant_id,org_id,case_id,material_id,role,purpose,legacy_status,legacy_selected,current_document_id,current_version,created_by,created_at FROM material_entities ORDER BY created_at,material_id`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer entities.Close()
	var materials []MaterialEntity
	for entities.Next() {
		var item MaterialEntity
		if err := entities.Scan(&item.TenantID, &item.OrgID, &item.CaseID, &item.ID, &item.Role, &item.Purpose, &item.LegacyStatus, &item.LegacySelected, &item.CurrentDocumentID, &item.CurrentVersion, &item.CreatedBy, &item.CreatedAt); err != nil {
			return nil, nil, nil, err
		}
		materials = append(materials, item)
	}
	if err := entities.Err(); err != nil {
		return nil, nil, nil, err
	}
	versions, err := q.Query(ctx, `SELECT tenant_id,org_id,case_id,material_id,version,document_id,COALESCE(previous_document_id,''),created_by,created_at FROM material_versions ORDER BY material_id,version`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer versions.Close()
	var history []MaterialVersion
	for versions.Next() {
		var item MaterialVersion
		if err := versions.Scan(&item.TenantID, &item.OrgID, &item.CaseID, &item.MaterialID, &item.Version, &item.DocumentID, &item.PreviousDocument, &item.CreatedBy, &item.CreatedAt); err != nil {
			return nil, nil, nil, err
		}
		history = append(history, item)
	}
	if err := versions.Err(); err != nil {
		return nil, nil, nil, err
	}
	chainRows, err := q.Query(ctx, `SELECT tenant_id,org_id,case_id,primary_material_id,legacy_selected_document_id,legacy_selection_status,selection_version,selected_document_ids,created_at,updated_at FROM case_contract_chains ORDER BY case_id`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer chainRows.Close()
	var chains []CaseMaterialChain
	for chainRows.Next() {
		var item CaseMaterialChain
		var primary, legacy *string
		var selected string
		if err := chainRows.Scan(&item.TenantID, &item.OrgID, &item.CaseID, &primary, &legacy, &item.LegacySelectionStatus, &item.SelectionVersion, &selected, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, nil, nil, err
		}
		item.PrimaryMaterialID, item.LegacySelectedDocumentID = primary, legacy
		if err := json.Unmarshal([]byte(selected), &item.SelectedDocumentIDs); err != nil {
			return nil, nil, nil, err
		}
		chains = append(chains, item)
	}
	if err := chainRows.Err(); err != nil {
		return nil, nil, nil, err
	}
	return materials, history, chains, nil
}

func loadUploadIdempotency(ctx context.Context, q queryer) ([]UploadIdempotencyRecord, error) {
	rows, err := q.Query(ctx, `SELECT tenant_id,org_id,case_id,actor_id,idempotency_key,request_hash,document_id,material_id,created_at FROM document_upload_idempotency ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UploadIdempotencyRecord
	for rows.Next() {
		var item UploadIdempotencyRecord
		if err := rows.Scan(&item.TenantID, &item.OrgID, &item.CaseID, &item.ActorID, &item.Key, &item.RequestHash, &item.DocumentID, &item.MaterialID, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
