// File metadata (manifest mirror):
// Purpose: Loads committed task command receipts only after matching request digests and rechecking current Contract Read visibility.
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
	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

type receiptReader interface {
	LoadWriteReceipt(context.Context, string, string, string, string) (contract.WriteReceipt, bool, error)
	LoadContract(context.Context, string, string) (contract.Aggregate, error)
}
type receiptReadAccess interface {
	CanRead(context.Context, contract.Aggregate) bool
}

// loadAuthorizedReceipt preserves current contract Read checks on replay.
// The caller has already acquired the idempotency lock.
func loadAuthorizedReceipt(ctx context.Context, reader receiptReader, access receiptReadAccess, tenantID, actorID, route, key, digest string) (contract.WriteReceipt, bool, error) {
	receipt, found, err := reader.LoadWriteReceipt(ctx, tenantID, actorID, route, key)
	if err != nil || !found {
		return receipt, found, err
	}
	if receipt.RequestDigest != digest {
		return contract.WriteReceipt{}, false, fault.Conflict("Idempotency-Key已用于不同请求载荷")
	}
	record, err := reader.LoadContract(ctx, tenantID, receipt.ContractID)
	if err != nil {
		return contract.WriteReceipt{}, false, err
	}
	if !access.CanRead(ctx, record) {
		return contract.WriteReceipt{}, false, fault.NotFound("没有找到对应记录")
	}
	return receipt, true, nil
}
