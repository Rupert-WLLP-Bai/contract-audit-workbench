package documentparse

import (
	"fmt"
	"strconv"
	"unicode/utf8"

	"contractaudit/internal/docir"
)

func docxGridValue(node *docxNode, fallback int) (int, error) {
	if node == nil {
		return fallback, nil
	}
	value, err := strconv.Atoi(node.attr("val"))
	if err != nil || value < 0 || value > 256 {
		return 0, fmt.Errorf("DOCX: invalid or over-limit table grid value")
	}
	return value, nil
}

func docxTableChildren(node *docxNode, local string) ([]*docxNode, error) {
	var result []*docxNode
	for _, child := range node.children {
		switch {
		case child.word(local):
			result = append(result, child)
		case child.word("del"), child.word("moveFrom"), child.word("tblPr"), child.word("tblGrid"), child.word("trPr"), child.word("tblPrEx"), docxNonContent(child):
			continue
		case child.word("ins"), child.word("moveTo"), child.word("sdtContent"), child.word("customXml"):
			more, err := docxTableChildren(child, local)
			if err != nil {
				return nil, err
			}
			result = append(result, more...)
		case child.word("sdt"):
			if content := child.child("sdtContent"); content != nil {
				more, err := docxTableChildren(content, local)
				if err != nil {
					return nil, err
				}
				result = append(result, more...)
			}
		default:
			return nil, fmt.Errorf("DOCX: unsupported table structure %s", child.name.Local)
		}
	}
	return result, nil
}

func (p *docxParser) table(node *docxNode, part string) (docxFragment, error) {
	p.tables++
	if p.tables > 1024 {
		return docxFragment{}, fmt.Errorf("DOCX: table count exceeds 1024")
	}
	tableID := "t-" + strconv.Itoa(p.tables)
	// Conditional table-style visibility needs Word's layout cascade. Reject it
	// explicitly rather than presenting potentially hidden contract text.
	styleID := node.child("tblPr").child("tblStyle").attr("val")
	seen := make(map[string]bool)
	for styleID != "" {
		if seen[styleID] {
			return docxFragment{}, fmt.Errorf("DOCX: cyclic table style inheritance")
		}
		seen[styleID] = true
		style := p.styles.definitions[styleID]
		if style == nil {
			return docxFragment{}, fmt.Errorf("DOCX: missing referenced table style")
		}
		if docxHasVisibility(style) {
			return docxFragment{}, fmt.Errorf("DOCX: conditional table-style visibility is unsupported")
		}
		styleID = style.child("basedOn").attr("val")
	}
	grid := 0
	if columns := node.child("tblGrid"); columns != nil {
		for _, column := range columns.children {
			if column.word("gridCol") {
				grid++
			}
		}
	}
	if grid > 256 {
		return docxFragment{}, fmt.Errorf("DOCX: table column limit exceeded")
	}
	rows, err := docxTableChildren(node, "tr")
	if err != nil {
		return docxFragment{}, err
	}
	var renderedRows []docxFragment
	previousMerges := make(map[int]int)
	rowNumber := 0
	for _, row := range rows {
		if err := p.pkg.ctx.Err(); err != nil {
			return docxFragment{}, err
		}
		if row.child("trPr").child("del") != nil {
			continue
		}
		rowNumber++
		if rowNumber > 10000 {
			return docxFragment{}, fmt.Errorf("DOCX: table row limit exceeded")
		}
		before, err := docxGridValue(row.child("trPr").child("gridBefore"), 0)
		if err != nil {
			return docxFragment{}, err
		}
		col := before + 1
		cells, err := docxTableChildren(row, "tc")
		if err != nil {
			return docxFragment{}, err
		}
		var renderedCells []docxFragment
		merges := make(map[int]int)
		for _, cell := range cells {
			props := cell.child("tcPr")
			span, err := docxGridValue(props.child("gridSpan"), 1)
			if err != nil || span == 0 {
				return docxFragment{}, fmt.Errorf("DOCX: invalid table cell span")
			}
			if col+span-1 > 256 || (grid != 0 && col+span-1 > grid) {
				return docxFragment{}, fmt.Errorf("DOCX: cell exceeds declared table grid")
			}
			if props.child("hMerge") != nil {
				return docxFragment{}, fmt.Errorf("DOCX: legacy hMerge is unsupported; use gridSpan")
			}
			if props.child("cellDel") != nil {
				col += span
				continue
			}
			merge := props.child("vMerge")
			continuation := false
			if merge != nil {
				switch merge.attr("val") {
				case "restart":
					merges[col] = span
				case "", "continue":
					if previousMerges[col] != span {
						return docxFragment{}, fmt.Errorf("DOCX: vertical merge has no matching origin/grid span")
					}
					merges[col] = span
					continuation = true
				default:
					return docxFragment{}, fmt.Errorf("DOCX: invalid vertical merge state")
				}
			}
			content := &docxNode{}
			for _, child := range cell.children {
				if !child.word("tcPr") {
					content.children = append(content.children, child)
				}
			}
			parts, err := p.blocks(content, part)
			if err != nil {
				return docxFragment{}, err
			}
			fragment, err := docxJoin(parts, "\n")
			if err != nil {
				return docxFragment{}, err
			}
			if continuation && fragment.text != "" {
				return docxFragment{}, fmt.Errorf("DOCX: vertical merge continuation contains ambiguous visible content")
			}
			if fragment.text != "" {
				fragment.cells = append(fragment.cells, docir.Cell{ID: tableID + "-r" + strconv.Itoa(rowNumber) + "c" + strconv.Itoa(col), TableID: tableID, Row: rowNumber, Col: col, Text: fragment.text, Start: 0, End: utf8.RuneCountInString(fragment.text)})
			}
			renderedCells = append(renderedCells, fragment)
			col += span
		}
		after, err := docxGridValue(row.child("trPr").child("gridAfter"), 0)
		if err != nil {
			return docxFragment{}, err
		}
		if grid != 0 && col-1+after != grid {
			return docxFragment{}, fmt.Errorf("DOCX: row does not cover declared table grid")
		}
		previousMerges = merges
		fragment, err := docxJoin(renderedCells, "\t")
		if err != nil {
			return docxFragment{}, err
		}
		renderedRows = append(renderedRows, fragment)
	}
	return docxJoin(renderedRows, "\n")
}

func docxHasVisibility(node *docxNode) bool {
	if node.word("vanish") || node.word("specVanish") {
		return true
	}
	for _, child := range node.children {
		if docxHasVisibility(child) {
			return true
		}
	}
	return false
}
