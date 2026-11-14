// File metadata (manifest mirror):
// Purpose: Preserves exact booked, partial, pending and unknown asset receipt states without treating logical quantity slots as physical serial identities.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	fulfillmentapp "contractaudit/internal/fulfillment/app"
	"math/big"
)

func BookingStatus(planned string, receipts []ContractAssetReceipt) string {
	net := new(big.Rat)
	for _, r := range receipts {
		q, err := fulfillmentapp.ParseDecimal("receipt quantity", r.Quantity, true)
		if err != nil {
			return "unknown"
		}
		if r.EventType == "reversal" {
			net.Sub(net, q)
		} else {
			net.Add(net, q)
		}
	}
	if net.Sign() <= 0 {
		return "pending_booking"
	}
	p, err := fulfillmentapp.ParseDecimal("handover quantity", planned, false)
	if err != nil {
		return "unknown"
	}
	if net.Cmp(p) < 0 {
		return "partial_booking"
	}
	if net.Cmp(p) == 0 {
		return "booked"
	}
	return "unknown"
}
