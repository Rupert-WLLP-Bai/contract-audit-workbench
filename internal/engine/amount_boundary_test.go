package engine

import (
	"math"
	"testing"
)

func TestAmountFenBoundaries(t *testing.T) {
	for _, tc := range []struct {
		number string
		unit   string
		want   int64
		ok     bool
	}{
		{"92233720368547758.07", "元", math.MaxInt64, true},
		{"92233720368547758.08", "元", 0, false},
		{"9223372036854.775807", "万元", math.MaxInt64, true},
		{"9223372036854.775808", "万元", 0, false},
		{"9223372036854775808", "元", 0, false},
		{"-1", "元", 0, false},
		{"1.001", "元", 0, false},
		{"1.0100", "元", 101, true},
		{"0.000001", "万元", 1, true},
		{"1", "未知", 0, false},
		{"1.x", "元", 0, false},
	} {
		got, ok := amountFen(tc.number, tc.unit)
		if ok != tc.ok || got != tc.want {
			t.Errorf("amountFen(%q, %q) = (%d, %v), want (%d, %v)", tc.number, tc.unit, got, ok, tc.want, tc.ok)
		}
	}
}
