// File metadata (manifest mirror):
// Purpose: Wires same-transaction Contract, Fulfillment and Asset ports, current material/case access, identity directory snapshots and existing event/outbox adapters for Asset compatibility entry points.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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
