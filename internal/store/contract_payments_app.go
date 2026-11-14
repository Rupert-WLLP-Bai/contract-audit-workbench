// File metadata (manifest mirror):
// Purpose: Assembles Contract, Fulfillment and Payment transaction-local ports with existing current authorization and event/outbox adapters for Store compatibility entry points.
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
	contractpg "contractaudit/internal/contract/postgres"
	fulfillmentpg "contractaudit/internal/fulfillment/postgres"
	payment "contractaudit/internal/payment/app"
	paymentpg "contractaudit/internal/payment/postgres"
	platform "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type contractLifecyclePorts struct {
	*contractpg.FulfillmentContracts
	*contractpg.LifecycleFacts
}
type paymentApplication struct {
	contracts  payment.Contracts
	repository payment.Repository
	acceptance payment.AcceptanceReader
	access     payment.Access
	events     storeVersionEventAdapter
}

func newPaymentApplication(tx pgx.Tx, user accounts.User) paymentApplication {
	executor := platform.Bind(tx)
	return paymentApplication{contracts: contractLifecyclePorts{contractpg.NewFulfillmentContracts(executor), contractpg.NewLifecycleFacts(executor)}, repository: paymentpg.NewRepository(executor), acceptance: fulfillmentpg.NewRepository(executor), access: storeFulfillmentAccess{tx: tx, user: user}, events: storeVersionEventAdapter{tx: tx}}
}
