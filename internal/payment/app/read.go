// File metadata (manifest mirror):
// Purpose: Builds historical payment plans and exact net-paid states from narrowly decoded immutable currency/payment snapshots and source receipts.
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
	"encoding/json"
	"math/big"
)

func ListPayments(ctx context.Context, record contract.Aggregate, c Contracts, r Repository) ([]ContractPaymentSummary, error) {
	snapshots, err := c.ListRevisionSnapshots(ctx, record.TenantID, record.ContractID)
	if err != nil {
		return nil, err
	}
	var out []ContractPaymentSummary
	for _, frozen := range snapshots {
		var draft struct {
			Contract *struct {
				Currency string `json:"currency"`
			} `json:"contract"`
			Payments []contract.DraftPaymentPlan `json:"payments"`
		}
		if err := json.Unmarshal(frozen.Snapshot, &draft); err != nil {
			return nil, err
		}
		currency := ""
		if draft.Contract != nil {
			currency = draft.Contract.Currency
		}
		for _, plan := range draft.Payments {
			item := ContractPaymentSummary{ContractID: record.ContractID, ContractRevisionID: frozen.RevisionID, PaymentSequence: plan.Sequence, PlannedAmount: plan.Amount, Currency: currency, NetPaid: "0.00", State: "unknown", Receipts: []ContractPaymentReceipt{}}
			receipts, err := r.ListReceipts(ctx, record.TenantID, record.ContractID, frozen.RevisionID, plan.Sequence)
			if err != nil {
				return nil, err
			}
			net := new(big.Int)
			for _, receipt := range receipts {
				item.Receipts = append(item.Receipts, receipt)
				cents, ok := paymentCents(receipt.Amount)
				if !ok {
					return nil, fault.Conflict("付款事实金额格式无效")
				}
				if receipt.EventType == "payment" {
					net.Add(net, big.NewInt(cents))
				} else {
					net.Sub(net, big.NewInt(cents))
				}
			}
			item.NetPaid = formatPaymentCents(net)
			if len(item.Receipts) > 0 {
				if _, ok := canonicalPaymentAmount(plan.Amount); ok {
					item.State = "unpaid"
				}
			}
			if net.Sign() > 0 {
				if planned, ok := paymentCents(plan.Amount); ok {
					item.State = "partial"
					if net.Cmp(big.NewInt(planned)) == 0 {
						item.State = "paid"
					}
					if net.Cmp(big.NewInt(planned)) > 0 {
						item.State = "overpaid"
					}
				} else {
					item.State = "unknown"
				}
			}
			out = append(out, item)
		}
	}
	return out, nil
}
