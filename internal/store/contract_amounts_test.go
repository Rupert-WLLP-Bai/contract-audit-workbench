package store

import (
	"testing"

	"contractaudit/internal/contractmoney"
)

// reconciledDraft mirrors the fixed-total scenario of the contractmoney
// package: one device line, one self-funded source and a 20% advance period.
func reconciledDraft() DraftContent {
	yes := true
	return DraftContent{Contract: &ContractBasics{
		Name: "测试设备采购合同", Currency: "CNY", Amount: "100000.00",
		SettlementMode: "fixed_total", PricesIncludeTax: &yes,
	},
		Items: []DraftItem{{
			ItemID: "c6052c81-8b05-49ce-89e9-f6e9c6403c10", Quantity: "10.000000",
			Unit: "台", UnitPrice: "10000.0000", LineAmount: "100000.00",
		}},
		Funding: []DraftFunding{{
			Nature: "self_funded", Channel: "unit_payment", Amount: "100000.00",
			FundingYear: new(int), CostCenter: "CC-DEMO-01", QuotaNumber: "YT-DEMO-001",
		}},
		Payments: []DraftPaymentPlan{
			{Sequence: 1, IsAdvance: &yes, Ratio: "0.200000", Amount: "20000.00", RatioBase: "100000.00",
				AdvanceAmount: "20000.00", UnitAmount: "20000.00", FiscalAmount: "0.00"},
			{Sequence: 2, Ratio: "0.800000", Amount: "80000.00", RatioBase: "100000.00",
				AdvanceAmount: "0.00", UnitAmount: "80000.00", FiscalAmount: "0.00"},
		},
	}
}

func TestValidateDraftAmountsKeepsIncompleteDraftsLegal(t *testing.T) {
	if err := validateDraftAmounts(&DraftContent{Contract: &ContractBasics{}}, contractmoney.StageDraft); err != nil {
		t.Fatalf("empty draft must stay legal: %v", err)
	}
	draft := reconciledDraft()
	draft.Funding[0].Amount = "50000.00"
	if err := validateDraftAmounts(&draft, contractmoney.StageDraft); err != nil {
		t.Fatalf("unreconciled draft sums must stay legal at draft stage: %v", err)
	}
	freeText := reconciledDraft()
	freeText.Payments[0].RatioBase = "按合同总价"
	freeText.Payments[1].RatioBase = "按年度预算"
	if err := validateDraftAmounts(&freeText, contractmoney.StageDraft); err != nil {
		t.Fatalf("free-text ratio bases must stay legal at draft stage: %v", err)
	}
}

func TestValidateDraftAmountsStillChecksPresentGrammar(t *testing.T) {
	draft := DraftContent{Contract: &ContractBasics{Amount: "100.0"}}
	err := validateDraftAmounts(&draft, contractmoney.StageDraft)
	if amountFaultStatus(err) != 422 {
		t.Fatalf("malformed present amount must answer 422, got %v", err)
	}
}

func TestValidateDraftAmountsReconcilesFinalStage(t *testing.T) {
	draft := reconciledDraft()
	draft.Funding[0].FundingYear = new(int)
	*draft.Funding[0].FundingYear = 2026
	if err := validateDraftAmounts(&draft, contractmoney.StageFinal); err != nil {
		t.Fatalf("reconciled fixed-total draft must pass the final stage: %v", err)
	}
	amounts, conflicting := contractAmountsFromDraft(&draft, contractmoney.StageFinal)
	if conflicting {
		t.Fatal("single declared base must not conflict")
	}
	if amounts.Funding[0].FiscalYear != "2026" {
		t.Fatalf("funding year must map to the FR-06 fiscal year, got %q", amounts.Funding[0].FiscalYear)
	}
	if !amounts.Payments[0].IsAdvance || amounts.Payments[1].IsAdvance {
		t.Fatal("advance classification must survive the mapping")
	}

	mismatched := reconciledDraft()
	mismatched.Funding[0].Amount = "90000.00"
	err := validateDraftAmounts(&mismatched, contractmoney.StageFinal)
	if amountFaultStatus(err) != 422 {
		t.Fatalf("funding mismatch at final stage must answer 422, got %v", err)
	}

	folded := reconciledDraft()
	folded.Payments[0].UnitAmount = "40000.00"
	err = validateDraftAmounts(&folded, contractmoney.StageFinal)
	if amountFaultStatus(err) != 422 {
		t.Fatalf("advance folded into the unit amount must answer 422, got %v", err)
	}
}

// ratioPlanDraft builds a fresh thirds plan so every case mutates its own
// slice; a struct copy would share the payments backing array.
func ratioPlanDraft() DraftContent {
	return DraftContent{
		Contract: &ContractBasics{Currency: "CNY", Amount: "100.00", SettlementMode: "framework"},
		Payments: []DraftPaymentPlan{
			{Sequence: 1, Ratio: "0.333333", Amount: "33.33", RatioBase: "100.00", AdvanceAmount: "0.00", UnitAmount: "33.33", FiscalAmount: "0.00"},
			{Sequence: 2, Ratio: "0.333333", Amount: "33.33", RatioBase: "100.00", AdvanceAmount: "0.00", UnitAmount: "33.33", FiscalAmount: "0.00"},
			{Sequence: 3, Ratio: "0.333334", Amount: "33.34", RatioBase: "100.00", AdvanceAmount: "0.00", UnitAmount: "33.34", FiscalAmount: "0.00"},
		},
	}
}

func TestValidateDraftAmountsRatioPlanRemainder(t *testing.T) {
	draft := ratioPlanDraft()
	if err := validateDraftAmounts(&draft, contractmoney.StageFinal); err != nil {
		t.Fatalf("thirds plan with the last-period remainder must pass: %v", err)
	}

	noBase := ratioPlanDraft()
	noBase.Payments[0].RatioBase, noBase.Payments[1].RatioBase, noBase.Payments[2].RatioBase = "", "", ""
	err := validateDraftAmounts(&noBase, contractmoney.StageFinal)
	if amountFaultStatus(err) != 422 {
		t.Fatalf("ratio plan without a declared base must answer 422, got %v", err)
	}

	brokenRemainder := ratioPlanDraft()
	brokenRemainder.Payments[2].Amount, brokenRemainder.Payments[2].UnitAmount = "33.33", "33.33"
	err = validateDraftAmounts(&brokenRemainder, contractmoney.StageFinal)
	if amountFaultStatus(err) != 422 {
		t.Fatalf("broken remainder attribution must answer 422, got %v", err)
	}

	splitBases := ratioPlanDraft()
	splitBases.Payments[2].RatioBase = "99.00"
	err = validateDraftAmounts(&splitBases, contractmoney.StageFinal)
	if amountFaultStatus(err) != 422 {
		t.Fatalf("distinct declared bases must answer 422, got %v", err)
	}
}

func TestLegacyAmountsKnownGate(t *testing.T) {
	if legacyAmountsKnown(&DraftContent{}) {
		t.Fatal("snapshot without a contract object has no known amounts")
	}
	if legacyAmountsKnown(&DraftContent{Contract: &ContractBasics{Amount: "100.00"}}) {
		t.Fatal("amount without currency must stay unknown")
	}
	if !legacyAmountsKnown(&DraftContent{Contract: &ContractBasics{Amount: "100.00", Currency: "CNY"}}) {
		t.Fatal("amount and currency together are known")
	}
}
