// File metadata (manifest mirror):
// Purpose: Coordinates Legal import replay, official baseline preparation, ordered Knowledge draft preparation, capture insertion, and same-transaction draft persistence.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"encoding/json"

	"contractaudit/internal/fault"
	knowledgeModel "contractaudit/internal/knowledge/model"
	legalModel "contractaudit/internal/legal/model"
	"contractaudit/internal/legalnorm"
)

type ImportRepository interface {
	FindReplay(context.Context, string, string, string) (CaptureReplay, bool, error)
	ReadSummary(context.Context, string, string) (CaptureSummary, error)
	InsertCapture(context.Context, CaptureInsert) error
}
type ImportReferenceChecker interface {
	ValidateImportedRefs(context.Context, string, []knowledgeModel.KnowledgeRef) error
}
type ImportKnowledge interface {
	ImportReferenceChecker
	PrepareLegalDrafts(context.Context, []knowledgeModel.GovernedKnowledgeContent) ([]knowledgeModel.PreparedVerifiedLegalDraft, error)
	PersistLegalDrafts(context.Context, string, []knowledgeModel.PreparedVerifiedLegalDraft) error
}
type CaptureIDSource interface{ NewCaptureID() (string, error) }
type PreparedImport struct {
	Input    legalnorm.CaptureInput
	Verified legalnorm.VerifiedBaseline
	Versions map[int]legalModel.LegalArticleVersionInput
}

// ReplayImport uses only the narrow summary and exact imported-ref checks.
func ReplayImport(ctx context.Context, repo ImportRepository, knowledge ImportReferenceChecker, tenant, actor, key, digest string) (legalModel.LegalSourceImportResult, bool, error) {
	replay, found, err := repo.FindReplay(ctx, tenant, actor, key)
	if err != nil || !found {
		return legalModel.LegalSourceImportResult{}, false, err
	}
	if replay.RequestDigest != digest {
		return legalModel.LegalSourceImportResult{}, false, fault.Conflict("同一幂等键不能用于不同法律来源内容")
	}
	summary, err := repo.ReadSummary(ctx, tenant, replay.CaptureID)
	if err != nil {
		return legalModel.LegalSourceImportResult{}, false, err
	}
	var refs []knowledgeModel.KnowledgeRef
	if err := json.Unmarshal(replay.ImportedRefs, &refs); err != nil || len(refs) == 0 {
		return legalModel.LegalSourceImportResult{}, false, fault.Conflict("法律来源幂等结果损坏")
	}
	if err := knowledge.ValidateImportedRefs(ctx, replay.CaptureID, refs); err != nil {
		return legalModel.LegalSourceImportResult{}, false, err
	}
	result := legalModel.LegalSourceImportResult{CaptureSnapshotID: summary.CaptureID, SourceID: summary.SourceID, NormVersion: summary.NormVersion, RawSHA256: summary.RawSHA256, ExtractedSHA256: summary.ExtractedSHA256, StatusSHA256: summary.StatusSHA256, Provenance: summary.Provenance, Articles: append(make([]knowledgeModel.KnowledgeRef, 0, len(refs)), refs...)}
	return result, true, nil
}

// PrepareImport is called only after the read-only replay misses and before
// entering the write transaction.
func PrepareImport(input legalModel.LegalSourceImportInput) (PreparedImport, error) {
	if len(input.Articles) != len(input.Capture.Articles) || len(input.Articles) == 0 || len(input.Articles) > 100 {
		return PreparedImport{}, fault.Invalid("法律条文治理清单与正文断言数量不一致")
	}
	capture := input.Capture
	capture.SourceSnapshotID = "legal-capture-pending"
	capture.Articles = append([]legalnorm.ArticleRef(nil), input.Capture.Articles...)
	for i := range capture.Articles {
		if input.Capture.SourceSnapshotID != "" && capture.Articles[i].SourceSnapshotID != "" && capture.Articles[i].SourceSnapshotID != input.Capture.SourceSnapshotID {
			return PreparedImport{}, fault.Invalid("法条引用快照身份彼此不一致")
		}
		capture.Articles[i].SourceSnapshotID = capture.SourceSnapshotID
	}
	verified, err := legalnorm.VerifyBaseline(capture)
	if err != nil {
		return PreparedImport{}, fault.Invalid("法律原文或状态证据未通过官方基线核验")
	}
	versions, err := legalModel.RequestVersions(input.Articles, verified.Capture.Articles)
	if err != nil {
		return PreparedImport{}, err
	}
	return PreparedImport{Input: capture, Verified: verified, Versions: versions}, nil
}

// CommitImport is called in the existing locked write transaction. Replay is
// checked again before generating an ID or preparing the Knowledge batch.
func CommitImport(ctx context.Context, repo ImportRepository, knowledge ImportKnowledge, ids CaptureIDSource, tenant, actor, key, digest string, prepared PreparedImport) (legalModel.LegalSourceImportResult, error) {
	if result, found, err := ReplayImport(ctx, repo, knowledge, tenant, actor, key, digest); err != nil || found {
		return result, err
	}
	captureID, err := ids.NewCaptureID()
	if err != nil {
		return legalModel.LegalSourceImportResult{}, err
	}
	verified := prepared.Verified
	verified.Capture.SourceSnapshotID = captureID
	for i := range verified.Capture.Articles {
		verified.Capture.Articles[i].Ref.SourceSnapshotID = captureID
	}
	manifest, err := legalModel.ArticleManifest(verified.Capture.Articles)
	if err != nil {
		return legalModel.LegalSourceImportResult{}, err
	}
	contents := make([]knowledgeModel.GovernedKnowledgeContent, 0, len(verified.Capture.Articles))
	for _, article := range verified.Capture.Articles {
		v := prepared.Versions[article.Ref.ArticleNumber]
		contents = append(contents, knowledgeModel.GovernedKnowledgeContent{Encoding: knowledgeModel.ContentEncoding, KnowledgeID: v.KnowledgeID, Version: v.DisplayVersion, Kind: "legal", Title: verified.Capture.Title + " " + article.Ref.ArticleLabel, Body: article.FullText, Summary: legalModel.SupplementalLegalSummary(verified.Capture.SourceID, verified.Capture.VersionID, article.Ref.ArticleNumber, article.Ref.ArticleLabel), SourceType: "reference", SourceReference: verified.Capture.OfficialURL, LegalMetadata: legalModel.MetadataFor(verified, article)})
	}
	drafts, err := knowledge.PrepareLegalDrafts(ctx, contents)
	if err != nil {
		return legalModel.LegalSourceImportResult{}, err
	}
	refs := make([]knowledgeModel.KnowledgeRef, 0, len(drafts))
	for _, draft := range drafts {
		refs = append(refs, draft.Ref)
	}
	refsJSON, err := json.Marshal(refs)
	if err != nil {
		return legalModel.LegalSourceImportResult{}, err
	}
	mapping, err := json.Marshal(map[string]string{"status": verified.Capture.CurrencyStatus, "evidence_id": verified.Capture.StatusEvidenceID, "mapping": "source-specific legalnorm adapter verified the exact registry title, version dates, and status code"})
	if err != nil {
		return legalModel.LegalSourceImportResult{}, err
	}
	err = repo.InsertCapture(ctx, CaptureInsert{TenantID: tenant, CaptureID: captureID, ActorID: actor, RequestDigest: digest, IdempotencyKey: key, Verified: verified, Input: prepared.Input, StatusMapping: mapping, ArticleManifest: manifest, ArticleManifestSHA: knowledgeModel.SHA256(manifest), ImportedRefs: refsJSON})
	if err != nil {
		return legalModel.LegalSourceImportResult{}, err
	}
	if err := knowledge.PersistLegalDrafts(ctx, captureID, drafts); err != nil {
		return legalModel.LegalSourceImportResult{}, err
	}
	capture := verified.Capture
	return legalModel.LegalSourceImportResult{CaptureSnapshotID: captureID, SourceID: capture.SourceID, NormVersion: capture.VersionID, RawSHA256: capture.RawSHA256, ExtractedSHA256: capture.FullTextSHA256, StatusSHA256: capture.StatusEvidenceSHA256, Provenance: verified.Provenance, Articles: refs}, nil
}
