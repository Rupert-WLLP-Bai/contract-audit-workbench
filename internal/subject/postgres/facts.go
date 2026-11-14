// File metadata (manifest mirror):
// Purpose: Reads exact Subject-owned binding and current-version facts for Legal protocols and current or immutable Contract references through a non-owning executor.
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
	"errors"

	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

type Binding struct {
	HeadVersion    int64
	SubjectID      string
	SubjectVersion int64
	Role, State    string
	SubjectHead    int64
	Found          bool
}
type ContractBinding struct {
	BindingID                                                                  string
	BindingVersion                                                             int64
	SubjectID                                                                  string
	SubjectVersion                                                             int64
	CaseRole, BindingState, DisplayName, VerificationState, SubjectPayloadHash string
}

func ReadRecordBinding(ctx context.Context, tx platformpostgres.Executor, tenant, org, caseID, bindingID string) (Binding, error) {
	var out Binding
	err := tx.QueryRow(ctx, `SELECT h.current_version,v.subject_id,v.subject_version,v.role,v.binding_state,sv.current_version FROM case_subject_roles h JOIN case_subject_role_versions v ON v.tenant_id=h.tenant_id AND v.org_id=h.org_id AND v.case_id=h.case_id AND v.binding_id=h.binding_id AND v.version=h.current_version JOIN subjects sv ON sv.tenant_id=v.tenant_id AND sv.subject_id=v.subject_id WHERE h.tenant_id=$1 AND h.org_id=$2 AND h.case_id=$3 AND h.binding_id=$4`, tenant, org, caseID, bindingID).Scan(&out.HeadVersion, &out.SubjectID, &out.SubjectVersion, &out.Role, &out.State, &out.SubjectHead)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	out.Found = err == nil
	return out, err
}
func ReadCurrentBinding(ctx context.Context, tx platformpostgres.Executor, tenant, org, caseID, bindingID string) (Binding, error) {
	var out Binding
	err := tx.QueryRow(ctx, `SELECT h.current_version,v.subject_id,v.subject_version,v.role,v.binding_state FROM case_subject_roles h JOIN case_subject_role_versions v ON v.tenant_id=h.tenant_id AND v.org_id=h.org_id AND v.case_id=h.case_id AND v.binding_id=h.binding_id AND v.version=h.current_version WHERE h.tenant_id=$1 AND h.org_id=$2 AND h.case_id=$3 AND h.binding_id=$4`, tenant, org, caseID, bindingID).Scan(&out.HeadVersion, &out.SubjectID, &out.SubjectVersion, &out.Role, &out.State)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	out.Found = err == nil
	return out, err
}
func ReadSubjectCurrentVersion(ctx context.Context, tx platformpostgres.Executor, tenant, id string) (int64, bool, error) {
	var v int64
	err := tx.QueryRow(ctx, `SELECT current_version FROM subjects WHERE tenant_id=$1 AND subject_id=$2`, tenant, id).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return v, err == nil, err
}
func CountActiveRole(ctx context.Context, tx platformpostgres.Executor, tenant, org, caseID, role string) (int, error) {
	var count int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM case_subject_roles h JOIN case_subject_role_versions v ON v.tenant_id=h.tenant_id AND v.org_id=h.org_id AND v.case_id=h.case_id AND v.binding_id=h.binding_id AND v.version=h.current_version WHERE h.tenant_id=$1 AND h.org_id=$2 AND h.case_id=$3 AND h.current_version=v.version AND v.role=$4 AND v.binding_state='active'`, tenant, org, caseID, role).Scan(&count)
	return count, err
}
func ListContractBindings(ctx context.Context, tx platformpostgres.Executor, tenant, org, caseID, subjectID, bindingID string) ([]ContractBinding, error) {
	rows, err := tx.Query(ctx, `SELECT r.binding_id, r.version, r.subject_id,
 r.subject_version, r.role, r.binding_state,
 sv.payload->>'display_name', sv.payload->>'verification_state', sv.payload_hash
 FROM case_subject_roles head
 JOIN case_subject_role_versions r
 ON r.tenant_id=head.tenant_id AND r.org_id=head.org_id AND r.case_id=head.case_id
 AND r.binding_id=head.binding_id AND r.version=head.current_version
 JOIN subject_versions sv
 ON sv.tenant_id=r.tenant_id AND sv.subject_id=r.subject_id AND sv.version=r.subject_version
 WHERE head.tenant_id=$1 AND head.org_id=$2 AND head.case_id=$3
 AND r.subject_id=$4 AND ($5='' OR head.binding_id=$5)
 ORDER BY head.binding_id`, tenant, org, caseID, subjectID, bindingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContractBinding{}
	for rows.Next() {
		var v ContractBinding
		if err := rows.Scan(&v.BindingID, &v.BindingVersion, &v.SubjectID, &v.SubjectVersion, &v.CaseRole, &v.BindingState, &v.DisplayName, &v.VerificationState, &v.SubjectPayloadHash); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func ReadExactContractBinding(ctx context.Context, tx platformpostgres.Executor, tenant, org, caseID, bindingID string, version int64) (ContractBinding, bool, error) {
	var v ContractBinding
	err := tx.QueryRow(ctx, `SELECT r.binding_id, r.version, r.subject_id, r.subject_version,
 r.role, r.binding_state, sv.payload_hash,
 sv.payload->>'display_name', sv.payload->>'verification_state'
 FROM case_subject_role_versions r
 JOIN subject_versions sv
 ON sv.tenant_id=r.tenant_id AND sv.subject_id=r.subject_id AND sv.version=r.subject_version
 WHERE r.tenant_id=$1 AND r.org_id=$2 AND r.case_id=$3
 AND r.binding_id=$4 AND r.version=$5`, tenant, org, caseID, bindingID, version).Scan(&v.BindingID, &v.BindingVersion, &v.SubjectID, &v.SubjectVersion, &v.CaseRole, &v.BindingState, &v.SubjectPayloadHash, &v.DisplayName, &v.VerificationState)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}
