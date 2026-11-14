package model

import (
	"strings"
	"testing"
)

func TestValidResourceIDBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		valid       bool
	}{
		{"120 bytes", strings.Repeat("a", 120), true},
		{"121 bytes", strings.Repeat("a", 121), false},
		{"valid Unicode", "法规-版本一", true},
		{"invalid UTF-8", string([]byte{0xff}), false},
		{"slash", "a/b", false},
		{"backslash", `a\b`, false},
		{"control", "a\x1fb", false},
		{"space", "a b", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidResourceID(tc.value); got != tc.valid {
				t.Fatalf("ValidResourceID(%q)=%v, want %v", tc.value, got, tc.valid)
			}
		})
	}
}
