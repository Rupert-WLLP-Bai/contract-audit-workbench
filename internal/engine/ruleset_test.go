package engine_test

import (
	"testing"

	"contractaudit/internal/engine"
)

func TestRuleSetHashMatchesTheFrozenPlaybook(t *testing.T) {
	if engine.RuleSetHash() != "863b89f74f261eb18ca18a13130448257dcada8646f4d5a7da49104a7e0c25c7" {
		t.Fatalf("rules %s", engine.RuleSetHash())
	}
	if engine.PlaybookHash() != "8a79134e0f34b361895e3d84b03f6d551079c20e04c49ff937cda8f4e38bf88c" {
		t.Fatalf("playbook %s", engine.PlaybookHash())
	}
	if engine.ComparatorRef != "typed-constraint-v2" {
		t.Fatalf("comparator %s", engine.ComparatorRef)
	}
}
