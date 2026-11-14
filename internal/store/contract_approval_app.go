// File metadata (manifest mirror):
// Purpose: Adapts existing current authorization, exact material resolution and object-bound draft/fingerprint codecs to typed approval submission ports.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	contractapp "contractaudit/internal/contract/app"
	contract "contractaudit/internal/contract/model"
	contractpg "contractaudit/internal/contract/postgres"
	platform "contractaudit/internal/platform/postgres"
	"encoding/json"
	"github.com/jackc/pgx/v5"
)

type approvalFreezeCodec struct{}

func (approvalFreezeCodec) OpenApprovalDraft(ctx context.Context, tenantID, contractID string, raw []byte) (contract.DraftContent, error) {
	return unsealStoredDraftBound(ctx, raw, tenantID, contractID)
}
func (approvalFreezeCodec) ValidateApprovalSeal(raw []byte) error {
	_, err := parseRestrictedFingerprints(raw)
	return err
}
func (approvalFreezeCodec) ApprovalFingerprintFacts(ctx context.Context, tenantID, contractID string, draft contract.DraftContent, raw []byte) (map[string]any, json.RawMessage, error) {
	baseline, err := parseRestrictedFingerprints(raw)
	if err != nil {
		return nil, nil, err
	}
	fps, err := computeRestrictedFingerprintsBound(ctx, &draft, clmFieldBinding(tenantID, contractID, "supplier.payee_name"), &baseline)
	if err != nil {
		return nil, nil, err
	}
	encoded, err := json.Marshal(fps)
	return flattenContractDraft(draft), encoded, err
}

type approvalMaterialResolver struct {
	tx   pgx.Tx
	user accounts.User
}

func (r approvalMaterialResolver) ResolveApprovalMaterials(ctx context.Context, anchor contract.FrozenVersionMaterial, refs []contract.MaterialRef) ([]contract.FrozenVersionMaterial, error) {
	return resolveApprovalMaterialSummary(ctx, r.tx, r.user, Case{ID: anchor.CaseID, OwnerOrgID: anchor.CaseOrgID}, refs)
}

type approvalSubmissionAccess struct {
	tx   pgx.Tx
	user accounts.User
}

func (a approvalSubmissionAccess) CanRead(ctx context.Context, record contract.Aggregate) bool {
	return authorizeContractRead(ctx, a.tx, record, a.user)
}
func (a approvalSubmissionAccess) CanManage(record contract.Aggregate) bool {
	return authorizeContract(record, a.user, access.Manage)
}

type approvalSubmissionFreeze struct {
	tx       pgx.Tx
	user     accounts.User
	executor platform.Executor
}

func (a approvalSubmissionFreeze) VerifyFreeze(ctx context.Context, tenantID, contractID, revisionID string) ([]contract.FrozenVersionMaterial, error) {
	return contractapp.VerifyApprovalFreeze(ctx, tenantID, contractID, revisionID, contractpg.NewApprovalFreeze(a.executor), approvalFreezeCodec{}, approvalMaterialResolver{tx: a.tx, user: a.user})
}
func (approvalDirectory) Users() []accounts.User { return accounts.List() }
