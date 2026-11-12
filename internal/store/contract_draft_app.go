package store

import (
	"context"

	"contractaudit/internal/accounts"
	contractapp "contractaudit/internal/contract/app"
	clmcontract "contractaudit/internal/contract/model"
	contractpostgres "contractaudit/internal/contract/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"

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
	return authorizeContract(record, a.user, "case.manage")
}

func contractDetailFromDraftResult(result clmcontract.CreateDraftResult) ContractDetail {
	return ContractDetail{
		Summary: result.Summary, CurrentRevisionID: result.CurrentRevisionID, Draft: result.Draft,
		ActiveRoundID: result.ActiveRoundID, AllowedCommands: result.AllowedCommands,
		SourceRef: result.SourceRef, SourceID: result.SourceID, SourceObservedAt: result.SourceObservedAt,
		SourceObservedState: result.SourceObservedState, SourceObservationDisposition: result.SourceObservationDisposition,
		Warnings: result.Warnings,
	}
}
