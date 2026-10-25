package docir_test

import (
	"testing"

	"contractaudit/internal/docir"
	"contractaudit/internal/docversion"
)

func TestBuildCodePointOffsets(t *testing.T) {
	ir := docir.Build("第一段。\n\n第二段。")
	if len(ir.Blocks) != 2 {
		t.Fatalf("blocks %d", len(ir.Blocks))
	}
	if ir.Blocks[0].ID != "b-1" || ir.Blocks[0].Start != 0 || ir.Blocks[0].End != 4 {
		t.Fatalf("first %+v", ir.Blocks[0])
	}
	if ir.Blocks[1].Start != 6 || ir.NormalizedText != "第一段。\n\n第二段。" {
		t.Fatalf("second %+v text %q", ir.Blocks[1], ir.NormalizedText)
	}
	if ir.CoordinateSpace != docir.CoordinateSpace {
		t.Fatal(ir.CoordinateSpace)
	}
}

func TestNormalizedDigestIgnoresOriginalPadding(t *testing.T) {
	raw := "合同正文   "
	ir := docir.Build(raw)
	if ir.NormalizedText != "合同正文" {
		t.Fatalf("normalized %q", ir.NormalizedText)
	}
	if ir.NormalizedDigest == docversion.Hash([]byte(raw)).String() {
		t.Fatal("normalized digest must differ from the original bytes")
	}
	if ir.NormalizedDigest != docversion.Hash([]byte("合同正文")).String() {
		t.Fatal(ir.NormalizedDigest)
	}
}

func TestSliceRejectsBadSpan(t *testing.T) {
	if _, ok := docir.Slice("中", 0, 2); ok {
		t.Fatal("one code point cannot end at 2")
	}
	got, ok := docir.Slice("中文", 0, 1)
	if !ok || got != "中" {
		t.Fatalf("%q %v", got, ok)
	}
}

func TestFormFeedPageAndClause(t *testing.T) {
	ir := docir.Build("封面未列期限。\f第二条 质保期为36个月。")
	if len(ir.Pages) != 2 || ir.Pages[1].Number != 2 {
		t.Fatalf("pages %+v", ir.Pages)
	}
	if len(ir.Blocks) != 2 || ir.Blocks[0].Page == nil || *ir.Blocks[0].Page != 1 || ir.Blocks[1].Page == nil || *ir.Blocks[1].Page != 2 {
		t.Fatalf("blocks %+v", ir.Blocks)
	}
	if len(ir.Clauses) != 1 || ir.Clauses[0].Heading != "第二条 质保期为36个月。" || ir.Clauses[0].Page == nil || *ir.Clauses[0].Page != 2 {
		t.Fatalf("clauses %+v", ir.Clauses)
	}
	located := docir.Locate(ir, "质保期为36个月")
	if located.Status != docir.StatusLocated || located.Page == nil || *located.Page != 2 {
		t.Fatalf("located %+v", located)
	}
	got, ok := docir.Slice(ir.NormalizedText, located.Start, located.End)
	if !ok || got != located.Quote {
		t.Fatalf("slice %q", got)
	}
	miss := docir.Bind(ir, "质保99个月", located.Start, located.End)
	if miss.Status != docir.StatusUnlocatable || miss.Page != nil || miss.Start != 0 || miss.End != 0 || miss.ElementID != "" || miss.Reason == "" {
		t.Fatalf("mismatch kept a coordinate: %+v", miss)
	}
}

func TestTableCellIsTheTightSpan(t *testing.T) {
	ir := docir.Build("| 项目 | 期限 |\n| --- | --- |\n| 质保期 | 36个月 |")
	if len(ir.Blocks) != 1 || ir.Blocks[0].Kind != "table" {
		t.Fatalf("block %+v", ir.Blocks)
	}
	var cell docir.Cell
	found := false
	for _, item := range ir.Cells {
		if item.Text == "36个月" {
			cell = item
			found = true
		}
	}
	if !found {
		t.Fatalf("cells %+v", ir.Cells)
	}
	got, ok := docir.Slice(ir.NormalizedText, cell.Start, cell.End)
	if !ok || got != "36个月" {
		t.Fatalf("cell slice %q", got)
	}
	if ir.ElementAt(cell.Start, cell.End) != cell.ID {
		t.Fatalf("element %s", ir.ElementAt(cell.Start, cell.End))
	}
	if _, ok := ir.PageAt(cell.Start, cell.End); ok {
		t.Fatal("a table in unpaged text invented a page")
	}
}

func TestUnpagedTextHasNoPage(t *testing.T) {
	ir := docir.Build("采购文件要求质保36个月。")
	if len(ir.Pages) != 0 {
		t.Fatalf("pages %+v", ir.Pages)
	}
	located := docir.Locate(ir, "质保36个月")
	if located.Status != docir.StatusLocated || located.Page != nil {
		t.Fatalf("%+v", located)
	}
}
