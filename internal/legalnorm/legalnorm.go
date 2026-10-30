// Package legalnorm verifies captured official normative sources without a
// database dependency. It derives article text from raw response bytes and
// rejects client-supplied article text that differs from that extraction.
package legalnorm

import "strings"

const (
	CanonicalTextVersion  = "legalnorm-whitespace-v1"
	StatusEffective       = "effective"
	StatusRepealed        = "repealed"
	StatusNotYetEffective = "not_yet_effective"
)

// TextSpan uses zero-based Unicode code-point offsets into an article's
// canonical FullText. End is exclusive.
type TextSpan struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Quote string `json:"quote"`
}

// ArticleRef identifies one immutable article edition and its body digest.
// FullText and CitationSpan are import assertions; they are never trusted as
// the source of the verified body.
type ArticleRef struct {
	SourceID         string   `json:"source_id"`
	SourceSnapshotID string   `json:"source_snapshot_id"`
	VersionID        string   `json:"version_id"`
	ArticleNumber    int      `json:"article_number"`
	ArticleLabel     string   `json:"article_label"`
	FullText         string   `json:"full_text"`
	TextSHA256       string   `json:"text_sha256"`
	CitationSpan     TextSpan `json:"citation_span,omitempty"`
	PromulgatedOn    string   `json:"promulgated_on"`
	EffectiveFrom    string   `json:"effective_from"`
	EffectiveUntil   string   `json:"effective_until,omitempty"`
	CurrencyStatus   string   `json:"currency_status"`
	StatusEvidenceID string   `json:"status_evidence_id"`
}

// CaptureInput carries raw captured response bytes and metadata assertions.
// All digests and article text are recomputed and checked by VerifyCapture.
type CaptureInput struct {
	SourceID             string       `json:"source_id"`
	SourceSnapshotID     string       `json:"source_snapshot_id"`
	VersionID            string       `json:"version_id"`
	Title                string       `json:"title"`
	OfficialURL          string       `json:"official_url"`
	ContentType          string       `json:"content_type"`
	RawBytes             []byte       `json:"raw_bytes"`
	ExpectedRawSHA256    string       `json:"raw_sha256"`
	SourceCapturedAt     string       `json:"source_captured_at"`
	PromulgatedOn        string       `json:"promulgated_on"`
	EffectiveFrom        string       `json:"effective_from"`
	EffectiveUntil       string       `json:"effective_until,omitempty"`
	CurrencyStatus       string       `json:"currency_status"`
	StatusEvidenceID     string       `json:"status_evidence_id"`
	StatusObservedAt     string       `json:"status_observed_at"`
	StatusEvidenceRaw    []byte       `json:"status_evidence_raw"`
	ExpectedStatusSHA256 string       `json:"status_evidence_sha256"`
	Articles             []ArticleRef `json:"articles"`
}

// VerifiedArticle contains the server-derived complete body and rune offsets
// into VerifiedCapture.FullText.
type VerifiedArticle struct {
	Ref        ArticleRef `json:"ref"`
	FullText   string     `json:"full_text"`
	TextSHA256 string     `json:"text_sha256"`
	Start      int        `json:"start"`
	End        int        `json:"end"`
}

// VerifiedCapture is the integrity-checked result of extracting raw source
// and registry bytes using a versioned adapter.
type VerifiedCapture struct {
	SourceID             string            `json:"source_id"`
	SourceSnapshotID     string            `json:"source_snapshot_id"`
	VersionID            string            `json:"version_id"`
	Title                string            `json:"title"`
	OfficialURL          string            `json:"official_url"`
	CapturedContentType  string            `json:"content_type"`
	RawSHA256            string            `json:"raw_sha256"`
	SourceCapturedAt     string            `json:"source_captured_at"`
	FullText             string            `json:"full_text"`
	FullTextSHA256       string            `json:"full_text_sha256"`
	ExtractorID          string            `json:"extractor_id"`
	ExtractorVersion     string            `json:"extractor_version"`
	CanonicalTextVersion string            `json:"canonical_text_version"`
	PromulgatedOn        string            `json:"promulgated_on"`
	EffectiveFrom        string            `json:"effective_from"`
	EffectiveUntil       string            `json:"effective_until,omitempty"`
	CurrencyStatus       string            `json:"currency_status"`
	StatusEvidenceID     string            `json:"status_evidence_id"`
	StatusObservedAt     string            `json:"status_observed_at"`
	StatusEvidenceSHA256 string            `json:"status_evidence_sha256"`
	Articles             []VerifiedArticle `json:"articles"`
}

// NormalizeWhitespace applies the versioned canonicalization used for source
// and article text: Unicode whitespace runs collapse to one ASCII space, with
// leading and trailing whitespace removed.
func NormalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
