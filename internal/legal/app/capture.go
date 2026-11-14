package app

import (
	"context"
	"encoding/json"
	"time"

	"contractaudit/internal/fault"
	knowledgeModel "contractaudit/internal/knowledge/model"
	legalModel "contractaudit/internal/legal/model"
	"contractaudit/internal/legalnorm"
)

type RawCapture struct {
	Capture                                      legalModel.LegalSourceCapture
	SourceAt, StatusAt, CreatedAt                time.Time
	ArticleManifest, ImportedRefs, StatusMapping []byte
	ArticleManifestSHA                           string
}
type CaptureReader interface {
	ReadCapture(context.Context, string, string) (RawCapture, bool, error)
}

func LoadCapture(ctx context.Context, repo CaptureReader, tenant, captureID string) (legalModel.LegalSourceCapture, error) {
	raw, found, err := repo.ReadCapture(ctx, tenant, captureID)
	if err != nil {
		return legalModel.LegalSourceCapture{}, err
	}
	if !found {
		return legalModel.LegalSourceCapture{}, fault.NotFound("没有找到法律来源快照")
	}
	capture := raw.Capture
	capture.SourceCapturedAt = raw.SourceAt.UTC().Format(time.RFC3339Nano)
	capture.StatusObservedAt = raw.StatusAt.UTC().Format(time.RFC3339Nano)
	capture.CreatedAt = raw.CreatedAt.UTC().Format(time.RFC3339Nano)
	if knowledgeModel.SHA256(capture.RawResponse) != capture.RawSHA256 || knowledgeModel.SHA256([]byte(capture.ExtractedFullText)) != capture.ExtractedSHA256 ||
		knowledgeModel.SHA256(capture.StatusResponse) != capture.StatusSHA256 || !knowledgeModel.ValidHash(capture.RequestDigest) {
		return legalModel.LegalSourceCapture{}, fault.Conflict("法律来源快照的摘要校验失败")
	}
	if err := json.Unmarshal(raw.ArticleManifest, &capture.Articles); err != nil || len(capture.Articles) == 0 {
		return legalModel.LegalSourceCapture{}, fault.Conflict("法律来源快照的条文索引无效")
	}
	canonicalArticles, err := legalModel.ArticleManifest(capture.Articles)
	if err != nil || knowledgeModel.SHA256(canonicalArticles) != raw.ArticleManifestSHA {
		return legalModel.LegalSourceCapture{}, fault.Conflict("法律来源快照的条文索引不是规范编码")
	}
	if err := json.Unmarshal(raw.ImportedRefs, &capture.ImportedArticles); err != nil {
		return legalModel.LegalSourceCapture{}, fault.Conflict("法律来源导入版本索引无效")
	}
	var mapping map[string]any
	if err := json.Unmarshal(raw.StatusMapping, &mapping); err != nil || len(mapping) == 0 {
		return legalModel.LegalSourceCapture{}, fault.Conflict("法律效力状态映射证据无效")
	}
	capture.StatusMapping = append(json.RawMessage(nil), raw.StatusMapping...)
	fullText := []rune(capture.ExtractedFullText)
	for _, article := range capture.Articles {
		if article.Start < 0 || article.End <= article.Start || article.End > len(fullText) ||
			string(fullText[article.Start:article.End]) != article.FullText || article.Ref.FullText != article.FullText ||
			knowledgeModel.SHA256([]byte(article.FullText)) != article.TextSHA256 {
			return legalModel.LegalSourceCapture{}, fault.Conflict("法律来源快照中的条文正文或位置损坏")
		}
	}
	return capture, nil
}

// CaptureReplay is the stored Legal idempotency identity, before Knowledge ref checks.
type CaptureReplay struct {
	RequestDigest, CaptureID string
	ImportedRefs             []byte
}

// CaptureSummary is the intentionally narrow projection used by replay.
type CaptureSummary struct {
	CaptureID, SourceID, NormVersion, RawSHA256, ExtractedSHA256, StatusSHA256, Provenance string
}

// CaptureInsert holds the original verified capture and exact serialized manifests.
type CaptureInsert struct {
	TenantID, CaptureID, ActorID, RequestDigest, IdempotencyKey string
	Verified                                                    legalnorm.VerifiedBaseline
	Input                                                       legalnorm.CaptureInput
	StatusMapping, ArticleManifest, ImportedRefs                []byte
	ArticleManifestSHA                                          string
}
