package store

import (
	"contractaudit/internal/accounts"
	asset "contractaudit/internal/asset/app"
	assetpg "contractaudit/internal/asset/postgres"
	contractpg "contractaudit/internal/contract/postgres"
	fulfillment "contractaudit/internal/fulfillment/app"
	fulfillmentpg "contractaudit/internal/fulfillment/postgres"
	platform "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type assetDirectory struct{}

func (assetDirectory) LookupUserSnapshot(id string) accounts.UserDirectorySnapshot {
	return accounts.LookupUserSnapshot(id)
}

type assetApplication struct {
	contracts         asset.Contracts
	revisions         asset.RevisionReader
	listRepository    asset.ListRepository
	receiptContracts  asset.ReceiptContracts
	receiptRepository asset.ReceiptRepository
	repository        asset.HandoverRepository
	access            fulfillment.FulfillmentAccess
	acceptance        asset.AcceptanceReader
	directory         asset.Directory
	events            storeVersionEventAdapter
}

func newAssetApplication(tx pgx.Tx, current accounts.User) assetApplication {
	executor := platform.Bind(tx)
	contracts := contractLifecyclePorts{contractpg.NewFulfillmentContracts(executor), contractpg.NewLifecycleFacts(executor)}
	repository := assetpg.NewRepository(executor)
	return assetApplication{
		contracts:         contracts,
		revisions:         contracts,
		listRepository:    repository,
		receiptContracts:  contracts,
		receiptRepository: repository,
		repository:        repository,
		access:            storeFulfillmentAccess{tx: tx, user: current},
		acceptance:        fulfillmentpg.NewRepository(executor),
		directory:         assetDirectory{},
		events:            storeVersionEventAdapter{tx: tx},
	}
}
