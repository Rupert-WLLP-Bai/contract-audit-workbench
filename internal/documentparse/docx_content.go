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
	return false, nil
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

func (p *docxParser) fieldCharacter(node *docxNode) error {
	if node.word("fldChar") {
		switch node.attr("fldCharType") {
		case "begin":
			if len(p.fields) >= 32 {
				return fmt.Errorf("DOCX: field nesting limit exceeded")
			}
			p.fields = append(p.fields, false)
		case "separate":
			if len(p.fields) == 0 {
				return fmt.Errorf("DOCX: invalid field separator")
			}
			p.fields[len(p.fields)-1] = true
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

func (p *docxParser) inlineVisible(visibility docxVisibility) bool {
	visible := !visibility.hidden && !visibility.special
	for _, result := range p.fields {
		if !result {
			visible = false
		}
	}
	return visible
}

func docxInlineIgnored(node *docxNode) bool {
	return node.word("del") || node.word("moveFrom") || node.word("delText") || node.word("delInstrText") || node.word("instrText") || node.word("pPr") || node.word("rPr") || docxNonContent(node)
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

func (p *docxParser) drawing(node *docxNode, part string) (string, error) {
	var imageIDs []string
	if err := collectDOCXDrawingIDs(node, &imageIDs); err != nil {
		return "", err
	}
	if len(imageIDs) != 1 {
		return "", fmt.Errorf("DOCX: drawing must contain exactly one supported raster image")
	}
	rel, ok := p.pkg.rels[part][imageIDs[0]]
	if !ok || !docxRelKind(rel.kind, "image") {
		return "", fmt.Errorf("DOCX: missing embedded image relationship")
	}
	return p.imageText(rel.target)
}

func collectDOCXDrawingIDs(node *docxNode, imageIDs *[]string) error {
	if err := inspectDOCXDrawingNode(node, imageIDs); err != nil {
		return err
	}
	for _, child := range node.children {
		if err := collectDOCXDrawingIDs(child, imageIDs); err != nil {
			return err
		}
	}
	return nil
}

func inspectDOCXDrawingNode(node *docxNode, imageIDs *[]string) error {
	switch node.name.Local {
	case "graphicData":
		return validateDOCXGraphicData(node)
	case "blip":
		return appendDOCXImageID(node, imageIDs)
	case "srcRect":
		return validateDOCXImageCrop(node)
	case "txbxContent", "chart", "relIds", "AlternateContent", "svgBlip", "oleObj", "hidden":
		return fmt.Errorf("DOCX: unsupported opaque or hidden drawing content %s", node.name.Local)
	}
	return validateDOCXDrawingVisibility(node)
}

func validateDOCXGraphicData(node *docxNode) error {
	uri := node.attr("uri")
	if uri != "http://schemas.openxmlformats.org/drawingml/2006/picture" && uri != "http://purl.oclc.org/ooxml/drawingml/picture" {
		return fmt.Errorf("DOCX: charts, diagrams, text boxes and opaque drawings are unsupported")
	}
	return nil
}

func appendDOCXImageID(node *docxNode, imageIDs *[]string) error {
	if node.name.Space != "http://schemas.openxmlformats.org/drawingml/2006/main" && node.name.Space != "http://purl.oclc.org/ooxml/drawingml/main" {
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
