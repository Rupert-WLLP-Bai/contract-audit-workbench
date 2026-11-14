// File metadata (manifest mirror):
// Purpose: Reads exact and batched Legal reference capture projections while preserving original query and scan behavior.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package postgres

import (
	"context"
	"errors"

	"contractaudit/internal/legal/model"
	"github.com/jackc/pgx/v5"
)

const referenceColumns = `capture_snapshot_id,source_id,norm_version,norm_title,document_type,publisher,issuer,official_url,provenance,
 source_captured_at,promulgated_on::text,effective_from::text,COALESCE(effective_until::text,''),response_status,media_type,raw_response_bytes,raw_response_sha256,extracted_full_text,extracted_full_text_sha256,
 extractor_id,extractor_version,canonicalization_version,status_observation_id,observed_status,status_registry_url,
 status_request_method,status_request_body,status_observed_at,status_response_status,status_media_type,status_response_bytes,status_response_sha256,article_manifest,article_manifest_sha256`

func scanReference(row pgx.Row) (model.ReferenceCapture, error) {
	var c model.ReferenceCapture
	err := row.Scan(&c.ID, &c.SourceID, &c.VersionID, &c.NormTitle, &c.DocumentType, &c.Publisher, &c.Issuer, &c.OfficialURL, &c.Provenance,
		&c.SourceCapturedAt, &c.PromulgatedOn, &c.EffectiveFrom, &c.EffectiveUntil, &c.ResponseStatus, &c.MediaType, &c.Raw, &c.RawSHA, &c.Extracted, &c.ExtractedSHA,
		&c.ExtractorID, &c.ExtractorVersion, &c.CanonicalVersion, &c.StatusID, &c.Status, &c.StatusRegistryURL,
		&c.StatusMethod, &c.StatusRequestBody, &c.StatusObservedAt, &c.StatusResponseStatus, &c.StatusMediaType, &c.StatusRaw, &c.StatusSHA, &c.ArticleRaw, &c.ArticleManifestSHA)
	return c, err
}
func (p *Captures) ReadReference(ctx context.Context, tenant, id string) (model.ReferenceCapture, bool, error) {
	c, err := scanReference(p.db.QueryRow(ctx, `SELECT `+referenceColumns+` FROM legal_source_captures WHERE tenant_id=$1 AND capture_snapshot_id=$2`, tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ReferenceCapture{}, false, nil
	}
	return c, err == nil, err
}
func (p *Captures) ReadReferences(ctx context.Context, tenant string, ids []string) (map[string]model.ReferenceCapture, error) {
	out := make(map[string]model.ReferenceCapture, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := p.db.Query(ctx, `SELECT `+referenceColumns+` FROM legal_source_captures WHERE tenant_id=$1 AND capture_snapshot_id=ANY($2::text[])`, tenant, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanReference(rows)
		if err != nil {
			return nil, err
		}
		out[c.ID] = c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
