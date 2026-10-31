package documentparse

// Characterization tests for the PDF adapter. The metadata, page-size and
// image-inventory parsers are unexported and have no exported wrapper, so
// their cases run in-package (as the existing pdf_test.go already does for
// pdfImagePages). ParsePDF itself is exercised through its public entry point.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// pageRangeError names the accepted page range from the configured limit.
func pageRangeError() string {
	return fmt.Sprintf("PDF page count must be between 1 and %d", pdfMaxPages)
}

func pinnedError(t *testing.T, got error, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("want error %q, got nil", want)
	}
	if got.Error() != want {
		t.Fatalf("error = %q, want %q", got.Error(), want)
	}
}

func pageCountInfo(pages, encrypted, form, javascript string) string {
	return "Pages: " + pages + "\nEncrypted: " + encrypted + "\nForm: " + form + "\nJavaScript: " + javascript + "\n"
}

var validPageCountInfo = pageCountInfo("2", "no", "none", "no")

func TestPDFPageCountPinned(t *testing.T) {
	tests := []struct {
		name  string
		info  string
		pages int
		want  string
	}{
		{"valid two pages", validPageCountInfo, 2, ""},
		{"one page", pageCountInfo("1", "no", "none", "no"), 1, ""},
		{"maximum pages", pageCountInfo(strconv.Itoa(pdfMaxPages), "no", "none", "no"), pdfMaxPages, ""},
		{"zero pages", pageCountInfo("0", "no", "none", "no"), 0, pageRangeError()},
		{"too many pages", pageCountInfo(strconv.Itoa(pdfMaxPages+1), "no", "none", "no"), 0, pageRangeError()},
		{"negative pages", pageCountInfo("-1", "no", "none", "no"), 0, pageRangeError()},
		{"non numeric pages", pageCountInfo("two", "no", "none", "no"), 0, pageRangeError()},
		{"missing pages", "Encrypted: no\nForm: none\nJavaScript: no\n", 0, pageRangeError()},
		{"encrypted yes", pageCountInfo("2", "yes", "none", "no"), 0, "encrypted PDFs are unsupported"},
		{"encrypted missing", "Pages: 2\nForm: none\nJavaScript: no\n", 0, "encrypted PDFs are unsupported"},
		{"form present", pageCountInfo("2", "no", "AcroForm", "no"), 0, "PDF forms or JavaScript require a flattened document"},
		{"form missing", "Pages: 2\nEncrypted: no\nJavaScript: no\n", 0, "PDF forms or JavaScript require a flattened document"},
		{"javascript present", pageCountInfo("2", "no", "none", "yes"), 0, "PDF forms or JavaScript require a flattened document"},
		{"javascript missing", "Pages: 2\nEncrypted: no\nForm: none\n", 0, "PDF forms or JavaScript require a flattened document"},
		{"form present javascript clean", pageCountInfo("2", "no", "AcroForm", "no"), 0, "PDF forms or JavaScript require a flattened document"},
		{"javascript present form clean", pageCountInfo("2", "no", "none", "yes"), 0, "PDF forms or JavaScript require a flattened document"},
		{"ambiguous pages", "Pages: 1\nPages: 2\nEncrypted: no\nForm: none\nJavaScript: no\n", 0, "PDF metadata has ambiguous required fields"},
		{"ambiguous encrypted", "Pages: 2\nEncrypted: no\nEncrypted: no\nForm: none\nJavaScript: no\n", 0, "PDF metadata has ambiguous required fields"},
		{"duplicate unknown key is ignored", "Pages: 2\nFoo: 1\nFoo: 2\nEncrypted: no\nForm: none\nJavaScript: no\n", 2, ""},
		{"unknown and malformed lines are ignored", "Producer: x\nPages: 2\nnot a field\nEncrypted: no\nForm: none\nJavaScript: no\n", 2, ""},
		{"lowercase pages key is ignored", "pages: 2\nEncrypted: no\nForm: none\nJavaScript: no\n", 0, pageRangeError()},
		{"lowercase encryption key is ignored", "Pages: 2\nencrypted: no\nForm: none\nJavaScript: no\n", 0, "encrypted PDFs are unsupported"},
		{"values are trimmed", "Pages:   2  \nEncrypted:\tno \nForm: none\nJavaScript: no\n", 2, ""},
		{"carriage returns", "Pages: 2\r\nEncrypted: no\r\nForm: none\r\nJavaScript: no\r\n", 2, ""},
		{"ambiguous before encryption", "Pages: 1\nPages: 2\nEncrypted: yes\nForm: none\nJavaScript: no\n", 0, "PDF metadata has ambiguous required fields"},
		{"encryption before form", pageCountInfo("2", "yes", "AcroForm", "yes"), 0, "encrypted PDFs are unsupported"},
		{"form before page range", pageCountInfo("0", "no", "AcroForm", "no"), 0, "PDF forms or JavaScript require a flattened document"},
		{"encryption before page range", pageCountInfo("0", "yes", "none", "no"), 0, "encrypted PDFs are unsupported"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pages, err := pdfPageCount(tc.info)
			if tc.want == "" {
				if err != nil || pages != tc.pages {
					t.Fatalf("pdfPageCount = %d, %v; want %d, nil", pages, err, tc.pages)
				}
				return
			}
			pinnedError(t, err, tc.want)
			if pages != 0 {
				t.Fatalf("failed page count returned %d", pages)
			}
		})
	}
}

func pageSizeLine(page string, width, height string) string {
	return "Page    " + page + " size: " + width + " x " + height + " pts\n"
}

func TestPDFCheckPageSizesPinned(t *testing.T) {
	tests := []struct {
		name  string
		info  string
		pages int
		want  string
	}{
		{"single page", pageSizeLine("1", "612.00", "792.00"), 1, ""},
		{"minimum dimensions", pageSizeLine("1", "1", "1"), 1, ""},
		{"two pages out of order", pageSizeLine("2", "612.00", "792.00") + pageSizeLine("1", "595.00", "842.00"), 2, ""},
		{"trailing whitespace", "Page 1 size: 612.00 x 792.00 pts \n", 1, ""},
		{"maximum width and height", pageSizeLine("1", "14400", "14400"), 1, ""},
		{"duplicate page", pageSizeLine("1", "612.00", "792.00") + pageSizeLine("1", "612.00", "792.00"), 1, "PDF page geometry is invalid or exceeds 14400-point limit"},
		{"page zero", pageSizeLine("0", "612.00", "792.00"), 1, "PDF page geometry is invalid or exceeds 14400-point limit"},
		{"page beyond count", pageSizeLine("3", "612.00", "792.00"), 2, "PDF page geometry is invalid or exceeds 14400-point limit"},
		{"missing page size", pageSizeLine("1", "612.00", "792.00"), 2, "PDF tool did not report every page size"},
		{"empty info", "", 1, "PDF tool did not report every page size"},
		{"zero width", pageSizeLine("1", "0", "792.00"), 1, "PDF page geometry is invalid or exceeds 14400-point limit"},
		{"zero height", pageSizeLine("1", "612.00", "0"), 1, "PDF page geometry is invalid or exceeds 14400-point limit"},
		{"negative width", pageSizeLine("1", "-1", "792.00"), 1, "PDF page geometry is invalid or exceeds 14400-point limit"},
		{"negative height", pageSizeLine("1", "612.00", "-1"), 1, "PDF page geometry is invalid or exceeds 14400-point limit"},
		{"width above limit", pageSizeLine("1", "14400.1", "792.00"), 1, "PDF page geometry is invalid or exceeds 14400-point limit"},
		{"height above limit", pageSizeLine("1", "612.00", "14400.1"), 1, "PDF page geometry is invalid or exceeds 14400-point limit"},
		{"width overflow", pageSizeLine("1", "1e999", "792.00"), 1, "PDF page geometry is invalid or exceeds 14400-point limit"},
		{"height overflow", pageSizeLine("1", "612.00", "1e999"), 1, "PDF page geometry is invalid or exceeds 14400-point limit"},
		{"unmatched lines are ignored", "Page x size: 1 x 2 pts\nPages: 1\n" + pageSizeLine("1", "612.00", "792.00"), 1, ""},
		{"negative page is not a match", pageSizeLine("-1", "612.00", "792.00"), 1, "PDF tool did not report every page size"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := pdfCheckPageSizes(tc.info, tc.pages)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			pinnedError(t, err, tc.want)
		})
	}
}

const imageInventoryHeader = "page num type width height\n---\n"

func TestPDFImagePagesPinned(t *testing.T) {
	rows := func(lines ...string) string {
		return imageInventoryHeader + strings.Join(lines, "\n") + "\n"
	}
	valid := rows(
		"1 0 image 100 200",
		"2 0 image 300 400",
	)
	pages, err := pdfImagePages(valid, 2)
	if err != nil || len(pages) != 2 || !pages[0] || !pages[1] {
		t.Fatalf("valid inventory = %v, %v", pages, err)
	}

	if pdfMaxAllPixels%pdfMaxImagePixels != 0 {
		t.Fatalf("total pixel budget %d is not a whole number of images at %d pixels", pdfMaxAllPixels, pdfMaxImagePixels)
	}
	// aggregateRows is the largest number of maximum-size images inside the
	// total budget; every row stays on page 1 so page validation is unchanged.
	aggregateRows := pdfMaxAllPixels / pdfMaxImagePixels

	tests := []struct {
		name  string
		info  string
		pages int
		want  string
	}{
		{"header only", imageInventoryHeader, 2, ""},
		{"empty info", "", 1, "PDF image inventory is invalid"},
		{"missing separator", "page num type width height\n", 1, "PDF image inventory is invalid"},
		{"wrong first line", "Page num type width height\n---\n", 1, "PDF image inventory is invalid"},
		{"wrong separator", "page num type width height\n=== \n", 1, "PDF image inventory is invalid"},
		{"incomplete row", rows("1 0 image 100"), 1, "PDF image inventory is incomplete"},
		{"blank row", rows("1 0 image 1 1", "", "1 0 image 1 1"), 1, "PDF image inventory is incomplete"},
		{"non numeric page", rows("x 0 image 1 1"), 1, "PDF embedded image exceeds pixel limit or has invalid dimensions"},
		{"page zero", rows("0 0 image 1 1"), 1, "PDF embedded image exceeds pixel limit or has invalid dimensions"},
		{"page beyond count", rows("2 0 image 1 1"), 1, "PDF embedded image exceeds pixel limit or has invalid dimensions"},
		{"width zero", rows("1 0 image 0 1"), 1, "PDF embedded image exceeds pixel limit or has invalid dimensions"},
		{"height zero", rows("1 0 image 1 0"), 1, "PDF embedded image exceeds pixel limit or has invalid dimensions"},
		{"width negative", rows("1 0 image -1 1"), 1, "PDF embedded image exceeds pixel limit or has invalid dimensions"},
		{"width above pixel limit", rows("1 0 image 40000001 1"), 1, "PDF embedded image exceeds pixel limit or has invalid dimensions"},
		{"height above pixel limit", rows("1 0 image 1 40000001"), 1, "PDF embedded image exceeds pixel limit or has invalid dimensions"},
		{"product above pixel limit", rows("1 0 image 10000 4001"), 1, "PDF embedded image exceeds pixel limit or has invalid dimensions"},
		{"width at pixel limit", rows("1 0 image 40000000 1"), 1, ""},
		{"height at pixel limit", rows("1 0 image 1 40000000"), 1, ""},
		{"aggregate at limit", imageInventoryHeader + strings.Repeat("1 0 image 10000 4000\n", aggregateRows), 1, ""},
		{"aggregate above limit", imageInventoryHeader + strings.Repeat("1 0 image 10000 4000\n", aggregateRows) + "1 0 image 1 1\n", 1, "PDF embedded images exceed total pixel limit"},
		{"image count above limit", imageInventoryHeader + strings.Repeat("1 0 image 1 1\n", pdfMaxImages+1), 1, "PDF image count exceeds resource limit"},
		{"image count at limit", imageInventoryHeader + strings.Repeat("1 0 image 1 1\n", pdfMaxImages), 1, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pdfImagePages(tc.info, tc.pages)
			if tc.want == "" {
				if err != nil || len(got) != tc.pages {
					t.Fatalf("pdfImagePages = %v, %v", got, err)
				}
				return
			}
			pinnedError(t, err, tc.want)
			if got != nil {
				t.Fatalf("failed inventory returned %v", got)
			}
		})
	}

	t.Run("repeated image on one page", func(t *testing.T) {
		got, err := pdfImagePages(rows("1 0 image 1 1", "1 0 image 2 2"), 1)
		if err != nil || !got[0] {
			t.Fatalf("pdfImagePages = %v, %v", got, err)
		}
	})
}

func TestPDFReadableTextPinned(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"plain text", "合同条款", true},
		{"allowed whitespace", "a\nb\r\nc\td", true},
		{"empty", "", false},
		{"whitespace only", " \n\t ", false},
		{"invalid utf8", string([]byte{0xff, 0xfe}), false},
		{"replacement rune", "a\uFFFDb", false},
		{"nul control", "a\x00b", false},
		{"vertical tab control", "a\vb", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pdfReadableText(tc.text); got != tc.want {
				t.Fatalf("pdfReadableText(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestPDFBoundedOutputWritePinned(t *testing.T) {
	exact := &pdfBoundedOutput{limit: 4}
	n, err := exact.Write([]byte("abcd"))
	if n != 4 || err != nil || exact.full {
		t.Fatalf("exact-fit write = %d, %v, full=%v", n, err, exact.full)
	}

	over := &pdfBoundedOutput{limit: 4}
	if n, err := over.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("first write = %d, %v", n, err)
	}
	n, err = over.Write([]byte("de"))
	pinnedError(t, err, "PDF tool output exceeds resource limit")
	if n != 0 || !over.full {
		t.Fatalf("over-limit write = %d, full=%v", n, over.full)
	}
	if over.buffer.Len() != 3 {
		t.Fatalf("over-limit write changed the buffer: %d", over.buffer.Len())
	}
}

func TestPDFCommandBoundedOutputPinned(t *testing.T) {
	_, err := pdfCommand(context.Background(), 16, "pdftotext", pdfFixturePath("single-native.pdf"), "-")
	pinnedError(t, err, "PDF tool pdftotext output exceeds resource limit")
}

func TestParsePDFPreflightPinned(t *testing.T) {
	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err := ParsePDF(ctx, []byte("%PDF-1.4\n%%EOF"), nil)
		if !errors.Is(err, context.Canceled) || result.IR.NormalizedText != "" || result.FromOCR {
			t.Fatalf("canceled parse = %#v, %v", result, err)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		_, err := ParsePDF(context.Background(), nil, nil)
		pinnedError(t, err, "PDF header or end marker is missing")
	})

	t.Run("missing magic", func(t *testing.T) {
		_, err := ParsePDF(context.Background(), []byte("not a PDF %%EOF"), nil)
		pinnedError(t, err, "PDF header or end marker is missing")
	})

	t.Run("missing end marker", func(t *testing.T) {
		_, err := ParsePDF(context.Background(), []byte("%PDF-1.4\nbody"), nil)
		pinnedError(t, err, "PDF header or end marker is missing")
	})

	t.Run("end marker outside the final window", func(t *testing.T) {
		body := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("a"), 1100)...)
		body = append(body, []byte("%%EOF")...)
		body = append(body, bytes.Repeat([]byte("b"), 1030)...)
		_, err := ParsePDF(context.Background(), body, nil)
		pinnedError(t, err, "PDF header or end marker is missing")
	})

	t.Run("oversized body", func(t *testing.T) {
		body := make([]byte, pdfMaxBytes+1)
		_, err := ParsePDF(context.Background(), body, nil)
		if err == nil || err.Error() != fmt.Sprintf("PDF exceeds %d-byte input limit", pdfMaxBytes) {
			t.Fatalf("oversized body = %v", err)
		}
	})

	t.Run("exact size limit is not the input-limit error", func(t *testing.T) {
		body := make([]byte, pdfMaxBytes)
		copy(body, "%PDF-")
		copy(body[len(body)-5:], "%%EOF")
		_, err := ParsePDF(context.Background(), body, nil)
		if err == nil {
			t.Fatal("garbage at the exact size limit unexpectedly parsed")
		}
		if strings.Contains(err.Error(), "input limit") {
			t.Fatalf("exact size limit rejected as oversized: %v", err)
		}
	})

	t.Run("workspace creation failure", func(t *testing.T) {
		t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing-directory"))
		_, err := ParsePDF(context.Background(), []byte("%PDF-1.4\n%%EOF"), nil)
		if err == nil || !strings.Contains(err.Error(), "creating PDF workspace") {
			t.Fatalf("workspace failure = %v", err)
		}
	})

	t.Run("valid preflight reaches the tool chain", func(t *testing.T) {
		// poppler rejects this garbage body; the point is that the header and
		// workspace path ran before the first tool invocation.
		_, err := ParsePDF(context.Background(), []byte("%PDF-1.4\n%%EOF"), nil)
		if err == nil || !strings.Contains(err.Error(), "pdfinfo") {
			t.Fatalf("garbage body = %v", err)
		}
	})

	t.Run("missing OCR callback names the page", func(t *testing.T) {
		_, err := ParsePDF(context.Background(), pdfFixture(t, "mixed-native-scan.pdf"), nil)
		pinnedError(t, err, "PDF page 1 requires an OCR callback")
	})

	t.Run("transcript at the exact page limit is accepted", func(t *testing.T) {
		result, err := ParsePDF(context.Background(), pdfFixture(t, "scanned-chinese-table.pdf"), func(context.Context, string, []byte) (string, error) {
			return strings.Repeat("x", pdfMaxPageText), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.FromOCR || len(result.IR.NormalizedText) != pdfMaxPageText {
			t.Fatalf("exact-limit transcript: ocr=%v text=%d", result.FromOCR, len(result.IR.NormalizedText))
		}
	})
}
