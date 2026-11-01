package contractmoney

import (
	"errors"
	"strings"
	"testing"
)

func TestParseCanonicalForms(t *testing.T) {
	cases := []struct {
		spec Spec
		in   string
		want string
	}{
		{MoneySpec, "0.00", "0.00"},
		{MoneySpec, "0.01", "0.01"},
		{MoneySpec, "100000.00", "100000.00"},
		{MoneySpec, "9999999999999999.99", "9999999999999999.99"},
		{UnitPriceSpec, "0.0050", "0.0050"},
		{UnitPriceSpec, "99999999999999.9999", "99999999999999.9999"},
		{QuantitySpec, "10.000000", "10.000000"},
		{QuantitySpec, "999999999999.999999", "999999999999.999999"},
		{RatioSpec, "0.000000", "0.000000"},
		{RatioSpec, "0.200000", "0.200000"},
		{RatioSpec, "0.999999", "0.999999"},
		{RatioSpec, "1.000000", "1.000000"},
		{TaxRateSpec, "0.0000", "0.0000"},
		{TaxRateSpec, "0.1300", "0.1300"},
		{TaxRateSpec, "1.0000", "1.0000"},
	}
	for _, c := range cases {
		d, err := Parse(c.spec, c.in)
		if err != nil {
			t.Fatalf("Parse(%v, %q) failed: %v", c.spec, c.in, err)
		}
		if got := d.String(); got != c.want {
			t.Fatalf("Parse(%v, %q).String() = %q, want %q", c.spec, c.in, got, c.want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		spec    Spec
		in      string
		code    string
		message string
	}{
		{MoneySpec, "", CodeMalformedDecimal, "empty value"},
		{MoneySpec, "100000.0", CodeMalformedDecimal, "expected exactly 2 fraction digits, got 1"},
		{MoneySpec, "100", CodeMalformedDecimal, "expected exactly 2 fraction digits, got 0"},
		{MoneySpec, "100.000", CodeMalformedDecimal, "expected exactly 2 fraction digits, got 3"},
		{MoneySpec, "1e5", CodeMalformedDecimal, "exponent notation is not allowed"},
		{MoneySpec, "1E5", CodeMalformedDecimal, "exponent notation is not allowed"},
		{MoneySpec, "-1.00", CodeMalformedDecimal, "sign is not allowed"},
		{MoneySpec, "+1.00", CodeMalformedDecimal, "sign is not allowed"},
		{MoneySpec, "0100.00", CodeMalformedDecimal, "leading zero in integer part"},
		{MoneySpec, "00.00", CodeMalformedDecimal, "leading zero in integer part"},
		{MoneySpec, ".50", CodeMalformedDecimal, "missing integer part"},
		{MoneySpec, "100.", CodeMalformedDecimal, "expected exactly 2 fraction digits, got 0"},
		{MoneySpec, "1.2.3", CodeMalformedDecimal, "more than one decimal point"},
		{MoneySpec, " 1.00", CodeMalformedDecimal, "whitespace is not allowed"},
		{MoneySpec, "1.00 ", CodeMalformedDecimal, "whitespace is not allowed"},
		{MoneySpec, "1,00", CodeMalformedDecimal, "invalid character"},
		{MoneySpec, "\u0661\u0662\u0663.\u0660\u0660", CodeMalformedDecimal, "invalid character"},
		{MoneySpec, "99999999999999999.99", CodeOverflow, "integer part exceeds 16 digits"},
		{QuantitySpec, "9999999999999.999999", CodeOverflow, "integer part exceeds 12 digits"},
		{RatioSpec, "0.5", CodeMalformedDecimal, "expected exactly 6 fraction digits, got 1"},
		{RatioSpec, "1.000001", CodeMalformedDecimal, "value must not exceed 1"},
		{RatioSpec, "2.000000", CodeMalformedDecimal, "value must not exceed 1"},
		{TaxRateSpec, "0.13000", CodeMalformedDecimal, "expected exactly 4 fraction digits, got 5"},
		{TaxRateSpec, "1.0001", CodeMalformedDecimal, "value must not exceed 1"},
	}
	for _, c := range cases {
		_, err := Parse(c.spec, c.in)
		if err == nil {
			t.Fatalf("Parse(%v, %q) unexpectedly succeeded", c.spec, c.in)
		}
		var ce *Error
		if !errors.As(err, &ce) {
			t.Fatalf("Parse(%v, %q) error %v is not *Error", c.spec, c.in, err)
		}
		if ce.Code != c.code {
			t.Fatalf("Parse(%v, %q) code = %q, want %q", c.spec, c.in, ce.Code, c.code)
		}
		if !strings.Contains(ce.Message, c.message) {
			t.Fatalf("Parse(%v, %q) message = %q, want it to contain %q", c.spec, c.in, ce.Message, c.message)
		}
	}
}

func isOverflow(err error) bool {
	var ce *Error
	return errors.As(err, &ce) && ce.Code == CodeOverflow
}

func TestDecimalArithmetic(t *testing.T) {
	hundred, err := Parse(MoneySpec, "100.00")
	if err != nil {
		t.Fatal(err)
	}
	twoHundred, err := Parse(MoneySpec, "200.00")
	if err != nil {
		t.Fatal(err)
	}

	sum, err := hundred.Add(twoHundred)
	if err != nil || sum.String() != "300.00" {
		t.Fatalf("Add = %q, %v; want 300.00, nil", sum.String(), err)
	}
	if order, err := hundred.Cmp(twoHundred); err != nil || order != -1 {
		t.Fatalf("Cmp = %d, %v; want -1, nil", order, err)
	}
	if order, err := hundred.Cmp(hundred); err != nil || order != 0 {
		t.Fatalf("Cmp = %d, %v; want 0, nil", order, err)
	}
	diff, err := twoHundred.Sub(hundred)
	if err != nil || diff.String() != "100.00" {
		t.Fatalf("Sub = %q, %v; want 100.00, nil", diff.String(), err)
	}
	if _, err := hundred.Sub(twoHundred); !errors.Is(err, ErrNegative) {
		t.Fatalf("Sub below zero = %v, want ErrNegative", err)
	}

	maxMoney, err := Parse(MoneySpec, "9999999999999999.99")
	if err != nil {
		t.Fatal(err)
	}
	oneCent, err := Parse(MoneySpec, "0.01")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := maxMoney.Add(oneCent); !isOverflow(err) {
		t.Fatalf("Add overflow = %v, want precision_overflow", err)
	}

	price, err := Parse(UnitPriceSpec, "1.0000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hundred.Add(price); !errors.Is(err, ErrSpecMismatch) {
		t.Fatalf("Add across specs = %v, want ErrSpecMismatch", err)
	}
	if _, err := (Decimal{spec: MoneySpec}).Add(hundred); !errors.Is(err, ErrUninitialized) {
		t.Fatalf("Add on zero Decimal = %v, want ErrUninitialized", err)
	}
}

func TestInvalidSpecs(t *testing.T) {
	if _, err := Parse(Spec{Precision: 18, Scale: 19}, "0.00"); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("Parse with scale above precision = %v, want ErrInvalidSpec", err)
	}
	if _, err := Parse(Spec{}, "1.00"); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("Parse with zero precision = %v, want ErrInvalidSpec", err)
	}
}

func TestMulRoundHalfUp(t *testing.T) {
	cases := []struct {
		name  string
		a     string
		aSpec Spec
		b     string
		bSpec Spec
		want  string
	}{
		{"half up on the third decimal", "1.000000", QuantitySpec, "0.1250", UnitPriceSpec, "0.13"},
		{"exact tie rounds up", "1.000000", QuantitySpec, "0.0050", UnitPriceSpec, "0.01"},
		{"long product rounds up", "3.333333", QuantitySpec, "30.0050", UnitPriceSpec, "100.02"},
		{"exact product stays exact", "10.000000", QuantitySpec, "10000.0000", UnitPriceSpec, "100000.00"},
		{"ratio against declared base", "0.333333", RatioSpec, "100.00", MoneySpec, "33.33"},
		{"plan ratio", "0.200000", RatioSpec, "100000.00", MoneySpec, "20000.00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, err := Parse(c.aSpec, c.a)
			if err != nil {
				t.Fatal(err)
			}
			b, err := Parse(c.bSpec, c.b)
			if err != nil {
				t.Fatal(err)
			}
			got, err := MulRound(a, b, MoneySpec)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != c.want {
				t.Fatalf("MulRound(%q, %q) = %q, want %q", c.a, c.b, got.String(), c.want)
			}
		})
	}

	quantityMax, err := Parse(QuantitySpec, "999999999999.999999")
	if err != nil {
		t.Fatal(err)
	}
	priceMax, err := Parse(UnitPriceSpec, "99999999999999.9999")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MulRound(quantityMax, priceMax, MoneySpec); !isOverflow(err) {
		t.Fatalf("MulRound overflow = %v, want precision_overflow", err)
	}
}
