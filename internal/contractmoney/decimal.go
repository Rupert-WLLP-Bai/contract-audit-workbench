// Package contractmoney implements the exact decimal money semantics of the
// contract domain: fixed-precision decimal strings, ROUND_HALF_UP arithmetic,
// and the settlement identities that submit and freeze must reconcile. The
// precision table, the non-negative wire grammar, and the fixed-total
// policies follow the CLM field dictionary (contracts/field-dictionary.json,
// rules FD-V02 to FD-V04).
//
// The package is pure domain code: no storage, transport, tenant, or clock
// dependency, and no mutation of its inputs. Amounts, unit prices,
// quantities, ratios, and tax rates only travel as canonical decimal
// strings, so JSON floats, exponent notation, signs, and out-of-precision
// values are rejected at the boundary instead of being coerced. Rounding
// happens only in MulRound, with the frozen ROUND_HALF_UP policy; tax
// amounts and discounts are deliberately out of scope and never derived, and
// sums across different currencies are refused before they can exist.
package contractmoney

import (
	"errors"
	"math/big"
	"strconv"
	"strings"
)

// Spec fixes the exact wire precision of one decimal domain value. Precision
// counts every significant digit and Scale the fraction digits, so a (18,2)
// Money value carries at most 16 integer digits. MaxOne confines the value
// to [0,1] and is used by ratio and tax rate.
type Spec struct {
	Precision int
	Scale     int
	MaxOne    bool
}

// The frozen precisions of the field dictionary: money with two fraction
// digits, unit price four, quantity six, ratio six within [0,1], tax rate
// four within [0,1].
var (
	MoneySpec     = Spec{Precision: 18, Scale: 2}
	UnitPriceSpec = Spec{Precision: 18, Scale: 4}
	QuantitySpec  = Spec{Precision: 18, Scale: 6}
	RatioSpec     = Spec{Precision: 7, Scale: 6, MaxOne: true}
	TaxRateSpec   = Spec{Precision: 5, Scale: 4, MaxOne: true}
)

// Error codes of the low-level failures. Validate reuses them in Violations
// so callers can branch on one set of codes.
const (
	CodeMalformedDecimal = "malformed_decimal"
	CodeOverflow         = "precision_overflow"
)

var (
	// ErrInvalidSpec reports a Spec whose Scale is outside [0, Precision].
	ErrInvalidSpec = errors.New("contractmoney: invalid spec")
	// ErrSpecMismatch reports operands that carry different Specs.
	ErrSpecMismatch = errors.New("contractmoney: spec mismatch")
	// ErrUninitialized reports use of the zero Decimal instead of a parsed value.
	ErrUninitialized = errors.New("contractmoney: uninitialized decimal")
	// ErrNegative reports a subtraction that would leave the non-negative domain.
	ErrNegative = errors.New("contractmoney: negative result")
)

// Error is the precise failure behind Parse and the arithmetic operations.
// Use errors.As and compare Code to tell malformed input from precision
// overflow.
type Error struct {
	Code    string
	Spec    Spec
	Input   string
	Message string
}

func (e *Error) Error() string {
	return "contractmoney: " + e.Message + ": " + strconv.Quote(e.Input)
}

// Decimal is an exact non-negative value, unscaled × 10^-Scale. Parse is the
// only constructor; the zero Decimal is unusable and every method rejects
// it. Values are immutable and every operation returns a new Decimal.
type Decimal struct {
	spec     Spec
	unscaled *big.Int
}

// Parse converts one canonical decimal string of the given spec. It refuses
// signs, exponent notation, whitespace, leading zeros, wrong fraction-digit
// counts, integer parts beyond Precision-Scale digits, and — for MaxOne
// specs — values above 1. The wire form is canonical: "100.5" is not a Money
// value, "100.00" is, and float artifacts such as "100000.000000000001" can
// never re-enter the domain.
func Parse(spec Spec, s string) (Decimal, error) {
	if spec.Precision <= 0 || spec.Scale < 0 || spec.Scale > spec.Precision {
		return Decimal{}, ErrInvalidSpec
	}
	intPart, fracPart, reason := scanDecimal(s)
	if reason != "" {
		return Decimal{}, &Error{Code: CodeMalformedDecimal, Spec: spec, Input: s, Message: reason}
	}
	if intPart == "" {
		return Decimal{}, &Error{Code: CodeMalformedDecimal, Spec: spec, Input: s, Message: "missing integer part"}
	}
	if len(intPart) > 1 && intPart[0] == '0' {
		return Decimal{}, &Error{Code: CodeMalformedDecimal, Spec: spec, Input: s, Message: "leading zero in integer part"}
	}
	if len(intPart) > spec.Precision-spec.Scale {
		return Decimal{}, &Error{Code: CodeOverflow, Spec: spec, Input: s,
			Message: "integer part exceeds " + strconv.Itoa(spec.Precision-spec.Scale) + " digits"}
	}
	if len(fracPart) != spec.Scale {
		return Decimal{}, &Error{Code: CodeMalformedDecimal, Spec: spec, Input: s,
			Message: "expected exactly " + strconv.Itoa(spec.Scale) + " fraction digits, got " + strconv.Itoa(len(fracPart))}
	}
	if spec.MaxOne && exceedsOne(intPart, fracPart) {
		return Decimal{}, &Error{Code: CodeMalformedDecimal, Spec: spec, Input: s, Message: "value must not exceed 1"}
	}
	unscaled, ok := new(big.Int).SetString(intPart+fracPart, 10)
	if !ok {
		return Decimal{}, &Error{Code: CodeMalformedDecimal, Spec: spec, Input: s, Message: "not a decimal number"}
	}
	return Decimal{spec: spec, unscaled: unscaled}, nil
}

// scanDecimal splits s at its single decimal point and names the first
// reason the string cannot be a canonical non-negative decimal.
func scanDecimal(s string) (intPart, fracPart, reason string) {
	if s == "" {
		return "", "", "empty value"
	}
	dot := -1
	for i := range s {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
		case c == '.':
			if dot >= 0 {
				return "", "", "more than one decimal point"
			}
			dot = i
		case c == '+' || c == '-':
			return "", "", "sign is not allowed"
		case c == 'e' || c == 'E':
			return "", "", "exponent notation is not allowed"
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			return "", "", "whitespace is not allowed"
		default:
			return "", "", "invalid character " + strconv.QuoteRune(rune(c))
		}
	}
	if dot < 0 {
		return s, "", ""
	}
	return s[:dot], s[dot+1:], ""
}

func exceedsOne(intPart, fracPart string) bool {
	switch {
	case intPart == "0":
		return false
	case intPart == "1":
		return strings.IndexFunc(fracPart, func(r rune) bool { return r != '0' }) >= 0
	default:
		return true
	}
}

// String renders the canonical wire form with exactly spec.Scale fraction
// digits and no sign or exponent.
func (d Decimal) String() string {
	if d.unscaled == nil {
		return ""
	}
	digits := d.unscaled.String()
	if d.spec.Scale == 0 {
		return digits
	}
	if len(digits) <= d.spec.Scale {
		digits = strings.Repeat("0", d.spec.Scale+1-len(digits)) + digits
	}
	return digits[:len(digits)-d.spec.Scale] + "." + digits[len(digits)-d.spec.Scale:]
}

// IsZero reports whether d is the exact zero of its spec.
func (d Decimal) IsZero() bool {
	return d.unscaled != nil && d.unscaled.Sign() == 0
}

func (d Decimal) initialized() bool { return d.unscaled != nil }

// Cmp compares two decimals of the same spec.
func (d Decimal) Cmp(o Decimal) (int, error) {
	if d.spec != o.spec {
		return 0, ErrSpecMismatch
	}
	if !d.initialized() || !o.initialized() {
		return 0, ErrUninitialized
	}
	return d.unscaled.Cmp(o.unscaled), nil
}

// Add returns the exact sum of two decimals of the same spec and refuses a
// sum that no longer fits the spec precision. Currency guarding belongs to
// the settlement validation, the only place different currencies meet.
func (d Decimal) Add(o Decimal) (Decimal, error) {
	if d.spec != o.spec {
		return Decimal{}, ErrSpecMismatch
	}
	if !d.initialized() || !o.initialized() {
		return Decimal{}, ErrUninitialized
	}
	sum := new(big.Int).Add(d.unscaled, o.unscaled)
	if sum.Cmp(maxUnscaled(d.spec)) > 0 {
		return Decimal{}, &Error{Code: CodeOverflow, Spec: d.spec, Input: d.String() + "+" + o.String(),
			Message: "sum exceeds " + strconv.Itoa(d.spec.Precision) + " digits of precision"}
	}
	return Decimal{spec: d.spec, unscaled: sum}, nil
}

// Sub returns the exact difference d-o and refuses to go below zero; the
// domain never holds negative amounts.
func (d Decimal) Sub(o Decimal) (Decimal, error) {
	if d.spec != o.spec {
		return Decimal{}, ErrSpecMismatch
	}
	if !d.initialized() || !o.initialized() {
		return Decimal{}, ErrUninitialized
	}
	if d.unscaled.Cmp(o.unscaled) < 0 {
		return Decimal{}, ErrNegative
	}
	return Decimal{spec: d.spec, unscaled: new(big.Int).Sub(d.unscaled, o.unscaled)}, nil
}

// MulRound multiplies two exact decimals and rounds the exact product into
// the output spec with ROUND_HALF_UP. This is the only rounding site: it
// carries quantity × unit_price into Money and ratio × declared base into
// Money. The rounded result must fit the output precision.
func MulRound(a, b Decimal, out Spec) (Decimal, error) {
	if out.Precision <= 0 || out.Scale < 0 || out.Scale > out.Precision {
		return Decimal{}, ErrInvalidSpec
	}
	if !a.initialized() || !b.initialized() {
		return Decimal{}, ErrUninitialized
	}
	product := new(big.Int).Mul(a.unscaled, b.unscaled)
	switch shift := out.Scale - a.spec.Scale - b.spec.Scale; {
	case shift > 0:
		product = new(big.Int).Mul(product, pow10(shift))
	case shift < 0:
		den := pow10(-shift)
		// ROUND_HALF_UP over non-negative exact values: (2n+d) ÷ 2d.
		num := new(big.Int).Add(new(big.Int).Lsh(product, 1), den)
		product = num.Quo(num, new(big.Int).Lsh(den, 1))
	}
	if product.Cmp(maxUnscaled(out)) > 0 {
		return Decimal{}, &Error{Code: CodeOverflow, Spec: out, Input: a.String() + "*" + b.String(),
			Message: "rounded product exceeds " + strconv.Itoa(out.Precision) + " digits of precision"}
	}
	return Decimal{spec: out, unscaled: product}, nil
}

func maxUnscaled(spec Spec) *big.Int {
	limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(spec.Precision)), nil)
	return limit.Sub(limit, big.NewInt(1))
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}
