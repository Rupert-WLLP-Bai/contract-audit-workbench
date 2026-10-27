package documentparse

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"contractaudit/internal/docir"
)

type docxGold struct {
	Fixtures []struct {
		File            string   `json:"file"`
		NormalizedText  string   `json:"normalized_text"`
		FromOCR         bool     `json:"from_ocr"`
		OCRTestResponse string   `json:"ocr_test_response"`
		Absent          []string `json:"absent"`
		ErrorContains   string   `json:"error_contains"`
		Cells           []struct {
			TableID  string   `json:"table_id"`
			Row      int      `json:"row"`
			Col      int      `json:"col"`
			Text     string   `json:"text"`
			Contains []string `json:"contains"`
		} `json:"cells"`
	} `json:"fixtures"`
}

func docxFixture(t *testing.T, filename string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/docx/" + filename)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestDOCXRealFixturesAndCellAnchors(t *testing.T) {
	var gold docxGold
	if err := json.Unmarshal(docxFixture(t, "expected.json"), &gold); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range gold.Fixtures {
		t.Run(fixture.File, func(t *testing.T) {
			calls := 0
			result, err := ParseDOCX(context.Background(), docxFixture(t, fixture.File), func(ctx context.Context, media string, body []byte) (string, error) {
				calls++
				if media != "image/png" && media != "image/jpeg" {
					t.Fatalf("wrong image media: %s", media)
				}
				if len(body) < 100 {
					t.Fatal("image bytes missing")
				}
				return fixture.OCRTestResponse, nil
			})
			if fixture.ErrorContains != "" {
				if err == nil || !strings.Contains(err.Error(), fixture.ErrorContains) {
					t.Fatalf("want %q error, got %v", fixture.ErrorContains, err)
				}
				if result.IR.NormalizedText != "" {
					t.Fatal("failed document returned partial success text")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ir := result.IR
			if fixture.NormalizedText != "" && ir.NormalizedText != fixture.NormalizedText {
				t.Fatalf("text mismatch\nwant: %q\ngot:  %q", fixture.NormalizedText, ir.NormalizedText)
			}
			if result.FromOCR != fixture.FromOCR || (calls > 0) != fixture.FromOCR {
				t.Fatalf("OCR provenance wrong: from=%v calls=%d", result.FromOCR, calls)
			}
			if ir.ExtractionVersion != DOCXVersion || ir.CoordinateSpace != docir.CoordinateSpace || len(ir.Pages) != 0 {
				t.Fatalf("invalid DOCX provenance: %+v", ir)
			}
			for _, absent := range fixture.Absent {
				if strings.Contains(ir.NormalizedText, absent) {
					t.Fatalf("nonvisible content leaked: %s", absent)
				}
			}
			for _, block := range ir.Blocks {
				quote, ok := docir.Slice(ir.NormalizedText, block.Start, block.End)
				if !ok || quote != block.Text || block.Page != nil {
					t.Fatalf("invalid block anchor: %+v", block)
				}
			}
			for _, cell := range ir.Cells {
				quote, ok := docir.Slice(ir.NormalizedText, cell.Start, cell.End)
				anchor := docir.Bind(ir, cell.Text, cell.Start, cell.End)
				if !ok || quote != cell.Text || cell.Page != nil || anchor.Status != docir.StatusLocated || anchor.ElementID != cell.ID {
					t.Fatalf("invalid cell anchor: %+v anchor=%+v", cell, anchor)
				}
			}
			for _, expected := range fixture.Cells {
				found := false
				for _, cell := range ir.Cells {
					if cell.TableID != expected.TableID || cell.Row != expected.Row || cell.Col != expected.Col {
						continue
					}
					found = true
					if expected.Text != "" && cell.Text != expected.Text {
						t.Fatalf("wrong cell text: %+v", cell)
					}
					for _, quote := range expected.Contains {
						if !strings.Contains(cell.Text, quote) {
							t.Fatalf("cell missing %q: %+v", quote, cell)
						}
					}
				}
				if !found {
					t.Fatalf("missing gold cell: %+v", expected)
				}
			}
			if fixture.File == "complex-tables.docx" {
				anchor := docir.Locate(ir, "36个月😀")
				if anchor.ElementID != "t-2-r1c2" || anchor.Status != docir.StatusLocated {
					t.Fatalf("nested Unicode quote lost cell: %+v", anchor)
				}
				if ir.Blocks[len(ir.Blocks)-2].Kind != "header" || ir.Blocks[len(ir.Blocks)-1].Kind != "footer" {
					t.Fatal("header/footer policy missing")
				}
			}
			t.Logf("%s: %d blocks, %d located cells, OCR=%v, observed pages=%d", fixture.File, len(ir.Blocks), len(ir.Cells), result.FromOCR, len(ir.Pages))
		})
	}
}

func docxRewrite(t *testing.T, original []byte, replacements map[string][]byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, file := range zr.File {
		body, replace := replacements[file.Name]
		if !replace {
			r, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			body, err = io.ReadAll(r)
			r.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		w, err := zw.Create(file.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range replacements {
		found := false
		for _, file := range zr.File {
			if file.Name == name {
				found = true
			}
		}
		if found {
			continue
		}
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestDOCXSymbolFontPreservesVisibleCharacters(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	document := []byte(`<w:document xmlns:w="` + docxWNS + `"><w:body><w:p><w:r><w:t>合计</w:t><w:sym w:font="Symbol" w:char="00E5"/><w:sym w:font="Symbol" w:char="F0A3"/><w:t>金额</w:t></w:r></w:p></w:body></w:document>`)
	result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": document}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IR.NormalizedText != "合计∑≤金额" {
		t.Fatalf("symbol font did not preserve visible characters: %q", result.IR.NormalizedText)
	}
	anchor := docir.Locate(result.IR, "∑≤")
	if anchor.Status != docir.StatusLocated {
		t.Fatalf("symbol quote is not locatable: %+v", anchor)
	}
}

func TestDOCXRejectsIncompleteAndUnboundedPackages(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	document := func(content string) []byte {
		return []byte(`<w:document xmlns:w="` + docxWNS + `"><w:body>` + content + `</w:body></w:document>`)
	}
	cases := []struct {
		name         string
		replacements map[string][]byte
		want         string
	}{
		{"malformed XML", map[string][]byte{"word/document.xml": []byte(`<w:document>`)}, "invalid XML"},
		{"DTD", map[string][]byte{"word/document.xml": []byte(`<!DOCTYPE document [<!ENTITY x "boom">]><document/>`)}, "directives/entities"},
		{"excessive depth", map[string][]byte{"word/document.xml": document(strings.Repeat("<w:ins>", 130) + strings.Repeat("</w:ins>", 130))}, "depth"},
		{"oversized XML", map[string][]byte{"word/document.xml": bytes.Repeat([]byte(" "), docxMaxXML+1)}, "resource limit"},
		{"ZIP traversal", map[string][]byte{"../outside.xml": []byte("outside")}, "path"},
		{"opaque OLE", map[string][]byte{"word/embeddings/oleObject.bin": []byte("opaque")}, "opaque embedded"},
		{"alt content", map[string][]byte{"word/document.xml": document(`<mc:AlternateContent xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"/>`)}, "unsupported block"},
		{"symbol font", map[string][]byte{"word/document.xml": document(`<w:p><w:r><w:sym w:font="Wingdings" w:char="F021"/></w:r></w:p>`)}, "unsupported visible inline"},
		{"missing field end", map[string][]byte{"word/document.xml": document(`<w:p><w:r><w:fldChar w:fldCharType="begin"/></w:r></w:p>`)}, "unterminated fields"},
		{"bad vertical merge", map[string][]byte{"word/document.xml": document(`<w:tbl><w:tr><w:tc><w:tcPr><w:vMerge/></w:tcPr><w:p/></w:tc></w:tr></w:tbl>`)}, "no matching origin"},
		{"missing raster", map[string][]byte{"word/document.xml": document(`<w:p><w:r><w:drawing/></w:r></w:p>`)}, "exactly one"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, item.replacements), nil)
			if err == nil || !strings.Contains(err.Error(), item.want) {
				t.Fatalf("want %q got %v", item.want, err)
			}
			if result.IR.NormalizedText != "" {
				t.Fatal("returned partial parse")
			}
		})
	}
	if _, err := ParseDOCX(context.Background(), []byte("not a DOCX"), nil); err == nil {
		t.Fatal("accepted plain bytes")
	}
	if _, err := ParseDOCX(context.Background(), make([]byte, docxMaxArchive+1), nil); err == nil {
		t.Fatal("accepted oversized archive")
	}
}

func TestDOCXImageFailuresAndCancellationAreNotSuccess(t *testing.T) {
	image := docxFixture(t, "embedded-image.docx")
	failure := errors.New("model unavailable")
	for _, item := range []struct {
		name string
		ocr  OCR
		want string
	}{
		{"missing OCR", nil, "configured OCR"},
		{"failed OCR", func(context.Context, string, []byte) (string, error) { return "", failure }, "model unavailable"},
		{"empty OCR", func(context.Context, string, []byte) (string, error) { return " \n", nil }, "no text"},
		{"invalid UTF8", func(context.Context, string, []byte) (string, error) { return string([]byte{255}), nil }, "invalid UTF-8"},
	} {
		t.Run(item.name, func(t *testing.T) {
			result, err := ParseDOCX(context.Background(), image, item.ocr)
			if err == nil || !strings.Contains(err.Error(), item.want) || result.IR.NormalizedText != "" {
				t.Fatalf("incomplete image became success: %+v %v", result, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ParseDOCX(ctx, image, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled parse: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	_, err := ParseDOCX(ctx, image, func(context.Context, string, []byte) (string, error) { cancel(); return "not a complete parse", nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled OCR: %v", err)
	}
}

func TestDOCXPlainTextKeepsExistingNormalization(t *testing.T) {
	result, err := ParseDOCX(context.Background(), docxFixture(t, "plain.docx"), nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy := docir.Build("第1条 合同范围😀\n\n采购带宽100Mbps，质保期36个月。")
	if result.IR.NormalizedText != legacy.NormalizedText || result.IR.NormalizedDigest != legacy.NormalizedDigest || fmt.Sprint(result.IR.Clauses) != fmt.Sprint(legacy.Clauses) {
		t.Fatal("plain DOCX changed existing text/normalization/clause behavior")
	}
}
