// Package postgres reads and writes Legal-owned captures through a caller-bound executor.
package postgres

import (
	"context"
	"errors"

	"contractaudit/internal/legal/app"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type Captures struct{ db platformpostgres.Executor }

func NewCaptures(db platformpostgres.Executor) *Captures { return &Captures{db: db} }
func (p *Captures) ReadCapture(ctx context.Context, tenant, captureID string) (app.RawCapture, bool, error) {
	var raw app.RawCapture
	capture := &raw.Capture
	err := p.db.QueryRow(ctx, `SELECT capture_snapshot_id,source_id,norm_version,norm_title,document_type,publisher,issuer,official_url,provenance,
		source_captured_at,promulgated_on::text,effective_from::text,COALESCE(effective_until::text,''),response_status,media_type,raw_response_bytes,raw_response_sha256,extracted_full_text,extracted_full_text_sha256,
		extractor_id,extractor_version,canonicalization_version,status_observation_id,observed_status,status_registry_url,status_request_method,status_request_body,
		status_observed_at,status_response_status,status_media_type,status_response_bytes,status_response_sha256,status_mapping_evidence,article_manifest,article_manifest_sha256,
		imported_article_refs,request_digest,idempotency_key,created_by,created_at
		FROM legal_source_captures WHERE tenant_id=$1 AND capture_snapshot_id=$2`, tenant, captureID).Scan(
		&capture.CaptureSnapshotID, &capture.SourceID, &capture.NormVersion, &capture.NormTitle, &capture.DocumentType, &capture.Publisher, &capture.Issuer, &capture.OfficialURL, &capture.Provenance,
		&raw.SourceAt, &capture.PromulgatedOn, &capture.EffectiveFrom, &capture.EffectiveUntil, &capture.ResponseStatus, &capture.MediaType, &capture.RawResponse, &capture.RawSHA256, &capture.ExtractedFullText, &capture.ExtractedSHA256,
		&capture.ExtractorID, &capture.ExtractorVersion, &capture.CanonicalVersion, &capture.StatusID, &capture.Status, &capture.StatusRegistryURL, &capture.StatusMethod, &capture.StatusRequestBody,
		&raw.StatusAt, &capture.StatusHTTPStatus, &capture.StatusMediaType, &capture.StatusResponse, &capture.StatusSHA256, &raw.StatusMapping, &raw.ArticleManifest, &raw.ArticleManifestSHA,
		&raw.ImportedRefs, &capture.RequestDigest, &capture.IdempotencyKey, &capture.CreatedBy, &raw.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.RawCapture{}, false, nil
	}
	return raw, err == nil, err
}

func (p *Captures) FindReplay(ctx context.Context, tenant, actor, key string) (app.CaptureReplay, bool, error) {
	var out app.CaptureReplay
	err := p.db.QueryRow(ctx, `SELECT request_digest,capture_snapshot_id,imported_article_refs FROM legal_source_captures WHERE tenant_id=$1 AND created_by=$2 AND idempotency_key=$3`, tenant, actor, key).Scan(&out.RequestDigest, &out.CaptureID, &out.ImportedRefs)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.CaptureReplay{}, false, nil
	}
	return out, err == nil, err
}
func (p *Captures) ReadSummary(ctx context.Context, tenant, captureID string) (app.CaptureSummary, error) {
	var out app.CaptureSummary
	err := p.db.QueryRow(ctx, `SELECT capture_snapshot_id,source_id,norm_version,raw_response_sha256,extracted_full_text_sha256,status_response_sha256,provenance FROM legal_source_captures WHERE tenant_id=$1 AND capture_snapshot_id=$2`, tenant, captureID).Scan(&out.CaptureID, &out.SourceID, &out.NormVersion, &out.RawSHA256, &out.ExtractedSHA256, &out.StatusSHA256, &out.Provenance)
	return out, err
}
func (p *Captures) InsertCapture(ctx context.Context, record app.CaptureInsert) error {
	verified, input := record.Verified, record.Input
	capture := verified.Capture
	statusRequestBody := verified.StatusRequestBody
	if statusRequestBody == nil {
		statusRequestBody = []byte{}
	}
	_, err := p.db.Exec(ctx, `INSERT INTO legal_source_captures
 (tenant_id,capture_snapshot_id,source_id,norm_version,norm_title,document_type,publisher,issuer,official_url,provenance,
 source_captured_at,promulgated_on,effective_from,effective_until,response_status,media_type,raw_response_bytes,raw_response_sha256,
 extracted_full_text,extracted_full_text_sha256,extractor_id,extractor_version,canonicalization_version,
 status_observation_id,observed_status,status_registry_url,status_request_method,status_request_body,status_observed_at,
 status_response_status,status_media_type,status_response_bytes,status_response_sha256,status_mapping_evidence,article_manifest,article_manifest_sha256,
 request_digest,idempotency_key,imported_article_refs,created_by)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::date,$13::date,NULLIF($14,'')::date,$15,$16,$17,$18,
 $19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40)`,
		record.TenantID, record.CaptureID, capture.SourceID, capture.VersionID, capture.Title, verified.DocumentType, verified.Publisher, verified.Issuer, capture.OfficialURL, verified.Provenance,
		capture.SourceCapturedAt, capture.PromulgatedOn, capture.EffectiveFrom, capture.EffectiveUntil, verified.SourceResponseStatus, capture.CapturedContentType, input.RawBytes, capture.RawSHA256,
		capture.FullText, capture.FullTextSHA256, capture.ExtractorID, capture.ExtractorVersion, capture.CanonicalTextVersion,
		capture.StatusEvidenceID, capture.CurrencyStatus, verified.StatusRegistryURL, verified.StatusRequestMethod, statusRequestBody, capture.StatusObservedAt,
		verified.StatusResponseStatus, verified.StatusContentType, input.StatusEvidenceRaw, capture.StatusEvidenceSHA256, record.StatusMapping, record.ArticleManifest, record.ArticleManifestSHA,
		record.RequestDigest, record.IdempotencyKey, record.ImportedRefs, record.ActorID)
	return err
}
