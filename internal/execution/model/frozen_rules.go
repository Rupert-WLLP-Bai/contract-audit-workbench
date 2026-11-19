// File metadata (manifest mirror):
// Purpose: Validates terminal attempts and frozen rule-pack identity and verifies that every citation of a model risk matches the original text and parse selected for the attempt.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-19
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"contractaudit/internal/docir"
	"contractaudit/internal/rulepack"
)

// FrozenRulePackScope contains only the case scalars used by the frozen pack.
type FrozenRulePackScope struct{ TenantID, BusinessLine, Scenario, ContractType string }

func RunMatchesCase(run Run, caseID string) bool { return run.CaseID == caseID }
func AttemptTerminalForRun(attempt Attempt, runID string) bool {
	return attempt.RunID == runID && attempt.FinishedAt != nil && (attempt.State == "completed" || attempt.State == "partial")
}
func HasFrozenAttemptHashes(attempt Attempt) bool {
	return attempt.HistoryKind == "frozen" && attempt.InputHash != nil && *attempt.InputHash != "" && attempt.OutputHash != nil && *attempt.OutputHash != ""
}

// RequiresFrozenDecisionEvidence identifies the decision that promotes a model suggestion to a confirmed risk.
func RequiresFrozenDecisionEvidence(action string, finding Finding) bool {
	return action == "confirm_risk" && (finding.Origin == "model" || finding.Origin == "ai")
}

// FrozenDecisionEvidenceMatches requires every citation to match the trusted
// frozen parse of its original document.
func FrozenDecisionEvidenceMatches[Original OriginalDocument](anchors []Anchor, docs []FrozenDocument[Original]) bool {
	if len(anchors) == 0 {
		return false
	}
	byID := make(map[string]FrozenDocument[Original], len(docs))
	for _, doc := range docs {
		id := doc.Document.OriginalProof().ID
		if _, exists := byID[id]; exists {
			return false
		}
		byID[id] = doc
	}
	for _, anchor := range anchors {
		doc, ok := byID[anchor.DocumentID]
		if !ok || !doc.Ready || anchor.Status != docir.StatusLocated || anchor.DocumentID == "" ||
			anchor.ParsedDocumentID == "" || anchor.ParsedDocumentID != doc.ParsedDocumentID ||
			anchor.NormalizedDigest == "" || doc.NormalizedHash == nil || anchor.NormalizedDigest != *doc.NormalizedHash ||
			strings.TrimSpace(anchor.Quote) == "" {
			return false
		}
		if quote, located := docir.Slice(doc.Text, anchor.Start, anchor.End); !located || quote != anchor.Quote {
			return false
		}
		if element := doc.IR.ElementAt(anchor.Start, anchor.End); anchor.ElementID != "" && element != "" && anchor.ElementID != element {
			return false
		}
		if page, hasPage := doc.IR.PageAt(anchor.Start, anchor.End); anchor.Page != nil && (!hasPage || *anchor.Page != page) {
			return false
		}
	}
	return true
}

func FrozenRulePack(attempt Attempt, caseItem FrozenRulePackScope) (rulepack.Manifest, error) {
	if attempt.RulePackCompilerRef == "" {
		return rulepack.Manifest{}, errors.New("missing rulepack compiler identity")
	}
	pack, present, err := parseFrozenRulePack(attempt)
	if err != nil {
		return rulepack.Manifest{}, err
	}
	if !present {
		return rulepack.Manifest{}, nil
	}
	if pack.Hash() != attempt.RulePackHash {
		return rulepack.Manifest{}, errors.New("normalized rulepack content hash mismatch")
	}
	manifest := pack.Manifest()
	if err := validateFrozenManifestScope(manifest, caseItem); err != nil {
		return rulepack.Manifest{}, err
	}
	return manifest, nil
}

func parseFrozenRulePack(attempt Attempt) (*rulepack.Pack, bool, error) {
	if len(attempt.RulePackSnapshot) == 0 {
		if attempt.RulePackHash != "" || attempt.RulePackSnapshotDigest != "" {
			return nil, false, errors.New("absent RulePack snapshot has invented hashes")
		}
		return nil, false, nil
	}
	if attempt.RulePackHash == "" || attempt.RulePackSnapshotDigest == "" || !json.Valid(attempt.RulePackSnapshot) {
		return nil, false, errors.New("missing frozen rulepack identity")
	}
	digest := sha256.Sum256(attempt.RulePackSnapshot)
	if hex.EncodeToString(digest[:]) != attempt.RulePackSnapshotDigest {
		return nil, false, errors.New("raw rulepack snapshot digest mismatch")
	}
	pack, err := rulepack.Parse(attempt.RulePackSnapshot)
	return pack, true, err
}

func validateFrozenManifestScope(manifest rulepack.Manifest, caseItem FrozenRulePackScope) error {
	if manifest.TenantID != caseItem.TenantID || manifest.Lifecycle.State != rulepack.StatePublished {
		return errors.New("rulepack scope or lifecycle mismatch")
	}
	for _, rule := range manifest.Rules {
		if !ruleMatchesFrozenScope(rule, caseItem) {
			return errors.New("rulepack business scope mismatch")
		}
	}
	return nil
}

func ruleMatchesFrozenScope(rule rulepack.Rule, caseItem FrozenRulePackScope) bool {
	return rule.TenantID == caseItem.TenantID && rule.BusinessScope.BusinessLine == caseItem.BusinessLine &&
		rule.BusinessScope.Scenario == caseItem.Scenario && rule.BusinessScope.ContractType == caseItem.ContractType
}
