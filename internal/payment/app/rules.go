// File metadata (manifest mirror):
// Purpose: Preserves exact legacy payment amount, currency, visible-text, cents formatting and cumulative reversal validation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"fmt"
	"math/big"
	"strings"
)

func BoundedVisibleText(value string, max int) bool {
	if value == "" || len(value) > max || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func canonicalPaymentAmount(v string) (string, bool) {
	if v == "" {
		return "", false
	}
	parts := strings.Split(v, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts[0]) > 16 || (len(parts[0]) > 1 && parts[0][0] == '0') {
		return "", false
	}
	for _, r := range parts[0] {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) == 0 || len(fraction) > 2 {
			return "", false
		}
		for _, r := range fraction {
			if r < '0' || r > '9' {
				return "", false
			}
		}
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	return parts[0] + "." + fraction, true
}

func currencyValid(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, r := range value {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
func paymentCents(v string) (int64, bool) {
	var whole, frac string
	parts := strings.Split(v, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, false
	}
	whole = parts[0]
	if len(parts) == 2 {
		frac = parts[1]
	}
	if len(frac) > 2 || len(whole) > 16 {
		return 0, false
	}
	for _, r := range whole + frac {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	for len(frac) < 2 {
		frac += "0"
	}
	if frac == "" {
		frac = "00"
	}
	var w int64
	for _, r := range whole {
		w = w*10 + int64(r-'0')
	}
	f := int64(frac[0]-'0')*10 + int64(frac[1]-'0')
	return w*100 + f, true
}
func formatPaymentCents(v *big.Int) string {
	negative := v.Sign() < 0
	value := new(big.Int).Abs(new(big.Int).Set(v))
	whole, remainder := new(big.Int).QuoRem(value, big.NewInt(100), new(big.Int))
	formatted := fmt.Sprintf("%s.%02d", whole.String(), remainder.Int64())
	if negative {
		return "-" + formatted
	}
	return formatted
}
func exceedsOriginal(original, reversed, amount string) bool {
	o, ok1 := paymentCents(original)
	r, ok2 := paymentCents(reversed)
	a, ok3 := paymentCents(amount)
	return !ok1 || !ok2 || !ok3 || r+a > o
}
