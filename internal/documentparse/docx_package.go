package documentparse

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
)

const (
	docxMaxArchive  = 32 << 20
	docxMaxExpanded = 64 << 20
	docxMaxPart     = 16 << 20
	docxMaxXML      = 8 << 20
	docxMaxEntries  = 1024
	docxMaxNodes    = 250000
	docxMaxDepth    = 128
	docxWNS         = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	docxWStrictNS   = "http://purl.oclc.org/ooxml/wordprocessingml/main"
	docxRNS         = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	docxRStrictNS   = "http://purl.oclc.org/ooxml/officeDocument/relationships"
	docxPackageRNS  = "http://schemas.openxmlformats.org/package/2006/relationships"
	docxTypesNS     = "http://schemas.openxmlformats.org/package/2006/content-types"
)

type docxNode struct {
	name     xml.Name
	attrs    []xml.Attr
	text     string
	children []*docxNode
}

func (n *docxNode) word(local string) bool {
	return n != nil && (n.name.Space == docxWNS || n.name.Space == docxWStrictNS) && n.name.Local == local
}

func (n *docxNode) child(local string) *docxNode {
	if n != nil {
		for _, child := range n.children {
			if child.word(local) {
				return child
			}
		}
	}
	return nil
}

func (n *docxNode) attr(local string) string {
	if n != nil {
		for _, attr := range n.attrs {
			if attr.Name.Local == local {
				return attr.Value
			}
		}
	}
	return ""
}

func (n *docxNode) relID(local string) string {
	for _, attr := range n.attrs {
		if (attr.Name.Space == docxRNS || attr.Name.Space == docxRStrictNS) && attr.Name.Local == local {
			return attr.Value
		}
	}
	return ""
}

type docxRelationship struct{ kind, target string }

type docxPackage struct {
	ctx          context.Context
	files        map[string]*zip.File
	rels         map[string]map[string]docxRelationship
	contentTypes map[string]string
	defaults     map[string]string
	nodes        int
}

func docxOpen(ctx context.Context, body []byte) (*docxPackage, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if len(body) > docxMaxArchive {
		return nil, "", fmt.Errorf("DOCX: archive exceeds 32 MiB limit")
	}
	if len(body) < 4 || !bytes.Equal(body[:4], []byte{'P', 'K', 3, 4}) {
		return nil, "", fmt.Errorf("DOCX: input is not an OOXML ZIP package")
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, "", fmt.Errorf("DOCX: invalid ZIP: %w", err)
	}
	if len(zr.File) > docxMaxEntries {
		return nil, "", fmt.Errorf("DOCX: ZIP entry limit exceeded")
	}
	p := &docxPackage{ctx: ctx, files: make(map[string]*zip.File), rels: make(map[string]map[string]docxRelationship), contentTypes: make(map[string]string), defaults: make(map[string]string)}
	var expanded uint64
	for _, file := range zr.File {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		if file.FileInfo().IsDir() {
			continue
		}
		name := file.Name
		if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || file.Mode().Type() != 0 {
			return nil, "", fmt.Errorf("DOCX: invalid ZIP part path or file type")
		}
		if _, exists := p.files[name]; exists {
			return nil, "", fmt.Errorf("DOCX: duplicate ZIP part %q", name)
		}
		if file.Flags&1 != 0 {
			return nil, "", fmt.Errorf("DOCX: encrypted ZIP parts are unsupported")
		}
		if file.UncompressedSize64 > docxMaxPart {
			return nil, "", fmt.Errorf("DOCX: part exceeds 16 MiB limit")
		}
		expanded += file.UncompressedSize64
		if expanded > docxMaxExpanded {
			return nil, "", fmt.Errorf("DOCX: expanded ZIP exceeds 64 MiB limit")
		}
		lower := strings.ToLower(name)
		if strings.Contains(lower, "/embeddings/") || strings.Contains(lower, "/activex/") || strings.HasSuffix(lower, "vbaproject.bin") {
			return nil, "", fmt.Errorf("DOCX: opaque embedded packages, OLE, ActiveX and macros are unsupported")
		}
		p.files[name] = file
	}
	types, err := p.xml("[Content_Types].xml")
	if err != nil {
		return nil, "", err
	}
	if types.name.Space != docxTypesNS || types.name.Local != "Types" {
		return nil, "", fmt.Errorf("DOCX: invalid content-types root")
	}
	for _, entry := range types.children {
		if entry.name.Space != docxTypesNS {
			return nil, "", fmt.Errorf("DOCX: invalid content-types entry")
		}
		kind := entry.attr("ContentType")
		if kind == "" {
			return nil, "", fmt.Errorf("DOCX: missing part content type")
		}
		lower := strings.ToLower(kind)
		if strings.Contains(lower, "macroenabled") || strings.Contains(lower, "vbaproject") || strings.Contains(lower, "oleobject") || strings.Contains(lower, "activex") {
			return nil, "", fmt.Errorf("DOCX: executable or opaque content type is unsupported")
		}
		switch entry.name.Local {
		case "Default":
			ext := strings.ToLower(entry.attr("Extension"))
			if ext == "" || p.defaults[ext] != "" {
				return nil, "", fmt.Errorf("DOCX: invalid duplicate default content type")
			}
			p.defaults[ext] = kind
		case "Override":
			name, err := docxTarget("", entry.attr("PartName"))
			if err != nil || p.contentTypes[name] != "" {
				return nil, "", fmt.Errorf("DOCX: invalid duplicate part content type")
			}
			p.contentTypes[name] = kind
		default:
			return nil, "", fmt.Errorf("DOCX: unsupported content-types entry")
		}
	}
	// External hyperlinks carry visible in-package text and are never fetched.
	// Other external resources would leave document evidence incomplete.
	for name := range p.files {
		if !strings.HasSuffix(name, ".rels") {
			continue
		}
		source := ""
		if name != "_rels/.rels" {
			if path.Base(path.Dir(name)) != "_rels" {
				return nil, "", fmt.Errorf("DOCX: invalid relationship part path")
			}
			source = path.Join(path.Dir(path.Dir(name)), strings.TrimSuffix(path.Base(name), ".rels"))
		}
		root, err := p.xml(name)
		if err != nil {
			return nil, "", err
		}
		if root.name.Space != docxPackageRNS || root.name.Local != "Relationships" {
			return nil, "", fmt.Errorf("DOCX: invalid relationships root")
		}
		rels := make(map[string]docxRelationship)
		for _, node := range root.children {
			if node.name.Space != docxPackageRNS || node.name.Local != "Relationship" {
				return nil, "", fmt.Errorf("DOCX: invalid relationship entry")
			}
			mode := node.attr("TargetMode")
			id, kind := node.attr("Id"), node.attr("Type")
			if id == "" || kind == "" {
				return nil, "", fmt.Errorf("DOCX: invalid relationship identity")
			}
			if _, duplicate := rels[id]; duplicate {
				return nil, "", fmt.Errorf("DOCX: duplicate relationship identity")
			}
			if mode == "External" && docxRelKind(kind, "hyperlink") {
				rels[id] = docxRelationship{kind: kind}
				continue
			}
			if mode != "" && mode != "Internal" {
				return nil, "", fmt.Errorf("DOCX: external resources are unsupported; no network resources are fetched")
			}
			for _, opaque := range []string{"oleObject", "package", "aFChunk", "control", "vbaProject"} {
				if strings.HasSuffix(kind, "/"+opaque) {
					return nil, "", fmt.Errorf("DOCX: opaque relationship %s is unsupported", opaque)
				}
			}
			target, err := docxTarget(source, node.attr("Target"))
			if err != nil {
				return nil, "", err
			}
			if p.files[target] == nil {
				return nil, "", fmt.Errorf("DOCX: relationship points to missing part %q", target)
			}
			rels[id] = docxRelationship{kind: kind, target: target}
		}
		p.rels[source] = rels
	}
	main := ""
	for _, rel := range p.rels[""] {
		if docxRelKind(rel.kind, "officeDocument") {
			if main != "" {
				return nil, "", fmt.Errorf("DOCX: multiple main document parts")
			}
			main = rel.target
		}
	}
	if main == "" || p.media(main) != "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml" {
		return nil, "", fmt.Errorf("DOCX: missing DOCX main document relationship/content type")
	}
	return p, main, nil
}

func docxRelKind(kind, local string) bool {
	return kind == docxRNS+"/"+local || kind == docxRStrictNS+"/"+local
}

func docxTarget(source, target string) (string, error) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "" || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.Path == "" || strings.ContainsAny(u.Path, "\\\x00") {
		return "", fmt.Errorf("DOCX: invalid or external relationship target")
	}
	name := path.Clean(path.Join(path.Dir(source), u.Path))
	if strings.HasPrefix(u.Path, "/") {
		name = path.Clean(strings.TrimPrefix(u.Path, "/"))
	}
	if name == "." || name == ".." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("DOCX: relationship escapes package")
	}
	return name, nil
}

func (p *docxPackage) media(name string) string {
	if kind := p.contentTypes[name]; kind != "" {
		return kind
	}
	return p.defaults[strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))]
}

func (p *docxPackage) data(name string, limit int64) ([]byte, error) {
	if err := p.ctx.Err(); err != nil {
		return nil, err
	}
	file := p.files[name]
	if file == nil {
		return nil, fmt.Errorf("DOCX: missing part %q", name)
	}
	if file.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("DOCX: part resource limit exceeded")
	}
	r, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("DOCX: opening part: %w", err)
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("DOCX: reading part: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("DOCX: part resource limit exceeded")
	}
	return data, p.ctx.Err()
}

func (p *docxPackage) xml(name string) (*docxNode, error) {
	body, err := p.data(name, docxMaxXML)
	if err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	var root *docxNode
	var stack []*docxNode
	for {
		if err := p.ctx.Err(); err != nil {
			return nil, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("DOCX: invalid XML in %s: %w", name, err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			p.nodes++
			if p.nodes > docxMaxNodes || len(stack) >= docxMaxDepth || len(token.Attr) > 128 {
				return nil, fmt.Errorf("DOCX: XML node/depth/attribute limit exceeded")
			}
			node := &docxNode{name: token.Name, attrs: token.Attr}
			if len(stack) == 0 {
				if root != nil {
					return nil, fmt.Errorf("DOCX: XML has multiple roots")
				}
				root = node
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, node)
			}
			stack = append(stack, node)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				if strings.TrimSpace(string(token)) != "" {
					return nil, fmt.Errorf("DOCX: text outside XML root")
				}
			} else {
				node := stack[len(stack)-1]
				node.text += string(token)
			}
		case xml.Directive:
			return nil, fmt.Errorf("DOCX: XML directives/entities are unsupported")
		case xml.ProcInst:
			if token.Target != "xml" || root != nil {
				return nil, fmt.Errorf("DOCX: XML processing instruction is unsupported")
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("DOCX: empty XML part")
	}
	return root, nil
}
