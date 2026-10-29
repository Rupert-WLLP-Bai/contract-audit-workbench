package documentparse

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
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

func docxPart(t *testing.T, archive []byte, partName string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range zr.File {
		if file.Name != partName {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	t.Fatalf("missing DOCX fixture part %q", partName)
	return nil
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

func TestDOCXEnforcesZIPEntryAndExpansionLimits(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	tooMany := make(map[string][]byte, docxMaxEntries-2)
	for index := 0; index < docxMaxEntries-2; index++ {
		tooMany[fmt.Sprintf("extra/%04d.xml", index)] = nil
	}
	largeTotal := bytes.Repeat([]byte{'x'}, 13<<20)
	tooExpanded := map[string][]byte{
		"extra/one.xml":   largeTotal,
		"extra/two.xml":   largeTotal,
		"extra/three.xml": largeTotal,
		"extra/four.xml":  largeTotal,
		"extra/five.xml":  largeTotal,
	}
	for _, item := range []struct {
		name         string
		replacements map[string][]byte
		want         string
	}{
		{"ZIP entry count", tooMany, "ZIP entry limit"},
		{"expanded ZIP bytes", tooExpanded, "expanded ZIP exceeds 64 MiB"},
		{"single part bytes", map[string][]byte{"extra/large.bin": bytes.Repeat([]byte{'x'}, docxMaxPart+1)}, "part exceeds 16 MiB"},
	} {
		t.Run(item.name, func(t *testing.T) {
			_, err := ParseDOCX(context.Background(), docxRewrite(t, plain, item.replacements), nil)
			if err == nil || !strings.Contains(err.Error(), item.want) {
				t.Fatalf("want %q error, got %v", item.want, err)
			}
		})
	}
}

func TestDOCXRejectsUnsafeRelationshipTargets(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	cases := []struct {
		name string
		xml  string
		want string
	}{
		{"external main part", `<Relationships xmlns="` + docxPackageRNS + `"><Relationship Id="main" Type="` + docxRNS + `/officeDocument" Target="https://example.invalid/document.xml"/></Relationships>`, "invalid or external relationship target"},
		{"package traversal", `<Relationships xmlns="` + docxPackageRNS + `"><Relationship Id="main" Type="` + docxRNS + `/officeDocument" Target="../../outside.xml"/></Relationships>`, "relationship escapes package"},
		{"query target", `<Relationships xmlns="` + docxPackageRNS + `"><Relationship Id="main" Type="` + docxRNS + `/officeDocument" Target="word/document.xml?download=1"/></Relationships>`, "invalid or external relationship target"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"_rels/.rels": []byte(item.xml)}), nil)
			if err == nil || !strings.Contains(err.Error(), item.want) {
				t.Fatalf("want %q error, got %v", item.want, err)
			}
			if result.IR.NormalizedText != "" {
				t.Fatalf("unsafe relationship returned partial text: %q", result.IR.NormalizedText)
			}
		})
	}
}

func TestDOCXRejectsExternalImageRelationships(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	relationships := []byte(`<Relationships xmlns="` + docxPackageRNS + `"><Relationship Id="image" Type="` + docxRNS + `/image" Target="https://example.invalid/image.png" TargetMode="External"/></Relationships>`)
	result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/_rels/document.xml.rels": relationships}), nil)
	if err == nil || !strings.Contains(err.Error(), "external resources are unsupported") {
		t.Fatalf("external image relationship was accepted: %v", err)
	}
	if result.IR.NormalizedText != "" {
		t.Fatalf("external relationship returned partial text: %q", result.IR.NormalizedText)
	}
}

func TestDOCXRejectsOpaqueAndMissingRelationships(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	for _, item := range []struct {
		name string
		xml  string
		want string
	}{
		{"opaque OLE relation", `<Relationships xmlns="` + docxPackageRNS + `"><Relationship Id="ole" Type="` + docxRNS + `/oleObject" Target="missing.bin"/></Relationships>`, "opaque relationship oleObject is unsupported"},
		{"missing related part", `<Relationships xmlns="` + docxPackageRNS + `"><Relationship Id="image" Type="` + docxRNS + `/image" Target="missing.png"/></Relationships>`, `relationship points to missing part "word/missing.png"`},
		{"duplicate identity", `<Relationships xmlns="` + docxPackageRNS + `"><Relationship Id="same" Type="` + docxRNS + `/image" Target="document.xml"/><Relationship Id="same" Type="` + docxRNS + `/image" Target="document.xml"/></Relationships>`, "duplicate relationship identity"},
	} {
		t.Run(item.name, func(t *testing.T) {
			result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/_rels/document.xml.rels": []byte(item.xml)}), nil)
			if err == nil || !strings.Contains(err.Error(), item.want) || result.IR.NormalizedText != "" {
				t.Fatalf("want fail-closed %q, got text=%q err=%v", item.want, result.IR.NormalizedText, err)
			}
		})
	}
}

func TestDOCXRejectsConditionalTableVisibility(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	contentTypes := []byte(`<Types xmlns="` + docxTypesNS + `"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`)
	relationships := []byte(`<Relationships xmlns="` + docxPackageRNS + `"><Relationship Id="styles" Type="` + docxRNS + `/styles" Target="styles.xml"/></Relationships>`)
	styles := []byte(`<w:styles xmlns:w="` + docxWNS + `"><w:style w:type="table" w:styleId="Hidden"><w:rPr><w:vanish/></w:rPr></w:style></w:styles>`)
	document := []byte(`<w:document xmlns:w="` + docxWNS + `"><w:body><w:tbl><w:tblPr><w:tblStyle w:val="Hidden"/></w:tblPr><w:tr><w:tc><w:p><w:r><w:t>must not be hidden</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:body></w:document>`)
	result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{
		"[Content_Types].xml":          contentTypes,
		"word/document.xml":            document,
		"word/_rels/document.xml.rels": relationships,
		"word/styles.xml":              styles,
	}), nil)
	if err == nil || !strings.Contains(err.Error(), "conditional table-style visibility is unsupported") {
		t.Fatalf("conditionally hidden table was accepted: %v", err)
	}
	if result.IR.NormalizedText != "" {
		t.Fatalf("hidden table returned partial text: %q", result.IR.NormalizedText)
	}
}

func TestDOCXRecursesSupportedTableWrappersInOrder(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	document := []byte(`<w:document xmlns:w="` + docxWNS + `"><w:body><w:tbl><w:tblGrid><w:gridCol/><w:gridCol/></w:tblGrid><w:ins><w:tr><w:customXml><w:tc><w:p><w:r><w:t>first</w:t></w:r></w:p></w:tc></w:customXml><w:tc><w:p><w:r><w:t>second</w:t></w:r></w:p></w:tc></w:tr></w:ins><w:moveTo><w:tr><w:sdt><w:sdtContent><w:tc><w:p><w:r><w:t>third</w:t></w:r></w:p></w:tc></w:sdtContent></w:sdt><w:tc><w:p><w:r><w:t>fourth</w:t></w:r></w:p></w:tc></w:tr></w:moveTo></w:tbl></w:body></w:document>`)
	result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": document}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.IR.Cells) != 4 {
		t.Fatalf("wrapped table cells missing: %+v", result.IR.Cells)
	}
	want := []string{"first", "second", "third", "fourth"}
	for index, text := range want {
		if result.IR.Cells[index].Text != text || result.IR.Cells[index].Row != index/2+1 || result.IR.Cells[index].Col != index%2+1 {
			t.Fatalf("wrapped cell order/coordinates changed at %d: %+v", index, result.IR.Cells[index])
		}
	}
}

func TestDOCXInlineBreaksAndSpecialHyphensStayInOrder(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	document := []byte(`<w:document xmlns:w="` + docxWNS + `"><w:body><w:p><w:r><w:t>A</w:t><w:tab/><w:ptab/><w:br/><w:cr/><w:lastRenderedPageBreak/><w:noBreakHyphen/><w:softHyphen/><w:t>B</w:t></w:r></w:p></w:body></w:document>`)
	result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": document}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IR.NormalizedText != "A\t\t\n\u2011\u00adB" {
		t.Fatalf("inline character order changed: %q", result.IR.NormalizedText)
	}
}

func TestDOCXStyleDefaultsKeepStyledVisibleText(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	contentTypes := []byte(`<Types xmlns="` + docxTypesNS + `"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`)
	relationships := []byte(`<Relationships xmlns="` + docxPackageRNS + `"><Relationship Id="styles" Type="` + docxRNS + `/styles" Target="styles.xml"/></Relationships>`)
	styles := []byte(`<w:styles xmlns:w="` + docxWNS + `"><w:docDefaults><w:rPrDefault><w:rPr><w:vanish w:val="0"/></w:rPr></w:rPrDefault></w:docDefaults><w:style w:type="paragraph" w:styleId="pDefault" w:default="1"/><w:style w:type="character" w:styleId="rDefault" w:default="true"/></w:styles>`)
	document := []byte(`<w:document xmlns:w="` + docxWNS + `"><w:body><w:p><w:pPr><w:pStyle w:val="pDefault"/></w:pPr><w:r><w:rPr><w:rStyle w:val="rDefault"/></w:rPr><w:t>styled</w:t></w:r></w:p></w:body></w:document>`)
	result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{
		"[Content_Types].xml":          contentTypes,
		"word/document.xml":            document,
		"word/_rels/document.xml.rels": relationships,
		"word/styles.xml":              styles,
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IR.NormalizedText != "styled" {
		t.Fatalf("default styles changed text visibility: %q", result.IR.NormalizedText)
	}
}

func TestDOCXFieldControlsExposeOnlyCachedResult(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	document := func(content string) []byte {
		return []byte(`<w:document xmlns:w="` + docxWNS + `"><w:body><w:p>` + content + `</w:p></w:body></w:document>`)
	}
	valid := `<w:r><w:fldChar w:fldCharType="begin"/><w:instrText>secret</w:instrText><w:fldChar w:fldCharType="separate"/><w:t>cached</w:t><w:fldChar w:fldCharType="end"/></w:r>`
	result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": document(valid)}), nil)
	if err != nil || result.IR.NormalizedText != "cached" {
		t.Fatalf("field did not retain only cached result: text=%q err=%v", result.IR.NormalizedText, err)
	}
	for _, item := range []struct{ name, body, want string }{
		{"separator without begin", `<w:r><w:fldChar w:fldCharType="separate"/></w:r>`, "invalid field separator"},
		{"end without begin", `<w:r><w:fldChar w:fldCharType="end"/></w:r>`, "invalid field terminator"},
		{"unsupported field kind", `<w:r><w:fldChar w:fldCharType="unknown"/></w:r>`, "unsupported field character"},
		{"excessive nesting", `<w:r>` + strings.Repeat(`<w:fldChar w:fldCharType="begin"/>`, 33) + `</w:r>`, "field nesting limit"},
	} {
		t.Run(item.name, func(t *testing.T) {
			got, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": document(item.body)}), nil)
			if err == nil || !strings.Contains(err.Error(), item.want) || got.IR.NormalizedText != "" {
				t.Fatalf("want fail-closed %q, got text=%q err=%v", item.want, got.IR.NormalizedText, err)
			}
		})
	}
}

func TestDOCXBlockWrappersAndIgnoredBlocksPreserveVisibleOrder(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	paragraph := func(value string) string { return `<w:p><w:r><w:t>` + value + `</w:t></w:r></w:p>` }
	content := `<w:del>` + paragraph("deleted") + `</w:del><w:moveFrom>` + paragraph("moved-from") + `</w:moveFrom>` +
		`<w:ins>` + paragraph("inserted") + `</w:ins><w:moveTo>` + paragraph("moved-to") + `</w:moveTo>` +
		`<w:customXml>` + paragraph("custom") + `</w:customXml><w:sdt><w:sdtContent>` + paragraph("controlled") + `</w:sdtContent></w:sdt><w:sdt/><w:sectPr/>`
	document := []byte(`<w:document xmlns:w="` + docxWNS + `"><w:body>` + content + `</w:body></w:document>`)
	result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": document}), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"inserted", "moved-to", "custom", "controlled"} {
		if !strings.Contains(result.IR.NormalizedText, want) {
			t.Errorf("missing visible wrapper content %q in %q", want, result.IR.NormalizedText)
		}
	}
	for _, absent := range []string{"deleted", "moved-from"} {
		if strings.Contains(result.IR.NormalizedText, absent) {
			t.Errorf("included deleted block %q in %q", absent, result.IR.NormalizedText)
		}
	}
	if strings.Index(result.IR.NormalizedText, "inserted") > strings.Index(result.IR.NormalizedText, "moved-to") || strings.Index(result.IR.NormalizedText, "moved-to") > strings.Index(result.IR.NormalizedText, "custom") {
		t.Fatalf("block order changed: %q", result.IR.NormalizedText)
	}
	bad := []byte(`<w:document xmlns:w="` + docxWNS + `"><w:body><w:unknown/></w:body></w:document>`)
	if _, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": bad}), nil); err == nil || !strings.Contains(err.Error(), "unsupported block unknown") {
		t.Fatalf("unsupported block accepted: %v", err)
	}
}

func TestDOCXInlineHyperlinksAndContentControls(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	document := []byte(`<w:document xmlns:w="` + docxWNS + `" xmlns:r="` + docxRNS + `"><w:body><w:p><w:hyperlink r:id="web"><w:r><w:t>外链文本</w:t></w:r></w:hyperlink><w:r><w:sdt><w:sdtContent><w:t>控件文本</w:t></w:sdtContent></w:sdt></w:r></w:p></w:body></w:document>`)
	relationships := []byte(`<Relationships xmlns="` + docxPackageRNS + `"><Relationship Id="web" Type="` + docxRNS + `/hyperlink" Target="https://example.invalid/" TargetMode="External"/></Relationships>`)
	result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{
		"word/document.xml":            document,
		"word/_rels/document.xml.rels": relationships,
	}), nil)
	if err != nil || result.IR.NormalizedText != "外链文本控件文本" {
		t.Fatalf("external link text/control content changed: text=%q err=%v", result.IR.NormalizedText, err)
	}
	badLink := strings.Replace(string(document), `r:id="web"`, `r:id="missing"`, 1)
	_, err = ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": []byte(badLink)}), nil)
	if err == nil || !strings.Contains(err.Error(), "invalid hyperlink relationship") {
		t.Fatalf("missing hyperlink relationship was accepted: %v", err)
	}
}

func TestDOCXRejectsCroppedImages(t *testing.T) {
	original := docxFixture(t, "embedded-image.docx")
	document := string(docxPart(t, original, "word/document.xml"))
	document = strings.Replace(document, `<a:blip r:embed="image1"/>`, `<a:srcRect l="1"/><a:blip r:embed="image1"/>`, 1)
	result, err := ParseDOCX(context.Background(), docxRewrite(t, original, map[string][]byte{"word/document.xml": []byte(document)}), func(context.Context, string, []byte) (string, error) { return "should not run", nil })
	if err == nil || !strings.Contains(err.Error(), "cropped images require rendered OCR") || result.IR.NormalizedText != "" {
		t.Fatalf("cropped image was accepted: text=%q err=%v", result.IR.NormalizedText, err)
	}
}

func TestDOCXRejectsInvalidMismatchedAndOversizedImages(t *testing.T) {
	original := docxFixture(t, "embedded-image.docx")
	contentTypes := strings.Replace(string(docxPart(t, original, "[Content_Types].xml")), `ContentType="image/png"`, `ContentType="image/jpeg"`, 1)
	for _, item := range []struct {
		name         string
		replacements map[string][]byte
		want         string
	}{
		{"invalid bytes", map[string][]byte{"word/media/bandwidth.png": []byte("invalid image")}, "valid PNG or JPEG"},
		{"mismatched media type", map[string][]byte{"[Content_Types].xml": []byte(contentTypes)}, "content type does not match bytes"},
		{"over 16 megapixels", map[string][]byte{"word/media/bandwidth.png": docxPNGHeader(4001, 4000)}, "16 megapixel limit"},
	} {
		t.Run(item.name, func(t *testing.T) {
			result, err := ParseDOCX(context.Background(), docxRewrite(t, original, item.replacements), func(context.Context, string, []byte) (string, error) { return "should not run", nil })
			if err == nil || !strings.Contains(err.Error(), item.want) || result.IR.NormalizedText != "" {
				t.Fatalf("want fail-closed %q, got text=%q err=%v", item.want, result.IR.NormalizedText, err)
			}
		})
	}
}

func docxPNGHeader(width, height uint32) []byte {
	chunk := make([]byte, 21)
	binary.BigEndian.PutUint32(chunk[:4], 13)
	copy(chunk[4:8], "IHDR")
	binary.BigEndian.PutUint32(chunk[8:12], width)
	binary.BigEndian.PutUint32(chunk[12:16], height)
	chunk[16] = 8 // bit depth; the remaining IHDR fields are zero-valued.
	data := append([]byte{137, 80, 78, 71, 13, 10, 26, 10}, chunk...)
	crc := crc32.ChecksumIEEE(data[12:29])
	var checksum [4]byte
	binary.BigEndian.PutUint32(checksum[:], crc)
	return append(data, checksum[:]...)
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
