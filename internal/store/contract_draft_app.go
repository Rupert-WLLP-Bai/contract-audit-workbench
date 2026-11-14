// File metadata (manifest mirror):
// Purpose: Bridges existing Store authorization, transaction ownership and bound draft field encryption to Contract application ports.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	contractapp "contractaudit/internal/contract/app"
	clmcontract "contractaudit/internal/contract/model"

	"github.com/jackc/pgx/v5"
)

type storeDraftCodec struct{}

func (storeDraftCodec) Seal(ctx context.Context, tenantID, contractID string, draft clmcontract.DraftContent) ([]byte, error) {
	_, sealed, _, err := sealDraftContentBound(ctx, draft, tenantID, contractID)
	return sealed, err
}

func (storeDraftCodec) Open(ctx context.Context, tenantID, contractID string, sealed []byte) (clmcontract.DraftContent, error) {
	return unsealStoredDraftBound(ctx, sealed, tenantID, contractID)
}

func (storeDraftCodec) Project(draft clmcontract.DraftContent, canManage bool) clmcontract.DraftContent {
	return projectDraftForViewer(draft, canManage)
}

type storeDraftAccess struct {
	tx   pgx.Tx
	user accounts.User
}

func (a storeDraftAccess) CanRead(ctx context.Context, record clmcontract.Aggregate) bool {
	return authorizeContractRead(ctx, a.tx, record, a.user)
}

func (a storeDraftAccess) CanManage(record clmcontract.Aggregate) bool {
	return authorizeContract(record, a.user, access.Manage)
}

type storeDraftWorkflowReader struct {
	tx   pgx.Tx
	user accounts.User
}

func (r storeDraftWorkflowReader) ReadContractWorkflow(ctx context.Context, tenantID, contractID string) (contractapp.ContractWorkflowFacts, error) {
	ctx = commandContext(ctx, r.user)
	var facts contractapp.ContractWorkflowFacts
	err := r.tx.QueryRow(ctx, `SELECT
		COALESCE((SELECT state FROM clm_approval_rounds WHERE tenant_id=$1 AND contract_id=$2 ORDER BY round_number DESC LIMIT 1),''),
		(SELECT approval_round_id FROM clm_approval_rounds WHERE tenant_id=$1 AND contract_id=$2 ORDER BY round_number DESC LIMIT 1),
		EXISTS(SELECT 1 FROM clm_approval_rounds WHERE tenant_id=$1 AND contract_id=$2 AND state='in_review')`,
		tenantID, contractID).Scan(&facts.LatestState, &facts.LatestRoundID, &facts.InReview)
	return facts, err
}

func contractDetailFromAppResult(result clmcontract.DraftDetail) ContractDetail {
	return ContractDetail{
		Summary: result.Summary, CurrentRevisionID: result.CurrentRevisionID, Draft: result.Draft,
		ActiveRoundID: result.ActiveRoundID, AllowedCommands: result.AllowedCommands,
		SourceRef: result.SourceRef, SourceID: result.SourceID, SourceObservedAt: result.SourceObservedAt,
		SourceObservedState: result.SourceObservedState, SourceObservationDisposition: result.SourceObservationDisposition,
		Warnings: result.Warnings,
	}
}
