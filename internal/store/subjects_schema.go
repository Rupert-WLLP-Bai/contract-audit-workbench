package store

import (
	"context"
	"encoding/json"
	"fmt"
)

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
