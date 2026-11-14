// File metadata (manifest mirror):
// Purpose: Selects current governed review dependencies, compares Retry selections, composes Legal authority freezing, and seals the v1 review snapshot.
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
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/knowledge/model"
	legalApp "contractaudit/internal/legal/app"
	legalModel "contractaudit/internal/legal/model"
	"contractaudit/internal/rulepack"
)

type ActiveKnowledgeIdentity struct{ KnowledgeID, VersionID string }
type ReviewRepository interface {
	VersionReader
	ActiveKnowledge(context.Context, string) ([]ActiveKnowledgeIdentity, error)
}
type ReviewFacts interface {
	KnowledgeReadFacts
	LoadRulePack(context.Context, model.GovernedKnowledgeContent) (RulePackBindingFacts, error)
	AuthorizationRevision(context.Context) (string, error)
}
type ReviewSelectionInput struct {
	TenantID      string
	Case          model.ReviewCaseFacts
	BusinessAt    time.Time
	Pack          model.GovernedRulePackRef
	Manifest      rulepack.Manifest
	OptionalLimit int
}
type ReviewSnapshot = model.ReviewKnowledgeSnapshot[legalModel.FrozenLegalAuthority]

// ResolveCurrent selects the current governed candidates from already trusted
// case and RulePack facts; its caller owns authorization and the transaction.
func ResolveCurrent(ctx context.Context, repo ReviewRepository, facts ReviewFacts, in ReviewSelectionInput) (ReviewSnapshot, error) {
	type requiredRule struct{ id, version, ruleID string }
	var required []requiredRule
	for _, rule := range in.Manifest.Rules {
		if !rule.Mandatory || rule.BusinessScope.BusinessLine != in.Case.BusinessLine || rule.BusinessScope.Scenario != in.Case.Scenario || rule.BusinessScope.ContractType != in.Case.ContractType {
			continue
		}
		for _, a := range rule.AuthoritySources {
			if a.Kind == "internal_policy" || a.Kind == "synthetic_demo" {
				required = append(required, requiredRule{a.ID, a.Version, rule.RuleID})
			}
		}
	}
	ids, err := repo.ActiveKnowledge(ctx, in.TenantID)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	var policies, optional []model.GovernedKnowledgeVersion
	seen := make(map[string]bool)
	for _, id := range ids {
		loaded, err := LoadExactVersion(ctx, repo, facts, in.TenantID, id.KnowledgeID, id.VersionID)
		if err != nil {
			return ReviewSnapshot{}, fault.Conflict("无法解析生效制度的编号或内容")
		}
		v := loaded.Version
		if v.Content.Kind == "legal" {
			continue
		}
		applies, err := model.KnowledgeApplies(v.Content, in.Case, in.BusinessAt)
		if err != nil {
			return ReviewSnapshot{}, err
		}
		if !applies {
			continue
		}
		if !v.Active || v.ApprovedDigest != v.Ref.ContentSHA256 || v.ApprovalActorID == "" || v.ApprovalReason == "" {
			return ReviewSnapshot{}, fault.Conflict("适用制度未发布或缺少有效批准")
		}
		if !(v.Content.Kind == "experience" && v.CrossCaseReviewed) {
			if err := authorizeKnowledgeRead(ctx, facts, v.Content); err != nil {
				var problem *fault.Error
				if errors.As(err, &problem) && problem.Status == 404 {
					if v.Content.Kind == "policy" {
						return ReviewSnapshot{}, fault.Conflict("强制政策的来源授权不可用")
					}
					continue
				}
				return ReviewSnapshot{}, err
			}
		}
		if seen[id.KnowledgeID] {
			return ReviewSnapshot{}, fault.Conflict("存在多个适用的制度版本")
		}
		seen[id.KnowledgeID] = true
		pack, err := facts.LoadRulePack(ctx, v.Content)
		if err != nil {
			return ReviewSnapshot{}, err
		}
		if err = ValidateRulePackBinding(in.TenantID, v.Content, pack); err != nil {
			return ReviewSnapshot{}, err
		}
		b := v.Content.Binding
		if b.PackID != in.Pack.PackID || b.PackVersion != in.Pack.Version || b.PackContentHash != in.Pack.ContentHash || b.PackSnapshotDigest != in.Pack.SnapshotDigest {
			return ReviewSnapshot{}, fault.Conflict("制度绑定与案件适用规则包不一致")
		}
		if v.Content.Kind == "experience" && v.Content.SourceCaseID != in.Case.CaseID && !v.CrossCaseReviewed {
			return ReviewSnapshot{}, fault.Conflict("跨案经验缺少敏感内容审阅")
		}
		if v.Content.Kind == "policy" {
			policies = append(policies, v)
		} else {
			optional = append(optional, v)
		}
	}
	for _, need := range required {
		matched := false
		for _, p := range policies {
			if p.Content.KnowledgeID != need.id || p.Content.Version != need.version {
				continue
			}
			for _, ruleID := range p.Content.Binding.RuleIDs {
				if ruleID == need.ruleID {
					matched = true
				}
			}
		}
		if !matched {
			return ReviewSnapshot{}, fault.Conflict("强制规则缺少适用的已发布政策绑定")
		}
	}
	for _, v := range optional {
		for _, dep := range v.Content.PolicyRefs {
			found := false
			for _, p := range policies {
				if p.Ref == dep {
					found = true
				}
			}
			if !found {
				return ReviewSnapshot{}, fault.Conflict("制度所依赖的精确政策未生效")
			}
		}
	}
	model.SortReviewCandidates(policies, optional)
	revision, err := facts.AuthorizationRevision(ctx)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	snapshot := ReviewSnapshot{Encoding: "review-knowledge-snapshot/v1", BusinessAt: in.BusinessAt.UTC().Format(time.RFC3339Nano), Scope: model.KnowledgeScope{OrgID: in.Case.OrgID, CaseID: in.Case.CaseID, BusinessLine: in.Case.BusinessLine, Scenario: in.Case.Scenario, ContractType: in.Case.ContractType}, AuthorizationRevision: revision, RulePack: in.Pack, RequiredPolicies: []model.GovernedKnowledgeSelection{}, OptionalCandidates: []model.GovernedKnowledgeSelection{}, Excluded: []model.GovernedKnowledgeExclusion{}}
	model.AddReviewSelections(&snapshot, policies, optional, in.OptionalLimit)
	return snapshot, nil
}

// CompareRetrySelections compares re-resolved current dependencies with the
// accepted snapshot; Legal fingerprint validation is supplied by its owner.
func CompareRetrySelections(frozen, current ReviewSnapshot, legalPresent, legalValid bool) error {
	if legalPresent && !legalValid {
		return fault.Conflict("本次审查的法律来源或适用事实已改变，请发起新的审查")
	}
	if current.Scope != frozen.Scope || current.AuthorizationRevision != frozen.AuthorizationRevision || current.RulePack != frozen.RulePack || len(current.RequiredPolicies) != len(frozen.RequiredPolicies) {
		return fault.Conflict("授权、规则包或强制政策已改变")
	}
	for i := range frozen.RequiredPolicies {
		if !reflect.DeepEqual(frozen.RequiredPolicies[i], current.RequiredPolicies[i]) {
			return fault.Conflict("强制政策已改变或撤回")
		}
	}
	seen := make(map[model.KnowledgeRef]bool, len(frozen.OptionalCandidates))
	for _, selected := range frozen.OptionalCandidates {
		if seen[selected.Ref] {
			return fault.Conflict("本次审查保存的制度记录包含重复候选")
		}
		seen[selected.Ref] = true
		found := false
		for _, v := range current.OptionalCandidates {
			if reflect.DeepEqual(selected, v) {
				found = true
				break
			}
		}
		if !found {
			return fault.Conflict("本次审查选择的可选制度已发生变化或被撤回")
		}
	}
	return nil
}

// ResolveReview composes current Knowledge selection with the Legal-owned
// frozen authority workflow and seals the existing v1 snapshot.
func ResolveReview(ctx context.Context, repo ReviewRepository, facts ReviewFacts, in ReviewSelectionInput, legalFacts legalApp.FreezeFacts, pack *rulepack.Pack, cutoff time.Time, cutoffMode, explicitCutoff string) (ReviewSnapshot, error) {
	snapshot, err := ResolveCurrent(ctx, repo, facts, in)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	if !cutoff.IsZero() {
		authorities, err := legalApp.FreezeReview(ctx, legalFacts, legalApp.BusinessScope{BusinessLine: in.Case.BusinessLine, Scenario: in.Case.Scenario, ContractType: in.Case.ContractType}, pack, in.BusinessAt, cutoff)
		if err != nil {
			return ReviewSnapshot{}, err
		}
		if len(authorities) > 0 {
			snapshot.EvidenceCutoffAt = cutoff.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
			snapshot.EvidenceCutoffMode = cutoffMode
			snapshot.ExplicitEvidenceCutoffAt = explicitCutoff
			snapshot.LegalAuthorities = authorities
		}
	}
	legal := legalModel.LegalReuseFields{EvidenceCutoffAt: snapshot.EvidenceCutoffAt, EvidenceCutoffMode: snapshot.EvidenceCutoffMode, ExplicitEvidenceCutoffAt: snapshot.ExplicitEvidenceCutoffAt, LegalReuseFingerprint: snapshot.LegalReuseFingerprint, LegalAuthorities: snapshot.LegalAuthorities}
	if err := legalModel.PrepareLegalReuse(&legal); err != nil {
		return ReviewSnapshot{}, err
	}
	snapshot.EvidenceCutoffAt, snapshot.EvidenceCutoffMode, snapshot.ExplicitEvidenceCutoffAt, snapshot.LegalReuseFingerprint, snapshot.LegalAuthorities = legal.EvidenceCutoffAt, legal.EvidenceCutoffMode, legal.ExplicitEvidenceCutoffAt, legal.LegalReuseFingerprint, legal.LegalAuthorities
	return model.SealReviewSnapshot(snapshot)
}
