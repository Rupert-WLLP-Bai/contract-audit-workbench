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
		more, keep, err := docxTableChild(child, local)
		if err != nil {
			return nil, err
		}
		if keep {
			result = append(result, more...)
		}
	}
	return result, nil
}

func docxTableChild(child *docxNode, local string) ([]*docxNode, bool, error) {
	if child.word(local) {
		return []*docxNode{child}, true, nil
	}
	if docxTableIgnorable(child) {
		return nil, false, nil
	}
	if docxTableWrapper(child) {
		more, err := docxTableChildren(child, local)
		return more, true, err
	}
	if child.word("sdt") {
		content := child.child("sdtContent")
		if content == nil {
			return nil, false, nil
		}
		more, err := docxTableChildren(content, local)
		return more, true, err
	}
	return nil, false, fmt.Errorf("DOCX: unsupported table structure %s", child.name.Local)
}

func docxTableIgnorable(child *docxNode) bool {
	switch {
	case child.word("del"), child.word("moveFrom"), child.word("tblPr"), child.word("tblGrid"), child.word("trPr"), child.word("tblPrEx"), docxNonContent(child):
		return true
	default:
		return false
	}
}

func docxTableWrapper(child *docxNode) bool {
	return child.word("ins") || child.word("moveTo") || child.word("sdtContent") || child.word("customXml")
}

func (p *docxParser) table(node *docxNode, part string) (docxFragment, error) {
	p.tables++
	if p.tables > 1024 {
		return docxFragment{}, fmt.Errorf("DOCX: table count exceeds 1024")
	}
	tableID := "t-" + strconv.Itoa(p.tables)
	if err := p.validateTableStyle(node); err != nil {
		return docxFragment{}, err
	}
	grid, err := docxTableGrid(node)
	if err != nil {
		return docxFragment{}, err
	}
	rows, err := docxTableChildren(node, "tr")
	if err != nil {
		return docxFragment{}, err
	}
	return p.renderTableRows(rows, grid, tableID, part)
}

func (p *docxParser) validateTableStyle(node *docxNode) error {
	styleID := node.child("tblPr").child("tblStyle").attr("val")
	seen := make(map[string]bool)
	for styleID != "" {
		if seen[styleID] {
			return fmt.Errorf("DOCX: cyclic table style inheritance")
		}
		seen[styleID] = true
		style := p.styles.definitions[styleID]
		if style == nil {
			return fmt.Errorf("DOCX: missing referenced table style")
		}
		if docxHasVisibility(style) {
			return fmt.Errorf("DOCX: conditional table-style visibility is unsupported")
		}
		styleID = style.child("basedOn").attr("val")
	}
	return nil
}

func docxTableGrid(node *docxNode) (int, error) {
	grid := 0
	if columns := node.child("tblGrid"); columns != nil {
		for _, column := range columns.children {
			if column.word("gridCol") {
				grid++
			}
		}
	}
	if grid > 256 {
		return 0, fmt.Errorf("DOCX: table column limit exceeded")
	}
	return grid, nil
}

func (p *docxParser) renderTableRows(rows []*docxNode, grid int, tableID, part string) (docxFragment, error) {
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
		fragment, merges, err := p.renderTableRow(row, grid, tableID, part, rowNumber, previousMerges)
		if err != nil {
			return docxFragment{}, err
		}
		previousMerges = merges
		renderedRows = append(renderedRows, fragment)
	}
	return docxJoin(renderedRows, "\n")
}

func (p *docxParser) renderTableRow(row *docxNode, grid int, tableID, part string, rowNumber int, previous map[int]int) (docxFragment, map[int]int, error) {
	before, err := docxGridValue(row.child("trPr").child("gridBefore"), 0)
	if err != nil {
		return docxFragment{}, nil, err
	}
	cells, err := docxTableChildren(row, "tc")
	if err != nil {
		return docxFragment{}, nil, err
	}
	fragment, merges, col, err := p.renderTableCells(cells, grid, tableID, part, rowNumber, before+1, previous)
	if err != nil {
		return docxFragment{}, nil, err
	}
	after, err := docxGridValue(row.child("trPr").child("gridAfter"), 0)
	if err != nil {
		return docxFragment{}, nil, err
	}
	if grid != 0 && col-1+after != grid {
		return docxFragment{}, nil, fmt.Errorf("DOCX: row does not cover declared table grid")
	}
	return fragment, merges, nil
}

func (p *docxParser) renderTableCells(cells []*docxNode, grid int, tableID, part string, row, startCol int, previous map[int]int) (docxFragment, map[int]int, int, error) {
	var rendered []docxFragment
	merges := make(map[int]int)
	col := startCol
	for _, cell := range cells {
		fragment, span, include, err := p.renderTableCell(cell, grid, tableID, part, row, col, previous, merges)
		if err != nil {
			return docxFragment{}, nil, col, err
		}
		if include {
			rendered = append(rendered, fragment)
		}
		col += span
	}
	fragment, err := docxJoin(rendered, "\t")
	return fragment, merges, col, err
}

func (p *docxParser) renderTableCell(cell *docxNode, grid int, tableID, part string, row, col int, previous, merges map[int]int) (docxFragment, int, bool, error) {
	props, span, include, err := docxCellLayout(cell, col, grid)
	if err != nil || !include {
		return docxFragment{}, span, false, err
	}
	continuation, err := docxMergeState(props.child("vMerge"), col, span, previous, merges)
	if err != nil {
		return docxFragment{}, 0, false, err
	}
	fragment, err := p.tableCellText(cell, part)
	if err != nil {
		return docxFragment{}, 0, false, err
	}
	if continuation && fragment.text != "" {
		return docxFragment{}, 0, false, fmt.Errorf("DOCX: vertical merge continuation contains ambiguous visible content")
	}
	if fragment.text != "" {
		fragment.cells = append(fragment.cells, docir.Cell{ID: tableID + "-r" + strconv.Itoa(row) + "c" + strconv.Itoa(col), TableID: tableID, Row: row, Col: col, Text: fragment.text, Start: 0, End: utf8.RuneCountInString(fragment.text)})
	}
	return fragment, span, true, nil
}

func docxCellLayout(cell *docxNode, col, grid int) (*docxNode, int, bool, error) {
	props := cell.child("tcPr")
	span, err := docxGridValue(props.child("gridSpan"), 1)
	if err != nil || span == 0 {
		return nil, 0, false, fmt.Errorf("DOCX: invalid table cell span")
	}
	if err := validateCellGrid(col, span, grid); err != nil {
		return nil, 0, false, err
	}
	if props.child("hMerge") != nil {
		return nil, 0, false, fmt.Errorf("DOCX: legacy hMerge is unsupported; use gridSpan")
	}
	if props.child("cellDel") != nil {
		return props, span, false, nil
	}
	return props, span, true, nil
}

func validateCellGrid(col, span, grid int) error {
	if col+span-1 > 256 || (grid != 0 && col+span-1 > grid) {
		return fmt.Errorf("DOCX: cell exceeds declared table grid")
	}
	return nil
}

func docxMergeState(merge *docxNode, col, span int, previous, current map[int]int) (bool, error) {
	if merge == nil {
		return false, nil
	}
	switch merge.attr("val") {
	case "restart":
		current[col] = span
		return false, nil
	case "", "continue":
		if previous[col] != span {
			return false, fmt.Errorf("DOCX: vertical merge has no matching origin/grid span")
		}
		current[col] = span
		return true, nil
	default:
		return false, fmt.Errorf("DOCX: invalid vertical merge state")
	}
}

func (p *docxParser) tableCellText(cell *docxNode, part string) (docxFragment, error) {
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
	return docxJoin(parts, "\n")
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
