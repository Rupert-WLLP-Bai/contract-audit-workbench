package revise_test

import (
	"testing"

	"contractaudit/internal/revise"
)

func warranty(relation, draftQuote, draftValue string) revise.Constraint {
	return revise.Constraint{
		Domain:      "warranty",
		RuleVersion: "warranty-months-v2",
		Relation:    relation,
		Verified: []revise.Verified{
			{Role: "procurement", Quote: "质保36个月", Value: "36", Unit: "个月"},
			{Role: "draft_contract", Quote: draftQuote, Value: draftValue, Unit: "个月"},
		},
	}
}

func TestSameVerifiedQuoteAndValuePersists(t *testing.T) {
	previous := []revise.Constraint{warranty("conflict", "质保24个月", "24")}
	// Fact order differs. There is no claim field, so a model sentence cannot change the mark.
	current := []revise.Constraint{{
		Domain:      "warranty",
		RuleVersion: "warranty-months-v2",
		Relation:    "conflict",
		Verified: []revise.Verified{
			{Role: "draft_contract", Quote: "质保24个月", Value: "24", Unit: "个月"},
			{Role: "procurement", Quote: "质保36个月", Value: "36", Unit: "个月"},
		},
	}}
	got := revise.Classify(previous, current)
	if len(got) != 1 || got[0].Mark != "持续" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Current == nil || got[0].Previous == nil {
		t.Fatalf("both sides %+v", got[0])
	}
}

func TestChangedVerifiedQuoteDoesNotPersist(t *testing.T) {
	previous := []revise.Constraint{warranty("conflict", "质保24个月", "24")}
	current := []revise.Constraint{warranty("conflict", "质量保证期为24个月", "24")}
	got := revise.Classify(previous, current)
	if !marks(got, "新增", "消除") {
		t.Fatalf("got %+v", got)
	}
}

func TestResolvedAndAdded(t *testing.T) {
	previous := []revise.Constraint{warranty("conflict", "质保24个月", "24")}
	resolved := revise.Classify(previous, nil)
	if len(resolved) != 1 || resolved[0].Mark != "消除" || resolved[0].Current != nil || resolved[0].Previous == nil {
		t.Fatalf("resolved %+v", resolved)
	}
	added := revise.Classify(nil, []revise.Constraint{warranty("conflict", "质保12个月", "12")})
	if len(added) != 1 || added[0].Mark != "新增" || added[0].Previous != nil || added[0].Current == nil {
		t.Fatalf("added %+v", added)
	}
}

func TestDifferentRelationIsNotTheSameConstraint(t *testing.T) {
	previous := []revise.Constraint{warranty("conflict", "质保24个月", "24")}
	current := []revise.Constraint{warranty("insufficient", "质保24个月", "24")}
	if !marks(revise.Classify(previous, current), "新增", "消除") {
		t.Fatal("relation was ignored")
	}
}

func marks(got []revise.Classified, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i, mark := range want {
		if got[i].Mark != mark {
			return false
		}
	}
	return true
}
