package store

import "contractaudit/internal/revise"

// RulePack revisions consume committed evaluations; absence of a finding alone
// never proves resolution. Both the rule identity and object set must be unique.
func compareRulePackRevisions(st *state, before, after Attempt, previous, current []Finding) []Revision {
	previousEvals := st.evaluationsForAttempt(before.RunID, before.ID)
	currentEvals := st.evaluationsForAttempt(after.RunID, after.ID)
	used := make([]bool, len(current))
	out := make([]Revision, 0, len(previous)+len(current))
	for _, finding := range previous {
		row := Revision{
			Mark: "未能判断", PreviousFindingID: finding.ID, Domain: finding.Domain,
			Relation: "insufficient", Claim: "复审缺少同一规则能力与对象的唯一结果，未能判断原风险是否消除。",
		}
		evidence := finding.Anchors
		if decisions := st.decisions(finding.ID); len(decisions) > 0 {
			last := decisions[len(decisions)-1]
			row.Prior = &last
		}
		if original, ok := revisionFindingEvaluation(previousEvals, finding.ID); ok {
			if next, matched := revisionMatchingEvaluation(currentEvals, original); matched {
				row.Claim = next.Reason
				switch next.Relation {
				case "compatible":
					row.Mark, row.Relation = revise.Resolved, "compatible"
				case "conflict", "insufficient":
					for i, candidate := range current {
						if candidate.ID != next.FindingID || candidate.Relation != next.Relation {
							continue
						}
						used[i] = true
						row.FindingID, row.Relation, row.Claim = candidate.ID, candidate.Relation, candidate.Claim
						evidence = candidate.Anchors
						if next.Relation == "conflict" {
							row.Mark = revise.Persisting
						}
						break
					}
				}
			}
		}
		row.Anchors = copyAnchors(evidence)
		out = append(out, row)
	}
	for i, finding := range current {
		if used[i] {
			continue
		}
		row := Revision{
			Mark: "未能判断", FindingID: finding.ID, Domain: finding.Domain,
			Relation: finding.Relation, Claim: finding.Claim, Anchors: copyAnchors(finding.Anchors),
		}
		if evaluation, ok := revisionFindingEvaluation(currentEvals, finding.ID); ok && evaluation.Relation == "conflict" {
			row.Mark = revise.Added
		}
		out = append(out, row)
	}
	return out
}

func revisionFindingEvaluation(evaluations []ConstraintEval, findingID string) (ConstraintEval, bool) {
	var found ConstraintEval
	count := 0
	for _, evaluation := range evaluations {
		if evaluation.FindingID == findingID {
			found = evaluation
			count++
		}
	}
	if count != 1 {
		return ConstraintEval{}, false
	}
	return revisionMatchingEvaluation(evaluations, found)
}

func revisionMatchingEvaluation(evaluations []ConstraintEval, original ConstraintEval) (ConstraintEval, bool) {
	if original.RuleID == "" || original.RuleVersion == "" || original.CapabilityID == "" {
		return ConstraintEval{}, false
	}
	var found ConstraintEval
	count := 0
	for _, evaluation := range evaluations {
		if evaluation.RuleID == original.RuleID && evaluation.RuleVersion == original.RuleVersion &&
			evaluation.CapabilityID == original.CapabilityID && evaluation.Domain == original.Domain &&
			sameStringSet(evaluation.ObjectRefs, original.ObjectRefs) {
			found = evaluation
			count++
		}
	}
	return found, count == 1
}
