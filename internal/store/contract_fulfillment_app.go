package store

import (
	"context"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	contractapp "contractaudit/internal/contract/app"
	clmcontract "contractaudit/internal/contract/model"
	contractpostgres "contractaudit/internal/contract/postgres"
	fulfillmentapp "contractaudit/internal/fulfillment/app"
	fulfillmentpostgres "contractaudit/internal/fulfillment/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

type storeFulfillmentAccess struct {
	tx   pgx.Tx
	user accounts.User
}

func (a storeFulfillmentAccess) CanRead(ctx context.Context, record clmcontract.Aggregate) bool {
	return authorizeContractRead(ctx, a.tx, record, a.user)
}

func (a storeFulfillmentAccess) CanReadForManagement(_ context.Context, record clmcontract.Aggregate) bool {
	return authorizeContract(record, a.user, access.Read)
}

func (a storeFulfillmentAccess) CanManage(_ context.Context, record clmcontract.Aggregate) bool {
	return authorizeContract(record, a.user, access.Manage)
}

func (a storeFulfillmentAccess) ResolveMaterial(ctx context.Context, ref clmcontract.MaterialRef, purpose string) (clmcontract.ContractVersionMaterial, error) {
	resolved, err := resolveMaterialVersion(ctx, a.tx, accounts.Tenant(a.user), ref)
	if err != nil {
		return clmcontract.ContractVersionMaterial{}, err
	}
	if err := requireSigningCaseRead(ctx, a.tx, a.user, resolved.CaseOrgID, resolved.CaseID); err != nil {
		return clmcontract.ContractVersionMaterial{}, err
	}
	return clmcontract.ContractVersionMaterial{
		MaterialID: resolved.MaterialID, MaterialVersionID: resolved.DocumentID,
		MaterialVersion: resolved.Version, Filename: resolved.Filename, Digest: resolved.Digest,
		Purpose: purpose, CaseID: resolved.CaseID, CaseOrgID: resolved.CaseOrgID,
	}, nil
}

func (a storeFulfillmentAccess) CanReadCase(ctx context.Context, caseOrgID, caseID string) error {
	return requireSigningCaseRead(ctx, a.tx, a.user, caseOrgID, caseID)
}

type fulfillmentApplication struct {
	principal fulfillmentapp.Principal
	contracts contractapp.FulfillmentContractCommands
	repo      fulfillmentapp.FulfillmentRepository
	access    fulfillmentapp.FulfillmentAccess
	events    contractapp.VersionBusinessEventAppender
	outbox    contractapp.VersionOutboxEnqueuer
}

func newFulfillmentApplication(tx pgx.Tx, current accounts.User) fulfillmentApplication {
	executor := platformpostgres.Bind(tx)
	eventAdapter := storeVersionEventAdapter{tx: tx}
	return fulfillmentApplication{
		principal: fulfillmentapp.Principal{TenantID: accounts.Tenant(current), ActorID: current.ID},
		contracts: contractpostgres.NewFulfillmentContracts(executor),
		repo:      fulfillmentpostgres.NewRepository(executor),
		access:    storeFulfillmentAccess{tx: tx, user: current},
		events:    eventAdapter,
		outbox:    eventAdapter,
	}
}
