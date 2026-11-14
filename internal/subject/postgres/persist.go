// File metadata (manifest mirror):
// Purpose: Persists changed Subject-owned rows and exact current identifier projections in original order through the caller-bound PostgreSQL executor.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	platformpostgres "contractaudit/internal/platform/postgres"
	"contractaudit/internal/subject/model"
)

func PersistRows(ctx context.Context, tx platformpostgres.Executor, st Snapshot) error {
	for _, item := range st.Subjects {
		if _, err := tx.Exec(ctx, `INSERT INTO subjects (tenant_id,subject_id,current_version,status,created_by_user_id,created_by_issuer,created_by_subject,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (tenant_id,subject_id) DO UPDATE SET current_version=EXCLUDED.current_version,status=EXCLUDED.status`,
			item.TenantID, item.ID, item.CurrentVersion, item.Status, item.CreatedByUser, item.CreatedByIssuer, item.CreatedBySubject, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.SubjectVersions {
		payload, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subject_versions (tenant_id,subject_id,version,payload,payload_hash,created_at)
			VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (tenant_id,subject_id,version) DO NOTHING`, item.TenantID, item.SubjectID, item.Version, payload, item.PayloadHash, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.Subjects {
		current, ok := model.SubjectVersionAt(st.SubjectVersions, item.ID, item.CurrentVersion)
		if !ok {
			return fmt.Errorf("subject %s points to a missing immutable version", item.ID)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM subject_current_identifier_claims WHERE tenant_id=$1 AND subject_id=$2`, item.TenantID, item.ID); err != nil {
			return err
		}
		for _, claim := range current.IdentityClaims {
			if _, err := tx.Exec(ctx, `INSERT INTO subject_current_identifier_claims
				(tenant_id,subject_id,namespace,identifier_type,value,verification_state,verification_scope,source_catalog_id,source_snapshot_id,source_digest,source_time,source_valid_from,source_valid_until)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, item.TenantID, item.ID, claim.Namespace, claim.IdentifierType, claim.Value,
				claim.VerificationState, claim.VerificationScope, claim.SourceCatalogID, claim.SourceSnapshotID, claim.SourceDigest, claim.SourceTime, claim.SourceValidFrom, claim.SourceValidUntil); err != nil {
				return err
			}
		}
	}
	for _, item := range st.CaseSubjectRoles {
		if _, err := tx.Exec(ctx, `INSERT INTO case_subject_roles (tenant_id,org_id,case_id,binding_id,current_version,created_by_user_id,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (tenant_id,org_id,case_id,binding_id) DO UPDATE SET current_version=EXCLUDED.current_version`,
			item.TenantID, item.OrgID, item.CaseID, item.BindingID, item.CurrentVersion, item.CreatedByUser, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.CaseSubjectRoleVersions {
		payload, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO case_subject_role_versions
			(tenant_id,org_id,case_id,binding_id,version,subject_id,subject_version,role,binding_state,source_kind,payload,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT (tenant_id,org_id,case_id,binding_id,version) DO NOTHING`,
			item.TenantID, item.OrgID, item.CaseID, item.BindingID, item.Version, item.SubjectID, item.SubjectVersion, item.Role, item.BindingState, item.SourceKind, payload, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.SubjectChangeRequests {
		payload, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subject_change_requests
			(tenant_id,org_id,source_case_id,request_id,target_kind,target_subject_id,target_binding_id,expected_subject_version,expected_binding_version,payload,payload_hash,affected_bindings_hash,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13) ON CONFLICT (tenant_id,request_id) DO NOTHING`,
			item.TenantID, item.OrgID, item.SourceCaseID, item.RequestID, item.TargetKind, item.TargetSubjectID, item.TargetBindingID, item.ExpectedSubjectVersion, item.ExpectedBindingVersion, payload, item.CanonicalPayloadHash, item.ExpectedBindingsHash, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.SubjectChangeDecisions {
		payload, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subject_change_decisions (tenant_id,request_id,decision_id,decision,actor_user_id,actor_issuer,actor_subject,payload,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (tenant_id,decision_id) DO NOTHING`,
			item.TenantID, item.RequestID, item.DecisionID, item.Decision, item.Actor.UserID, item.Actor.Issuer, item.Actor.Subject, payload, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.SubjectIdempotency {
		if _, err := tx.Exec(ctx, `INSERT INTO subject_write_idempotency (tenant_id,actor_user_id,operation,target_id,idempotency_key,payload_hash,http_status,response,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (tenant_id,actor_user_id,operation,target_id,idempotency_key) DO NOTHING`,
			item.TenantID, item.ActorUserID, item.Operation, item.TargetID, item.Key, item.PayloadHash, item.HTTPStatus, item.Response, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.SubjectSourceSnapshots {
		if _, err := tx.Exec(ctx, `INSERT INTO subject_source_snapshots
			(tenant_id,org_id,case_id,snapshot_id,source_catalog_id,source_version,source_payload,source_digest,source_scope,captured_at,created_by_user_id,created_by_issuer,created_by_subject)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT (tenant_id,org_id,case_id,snapshot_id) DO NOTHING`,
			item.TenantID, item.OrgID, item.CaseID, item.SnapshotID, item.SourceCatalogID, item.SourceVersion, item.SourcePayload, item.SourceDigest, item.SourceScope, item.CapturedAt, item.CreatedByUser, item.CreatedByIssuer, item.CreatedBySubject); err != nil {
			return err
		}
	}
	return nil
}
