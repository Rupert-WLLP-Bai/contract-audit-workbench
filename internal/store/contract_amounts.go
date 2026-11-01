package store

import (
	"errors"
	"strconv"

	"contractaudit/internal/contractmoney"
	"contractaudit/internal/fault"
)

// Contract money validation bridges the draft content onto the settlement
// identities of internal/contractmoney. Draft saves validate at StageDraft:
// present values keep their grammar while incomplete sections stay legal.
// Legacy registration freezes a revision, so it validates at StageFinal and
// refuses any snapshot whose items, funding and payment plan do not reconcile
// with the contract amount. The check is pure computation inside the already
// authorized write transaction; it issues no SQL and never widens authority.
//
// Failures answer 422 invalid: the submitted payload itself is semantically
// wrong. That is distinct from the 409 conflicts that optimistic concurrency
// and lifecycle state already answer with.

// maxAmountViolationsInMessage bounds the aggregated 422 message; the full
// violation list stays in the server log through the standard error path.
const maxAmountViolationsInMessage = 8

// validateDraftAmounts reconciles one draft's amounts at the requested stage.
func validateDraftAmounts(draft *DraftContent, stage contractmoney.Stage) error {
	amounts, conflictingBase := contractAmountsFromDraft(draft, stage)
	if conflictingBase {
		return fault.Invalid("payments[].ratio_base 在比例计划中必须声明同一个基数")
	}
	result := contractmoney.Validate(amounts)
	if result.Valid {
		return nil
	}
	message := "合同金额与付款计划校验未通过"
	for index, violation := range result.Violations {
		if index == maxAmountViolationsInMessage {
			message += ";（其余 " + strconv.Itoa(len(result.Violations)-index) + " 项从略）"
			break
		}
		message += "; " + violation.Path + ": " + violation.Message
	}
	return fault.Invalid(message)
}

// contractAmountsFromDraft maps the typed draft onto the settlement view.
// Only present values travel: contractmoney treats "" as absent. Draft rows
// carry no currency of their own, so every row inherits the contract currency
// and the cross-currency refusal can only fire on contract-level data.
func contractAmountsFromDraft(draft *DraftContent, stage contractmoney.Stage) (contractmoney.ContractAmounts, bool) {
	amounts := contractmoney.ContractAmounts{Stage: stage}
	if draft == nil || draft.Contract == nil {
		return amounts, false
	}
	basics := draft.Contract
	switch basics.SettlementMode {
	case "fixed_total":
		amounts.Mode = contractmoney.SettlementFixedTotal
	case "framework":
		amounts.Mode = contractmoney.SettlementFramework
	case "by_quantity":
		amounts.Mode = contractmoney.SettlementByQuantity
	}
	amounts.Currency = basics.Currency
	amounts.Amount = basics.Amount
	amounts.PricesIncludeTax = basics.PricesIncludeTax
	for _, item := range draft.Items {
		amounts.Items = append(amounts.Items, contractmoney.Item{
			ItemID: item.ItemID, Quantity: item.Quantity, Unit: item.Unit,
			UnitPrice: item.UnitPrice, LineAmount: item.LineAmount, TaxRate: item.TaxRate,
		})
	}
	for _, funding := range draft.Funding {
		row := contractmoney.Funding{
			Nature: funding.Nature, Channel: funding.Channel, Amount: funding.Amount,
			CostCenter: funding.CostCenter, QuotaNumber: funding.QuotaNumber,
		}
		if funding.FundingYear != nil {
			row.FiscalYear = strconv.Itoa(*funding.FundingYear)
		}
		amounts.Funding = append(amounts.Funding, row)
	}
	base, conflictingBase := draftRatioBase(draft.Payments, stage)
	amounts.RatioBase = base
	for _, plan := range draft.Payments {
		amounts.Payments = append(amounts.Payments, contractmoney.Payment{
			Sequence: plan.Sequence, IsAdvance: plan.IsAdvance != nil && *plan.IsAdvance,
			Ratio: plan.Ratio, Amount: plan.Amount, AdvanceAmount: plan.AdvanceAmount,
			UnitAmount: plan.UnitAmount, FiscalAmount: plan.FiscalAmount,
		})
	}
	return amounts, conflictingBase
}

// draftRatioBase resolves the declared base of a ratio-based plan. The draft
// stores one free-form string per period while the settlement policy needs a
// single Money base for the whole plan. An incomplete draft never fails on
// the base: free text, several distinct texts and a non-Money value all stay
// legal at StageDraft and are only reconciled once the revision freezes.
func draftRatioBase(payments []DraftPaymentPlan, stage contractmoney.Stage) (string, bool) {
	distinct := make([]string, 0, 2)
	for _, plan := range payments {
		if plan.RatioBase == "" {
			continue
		}
		known := false
		for _, value := range distinct {
			if value == plan.RatioBase {
				known = true
				break
			}
		}
		if !known {
			distinct = append(distinct, plan.RatioBase)
		}
	}
	switch {
	case len(distinct) == 0:
		return "", false
	case len(distinct) > 1:
		if stage == contractmoney.StageDraft {
			return "", false
		}
		return "", true
	}
	base := distinct[0]
	if _, err := contractmoney.Parse(contractmoney.MoneySpec, base); err != nil {
		if stage == contractmoney.StageDraft {
			return "", false
		}
		// Forward the raw value so contractmoney reports the malformed base
		// at payments.ratio_base instead of this layer.
		return base, false
	}
	return base, false
}

// legacyAmountsKnown reports whether the historical snapshot carries the
// contract-level amount and currency pair. Unknown historical amounts stay
// legal (the SQL pair constraint stores both NULL together), so registration
// only reconciles what is actually known; a later freeze must enforce the
// full final policy.
func legacyAmountsKnown(snapshot *DraftContent) bool {
	return snapshot != nil && snapshot.Contract != nil &&
		snapshot.Contract.Amount != "" && snapshot.Contract.Currency != ""
}

// amountFaultStatus returns the HTTP status the validation error carries, for
// tests: amount rejections are payload errors, never conflicts.
func amountFaultStatus(err error) int {
	var fe *fault.Error
	if errors.As(err, &fe) {
		return fe.Status
	}
	return 0
}
