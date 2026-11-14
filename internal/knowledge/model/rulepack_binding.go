// File metadata (manifest mirror):
// Purpose: Validates Knowledge authority, scope, effective windows and policy thresholds against an exact existing RulePack manifest.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package model owns Knowledge's pure checks against an exact RulePack manifest.
package model

import (
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"
)

// ValidateRulePackManifest checks Knowledge-owned binding, scope, authority,
// effective-window and threshold rules against an already-loaded manifest.
// Persisted status, pointer identity and byte/hash facts remain the caller's
// responsibility.
func ValidateRulePackManifest(tenant string, content GovernedKnowledgeContent, manifest rulepack.Manifest) error {
	if manifest.ID != content.Binding.PackID || manifest.Version != content.Binding.PackVersion || manifest.TenantID != tenant ||
		manifest.Lifecycle.State != rulepack.StatePublished || manifest.Lifecycle.Verification == nil || !manifest.Lifecycle.Verification.Passed {
		return fault.Conflict("制度规则包发布或验证记录无效")
	}
	from, e1 := ParseBusinessTime(content.ValidFrom)
	until, e2 := ParseBusinessTime(content.ValidUntil)
	packFrom, e3 := ParseBusinessTime(manifest.EffectiveFrom)
	packUntil, e4 := ParseBusinessTime(manifest.EffectiveUntil)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || from.Before(packFrom) || until.After(packUntil) {
		return fault.Conflict("制度有效期超出规则包的有效期")
	}
	rules := make(map[string]rulepack.Rule, len(manifest.Rules))
	for _, rule := range manifest.Rules {
		rules[rule.RuleID] = rule
	}
	for _, id := range content.Binding.RuleIDs {
		rule, ok := rules[id]
		if !ok || rule.TenantID != tenant || rule.BusinessScope.BusinessLine != content.Scope.BusinessLine ||
			rule.BusinessScope.Scenario != content.Scope.Scenario || rule.BusinessScope.ContractType != content.Scope.ContractType {
			return fault.Conflict("制度引用的规则不存在或不适用于制度范围")
		}
		if content.Kind == "policy" {
			found := false
			for _, source := range rule.AuthoritySources {
				if source.ID == content.KnowledgeID && source.Version == content.Version &&
					(source.Kind == "internal_policy" && content.SourceType == "reference" || source.Kind == "synthetic_demo" && content.SourceType == "synthetic_policy") {
					found = true
				}
			}
			if !found {
				return fault.Conflict("规则未引用该精确政策版本")
			}
		}
	}
	if content.Kind == "policy" {
		preview := CompareGovernedPolicyThresholds(content, manifest)
		if !preview.Approvable {
			return fault.Conflict("政策阈值无法唯一提取、验证或与规则包一致；请查看制度规则差异")
		}
	}
	return nil
}
