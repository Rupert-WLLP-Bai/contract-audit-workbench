// File metadata (manifest mirror):
// Purpose: Defines Legal import wire values and pure request digest, article manifest, version mapping, and metadata rules.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"contractaudit/internal/fault"
	knowledgeModel "contractaudit/internal/knowledge/model"
	"contractaudit/internal/legalnorm"
)

type LegalArticleVersionInput struct {
	ArticleNumber  int    `json:"article_number"`
	KnowledgeID    string `json:"knowledge_id"`
	DisplayVersion string `json:"display_version"`
}

// LegalSourceImportInput is an assertion-bearing request. Capture article
// bodies are checked by legalnorm against raw bytes; governed content is built
// from VerifiedArticle.FullText, never from the asserted text.
type LegalSourceImportInput struct {
	Capture        legalnorm.CaptureInput     `json:"capture"`
	Articles       []LegalArticleVersionInput `json:"articles"`
	IdempotencyKey string                     `json:"idempotency_key"`
}

type LegalSourceImportResult struct {
	CaptureSnapshotID string                        `json:"capture_snapshot_id"`
	SourceID          string                        `json:"source_id"`
	NormVersion       string                        `json:"norm_version"`
	RawSHA256         string                        `json:"raw_sha256"`
	ExtractedSHA256   string                        `json:"extracted_full_text_sha256"`
	StatusSHA256      string                        `json:"status_evidence_sha256"`
	Provenance        string                        `json:"provenance"`
	Articles          []knowledgeModel.KnowledgeRef `json:"articles"`
}

func ArticleManifest(articles []legalnorm.VerifiedArticle) ([]byte, error) {
	canonical := make([]legalnorm.VerifiedArticle, len(articles))
	for i, article := range articles {
		canonical[i] = article
		canonical[i].Ref.FullText = article.FullText
		canonical[i].Ref.TextSHA256 = article.TextSHA256
	}
	return json.Marshal(canonical)
}

func RequestDigest(input LegalSourceImportInput) (string, error) {
	capture := input.Capture
	capture.SourceSnapshotID = ""
	capture.Articles = append([]legalnorm.ArticleRef(nil), input.Capture.Articles...)
	for i := range capture.Articles {
		capture.Articles[i].SourceSnapshotID = ""
	}
	canonical, err := json.Marshal(struct {
		Capture  legalnorm.CaptureInput     `json:"capture"`
		Articles []LegalArticleVersionInput `json:"articles"`
	}{capture, input.Articles})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func RequestVersions(articles []LegalArticleVersionInput, verified []legalnorm.VerifiedArticle) (map[int]LegalArticleVersionInput, error) {
	if len(articles) != len(verified) || len(articles) == 0 || len(articles) > 100 {
		return nil, fault.Invalid("法律条文版本清单与服务端提取结果数量不一致")
	}
	byNumber := make(map[int]LegalArticleVersionInput, len(articles))
	for _, article := range articles {
		if article.ArticleNumber <= 0 || !knowledgeModel.ValidResourceID(article.KnowledgeID) || !knowledgeModel.ValidResourceID(article.DisplayVersion) {
			return nil, fault.Invalid("法律条文治理编号无效")
		}
		if _, exists := byNumber[article.ArticleNumber]; exists {
			return nil, fault.Invalid("法律条文治理编号重复")
		}
		byNumber[article.ArticleNumber] = article
	}
	for _, article := range verified {
		if _, exists := byNumber[article.Ref.ArticleNumber]; !exists {
			return nil, fault.Invalid("法律条文治理清单缺少已提取条号")
		}
	}
	return byNumber, nil
}

func MetadataFor(verified legalnorm.VerifiedBaseline, article legalnorm.VerifiedArticle) *knowledgeModel.LegalMetadata {
	capture := verified.Capture
	return &knowledgeModel.LegalMetadata{
		SourceID: capture.SourceID, NormTitle: capture.Title, DocumentType: verified.DocumentType,
		NormVersion: capture.VersionID, ArticleNumber: article.Ref.ArticleNumber, ArticleLabel: article.Ref.ArticleLabel,
		Issuer: verified.Issuer, Publisher: verified.Publisher, OfficialURL: capture.OfficialURL,
		PromulgatedOn: capture.PromulgatedOn, EffectiveFrom: capture.EffectiveFrom, EffectiveUntil: capture.EffectiveUntil,
		OfficialStatusObservationID: capture.StatusEvidenceID, OfficialStatus: capture.CurrencyStatus,
		StatusRegistryURL: verified.StatusRegistryURL, StatusObservedAt: capture.StatusObservedAt,
		StatusEvidenceSHA256: capture.StatusEvidenceSHA256, StatusResponseStatus: verified.StatusResponseStatus,
		StatusContentType: verified.StatusContentType, SourceSnapshotID: capture.SourceSnapshotID,
		SourceProvenance: verified.Provenance, SourceCapturedAt: capture.SourceCapturedAt,
		SourceResponseStatus: verified.SourceResponseStatus, SourceContentType: capture.CapturedContentType,
		SourceSHA256: capture.RawSHA256, NormTextSHA256: article.TextSHA256,
		CanonicalTextVersion: capture.CanonicalTextVersion, SourceTextStart: article.Start, SourceTextEnd: article.End,
		ExtractorID: capture.ExtractorID, ExtractorVersion: capture.ExtractorVersion,
		TextCheckStatus: "PASS", LegalReviewStatus: "NOT_RUN", Reviewer: nil,
	}
}
