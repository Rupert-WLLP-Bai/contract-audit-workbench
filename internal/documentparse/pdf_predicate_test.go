package documentparse

// Boundary tests for the pure render-geometry predicate extracted from
// pdfRenderPage. Real Poppler renders are always inside the configured square,
// so the invalid boundaries cannot be produced through ParsePDF; this table
// pins the behavior that the old inline condition had at those boundaries.

import (
	"errors"
	"testing"
)

func TestPDFRenderValidPinned(t *testing.T) {
	decodeErr := errors.New("decode failed")
	tests := []struct {
		name   string
		err    error
		width  int
		height int
		want   bool
	}{
		{"valid render", nil, 1000, pdfRenderSide, true},
		{"minimum width", nil, 1, pdfRenderSide, true},
		{"minimum height", nil, pdfRenderSide, 1, true},
		{"zero width", nil, 0, pdfRenderSide, false},
		{"zero height", nil, pdfRenderSide, 0, false},
		{"negative width", nil, -1, pdfRenderSide, false},
		{"width above side", nil, pdfRenderSide + 1, pdfRenderSide, false},
		{"height above side", nil, pdfRenderSide, pdfRenderSide + 1, false},
		{"decode error with plausible size", decodeErr, pdfRenderSide, pdfRenderSide, false},
		{"decode error with zero size", decodeErr, 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pdfRenderValid(tc.err, tc.width, tc.height); got != tc.want {
				t.Fatalf("pdfRenderValid(%v, %d, %d) = %v, want %v", tc.err, tc.width, tc.height, got, tc.want)
			}
		})
	}
}
