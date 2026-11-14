package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

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
