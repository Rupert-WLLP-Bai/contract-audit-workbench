package app

import (
	"context"
	contractapp "contractaudit/internal/contract/app"
	contract "contractaudit/internal/contract/model"
	fulfillmentapp "contractaudit/internal/fulfillment/app"
	"time"
)

type Contracts interface {
	LockContract(context.Context, string) error
	LoadContract(context.Context, string, string) (contract.Aggregate, error)
	LockWriteReceipt(context.Context, string, string, string, string) error
	LoadWriteReceipt(context.Context, string, string, string, string) (contract.WriteReceipt, bool, error)
	InsertWriteReceipt(context.Context, string, string, string, string, string, string, int, []byte) error
	LoadFulfillmentRevision(context.Context, string, string, string) (contract.FrozenContractRevision, error)
	LoadFulfillmentRevisionItem(context.Context, string, string, string, string) (contract.RevisionPlanItem, bool, error)
	AdvanceFactVersion(context.Context, string, string, int64) (int64, error)
	LoadSigningRegistration(context.Context, string, string, string) (string, bool, error)
	ListRevisionSnapshots(context.Context, string, string) ([]contractapp.LifecycleRevisionSnapshot, error)
}
type Access = fulfillmentapp.FulfillmentAccess

type AcceptanceReader interface {
	SumAcceptedQuantity(context.Context, string, string, string, string) (string, error)
}
type Repository interface {
	LockSourceEvent(context.Context, string, string, string) error
	LoadReceipt(context.Context, string, string, string) (ContractPaymentReceipt, bool, error)
	SumReversals(context.Context, string, string, string) (string, error)
	InsertReceipt(context.Context, string, ContractPaymentReceipt) (time.Time, error)
	InsertEligibility(context.Context, string, string, ContractPaymentEligibility) (time.Time, error)
	ListReceipts(context.Context, string, string, string, int) ([]ContractPaymentReceipt, error)
}
