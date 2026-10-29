package docir_test

// Characterization tests for the document IR surface. They pin offsets,
// page recording, table extraction, and coordinate coverage before the
// builder and cover helpers are split. Every test calls an exported entry
// point.

import (
	"strings"
	"testing"

	"contractaudit/internal/docir"
	"contractaudit/internal/docversion"
)

func pageNumbers(ir docir.IR) []int {
	out := make([]int, len(ir.Pages))
	for i, page := range ir.Pages {
		out[i] = page.Number
	}
	return out
}

func blockStartEnds(blocks []docir.Block) [][2]int {
	out := make([][2]int, len(blocks))
	for i, block := range blocks {
		out[i] = [2]int{block.Start, block.End}
	}
	return out
}

func cellStartsEnds(cells []docir.Cell) [][2]int {
	out := make([][2]int, len(cells))
	for i, cell := range cells {
		out[i] = [2]int{cell.Start, cell.End}
	}
	return out
}

func findCell(t *testing.T, ir docir.IR, text string) docir.Cell {
	t.Helper()
	for _, cell := range ir.Cells {
		if cell.Text == text {
			return cell
		}
	}
	t.Fatalf("cell %q not found in %+v", text, ir.Cells)
	return docir.Cell{}
}

func assertCellSlices(t *testing.T, ir docir.IR) {
	t.Helper()
	for _, cell := range ir.Cells {
		got, ok := docir.Slice(ir.NormalizedText, cell.Start, cell.End)
		if !ok || got != cell.Text {
			t.Fatalf("cell %s slice = %q, %v; want %q", cell.ID, got, ok, cell.Text)
		}
		if cell.TableID == "" {
			t.Fatalf("cell %s has no table id", cell.ID)
		}
	}
}

func TestBuildEmptyInputPinned(t *testing.T) {
	for _, raw := range []string{"", "   \n\n\t\n", "\n\n\n"} {
		ir := docir.Build(raw)
		if ir.NormalizedText != "" {
			t.Fatalf("normalized %q for %q", ir.NormalizedText, raw)
		}
		if ir.NormalizedDigest != docversion.Hash([]byte("")).String() {
			t.Fatalf("digest %q", ir.NormalizedDigest)
		}
		if ir.CoordinateSpace != docir.CoordinateSpace || ir.ExtractionVersion != docir.ExtractionVersion {
			t.Fatalf("versions %q %q", ir.CoordinateSpace, ir.ExtractionVersion)
		}
		if len(ir.Blocks) != 0 || len(ir.Pages) != 0 || len(ir.Cells) != 0 || len(ir.Clauses) != 0 {
			t.Fatalf("non-empty IR for %q: %+v", raw, ir)
		}
		if ir.Pages == nil || ir.Cells == nil || ir.Clauses == nil {
			t.Fatalf("empty slices must be non-nil: pages=%v cells=%v clauses=%v", ir.Pages, ir.Cells, ir.Clauses)
		}
		if ir.Blocks != nil {
			t.Fatalf("empty input must not invent blocks: %+v", ir.Blocks)
		}
	}
}

func TestBuildNormalizationPinned(t *testing.T) {
	ir := docir.Build("\uFEFF甲\r\n\r\n乙\r丙")
	if ir.NormalizedText != "甲\n\n乙\n丙" {
		t.Fatalf("normalized %q", ir.NormalizedText)
	}
	if len(ir.Blocks) != 2 {
		t.Fatalf("blocks %+v", ir.Blocks)
	}
	if ir.Blocks[0] != (docir.Block{ID: "b-1", Kind: "paragraph", Text: "甲", Start: 0, End: 1, Page: nil}) {
		t.Fatalf("first block %+v", ir.Blocks[0])
	}
	if ir.Blocks[1] != (docir.Block{ID: "b-2", Kind: "paragraph", Text: "乙\n丙", Start: 3, End: 6, Page: nil}) {
		t.Fatalf("second block %+v", ir.Blocks[1])
	}
	if ir.NormalizedDigest != docversion.Hash([]byte("甲\n\n乙\n丙")).String() {
		t.Fatalf("digest %q", ir.NormalizedDigest)
	}

	lone := docir.Build("甲\r乙")
	if lone.NormalizedText != "甲\n乙" || len(lone.Blocks) != 1 || lone.Blocks[0].Text != "甲\n乙" || lone.Blocks[0].End != 3 {
		t.Fatalf("lone carriage return: %+v", lone)
	}

	padded := docir.Build("  合同正文  ")
	if padded.NormalizedText != "合同正文" || len(padded.Blocks) != 1 || padded.Blocks[0].Start != 0 || padded.Blocks[0].End != 4 {
		t.Fatalf("padded input: %+v", padded)
	}
}

func TestBuildFormFeedPinned(t *testing.T) {
	ir := docir.Build("a\fb")
	if ir.NormalizedText != "a\n\nb" {
		t.Fatalf("normalized %q", ir.NormalizedText)
	}
	if len(ir.Pages) != 2 || ir.Pages[0] != (docir.Page{Number: 1, Start: 0, End: 1}) || ir.Pages[1] != (docir.Page{Number: 2, Start: 3, End: 4}) {
		t.Fatalf("pages %+v", ir.Pages)
	}
	if len(ir.Blocks) != 2 || blockStartEnds(ir.Blocks)[0] != [2]int{0, 1} || blockStartEnds(ir.Blocks)[1] != [2]int{3, 4} {
		t.Fatalf("blocks %+v", ir.Blocks)
	}
	if ir.Blocks[0].Page == nil || *ir.Blocks[0].Page != 1 || ir.Blocks[1].Page == nil || *ir.Blocks[1].Page != 2 {
		t.Fatalf("block pages %+v", ir.Blocks)
	}

	skip := docir.Build("a\f\fb")
	if got := pageNumbers(skip); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("an empty page must not be recorded: %v", got)
	}
	if skip.Pages[1] != (docir.Page{Number: 3, Start: 3, End: 4}) {
		t.Fatalf("third page %+v", skip.Pages[1])
	}
	if skip.Blocks[1].Page == nil || *skip.Blocks[1].Page != 3 || skip.Blocks[1].Start != 3 {
		t.Fatalf("third page block %+v", skip.Blocks[1])
	}

	leading := docir.Build("\fa")
	if len(leading.Pages) != 1 || leading.Pages[0] != (docir.Page{Number: 2, Start: 0, End: 1}) {
		t.Fatalf("leading break pages %+v", leading.Pages)
	}
	if leading.Blocks[0].Page == nil || *leading.Blocks[0].Page != 2 {
		t.Fatalf("leading break block %+v", leading.Blocks[0])
	}

	trailing := docir.Build("a\f")
	if len(trailing.Pages) != 1 || pageNumbers(trailing)[0] != 1 {
		t.Fatalf("trailing break pages %+v", trailing.Pages)
	}

	multi := docir.Build("a\f\f\fa")
	if got := pageNumbers(multi); len(got) != 2 || got[0] != 1 || got[1] != 4 {
		t.Fatalf("multi break pages %v", got)
	}
	if multi.Blocks[1].Start != 3 {
		t.Fatalf("multi break block %+v", multi.Blocks[1])
	}
}

func TestBuildTablePinned(t *testing.T) {
	raw := "| 项目 | 期限 |\n| --- | --- |\n| 质保期 | 36个月 |"
	ir := docir.Build(raw)
	if ir.NormalizedText != raw {
		t.Fatalf("normalized %q", ir.NormalizedText)
	}
	if len(ir.Blocks) != 1 || ir.Blocks[0].Kind != "table" || ir.Blocks[0].ID != "b-1" {
		t.Fatalf("block %+v", ir.Blocks)
	}
	if ir.Blocks[0].Start != 0 || ir.Blocks[0].End != 40 {
		t.Fatalf("block offsets %+v", ir.Blocks[0])
	}
	if len(ir.Cells) != 4 {
		t.Fatalf("cells %+v", ir.Cells)
	}
	wantTexts := []string{"项目", "期限", "质保期", "36个月"}
	wantIDs := []string{"t-1-r1c1", "t-1-r1c2", "t-1-r2c1", "t-1-r2c2"}
	wantRows := []int{1, 1, 2, 2}
	wantCols := []int{1, 2, 1, 2}
	wantSpans := [][2]int{{2, 4}, {7, 9}, {28, 31}, {34, 38}}
	for i, cell := range ir.Cells {
		if cell.Text != wantTexts[i] || cell.ID != wantIDs[i] || cell.TableID != "t-1" || cell.Row != wantRows[i] || cell.Col != wantCols[i] {
			t.Fatalf("cell %d = %+v", i, cell)
		}
		if [2]int{cell.Start, cell.End} != wantSpans[i] {
			t.Fatalf("cell %d span %+v, want %v", i, cell, wantSpans[i])
		}
		if cell.Page != nil {
			t.Fatalf("unpaged table invented a page: %+v", cell)
		}
	}
	assertCellSlices(t, ir)
}

func TestBuildTableVariantsPinned(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		kind  string
		text  string
		cells int
	}{
		{"rule only", "| --- | --- |", "paragraph", "| --- | --- |", 0},
		{"single data row", "| a | b |\n| --- | --- |", "paragraph", "| a | b |\n| --- | --- |", 0},
		{"line without pipe", "| a | b |\nplain line", "paragraph", "| a | b |\nplain line", 0},
		{"exactly two cells", "| a |\n| --- |\n| b |", "table", "| a |\n| --- |\n| b |", 2},
		{"empty cells", "| a || b |\n|---|---|---|\n| c || d |", "table", "| a || b |\n|---|---|---|\n| c || d |", 4},
		{"leading pipe omitted", "a | b\n---|---\nc | d", "table", "a | b\n---|---\nc | d", 4},
		{"colon line is a data row", "| a | b |\n| : |\n| c | d |\n| --- | --- |", "table", "| a | b |\n| : |\n| c | d |\n| --- | --- |", 5},
		{"invalid rule character is a data row", "| a |\n| --- |\n| -x- |\n| b |", "table", "| a |\n| --- |\n| -x- |\n| b |", 3},
		{"whitespace line inside table", "| a | b |\n   \n| c | d |\n|---|---|", "table", "| a | b |\n   \n| c | d |\n|---|---|", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ir := docir.Build(tc.raw)
			if len(ir.Blocks) != 1 || ir.Blocks[0].Kind != tc.kind {
				t.Fatalf("blocks %+v", ir.Blocks)
			}
			if len(ir.Cells) != tc.cells {
				t.Fatalf("cells %+v", ir.Cells)
			}
			assertCellSlices(t, ir)
		})
	}

	t.Run("rule only keeps paragraph kind", func(t *testing.T) {
		ir := docir.Build("| --- | --- |")
		if ir.Blocks[0].Text != "| --- | --- |" {
			t.Fatalf("block %+v", ir.Blocks[0])
		}
	})

	t.Run("empty cells skip columns", func(t *testing.T) {
		ir := docir.Build("| a || b |\n|---|---|---|\n| c || d |")
		for _, cell := range ir.Cells {
			if cell.Col != 1 && cell.Col != 3 {
				t.Fatalf("empty middle cell must not get a column: %+v", cell)
			}
		}
	})

	t.Run("first cell starts at column zero", func(t *testing.T) {
		ir := docir.Build("a | b\n---|---\nc | d")
		first := findCell(t, ir, "a")
		if first.Start != 0 || first.Row != 1 || first.Col != 1 {
			t.Fatalf("first cell %+v", first)
		}
	})

	t.Run("invalid rule character keeps its row", func(t *testing.T) {
		ir := docir.Build("| a |\n| --- |\n| -x- |\n| b |")
		cell := findCell(t, ir, "-x-")
		if cell.Row != 2 || cell.Col != 1 {
			t.Fatalf("invalid rule line cell %+v", cell)
		}
		if b := findCell(t, ir, "b"); b.Row != 3 {
			t.Fatalf("row after invalid rule line %+v", b)
		}
	})

	t.Run("colon line keeps its row", func(t *testing.T) {
		ir := docir.Build("| a | b |\n| : |\n| c | d |\n| --- | --- |")
		colon := findCell(t, ir, ":")
		if colon.Row != 2 || colon.Col != 1 {
			t.Fatalf("colon cell %+v", colon)
		}
		c := findCell(t, ir, "c")
		if c.Row != 3 {
			t.Fatalf("c cell %+v", c)
		}
	})

	t.Run("whitespace line keeps row numbers", func(t *testing.T) {
		ir := docir.Build("| a | b |\n   \n| c | d |\n|---|---|")
		if len(ir.Cells) != 4 {
			t.Fatalf("cells %+v", ir.Cells)
		}
		if cell := findCell(t, ir, "c"); cell.Row != 2 || cell.Col != 1 {
			t.Fatalf("c cell %+v", cell)
		}
		if cell := findCell(t, ir, "d"); cell.Row != 2 || cell.Col != 2 {
			t.Fatalf("d cell %+v", cell)
		}
	})

	t.Run("single cell table rolls back counter", func(t *testing.T) {
		ir := docir.Build("| a |\n| --- |\n|  |\n|  |\n\n| x | y |\n| --- | --- |\n| 1 | 2 |")
		if len(ir.Blocks) != 2 || ir.Blocks[0].Kind != "paragraph" || ir.Blocks[1].Kind != "table" {
			t.Fatalf("blocks %+v", ir.Blocks)
		}
		if len(ir.Cells) != 4 {
			t.Fatalf("cells %+v", ir.Cells)
		}
		if ir.Cells[0].TableID != "t-1" {
			t.Fatalf("rolled back table number reused: %+v", ir.Cells[0])
		}
	})

	t.Run("two tables number in order", func(t *testing.T) {
		ir := docir.Build("| a | b |\n|---|---|\n| c | d |\n\n| e | f |\n|---|---|\n| g | h |")
		if len(ir.Cells) != 8 {
			t.Fatalf("cells %+v", ir.Cells)
		}
		if ir.Cells[0].TableID != "t-1" || ir.Cells[4].TableID != "t-2" {
			t.Fatalf("table ids %+v", ir.Cells)
		}
	})

	t.Run("paged table carries page", func(t *testing.T) {
		ir := docir.Build("封面\f| a | b |\n|---|---|\n| c | d |")
		cell := findCell(t, ir, "d")
		if cell.Page == nil || *cell.Page != 2 {
			t.Fatalf("cell %+v", cell)
		}
		if page, ok := ir.PageAt(cell.Start, cell.End); !ok || page != 2 {
			t.Fatalf("PageAt = %d, %v", page, ok)
		}
		if got := ir.ElementAt(cell.Start, cell.End); got != cell.ID {
			t.Fatalf("ElementAt = %q, want %q", got, cell.ID)
		}
	})
}

func TestBuildTableOffsetsPinned(t *testing.T) {
	ir := docir.Build("| 36 | 36 |\n|---|---|\n| 36 | 36 |")
	if len(ir.Cells) != 4 {
		t.Fatalf("cells %+v", ir.Cells)
	}
	want := [][2]int{{2, 4}, {7, 9}, {24, 26}, {29, 31}}
	got := cellStartsEnds(ir.Cells)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cell %d span %v, want %v", i, got[i], want[i])
		}
	}
	assertCellSlices(t, ir)
}

func TestCoverPrecedencePinned(t *testing.T) {
	ir := docir.Build("第一条 甲\n\n乙乙\n\n第二条 丙")
	if len(ir.Blocks) != 3 || len(ir.Clauses) != 2 {
		t.Fatalf("blocks %+v clauses %+v", ir.Blocks, ir.Clauses)
	}
	if ir.Blocks[0].Start != 0 || ir.Blocks[0].End != 5 || ir.Blocks[1].Start != 7 || ir.Blocks[1].End != 9 || ir.Blocks[2].Start != 11 || ir.Blocks[2].End != 16 {
		t.Fatalf("block spans %v", blockStartEnds(ir.Blocks))
	}
	if ir.Clauses[0].ID != "k-1" || ir.Clauses[0].Start != 0 || ir.Clauses[0].End != 11 {
		t.Fatalf("first clause %+v", ir.Clauses[0])
	}
	if ir.Clauses[1].ID != "k-2" || ir.Clauses[1].Start != 11 || ir.Clauses[1].End != 16 {
		t.Fatalf("second clause %+v", ir.Clauses[1])
	}
	if ir.Clauses[0].Heading != "第一条 甲" || ir.Clauses[1].Heading != "第二条 丙" {
		t.Fatalf("headings %+v", ir.Clauses)
	}

	tests := []struct {
		name  string
		start int
		end   int
		want  string
	}{
		{"inside first block", 0, 5, "b-1"},
		{"inside second block", 7, 9, "b-2"},
		{"inside third block", 11, 16, "b-3"},
		{"across blocks uses clause", 0, 9, "k-1"},
		{"separator belongs to clause", 5, 6, "k-1"},
		{"clause boundary end", 5, 11, "k-1"},
		{"outside document", 0, 17, ""},
		{"before document", -1, 3, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ir.ElementAt(tc.start, tc.end); got != tc.want {
				t.Fatalf("ElementAt(%d,%d) = %q, want %q", tc.start, tc.end, got, tc.want)
			}
		})
	}
	if page, ok := ir.PageAt(0, 9); ok || page != 0 {
		t.Fatalf("an unpaged clause invented a page: %d, %v", page, ok)
	}

	table := docir.Build("| a | b |\n|---|---|\n| c | d |")
	cell := findCell(t, table, "a")
	if got := table.ElementAt(cell.Start, cell.End); got != cell.ID {
		t.Fatalf("cell span = %q, want %q", got, cell.ID)
	}
	if got := table.ElementAt(cell.Start-1, cell.End); got != "b-1" {
		t.Fatalf("span crossing the cell start = %q, want b-1", got)
	}
	if got := table.ElementAt(cell.Start, cell.End+1); got != "b-1" {
		t.Fatalf("span crossing the cell end = %q, want b-1", got)
	}
	if got := table.ElementAt(0, table.Blocks[0].End); got != "b-1" {
		t.Fatalf("full block span = %q, want b-1", got)
	}
	if got := table.ElementAt(0, table.Blocks[0].End+1); got != "" {
		t.Fatalf("span beyond block = %q, want empty", got)
	}

	paged := docir.Build("第一条 甲\f乙")
	if len(paged.Clauses) != 1 || paged.Clauses[0].End != 8 || paged.Clauses[0].Page == nil || *paged.Clauses[0].Page != 1 {
		t.Fatalf("paged clause %+v", paged.Clauses)
	}
	if got := paged.ElementAt(5, 8); got != "k-1" {
		t.Fatalf("paged clause span = %q", got)
	}
	if page, ok := paged.PageAt(5, 8); !ok || page != 1 {
		t.Fatalf("clause PageAt = %d, %v", page, ok)
	}
}

func TestClausesPinned(t *testing.T) {
	none := docir.Build("普通段落\n\n另一段")
	if len(none.Clauses) != 0 || none.Clauses == nil {
		t.Fatalf("clauses %+v", none.Clauses)
	}

	ir := docir.Build("第一条 甲\n\n乙\n\n第二条 丙\n\n丁")
	if len(ir.Clauses) != 2 {
		t.Fatalf("clauses %+v", ir.Clauses)
	}
	if ir.Clauses[0].ID != "k-1" || ir.Clauses[0].Start != 0 || ir.Clauses[0].End != 10 || ir.Clauses[0].Heading != "第一条 甲" {
		t.Fatalf("first clause %+v", ir.Clauses[0])
	}
	if ir.Clauses[1].ID != "k-2" || ir.Clauses[1].Start != 10 || ir.Clauses[1].End != 18 || ir.Clauses[1].Heading != "第二条 丙" {
		t.Fatalf("second clause %+v", ir.Clauses[1])
	}

	multiLine := docir.Build("第一条 甲\n续行内容")
	if len(multiLine.Clauses) != 1 || multiLine.Clauses[0].Heading != "第一条 甲" || multiLine.Clauses[0].End != 10 {
		t.Fatalf("multi-line clause %+v", multiLine.Clauses)
	}

	for raw, want := range map[string]int{
		"第十二条 甲":   1,
		"第123条 甲":  1,
		"第1234条 甲": 0,
		"第x条 甲":    0,
		"前言 第一条":   0,
	} {
		if got := len(docir.Build(raw).Clauses); got != want {
			t.Fatalf("clauses for %q = %d, want %d", raw, got, want)
		}
	}
}

func TestTwoDigitIdentifiersPinned(t *testing.T) {
	blocks := make([]string, 12)
	for i := range blocks {
		blocks[i] = "a"
	}
	ir := docir.Build(strings.Join(blocks, "\n\n"))
	if len(ir.Blocks) != 12 || ir.Blocks[11].ID != "b-12" {
		t.Fatalf("blocks %+v", ir.Blocks)
	}

	var paged strings.Builder
	for i := 0; i < 12; i++ {
		if i > 0 {
			paged.WriteString("\f")
		}
		paged.WriteString("a")
	}
	pageIR := docir.Build(paged.String())
	if len(pageIR.Pages) != 12 || pageIR.Pages[11].Number != 12 {
		t.Fatalf("pages %+v", pageIR.Pages)
	}
	if pageIR.Blocks[11].Page == nil || *pageIR.Blocks[11].Page != 12 {
		t.Fatalf("last block %+v", pageIR.Blocks[11])
	}
}

func TestSliceBoundariesPinned(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		start, end int
		want       string
		wantOK     bool
	}{
		{"whole string", "中文", 0, 2, "中文", true},
		{"first rune", "中文", 0, 1, "中", true},
		{"end beyond length", "中文", 0, 3, "", false},
		{"negative start", "中文", -1, 1, "", false},
		{"empty span", "中文", 1, 1, "", false},
		{"reversed span", "中文", 2, 1, "", false},
		{"zero span", "中文", 0, 0, "", false},
		{"empty text", "", 0, 1, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := docir.Slice(tc.text, tc.start, tc.end)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("Slice(%q,%d,%d) = %q, %v; want %q, %v", tc.text, tc.start, tc.end, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestLocateAndBindPinned(t *testing.T) {
	ir := docir.Build("前言\n\n合同期限36个月")
	located := docir.Locate(ir, "36个月")
	if located.Status != docir.StatusLocated || located.Start != 8 || located.End != 12 || located.Space != docir.CoordinateSpace {
		t.Fatalf("located %+v", located)
	}
	if located.Page != nil || located.ElementID != "b-2" {
		t.Fatalf("located coordinates %+v", located)
	}
	if got, ok := docir.Slice(ir.NormalizedText, located.Start, located.End); !ok || got != located.Quote {
		t.Fatalf("slice %q, %v", got, ok)
	}

	first := docir.Locate(docir.Build("abc 定义"), "abc")
	if first.Status != docir.StatusLocated || first.Start != 0 || first.End != 3 {
		t.Fatalf("location at offset zero %+v", first)
	}

	for _, quote := range []string{"", "不存在的内容"} {
		miss := docir.Locate(ir, quote)
		if miss.Status != docir.StatusUnlocatable || miss.Reason == "" || miss.Page != nil || miss.Start != 0 || miss.End != 0 || miss.ElementID != "" || miss.Space != "" {
			t.Fatalf("miss %q kept a coordinate: %+v", quote, miss)
		}
	}

	for _, span := range [][2]int{{1, 3}, {-1, 2}, {0, 13}, {5, 4}} {
		miss := docir.Bind(ir, "前", span[0], span[1])
		if miss.Status != docir.StatusUnlocatable || miss.Page != nil || miss.Start != 0 || miss.End != 0 {
			t.Fatalf("bad bind %v kept a coordinate: %+v", span, miss)
		}
	}
	empty := docir.Bind(ir, "", 0, 1)
	if empty.Status != docir.StatusUnlocatable {
		t.Fatalf("empty quote located: %+v", empty)
	}

	bound := docir.Bind(ir, "前言", 0, 2)
	if bound.Status != docir.StatusLocated || bound.ElementID != "b-1" || bound.Start != 0 || bound.End != 2 {
		t.Fatalf("bound %+v", bound)
	}

	paged := docir.Build("封面\f正文")
	hit := docir.Bind(paged, "正文", 4, 6)
	if hit.Status != docir.StatusLocated || hit.Page == nil || *hit.Page != 2 || hit.ElementID != "b-2" {
		t.Fatalf("paged bind %+v", hit)
	}
}
