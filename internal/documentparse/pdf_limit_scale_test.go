package documentparse

import (
	"fmt"
	"strings"
	"testing"
)

// TestPDFPageCountAdmitsFrozenCorpusScale pins the page ceiling to the largest
// public original frozen for issue #15: a 92-page scanned contract, still
// rejected one page past the configured limit.
func TestPDFPageCountAdmitsFrozenCorpusScale(t *testing.T) {
	if pdfMaxPages != 128 {
		t.Fatalf("page limit = %d; want 128", pdfMaxPages)
	}
	pages, err := pdfPageCount(pageCountInfo("92", "no", "none", "no"))
	if err != nil || pages != 92 {
		t.Fatalf("92-page scan must parse: pages=%d err=%v", pages, err)
	}
	if _, err := pdfPageCount(pageCountInfo(fmt.Sprintf("%d", pdfMaxPages+1), "no", "none", "no")); err == nil {
		t.Fatalf("%d pages must stay rejected", pdfMaxPages+1)
	}
}

// TestPDFImageInventoryAdmitsFrozenCorpusScale pins the aggregate image budget
// to the observed 633,032,192 pixels of that scan while still rejecting one
// pixel past the configured budget.
func TestPDFImageInventoryAdmitsFrozenCorpusScale(t *testing.T) {
	const corpusPages = 92
	const corpusAggregatePixels = 633_032_192
	if pdfMaxAllPixels != 700_000_000 {
		t.Fatalf("aggregate pixel limit = %d; want 700000000", pdfMaxAllPixels)
	}
	perPage := corpusAggregatePixels / corpusPages
	var inventory strings.Builder
	inventory.WriteString(imageInventoryHeader)
	for page := 1; page <= corpusPages; page++ {
		pixels := perPage
		if page == corpusPages {
			pixels += corpusAggregatePixels % corpusPages
		}
		fmt.Fprintf(&inventory, "%d 0 image 1 %d\n", page, pixels)
	}
	if _, err := pdfImagePages(inventory.String(), corpusPages); err != nil {
		t.Fatalf("observed corpus scale must fit the budget: %v", err)
	}

	over := pdfImageInventoryAtLimit() + "1 0 image 1 1\n"
	if _, err := pdfImagePages(over, 1); err == nil || !strings.Contains(err.Error(), "total pixel limit") {
		t.Fatalf("one pixel past the budget must stay rejected: %v", err)
	}
}
