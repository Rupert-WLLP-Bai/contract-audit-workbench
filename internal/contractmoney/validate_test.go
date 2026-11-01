package contractmoney

import "testing"

// fixedTotalDraft is scenario SC-01 version 1 of fixtures/clm-scenarios.json:
// one demo device line, one self-funded source, and two payment periods with
// a 20% advance. All data is synthetic.
func fixedTotalDraft() ContractAmounts {
	yes := true
	return ContractAmounts{
		Mode:             SettlementFixedTotal,
		Stage:            StageFinal,
		Currency:         "CNY",
		Amount:           "100000.00",
		PricesIncludeTax: &yes,
		RatioBase:        "100000.00",
		Items: []Item{{
			ItemID:     "IT-1",
			Quantity:   "10.000000",
			Unit:       "台",
			UnitPrice:  "10000.0000",
			LineAmount: "100000.00",
		}},
		Funding: []Funding{{
			Nature:      "self_funded",
			Channel:     "unit_payment",
			Amount:      "100000.00",
			QuotaNumber: "YT-DEMO-001",
			FiscalYear:  "2026",
			CostCenter:  "CC-DEMO-01",
		}},
		Payments: []Payment{
			{Sequence: 1, IsAdvance: true, Ratio: "0.200000", Amount: "20000.00",
				AdvanceAmount: "20000.00", UnitAmount: "20000.00", FiscalAmount: "0.00"},
			{Sequence: 2, IsAdvance: false, Ratio: "0.800000", Amount: "80000.00",
				AdvanceAmount: "0.00", UnitAmount: "80000.00", FiscalAmount: "0.00"},
		},
	}
}

// ratioPlan is a framework plan whose thirds only reconcile through the
// explicit last-period remainder.
func ratioPlan() ContractAmounts {
	return ContractAmounts{
		Mode:      SettlementFramework,
		Stage:     StageFinal,
		Currency:  "CNY",
		Amount:    "100.00",
		RatioBase: "100.00",
		Payments: []Payment{
			{Sequence: 1, Ratio: "0.333333", Amount: "33.33", AdvanceAmount: "0.00", UnitAmount: "33.33", FiscalAmount: "0.00"},
			{Sequence: 2, Ratio: "0.333333", Amount: "33.33", AdvanceAmount: "0.00", UnitAmount: "33.33", FiscalAmount: "0.00"},
			{Sequence: 3, Ratio: "0.333334", Amount: "33.34", AdvanceAmount: "0.00", UnitAmount: "33.34", FiscalAmount: "0.00"},
		},
	}
}

func frameworkDraft() ContractAmounts {
	return ContractAmounts{
		Mode:     SettlementFramework,
		Stage:    StageFinal,
		Currency: "CNY",
		Amount:   "100000.00",
		Funding: []Funding{{
			Nature: "self_funded", Channel: "unit_payment", Amount: "50000.00",
		}},
		Payments: []Payment{{
			Sequence: 1, Amount: "50000.00", AdvanceAmount: "0.00",
			UnitAmount: "50000.00", FiscalAmount: "0.00",
		}},
	}
}

func mutated(base ContractAmounts, change func(*ContractAmounts)) ContractAmounts {
	next := base
	change(&next)
	return next
}

func violationAt(t *testing.T, res Result, path string) Violation {
	t.Helper()
	for _, v := range res.Violations {
		if v.Path == path {
			return v
		}
	}
	t.Fatalf("no violation at %s; got %+v", path, res.Violations)
	return Violation{}
}

func wantNoViolations(t *testing.T, res Result) {
	t.Helper()
	if len(res.Violations) != 0 {
		t.Fatalf("expected no violations, got %+v", res.Violations)
	}
}

func TestFixedTotalScenarioIsValid(t *testing.T) {
	res := Validate(fixedTotalDraft())
	if !res.Valid {
		t.Fatalf("expected a valid plan, got %+v", res.Violations)
	}
	report := res.Report
	if report.Amount != "100000.00" || report.ItemsTotal != "100000.00" ||
		report.FundingTotal != "100000.00" || report.PaymentsTotal != "100000.00" {
		t.Fatalf("grand totals drifted: %+v", report)
	}
	if report.AdvanceTotal != "20000.00" {
		t.Fatalf("advance classification total = %q, want 20000.00", report.AdvanceTotal)
	}
	if report.UnitTotal != "100000.00" || report.FiscalTotal != "0.00" {
		t.Fatalf("unit/fiscal totals drifted: %+v", report)
	}
}

func TestAdvanceRecalculationIsValid(t *testing.T) {
	// SC-01 version 2: the advance share drops from 20% to 10% and the final
	// payment rises accordingly; the identities must still hold.
	res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
		in.Payments[0] = Payment{Sequence: 1, IsAdvance: true, Ratio: "0.100000",
			Amount: "10000.00", AdvanceAmount: "10000.00", UnitAmount: "10000.00", FiscalAmount: "0.00"}
		in.Payments[1] = Payment{Sequence: 2, IsAdvance: false, Ratio: "0.900000",
			Amount: "90000.00", AdvanceAmount: "0.00", UnitAmount: "90000.00", FiscalAmount: "0.00"}
	}))
	if !res.Valid {
		t.Fatalf("expected a valid recalculation, got %+v", res.Violations)
	}
	if res.Report.AdvanceTotal != "10000.00" || res.Report.PaymentsTotal != "100000.00" {
		t.Fatalf("recalculated totals drifted: %+v", res.Report)
	}
}

func TestAdvanceClassificationViolations(t *testing.T) {
	t.Run("advance folded on top breaks the classification", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Payments[0].AdvanceAmount = "25000.00"
		}))
		v := violationAt(t, res, "payments[0].advance_amount")
		if v.Code != CodeMismatch || v.Expected != "20000.00" || v.Actual != "25000.00" {
			t.Fatalf("unexpected violation: %+v", v)
		}
	})
	t.Run("non-advance period must carry zero", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Payments[1].AdvanceAmount = "5000.00"
		}))
		v := violationAt(t, res, "payments[1].advance_amount")
		if v.Code != CodeMismatch || v.Expected != "0.00" || v.Actual != "5000.00" {
			t.Fatalf("unexpected violation: %+v", v)
		}
	})
	t.Run("advance added into the unit amount breaks the period identity", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Payments[0].UnitAmount = "40000.00"
		}))
		v := violationAt(t, res, "payments[0].unit_amount")
		if v.Code != CodeMismatch || v.Expected != "20000.00" || v.Actual != "40000.00" {
			t.Fatalf("unexpected violation: %+v", v)
		}
	})
}

func TestRatioRemainderAttribution(t *testing.T) {
	taxInclusive := true
	t.Run("last period absorbs the remainder", func(t *testing.T) {
		res := Validate(ratioPlan())
		wantNoViolations(t, res)
	})
	t.Run("final period must equal the residual", func(t *testing.T) {
		res := Validate(mutated(ratioPlan(), func(in *ContractAmounts) {
			in.Payments[2].Amount = "33.33"
			in.Payments[2].UnitAmount = "33.33"
		}))
		v := violationAt(t, res, "payments[2].amount")
		if v.Code != CodeMismatch || v.Expected != "33.34" || v.Actual != "33.33" {
			t.Fatalf("unexpected violation: %+v", v)
		}
	})
	t.Run("non-final periods are exact ratio products", func(t *testing.T) {
		res := Validate(mutated(ratioPlan(), func(in *ContractAmounts) {
			in.Payments[0].Amount = "33.34"
			in.Payments[0].UnitAmount = "33.34"
		}))
		if len(res.Violations) != 2 {
			t.Fatalf("expected the ratio and residual violations, got %+v", res.Violations)
		}
		if v := violationAt(t, res, "payments[0].amount"); v.Expected != "33.33" || v.Actual != "33.34" {
			t.Fatalf("unexpected ratio violation: %+v", v)
		}
		if v := violationAt(t, res, "payments[2].amount"); v.Expected != "33.33" || v.Actual != "33.34" {
			t.Fatalf("unexpected residual violation: %+v", v)
		}
	})
	t.Run("ratio base is never assumed", func(t *testing.T) {
		res := Validate(mutated(ratioPlan(), func(in *ContractAmounts) { in.RatioBase = "" }))
		v := violationAt(t, res, "payments.ratio_base")
		if v.Code != CodeRatioBaseMissing {
			t.Fatalf("unexpected violation: %+v", v)
		}
	})
	t.Run("plan without ratios needs no base", func(t *testing.T) {
		res := Validate(ContractAmounts{
			Mode: SettlementFixedTotal, Stage: StageFinal, Currency: "CNY", Amount: "100.00",
			PricesIncludeTax: &taxInclusive,
			Items:            []Item{{ItemID: "IT-1", Quantity: "1.000000", Unit: "件", UnitPrice: "100.0000", LineAmount: "100.00"}},
			Funding:          []Funding{{Nature: "self_funded", Channel: "unit_payment", Amount: "100.00"}},
			Payments: []Payment{{
				Sequence: 1, Amount: "100.00", AdvanceAmount: "0.00", UnitAmount: "100.00", FiscalAmount: "0.00",
			}},
		})
		wantNoViolations(t, res)
	})
}

func TestDecimalRejectionsByPath(t *testing.T) {
	cases := []struct {
		name   string
		stage  Stage
		change func(*ContractAmounts)
		path   string
		code   string
	}{
		{"float artifact scale", StageDraft, func(in *ContractAmounts) { in.Amount = "100000.0" }, "contract.amount", CodeMalformedDecimal},
		{"exponent notation", StageDraft, func(in *ContractAmounts) { in.Amount = "1e5" }, "contract.amount", CodeMalformedDecimal},
		{"negative money", StageFinal, func(in *ContractAmounts) { in.Amount = "-1.00" }, "contract.amount", CodeMalformedDecimal},
		{"leading zero", StageFinal, func(in *ContractAmounts) { in.Amount = "0100.00" }, "contract.amount", CodeMalformedDecimal},
		{"money precision overflow", StageFinal, func(in *ContractAmounts) { in.Amount = "99999999999999999.99" }, "contract.amount", CodeOverflow},
		{"ratio scale", StageDraft, func(in *ContractAmounts) { in.Payments[0].Ratio = "0.5" }, "payments[0].ratio", CodeMalformedDecimal},
		{"ratio above one", StageFinal, func(in *ContractAmounts) { in.Payments[0].Ratio = "1.000001" }, "payments[0].ratio", CodeMalformedDecimal},
		{"tax rate scale", StageDraft, func(in *ContractAmounts) { in.Items[0].TaxRate = "0.13000" }, "items[0].tax_rate", CodeMalformedDecimal},
		{"tax rate above one", StageFinal, func(in *ContractAmounts) { in.Items[0].TaxRate = "1.0001" }, "items[0].tax_rate", CodeMalformedDecimal},
		{"ratio base malformed", StageFinal, func(in *ContractAmounts) { in.RatioBase = "100.5" }, "payments.ratio_base", CodeMalformedDecimal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
				in.Stage = c.stage
				c.change(in)
			}))
			if res.Valid {
				t.Fatalf("expected a violation")
			}
			if len(res.Violations) != 1 {
				t.Fatalf("expected exactly one violation, got %+v", res.Violations)
			}
			if got := res.Violations[0]; got.Path != c.path || got.Code != c.code {
				t.Fatalf("violation = %+v, want path %s code %s", got, c.path, c.code)
			}
		})
	}
}

func TestFinalStageRequiredFields(t *testing.T) {
	t.Run("empty final input", func(t *testing.T) {
		res := Validate(ContractAmounts{Stage: StageFinal})
		want := []struct{ path, code string }{
			{"contract.settlement_mode", CodeInvalidMode},
			{"contract.currency", CodeMissing},
			{"contract.amount", CodeMissing},
		}
		if len(res.Violations) != len(want) {
			t.Fatalf("expected %d violations, got %+v", len(want), res.Violations)
		}
		for i, w := range want {
			if got := res.Violations[i]; got.Path != w.path || got.Code != w.code {
				t.Fatalf("violation %d = %+v, want path %s code %s", i, got, w.path, w.code)
			}
		}
	})
	t.Run("unset stage fails closed", func(t *testing.T) {
		res := Validate(ContractAmounts{})
		if res.Valid || len(res.Violations) != 3 {
			t.Fatalf("expected the final-stage violations, got %+v", res.Violations)
		}
	})
	t.Run("absent item quantity", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Items[0].Quantity = ""
		}))
		if v := violationAt(t, res, "items[0].quantity"); v.Code != CodeMissing {
			t.Fatalf("unexpected violation: %+v", v)
		}
	})
	t.Run("absent unit amount", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Payments[0].UnitAmount = ""
		}))
		if v := violationAt(t, res, "payments[0].unit_amount"); v.Code != CodeMissing {
			t.Fatalf("unexpected violation: %+v", v)
		}
	})
	t.Run("absent advance amount", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Payments[0].AdvanceAmount = ""
		}))
		if v := violationAt(t, res, "payments[0].advance_amount"); v.Code != CodeMissing {
			t.Fatalf("unexpected violation: %+v", v)
		}
	})
}

func TestQuantityMustBePositive(t *testing.T) {
	t.Run("final stage rejects zero quantity", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Items[0].Quantity = "0.000000"
		}))
		if v := violationAt(t, res, "items[0].quantity"); v.Code != CodeNotPositive {
			t.Fatalf("unexpected violation: %+v", v)
		}
		if v := violationAt(t, res, "items[0].line_amount"); v.Expected != "0.00" {
			t.Fatalf("zero quantity must also break the line identity: %+v", v)
		}
	})
	t.Run("draft stage keeps zero well-formed", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Stage = StageDraft
			in.Items[0].Quantity = "0.000000"
		}))
		wantNoViolations(t, res)
	})
}

func TestSequenceRules(t *testing.T) {
	t.Run("sequence below one", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Payments[0].Sequence = 0
		}))
		if v := violationAt(t, res, "payments[0].sequence"); v.Code != CodeNotPositive {
			t.Fatalf("unexpected violation: %+v", v)
		}
	})
	t.Run("duplicate sequence", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Payments[1].Sequence = 1
		}))
		if v := violationAt(t, res, "payments[1].sequence"); v.Code != CodeDuplicate {
			t.Fatalf("unexpected violation: %+v", v)
		}
	})
}

func TestCurrencyMismatchRefusesSum(t *testing.T) {
	t.Run("funding currency refused", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Funding[0].Currency = "USD"
		}))
		if len(res.Violations) != 1 {
			t.Fatalf("expected only the currency violation, got %+v", res.Violations)
		}
		v := violationAt(t, res, "funding[0].currency")
		if v.Code != CodeCurrencyMismatch || v.Expected != "CNY" || v.Actual != "USD" {
			t.Fatalf("unexpected violation: %+v", v)
		}
		if res.Report.FundingTotal != "" {
			t.Fatalf("mixed-currency total must not be reported: %q", res.Report.FundingTotal)
		}
	})
	t.Run("payment currency refused", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Payments[1].Currency = "USD"
		}))
		if len(res.Violations) != 1 {
			t.Fatalf("expected only the currency violation, got %+v", res.Violations)
		}
		if v := violationAt(t, res, "payments[1].currency"); v.Code != CodeCurrencyMismatch {
			t.Fatalf("unexpected violation: %+v", v)
		}
	})
	t.Run("item currency refused", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Items[0].Currency = "USD"
		}))
		if len(res.Violations) != 1 {
			t.Fatalf("expected only the currency violation, got %+v", res.Violations)
		}
		if v := violationAt(t, res, "items[0].currency"); v.Code != CodeCurrencyMismatch {
			t.Fatalf("unexpected violation: %+v", v)
		}
	})
	t.Run("declared matching currency is accepted", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Items[0].Currency = "CNY"
			in.Funding[0].Currency = "CNY"
			in.Payments[0].Currency = "CNY"
			in.Payments[1].Currency = "CNY"
		}))
		wantNoViolations(t, res)
	})
}

func TestSettlementModeIdentities(t *testing.T) {
	t.Run("framework does not force the fixed-total identity", func(t *testing.T) {
		res := Validate(frameworkDraft())
		wantNoViolations(t, res)
		if res.Report.FundingTotal != "50000.00" || res.Report.PaymentsTotal != "50000.00" {
			t.Fatalf("framework totals drifted: %+v", res.Report)
		}
	})
	t.Run("fixed total forces the grand identity", func(t *testing.T) {
		in := frameworkDraft()
		in.Mode = SettlementFixedTotal
		res := Validate(in)
		if res.Valid || len(res.Violations) != 3 {
			t.Fatalf("expected the three section mismatches, got %+v", res.Violations)
		}
		want := []struct{ path, actual string }{
			{"items", "0.00"},
			{"funding", "50000.00"},
			{"payments", "50000.00"},
		}
		for i, w := range want {
			got := res.Violations[i]
			if got.Path != w.path || got.Code != CodeMismatch || got.Expected != "100000.00" || got.Actual != w.actual {
				t.Fatalf("violation %d = %+v, want path %s actual %s", i, got, w.path, w.actual)
			}
		}
	})
	t.Run("unknown mode fails closed without claiming identities", func(t *testing.T) {
		in := frameworkDraft()
		in.Mode = "per_milestone"
		res := Validate(in)
		if len(res.Violations) != 1 {
			t.Fatalf("expected only the mode violation, got %+v", res.Violations)
		}
		if got := res.Violations[0]; got.Path != "contract.settlement_mode" || got.Code != CodeInvalidMode {
			t.Fatalf("unexpected violation: %+v", got)
		}
	})
}

func TestDraftStageDoesNotForceCompleteness(t *testing.T) {
	t.Run("empty draft is valid", func(t *testing.T) {
		res := Validate(ContractAmounts{Stage: StageDraft})
		wantNoViolations(t, res)
	})
	t.Run("unreconciled sums are not violations yet", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Stage = StageDraft
			in.Funding[0].Amount = "50000.00"
		}))
		wantNoViolations(t, res)
	})
	t.Run("present values still keep their grammar", func(t *testing.T) {
		res := Validate(mutated(fixedTotalDraft(), func(in *ContractAmounts) {
			in.Stage = StageDraft
			in.Amount = "abc"
		}))
		if len(res.Violations) != 1 {
			t.Fatalf("expected the malformed amount, got %+v", res.Violations)
		}
		if got := res.Violations[0]; got.Path != "contract.amount" || got.Code != CodeMalformedDecimal {
			t.Fatalf("unexpected violation: %+v", got)
		}
	})
}

func TestTaxAndDiscountNeverAssumed(t *testing.T) {
	taxInclusive := true
	in := ContractAmounts{
		Mode: SettlementFixedTotal, Stage: StageFinal, Currency: "CNY", Amount: "100.00",
		PricesIncludeTax: &taxInclusive,
		Items:            []Item{{ItemID: "IT-1", Quantity: "1.000000", Unit: "件", UnitPrice: "100.0000", LineAmount: "100.00", TaxRate: "0.1300"}},
		Funding:          []Funding{{Nature: "self_funded", Channel: "unit_payment", Amount: "100.00"}},
		Payments: []Payment{{
			Sequence: 1, Amount: "100.00", AdvanceAmount: "0.00", UnitAmount: "100.00", FiscalAmount: "0.00",
		}},
	}
	res := Validate(in)
	wantNoViolations(t, res)

	// A line that only reconciles through the unmodeled 13% net amount is a
	// mismatch, not an implied discount.
	in.Items[0].LineAmount = "88.50"
	res = Validate(in)
	if len(res.Violations) != 2 {
		t.Fatalf("expected the line and section mismatches, got %+v", res.Violations)
	}
	if v := violationAt(t, res, "items[0].line_amount"); v.Expected != "100.00" || v.Actual != "88.50" {
		t.Fatalf("unexpected line violation: %+v", v)
	}
	if v := violationAt(t, res, "items"); v.Actual != "88.50" {
		t.Fatalf("unexpected section violation: %+v", v)
	}
}

func TestSectionSumOverflow(t *testing.T) {
	in := ContractAmounts{
		Mode: SettlementFixedTotal, Stage: StageFinal, Currency: "CNY", Amount: "0.00",
		Payments: []Payment{
			{Sequence: 1, Amount: "9999999999999999.99", AdvanceAmount: "0.00",
				UnitAmount: "9999999999999999.99", FiscalAmount: "0.00"},
			{Sequence: 2, Amount: "9999999999999999.99", AdvanceAmount: "0.00",
				UnitAmount: "9999999999999999.99", FiscalAmount: "0.00"},
		},
	}
	res := Validate(in)
	if res.Valid || len(res.Violations) != 2 {
		t.Fatalf("expected both plan sums to overflow, got %+v", res.Violations)
	}
	for _, got := range res.Violations {
		if got.Path != "payments" || got.Code != CodeOverflow {
			t.Fatalf("unexpected violation: %+v", got)
		}
	}
	if res.Report.PaymentsTotal != "" {
		t.Fatalf("overflowed sum must not be reported: %q", res.Report.PaymentsTotal)
	}
}

func TestUnitFiscalSumOverflow(t *testing.T) {
	in := ContractAmounts{
		Mode: SettlementFramework, Stage: StageFinal, Currency: "CNY", Amount: "9999999999999999.99",
		Payments: []Payment{{
			Sequence: 1, Amount: "9999999999999999.99", AdvanceAmount: "0.00",
			UnitAmount: "9999999999999999.99", FiscalAmount: "0.01",
		}},
	}
	res := Validate(in)
	if res.Valid || len(res.Violations) != 1 {
		t.Fatalf("expected one overflow violation, got %+v", res.Violations)
	}
	if got := res.Violations[0]; got.Path != "payments[0].unit_amount" || got.Code != CodeOverflow {
		t.Fatalf("unexpected violation: %+v", got)
	}
}

func TestFixedTotalRequiresDeclaredTaxInclusivePrices(t *testing.T) {
	for _, test := range []struct {
		name  string
		basis *bool
	}{
		{name: "unknown"},
		{name: "exclusive", basis: new(bool)},
	} {
		t.Run(test.name, func(t *testing.T) {
			in := fixedTotalDraft()
			in.PricesIncludeTax = test.basis
			res := Validate(in)
			if res.Valid {
				t.Fatal("fixed total was accepted without an inclusive tax basis")
			}
			violationAt(t, res, "contract.prices_include_tax")
		})
	}
}

func TestRatioPlanChecksTheFinalRatio(t *testing.T) {
	for _, ratio := range []string{"0.100000", ""} {
		in := ratioPlan()
		in.Payments[2].Ratio = ratio
		if res := Validate(in); res.Valid {
			t.Fatalf("accepted final ratio %q for a one-third payment", ratio)
		}
	}
}

func TestDraftNeverReportsMixedCurrencyTotals(t *testing.T) {
	in := fixedTotalDraft()
	in.Stage = StageDraft
	in.Payments[1].Currency = "USD"
	res := Validate(in)
	if res.Report.PaymentsTotal != "" {
		t.Fatalf("mixed-currency payment total was reported: %q", res.Report.PaymentsTotal)
	}
}

func TestRatioBaseAloneDoesNotForceFrameworkTotal(t *testing.T) {
	in := frameworkDraft()
	in.RatioBase = "100000.00"
	wantNoViolations(t, Validate(in))
}
