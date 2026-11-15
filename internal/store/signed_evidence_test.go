// File metadata (manifest mirror):
// Purpose: Exercises frozen-evidence reference and anchor matching plus report/input identity comparison used before an export bundle is signed.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"testing"

	"contractaudit/internal/docir"
)

const evidenceTestDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func evidenceTestDocument() FrozenDocument {
	page := 1
	text := "第一条 付款需先完成验收"
	normalized := evidenceTestDigest
	return FrozenDocument{
		Ready:                true,
		Document:             Document{ID: "doc_evidence"},
		ParsedDocumentID:     "parsed_evidence",
		ParsedArtifactDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Text:                 text,
		NormalizedHash:       &normalized,
		IR: docir.IR{
			NormalizedText: text,
			Pages:          []docir.Page{{Number: page, Start: 0, End: 12}},
			Blocks:         []docir.Block{{ID: "block-1", Kind: "paragraph", Text: text, Start: 0, End: 12, Page: &page}},
		},
	}
}

func evidenceTestReference() ExperienceEvidenceRef {
	return ExperienceEvidenceRef{
		DocumentID: "doc_evidence", ParsedDocumentID: "parsed_evidence",
		NormalizedDigest: evidenceTestDigest, Quote: "付款需先完成验收", Start: 4, End: 12,
	}
}

func TestEvidenceReferenceMatchesFrozenParse(t *testing.T) {
	doc := evidenceTestDocument()
	valid := evidenceTestReference()
	if !evidenceReferenceMatches(valid, doc) {
		t.Fatal("valid reference did not match the frozen parse")
	}
	notReady := doc
	notReady.Ready = false
	if evidenceReferenceMatches(valid, notReady) {
		t.Fatal("reference matched a document that is not ready")
	}
	noHash := doc
	noHash.NormalizedHash = nil
	if evidenceReferenceMatches(valid, noHash) {
		t.Fatal("reference matched a document without a normalized hash")
	}
	cases := []struct {
		name   string
		mutate func(*ExperienceEvidenceRef)
	}{
		{"document identity", func(ref *ExperienceEvidenceRef) { ref.DocumentID = "doc_other" }},
		{"parsed identity", func(ref *ExperienceEvidenceRef) { ref.ParsedDocumentID = "parsed_other" }},
		{"missing digest", func(ref *ExperienceEvidenceRef) { ref.NormalizedDigest = "" }},
		{"digest mismatch", func(ref *ExperienceEvidenceRef) {
			ref.NormalizedDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		}},
		{"empty quote", func(ref *ExperienceEvidenceRef) { ref.Quote = "" }},
		{"span out of range", func(ref *ExperienceEvidenceRef) { ref.End = 200 }},
		{"quote mismatch", func(ref *ExperienceEvidenceRef) { ref.Quote = "付款需先完成" }},
	}
	for _, check := range cases {
		ref := valid
		check.mutate(&ref)
		if evidenceReferenceMatches(ref, doc) {
			t.Fatalf("reference with %s matched", check.name)
		}
	}
}

func TestEvidenceAnchorMatchesFrozenParse(t *testing.T) {
	doc := evidenceTestDocument()
	one := 1
	valid := Anchor{
		ID: "anchor-1", DocumentID: "doc_evidence", ParsedDocumentID: "parsed_evidence",
		NormalizedDigest: evidenceTestDigest, Quote: "付款需先完成验收",
		Status: "located", Start: 4, End: 12, Page: &one, ElementID: "block-1",
	}
	if !evidenceAnchorMatches(valid, doc) {
		t.Fatal("valid anchor did not match the frozen parse")
	}
	minimal := Anchor{DocumentID: "doc_evidence", Quote: "付款需先完成验收", Status: "located", Start: 4, End: 12}
	if !evidenceAnchorMatches(minimal, doc) {
		t.Fatal("anchor without optional parse identity did not match")
	}
	two := 2
	cases := []struct {
		name   string
		mutate func(*Anchor)
	}{
		{"document identity", func(anchor *Anchor) { anchor.DocumentID = "doc_other" }},
		{"empty quote", func(anchor *Anchor) { anchor.Quote = "" }},
		{"empty span", func(anchor *Anchor) { anchor.End = anchor.Start }},
		{"parsed identity without digest", func(anchor *Anchor) { anchor.NormalizedDigest = "" }},
		{"parsed identity mismatch", func(anchor *Anchor) { anchor.ParsedDocumentID = "parsed_other" }},
		{"digest mismatch", func(anchor *Anchor) {
			anchor.NormalizedDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		}},
		{"quote mismatch", func(anchor *Anchor) { anchor.Quote = "付款需先完成" }},
		{"element mismatch", func(anchor *Anchor) { anchor.ElementID = "block-other" }},
		{"page mismatch", func(anchor *Anchor) { anchor.Page = &two }},
	}
	for _, check := range cases {
		anchor := valid
		check.mutate(&anchor)
		if evidenceAnchorMatches(anchor, doc) {
			t.Fatalf("anchor with %s matched", check.name)
		}
	}
}

func TestEvidenceReportReferencesRejectEveryIdentityMismatch(t *testing.T) {
	doc := evidenceTestDocument()
	input := AttemptInput{AttemptID: "att_evidence", RunID: "run_evidence", Documents: []FrozenDocument{doc}}
	report := reportEnvelope{
		Case:    reportCase{ID: "case_evidence"},
		Run:     Run{ID: "run_evidence"},
		Attempt: Attempt{ID: "att_evidence"},
		Documents: []reportDocument{{
			Document: doc.Document,
			Parsed:   ParsedDocument{ID: doc.ParsedDocumentID, ArtifactDigest: doc.ParsedArtifactDigest, Text: doc.Text},
		}},
		Findings: []Finding{{
			CaseID: "case_evidence", RunID: "run_evidence", AttemptID: "att_evidence",
			Anchors: []Anchor{
				{DocumentID: "doc_evidence", ParsedDocumentID: "parsed_evidence", NormalizedDigest: evidenceTestDigest, Quote: "付款需先完成验收", Status: "located", Start: 4, End: 12},
				{DocumentID: "doc_missing", Status: "unlocated"},
			},
		}},
		Evaluations: []ConstraintEval{{
			CaseID: "case_evidence", RunID: "run_evidence", AttemptID: "att_evidence",
			Evidence: []EvaluationEvidence{{FactID: "fact-1", Ref: evidenceTestReference()}},
		}},
	}
	if err := verifyEvidenceReportReferences(report, input); err != nil {
		t.Fatalf("consistent report was rejected: %v", err)
	}

	countMismatch := report
	countMismatch.Documents = append(append([]reportDocument{}, report.Documents...), report.Documents[0])
	if err := verifyEvidenceReportReferences(countMismatch, input); err == nil {
		t.Fatal("document count mismatch was accepted")
	}
	duplicate := input
	duplicate.Documents = append(append([]FrozenDocument{}, input.Documents...), doc)
	if err := verifyEvidenceReportReferences(report, duplicate); err == nil {
		t.Fatal("duplicate frozen document was accepted")
	}
	documentMismatch := report
	documentMismatch.Documents = []reportDocument{{Document: Document{ID: "doc_other"}, Parsed: report.Documents[0].Parsed}}
	if err := verifyEvidenceReportReferences(documentMismatch, input); err == nil {
		t.Fatal("document identity mismatch was accepted")
	}
	parsedIdentity := report
	parsedIdentity.Documents = []reportDocument{{Document: doc.Document, Parsed: ParsedDocument{ID: "parsed_other", ArtifactDigest: doc.ParsedArtifactDigest, Text: doc.Text}}}
	if err := verifyEvidenceReportReferences(parsedIdentity, input); err == nil {
		t.Fatal("parsed identity mismatch was accepted")
	}
	artifactDigest := report
	artifactDigest.Documents = []reportDocument{{Document: doc.Document, Parsed: ParsedDocument{ID: doc.ParsedDocumentID, ArtifactDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", Text: doc.Text}}}
	if err := verifyEvidenceReportReferences(artifactDigest, input); err == nil {
		t.Fatal("artifact digest mismatch was accepted")
	}
	parsedText := report
	parsedText.Documents = []reportDocument{{Document: doc.Document, Parsed: ParsedDocument{ID: doc.ParsedDocumentID, ArtifactDigest: doc.ParsedArtifactDigest, Text: "tampered"}}}
	if err := verifyEvidenceReportReferences(parsedText, input); err == nil {
		t.Fatal("parsed text mismatch was accepted")
	}

	findingIdentity := report
	findingIdentity.Findings = append([]Finding{}, report.Findings...)
	findingIdentity.Findings[0].RunID = "run_other"
	if err := verifyEvidenceReportReferences(findingIdentity, input); err == nil {
		t.Fatal("finding identity mismatch was accepted")
	}
	anchorMismatch := report
	anchorMismatch.Findings = append([]Finding{}, report.Findings...)
	anchorMismatch.Findings[0].Anchors = append([]Anchor{}, report.Findings[0].Anchors...)
	anchorMismatch.Findings[0].Anchors[0].Quote = "tampered"
	if err := verifyEvidenceReportReferences(anchorMismatch, input); err == nil {
		t.Fatal("anchor mismatch was accepted")
	}

	evaluationIdentity := report
	evaluationIdentity.Evaluations = append([]ConstraintEval{}, report.Evaluations...)
	evaluationIdentity.Evaluations[0].AttemptID = "att_other"
	if err := verifyEvidenceReportReferences(evaluationIdentity, input); err == nil {
		t.Fatal("evaluation identity mismatch was accepted")
	}
	evaluationRef := report
	evaluationRef.Evaluations = append([]ConstraintEval{}, report.Evaluations...)
	evaluationRef.Evaluations[0].Evidence = []EvaluationEvidence{{Ref: ExperienceEvidenceRef{DocumentID: "doc_missing", ParsedDocumentID: "parsed_evidence", NormalizedDigest: evidenceTestDigest, Quote: "付款需先完成验收", Start: 4, End: 12}}}
	if err := verifyEvidenceReportReferences(evaluationRef, input); err == nil {
		t.Fatal("evaluation reference mismatch was accepted")
	}
}

func TestEqualEvidenceJSONComparesCanonicalIdentity(t *testing.T) {
	left := Document{ID: "doc_1", TenantID: "tenant-1", Digest: "sha256:aa"}
	if !equalEvidenceJSON(left, Document{Digest: "sha256:aa", TenantID: "tenant-1", ID: "doc_1"}) {
		t.Fatal("equal documents with different field order compared unequal")
	}
	if equalEvidenceJSON(left, Document{ID: "doc_2", TenantID: "tenant-1", Digest: "sha256:aa"}) {
		t.Fatal("different documents compared equal")
	}
	if equalEvidenceJSON(left, make(chan int)) {
		t.Fatal("an unserializable value compared equal")
	}
}
