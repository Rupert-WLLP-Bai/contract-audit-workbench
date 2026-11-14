// File metadata (manifest mirror):
// Purpose: Checks persisted exact RulePack bytes, digests, lifecycle and pointer facts before applying Knowledge-owned binding rules.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package app coordinates Knowledge rules with exact facts loaded by the caller.
package app

import (
	"contractaudit/internal/fault"
	"contractaudit/internal/knowledge/model"
	"contractaudit/internal/rulepack"
)

// RulePackBindingFacts is the exact persisted RulePack row and pointer state
// needed to validate one Knowledge binding. The caller owns the transaction,
// authorization, query and lock ordering.
type RulePackBindingFacts struct {
	Manifest       []byte
	ContentHash    string
	SnapshotDigest string
	Status         string
	PointerVersion string
	Active         bool
}

// ValidateRulePackBinding validates persisted facts, then applies Knowledge's
// manifest, scope, authority, effective-window and threshold rules.
func ValidateRulePackBinding(tenant string, content model.GovernedKnowledgeContent, facts RulePackBindingFacts) error {
	pack, err := rulepack.Parse(facts.Manifest)
	if err != nil || facts.Status != string(rulepack.StatePublished) || !facts.Active || facts.PointerVersion != content.Binding.PackVersion ||
		facts.ContentHash != content.Binding.PackContentHash || facts.SnapshotDigest != content.Binding.PackSnapshotDigest ||
		model.SHA256(facts.Manifest) != facts.SnapshotDigest || pack.Hash() != facts.ContentHash {
		return fault.Conflict("制度关联的规则包未启用，或内容编号不一致")
	}
	return model.ValidateRulePackManifest(tenant, content, pack.Manifest())
}
