// File metadata (manifest mirror):
// Purpose: Validates frozen Legal article and source/status capture identities through exact immutable Knowledge and Legal facts.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"contractaudit/internal/fault"
	knowledgeModel "contractaudit/internal/knowledge/model"
	legalModel "contractaudit/internal/legal/model"
	"time"
)

type FrozenCaptureFacts interface {
	LoadKnowledgeVersion(context.Context, string, string) (knowledgeModel.GovernedKnowledgeVersion, error)
	LoadSourceCapture(context.Context, string) (legalModel.LegalSourceCapture, error)
}

func ValidateFrozenCaptures(ctx context.Context, facts FrozenCaptureFacts, authorities []legalModel.FrozenLegalAuthority) error {
	for _, authority := range authorities {
		if authority.LegacyRuleGap {
			continue
		}
		if authority.Article == nil || authority.ExactReference == nil {
			return fault.Conflict("冻结的规范性法律引用缺少精确条文")
		}
		frozen := authority.Article
		version, err := facts.LoadKnowledgeVersion(ctx, frozen.Ref.KnowledgeID, frozen.Ref.VersionID)
		if err != nil || version.Ref != frozen.Ref || version.Content.Kind != "legal" || version.Content.Body != frozen.CanonicalBody || version.Content.LegalMetadata == nil {
			return fault.Conflict("冻结的法律条文版本或正文不再匹配")
		}
		metadata := version.Content.LegalMetadata
		if metadata.SourceSnapshotID != frozen.SourceSnapshotID || metadata.SourceID != frozen.SourceID || metadata.NormVersion != frozen.NormVersion ||
			metadata.NormTitle != frozen.NormTitle || metadata.ArticleNumber != frozen.ArticleNumber || metadata.ArticleLabel != frozen.ArticleLabel ||
			metadata.DocumentType != frozen.DocumentType || metadata.EffectiveFrom != frozen.EffectiveFrom || metadata.EffectiveUntil != frozen.EffectiveUntil {
			return fault.Conflict("冻结的法律来源元数据不再匹配")
		}
		sourceCapture, err := facts.LoadSourceCapture(ctx, frozen.Source.CaptureSnapshotID)
		if err != nil || sourceCapture.SourceID != frozen.SourceID || sourceCapture.NormVersion != frozen.NormVersion ||
			sourceCapture.RawSHA256 != frozen.Source.RawResponseSHA256 || sourceCapture.ExtractedSHA256 != frozen.Source.ExtractedFullTextSHA256 ||
			sourceCapture.Publisher != frozen.Source.Publisher || sourceCapture.Issuer != frozen.Source.Issuer || sourceCapture.OfficialURL != frozen.Source.OfficialURL ||
			sourceCapture.Provenance != frozen.Source.Provenance || sourceCapture.ExtractorID != frozen.Source.ExtractorID ||
			sourceCapture.ExtractorVersion != frozen.Source.ExtractorVersion || sourceCapture.CanonicalVersion != frozen.Source.CanonicalizationVersion ||
			!sameTimestamp(frozen.Source.CapturedAt, parseCaptureTime(sourceCapture.SourceCapturedAt)) || sourceCapture.ResponseStatus != frozen.Source.ResponseStatus {
			return fault.Conflict("冻结的法律原始来源快照摘要或元数据不再匹配")
		}
		statusCapture := sourceCapture
		if frozen.Status.CaptureSnapshotID != sourceCapture.CaptureSnapshotID {
			statusCapture, err = facts.LoadSourceCapture(ctx, frozen.Status.CaptureSnapshotID)
			if err != nil || statusCapture.SourceID != frozen.SourceID || statusCapture.NormVersion != frozen.NormVersion ||
				statusCapture.ExtractedSHA256 != sourceCapture.ExtractedSHA256 || statusCapture.SourceCapturedAt == "" {
				return fault.Conflict("冻结的状态观察快照没有相同的规范文本")
			}
			matching := false
			for _, article := range statusCapture.Articles {
				if article.Ref.ArticleNumber == frozen.ArticleNumber && article.FullText == frozen.CanonicalBody && article.TextSHA256 == frozen.ArticleTextSHA256 {
					matching = true
					break
				}
			}
			if !matching {
				return fault.Conflict("冻结的状态观察快照没有相同的引用条文")
			}
		}
		if statusCapture.RawSHA256 != frozen.Status.CaptureRawResponseSHA256 || statusCapture.StatusSHA256 != frozen.Status.ResponseSHA256 ||
			statusCapture.StatusID != frozen.Status.ObservationID || statusCapture.Status != frozen.Status.ObservedStatus ||
			statusCapture.StatusRegistryURL != frozen.Status.RegistryURL || !sameTimestamp(frozen.Status.ObservedAt, parseCaptureTime(statusCapture.StatusObservedAt)) ||
			statusCapture.StatusHTTPStatus != frozen.Status.ResponseStatus {
			return fault.Conflict("冻结的法律状态观察原始摘要或元数据不再匹配")
		}
	}
	return nil
}

func sameTimestamp(raw string, stored time.Time) bool {
	return legalModel.SameTimestamp(raw, stored)
}
func parseCaptureTime(raw string) time.Time { at, _ := time.Parse(time.RFC3339Nano, raw); return at }
