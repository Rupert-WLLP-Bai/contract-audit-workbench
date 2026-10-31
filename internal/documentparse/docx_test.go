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
		{"alt content", map[string][]byte{"word/document.xml": document(`<mc:AlternateContent xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"/>`)}, "no supported branch"},
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

func TestDOCXPublicStructureAndVisibleErrorBoundaries(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	cases := []struct {
		name, xml, want string
	}{
		{"wrong root with body", `<w:notDocument xmlns:w="` + docxWNS + `"><w:body><w:p/></w:body></w:notDocument>`, "invalid main document/body"},
		{"document without body", `<w:document xmlns:w="` + docxWNS + `"><w:p/></w:document>`, "invalid main document/body"},
		{"visible unsupported footnote", `<w:document xmlns:w="` + docxWNS + `"><w:body><w:p><w:r><w:t>visible</w:t><w:footnoteReference w:id="1"/></w:r></w:p></w:body></w:document>`, "visible footnote/endnote references are unsupported"},
		{"invalid table fragment", `<w:document xmlns:w="` + docxWNS + `"><w:body><w:tbl><w:tr><w:tc><w:tcPr><w:vMerge/></w:tcPr><w:p/></w:tc></w:tr></w:tbl></w:body></w:document>`, "vertical merge has no matching origin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": []byte(tc.xml)}), nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseDOCX error = %v, want %q", err, tc.want)
			}
			if result.IR.NormalizedText != "" {
				t.Fatalf("failed parse returned partial text %q", result.IR.NormalizedText)
			}
		})
	}
}

func TestDOCXPublicSingleCharacterStoryAndTableBoundaries(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	document := []byte(`<w:document xmlns:w="` + docxWNS + `"><w:body><w:p><w:r><w:t>A</w:t></w:r></w:p><w:p><w:r><w:t>B</w:t></w:r></w:p></w:body></w:document>`)
	result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": document}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IR.NormalizedText != "A\n\nB" || len(result.IR.Blocks) != 2 {
		t.Fatalf("one-rune paragraphs lost their separator: text=%q blocks=%+v", result.IR.NormalizedText, result.IR.Blocks)
	}

	table := []byte(`<w:document xmlns:w="` + docxWNS + `"><w:body><w:tbl><w:tr><w:tc><w:p><w:r><w:t>A</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>B</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:body></w:document>`)
	result, err = ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": table}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IR.NormalizedText != "A\tB" || len(result.IR.Cells) != 2 || len(result.IR.Blocks) != 1 || result.IR.Blocks[0].Kind != "table" {
		t.Fatalf("single-character table cells lost their delimiter or table kind: %+v", result.IR)
	}

	oneCell := []byte(`<w:document xmlns:w="` + docxWNS + `"><w:body><w:tbl><w:tr><w:tc><w:p><w:r><w:t>only</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:body></w:document>`)
	result, err = ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": oneCell}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.IR.Blocks) != 1 || result.IR.Blocks[0].Kind != "table" || len(result.IR.Cells) != 1 {
		t.Fatalf("one-cell table was not marked as a table: %+v", result.IR)
	}
}

func TestDOCXPublicStoryReferencesAreValidatedAndDeduplicated(t *testing.T) {
	complex := docxFixture(t, "complex-tables.docx")
	document := string(docxPart(t, complex, "word/document.xml"))
	duplicate := strings.Replace(document, "</w:body>", `<w:sectPr><w:headerReference w:type="default" r:id="header"/><w:headerReference w:type="first" r:id="header"/></w:sectPr></w:body>`, 1)
	result, err := ParseDOCX(context.Background(), docxRewrite(t, complex, map[string][]byte{"word/document.xml": []byte(duplicate)}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(result.IR.NormalizedText, "合成页眉：内部演示"); got != 1 {
		t.Fatalf("referenced header appeared %d times, want once", got)
	}

	wrongKind := strings.Replace(document, "</w:body>", `<w:sectPr><w:headerReference w:type="default" r:id="footer"/></w:sectPr></w:body>`, 1)
	result, err = ParseDOCX(context.Background(), docxRewrite(t, complex, map[string][]byte{"word/document.xml": []byte(wrongKind)}), nil)
	if err == nil || !strings.Contains(err.Error(), "missing or invalid header relationship") || result.IR.NormalizedText != "" {
		t.Fatalf("wrong-kind header relationship did not fail closed: text=%q err=%v", result.IR.NormalizedText, err)
	}
}

func TestDOCXPublicNonContentIsNamespaceScoped(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	transitional := []byte(`<w:document xmlns:w="` + docxWNS + `" xmlns:s="` + docxWStrictNS + `"><w:body><w:p><w:r><w:t>visible</w:t><w:bookmarkStart w:id="1" w:name="x"/><w:bookmarkEnd w:id="1"/><s:commentReference s:id="2"/></w:r></w:p></w:body></w:document>`)
	result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": transitional}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IR.NormalizedText != "visible" {
		t.Fatalf("Word non-content nodes changed visible text: %q", result.IR.NormalizedText)
	}

	foreign := []byte(`<w:document xmlns:w="` + docxWNS + `" xmlns:x="urn:foreign"><w:body><w:p><w:r><w:t>visible</w:t><x:bookmarkStart/></w:r></w:p></w:body></w:document>`)
	result, err = ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": foreign}), nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported visible inline content bookmarkStart") || result.IR.NormalizedText != "" {
		t.Fatalf("foreign-namespace bookmark was ignored: text=%q err=%v", result.IR.NormalizedText, err)
	}
}

func TestDOCXPublicCombinedStoriesEnforceExactTextLimit(t *testing.T) {
	complex := docxFixture(t, "complex-tables.docx")
	mainText := strings.Repeat("m", docxMaxText/2)
	makeArchive := func(headerLength int) []byte {
		document := []byte(`<w:document xmlns:w="` + docxWNS + `" xmlns:r="` + docxRNS + `"><w:body><w:p><w:r><w:t>` + mainText + `</w:t></w:r></w:p><w:sectPr><w:headerReference w:type="default" r:id="header"/></w:sectPr></w:body></w:document>`)
		header := []byte(`<w:hdr xmlns:w="` + docxWNS + `"><w:p><w:r><w:t>` + strings.Repeat("h", headerLength) + `</w:t></w:r></w:p></w:hdr>`)
		return docxRewrite(t, complex, map[string][]byte{"word/document.xml": document, "word/header1.xml": header})
	}

	exactHeaderLength := docxMaxText - len(mainText) - 2
	result, err := ParseDOCX(context.Background(), makeArchive(exactHeaderLength), nil)
	if err != nil {
		t.Fatalf("exact combined text limit failed: %v", err)
	}
	if len(result.IR.NormalizedText) != docxMaxText {
		t.Fatalf("exact combined text length = %d, want %d", len(result.IR.NormalizedText), docxMaxText)
	}

	_, err = ParseDOCX(context.Background(), makeArchive(exactHeaderLength+1), nil)
	if err == nil || !strings.Contains(err.Error(), "extracted text exceeds 8 MiB limit") {
		t.Fatalf("combined text above limit was accepted: %v", err)
	}
}

func TestDOCXPublicTableJoinEnforcesTextLimitBeforeLaterTables(t *testing.T) {
	original := docxFixture(t, "embedded-image.docx")
	document := string(docxPart(t, original, "word/document.xml"))
	start := strings.Index(document, "<w:drawing>")
	if start < 0 {
		t.Fatal("embedded-image fixture has no drawing")
	}
	end := strings.Index(document[start:], "</w:drawing>")
	if end < 0 {
		t.Fatal("embedded-image fixture has an unterminated drawing")
	}
	drawing := document[start : start+end+len("</w:drawing>")]
	draw := func(id string) string { return strings.Replace(drawing, `r:embed="image1"`, `r:embed="`+id+`"`, 1) }
	prefix := document[:strings.Index(document, "<w:body>")]
	imageCell := func(id string) string {
		return `<w:tc><w:p><w:r>` + draw(id) + `</w:r></w:p></w:tc>`
	}
	makeDocument := func(laterTable bool) []byte {
		body := `<w:tbl><w:tblGrid><w:gridCol/><w:gridCol/></w:tblGrid><w:tr>` + imageCell("image1") + imageCell("image2") + `</w:tr></w:tbl>`
		if laterTable {
			body += `<w:tbl><w:tblGrid><w:gridCol/></w:tblGrid><w:tr>` + imageCell("image3") + `</w:tr></w:tbl>`
		}
		return []byte(prefix + `<w:body>` + body + `</w:body></w:document>`)
	}
	relationships := string(docxPart(t, original, "word/_rels/document.xml.rels"))
	relationships = strings.Replace(relationships, `</Relationships>`, `<Relationship Id="image2" Type="`+docxRNS+`/image" Target="media/image2.png"/><Relationship Id="image3" Type="`+docxRNS+`/image" Target="media/image3.png"/></Relationships>`, 1)
	image := docxPart(t, original, "word/media/bandwidth.png")
	archive := func(laterTable bool) []byte {
		return docxRewrite(t, original, map[string][]byte{
			"word/document.xml":            makeDocument(laterTable),
			"word/_rels/document.xml.rels": []byte(relationships),
			"word/media/image2.png":        image,
			"word/media/image3.png":        image,
		})
	}
	parse := func(body []byte, lengths []int) (Result, error, int) {
		calls := 0
		result, err := ParseDOCX(context.Background(), body, func(context.Context, string, []byte) (string, error) {
			if calls >= len(lengths) {
				calls++
				return "unexpected OCR call", nil
			}
			value := strings.Repeat("x", lengths[calls])
			calls++
			return value, nil
		})
		return result, err, calls
	}

	half := docxMaxText / 2
	result, err, calls := parse(archive(false), []int{half, half - 1})
	if err != nil || calls != 2 || len(result.IR.NormalizedText) != docxMaxText {
		t.Fatalf("exact table join limit: text=%d OCR calls=%d err=%v", len(result.IR.NormalizedText), calls, err)
	}

	_, err, calls = parse(archive(true), []int{half, half})
	if err == nil || !strings.Contains(err.Error(), "extracted text exceeds 8 MiB limit") || calls != 2 {
		t.Fatalf("oversized table join must stop before later table: OCR calls=%d err=%v", calls, err)
	}
}

const (
	docxAltDecl  = `xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"`
	docxWPSDecl  = `xmlns:wps="http://schemas.microsoft.com/office/word/2010/wordprocessingShape"`
	docxVMLDecl  = `xmlns:v="urn:schemas-microsoft-com:vml"`
	docxDrawingN = `xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"`
)

// docxShapeBody renders one wordprocessingShape drawing whose visible content is
// a text box, the construct Word uses for page numbers in footers.
func docxShapeBody(inner string) string {
	return `<w:drawing><wp:inline><a:graphic><a:graphicData uri="` + docxWPSNS + `"><wps:wsp><wps:cNvSpPr/>` +
		`<wps:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="1" cy="1"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></wps:spPr>` +
		`<wps:txbx><w:txbxContent>` + inner + `</w:txbxContent></wps:txbx><wps:bodyPr><a:spAutoFit/></wps:bodyPr>` +
		`</wps:wsp></a:graphicData></a:graphic></wp:inline></w:drawing>`
}

func docxTextBoxParagraph(value string) string {
	return `<w:p><w:r><w:t>` + value + `</w:t></w:r></w:p>`
}

func docxAltContentDocument(content string) []byte {
	return []byte(`<w:document xmlns:w="` + docxWNS + `" xmlns:r="` + docxRNS + `" ` + docxDrawingN + ` ` + docxWPSDecl + ` ` + docxAltDecl + ` ` + docxVMLDecl + `><w:body>` +
		content + `</w:body></w:document>`)
}

func TestDOCXShapeTextBoxCarriesVisibleParagraphs(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	document := docxAltContentDocument(docxTextBoxParagraph("正文") +
		`<w:p><w:r>` + docxShapeBody(docxTextBoxParagraph("框内条款")+docxTextBoxParagraph("框内第二条")) + `</w:r></w:p>`)
	result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": document}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.IR.NormalizedText, "框内条款") || !strings.Contains(result.IR.NormalizedText, "框内第二条") {
		t.Fatalf("text box content was dropped: %q", result.IR.NormalizedText)
	}
	if anchor := docir.Locate(result.IR, "框内条款"); anchor.Status != docir.StatusLocated {
		t.Fatalf("text box quote is not locatable: %+v", anchor)
	}
}

func TestDOCXAlternateContentReadsExactlyOneBranch(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	// alternate builds one AlternateContent whose single Choice carries the given
	// attributes, so prefix aliases, rebinding and undeclared prefixes are all
	// expressible without assuming a literal wps spelling.
	alternate := func(choiceAttrs, choice, fallback string) string {
		fallbackXML := ""
		if fallback != "" {
			fallbackXML = `<mc:Fallback>` + fallback + `</mc:Fallback>`
		}
		return `<w:p><w:r><mc:AlternateContent><mc:Choice ` + choiceAttrs + `>` + choice + `</mc:Choice>` + fallbackXML + `</mc:AlternateContent></w:r></w:p>`
	}
	fallbackVML := `<w:pict><v:shape><v:textbox>` + docxTextBoxParagraph("回退分支") + `</v:textbox></v:shape></w:pict>`
	for _, item := range []struct {
		name    string
		content string
		want    string
		absent  string
		ordered []string
	}{
		{
			name:    "supported choice wins",
			content: alternate(`Requires="wps"`, docxShapeBody(docxTextBoxParagraph("首选分支")), fallbackVML),
			want:    "首选分支",
			absent:  "回退分支",
		},
		{
			name:    "alias prefix bound to the shape namespace is supported",
			content: alternate(`Requires="shape" xmlns:shape="`+docxWPSNS+`"`, docxShapeBody(docxTextBoxParagraph("别名分支")), `<w:r><w:t>回退文本</w:t></w:r>`),
			want:    "别名分支",
			absent:  "回退文本",
		},
		{
			name:    "bound unknown requirement uses fallback",
			content: alternate(`Requires="wpg" xmlns:wpg="http://schemas.microsoft.com/office/word/2010/wordprocessingGroup"`, `<w:drawing/>`, `<w:r><w:t>回退文本</w:t></w:r>`),
			want:    "回退文本",
			absent:  "",
		},
		{
			name:    "rebound wps prefix is not the shape namespace",
			content: alternate(`Requires="wps" xmlns:wps="urn:unknown-shape"`, docxShapeBody(docxTextBoxParagraph("误绑分支")), `<w:r><w:t>回退文本</w:t></w:r>`),
			want:    "回退文本",
			absent:  "误绑分支",
		},
		{
			name:    "one unsupported requirement skips the choice",
			content: alternate(`Requires="wps w14" xmlns:w14="http://schemas.microsoft.com/office/word/2010/wordml"`, docxShapeBody(docxTextBoxParagraph("多前缀分支")), `<w:r><w:t>回退文本</w:t></w:r>`),
			want:    "回退文本",
			absent:  "多前缀分支",
		},
		{
			name:    "later supported choice wins over fallback",
			content: `<w:p><w:r><mc:AlternateContent><mc:Choice Requires="wpg" xmlns:wpg="urn:group">` + docxShapeBody(docxTextBoxParagraph("首选项分支")) + `</mc:Choice><mc:Choice Requires="wps">` + docxShapeBody(docxTextBoxParagraph("次选分支")) + `</mc:Choice><mc:Fallback><w:r><w:t>回退文本</w:t></w:r></mc:Fallback></mc:AlternateContent></w:r></w:p>`,
			want:    "次选分支",
			absent:  "首选项分支",
		},
		{
			name:    "selected branch keeps surrounding visible order once",
			content: `<w:p><w:r><w:t>前文</w:t></w:r><w:r><mc:AlternateContent><mc:Choice Requires="wps">` + docxShapeBody(docxTextBoxParagraph("框内分支")) + `</mc:Choice><mc:Fallback>` + docxTextBoxParagraph("回退文本") + `</mc:Fallback></mc:AlternateContent></w:r><w:r><w:t>后文</w:t></w:r></w:p>`,
			want:    "框内分支",
			absent:  "回退文本",
			ordered: []string{"前文", "框内分支", "后文"},
		},
		{
			name:    "fallback only",
			content: `<w:p><w:r><mc:AlternateContent><mc:Fallback><w:r><w:t>仅回退</w:t></w:r></mc:Fallback></mc:AlternateContent></w:r></w:p>`,
			want:    "仅回退",
			absent:  "",
		},
		{
			name:    "block level alternate",
			content: `<mc:AlternateContent><mc:Choice Requires="wps"><w:p><w:r>` + docxShapeBody(docxTextBoxParagraph("块级首选")) + `</w:r></w:p></mc:Choice><mc:Fallback>` + docxTextBoxParagraph("块级回退") + `</mc:Fallback></mc:AlternateContent>`,
			want:    "块级首选",
			absent:  "块级回退",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": docxAltContentDocument(item.content)}), nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(result.IR.NormalizedText, item.want) {
				t.Fatalf("missing %q in %q", item.want, result.IR.NormalizedText)
			}
			if item.absent != "" && strings.Contains(result.IR.NormalizedText, item.absent) {
				t.Fatalf("unrendered branch leaked %q into %q", item.absent, result.IR.NormalizedText)
			}
			previous := -1
			for _, quote := range item.ordered {
				if count := strings.Count(result.IR.NormalizedText, quote); count != 1 {
					t.Fatalf("%q appears %d times, want exactly once in %q", quote, count, result.IR.NormalizedText)
				}
				if at := strings.Index(result.IR.NormalizedText, quote); at <= previous {
					t.Fatalf("visible order changed around %q in %q", quote, result.IR.NormalizedText)
				} else {
					previous = at
				}
			}
		})
	}
}

func TestDOCXNamespaceDeclarationsAreNotAttributes(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	// A declaration named after a real attribute must not shadow it, and the
	// shape namespace itself may be declared through any prefix.
	document := docxAltContentDocument(
		`<w:p><w:r>` + docxShapeBody(docxTextBoxParagraph("声明不遮蔽")) + `</w:r></w:p>` +
			`<mc:AlternateContent><mc:Choice Requires="s" xmlns:s="` + docxWPSNS + `">` + docxTextBoxParagraph("别名声明正文") + `</mc:Choice></mc:AlternateContent>`)
	document = bytes.Replace(document, []byte(`<a:graphicData uri="`+docxWPSNS+`">`), []byte(`<a:graphicData xmlns:uri="urn:bogus" uri="`+docxWPSNS+`">`), 1)
	result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": document}), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"声明不遮蔽", "别名声明正文"} {
		if !strings.Contains(result.IR.NormalizedText, want) {
			t.Fatalf("missing %q in %q", want, result.IR.NormalizedText)
		}
	}
}

func TestDOCXAlternateContentFailuresStayClosed(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	for _, item := range []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "choice without requires",
			content: `<w:p><w:r><mc:AlternateContent><mc:Choice>` + docxShapeBody(docxTextBoxParagraph("无声明分支")) + `</mc:Choice><mc:Fallback><w:r><w:t>回退文本</w:t></w:r></mc:Fallback></mc:AlternateContent></w:r></w:p>`,
			want:    "choice has no Requires",
		},
		{
			name:    "choice with empty requires",
			content: `<w:p><w:r><mc:AlternateContent><mc:Choice Requires="  ">` + docxShapeBody(docxTextBoxParagraph("空声明分支")) + `</mc:Choice></mc:AlternateContent></w:r></w:p>`,
			want:    "choice has no Requires",
		},
		{
			name:    "undeclared requires prefix",
			content: `<w:p><w:r><mc:AlternateContent><mc:Choice Requires="nope">` + docxShapeBody(docxTextBoxParagraph("未声明分支")) + `</mc:Choice><mc:Fallback><w:r><w:t>回退文本</w:t></w:r></mc:Fallback></mc:AlternateContent></w:r></w:p>`,
			want:    `requires undeclared prefix "nope"`,
		},
		{
			name:    "rebound wps without fallback",
			content: `<w:p><w:r><mc:AlternateContent><mc:Choice Requires="wps" xmlns:wps="urn:unknown-shape">` + docxShapeBody(docxTextBoxParagraph("误绑分支")) + `</mc:Choice></mc:AlternateContent></w:r></w:p>`,
			want:    "no supported branch",
		},
		{
			name:    "choice after fallback",
			content: `<w:p><w:r><mc:AlternateContent><mc:Fallback><w:r><w:t>回退</w:t></w:r></mc:Fallback><mc:Choice Requires="wps"><w:r><w:t>首选</w:t></w:r></mc:Choice></mc:AlternateContent></w:r></w:p>`,
			want:    "choice after fallback",
		},
		{
			name:    "duplicate fallback",
			content: `<w:p><w:r><mc:AlternateContent><mc:Fallback><w:r><w:t>a</w:t></w:r></mc:Fallback><mc:Fallback><w:r><w:t>b</w:t></w:r></mc:Fallback></mc:AlternateContent></w:r></w:p>`,
			want:    "duplicate AlternateContent fallback",
		},
		{
			name:    "foreign alternate entry",
			content: `<w:p><w:r><mc:AlternateContent><w:r><w:t>x</w:t></w:r></mc:AlternateContent></w:r></w:p>`,
			want:    "is not markup compatibility",
		},
		{
			name:    "shape without text box",
			content: `<w:p><w:r><w:drawing><wp:inline><a:graphic><a:graphicData uri="` + docxWPSNS + `"><wps:wsp><wps:spPr/></wps:wsp></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`,
			want:    "exactly one text box",
		},
		{
			name:    "grouped shape",
			content: `<w:p><w:r><w:drawing><wp:inline><a:graphic><a:graphicData uri="` + docxWPSNS + `"><wps:wgp/></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`,
			want:    "grouped or nested shapes are unsupported",
		},
		{
			name:    "image and text box in one drawing",
			content: `<w:p><w:r><w:drawing><wp:inline><a:graphic><a:graphicData uri="` + docxWPSNS + `"><wps:wsp><wps:txbx><w:txbxContent>` + docxTextBoxParagraph("框内") + `</w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic><a:graphic><a:graphicData uri="` + docxPictureNS + `"><pic:pic><pic:blipFill><a:blip r:embed="image1"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`,
			want:    "exactly one supported raster image or one text box",
		},
		{
			name:    "table in text box",
			content: `<w:p><w:r>` + docxShapeBody(`<w:tbl><w:tr><w:tc>`+docxTextBoxParagraph("单元格")+`</w:tc></w:tr></w:tbl>`) + `</w:r></w:p>`,
			want:    "tables inside text boxes are unsupported",
		},
		{
			name:    "wordprocessing content in shape properties",
			content: `<w:p><w:r><w:drawing><wp:inline><a:graphic><a:graphicData uri="` + docxWPSNS + `"><wps:wsp><wps:spPr><w:t>藏在属性里</w:t></wps:spPr><wps:txbx><w:txbxContent>` + docxTextBoxParagraph("框内") + `</w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`,
			want:    "unsupported shape property",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": docxAltContentDocument(item.content)}), nil)
			if err == nil || !strings.Contains(err.Error(), item.want) {
				t.Fatalf("want %q, got %v", item.want, err)
			}
			if result.IR.NormalizedText != "" {
				t.Fatalf("failed parse returned text %q", result.IR.NormalizedText)
			}
		})
	}
}

func TestDOCXPaginationFieldsNeverBecomeContractText(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	page := `<w:r><w:fldChar w:fldCharType="begin"/><w:instrText xml:space="preserve"> PAGE  \* MERGEFORMAT </w:instrText><w:fldChar w:fldCharType="separate"/><w:t>7</w:t><w:fldChar w:fldCharType="end"/></w:r>`
	numpages := `<w:r><w:fldChar w:fldCharType="begin"/><w:instrText> NUMPAGES </w:instrText><w:fldChar w:fldCharType="separate"/><w:t>12</w:t><w:fldChar w:fldCharType="end"/></w:r>`
	reference := `<w:r><w:fldChar w:fldCharType="begin"/><w:instrText> REF clauseA </w:instrText><w:fldChar w:fldCharType="separate"/><w:t>5.1</w:t><w:fldChar w:fldCharType="end"/></w:r>`
	document := docxAltContentDocument(`<w:p>` + page + numpages + reference + docxShapeParagraphRun("页") + `</w:p>` +
		`<w:p><w:fldSimple w:instr=" PAGE "><w:r><w:t>9</w:t></w:r></w:fldSimple><w:fldSimple w:instr=" REF clauseB "><w:r><w:t>6.2</w:t></w:r></w:fldSimple></w:p>`)
	result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": document}), nil)
	if err != nil {
		t.Fatal(err)
	}
	text := result.IR.NormalizedText
	for _, absent := range []string{"7", "12", "9"} {
		if strings.Contains(text, absent) {
			t.Fatalf("pagination number %q leaked into contract text %q", absent, text)
		}
	}
	for _, want := range []string{"5.1", "6.2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("cached result %q of a non-pagination field was dropped from %q", want, text)
		}
	}
}

func docxShapeParagraphRun(value string) string {
	return `<w:r><w:t>` + value + `</w:t></w:r>`
}

func TestDOCXFieldInstructionFailuresStayClosed(t *testing.T) {
	plain := docxFixture(t, "plain.docx")
	for _, item := range []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "instruction outside a field",
			content: `<w:p><w:r><w:instrText> PAGE </w:instrText></w:r></w:p>`,
			want:    "field instruction outside an open field",
		},
		{
			name:    "instruction after separator",
			content: `<w:p><w:r><w:fldChar w:fldCharType="begin"/><w:fldChar w:fldCharType="separate"/><w:instrText> PAGE </w:instrText><w:fldChar w:fldCharType="end"/></w:r></w:p>`,
			want:    "field instruction outside an open field",
		},
		{
			name:    "instruction over the limit",
			content: `<w:p><w:r><w:fldChar w:fldCharType="begin"/><w:instrText>` + strings.Repeat("A", docxMaxFieldInstruction+1) + `</w:instrText><w:fldChar w:fldCharType="end"/></w:r></w:p>`,
			want:    "field instruction limit exceeded",
		},
		{
			name:    "simple field without instruction",
			content: `<w:p><w:fldSimple><w:r><w:t>无指令</w:t></w:r></w:fldSimple></w:p>`,
			want:    "simple field has no instruction",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			result, err := ParseDOCX(context.Background(), docxRewrite(t, plain, map[string][]byte{"word/document.xml": docxAltContentDocument(item.content)}), nil)
			if err == nil || !strings.Contains(err.Error(), item.want) {
				t.Fatalf("want %q, got %v", item.want, err)
			}
			if result.IR.NormalizedText != "" {
				t.Fatalf("failed parse returned text %q", result.IR.NormalizedText)
			}
		})
	}
}
