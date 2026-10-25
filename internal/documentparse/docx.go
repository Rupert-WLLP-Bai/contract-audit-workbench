package documentparse

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"contractaudit/internal/docir"
)

const docxMaxText = 8 << 20

type docxFragment struct {
	text  string
	cells []docir.Cell
}

type docxStory struct{ part, kind string }

type docxParser struct {
	pkg         *docxPackage
	ocr         OCR
	styles      docxStyles
	fromOCR     bool
	images      map[string]string
	imageCount  int
	tables      int
	stories     []docxStory
	seenStories map[string]bool
	fields      []bool
}

// ParseDOCX reads the current visible OOXML story, then each referenced header
// and footer once in first-reference order. Deleted/moved-from and hidden text
// are excluded. Cached field results are read without evaluating fields. DOCX
// pagination is a layout artifact, so no pages or geometric boxes are invented.
func ParseDOCX(ctx context.Context, body []byte, ocr OCR) (Result, error) {
	pkg, main, err := docxOpen(ctx, body)
	if err != nil {
		return Result{}, err
	}
	parser := &docxParser{pkg: pkg, ocr: ocr, images: make(map[string]string), seenStories: make(map[string]bool)}
	if err := parser.loadStyles(main); err != nil {
		return Result{}, err
	}
	document, err := pkg.xml(main)
	if err != nil {
		return Result{}, err
	}
	if !document.word("document") || document.child("body") == nil {
		return Result{}, fmt.Errorf("DOCX: invalid main document/body")
	}
	mainFragments, err := parser.blocks(document.child("body"), main)
	if err != nil {
		return Result{}, err
	}
	fragments := mainFragments
	if len(parser.fields) != 0 {
		return Result{}, fmt.Errorf("DOCX: main story has unterminated fields")
	}
	kinds := make([]string, len(fragments))
	for i := range kinds {
		kinds[i] = "paragraph"
	}
	for _, story := range parser.stories {
		root, err := pkg.xml(story.part)
		if err != nil {
			return Result{}, err
		}
		local := "hdr"
		if story.kind == "footer" {
			local = "ftr"
		}
		if !root.word(local) {
			return Result{}, fmt.Errorf("DOCX: invalid %s root", story.kind)
		}
		parts, err := parser.blocks(root, story.part)
		if err != nil {
			return Result{}, err
		}
		for _, part := range parts {
			fragments = append(fragments, part)
			kinds = append(kinds, story.kind)
		}
		if len(parser.fields) != 0 {
			return Result{}, fmt.Errorf("DOCX: header/footer has unterminated fields")
		}
	}
	var text strings.Builder
	cells := []docir.Cell{}
	offset := 0
	for _, fragment := range fragments {
		if text.Len() > 0 {
			text.WriteString("\n\n")
			offset += 2
		}
		for _, cell := range fragment.cells {
			cell.Start += offset
			cell.End += offset
			cells = append(cells, cell)
		}
		if text.Len()+len(fragment.text) > docxMaxText {
			return Result{}, fmt.Errorf("DOCX: extracted text exceeds 8 MiB limit")
		}
		text.WriteString(fragment.text)
		offset += utf8.RuneCountInString(fragment.text)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if text.Len() == 0 {
		return Result{}, fmt.Errorf("DOCX: document has no supported visible text")
	}
	ir := docir.Build(text.String())
	// OOXML table coordinates come from tc/gridSpan, never pipe heuristics.
	ir.Cells = cells
	ir.ExtractionVersion = DOCXVersion
	if len(ir.Blocks) != len(fragments) {
		return Result{}, fmt.Errorf("DOCX: inconsistent normalized block boundaries")
	}
	for i := range ir.Blocks {
		ir.Blocks[i].Kind = kinds[i]
		if len(fragments[i].cells) > 0 {
			ir.Blocks[i].Kind = "table"
		}
	}
	return Result{IR: ir, FromOCR: parser.fromOCR}, nil
}

func docxCleanText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\f", "\n")
	text = strings.TrimPrefix(text, "\uFEFF")
	// Single line separators preserve explicit breaks while paragraph boundaries
	// remain reserved for real OOXML paragraphs/whole tables in docir.Build.
	for strings.Contains(text, "\n\n") {
		text = strings.ReplaceAll(text, "\n\n", "\n")
	}
	return strings.TrimSpace(text)
}

func docxJoin(parts []docxFragment, delimiter string) (docxFragment, error) {
	var out strings.Builder
	var cells []docir.Cell
	offset := 0
	for _, part := range parts {
		if part.text == "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteString(delimiter)
			offset += utf8.RuneCountInString(delimiter)
		}
		if out.Len()+len(part.text) > docxMaxText {
			return docxFragment{}, fmt.Errorf("DOCX: extracted text exceeds 8 MiB limit")
		}
		for _, cell := range part.cells {
			cell.Start += offset
			cell.End += offset
			cells = append(cells, cell)
		}
		out.WriteString(part.text)
		offset += utf8.RuneCountInString(part.text)
	}
	return docxFragment{text: out.String(), cells: cells}, nil
}

func (p *docxParser) blocks(node *docxNode, part string) ([]docxFragment, error) {
	var out []docxFragment
	for _, child := range node.children {
		if err := p.pkg.ctx.Err(); err != nil {
			return nil, err
		}
		switch {
		case child.word("p"):
			text, err := p.paragraph(child, part)
			if err != nil {
				return nil, err
			}
			if text != "" {
				out = append(out, docxFragment{text: text})
			}
		case child.word("tbl"):
			fragment, err := p.table(child, part)
			if err != nil {
				return nil, err
			}
			if fragment.text != "" {
				out = append(out, fragment)
			}
		case child.word("sectPr"):
			if err := p.section(child, part); err != nil {
				return nil, err
			}
		case child.word("del"), child.word("moveFrom"):
			continue
		case child.word("ins"), child.word("moveTo"), child.word("sdtContent"), child.word("customXml"):
			parts, err := p.blocks(child, part)
			if err != nil {
				return nil, err
			}
			out = append(out, parts...)
		case child.word("sdt"):
			if content := child.child("sdtContent"); content != nil {
				parts, err := p.blocks(content, part)
				if err != nil {
					return nil, err
				}
				out = append(out, parts...)
			}
		case docxNonContent(child):
			continue
		default:
			return nil, fmt.Errorf("DOCX: unsupported block %s", child.name.Local)
		}
	}
	return out, nil
}

func (p *docxParser) section(node *docxNode, part string) error {
	for _, child := range node.children {
		kind := ""
		if child.word("headerReference") {
			kind = "header"
		}
		if child.word("footerReference") {
			kind = "footer"
		}
		if kind == "" {
			continue
		}
		rel, ok := p.pkg.rels[part][child.relID("id")]
		if !ok || !docxRelKind(rel.kind, kind) {
			return fmt.Errorf("DOCX: missing or invalid %s relationship", kind)
		}
		if !p.seenStories[rel.target] {
			p.seenStories[rel.target] = true
			p.stories = append(p.stories, docxStory{part: rel.target, kind: kind})
		}
	}
	return nil
}

func docxNonContent(n *docxNode) bool {
	if n.name.Space != docxWNS && n.name.Space != docxWStrictNS {
		return false
	}
	switch n.name.Local {
	case "bookmarkStart", "bookmarkEnd", "commentRangeStart", "commentRangeEnd", "commentReference", "proofErr", "permStart", "permEnd", "moveFromRangeStart", "moveFromRangeEnd", "moveToRangeStart", "moveToRangeEnd", "customXmlInsRangeStart", "customXmlInsRangeEnd", "customXmlDelRangeStart", "customXmlDelRangeEnd":
		return true
	}
	return false
}
