package store

import (
	"context"
	"encoding/json"
)

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
