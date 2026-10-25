package documentparse

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"contractaudit/internal/docir"
)

func pdfFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "pdf", name))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func pdfTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"pdfinfo", "pdftotext", "pdfimages", "pdftoppm"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("real PDF executable %s required: %v", tool, err)
		}
	}
}

func pdfAnchor(t *testing.T, ir docir.IR, quote string, page int) docir.QuoteAnchor {
	t.Helper()
	anchor := docir.Locate(ir, quote)
	if anchor.Status != docir.StatusLocated || anchor.Page == nil || *anchor.Page != page {
		t.Fatalf("quote %q: got %#v; want observed page %d", quote, anchor, page)
	}
	actual, ok := docir.Slice(ir.NormalizedText, anchor.Start, anchor.End)
	if !ok || actual != quote || anchor.End-anchor.Start != utf8.RuneCountInString(quote) {
		t.Fatalf("quote %q has invalid codepoint slice [%d,%d): %q", quote, anchor.Start, anchor.End, actual)
	}
	t.Logf("observed quote=%q page=%d codepoints=[%d,%d)", quote, page, anchor.Start, anchor.End)
	return anchor
}

func TestPDFNativeExecutableExtraction(t *testing.T) {
	pdfTools(t)
	result, err := ParsePDF(context.Background(), pdfFixture(t, "native-chinese-table.pdf"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.FromOCR || len(result.IR.Pages) != 2 || result.IR.ExtractionVersion != PDFVersion {
		t.Fatalf("incorrect native PDF provenance: OCR=%v pages=%#v version=%s", result.FromOCR, result.IR.Pages, result.IR.ExtractionVersion)
	}
	pdfAnchor(t, result.IR, "第一条 带宽为100 Mbps，服务对象为合成园区。", 1)
	pdfAnchor(t, result.IR, "第二条 自然月服务可用率99.95%。", 2)
	pdfAnchor(t, result.IR, "旋转文字：故障响应4小时。", 2)
	for _, quote := range []string{"| 项目 | 合同承诺 |", "| 带宽 | 100 Mbps |", "| 质保 | 36个月 |"} {
		pdfAnchor(t, result.IR, quote, 1)
	}
	quote := "重复证据：服务可用率99.95%。"
	for _, page := range result.IR.Pages {
		text, ok := docir.Slice(result.IR.NormalizedText, page.Start, page.End)
		if !ok {
			t.Fatalf("invalid observed page %#v", page)
		}
		at := strings.Index(text, quote)
		if at < 0 {
			t.Fatalf("page %d lost repeated evidence: %q", page.Number, text)
		}
		start := page.Start + utf8.RuneCountInString(text[:at])
		anchor := docir.Bind(result.IR, quote, start, start+utf8.RuneCountInString(quote))
		if anchor.Page == nil || *anchor.Page != page.Number {
			t.Fatalf("repeated quote bound to another page: %#v", anchor)
		}
	}
	if len(result.IR.Cells) != 6 {
		t.Fatalf("native textual table lost its six actual cells: %#v", result.IR.Cells)
	}
	for _, cell := range result.IR.Cells {
		actual, ok := docir.Slice(result.IR.NormalizedText, cell.Start, cell.End)
		if !ok || actual != cell.Text || cell.Page == nil || *cell.Page != 1 {
			t.Fatalf("invalid native textual table cell: %#v", cell)
		}
	}
}

func TestPDFSinglePageHasObservedPageOne(t *testing.T) {
	result, err := ParsePDF(context.Background(), pdfFixture(t, "single-native.pdf"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.IR.Pages) != 1 || result.IR.Pages[0].Number != 1 {
		t.Fatalf("single-page PDF lost its real page: %#v", result.IR.Pages)
	}
	pdfAnchor(t, result.IR, "合成单页合同：质保36个月。", 1)
}

func TestPDFScannedAndMixedPagesUseRenderedPNG(t *testing.T) {
	pdfTools(t)
	for _, name := range []string{"scanned-chinese-table.pdf", "mixed-native-scan.pdf"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			result, err := ParsePDF(context.Background(), pdfFixture(t, name), func(ctx context.Context, media string, body []byte) (string, error) {
				calls++
				if ctx.Err() != nil || media != "image/png" {
					t.Fatalf("incorrect OCR boundary: %s, %v", media, ctx.Err())
				}
				image, err := png.Decode(bytes.NewReader(body))
				if err != nil || image.Bounds().Dx() < 1000 || image.Bounds().Dy() != pdfRenderSide {
					t.Fatalf("not a real full-page PNG render: %v", err)
				}
				dark := 0
				for y := 0; y < image.Bounds().Dy(); y += 8 {
					for x := 0; x < image.Bounds().Dx(); x += 8 {
						r, g, b, _ := image.At(x, y).RGBA()
						if r+g+b < 65535 {
							dark++
						}
					}
				}
				if dark < 200 {
					t.Fatalf("render lost visible text/table evidence: %d dark sampled pixels", dark)
				}
				t.Logf("real PDF render %dx%d; %d dark samples", image.Bounds().Dx(), image.Bounds().Dy(), dark)
				return "扫描条款：带宽80 Mbps。\n\n| 项目 | 承诺 |\n| 带宽 | 80 Mbps |\n| 质保 | 24个月 |", nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || !result.FromOCR {
				t.Fatalf("lost image page or OCR provenance: calls=%d fromOCR=%v", calls, result.FromOCR)
			}
			pdfAnchor(t, result.IR, "扫描条款：带宽80 Mbps。", 1)
			if len(result.IR.Cells) != 6 {
				t.Fatalf("OCR's textual table cells were lost: %#v", result.IR.Cells)
			}
			for _, cell := range result.IR.Cells {
				actual, ok := docir.Slice(result.IR.NormalizedText, cell.Start, cell.End)
				if !ok || actual != cell.Text || cell.Page == nil || *cell.Page != 1 {
					t.Fatalf("invalid transcribed table anchor: %#v", cell)
				}
			}
			if name == "mixed-native-scan.pdf" {
				pdfAnchor(t, result.IR, "合成混合页：原生承诺100 Mbps。", 1)
				pdfAnchor(t, result.IR, "原生备注：图片附件也属于本页证据。", 1)
				pdfAnchor(t, result.IR, "第二页原生条款：质保36个月。", 2)
			}
		})
	}
}

func TestPDFFailsWithoutPartialReadyProduct(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		want string
	}{
		{"wrong magic", []byte("not a PDF"), "header"},
		{"corrupt", pdfFixture(t, "corrupt.pdf"), "pdfinfo"},
		{"encrypted", pdfFixture(t, "encrypted.pdf"), "pdfinfo"},
		{"interactive form", pdfFixture(t, "interactive-form.pdf"), "flattened"},
		{"javascript", pdfFixture(t, "javascript.pdf"), "flattened"},
		{"too many pages", pdfFixture(t, "over-page-limit.pdf"), "page count"},
		{"oversized page", pdfFixture(t, "over-page-size.pdf"), "geometry"},
		{"oversized image", pdfFixture(t, "over-image-pixels.pdf"), "pixel limit"},
		{"image requires callback", pdfFixture(t, "mixed-native-scan.pdf"), "OCR callback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			var ocr OCR
			if tc.name != "image requires callback" {
				ocr = func(context.Context, string, []byte) (string, error) { called = true; return "unreachable", nil }
			}
			result, err := ParsePDF(context.Background(), tc.body, ocr)
			if err == nil || !strings.Contains(err.Error(), tc.want) || result.IR.NormalizedText != "" || result.FromOCR || called {
				t.Fatalf("want explicit %q failure before OCR, got result=%#v err=%v called=%v", tc.want, result, err, called)
			}
		})
	}
	tooLarge := make([]byte, pdfMaxBytes+1)
	if _, err := ParsePDF(context.Background(), tooLarge, nil); err == nil || !strings.Contains(err.Error(), "input limit") {
		t.Fatalf("oversized input was accepted: %v", err)
	}
}

func TestPDFOCRFailureIsNeverOmitted(t *testing.T) {
	failure := errors.New("synthetic OCR unavailable")
	for _, name := range []string{"scanned-chinese-table.pdf", "mixed-native-scan.pdf", "blank.pdf"} {
		for _, mode := range []string{"failed", "empty", "invented page break", "excessive"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				result, err := ParsePDF(context.Background(), pdfFixture(t, name), func(context.Context, string, []byte) (string, error) {
					switch mode {
					case "failed":
						return "", failure
					case "invented page break":
						return "one\ftwo", nil
					case "excessive":
						return strings.Repeat("x", pdfMaxPageText+1), nil
					default:
						return " \n\t", nil
					}
				})
				if err == nil || result.IR.NormalizedText != "" || result.FromOCR {
					t.Fatalf("incomplete OCR page became successful: result=%#v err=%v", result, err)
				}
				if mode == "failed" && !errors.Is(err, failure) {
					t.Fatalf("lost OCR failure cause: %v", err)
				}
			})
		}
	}
}

func TestPDFCancellationAndWorkspaceCleanup(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ParsePDF(ctx, pdfFixture(t, "single-native.pdf"), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled parse: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := ParsePDF(ctx, pdfFixture(t, "mixed-native-scan.pdf"), func(ctx context.Context, _ string, _ []byte) (string, error) {
			close(entered)
			<-ctx.Done()
			return "", ctx.Err()
		})
		done <- err
	}()
	select {
	case <-entered:
		cancel()
	case err := <-done:
		t.Fatalf("parse ended before cancellable OCR: %v", err)
	case <-ctx.Done():
		t.Fatal("parse never reached OCR")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("OCR cancellation lost: %v", err)
	}
	_, _ = ParsePDF(context.Background(), pdfFixture(t, "corrupt.pdf"), nil)
	entries, err := os.ReadDir(temp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary original survived cancellation/failure: %v %v", entries, err)
	}
}

func TestPDFProcessBoundsAndMissingDependency(t *testing.T) {
	t.Run("missing executable", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := ParsePDF(context.Background(), pdfFixture(t, "single-native.pdf"), nil)
		if err == nil || !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("missing dependency was not explicit: %v", err)
		}
	})
	t.Run("bounded process output", func(t *testing.T) {
		_, err := pdfCommand(context.Background(), 16, "pdftotext", pdfFixturePath("single-native.pdf"), "-")
		if err == nil || !strings.Contains(err.Error(), "resource limit") {
			t.Fatalf("unbounded executable output: %v", err)
		}
	})
	t.Run("running process cancellation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := pdfCommand(ctx, 16, "sleep", "10")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("process exceeded context deadline: %v", err)
		}
	})
}

func pdfFixturePath(name string) string { return filepath.Join("testdata", "pdf", name) }
