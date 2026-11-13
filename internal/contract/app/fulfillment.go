package app

import (
	"context"
	"encoding/json"

	clmcontract "contractaudit/internal/contract/model"
)

// FulfillmentContractCommands exposes only the Contract-owned facts and
// aggregate mutations needed by Fulfillment. Implementations use the caller's
// existing transaction and never own its scope or lifetime.
type FulfillmentContractCommands interface {
	LockWriteReceipt(context.Context, string, string, string, string) error
	LoadWriteReceipt(context.Context, string, string, string, string) (clmcontract.WriteReceipt, bool, error)
	InsertWriteReceipt(context.Context, string, string, string, string, string, string, int, []byte) error
	LockContract(context.Context, string) error
	LoadContract(context.Context, string, string) (clmcontract.Aggregate, error)
	LoadFulfillmentRevision(context.Context, string, string, string) (clmcontract.FrozenContractRevision, error)
	LoadFulfillmentRevisionItem(context.Context, string, string, string, string) (clmcontract.RevisionPlanItem, bool, error)
	ListFulfillmentRevisionItems(context.Context, string, string) ([]FulfillmentRevisionItem, error)
	AdvanceFulfillmentAggregate(context.Context, clmcontract.Aggregate, int64) (clmcontract.Aggregate, error)
}

// FulfillmentRevisionItem binds one immutable Contract item projection to its
// revision without leaking Contract SQL into the Fulfillment adapter.
type FulfillmentRevisionItem struct {
	RevisionID string
	Item       clmcontract.RevisionPlanItem
}

// DecodeFulfillmentSnapshot restores the typed, immutable Contract content
// from one revision already loaded through the current transaction.
func DecodeFulfillmentSnapshot(revision clmcontract.FrozenContractRevision) (clmcontract.DraftContent, error) {
	var draft clmcontract.DraftContent
	if err := json.Unmarshal(revision.Snapshot, &draft); err != nil {
		return clmcontract.DraftContent{}, err
	}
	return draft, nil
}
