// File metadata (manifest mirror):
// Purpose: Validates normative references and freezes exact Legal articles, provenance, applicability results, and dependency gaps for a review.
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
	"contractaudit/internal/rulepack"
	"errors"
	"fmt"
	"strings"
	"time"
)

type BusinessScope struct{ BusinessLine, Scenario, ContractType string }
type FreezeFacts interface {
	ApplicabilityFacts
	LoadKnowledgeVersion(context.Context, string, string) (knowledgeModel.GovernedKnowledgeVersion, error)
	LoadSourceCapture(context.Context, string) (legalModel.LegalSourceCapture, error)
}

func validateReferences(ctx context.Context, facts FreezeFacts, pack *rulepack.Pack) error {
	if pack == nil {
		return fault.Invalid("规则包无效")
	}
	for _, rule := range pack.Manifest().Rules {
		for _, ref := range rule.LegalAuthorities {
			normative := false
			for _, source := range rule.AuthoritySources {
				if source.ID == ref.AuthoritySourceID && source.Kind == "normative" && source.Version == ref.NormVersion {
					normative = true
					break
				}
			}
			if !normative || ref.SourceID != ref.AuthoritySourceID {
				return fault.Conflict("法律引用没有匹配的规范性规则来源")
			}
			version, err := facts.LoadKnowledgeVersion(ctx, ref.KnowledgeID, ref.VersionID)
			if err != nil {
				return fault.Conflict("规则引用的法律条文不存在或来源身份无效")
			}
			metadata := version.Content.LegalMetadata
			if !IsPublishableLegalAuthority(version) ||
				version.Ref.ContentSHA256 != ref.ContentSHA256 || metadata.SourceID != ref.SourceID ||
				metadata.SourceSnapshotID != ref.SourceSnapshotID || metadata.NormTitle != ref.NormTitle || metadata.NormVersion != ref.NormVersion ||
				metadata.ArticleNumber != ref.ArticleNumber || metadata.DocumentType != ref.DocumentType || metadata.SourceSHA256 != ref.SourceSHA256 ||
				metadata.NormTextSHA256 != ref.ArticleTextSHA256 || metadata.EffectiveFrom != ref.EffectiveFrom || metadata.EffectiveUntil != ref.EffectiveUntil {
				return fault.Conflict("规则引用与已发布法律条文的精确版本不一致")
			}
			runes := []rune(version.Content.Body)
			if ref.QuoteSpan.Start < 0 || ref.QuoteSpan.End <= ref.QuoteSpan.Start || ref.QuoteSpan.End > len(runes) ||
				string(runes[ref.QuoteSpan.Start:ref.QuoteSpan.End]) != ref.QuoteSpan.Quote {
				return fault.Conflict("规则法律引用的原文引句定位不匹配")
			}
		}
	}
	return nil
}

func IsPublishableLegalAuthority(version knowledgeModel.GovernedKnowledgeVersion) bool {
	metadata := version.Content.LegalMetadata
	return version.Content.Kind == "legal" && version.Status == "published" && metadata != nil &&
		version.ApprovedDigest != "" && version.ApprovedDigest == version.Ref.ContentSHA256 &&
		strings.TrimSpace(version.ApprovalActorID) != "" && strings.TrimSpace(version.ApprovalReason) != "" &&
		metadata.DocumentType != "draft" && metadata.DocumentType != "legislative_plan"
}

func FreezeReview(ctx context.Context, facts FreezeFacts, scope BusinessScope, pack *rulepack.Pack, businessAt, evidenceCutoff time.Time) ([]legalModel.FrozenLegalAuthority, error) {
	if pack == nil {
		return nil, errors.New("nil rule pack while freezing legal authorities")
	}
	if err := validateReferences(ctx, facts, pack); err != nil {
		return nil, err
	}
	resolved, err := ResolveApplicability(ctx, facts, businessAt, evidenceCutoff)
	if err != nil {
		return nil, err
	}
	factSet := legalModel.LegalApplicabilityFactSet{BusinessAt: businessAt.UTC(), EvidenceCutoffAt: evidenceCutoff.UTC(), Facts: resolved}
	businessDate, err := BusinessDate(businessAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	manifest := pack.Manifest()
	var frozen []legalModel.FrozenLegalAuthority
	for _, rule := range manifest.Rules {
		if rule.BusinessScope.BusinessLine != scope.BusinessLine || rule.BusinessScope.Scenario != scope.Scenario || rule.BusinessScope.ContractType != scope.ContractType {
			continue
		}
		for _, source := range rule.AuthoritySources {
			if source.Kind != "normative" {
				continue
			}
			refs := make([]rulepack.LegalAuthorityReference, 0, len(rule.LegalAuthorities))
			for _, reference := range rule.LegalAuthorities {
				if reference.AuthoritySourceID == source.ID && reference.NormVersion == source.Version {
					refs = append(refs, reference)
				}
			}
			if len(refs) == 0 {
				frozen = append(frozen, legalModel.FrozenLegalAuthority{RuleID: rule.RuleID, AuthoritySourceID: source.ID, AuthoritySourceVersion: source.Version,
					RulePackID: manifest.ID, RulePackVersion: manifest.Version, RulePackContentSHA256: pack.Hash(), Outcome: "unknown",
					OutcomeReason: "legal_reference_missing", Conditions: []legalModel.FrozenLegalApplicabilityCondition{}, SelectedFactVersions: []legalModel.LegalApplicabilityFactVersion{},
					MissingDependencies: []legalModel.FrozenLegalDependencyGap{{Kind: "authority", Reason: "legal_reference_missing"}},
					LegacyRuleGap:       true, LegalReviewStatus: "NOT_RUN"})
				continue
			}
			for _, reference := range refs {
				version, err := facts.LoadKnowledgeVersion(ctx, reference.KnowledgeID, reference.VersionID)
				if err != nil || !IsPublishableLegalAuthority(version) || version.Ref.ContentSHA256 != reference.ContentSHA256 ||
					version.ApprovedDigest != reference.ContentSHA256 || strings.TrimSpace(version.ApprovalActorID) == "" || strings.TrimSpace(version.ApprovalReason) == "" {
					return nil, fault.Conflict("规范性规则引用的法律条文没有可验证的正式批准")
				}
				metadata := version.Content.LegalMetadata
				if metadata == nil {
					return nil, fault.Conflict("规范性规则引用缺少法律来源元数据")
				}
				capture, err := facts.LoadSourceCapture(ctx, metadata.SourceSnapshotID)
				if err != nil {
					return nil, err
				}
				evaluation, err := legalModel.EvaluateLegalAuthorityApplicability([]rulepack.LegalAuthorityReference{reference}, factSet)
				if err != nil {
					return nil, err
				}
				authority := legalModel.FrozenLegalAuthority{
					RuleID: rule.RuleID, AuthoritySourceID: source.ID, AuthoritySourceVersion: source.Version,
					RulePackID: manifest.ID, RulePackVersion: manifest.Version, RulePackContentSHA256: pack.Hash(),
					ExactReference: &reference, Outcome: evaluation.Outcome, OutcomeReason: evaluation.OutcomeReason,
					Conditions: evaluation.Conditions, SelectedFactVersions: evaluation.SelectedFactVersions,
					MissingDependencies: evaluation.MissingDependencies, LegalReviewStatus: "NOT_RUN",
				}
				article := legalModel.FrozenLegalArticle{Ref: version.Ref, ContentSHA256: version.Ref.ContentSHA256, SourceID: metadata.SourceID,
					SourceSnapshotID: metadata.SourceSnapshotID, NormTitle: metadata.NormTitle, NormVersion: metadata.NormVersion,
					ArticleNumber: metadata.ArticleNumber, ArticleLabel: metadata.ArticleLabel, DocumentType: metadata.DocumentType,
					CanonicalBody: version.Content.Body, ArticleTextSHA256: reference.ArticleTextSHA256,
					QuoteSpan: reference.QuoteSpan, EffectiveFrom: metadata.EffectiveFrom, EffectiveUntil: metadata.EffectiveUntil,
					Source: legalModel.FrozenLegalSourceProvenance{CaptureSnapshotID: capture.CaptureSnapshotID, Provenance: capture.Provenance,
						Publisher: capture.Publisher, Issuer: capture.Issuer, OfficialURL: capture.OfficialURL,
						RawResponseSHA256: capture.RawSHA256, ExtractedFullTextSHA256: capture.ExtractedSHA256,
						ExtractorID: capture.ExtractorID, ExtractorVersion: capture.ExtractorVersion, CanonicalizationVersion: capture.CanonicalVersion,
						CapturedAt: capture.SourceCapturedAt, ResponseStatus: capture.ResponseStatus},
					Status: legalModel.FrozenLegalStatusProvenance{CaptureSnapshotID: capture.CaptureSnapshotID, CaptureRawResponseSHA256: capture.RawSHA256, ObservationID: capture.StatusID,
						ObservedStatus: capture.Status, RegistryURL: capture.StatusRegistryURL, ObservedAt: capture.StatusObservedAt,
						ResponseSHA256: capture.StatusSHA256, ResponseStatus: capture.StatusHTTPStatus},
				}
				authority.Article = &article
				if reason := legalSourceEvidenceGap(capture, evidenceCutoff); reason != "" {
					authority.Outcome, authority.OutcomeReason = "unknown", reason
					authority.MissingDependencies = append(authority.MissingDependencies, legalModel.FrozenLegalDependencyGap{Kind: "source_status", Reason: reason})
				} else if capture.Status == "repealed" {
					authority.Outcome, authority.OutcomeReason = "unknown", "historical_transition_basis_required"
					authority.MissingDependencies = append(authority.MissingDependencies, legalModel.FrozenLegalDependencyGap{Kind: "transition", Reason: "historical_transition_basis_required"})
				} else if businessDate < metadata.EffectiveFrom {
					authority.Outcome, authority.OutcomeReason = "not_applicable", "not_yet_effective"
				} else if metadata.EffectiveUntil != "" && businessDate >= metadata.EffectiveUntil {
					authority.Outcome, authority.OutcomeReason = "unknown", "historical_transition_basis_required"
					authority.MissingDependencies = append(authority.MissingDependencies, legalModel.FrozenLegalDependencyGap{Kind: "transition", Reason: "historical_transition_basis_required"})
				} else if capture.Status != "effective" {
					authority.Outcome, authority.OutcomeReason = "unknown", "source_status_unverified"
					authority.MissingDependencies = append(authority.MissingDependencies, legalModel.FrozenLegalDependencyGap{Kind: "source_status", Reason: "source_status_unverified"})
				}
				if err := legalModel.ValidateFrozenLegalArticle(authority); err != nil {
					return nil, fault.Conflict("冻结的精确法律条文身份无效")
				}
				frozen = append(frozen, authority)
			}
		}
	}
	return frozen, nil
}

func BusinessDate(raw string) (string, error) {
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return "", fault.Invalid("法律检索业务时间必须是带时区的 RFC3339 时间")
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return "", fmt.Errorf("load legal business timezone: %w", err)
	}
	return at.In(loc).Format("2006-01-02"), nil
}
func parseLegalFactTime(raw string) (time.Time, error) { return legalModel.ParseFactTime(raw) }
func legalSourceEvidenceGap(c legalModel.LegalSourceCapture, cutoff time.Time) string {
	source, err1 := parseLegalFactTime(c.SourceCapturedAt)
	status, err2 := parseLegalFactTime(c.StatusObservedAt)
	if err1 != nil || source.After(cutoff) {
		return "source_capture_after_cutoff"
	}
	if err2 != nil || status.After(cutoff) {
		return "status_capture_after_cutoff"
	}
	if c.ResponseStatus != 200 || c.StatusHTTPStatus != 200 || c.StatusSHA256 == "" || c.RawSHA256 == "" {
		return "source_capture_unverified"
	}
	return ""
}
