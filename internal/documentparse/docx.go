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
	fields      []docxField
}

// ParseDOCX reads the current visible OOXML story, then each referenced header
// and footer once in first-reference order. Deleted/moved-from and hidden text
// are excluded. One markup-compatibility branch is read, so an alternative
// representation is never duplicated; text boxes drawn by shapes contribute
// their visible paragraphs. Cached field results are read without evaluating
// fields, except page and section numbers, which follow layout. DOCX pagination
// is a layout artifact, so no pages or geometric boxes are invented.
func ParseDOCX(ctx context.Context, body []byte, ocr OCR) (Result, error) {
	pkg, main, err := docxOpen(ctx, body)
	if err != nil {
		return Result{}, err
	}
	parser := &docxParser{pkg: pkg, ocr: ocr, images: make(map[string]string), seenStories: make(map[string]bool)}
	if err := parser.loadStyles(main); err != nil {
		return Result{}, err
	}
	fragments, kinds, err := parser.documentStories(main)
	if err != nil {
		return Result{}, err
	}
	return parser.result(ctx, fragments, kinds)
}

func (p *docxParser) documentStories(main string) ([]docxFragment, []string, error) {
	fragments, err := p.mainStoryBlocks(main)
	if err != nil {
		return nil, nil, err
	}
	kinds := docxBlockKinds(len(fragments), "paragraph")
	for _, story := range p.stories {
		parts, err := p.storyBlocks(story)
		if err != nil {
			return nil, nil, err
		}
		fragments = append(fragments, parts...)
		kinds = append(kinds, docxBlockKinds(len(parts), story.kind)...)
	}
	return fragments, kinds, nil
}

func (p *docxParser) mainStoryBlocks(main string) ([]docxFragment, error) {
	document, err := p.pkg.xml(main)
	if err != nil {
		return nil, err
	}
	if !document.word("document") || document.child("body") == nil {
		return nil, fmt.Errorf("DOCX: invalid main document/body")
	}
	fragments, err := p.blocks(document.child("body"), main)
	if err != nil {
		return nil, err
	}
	if len(p.fields) != 0 {
		return nil, fmt.Errorf("DOCX: main story has unterminated fields")
	}
	return fragments, nil
}

func docxBlockKinds(count int, kind string) []string {
	kinds := make([]string, count)
	for i := range kinds {
		kinds[i] = kind
	}
	return kinds
}

func (p *docxParser) storyBlocks(story docxStory) ([]docxFragment, error) {
	root, err := p.pkg.xml(story.part)
	if err != nil {
		return nil, err
	}
	local := "hdr"
	if story.kind == "footer" {
		local = "ftr"
	}
	if !root.word(local) {
		return nil, fmt.Errorf("DOCX: invalid %s root", story.kind)
	}
	parts, err := p.blocks(root, story.part)
	if err != nil {
		return nil, err
	}
	if len(p.fields) != 0 {
		return nil, fmt.Errorf("DOCX: header/footer has unterminated fields")
	}
	return parts, nil
}

func (p *docxParser) result(ctx context.Context, fragments []docxFragment, kinds []string) (Result, error) {
	text, cells, err := docxStoryText(fragments)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if text == "" {
		return Result{}, fmt.Errorf("DOCX: document has no supported visible text")
	}
	ir := docir.Build(text)
	ir.Cells = cells
	ir.ExtractionVersion = DOCXVersion
	if err := docxBlockMetadata(&ir, fragments, kinds); err != nil {
		return Result{}, err
	}
	return Result{IR: ir, FromOCR: p.fromOCR}, nil
}

func docxStoryText(fragments []docxFragment) (string, []docir.Cell, error) {
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
			return "", nil, fmt.Errorf("DOCX: extracted text exceeds 8 MiB limit")
		}
		text.WriteString(fragment.text)
		offset += utf8.RuneCountInString(fragment.text)
	}
	return text.String(), cells, nil
}

func docxBlockMetadata(ir *docir.IR, fragments []docxFragment, kinds []string) error {
	if len(ir.Blocks) != len(fragments) {
		return fmt.Errorf("DOCX: inconsistent normalized block boundaries")
	}
	for i := range ir.Blocks {
		ir.Blocks[i].Kind = kinds[i]
		if len(fragments[i].cells) > 0 {
			ir.Blocks[i].Kind = "table"
		}
	}
	return nil
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
		parts, err := p.block(child, part)
		if err != nil {
			return nil, err
		}
		out = append(out, parts...)
	}
	return out, nil
}

func (p *docxParser) block(child *docxNode, part string) ([]docxFragment, error) {
	switch docxBlockKind(child) {
	case "paragraph":
		return p.paragraphBlock(child, part)
	case "table":
		return p.tableBlock(child, part)
	case "section":
		return nil, p.section(child, part)
	case "ignored":
		return nil, nil
	case "wrapper":
		return p.blocks(child, part)
	case "sdt":
		return p.blockSDT(child, part)
	case "alternate":
		branch, err := docxAlternateBranch(child)
		if err != nil {
			return nil, err
		}
		return p.blocks(branch, part)
	default:
		return nil, fmt.Errorf("DOCX: unsupported block %s", child.name.Local)
	}
}

func (p *docxParser) paragraphBlock(child *docxNode, part string) ([]docxFragment, error) {
	text, err := p.paragraph(child, part)
	if err != nil || text == "" {
		return nil, err
	}
	return []docxFragment{{text: text}}, nil
}

func (p *docxParser) tableBlock(child *docxNode, part string) ([]docxFragment, error) {
	fragment, err := p.table(child, part)
	if err != nil || fragment.text == "" {
		return nil, err
	}
	return []docxFragment{fragment}, nil
}

func docxBlockKind(child *docxNode) string {
	switch {
	case child.word("p"):
		return "paragraph"
	case child.word("tbl"):
		return "table"
	case child.word("sectPr"):
		return "section"
	case docxBlockIgnored(child):
		return "ignored"
	case docxBlockWrapper(child):
		return "wrapper"
	case child.word("sdt"):
		return "sdt"
	case child.name.Space == docxMCNS && child.name.Local == "AlternateContent":
		return "alternate"
	default:
		return "unsupported"
	}
}

func (p *docxParser) blockSDT(child *docxNode, part string) ([]docxFragment, error) {
	if content := child.child("sdtContent"); content != nil {
		return p.blocks(content, part)
	}
	return nil, nil
}

func docxBlockIgnored(child *docxNode) bool {
	return child.word("del") || child.word("moveFrom") || docxNonContent(child)
}

func docxBlockWrapper(child *docxNode) bool {
	return child.word("ins") || child.word("moveTo") || child.word("sdtContent") || child.word("customXml")
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
