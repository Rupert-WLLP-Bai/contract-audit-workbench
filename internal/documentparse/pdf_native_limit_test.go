package documentparse

import (
	"context"
	"strings"
	"testing"
)

func TestPDFPageTextsPinsNativePageTextLimitByPage(t *testing.T) {
	overLimit := strings.Repeat("x", pdfMaxPageText+1)
	tests := []struct {
		name     string
		pages    int
		segments []string
		wantErr  string
	}{
		{"first page", 1, []string{overLimit, ""}, "PDF page 1 exceeds text limit"},
		{"second page", 2, []string{"first page", overLimit, ""}, "PDF page 2 exceeds text limit"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ocrCalled := false
			ocr := func(context.Context, string, []byte) (string, error) {
				ocrCalled = true
				return "unexpected transcript", nil
			}
			texts, fromOCR, err := pdfPageTexts(context.Background(), "", tc.pages, tc.segments, make([]bool, tc.pages), ocr)
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("pdfPageTexts error = %v, want %q", err, tc.wantErr)
			}
			if texts != nil || fromOCR || ocrCalled {
				t.Fatalf("oversized native text returned texts=%v fromOCR=%v ocrCalled=%v", texts, fromOCR, ocrCalled)
			}
		})
	}
}

func TestPDFPageTextsAcceptsNativeTextAtExactPageLimit(t *testing.T) {
	expected := strings.Repeat("x", pdfMaxPageText)
	ocrCalled := false
	texts, fromOCR, err := pdfPageTexts(context.Background(), "", 1, []string{expected, ""}, []bool{false}, func(context.Context, string, []byte) (string, error) {
		ocrCalled = true
		return "unexpected transcript", nil
	})
	if err != nil {
		t.Fatalf("pdfPageTexts rejected text at the exact limit: %v", err)
	}
	if len(texts) != 1 || texts[0] != expected || fromOCR || ocrCalled {
		t.Fatalf("exact-limit native output: pages=%d matches=%v fromOCR=%v ocrCalled=%v", len(texts), len(texts) == 1 && texts[0] == expected, fromOCR, ocrCalled)
	}
}
