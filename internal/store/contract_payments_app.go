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

type paymentContracts struct {
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
	return paymentApplication{contracts: paymentContracts{contractpg.NewFulfillmentContracts(executor), contractpg.NewLifecycleFacts(executor)}, repository: paymentpg.NewRepository(executor), acceptance: fulfillmentpg.NewRepository(executor), access: storeFulfillmentAccess{tx: tx, user: user}, events: storeVersionEventAdapter{tx: tx}}
}
