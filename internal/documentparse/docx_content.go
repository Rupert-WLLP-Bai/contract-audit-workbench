package documentparse

import (
	"bytes"
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
	if node.word("del") || node.word("moveFrom") || node.word("delText") || node.word("delInstrText") || node.word("instrText") || node.word("pPr") || node.word("rPr") || docxNonContent(node) {
		return nil
	}
	if node.word("r") {
		style := node.child("rPr").child("rStyle").attr("val")
		if style == "" {
			style = p.styles.character
		}
		var err error
		visibility, err = p.styles.apply(visibility, style)
		if err != nil {
			return err
		}
		visibility, err = docxVisibilityProps(visibility, node.child("rPr"), false)
		if err != nil {
			return err
		}
	}
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
		return nil
	}
	visible := !visibility.hidden && !visibility.special
	for _, result := range p.fields {
		if !result {
			visible = false
		}
	}
	appendText := func(value string) error {
		if !visible {
			return nil
		}
		if text.Len()+len(value) > docxMaxText {
			return fmt.Errorf("DOCX: extracted text exceeds 8 MiB limit")
		}
		text.WriteString(value)
		return nil
	}
	switch {
	case node.word("t"):
		return appendText(node.text)
	case node.word("tab"), node.word("ptab"):
		return appendText("\t")
	case node.word("br"), node.word("cr"), node.word("lastRenderedPageBreak"):
		return appendText("\n")
	case node.word("noBreakHyphen"):
		return appendText("\u2011")
	case node.word("softHyphen"):
		return appendText("\u00AD")
	case node.word("sym"):
		if !visible {
			return nil
		}
		symbol, err := docxSymbol(node)
		if err != nil {
			return err
		}
		if text.Len()+utf8.RuneLen(symbol) > docxMaxText {
			return fmt.Errorf("DOCX: extracted text exceeds 8 MiB limit")
		}
		text.WriteRune(symbol)
		return nil
	case node.word("drawing"):
		if !visible {
			return nil
		}
		value, err := p.drawing(node, part)
		if err != nil {
			return err
		}
		return appendText("\n" + value + "\n")
	case node.word("sdt"):
		if content := node.child("sdtContent"); content != nil {
			return p.inline(content, part, visibility, text)
		}
		return nil
	case node.word("hyperlink"):
		if id := node.relID("id"); id != "" {
			rel, ok := p.pkg.rels[part][id]
			if !ok || !docxRelKind(rel.kind, "hyperlink") {
				return fmt.Errorf("DOCX: invalid hyperlink relationship")
			}
		}
	case node.word("footnoteReference"), node.word("endnoteReference"):
		if visible {
			return fmt.Errorf("DOCX: visible footnote/endnote references are unsupported; note content cannot be omitted")
		}
		return nil
	case node.word("p"), node.word("r"), node.word("ins"), node.word("moveTo"), node.word("smartTag"), node.word("customXml"), node.word("sdtContent"), node.word("fldSimple"):
		// These containers retain their visible text in document order.
	default:
		if !visible {
			return nil
		}
		return fmt.Errorf("DOCX: unsupported visible inline content %s", node.name.Local)
	}
	for _, child := range node.children {
		if err := p.inline(child, part, visibility, text); err != nil {
			return err
		}
	}
	return nil
}

func (p *docxParser) drawing(node *docxNode, part string) (string, error) {
	var imageIDs []string
	var visit func(*docxNode) error
	visit = func(n *docxNode) error {
		switch n.name.Local {
		case "graphicData":
			uri := n.attr("uri")
			if uri != "http://schemas.openxmlformats.org/drawingml/2006/picture" && uri != "http://purl.oclc.org/ooxml/drawingml/picture" {
				return fmt.Errorf("DOCX: charts, diagrams, text boxes and opaque drawings are unsupported")
			}
		case "blip":
			if n.name.Space != "http://schemas.openxmlformats.org/drawingml/2006/main" && n.name.Space != "http://purl.oclc.org/ooxml/drawingml/main" {
				return fmt.Errorf("DOCX: unsupported image namespace")
			}
			if n.relID("link") != "" || n.relID("embed") == "" {
				return fmt.Errorf("DOCX: linked/external images are unsupported")
			}
			if len(n.children) != 0 {
				return fmt.Errorf("DOCX: image effects and alternate image representations are unsupported")
			}
			imageIDs = append(imageIDs, n.relID("embed"))
		case "srcRect":
			for _, attr := range n.attrs {
				if attr.Value != "0" {
					return fmt.Errorf("DOCX: cropped images require rendered OCR and are unsupported")
				}
			}
		case "txbxContent", "chart", "relIds", "AlternateContent", "svgBlip", "oleObj", "hidden":
			return fmt.Errorf("DOCX: unsupported opaque or hidden drawing content %s", n.name.Local)
		}
		if n.name.Local == "docPr" && (n.attr("hidden") == "1" || n.attr("hidden") == "true") {
			return fmt.Errorf("DOCX: hidden drawing is unsupported")
		}
		for _, child := range n.children {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(node); err != nil {
		return "", err
	}
	if len(imageIDs) != 1 {
		return "", fmt.Errorf("DOCX: drawing must contain exactly one supported raster image")
	}
	rel, ok := p.pkg.rels[part][imageIDs[0]]
	if !ok || !docxRelKind(rel.kind, "image") {
		return "", fmt.Errorf("DOCX: missing embedded image relationship")
	}
	if value, exists := p.images[rel.target]; exists {
		return value, nil
	}
	p.imageCount++
	if p.imageCount > 32 {
		return "", fmt.Errorf("DOCX: embedded image count exceeds 32")
	}
	if p.ocr == nil {
		return "", fmt.Errorf("DOCX: embedded image requires configured OCR")
	}
	body, err := p.pkg.data(rel.target, docxMaxPart)
	if err != nil {
		return "", err
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || (format != "png" && format != "jpeg") {
		return "", fmt.Errorf("DOCX: embedded image must be valid PNG or JPEG")
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 16000000 {
		return "", fmt.Errorf("DOCX: embedded image exceeds 16 megapixel limit")
	}
	media := "image/" + format
	if p.pkg.media(rel.target) != media {
		return "", fmt.Errorf("DOCX: embedded image content type does not match bytes")
	}
	text, err := p.ocr(p.pkg.ctx, media, body)
	if err != nil {
		return "", fmt.Errorf("DOCX: embedded image OCR failed: %w", err)
	}
	if err := p.pkg.ctx.Err(); err != nil {
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
	p.fromOCR = true
	p.images[rel.target] = text
	return text, nil
}
