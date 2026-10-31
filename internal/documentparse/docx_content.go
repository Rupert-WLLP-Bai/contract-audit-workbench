package documentparse

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"unicode/utf8"
)

func (p *docxParser) paragraph(node *docxNode, part string) (string, error) {
	props := node.child("pPr")
	if section := props.child("sectPr"); section != nil {
		if err := p.section(section, part); err != nil {
			return "", err
		}
	}
	style := props.child("pStyle").attr("val")
	if style == "" {
		style = p.styles.paragraph
	}
	visibility, err := p.styles.apply(p.styles.defaults, style)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	if err := p.inline(node, part, visibility, &text); err != nil {
		return "", err
	}
	return docxCleanText(text.String()), nil
}

func (p *docxParser) inline(node *docxNode, part string, visibility docxVisibility, text *strings.Builder) error {
	if err := p.pkg.ctx.Err(); err != nil {
		return err
	}
	if docxInlineIgnored(node) {
		return nil
	}
	var err error
	visibility, err = p.inlineRunVisibility(node, visibility)
	if err != nil {
		return err
	}
	if node.word("fldChar") {
		return p.fieldCharacter(node)
	}
	return p.inlineContent(node, part, visibility, text)
}

func (p *docxParser) inlineContent(node *docxNode, part string, visibility docxVisibility, text *strings.Builder) error {
	visible := p.inlineVisible(visibility)
	if handled, err := p.inlineLeaf(node, part, visible, text); handled {
		return err
	}
	return p.inlineContainerContent(node, part, visibility, visible, text)
}

func (p *docxParser) inlineContainerContent(node *docxNode, part string, visibility docxVisibility, visible bool, text *strings.Builder) error {
	if err := p.inlineRelationship(node, part); err != nil {
		return err
	}
	if handled, err := p.inlineSpecialContainer(node, part, visibility, visible, text); handled {
		return err
	}
	if !p.inlineContainer(node) {
		if !visible {
			return nil
		}
		return fmt.Errorf("DOCX: unsupported visible inline content %s", node.name.Local)
	}
	return p.inlineChildren(node, part, visibility, text)
}

func (p *docxParser) inlineSpecialContainer(node *docxNode, part string, visibility docxVisibility, visible bool, text *strings.Builder) (bool, error) {
	if node.word("footnoteReference") || node.word("endnoteReference") {
		return true, docxFootnoteVisibility(visible)
	}
	if node.word("sdt") {
		return true, p.inlineSDT(node, part, visibility, text)
	}
	if node.word("fldSimple") {
		return true, p.simpleField(node, part, visibility, text)
	}
	if node.name.Space == docxMCNS && node.name.Local == "AlternateContent" {
		branch, err := docxAlternateBranch(node)
		if err != nil {
			return true, err
		}
		return true, p.inlineChildren(branch, part, visibility, text)
	}
	return false, nil
}

// simpleField reads the cached result of a w:fldSimple field. Pagination
// instructions only cache a page or section number, which is a layout artifact
// and never contract text.
func (p *docxParser) simpleField(node *docxNode, part string, visibility docxVisibility, text *strings.Builder) error {
	instruction := strings.TrimSpace(node.attr("instr"))
	if instruction == "" {
		return fmt.Errorf("DOCX: simple field has no instruction")
	}
	if docxPaginationField(instruction) {
		return nil
	}
	return p.inlineChildren(node, part, visibility, text)
}

// docxAlternateBranch returns the single branch a markup-compatibility consumer
// renders: the first mc:Choice whose Requires are all understood, else
// mc:Fallback. Reading exactly one branch keeps equivalent alternatives from
// being duplicated or merged.
func docxAlternateBranch(node *docxNode) (*docxNode, error) {
	var chosen, fallback *docxNode
	for _, child := range node.children {
		if child.name.Space != docxMCNS {
			return nil, fmt.Errorf("DOCX: AlternateContent entry %s is not markup compatibility", child.name.Local)
		}
		switch child.name.Local {
		case "Choice":
			if fallback != nil {
				return nil, fmt.Errorf("DOCX: AlternateContent choice after fallback")
			}
			if chosen != nil {
				continue
			}
			supported, err := docxChoiceSupported(child)
			if err != nil {
				return nil, err
			}
			if supported {
				chosen = child
			}
		case "Fallback":
			if fallback != nil {
				return nil, fmt.Errorf("DOCX: duplicate AlternateContent fallback")
			}
			fallback = child
		default:
			return nil, fmt.Errorf("DOCX: unsupported AlternateContent entry %s", child.name.Local)
		}
	}
	if chosen == nil {
		chosen = fallback
	}
	if chosen == nil {
		return nil, fmt.Errorf("DOCX: AlternateContent has no supported branch")
	}
	return chosen, nil
}

// docxChoiceSupported resolves every prefix a mc:Choice requires against the
// namespaces actually in scope and reports whether this parser implements all of
// them. Only the wordprocessingShape namespace is understood, so an alias bound
// to it is supported while that spelling bound to anything else is not. A choice
// without Requires, or one naming an undeclared prefix, is invalid markup rather
// than an alternative this parser may silently skip.
func docxChoiceSupported(node *docxNode) (bool, error) {
	requires := strings.TrimSpace(node.attr("Requires"))
	if requires == "" {
		return false, fmt.Errorf("DOCX: AlternateContent choice has no Requires")
	}
	for _, prefix := range strings.Fields(requires) {
		uri, bound := node.namespaces[prefix]
		if !bound || uri == "" {
			return false, fmt.Errorf("DOCX: AlternateContent requires undeclared prefix %q", prefix)
		}
		if uri != docxWPSNS {
			return false, nil
		}
	}
	return true, nil
}

func (p *docxParser) inlineChildren(node *docxNode, part string, visibility docxVisibility, text *strings.Builder) error {
	for _, child := range node.children {
		if err := p.inline(child, part, visibility, text); err != nil {
			return err
		}
	}
	return nil
}

func docxFootnoteVisibility(visible bool) error {
	if visible {
		return fmt.Errorf("DOCX: visible footnote/endnote references are unsupported; note content cannot be omitted")
	}
	return nil
}

func (p *docxParser) inlineSDT(node *docxNode, part string, visibility docxVisibility, text *strings.Builder) error {
	if content := node.child("sdtContent"); content != nil {
		return p.inline(content, part, visibility, text)
	}
	return nil
}

func (p *docxParser) inlineRunVisibility(node *docxNode, visibility docxVisibility) (docxVisibility, error) {
	var err error
	if node.word("r") {
		style := node.child("rPr").child("rStyle").attr("val")
		if style == "" {
			style = p.styles.character
		}
		visibility, err = p.styles.apply(visibility, style)
		if err != nil {
			return visibility, err
		}
		visibility, err = docxVisibilityProps(visibility, node.child("rPr"), false)
		if err != nil {
			return visibility, err
		}
	}
	return visibility, nil
}

// docxField tracks one nested complex field. Its cached result is visible only
// after the separator, and instructions such as PAGE cache a layout number.
type docxField struct {
	separated   bool
	instruction string
}

const docxMaxFieldInstruction = 1024

func (p *docxParser) fieldCharacter(node *docxNode) error {
	if node.word("fldChar") {
		switch node.attr("fldCharType") {
		case "begin":
			if len(p.fields) >= 32 {
				return fmt.Errorf("DOCX: field nesting limit exceeded")
			}
			p.fields = append(p.fields, docxField{})
		case "separate":
			if len(p.fields) == 0 {
				return fmt.Errorf("DOCX: invalid field separator")
			}
			p.fields[len(p.fields)-1].separated = true
		case "end":
			if len(p.fields) == 0 {
				return fmt.Errorf("DOCX: invalid field terminator")
			}
			p.fields = p.fields[:len(p.fields)-1]
		default:
			return fmt.Errorf("DOCX: unsupported field character")
		}
	}
	return nil
}

// fieldInstruction keeps the bounded instruction of the innermost field. The
// text itself is never visible; it decides whether the cached result is a
// layout number or contract text.
func (p *docxParser) fieldInstruction(node *docxNode) error {
	if len(p.fields) == 0 || p.fields[len(p.fields)-1].separated {
		return fmt.Errorf("DOCX: field instruction outside an open field")
	}
	field := &p.fields[len(p.fields)-1]
	if utf8.RuneCountInString(field.instruction)+utf8.RuneCountInString(node.text) > docxMaxFieldInstruction {
		return fmt.Errorf("DOCX: field instruction limit exceeded")
	}
	field.instruction += node.text
	return nil
}

// docxPaginationField reports whether a field instruction only caches a page or
// section number. Those values follow layout, so they are excluded exactly as
// DOCX pages and boxes are never invented.
func docxPaginationField(instruction string) bool {
	keywords := strings.Fields(instruction)
	if len(keywords) == 0 {
		return false
	}
	switch strings.ToUpper(keywords[0]) {
	case "PAGE", "NUMPAGES", "SECTIONPAGES", "SECTION":
		return true
	}
	return false
}

func (p *docxParser) inlineVisible(visibility docxVisibility) bool {
	visible := !visibility.hidden && !visibility.special
	for _, field := range p.fields {
		if !field.separated || docxPaginationField(field.instruction) {
			visible = false
		}
	}
	return visible
}

func docxInlineIgnored(node *docxNode) bool {
	return node.word("del") || node.word("moveFrom") || node.word("delText") || node.word("delInstrText") || node.word("pPr") || node.word("rPr") || docxNonContent(node)
}

func (p *docxParser) inlineLeaf(node *docxNode, part string, visible bool, text *strings.Builder) (bool, error) {
	if handled, err := inlineTextLeaf(node, visible, text); handled {
		return true, err
	}
	return p.inlineSpecialLeaf(node, part, visible, text)
}

func inlineTextLeaf(node *docxNode, visible bool, text *strings.Builder) (bool, error) {
	switch {
	case node.word("t"):
		return true, appendInlineText(text, visible, node.text)
	case node.word("tab"), node.word("ptab"):
		return true, appendInlineText(text, visible, "\t")
	case node.word("br"), node.word("cr"), node.word("lastRenderedPageBreak"):
		return true, appendInlineText(text, visible, "\n")
	case node.word("noBreakHyphen"):
		return true, appendInlineText(text, visible, "\u2011")
	case node.word("softHyphen"):
		return true, appendInlineText(text, visible, "\u00AD")
	}
	return false, nil
}

func appendInlineText(text *strings.Builder, visible bool, value string) error {
	if !visible {
		return nil
	}
	if text.Len()+len(value) > docxMaxText {
		return fmt.Errorf("DOCX: extracted text exceeds 8 MiB limit")
	}
	text.WriteString(value)
	return nil
}

func (p *docxParser) inlineSpecialLeaf(node *docxNode, part string, visible bool, text *strings.Builder) (bool, error) {
	switch {
	case node.word("sym"):
		if !visible {
			return true, nil
		}
		symbol, err := docxSymbol(node)
		if err != nil {
			return true, err
		}
		if text.Len()+utf8.RuneLen(symbol) > docxMaxText {
			return true, fmt.Errorf("DOCX: extracted text exceeds 8 MiB limit")
		}
		text.WriteRune(symbol)
		return true, nil
	case node.word("instrText"):
		return true, p.fieldInstruction(node)
	case node.word("drawing"):
		if !visible {
			return true, nil
		}
		value, err := p.drawing(node, part)
		if err != nil {
			return true, err
		}
		return true, appendInlineText(text, visible, "\n"+value+"\n")
	default:
		return false, nil
	}
}

func (p *docxParser) inlineRelationship(node *docxNode, part string) error {
	if !node.word("hyperlink") {
		return nil
	}
	id := node.relID("id")
	if id == "" {
		return nil
	}
	rel, ok := p.pkg.rels[part][id]
	if !ok || !docxRelKind(rel.kind, "hyperlink") {
		return fmt.Errorf("DOCX: invalid hyperlink relationship")
	}
	return nil
}

func (p *docxParser) inlineContainer(node *docxNode) bool {
	switch {
	case node.word("p"), node.word("r"), node.word("ins"), node.word("moveTo"), node.word("smartTag"), node.word("customXml"), node.word("sdtContent"), node.word("fldSimple"), node.word("hyperlink"):
		return true
	default:
		return false
	}
}

// docxDrawingContent records the visible content one w:drawing carries. A
// drawing yields either one raster image or one text box; mixing both would
// require rendered layout to know what is actually painted.
type docxDrawingContent struct {
	imageIDs  []string
	textBoxes []*docxNode
}

func (p *docxParser) drawing(node *docxNode, part string) (string, error) {
	var content docxDrawingContent
	if err := collectDOCXDrawingIDs(node, &content); err != nil {
		return "", err
	}
	switch {
	case len(content.imageIDs) == 1 && len(content.textBoxes) == 0:
		rel, ok := p.pkg.rels[part][content.imageIDs[0]]
		if !ok || !docxRelKind(rel.kind, "image") {
			return "", fmt.Errorf("DOCX: missing embedded image relationship")
		}
		return p.imageText(rel.target)
	case len(content.imageIDs) == 0 && len(content.textBoxes) == 1:
		return p.textBox(content.textBoxes[0], part)
	default:
		return "", fmt.Errorf("DOCX: drawing must contain exactly one supported raster image or one text box")
	}
}

func collectDOCXDrawingIDs(node *docxNode, content *docxDrawingContent) error {
	descend, err := inspectDOCXDrawingNode(node, content)
	if err != nil || !descend {
		return err
	}
	for _, child := range node.children {
		if err := collectDOCXDrawingIDs(child, content); err != nil {
			return err
		}
	}
	return nil
}

// inspectDOCXDrawingNode reports whether the walk continues into the children.
// Text box content stops here: it is parsed as WordprocessingML blocks once the
// branch that carries it is selected, so nested images are not double counted.
func inspectDOCXDrawingNode(node *docxNode, content *docxDrawingContent) (bool, error) {
	switch node.name.Local {
	case "graphicData":
		return true, validateDOCXGraphicData(node)
	case "blip":
		return true, appendDOCXImageID(node, &content.imageIDs)
	case "srcRect":
		return true, validateDOCXImageCrop(node)
	case "txbxContent":
		if !node.word("txbxContent") {
			return false, fmt.Errorf("DOCX: unsupported text box namespace")
		}
		content.textBoxes = append(content.textBoxes, node)
		return false, nil
	case "chart", "relIds", "AlternateContent", "svgBlip", "oleObj", "hidden":
		return false, fmt.Errorf("DOCX: unsupported opaque or hidden drawing content %s", node.name.Local)
	}
	return true, validateDOCXDrawingVisibility(node)
}

func validateDOCXGraphicData(node *docxNode) error {
	switch node.attr("uri") {
	case docxPictureNS, docxPictureStrictNS:
		return nil
	case docxWPSNS:
		return validateDOCXShape(node)
	default:
		return fmt.Errorf("DOCX: charts, diagrams, grouped shapes and opaque drawings are unsupported")
	}
}

// validateDOCXShape accepts only a single shape whose visible content is one
// text box. Words drawn elsewhere, grouped shapes and shape text drawn by a
// nested layout are refused rather than guessed.
func validateDOCXShape(node *docxNode) error {
	var shape *docxNode
	for _, child := range node.children {
		if child.name.Space != docxWPSNS || child.name.Local != "wsp" || shape != nil {
			return fmt.Errorf("DOCX: grouped or nested shapes are unsupported")
		}
		shape = child
	}
	if shape == nil {
		return fmt.Errorf("DOCX: drawing shape is missing")
	}
	boxes := 0
	for _, child := range shape.children {
		if child.name.Space != docxWPSNS {
			return fmt.Errorf("DOCX: unsupported shape child %s", child.name.Local)
		}
		switch child.name.Local {
		case "cNvSpPr", "spPr", "style", "bodyPr":
			if err := docxDrawingProperties(child); err != nil {
				return err
			}
		case "txbx":
			boxes++
			if len(child.children) != 1 || !child.children[0].word("txbxContent") {
				return fmt.Errorf("DOCX: shape text box must hold exactly one text box body")
			}
		default:
			return fmt.Errorf("DOCX: unsupported shape content %s", child.name.Local)
		}
	}
	if boxes != 1 {
		return fmt.Errorf("DOCX: shape must contain exactly one text box")
	}
	return nil
}

// docxDrawingProperties keeps shape appearance purely DrawingML, so no
// WordprocessingML text hides inside properties this parser does not render.
func docxDrawingProperties(node *docxNode) error {
	for _, child := range node.children {
		if child.name.Space != docxANS && child.name.Space != docxAStrictNS {
			return fmt.Errorf("DOCX: unsupported shape property %s", child.name.Local)
		}
		if err := docxDrawingProperties(child); err != nil {
			return err
		}
	}
	return nil
}

// textBox reads the paragraphs a text box paints. Tables are refused because
// their cell coordinates cannot be anchored to this drawing's position.
func (p *docxParser) textBox(node *docxNode, part string) (string, error) {
	parts, err := p.blocks(node, part)
	if err != nil {
		return "", err
	}
	for _, part := range parts {
		if len(part.cells) > 0 {
			return "", fmt.Errorf("DOCX: tables inside text boxes are unsupported")
		}
	}
	fragment, err := docxJoin(parts, "\n")
	if err != nil {
		return "", err
	}
	return fragment.text, nil
}

func appendDOCXImageID(node *docxNode, imageIDs *[]string) error {
	if node.name.Space != docxANS && node.name.Space != docxAStrictNS {
		return fmt.Errorf("DOCX: unsupported image namespace")
	}
	if node.relID("link") != "" || node.relID("embed") == "" {
		return fmt.Errorf("DOCX: linked/external images are unsupported")
	}
	if len(node.children) != 0 {
		return fmt.Errorf("DOCX: image effects and alternate image representations are unsupported")
	}
	*imageIDs = append(*imageIDs, node.relID("embed"))
	return nil
}

func validateDOCXImageCrop(node *docxNode) error {
	for _, attr := range node.attrs {
		if attr.Value != "0" {
			return fmt.Errorf("DOCX: cropped images require rendered OCR and are unsupported")
		}
	}
	return nil
}

func validateDOCXDrawingVisibility(node *docxNode) error {
	if node.name.Local == "docPr" && (node.attr("hidden") == "1" || node.attr("hidden") == "true") {
		return fmt.Errorf("DOCX: hidden drawing is unsupported")
	}
	return nil
}

func (p *docxParser) imageText(target string) (string, error) {
	if value, exists := p.images[target]; exists {
		return value, nil
	}
	p.imageCount++
	if p.imageCount > 32 {
		return "", fmt.Errorf("DOCX: embedded image count exceeds 32")
	}
	if p.ocr == nil {
		return "", fmt.Errorf("DOCX: embedded image requires configured OCR")
	}
	body, media, err := p.readOCRImage(target)
	if err != nil {
		return "", err
	}
	text, err := p.ocr(p.pkg.ctx, media, body)
	if err != nil {
		return "", fmt.Errorf("DOCX: embedded image OCR failed: %w", err)
	}
	text, err = normalizeOCRText(text, p.pkg.ctx)
	if err != nil {
		return "", err
	}
	p.fromOCR = true
	p.images[target] = text
	return text, nil
}

func (p *docxParser) readOCRImage(target string) ([]byte, string, error) {
	body, err := p.pkg.data(target, docxMaxPart)
	if err != nil {
		return nil, "", err
	}
	media, err := decodeOCRImage(body)
	if err != nil {
		return nil, "", err
	}
	if p.pkg.media(target) != media {
		return nil, "", fmt.Errorf("DOCX: embedded image content type does not match bytes")
	}
	return body, media, nil
}

func decodeOCRImage(body []byte) (string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || (format != "png" && format != "jpeg") {
		return "", fmt.Errorf("DOCX: embedded image must be valid PNG or JPEG")
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 16000000 {
		return "", fmt.Errorf("DOCX: embedded image exceeds 16 megapixel limit")
	}
	return "image/" + format, nil
}

func normalizeOCRText(text string, ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("DOCX: embedded image OCR returned invalid UTF-8")
	}
	if len(text) > docxMaxText {
		return "", fmt.Errorf("DOCX: embedded image OCR text limit exceeded")
	}
	text = docxCleanText(text)
	if text == "" {
		return "", fmt.Errorf("DOCX: embedded image OCR returned no text")
	}
	return text, nil
}
