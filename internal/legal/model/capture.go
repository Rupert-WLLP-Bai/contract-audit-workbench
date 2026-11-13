package model

import (
	knowledgeModel "contractaudit/internal/knowledge/model"
	"contractaudit/internal/legalnorm"
	"encoding/json"
)

type LegalSourceCapture struct {
	CaptureSnapshotID string                        `json:"capture_snapshot_id"`
	SourceID          string                        `json:"source_id"`
	NormVersion       string                        `json:"norm_version"`
	NormTitle         string                        `json:"norm_title"`
	DocumentType      string                        `json:"document_type"`
	Publisher         string                        `json:"publisher"`
	Issuer            string                        `json:"issuer"`
	OfficialURL       string                        `json:"official_url"`
	Provenance        string                        `json:"provenance"`
	PromulgatedOn     string                        `json:"promulgated_on"`
	EffectiveFrom     string                        `json:"effective_from"`
	EffectiveUntil    string                        `json:"effective_until,omitempty"`
	SourceCapturedAt  string                        `json:"source_captured_at"`
	ResponseStatus    int                           `json:"response_status"`
	MediaType         string                        `json:"media_type"`
	RawResponse       []byte                        `json:"raw_response,omitempty"`
	RawSHA256         string                        `json:"raw_sha256"`
	ExtractedFullText string                        `json:"extracted_full_text"`
	ExtractedSHA256   string                        `json:"extracted_full_text_sha256"`
	ExtractorID       string                        `json:"extractor_id"`
	ExtractorVersion  string                        `json:"extractor_version"`
	CanonicalVersion  string                        `json:"canonicalization_version"`
	StatusID          string                        `json:"status_observation_id"`
	Status            string                        `json:"observed_status"`
	StatusRegistryURL string                        `json:"status_registry_url"`
	StatusMethod      string                        `json:"status_request_method"`
	StatusRequestBody []byte                        `json:"status_request_body,omitempty"`
	StatusObservedAt  string                        `json:"status_observed_at"`
	StatusHTTPStatus  int                           `json:"status_response_status"`
	StatusMediaType   string                        `json:"status_media_type"`
	StatusResponse    []byte                        `json:"status_response,omitempty"`
	StatusSHA256      string                        `json:"status_response_sha256"`
	StatusMapping     json.RawMessage               `json:"status_mapping_evidence"`
	Articles          []legalnorm.VerifiedArticle   `json:"articles"`
	ImportedArticles  []knowledgeModel.KnowledgeRef `json:"imported_articles"`
	RequestDigest     string                        `json:"request_digest"`
	IdempotencyKey    string                        `json:"idempotency_key"`
	CreatedBy         string                        `json:"created_by"`
	CreatedAt         string                        `json:"created_at"`
}
