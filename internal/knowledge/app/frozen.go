// File metadata (manifest mirror):
// Purpose: Validates frozen review identities and exact Knowledge dependencies, reads authorized content projections, and selects immutable Legal citations.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"errors"
	"reflect"

	"contractaudit/internal/fault"
	"contractaudit/internal/knowledge/model"
	legalApp "contractaudit/internal/legal/app"
	legalModel "contractaudit/internal/legal/model"
)

func sealFrozen(snapshot ReviewSnapshot) (ReviewSnapshot, error) {
	fields := legalModel.LegalReuseFields{EvidenceCutoffAt: snapshot.EvidenceCutoffAt, EvidenceCutoffMode: snapshot.EvidenceCutoffMode, ExplicitEvidenceCutoffAt: snapshot.ExplicitEvidenceCutoffAt, LegalReuseFingerprint: snapshot.LegalReuseFingerprint, LegalAuthorities: snapshot.LegalAuthorities}
	if err := legalModel.PrepareLegalReuse(&fields); err != nil {
		return ReviewSnapshot{}, err
	}
	snapshot.EvidenceCutoffAt, snapshot.EvidenceCutoffMode, snapshot.ExplicitEvidenceCutoffAt, snapshot.LegalReuseFingerprint, snapshot.LegalAuthorities = fields.EvidenceCutoffAt, fields.EvidenceCutoffMode, fields.ExplicitEvidenceCutoffAt, fields.LegalReuseFingerprint, fields.LegalAuthorities
	return model.SealReviewSnapshot(snapshot)
}

// DecodeFrozenSnapshot validates the persisted v1 bytes and their Legal-aware seal.
func DecodeFrozenSnapshot(raw []byte) (*ReviewSnapshot, error) {
	value, err := model.DecodeCanonicalReviewSnapshot[legalModel.FrozenLegalAuthority](raw)
	if err != nil || value == nil {
		return value, err
	}
	sealed, err := sealFrozen(*value)
	if err != nil || sealed.SnapshotSHA256 != value.SnapshotSHA256 {
		return nil, errors.New("frozen governed knowledge digest mismatch")
	}
	return value, nil
}

// ValidateFrozenIdentity must precede trusted actor and case lookup.
func ValidateFrozenIdentity(snapshot ReviewSnapshot) error {
	if snapshot.Encoding != "review-knowledge-snapshot/v1" || !model.ValidHash(snapshot.SnapshotSHA256) {
		return fault.Conflict("本次审查保存的制度记录无效")
	}
	sealed, err := sealFrozen(snapshot)
	if err != nil || sealed.SnapshotSHA256 != snapshot.SnapshotSHA256 {
		return fault.Conflict("本次审查保存的制度记录摘要不一致")
	}
	return nil
}

func ValidateFrozenDependencies(ctx context.Context, repo VersionReader, facts ReviewFacts, legalFacts legalApp.FrozenCaptureFacts, tenant string, snapshot ReviewSnapshot) error {
	revision, err := facts.AuthorizationRevision(ctx)
	if err != nil || revision != snapshot.AuthorizationRevision {
		return fault.Conflict("本次审查的案件授权已改变")
	}
	if err := legalApp.ValidateFrozenCaptures(ctx, legalFacts, snapshot.LegalAuthorities); err != nil {
		return err
	}
	for _, selected := range snapshot.RequiredPolicies {
		loaded, err := LoadExactVersion(ctx, repo, facts, tenant, selected.Ref.KnowledgeID, selected.Ref.VersionID)
		if err != nil || !loaded.PointerActive || loaded.Version.Ref != selected.Ref || !reflect.DeepEqual(model.KnowledgeSelection(loaded.Version), selected) {
			return fault.Conflict("强制政策已改变或撤回")
		}
	}
	for _, selected := range snapshot.OptionalCandidates {
		loaded, err := LoadExactVersion(ctx, repo, facts, tenant, selected.Ref.KnowledgeID, selected.Ref.VersionID)
		if err != nil || loaded.Version.Ref != selected.Ref {
			return fault.Conflict("本次审查保存的可选制度无法验证")
		}
		v := loaded.Version
		if v.Content.Kind == "experience" && v.CrossCaseReviewed {
			continue
		}
		if err := authorizeKnowledgeRead(ctx, facts, v.Content); err != nil {
			var problem *fault.Error
			if errors.As(err, &problem) && (problem.Status == 403 || problem.Status == 404) {
				return fault.Conflict("本次审查可选制度的案件授权已改变")
			}
			return err
		}
	}
	return nil
}

func ReadFrozenContent(ctx context.Context, repo VersionReader, facts LegalFactsReader, tenant string, snapshot ReviewSnapshot, ref model.KnowledgeRef) (model.GovernedKnowledgeReviewContent, error) {
	found := false
	for _, v := range snapshot.RequiredPolicies {
		if v.Ref == ref {
			found = true
		}
	}
	for _, v := range snapshot.OptionalCandidates {
		if v.Ref == ref {
			found = true
		}
	}
	if !found {
		return model.GovernedKnowledgeReviewContent{}, fault.NotFound("制度不在本次审查保存的候选列表中")
	}
	loaded, err := LoadExactVersion(ctx, repo, facts, tenant, ref.KnowledgeID, ref.VersionID)
	if err != nil || loaded.Version.Ref != ref || !loaded.Version.Active {
		return model.GovernedKnowledgeReviewContent{}, fault.Conflict("本次审查使用的制度已撤回，或记录编号不一致")
	}
	c := loaded.Version.Content
	return model.GovernedKnowledgeReviewContent{Ref: ref, Kind: c.Kind, Title: c.Title, Body: c.Body, Summary: c.Summary, ValidFrom: c.ValidFrom, ValidUntil: c.ValidUntil, ExceptionBasis: c.ExceptionBasis, ReviewedApplicability: c.ReviewedApplicability, ReviewedExclusions: c.ReviewedExclusions, CheckAction: c.CheckAction, MinimumEvidence: c.MinimumEvidence, Counterexamples: c.Counterexamples}, nil
}

func SelectFrozenLegalCitation(snapshot ReviewSnapshot, citationID string) (legalModel.FrozenLegalAuthority, error) {
	sealed, err := sealFrozen(snapshot)
	fields := legalModel.LegalReuseFields{EvidenceCutoffAt: snapshot.EvidenceCutoffAt, EvidenceCutoffMode: snapshot.EvidenceCutoffMode, ExplicitEvidenceCutoffAt: snapshot.ExplicitEvidenceCutoffAt, LegalReuseFingerprint: snapshot.LegalReuseFingerprint, LegalAuthorities: snapshot.LegalAuthorities}
	if err != nil || sealed.SnapshotSHA256 != snapshot.SnapshotSHA256 || snapshot.LegalReuseFingerprint == "" || legalModel.PrepareLegalReuse(&fields) != nil || fields.LegalReuseFingerprint != snapshot.LegalReuseFingerprint {
		return legalModel.FrozenLegalAuthority{}, fault.Conflict("所选审查的冻结法律依据摘要不一致")
	}
	var selected *legalModel.FrozenLegalAuthority
	for i := range snapshot.LegalAuthorities {
		a := &snapshot.LegalAuthorities[i]
		if a.CitationID == citationID {
			if selected != nil {
				return legalModel.FrozenLegalAuthority{}, fault.Conflict("所选审查包含重复的法律引用身份")
			}
			selected = a
		}
	}
	if selected == nil {
		return legalModel.FrozenLegalAuthority{}, fault.NotFound("法律引用不属于本次审查")
	}
	if err := legalModel.ValidateFrozenLegalAuthorities([]legalModel.FrozenLegalAuthority{*selected}); err != nil {
		return legalModel.FrozenLegalAuthority{}, fault.Conflict("所选审查的法律引用身份无效")
	}
	return *selected, nil
}
