package contractmoney

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
)

// Stage selects how much Validate enforces.
type Stage string

const (
	// StageDraft checks only the grammar of values that are present: a draft
	// may be incomplete, and sums, ratios, and classifications are not
	// reconciled yet.
	StageDraft Stage = "draft"
	// StageFinal enforces the complete settlement policy. Submit, approval
	// freezing, and registration all validate at StageFinal.
	StageFinal Stage = "final"
)

// SettlementMode selects the identity policy. Only SettlementFixedTotal
// forces items, funding, and payments to reconcile with the contract amount;
// framework and by-quantity contracts keep their per-line and per-period
// checks but are never forced against a fixed grand total.
type SettlementMode string

const (
	SettlementFixedTotal SettlementMode = "fixed_total"
	SettlementFramework  SettlementMode = "framework"
	SettlementByQuantity SettlementMode = "by_quantity"
)

// RemainderPolicy names the frozen remainder attribution: with a declared
// ratio base, every non-final period equals ratio × base rounded
// ROUND_HALF_UP, and the last period absorbs the exact remainder.
const RemainderPolicy = "last_period"

// Settlement-level violation codes. The decimal grammar and overflow codes
// are shared with the low-level errors.
const (
	CodeNotPositive      = "not_positive"
	CodeMissing          = "required_missing"
	CodeMismatch         = "identity_mismatch"
	CodeCurrencyMismatch = "currency_mismatch"
	CodeRatioBaseMissing = "ratio_base_missing"
	CodeInvalidMode      = "invalid_settlement_mode"
	CodeInvalidCurrency  = "invalid_currency"
	CodeDuplicate        = "duplicate_sequence"
)

// Item is one procurement line, carrying only the amount-relevant fields.
// Decimal fields are canonical wire strings; "" means absent, and an empty
// Currency inherits the contract currency.
type Item struct {
	ItemID     string
	Quantity   string
	Unit       string
	UnitPrice  string
	LineAmount string
	TaxRate    string // "" keeps the rate unknown; it is never assumed to be zero
	Currency   string
}

// Funding is one funding source. FiscalYear and CostCenter carry the FR-06
// annual and cost-center facts: the validation never drops or derives them,
// and Report echoes the rows verbatim so they reach the frozen revision.
type Funding struct {
	Nature      string
	Channel     string
	Amount      string
	QuotaNumber string
	FiscalYear  string // FR-06 年度
	CostCenter  string // FR-06 成本中心
	Currency    string
}

// Payment is one planned period. AdvanceAmount is the classification of the
// period itself: it must equal Amount on an advance period and be zero on
// every other period, and it is never added to UnitAmount or FiscalAmount.
// A plan that folded the advance into the period amount fails the
// unit+fiscal identity instead of being silently accepted.
type Payment struct {
	Sequence      int
	IsAdvance     bool
	Ratio         string // "" = not ratio-based
	Amount        string
	AdvanceAmount string
	UnitAmount    string
	FiscalAmount  string
	Currency      string
}

// ContractAmounts is the amount view of one contract revision — exactly what
// submit and freeze handlers hand to Validate.
type ContractAmounts struct {
	Mode     SettlementMode
	Stage    Stage
	Currency string
	Amount   string // contract amount; "" = absent
	// PricesIncludeTax declares the unit-price tax basis. Fixed-total
	// reconciliation currently supports inclusive prices only; tax amounts
	// are not inferred from the rate.
	PricesIncludeTax *bool
	// RatioBase is the declared base the payment ratios apply to, in Money
	// form. It is never assumed to be the contract amount.
	RatioBase string
	Items     []Item
	Funding   []Funding
	Payments  []Payment
}

// Violation is one failed check. Path uses the field-dictionary spelling so
// callers can map it onto API field errors directly.
type Violation struct {
	Path     string
	Code     string
	Message  string
	Expected string // canonical expected value when the check compares values
	Actual   string // canonical actual value when the check compares values
}

// Report summarizes what Validate reconciled. Totals are canonical Money
// strings of the contract currency; "" means no exact total exists because
// the section is empty, holds an unparseable value, or mixes currencies.
// AdvanceTotal is the advance classification subset of PaymentsTotal and is
// never added to UnitTotal or FiscalTotal. Funding is echoed verbatim to
// keep the FR-06 annual and cost-center facts with the validated totals.
type Report struct {
	Mode             SettlementMode
	Stage            Stage
	Currency         string
	Amount           string
	ItemsTotal       string
	FundingTotal     string
	PaymentsTotal    string
	AdvanceTotal     string
	UnitTotal        string
	FiscalTotal      string
	RemainderPolicy  string
	PricesIncludeTax *bool
	Funding          []Funding
}

// Result is the freeze-time answer: Valid is true exactly when no violation
// was recorded. The report is populated even when invalid, over the values
// that did parse.
type Result struct {
	Valid      bool
	Violations []Violation
	Report     Report
}

// zeroMoney is the exact zero of MoneySpec; Decimal is immutable, so the
// shared value is safe to reuse.
var zeroMoney = Decimal{spec: MoneySpec, unscaled: big.NewInt(0)}

// Validate checks one contract revision's amounts at the requested stage and
// returns every violation in a deterministic field order. Anything but
// StageDraft is enforced as StageFinal, so an unset stage fails closed. At
// StageFinal the contract currency, amount, and a known settlement mode are
// required; an unknown mode skips the total identities, because no identity
// policy can be claimed for it. Fixed-total settlements additionally
// reconcile item lines, funding, and the payment plan with the contract
// amount, recompute each line as quantity × unit_price rounded
// ROUND_HALF_UP, and attribute the ratio rounding remainder to the last
// payment period. Tax rates and discounts are never assumed: an amount that
// only reconciles through an unmodeled tax or discount is a mismatch.
func Validate(in ContractAmounts) Result {
	v := &validation{in: in, final: in.Stage != StageDraft}
	res := Result{Report: Report{
		Mode:             in.Mode,
		Stage:            in.Stage,
		Currency:         in.Currency,
		RemainderPolicy:  RemainderPolicy,
		PricesIncludeTax: in.PricesIncludeTax,
		Funding:          in.Funding,
	}}

	var mode SettlementMode
	switch in.Mode {
	case SettlementFixedTotal, SettlementFramework, SettlementByQuantity:
		mode = in.Mode
	default:
		if v.final {
			v.add("contract.settlement_mode", CodeInvalidMode,
				"settlement mode must be fixed_total, framework, or by_quantity", "", "")
		}
	}
	v.fixedTotal = v.final && mode == SettlementFixedTotal
	if v.final && len(in.Items) > 0 {
		switch {
		case in.PricesIncludeTax == nil:
			v.add("contract.prices_include_tax", CodeMissing, "declare whether unit prices include tax", "", "")
		case v.fixedTotal && !*in.PricesIncludeTax:
			v.add("contract.prices_include_tax", CodeMismatch, "fixed-total reconciliation requires tax-inclusive prices until tax amounts are modeled", "true", "false")
		}
	}

	switch {
	case in.Currency == "":
		if v.final {
			v.add("contract.currency", CodeMissing, "contract currency is required at submit and freeze", "", "")
		}
	case isISOCurrency(in.Currency):
		v.currency = in.Currency
	default:
		v.add("contract.currency", CodeInvalidCurrency, "currency must be three uppercase ISO letters", "", "")
	}

	if amount, ok := v.money("contract.amount", in.Amount, true); ok {
		v.amount, v.amountOK = amount, true
		res.Report.Amount = amount.String()
	}

	items := v.validateItems(in.Items)
	funding := v.validateFunding(in.Funding)
	amounts, advances, units, fiscals := v.validatePayments(in.Payments)

	res.Report.ItemsTotal = items.reportTotal()
	res.Report.FundingTotal = funding.reportTotal()
	res.Report.PaymentsTotal = amounts.reportTotal()
	res.Report.AdvanceTotal = advances.reportTotal()
	res.Report.UnitTotal = units.reportTotal()
	res.Report.FiscalTotal = fiscals.reportTotal()

	res.Violations = v.out
	res.Valid = len(v.out) == 0
	return res
}

type validation struct {
	in         ContractAmounts
	final      bool
	fixedTotal bool
	currency   string // contract currency when known, else ""
	amount     Decimal
	amountOK   bool
	out        []Violation
}

func (v *validation) add(path, code, message, expected, actual string) {
	v.out = append(v.out, Violation{Path: path, Code: code, Message: message, Expected: expected, Actual: actual})
}

// decimal parses one present value; absent values only violate when the
// stage requires them.
func (v *validation) decimal(path, raw string, spec Spec, required bool) (Decimal, bool) {
	if raw == "" {
		if required && v.final {
			v.add(path, CodeMissing, "value is required at this stage", "", "")
		}
		return Decimal{}, false
	}
	d, err := Parse(spec, raw)
	if err != nil {
		v.decimalError(path, err, raw)
		return Decimal{}, false
	}
	return d, true
}

func (v *validation) money(path, raw string, required bool) (Decimal, bool) {
	return v.decimal(path, raw, MoneySpec, required)
}

func (v *validation) decimalError(path string, err error, raw string) {
	var ce *Error
	if errors.As(err, &ce) {
		v.add(path, ce.Code, ce.Message, "", raw)
		return
	}
	v.add(path, CodeMalformedDecimal, err.Error(), "", raw)
}

func (v *validation) arithError(path string, err error) {
	var ce *Error
	if errors.As(err, &ce) {
		v.add(path, ce.Code, ce.Message, "", "")
		return
	}
	v.add(path, CodeMismatch, err.Error(), "", "")
}

// rowCurrency rejects a row that declares a currency other than the contract
// currency; the caller must then refuse to include the row in any sum.
func (v *validation) rowCurrency(path, row string) bool {
	if v.currency == "" {
		return true
	}
	if row == "" || row == v.currency {
		return false
	}
	if v.final {
		v.add(path, CodeCurrencyMismatch,
			"row currency must match the contract currency; cross-currency sums are refused", v.currency, row)
	}
	return true
}

func (v *validation) reconcile(path string, s sectionSum, want Decimal, message string) {
	if !s.reconcilable() {
		return
	}
	have := s.value
	if !s.any {
		have = zeroMoney
	}
	if !sameValue(have, want) {
		v.add(path, CodeMismatch, message, want.String(), have.String())
	}
}

func (v *validation) validateItems(items []Item) sectionSum {
	var total sectionSum
	for i := range items {
		it := &items[i]
		path := fmt.Sprintf("items[%d]", i)
		if v.rowCurrency(path+".currency", it.Currency) {
			total.fail()
		}
		quantity, quantityOK := v.decimal(path+".quantity", it.Quantity, QuantitySpec, v.final)
		if quantityOK && v.final && quantity.IsZero() {
			v.add(path+".quantity", CodeNotPositive, "quantity must be greater than zero", "", it.Quantity)
		}
		price, priceOK := v.decimal(path+".unit_price", it.UnitPrice, UnitPriceSpec, v.final)
		line, lineOK := v.money(path+".line_amount", it.LineAmount, v.final)
		if it.TaxRate != "" {
			v.decimal(path+".tax_rate", it.TaxRate, TaxRateSpec, false)
		}
		if v.fixedTotal && quantityOK && priceOK && lineOK {
			expected, err := MulRound(quantity, price, MoneySpec)
			switch {
			case err != nil:
				v.arithError(path+".line_amount", err)
			case !sameValue(expected, line):
				v.add(path+".line_amount", CodeMismatch,
					"line amount must equal quantity × unit price rounded to cents (ROUND_HALF_UP); model any discount or tax explicitly instead",
					expected.String(), it.LineAmount)
			}
		}
		if lineOK {
			total.include(line, v, "items")
		} else {
			total.fail()
		}
	}
	if v.fixedTotal && v.amountOK && total.reconcilable() {
		v.reconcile("items", total, v.amount, "item line amounts must sum to the contract amount")
	}
	return total
}

func (v *validation) validateFunding(rows []Funding) sectionSum {
	var total sectionSum
	for i := range rows {
		f := &rows[i]
		path := fmt.Sprintf("funding[%d]", i)
		if v.rowCurrency(path+".currency", f.Currency) {
			total.fail()
		}
		if amount, ok := v.money(path+".amount", f.Amount, v.final); ok {
			total.include(amount, v, "funding")
		} else {
			total.fail()
		}
	}
	if v.fixedTotal && v.amountOK && total.reconcilable() {
		v.reconcile("funding", total, v.amount, "funding amounts must sum to the contract amount")
	}
	return total
}

func (v *validation) validatePayments(rows []Payment) (amounts, advances, units, fiscals sectionSum) {
	seen := make(map[int]bool, len(rows))
	ratios := make([]*Decimal, len(rows))
	amountDecs := make([]*Decimal, len(rows))
	ratioSeen := false
	base, baseOK := v.money("payments.ratio_base", v.in.RatioBase, false)

	for i := range rows {
		p := &rows[i]
		path := paymentPath(i)
		if v.rowCurrency(path+".currency", p.Currency) {
			amounts.fail()
			advances.fail()
			units.fail()
			fiscals.fail()
		}
		if v.final {
			switch {
			case p.Sequence < 1:
				v.add(path+".sequence", CodeNotPositive, "payment sequence must be at least 1", "", strconv.Itoa(p.Sequence))
			case seen[p.Sequence]:
				v.add(path+".sequence", CodeDuplicate, "payment sequence must be unique within the contract version", "", strconv.Itoa(p.Sequence))
			default:
				seen[p.Sequence] = true
			}
		}
		amount, amountOK := v.money(path+".amount", p.Amount, v.final)
		advance, advanceOK := v.money(path+".advance_amount", p.AdvanceAmount, v.final)
		unit, unitOK := v.money(path+".unit_amount", p.UnitAmount, v.final)
		fiscal, fiscalOK := v.money(path+".fiscal_amount", p.FiscalAmount, v.final)

		if amountOK {
			amounts.include(amount, v, "payments")
			amountDecs[i] = &amount
		} else {
			amounts.fail()
		}
		if advanceOK {
			if p.IsAdvance {
				advances.include(advance, v, "payments")
			}
		} else {
			advances.fail()
		}
		if unitOK {
			units.include(unit, v, "payments")
		} else {
			units.fail()
		}
		if fiscalOK {
			fiscals.include(fiscal, v, "payments")
		} else {
			fiscals.fail()
		}

		if v.final && amountOK && unitOK && fiscalOK {
			combined, err := unit.Add(fiscal)
			switch {
			case err != nil:
				v.arithError(path+".unit_amount", err)
			case !sameValue(combined, amount):
				v.add(path+".unit_amount", CodeMismatch,
					"unit amount plus fiscal amount must equal the period amount", amount.String(), combined.String())
			}
		}
		if v.final && amountOK && advanceOK {
			want, wantDec, message := zeroMoney, zeroMoney, "advance amount must be zero on a non-advance period"
			if p.IsAdvance {
				want, wantDec, message = amount, amount, "advance amount must equal the period amount on an advance period"
			}
			if !sameValue(advance, wantDec) {
				v.add(path+".advance_amount", CodeMismatch, message, want.String(), p.AdvanceAmount)
			}
		}
		if p.Ratio != "" {
			if r, ok := v.decimal(path+".ratio", p.Ratio, RatioSpec, false); ok {
				ratios[i] = &r
				ratioSeen = true
			}
		}
	}

	if v.fixedTotal && v.amountOK && amounts.reconcilable() {
		v.reconcile("payments", amounts, v.amount, "payment plan amounts must sum to the contract amount")
	}
	if v.final && ratioSeen && v.in.RatioBase == "" {
		v.add("payments.ratio_base", CodeRatioBaseMissing,
			"a ratio-based payment plan must declare the base its ratios apply to; the base is never assumed to be the contract amount", "", "")
	}
	if v.final && ratioSeen {
		allRatios := true
		var total big.Int
		for i := range rows {
			if ratios[i] == nil {
				allRatios = false
				if rows[i].Ratio == "" {
					v.add(paymentPath(i)+".ratio", CodeMissing, "ratio is required for every period in a ratio-based plan", "", "")
				}
				continue
			}
			total.Add(&total, ratios[i].unscaled)
		}
		if allRatios && total.Cmp(pow10(RatioSpec.Scale)) != 0 {
			v.add("payments", CodeMismatch, "payment ratios must sum to one", "1.000000", Decimal{spec: RatioSpec, unscaled: &total}.String())
			allRatios = false
		}
		if allRatios && baseOK && amounts.reconcilable() {
			v.checkRatios(rows, ratios, amountDecs, base)
		}
	}
	return amounts, advances, units, fiscals
}

// checkRatios enforces the declared-base policy: non-final periods equal
// ratio × base rounded ROUND_HALF_UP, and the last period — the one with the
// greatest sequence — absorbs the exact remainder.
func (v *validation) checkRatios(rows []Payment, ratios, amountDecs []*Decimal, base Decimal) {
	last := 0
	for i := range rows {
		if rows[i].Sequence > rows[last].Sequence {
			last = i
		}
	}
	for i := range rows {
		if i == last || ratios[i] == nil {
			continue
		}
		expected, err := MulRound(*ratios[i], base, MoneySpec)
		switch {
		case err != nil:
			v.arithError(paymentPath(i)+".amount", err)
		case !sameValue(expected, *amountDecs[i]):
			v.add(paymentPath(i)+".amount", CodeMismatch,
				"payment amount must equal ratio × declared ratio base rounded to cents (ROUND_HALF_UP)",
				expected.String(), rows[i].Amount)
		}
	}
	remainder := base
	attributable := true
	for i := range rows {
		if i == last {
			continue
		}
		next, err := remainder.Sub(*amountDecs[i])
		if err != nil {
			attributable = false
			break
		}
		remainder = next
	}
	switch {
	case !attributable:
		v.add(paymentPath(last)+".amount", CodeMismatch,
			"amounts before the last period already exceed the declared ratio base; no non-negative remainder is left to attribute",
			"", rows[last].Amount)
	case !sameValue(remainder, *amountDecs[last]):
		v.add(paymentPath(last)+".amount", CodeMismatch,
			"the last payment period must absorb the rounding remainder against the declared ratio base",
			remainder.String(), rows[last].Amount)
	}
}

// sectionSum accumulates one Money family exactly. incomplete marks a member
// that never parsed or was refused by currency; poisoned an overflow. Either
// blocks the section identity, because no exact total exists to compare.
type sectionSum struct {
	value      Decimal
	any        bool
	incomplete bool
	poisoned   bool
}

func (s *sectionSum) include(d Decimal, v *validation, path string) {
	if s.poisoned || s.incomplete {
		return
	}
	if !s.any {
		s.value, s.any = d, true
		return
	}
	total, err := s.value.Add(d)
	if err != nil {
		s.poisoned = true
		v.arithError(path, err)
		return
	}
	s.value = total
}

func (s *sectionSum) fail() { s.incomplete = true }

func (s *sectionSum) reconcilable() bool { return !s.incomplete && !s.poisoned }

func (s sectionSum) reportTotal() string {
	if !s.any || s.incomplete || s.poisoned {
		return ""
	}
	return s.value.String()
}

func sameValue(a, b Decimal) bool {
	equal, err := a.Cmp(b)
	return err == nil && equal == 0
}

func isISOCurrency(s string) bool {
	if len(s) != 3 {
		return false
	}
	for i := range s {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

func paymentPath(i int) string { return fmt.Sprintf("payments[%d]", i) }
