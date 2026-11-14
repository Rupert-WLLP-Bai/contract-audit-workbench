// File metadata (manifest mirror):
// Purpose: Defines narrow consumer-specific Asset contracts for handovers, trusted receipts, directory facts and historical list composition without persistence or transaction ownership.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"contractaudit/internal/accounts"
	contractapp "contractaudit/internal/contract/app"
	contract "contractaudit/internal/contract/model"
	fulfillment "contractaudit/internal/fulfillment/app"
	"time"
)

type Contracts interface {
	LockWriteReceipt(context.Context, string, string, string, string) error
	LoadWriteReceipt(context.Context, string, string, string, string) (contract.WriteReceipt, bool, error)
	InsertWriteReceipt(context.Context, string, string, string, string, string, string, int, []byte) error
	LockContract(context.Context, string) error
	LoadContract(context.Context, string, string) (contract.Aggregate, error)
	LoadFulfillmentRevision(context.Context, string, string, string) (contract.FrozenContractRevision, error)
	LoadFulfillmentRevisionItem(context.Context, string, string, string, string) (contract.RevisionPlanItem, bool, error)
	AdvanceFulfillmentAggregate(context.Context, contract.Aggregate, int64) (contract.Aggregate, error)
	LoadSigningRegistration(context.Context, string, string, string) (string, bool, error)
}
type AcceptanceReader interface {
	LoadAcceptance(context.Context, string, string, string) (fulfillment.AcceptanceFact, bool, error)
}
type HandoverRepository interface {
	LoadPlan(context.Context, string, string, string, string) (ProjectedPlan, bool, error)
	HasOverlap(context.Context, string, string, string, string, string, string, string) (bool, error)
	AllocatedQuantity(context.Context, string, string, string, string) (string, error)
	InsertHandover(context.Context, string, string, ContractAssetHandover, string, []byte) (time.Time, error)
}
type Directory interface {
	LookupUserSnapshot(string) accounts.UserDirectorySnapshot
}
type Access = fulfillment.FulfillmentAccess

type ReceiptContracts interface {
	LockContract(context.Context, string) error
	LoadContract(context.Context, string, string) (contract.Aggregate, error)
	AdvanceFactVersion(context.Context, string, string, int64) (int64, error)
}
type ReceiptHandover struct{ RevisionID, AssetPlanID, ItemID, Unit, Quantity string }
type ReceiptRepository interface {
	LockSourceEvent(context.Context, string, string, string) error
	LoadReceiptDigest(context.Context, string, string, string) (string, bool, error)
	LoadReceipt(context.Context, string, string, string) (ContractAssetReceipt, error)
	LoadReceiptHandover(context.Context, string, string, string) (ReceiptHandover, bool, error)
	SumReceiptReversals(context.Context, string, string, string) (string, error)
	NetHandoverReceipts(context.Context, string, string, string) (string, error)
	InsertReceipt(context.Context, string, ContractAssetReceipt) (time.Time, error)
}

type RevisionReader interface {
	ListRevisionSnapshots(context.Context, string, string) ([]contractapp.LifecycleRevisionSnapshot, error)
}
type HandoverRow struct {
	Handover ContractAssetHandover
	Evidence []byte
}
type ListRepository interface {
	LoadProjectedPlans(context.Context, string, string, string) (map[string]ProjectedPlan, error)
	ListHandovers(context.Context, string, string, string, string) ([]HandoverRow, error)
	ListReceipts(context.Context, string, string, string) ([]ContractAssetReceipt, error)
}
