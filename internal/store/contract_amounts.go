// File metadata (manifest mirror):
// Purpose: Contract money validation bridges the draft content onto the settlement identities of internal/contractmoney. Draft saves validate at StageDraft: present values keep their grammar… Related `store` operations include validateDraftAmounts, contractAmountsFromDraft, draftRatioBase, legacyAmountsKnown.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-12
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"errors"

	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/contractmoney"
	"contractaudit/internal/fault"
)

func validateDraftAmounts(draft *DraftContent, stage contractmoney.Stage) error {
	return clmcontract.ValidateDraftAmounts(draft, stage)
}
func contractAmountsFromDraft(draft *DraftContent, stage contractmoney.Stage) (contractmoney.ContractAmounts, bool) {
	return clmcontract.ContractAmountsFromDraft(draft, stage)
}
func legacyAmountsKnown(snapshot *DraftContent) bool { return clmcontract.LegacyAmountsKnown(snapshot) }
func amountFaultStatus(err error) int {
	var fe *fault.Error
	if errors.As(err, &fe) {
		return fe.Status
	}
	return 0
}
