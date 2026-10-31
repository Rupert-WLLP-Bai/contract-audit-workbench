package store

import (
	"contractaudit/internal/engine"
	"contractaudit/internal/fault"
)

const ReviewScopeSingleContract = "single_contract_precheck"

// ReviewScopeIdentity is reconstructible from the immutable rule-set hash.
// The hash itself is stored in the existing snapshot column at acceptance.
type ReviewScopeIdentity struct {
	ID             string `json:"id"`
	RuleSetID      string `json:"rule_set_id"`
	RuleSetVersion string `json:"rule_set_version"`
	RuleSetHash    string `json:"rule_set_hash"`
}

func normalizeReviewScope(scope string) (string, error) {
	switch scope {
	case "", ReviewScopeFull:
		return ReviewScopeFull, nil
	case ReviewScopeSingleContract:
		return scope, nil
	default:
		return "", fault.Invalid("不支持的审查范围")
	}
}

func snapshotScope(snapshot RunSnapshot) *ReviewScopeIdentity {
	if snapshot.RuleSetHash != engine.SingleContractRuleSetHash() || snapshot.PlaybookHash != engine.SingleContractPlaybookHash() {
		return nil
	}
	mapping, err := engine.PublishedSingleContractMapping(snapshot.RuleSetHash)
	if err != nil {
		return nil
	}
	return &ReviewScopeIdentity{ID: mapping.Scope, RuleSetID: mapping.ID, RuleSetVersion: mapping.Version, RuleSetHash: snapshot.RuleSetHash}
}

func scopeSnapshot(snapshot RunSnapshot) RunSnapshot {
	snapshot.ReviewScope = snapshotScope(snapshot)
	return snapshot
}

func applyReviewScope(snapshot RunSnapshot, scope string) RunSnapshot {
	if scope == ReviewScopeSingleContract {
		snapshot.RuleSetHash = engine.SingleContractRuleSetHash()
		snapshot.PlaybookHash = engine.SingleContractPlaybookHash()
	}
	return scopeSnapshot(snapshot)
}

func isSingleContractSnapshot(snapshot RunSnapshot) bool {
	return snapshotScope(snapshot) != nil
}

func singleContractChecks(materials []ReviewReadinessMaterial, primaryID string) []ReviewReadinessCheck {
	check := ReviewReadinessCheck{RuleID: engine.SingleContractPrecheckRule, Status: "ready", MissingDependencies: []string{}, NextAction: "start_review"}
	foundPrimary := false
	for _, material := range materials {
		if material.DocumentID != primaryID || !material.Selected {
			continue
		}
		foundPrimary = true
		if material.Parse.Status != "ready" {
			check.Status = "not_executed"
			check.MissingDependencies = []string{"parsed_material_role:draft_contract"}
			if material.Parse.Status == "pending" {
				check.NextAction = "受理后解析主合同，再执行本项检查"
			} else {
				check.NextAction = "reparse_material"
			}
		}
		break
	}
	if !foundPrimary {
		check.Status = "not_executed"
		check.MissingDependencies = []string{"material_role:draft_contract"}
		check.NextAction = "provide_missing_material_or_legal_basis"
	}
	return []ReviewReadinessCheck{check,
		{RuleID: "cross_material_comparison", Status: "not_executed", MissingDependencies: []string{"comparison_materials"}, NextAction: "补充采购、审批等材料后发起完整审查"},
		{RuleID: "formal_policy_review", Status: "not_executed", MissingDependencies: []string{"formal_policy_review"}, NextAction: "核对适用制度和法律依据，并在材料齐备后发起完整审查"},
	}
}
