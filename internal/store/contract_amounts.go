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
