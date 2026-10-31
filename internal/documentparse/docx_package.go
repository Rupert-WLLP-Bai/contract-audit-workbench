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
	docxMaxArchive      = 32 << 20
	docxMaxExpanded     = 64 << 20
	docxMaxPart         = 16 << 20
	docxMaxXML          = 8 << 20
	docxMaxEntries      = 1024
	docxMaxNodes        = 250000
	docxMaxDepth        = 128
	docxWNS             = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	docxWStrictNS       = "http://purl.oclc.org/ooxml/wordprocessingml/main"
	docxRNS             = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	docxRStrictNS       = "http://purl.oclc.org/ooxml/officeDocument/relationships"
	docxPackageRNS      = "http://schemas.openxmlformats.org/package/2006/relationships"
	docxTypesNS         = "http://schemas.openxmlformats.org/package/2006/content-types"
	docxMCNS            = "http://schemas.openxmlformats.org/markup-compatibility/2006"
	docxANS             = "http://schemas.openxmlformats.org/drawingml/2006/main"
	docxAStrictNS       = "http://purl.oclc.org/ooxml/drawingml/main"
	docxWPSNS           = "http://schemas.microsoft.com/office/word/2010/wordprocessingShape"
	docxPictureNS       = "http://schemas.openxmlformats.org/drawingml/2006/picture"
	docxPictureStrictNS = "http://purl.oclc.org/ooxml/drawingml/picture"
)

type docxNode struct {
	name     xml.Name
	attrs    []xml.Attr
	text     string
	children []*docxNode
	// namespaces holds the in-scope prefix bindings at this element. It is
	// shared with the parent until an element declares xmlns itself, so markup
	// compatibility Requires prefixes resolve exactly as the document declares
	// them rather than by literal prefix spelling.
	namespaces map[string]string
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

// attr reads one unqualified attribute. Namespace declarations are kept as
// attributes by the XML decoder, so they are skipped here: a declaration such as
// xmlns:uri must never stand in for a real uri attribute.
func (n *docxNode) attr(local string) string {
	if n != nil {
		for _, attr := range n.attrs {
			if attr.Name.Space == "xmlns" || (attr.Name.Space == "" && attr.Name.Local == "xmlns") {
				continue
			}
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
	files, err := docxArchiveFiles(ctx, body)
	if err != nil {
		return nil, "", err
	}
	p := &docxPackage{ctx: ctx, files: files, rels: make(map[string]map[string]docxRelationship), contentTypes: make(map[string]string), defaults: make(map[string]string)}
	if err := p.loadContentTypes(); err != nil {
		return nil, "", err
	}
	if err := p.loadRelationships(); err != nil {
		return nil, "", err
	}
	main, err := p.mainDocument()
	if err != nil {
		return nil, "", err
	}
	return p, main, nil
}

func docxArchiveFiles(ctx context.Context, body []byte) (map[string]*zip.File, error) {
	zr, err := docxZipReader(ctx, body)
	if err != nil {
		return nil, err
	}
	if len(zr.File) > docxMaxEntries {
		return nil, fmt.Errorf("DOCX: ZIP entry limit exceeded")
	}
	files := make(map[string]*zip.File)
	var expanded uint64
	for _, file := range zr.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if file.FileInfo().IsDir() {
			continue
		}
		if err := addArchiveFile(files, file, &expanded); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func docxZipReader(ctx context.Context, body []byte) (*zip.Reader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(body) > docxMaxArchive {
		return nil, fmt.Errorf("DOCX: archive exceeds 32 MiB limit")
	}
	if len(body) < 4 || !bytes.Equal(body[:4], []byte{'P', 'K', 3, 4}) {
		return nil, fmt.Errorf("DOCX: input is not an OOXML ZIP package")
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("DOCX: invalid ZIP: %w", err)
	}
	return zr, nil
}

func addArchiveFile(files map[string]*zip.File, file *zip.File, expanded *uint64) error {
	if err := docxValidateArchivePath(file); err != nil {
		return err
	}
	if _, exists := files[file.Name]; exists {
		return fmt.Errorf("DOCX: duplicate ZIP part %q", file.Name)
	}
	if err := docxValidateArchiveResource(file); err != nil {
		return err
	}
	*expanded += file.UncompressedSize64
	if *expanded > docxMaxExpanded {
		return fmt.Errorf("DOCX: expanded ZIP exceeds 64 MiB limit")
	}
	if docxOpaquePart(file.Name) {
		return fmt.Errorf("DOCX: opaque embedded packages, OLE, ActiveX and macros are unsupported")
	}
	files[file.Name] = file
	return nil
}

func docxValidateArchivePath(file *zip.File) error {
	if !docxInvalidArchivePath(file) {
		return nil
	}
	return fmt.Errorf("DOCX: invalid ZIP part path or file type")
}

func docxValidateArchiveResource(file *zip.File) error {
	if file.Flags&1 != 0 {
		return fmt.Errorf("DOCX: encrypted ZIP parts are unsupported")
	}
	if file.UncompressedSize64 > docxMaxPart {
		return fmt.Errorf("DOCX: part exceeds 16 MiB limit")
	}
	return nil
}

func docxInvalidArchivePath(file *zip.File) bool {
	name := file.Name
	return name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || file.Mode().Type() != 0
}

func docxOpaquePart(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "/embeddings/") || strings.Contains(lower, "/activex/") || strings.HasSuffix(lower, "vbaproject.bin")
}

func (p *docxPackage) loadContentTypes() error {
	types, err := p.xml("[Content_Types].xml")
	if err != nil {
		return err
	}
	if types.name.Space != docxTypesNS || types.name.Local != "Types" {
		return fmt.Errorf("DOCX: invalid content-types root")
	}
	for _, entry := range types.children {
		if err := p.addContentType(entry); err != nil {
			return err
		}
	}
	return nil
}

func (p *docxPackage) addContentType(entry *docxNode) error {
	if entry.name.Space != docxTypesNS {
		return fmt.Errorf("DOCX: invalid content-types entry")
	}
	kind := entry.attr("ContentType")
	if kind == "" {
		return fmt.Errorf("DOCX: missing part content type")
	}
	if docxOpaqueContentType(kind) {
		return fmt.Errorf("DOCX: executable or opaque content type is unsupported")
	}
	switch entry.name.Local {
	case "Default":
		return p.addDefaultType(entry, kind)
	case "Override":
		return p.addOverrideType(entry, kind)
	default:
		return fmt.Errorf("DOCX: unsupported content-types entry")
	}
}

func (p *docxPackage) addDefaultType(entry *docxNode, kind string) error {
	ext := strings.ToLower(entry.attr("Extension"))
	if ext == "" || p.defaults[ext] != "" {
		return fmt.Errorf("DOCX: invalid duplicate default content type")
	}
	p.defaults[ext] = kind
	return nil
}

func (p *docxPackage) addOverrideType(entry *docxNode, kind string) error {
	name, err := docxTarget("", entry.attr("PartName"))
	if err != nil || p.contentTypes[name] != "" {
		return fmt.Errorf("DOCX: invalid duplicate part content type")
	}
	p.contentTypes[name] = kind
	return nil
}

func docxOpaqueContentType(kind string) bool {
	lower := strings.ToLower(kind)
	return strings.Contains(lower, "macroenabled") || strings.Contains(lower, "vbaproject") || strings.Contains(lower, "oleobject") || strings.Contains(lower, "activex")
}

func (p *docxPackage) loadRelationships() error {
	// External hyperlinks carry visible in-package text and are never fetched.
	// Other external resources would leave document evidence incomplete.
	for name := range p.files {
		if !strings.HasSuffix(name, ".rels") {
			continue
		}
		if err := p.loadRelationshipsFor(name); err != nil {
			return err
		}
	}
	return nil
}

func (p *docxPackage) loadRelationshipsFor(name string) error {
	source, err := docxRelationshipSource(name)
	if err != nil {
		return err
	}
	root, err := p.xml(name)
	if err != nil {
		return err
	}
	if root.name.Space != docxPackageRNS || root.name.Local != "Relationships" {
		return fmt.Errorf("DOCX: invalid relationships root")
	}
	rels := make(map[string]docxRelationship)
	for _, node := range root.children {
		rel, err := p.relationship(source, node, rels)
		if err != nil {
			return err
		}
		rels[node.attr("Id")] = rel
	}
	p.rels[source] = rels
	return nil
}

func docxRelationshipSource(name string) (string, error) {
	if name == "_rels/.rels" {
		return "", nil
	}
	if path.Base(path.Dir(name)) != "_rels" {
		return "", fmt.Errorf("DOCX: invalid relationship part path")
	}
	return path.Join(path.Dir(path.Dir(name)), strings.TrimSuffix(path.Base(name), ".rels")), nil
}

func (p *docxPackage) relationship(source string, node *docxNode, rels map[string]docxRelationship) (docxRelationship, error) {
	if node.name.Space != docxPackageRNS || node.name.Local != "Relationship" {
		return docxRelationship{}, fmt.Errorf("DOCX: invalid relationship entry")
	}
	mode := node.attr("TargetMode")
	id, kind := node.attr("Id"), node.attr("Type")
	if id == "" || kind == "" {
		return docxRelationship{}, fmt.Errorf("DOCX: invalid relationship identity")
	}
	if _, duplicate := rels[id]; duplicate {
		return docxRelationship{}, fmt.Errorf("DOCX: duplicate relationship identity")
	}
	if docxExternalHyperlink(mode, kind) {
		return docxRelationship{kind: kind}, nil
	}
	target, err := p.internalRelationshipTarget(source, mode, kind, node.attr("Target"))
	if err != nil {
		return docxRelationship{}, err
	}
	return docxRelationship{kind: kind, target: target}, nil
}

func docxExternalHyperlink(mode, kind string) bool {
	return mode == "External" && docxRelKind(kind, "hyperlink")
}

func (p *docxPackage) internalRelationshipTarget(source, mode, kind, value string) (string, error) {
	if mode != "" && mode != "Internal" {
		return "", fmt.Errorf("DOCX: external resources are unsupported; no network resources are fetched")
	}
	if opaque := docxOpaqueRelationship(kind); opaque != "" {
		return "", fmt.Errorf("DOCX: opaque relationship %s is unsupported", opaque)
	}
	target, err := docxTarget(source, value)
	if err != nil {
		return "", err
	}
	if p.files[target] == nil {
		return "", fmt.Errorf("DOCX: relationship points to missing part %q", target)
	}
	return target, nil
}

func docxOpaqueRelationship(kind string) string {
	for _, opaque := range []string{"oleObject", "package", "aFChunk", "control", "vbaProject"} {
		if strings.HasSuffix(kind, "/"+opaque) {
			return opaque
		}
	}
	return ""
}

func (p *docxPackage) mainDocument() (string, error) {
	main := ""
	for _, rel := range p.rels[""] {
		if docxRelKind(rel.kind, "officeDocument") {
			if main != "" {
				return "", fmt.Errorf("DOCX: multiple main document parts")
			}
			main = rel.target
		}
	}
	if main == "" || p.media(main) != "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml" {
		return "", fmt.Errorf("DOCX: missing DOCX main document relationship/content type")
	}
	return main, nil
}

func docxRelKind(kind, local string) bool {
	return kind == docxRNS+"/"+local || kind == docxRStrictNS+"/"+local
}

func docxTarget(source, target string) (string, error) {
	u, err := url.Parse(target)
	if err != nil || !docxInternalURL(u) {
		return "", fmt.Errorf("DOCX: invalid or external relationship target")
	}
	name := path.Clean(path.Join(path.Dir(source), u.Path))
	if strings.HasPrefix(u.Path, "/") {
		name = path.Clean(strings.TrimPrefix(u.Path, "/"))
	}
	if docxPackagePathEscapes(name) {
		return "", fmt.Errorf("DOCX: relationship escapes package")
	}
	return name, nil
}

func docxInternalURL(u *url.URL) bool {
	return u != nil && u.Scheme == "" && u.Host == "" && u.RawQuery == "" && u.Fragment == "" && u.Opaque == "" && u.Path != "" && !strings.ContainsAny(u.Path, "\\\x00")
}

func docxPackagePathEscapes(name string) bool {
	return name == "." || name == ".." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/")
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
	tree := &docxXMLTree{pkg: p}
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
		if err := tree.consume(token); err != nil {
			return nil, err
		}
	}
	if tree.root == nil {
		return nil, fmt.Errorf("DOCX: empty XML part")
	}
	return tree.root, nil
}

type docxXMLTree struct {
	pkg   *docxPackage
	root  *docxNode
	stack []*docxNode
}

func (tree *docxXMLTree) consume(token xml.Token) error {
	switch token := token.(type) {
	case xml.StartElement:
		return tree.start(token)
	case xml.EndElement:
		tree.stack = tree.stack[:len(tree.stack)-1]
	case xml.CharData:
		return tree.characters(token)
	case xml.Directive:
		return fmt.Errorf("DOCX: XML directives/entities are unsupported")
	case xml.ProcInst:
		if token.Target != "xml" || tree.root != nil {
			return fmt.Errorf("DOCX: XML processing instruction is unsupported")
		}
	}
	return nil
}

func (tree *docxXMLTree) start(token xml.StartElement) error {
	tree.pkg.nodes++
	if tree.pkg.nodes > docxMaxNodes || len(tree.stack) >= docxMaxDepth || len(token.Attr) > 128 {
		return fmt.Errorf("DOCX: XML node/depth/attribute limit exceeded")
	}
	node := &docxNode{name: token.Name, attrs: token.Attr}
	if len(tree.stack) == 0 {
		node.namespaces = docxNamespaceScope(nil, token.Attr)
	} else {
		node.namespaces = docxNamespaceScope(tree.stack[len(tree.stack)-1].namespaces, token.Attr)
	}
	if len(tree.stack) == 0 {
		if tree.root != nil {
			return fmt.Errorf("DOCX: XML has multiple roots")
		}
		tree.root = node
	} else {
		parent := tree.stack[len(tree.stack)-1]
		parent.children = append(parent.children, node)
	}
	tree.stack = append(tree.stack, node)
	return nil
}

// docxNamespaceScope returns the prefix bindings in scope for one element. The
// parent map is reused when the element declares no namespace, so only declaring
// elements allocate.
func docxNamespaceScope(parent map[string]string, attrs []xml.Attr) map[string]string {
	declared := 0
	for _, attr := range attrs {
		if attr.Name.Space == "xmlns" || (attr.Name.Space == "" && attr.Name.Local == "xmlns") {
			declared++
		}
	}
	if declared == 0 {
		return parent
	}
	scope := make(map[string]string, len(parent)+declared)
	for prefix, uri := range parent {
		scope[prefix] = uri
	}
	for _, attr := range attrs {
		switch {
		case attr.Name.Space == "xmlns":
			if attr.Value == "" {
				delete(scope, attr.Name.Local)
				continue
			}
			scope[attr.Name.Local] = attr.Value
		case attr.Name.Space == "" && attr.Name.Local == "xmlns":
			if attr.Value == "" {
				delete(scope, "")
				continue
			}
			scope[""] = attr.Value
		}
	}
	return scope
}

func (tree *docxXMLTree) characters(token xml.CharData) error {
	if len(tree.stack) == 0 {
		if strings.TrimSpace(string(token)) != "" {
			return fmt.Errorf("DOCX: text outside XML root")
		}
		return nil
	}
	node := tree.stack[len(tree.stack)-1]
	node.text += string(token)
	return nil
}
