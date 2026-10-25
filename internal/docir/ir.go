// Package docir builds a structured text view of a UTF-8 file.
// Offsets are Unicode code points into NormalizedText. The digest covers that
// text, not the original file bytes. A form feed is an observed page break and
// is left out of the normalized text; a file with no form feed has no page number.
package docir

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"contractaudit/internal/docversion"
)

const (
	CoordinateSpace      = "unicode_codepoint"
	ExtractionVersion    = "text-structure-v2"
	NormalizationVersion = "text-normalization-v1"

	StatusLocated     = "located"
	StatusUnlocatable = "unlocatable"
)

var clauseHead = regexp.MustCompile(`^第(?:[0-9]{1,3}|[一二三四五六七八九十百千零]{1,4})条`)

// Block is one non-empty paragraph in normalized text.
type Block struct {
	ID    string
	Kind  string
	Text  string
	Start int
	End   int
	Page  *int
}

// Page is a form-feed page. Number is the 1-based break index, not a guess.
type Page struct {
	Number int
	Start  int
	End    int
}

// Cell is one markdown-table cell. Its span is inside the parent block.
type Cell struct {
	ID      string
	TableID string
	Row     int
	Col     int
	Text    string
	Start   int
	End     int
	Page    *int
}

// Clause runs from a 第N条 opener through the start of the next opener.
type Clause struct {
	ID      string
	Heading string
	Start   int
	End     int
	Page    *int
}

// IR is the parse product for a text file.
type IR struct {
	CoordinateSpace   string
	NormalizedText    string
	NormalizedDigest  string
	ExtractionVersion string
	Pages             []Page
	Blocks            []Block
	Cells             []Cell
	Clauses           []Clause
}

// QuoteAnchor is a quote checked against one code-point span.
// StatusLocated means the slice equals Quote. Anything else has no page,
// element, or offsets. This text IR has no page image, so it never carries a box.
type QuoteAnchor struct {
	Status    string
	Reason    string
	Quote     string
	Space     string
	ElementID string
	Page      *int
	Start     int
	End       int
}

// Build splits form-feed pages and blank-line paragraphs. Each block is trimmed.
// Blocks are joined with a blank line. A leading UTF-8 BOM is removed from the
// normalized text only.
func Build(raw string) IR {
	raw = strings.TrimPrefix(raw, "\uFEFF")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	segments := strings.Split(raw, "\f")
	paged := len(segments) > 1

	var blocks []Block
	var pages []Page
	var b strings.Builder
	offset := 0
	for i, segment := range segments {
		var page *int
		if paged {
			page = intPtr(i + 1)
		}
		pageStart := offset
		wrote := false
		for _, part := range strings.Split(segment, "\n\n") {
			text := strings.TrimSpace(part)
			if text == "" {
				continue
			}
			if len(blocks) > 0 {
				b.WriteString("\n\n")
				offset += 2
				if !wrote {
					pageStart = offset
				}
			}
			b.WriteString(text)
			n := utf8.RuneCountInString(text)
			blocks = append(blocks, Block{
				ID:    "b-" + itoa(len(blocks)+1),
				Kind:  "paragraph",
				Text:  text,
				Start: offset,
				End:   offset + n,
				Page:  page,
			})
			offset += n
			wrote = true
		}
		if paged && wrote {
			pages = append(pages, Page{Number: i + 1, Start: pageStart, End: offset})
		}
	}
	normalized := b.String()
	var cells []Cell
	tables := 0
	for i := range blocks {
		got := cellsIn(blocks[i], &tables)
		if len(got) == 0 {
			continue
		}
		blocks[i].Kind = "table"
		cells = append(cells, got...)
	}
	if pages == nil {
		pages = []Page{}
	}
	if cells == nil {
		cells = []Cell{}
	}
	return IR{
		CoordinateSpace:   CoordinateSpace,
		NormalizedText:    normalized,
		NormalizedDigest:  docversion.Hash([]byte(normalized)).String(),
		ExtractionVersion: ExtractionVersion,
		Pages:             pages,
		Blocks:            blocks,
		Cells:             cells,
		Clauses:           clausesOf(normalized, blocks),
	}
}

// Bind accepts the span only when its code-point slice is quote.
// A mismatch returns unlocatable and drops the page, element, and offsets.
func Bind(ir IR, quote string, start, end int) QuoteAnchor {
	got, ok := Slice(ir.NormalizedText, start, end)
	if quote == "" || !ok || got != quote {
		return unlocatable(quote, "引用与坐标切片不一致，不能编造页码或框。")
	}
	page, element := ir.cover(start, end)
	return QuoteAnchor{
		Status: StatusLocated, Quote: quote, Space: CoordinateSpace,
		ElementID: element, Page: page, Start: start, End: end,
	}
}

// Locate finds quote as an exact substring. A miss has no coordinate.
func Locate(ir IR, quote string) QuoteAnchor {
	if quote == "" {
		return unlocatable(quote, "引用为空，不能编造坐标。")
	}
	byteAt := strings.Index(ir.NormalizedText, quote)
	if byteAt < 0 {
		return unlocatable(quote, "引用在正文中不存在，不能编造页码或框。")
	}
	start := utf8.RuneCountInString(ir.NormalizedText[:byteAt])
	end := start + utf8.RuneCountInString(quote)
	return Bind(ir, quote, start, end)
}

func unlocatable(quote, reason string) QuoteAnchor {
	return QuoteAnchor{Status: StatusUnlocatable, Reason: reason, Quote: quote}
}

// PageAt is the observed page containing the span.
func (ir IR) PageAt(start, end int) (int, bool) {
	page, _ := ir.cover(start, end)
	if page == nil {
		return 0, false
	}
	return *page, true
}

// ElementAt is the tightest cell, block, or clause that contains the span.
func (ir IR) ElementAt(start, end int) string {
	_, id := ir.cover(start, end)
	return id
}

func (ir IR) cover(start, end int) (*int, string) {
	for _, cell := range ir.Cells {
		if start >= cell.Start && end <= cell.End {
			return cell.Page, cell.ID
		}
	}
	for _, block := range ir.Blocks {
		if start >= block.Start && end <= block.End {
			return block.Page, block.ID
		}
	}
	for _, clause := range ir.Clauses {
		if start >= clause.Start && end <= clause.End {
			return clause.Page, clause.ID
		}
	}
	return nil, ""
}

// Slice returns text[start:end] in Unicode code points.
func Slice(text string, start, end int) (string, bool) {
	rs := []rune(text)
	if start < 0 || end <= start || end > len(rs) {
		return "", false
	}
	return string(rs[start:end]), true
}

func clausesOf(text string, blocks []Block) []Clause {
	var heads []int
	for i, block := range blocks {
		if clauseHead.MatchString(block.Text) {
			heads = append(heads, i)
		}
	}
	if len(heads) == 0 {
		return []Clause{}
	}
	total := utf8.RuneCountInString(text)
	out := make([]Clause, 0, len(heads))
	for n, i := range heads {
		end := total
		if n+1 < len(heads) {
			end = blocks[heads[n+1]].Start
		}
		line, _, _ := strings.Cut(blocks[i].Text, "\n")
		out = append(out, Clause{
			ID:      "k-" + itoa(n+1),
			Heading: strings.TrimSpace(line),
			Start:   blocks[i].Start,
			End:     end,
			Page:    blocks[i].Page,
		})
	}
	return out
}

func cellsIn(block Block, tableN *int) []Cell {
	lines := strings.Split(block.Text, "\n")
	dataRows := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.Contains(trimmed, "|") {
			return nil
		}
		if !isRule(trimmed) {
			dataRows++
		}
	}
	if dataRows < 2 {
		return nil
	}
	*tableN++
	tableID := "t-" + itoa(*tableN)
	var cells []Cell
	row := 0
	linePos := 0
	for li, line := range lines {
		if !isRule(line) {
			parts := splitRow(line)
			if len(parts) > 0 {
				row++
				search := 0
				runes := []rune(line)
				for col, raw := range parts {
					text := strings.TrimSpace(raw)
					if text == "" {
						continue
					}
					rel := indexRunes(runes, []rune(text), search)
					if rel < 0 {
						continue
					}
					start := block.Start + linePos + rel
					end := start + utf8.RuneCountInString(text)
					cells = append(cells, Cell{
						ID: tableID + "-r" + itoa(row) + "c" + itoa(col+1), TableID: tableID,
						Row: row, Col: col + 1, Text: text, Start: start, End: end, Page: block.Page,
					})
					search = rel + utf8.RuneCountInString(text)
				}
			}
		}
		linePos += utf8.RuneCountInString(line)
		if li < len(lines)-1 {
			linePos++
		}
	}
	if len(cells) < 2 {
		*tableN--
		return nil
	}
	return cells
}

func isRule(line string) bool {
	if !strings.Contains(line, "|") || !strings.Contains(line, "-") {
		return false
	}
	parts := splitRow(line)
	if len(parts) == 0 {
		return false
	}
	saw := false
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		saw = true
		for _, r := range part {
			if r != '-' && r != ':' {
				return false
			}
		}
	}
	return saw
}

func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.Contains(line, "|") {
		return nil
	}
	line = strings.Trim(line, "|")
	return strings.Split(line, "|")
}

func indexRunes(hay, needle []rune, from int) int {
	if len(needle) == 0 || from > len(hay) {
		return -1
	}
	for i := from; i+len(needle) <= len(hay); i++ {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func intPtr(n int) *int {
	v := n
	return &v
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
