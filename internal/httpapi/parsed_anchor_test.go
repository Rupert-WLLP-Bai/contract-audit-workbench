package httpapi

import (
	"strings"
	"testing"

	"contractaudit/internal/docir"
	"contractaudit/internal/store"
)

func TestLocatedAnchorRequiresItsFrozenParsedIdentity(t *testing.T) {
	ir := docir.Build("质保😀36个月。")
	parsed := &store.ParsedDocument{ID: "parsed-old", DocumentID: "doc-1", OriginalDigest: "original-1", Status: "ready", NormalizedDigest: ir.NormalizedDigest}
	doc := store.Interpreted{Document: store.Document{ID: "doc-1", Digest: "original-1"}, Ready: true, Text: ir.NormalizedText, IR: ir, ParsedDocument: parsed}
	anchor := store.Anchor{ID: "anchor-1", DocumentID: "doc-1", ParsedDocumentID: "parsed-old", NormalizedDigest: ir.NormalizedDigest,
		Quote: ir.NormalizedText, Status: "located", Start: 0, End: len([]rune(ir.NormalizedText))}
	valid := anchorJSON(anchor, doc)
	if valid["status"] != "located" || valid["text_offset"] == nil || valid["parsed_document_id"] != "parsed-old" || valid["bbox"] != nil {
		t.Fatalf("valid frozen source was not located: %+v", valid)
	}
	for name, alter := range map[string]func(*store.Interpreted){
		"different-parse-identical-text": func(d *store.Interpreted) {
			copy := *d.ParsedDocument
			copy.ID = "parsed-new"
			d.ParsedDocument = &copy
		},
		"different-document-identical-text": func(d *store.Interpreted) { d.Document.ID = "doc-2" },
		"different-text-digest":             func(d *store.Interpreted) { d.IR.NormalizedDigest = strings.Repeat("0", 64) },
		"different-original":                func(d *store.Interpreted) { d.Document.Digest = "original-2" },
		"failed-parse": func(d *store.Interpreted) {
			copy := *d.ParsedDocument
			copy.Status = "failed"
			d.ParsedDocument = &copy
		},
		"missing-parsed-source": func(d *store.Interpreted) { d.ParsedDocument = nil },
	} {
		t.Run(name, func(t *testing.T) {
			changed := doc
			alter(&changed)
			got := anchorJSON(anchor, changed)
			if got["status"] != "unlocatable" || got["text_offset"] != nil || got["page"] != nil || got["bbox"] != nil || got["mismatch_reason"] == nil {
				t.Fatalf("mismatched parsed source retained coordinates: %+v", got)
			}
		})
	}
}
