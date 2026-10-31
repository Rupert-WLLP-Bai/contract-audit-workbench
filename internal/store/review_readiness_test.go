package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/rulepack"
)

func TestEvaluateReviewReadinessSeparatesAdmissionFromRuleEvidence(t *testing.T) {
	base := reviewReadinessInputs{
		CanRead: true, CanStartRole: true, Writable: true, SelectionConfirmed: true, BusinessAtProvided: true, MinimumMaterials: 2,
		Materials: []ReviewReadinessMaterial{
			{MaterialID: "mat-a", Version: 1, DocumentID: "doc-a", Selected: true},
			{MaterialID: "mat-b", Version: 1, DocumentID: "doc-b", Selected: true},
		},
		Checks: []ReviewReadinessCheck{{RuleID: "RULE-PROCUREMENT", Status: "not_executed", MissingDependencies: []string{"procurement_material"}, NextAction: "upload_procurement_material"}},
	}

	scope := evaluateReviewReadiness(base)
	if !scope.CanStart || scope.Status != "ready" {
		t.Fatalf("missing evidence for an individual rule must be visible without blocking an otherwise admissible review: %+v", scope)
	}
	if len(scope.Checks) != 1 || scope.Checks[0].Status != "not_executed" || len(scope.Checks[0].MissingDependencies) != 1 {
		t.Fatalf("per-rule missing evidence was lost: %+v", scope.Checks)
	}

	base.SelectionConfirmed = false
	scope = evaluateReviewReadiness(base)
	if scope.CanStart || scope.Status != "blocked" || !hasReadinessBlocker(scope, "material_selection_pending") {
		t.Fatalf("unconfirmed material selection must block admission: %+v", scope)
	}

	base.SelectionConfirmed = true
	base.Materials = base.Materials[:1]
	scope = evaluateReviewReadiness(base)
	if scope.CanStart || !hasReadinessBlocker(scope, "required_materials_missing") {
		t.Fatalf("full review must retain the existing two-material minimum: %+v", scope)
	}
}

func TestEvaluateReviewReadinessKeepsRecoverableParseFailureVisibleWithoutChangingAdmission(t *testing.T) {
	input := reviewReadinessInputs{
		CanRead: true, CanStartRole: true, Writable: true, SelectionConfirmed: true, BusinessAtProvided: true, MinimumMaterials: 2,
		Materials: []ReviewReadinessMaterial{{MaterialID: "a", Version: 1, Selected: true}, {MaterialID: "b", Version: 1, Selected: true}},
		Checks:    []ReviewReadinessCheck{{RuleID: "RULE-1", Status: "not_executed", MissingDependencies: []string{"failed_or_stale_parse_role:draft_contract"}, NextAction: "reparse_material"}},
	}
	scope := evaluateReviewReadiness(input)
	if !scope.CanStart || len(scope.Blockers) != 0 {
		t.Fatalf("readiness must preserve the existing acceptance/retry path for a recoverable parse failure: %+v", scope)
	}
	if len(scope.Checks) != 1 || scope.Checks[0].Status != "not_executed" || scope.Checks[0].NextAction != "reparse_material" {
		t.Fatalf("recoverable parse failure must remain visible with a retry action: %+v", scope.Checks)
	}
}

func TestEvaluateReviewReadinessAllowsCaseReadButSeparatesStartPermission(t *testing.T) {
	input := reviewReadinessInputs{
		CanRead: true, CanStartRole: false, Writable: true, SelectionConfirmed: true, BusinessAtProvided: true, MinimumMaterials: 2,
		Materials: []ReviewReadinessMaterial{{MaterialID: "a", Version: 1}, {MaterialID: "b", Version: 1}},
	}
	scope := evaluateReviewReadiness(input)
	if scope.CanStart || !hasReadinessBlocker(scope, "review_start_role_required") {
		t.Fatalf("viewer should see readiness but cannot start: %+v", scope)
	}

	input.CanStartRole = true
	input.Writable = false
	scope = evaluateReviewReadiness(input)
	if scope.CanStart || !hasReadinessBlocker(scope, "case_not_writable") {
		t.Fatalf("archived or read-only cases must not report startable: %+v", scope)
	}

	input.Writable = true
	input.BusinessAtProvided = false
	scope = evaluateReviewReadiness(input)
	if scope.CanStart || !hasReadinessBlocker(scope, "business_at_required") {
		t.Fatalf("missing business time must be explicit instead of defaulting to now: %+v", scope)
	}

	input.BusinessAtProvided = true
	input.AcceptanceBlockers = []ReviewReadinessBlocker{readinessBlocker("governed_dependencies_incomplete", "blocked", "缺少已发布规则或强制制度", []string{"published_rule_pack"}, "补齐治理配置")}
	scope = evaluateReviewReadiness(input)
	if scope.CanStart || !hasReadinessBlocker(scope, "governed_dependencies_incomplete") {
		t.Fatalf("missing published dependencies must block acceptance, not become a per-rule gap: %+v", scope)
	}
}

func TestReviewReadinessRevisionIsOrderIndependentAndTracksParseIdentity(t *testing.T) {
	at := time.Date(2026, 10, 31, 9, 10, 11, 0, time.FixedZone("UTC+8", 8*60*60))
	materials := []ReviewReadinessMaterial{
		{MaterialID: "mat-b", Version: 1, DocumentID: "doc-b", Parse: ReviewReadinessParse{Status: "pending"}},
		{MaterialID: "mat-a", Version: 2, DocumentID: "doc-a-v2", Parse: ReviewReadinessParse{Status: "ready", ParsedDocumentID: "parsed-a"}},
	}
	scope := ReviewReadinessScope{ID: ReviewScopeFull, Status: "ready", CanStart: true, Checks: []ReviewReadinessCheck{}, Blockers: []ReviewReadinessBlocker{}}
	input := reviewReadinessRevisionInput{CaseID: "case-1", BusinessAt: at, CaseUpdatedAt: "2026-10-31T01:10:11Z", MaterialSelectionVersion: 3, Materials: materials, Scope: scope}
	first, err := reviewReadinessRevision(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Materials = []ReviewReadinessMaterial{materials[1], materials[0]}
	second, err := reviewReadinessRevision(input)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("revision must not depend on database row ordering: %s != %s", first, second)
	}
	input.Materials[1].Parse.ParsedDocumentID = "parsed-b-new"
	third, err := reviewReadinessRevision(input)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("revision did not change when the current parse identity changed")
	}
}

func TestReadinessRequiresExactNormativeAuthoritySourceAndVersion(t *testing.T) {
	sources := normativeSourceKeys([]rulepack.AuthoritySource{
		{ID: "law-a", Version: "v2", Kind: "normative"},
		{ID: "guidance", Version: "v1", Kind: "guidance"},
	})
	state, missing := readinessAuthorityGateState(sources, []rulepack.AuthorityGate{{SourceID: "law-a", SourceVersion: "v2", Outcome: "unknown"}})
	if state != "facts_unknown" || missing {
		t.Fatal("an exact gate must cover the normative source even when its applicability is unknown")
	}
	for name, gates := range map[string][]rulepack.AuthorityGate{
		"same count, different source":   {{SourceID: "law-b", SourceVersion: "v2"}},
		"same source, different version": {{SourceID: "law-a", SourceVersion: "v1"}},
		"non-normative gate only":        {{SourceID: "guidance", SourceVersion: "v1"}},
	} {
		t.Run(name, func(t *testing.T) {
			state, missing := readinessAuthorityGateState(sources, gates)
			if state != "authority_gap" || !missing {
				t.Fatalf("unrelated gate incorrectly covered normative source: %+v", gates)
			}
		})
	}
}

func TestReadinessAuthorityGateStateIsOrderIndependent(t *testing.T) {
	sources := normativeSourceKeys([]rulepack.AuthoritySource{
		{ID: "law-a", Version: "v1", Kind: "normative"},
		{ID: "law-b", Version: "v2", Kind: "normative"},
	})
	cases := []struct {
		name    string
		gates   []rulepack.AuthorityGate
		state   string
		missing bool
	}{
		{name: "not applicable wins over fact unknown", gates: []rulepack.AuthorityGate{
			{SourceID: "law-a", SourceVersion: "v1", Outcome: "unknown"},
			{SourceID: "law-b", SourceVersion: "v2", Outcome: "not_applicable"},
		}, state: "not_applicable"},
		{name: "reversed order has same result", gates: []rulepack.AuthorityGate{
			{SourceID: "law-b", SourceVersion: "v2", Outcome: "not_applicable"},
			{SourceID: "law-a", SourceVersion: "v1", Outcome: "unknown"},
		}, state: "not_applicable"},
		{name: "explicit missing reference remains authority gap", gates: []rulepack.AuthorityGate{
			{SourceID: "law-a", SourceVersion: "v1", Outcome: "not_applicable"},
			{SourceID: "law-b", SourceVersion: "v2", Outcome: "unknown", ReferenceMissing: true},
		}, state: "authority_gap"},
		{name: "missing exact source remains authority gap", gates: []rulepack.AuthorityGate{
			{SourceID: "law-a", SourceVersion: "v1", Outcome: "not_applicable"},
		}, state: "authority_gap", missing: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			state, missing := readinessAuthorityGateState(sources, test.gates)
			if state != test.state || missing != test.missing {
				t.Fatalf("gate state=%q missing=%t, want %q missing=%t", state, missing, test.state, test.missing)
			}
		})
	}
}

func TestReviewReadinessWireOmitsUnsetBusinessTimeAndKeepsCollections(t *testing.T) {
	readiness := ReviewReadiness{CaseID: "case-1", Revision: strings.Repeat("a", 64), Materials: []ReviewReadinessMaterial{},
		Scopes: []ReviewReadinessScope{{ID: ReviewScopeFull, Status: "blocked", Checks: []ReviewReadinessCheck{}, Blockers: []ReviewReadinessBlocker{}}}}
	encoded, err := json.Marshal(readiness)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["business_at"]; ok {
		t.Fatalf("unset business time must be omitted rather than fabricated: %s", encoded)
	}
	if string(wire["materials"]) != "[]" {
		t.Fatalf("materials should serialize as an empty array, got %s", wire["materials"])
	}
	if len(wire["scopes"]) == 0 || wire["scopes"][0] != '[' {
		t.Fatalf("scopes should serialize as an array, got %s", wire["scopes"])
	}
}

func hasReadinessBlocker(scope ReviewReadinessScope, code string) bool {
	for _, blocker := range scope.Blockers {
		if blocker.Code == code {
			return true
		}
	}
	return false
}
