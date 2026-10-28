package reviewimpact_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/reviewimpact"
)

const checkBandwidth = "line.configured_bandwidth"
const checkAdvancePayment = "line.existing_advance_payment"

var businessAt = time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)

func scope(grantVersion string) auditext.AuthorizedScope {
	return auditext.AuthorizedScope{
		TenantID:     "tenant-a",
		CaseID:       "case-001",
		ActorID:      "actor-1",
		GrantVersion: grantVersion,
	}
}

func documentEvidence(value string) auditext.EvidenceRef {
	return auditext.EvidenceRef{
		Kind:              auditext.EvidenceDocument,
		DocumentVersionID: "doc-v1",
		ParsedVersionID:   "parse-v1",
		Start:             0,
		End:               40,
		Quote:             "合成示例：本线路最低保障带宽" + value + "Mbit/s。",
	}
}

func frozen() auditext.FrozenInput {
	return auditext.FrozenInput{
		Scope:                  scope("grant-v1"),
		Pack:                   auditext.PackRef{ID: "operator.dedicated_line", Version: "1.0.0"},
		ContractVersion:        "doc-v1",
		RulePackVersion:        "1.0.0",
		BindingStatus:          auditext.BindingConfirmed,
		BindingVersion:         "binding-v1",
		SnapshotSetID:          "set-v1",
		ExternalObjectID:       "line-001",
		SourceSystem:           "oss-east",
		ModelConfigurationID:   "model-v1",
		PlannerConfigurationID: "planner-v1",
		ParserConfigurationID:  "parser-v1",
		BusinessAt:             businessAt,
		Facts: []auditext.Fact{{
			Name:         "minimum_bandwidth",
			Value:        auditext.ExactQuantity{Value: "100", Unit: "Mbit/s"},
			Evidence:     []auditext.EvidenceRef{documentEvidence("100")},
			SemanticKind: "configured_value",
		}},
	}
}

func fieldDependency(version string, present bool) auditext.Dependency {
	return auditext.Dependency{
		Kind:    auditext.DependencyField,
		Ref:     "external.configured_bandwidth",
		Version: version,
		Present: present,
	}
}

func recordSetDependency(version string) auditext.Dependency {
	return auditext.Dependency{
		Kind:    auditext.DependencyRecordSet,
		Ref:     "external.records",
		Version: version,
		Present: true,
	}
}

func mappingDependency(version string) auditext.Dependency {
	return auditext.Dependency{
		Kind:    auditext.DependencyMapping,
		Ref:     "object.binding",
		Version: version,
		Present: true,
	}
}

func checkResult(id string, complete bool, items ...auditext.Dependency) auditext.CheckResult {
	return auditext.CheckResult{
		CheckID:             id,
		Relation:            auditext.RelationCompatible,
		ReasonCode:          "configured_bandwidth_meets_contract",
		MachineStatus:       auditext.MachineCompleted,
		RequiresHumanReview: true,
		Dependencies:        auditext.DependencySet{Complete: complete, Items: items},
	}
}

func bandwidthCheck(fieldVersion, snapshotSet string) auditext.CheckResult {
	return checkResult(checkBandwidth, true,
		fieldDependency(fieldVersion, true),
		recordSetDependency(snapshotSet),
		mappingDependency("binding-v1"),
	)
}

func request(base, target auditext.FrozenInput, baseChecks, targetChecks []auditext.CheckResult) auditext.ImpactRequest {
	return auditext.ImpactRequest{
		BaseEvaluationID:   "eval-base",
		TargetEvaluationID: "eval-target",
		Base:               base,
		Target:             target,
		BaseChecks:         baseChecks,
		TargetChecks:       targetChecks,
		Mode:               auditext.ImpactShadow,
	}
}

func planNow(t *testing.T, req auditext.ImpactRequest) auditext.ImpactPlan {
	t.Helper()
	plan, err := reviewimpact.New().Plan(context.Background(), req)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan does not validate: %v", err)
	}
	if plan.SchemaVersion != auditext.ImpactPlanSchema {
		t.Fatalf("schema version = %q", plan.SchemaVersion)
	}
	if plan.BusinessResultSource != "full" {
		t.Fatalf("business result source = %q, want full", plan.BusinessResultSource)
	}
	if plan.DataClass != auditext.DataClassSynthetic {
		t.Fatalf("data class = %q", plan.DataClass)
	}
	return plan
}

func findCheck(t *testing.T, plan auditext.ImpactPlan, id string) auditext.ImpactCheck {
	t.Helper()
	for _, check := range plan.Checks {
		if check.CheckID == id {
			return check
		}
	}
	t.Fatalf("check %q not present in plan: %+v", id, plan.Checks)
	return auditext.ImpactCheck{}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func requireAction(t *testing.T, check auditext.ImpactCheck, action auditext.ImpactAction, reason string) {
	t.Helper()
	if check.Action != action {
		t.Fatalf("check %q action = %q, want %q", check.CheckID, check.Action, action)
	}
	if len(check.Reasons) != 1 || check.Reasons[0] != reason {
		t.Fatalf("check %q reasons = %v, want [%s]", check.CheckID, check.Reasons, reason)
	}
}

func TestPlanNoChangesKeepsReuseCandidate(t *testing.T) {
	base := frozen()
	target := frozen()
	baseCheck := bandwidthCheck("rev-1", "set-v1")
	targetCheck := bandwidthCheck("rev-1", "set-v1")

	plan := planNow(t, request(base, target, []auditext.CheckResult{baseCheck}, []auditext.CheckResult{targetCheck}))

	if plan.Fallback != auditext.FallbackNone {
		t.Fatalf("fallback = %q, want none", plan.Fallback)
	}
	if plan.FallbackReason != "" {
		t.Fatalf("fallback reason = %q, want empty for no fallback", plan.FallbackReason)
	}
	if !plan.DependencyComplete {
		t.Fatal("dependency complete must be true when there is no fallback")
	}
	if len(plan.ChangedInputs) != 0 {
		t.Fatalf("changed inputs = %v, want none", plan.ChangedInputs)
	}
	requireAction(t, findCheck(t, plan, checkBandwidth), auditext.ImpactReuseCandidate, "unchanged_complete_dependencies")
}

func TestPlanExternalFieldChangeRecomputesOnlyDependencyClosure(t *testing.T) {
	base := frozen()
	target := frozen()
	baseBandwidth := bandwidthCheck("rev-1", "set-v1")
	targetBandwidth := bandwidthCheck("rev-2", "set-v1")
	basePayment := checkResult(checkAdvancePayment, true, mappingDependency("binding-v1"))
	targetPayment := checkResult(checkAdvancePayment, true, mappingDependency("binding-v1"))

	plan := planNow(t, request(
		base,
		target,
		[]auditext.CheckResult{baseBandwidth, basePayment},
		[]auditext.CheckResult{targetBandwidth, targetPayment},
	))

	if plan.Fallback != auditext.FallbackNone || !plan.DependencyComplete {
		t.Fatalf("plan fallback=%q complete=%v, want none/true", plan.Fallback, plan.DependencyComplete)
	}
	if len(plan.ChangedInputs) != 1 || plan.ChangedInputs[0] != "external.configured_bandwidth" {
		t.Fatalf("changed inputs = %v, want [external.configured_bandwidth]", plan.ChangedInputs)
	}
	requireAction(t, findCheck(t, plan, checkBandwidth), auditext.ImpactRecompute, "dependent_field_changed")
	requireAction(t, findCheck(t, plan, checkAdvancePayment), auditext.ImpactReuseCandidate, "unchanged_complete_dependencies")
}

func TestPlanFieldPresenceChangeRecomputes(t *testing.T) {
	base := frozen()
	target := frozen()
	baseCheck := bandwidthCheck("rev-1", "set-v1")
	targetCheck := checkResult(checkBandwidth, true,
		fieldDependency("rev-1", false),
		recordSetDependency("set-v1"),
		mappingDependency("binding-v1"),
	)

	plan := planNow(t, request(base, target, []auditext.CheckResult{baseCheck}, []auditext.CheckResult{targetCheck}))

	if plan.Fallback != auditext.FallbackNone || !plan.DependencyComplete {
		t.Fatalf("plan fallback=%q complete=%v, want none/true", plan.Fallback, plan.DependencyComplete)
	}
	if !contains(plan.ChangedInputs, "external.configured_bandwidth") {
		t.Fatalf("changed inputs = %v", plan.ChangedInputs)
	}
	requireAction(t, findCheck(t, plan, checkBandwidth), auditext.ImpactRecompute, "dependent_field_changed")
}

func TestPlanContractFactChangeForcesFull(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*auditext.FrozenInput)
	}{
		{
			name: "negation-marker-changes",
			mutate: func(input *auditext.FrozenInput) {
				input.Facts[0].Uncertain = true
			},
		},
		{
			name: "contract-value-changes",
			mutate: func(input *auditext.FrozenInput) {
				input.Facts[0].Value = auditext.ExactQuantity{Value: "200", Unit: "Mbit/s"}
			},
		},
		{
			name: "contract-evidence-changes",
			mutate: func(input *auditext.FrozenInput) {
				input.Facts[0].Evidence = []auditext.EvidenceRef{documentEvidence("100")}
				input.Facts[0].Evidence[0].Quote = "合成示例：本线路最低保障带宽100Mbit/s，另行约定除外。"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := frozen()
			target := frozen()
			test.mutate(&target)
			baseCheck := bandwidthCheck("rev-1", "set-v1")
			targetCheck := bandwidthCheck("rev-1", "set-v1")

			plan := planNow(t, request(base, target, []auditext.CheckResult{baseCheck}, []auditext.CheckResult{targetCheck}))

			if plan.Fallback != auditext.FallbackFull {
				t.Fatalf("fallback = %q, want full", plan.Fallback)
			}
			if plan.FallbackReason != "contract_fact_changed" {
				t.Fatalf("fallback reason = %q", plan.FallbackReason)
			}
			if plan.DependencyComplete {
				t.Fatal("a full fallback must not claim complete dependencies")
			}
			if !contains(plan.ChangedInputs, "contract.fact.minimum_bandwidth") {
				t.Fatalf("changed inputs = %v", plan.ChangedInputs)
			}
			requireAction(t, findCheck(t, plan, checkBandwidth), auditext.ImpactRecompute, "contract_fact_changed")
		})
	}
}

func TestPlanFactAddedOrRemovedForcesFull(t *testing.T) {
	t.Run("added", func(t *testing.T) {
		base := frozen()
		target := frozen()
		target.Facts = append(target.Facts, auditext.Fact{
			Name:         "other_fact",
			Value:        auditext.ExactQuantity{Value: "1", Unit: "Mbit/s"},
			SemanticKind: "configured_value",
		})
		targetCheck := bandwidthCheck("rev-1", "set-v1")
		plan := planNow(t, request(base, target, []auditext.CheckResult{bandwidthCheck("rev-1", "set-v1")}, []auditext.CheckResult{targetCheck}))

		if plan.Fallback != auditext.FallbackFull || plan.FallbackReason != "unknown_dependency" {
			t.Fatalf("fallback=%q reason=%q, want full/unknown_dependency", plan.Fallback, plan.FallbackReason)
		}
		if !contains(plan.ChangedInputs, "contract.fact.other_fact") {
			t.Fatalf("changed inputs = %v", plan.ChangedInputs)
		}
	})

	t.Run("removed", func(t *testing.T) {
		base := frozen()
		target := frozen()
		base.Facts = append(base.Facts, auditext.Fact{
			Name:         "other_fact",
			Value:        auditext.ExactQuantity{Value: "1", Unit: "Mbit/s"},
			SemanticKind: "configured_value",
		})
		plan := planNow(t, request(base, target,
			[]auditext.CheckResult{bandwidthCheck("rev-1", "set-v1")},
			[]auditext.CheckResult{bandwidthCheck("rev-1", "set-v1")}))

		if plan.Fallback != auditext.FallbackFull || plan.FallbackReason != "unknown_dependency" {
			t.Fatalf("fallback=%q reason=%q, want full/unknown_dependency", plan.Fallback, plan.FallbackReason)
		}
		if !contains(plan.ChangedInputs, "contract.fact.other_fact") {
			t.Fatalf("changed inputs = %v", plan.ChangedInputs)
		}
	})
}

func TestPlanIncompleteDependenciesForceFull(t *testing.T) {
	base := frozen()
	target := frozen()

	t.Run("base-incomplete", func(t *testing.T) {
		baseCheck := checkResult(checkBandwidth, false, fieldDependency("rev-1", true))
		targetCheck := bandwidthCheck("rev-1", "set-v1")
		plan := planNow(t, request(base, target, []auditext.CheckResult{baseCheck}, []auditext.CheckResult{targetCheck}))

		if plan.Fallback != auditext.FallbackFull || plan.FallbackReason != "dependency_incomplete" {
			t.Fatalf("fallback=%q reason=%q, want full/dependency_incomplete", plan.Fallback, plan.FallbackReason)
		}
		if plan.DependencyComplete {
			t.Fatal("incomplete dependencies cannot be reported complete")
		}
		requireAction(t, findCheck(t, plan, checkBandwidth), auditext.ImpactRecompute, "dependency_incomplete")
	})

	t.Run("target-incomplete", func(t *testing.T) {
		baseCheck := bandwidthCheck("rev-1", "set-v1")
		targetCheck := checkResult(checkBandwidth, false, fieldDependency("rev-1", true))
		plan := planNow(t, request(base, target, []auditext.CheckResult{baseCheck}, []auditext.CheckResult{targetCheck}))

		if plan.Fallback != auditext.FallbackFull || plan.FallbackReason != "dependency_incomplete" {
			t.Fatalf("fallback=%q reason=%q, want full/dependency_incomplete", plan.Fallback, plan.FallbackReason)
		}
	})
}

func TestPlanForcedVersionChanges(t *testing.T) {
	tests := []struct {
		name            string
		mutate          func(*auditext.FrozenInput)
		wantChanged     string
		wantReasonInput bool
	}{
		{
			name:            "pack-id",
			mutate:          func(input *auditext.FrozenInput) { input.Pack.ID = "operator.cloud_services" },
			wantChanged:     "capability_pack.id",
			wantReasonInput: true,
		},
		{
			name:            "pack-version",
			mutate:          func(input *auditext.FrozenInput) { input.Pack.Version = "2.0.0" },
			wantChanged:     "capability_pack.version",
			wantReasonInput: true,
		},
		{
			name:            "contract-version",
			mutate:          func(input *auditext.FrozenInput) { input.ContractVersion = "doc-v2" },
			wantChanged:     "contract.version",
			wantReasonInput: true,
		},
		{
			name:            "rule-pack-version",
			mutate:          func(input *auditext.FrozenInput) { input.RulePackVersion = "2.0.0" },
			wantChanged:     "rule_pack.version",
			wantReasonInput: true,
		},
		{
			name: "binding-status",
			mutate: func(input *auditext.FrozenInput) {
				input.BindingStatus = auditext.BindingMissing
				input.BindingVersion = ""
				input.ExternalObjectID = ""
				input.SourceSystem = ""
			},
			wantChanged:     "external.object_binding",
			wantReasonInput: true,
		},
		{
			name:            "binding-version",
			mutate:          func(input *auditext.FrozenInput) { input.BindingVersion = "binding-v2" },
			wantChanged:     "external.object_binding",
			wantReasonInput: true,
		},
		{
			name:            "external-object",
			mutate:          func(input *auditext.FrozenInput) { input.ExternalObjectID = "line-002" },
			wantChanged:     "external.object",
			wantReasonInput: true,
		},
		{
			name:            "source-system",
			mutate:          func(input *auditext.FrozenInput) { input.SourceSystem = "oss-west" },
			wantChanged:     "external.source_system",
			wantReasonInput: true,
		},
		{
			name:            "model-configuration",
			mutate:          func(input *auditext.FrozenInput) { input.ModelConfigurationID = "model-v2" },
			wantChanged:     "model.configuration",
			wantReasonInput: true,
		},
		{
			name:            "planner-configuration",
			mutate:          func(input *auditext.FrozenInput) { input.PlannerConfigurationID = "planner-v2" },
			wantChanged:     "planner.configuration",
			wantReasonInput: true,
		},
		{
			name:            "parser-configuration",
			mutate:          func(input *auditext.FrozenInput) { input.ParserConfigurationID = "parser-v2" },
			wantChanged:     "parser.configuration",
			wantReasonInput: true,
		},
		{
			name:            "business-time",
			mutate:          func(input *auditext.FrozenInput) { input.BusinessAt = businessAt.Add(24 * time.Hour) },
			wantChanged:     "business_at",
			wantReasonInput: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := frozen()
			target := frozen()
			test.mutate(&target)
			plan := planNow(t, request(base, target,
				[]auditext.CheckResult{bandwidthCheck("rev-1", "set-v1")},
				[]auditext.CheckResult{bandwidthCheck("rev-1", "set-v1")}))

			if plan.Fallback != auditext.FallbackFull {
				t.Fatalf("fallback = %q, want full", plan.Fallback)
			}
			if plan.FallbackReason != "input_changed" {
				t.Fatalf("fallback reason = %q, want input_changed", plan.FallbackReason)
			}
			if plan.DependencyComplete {
				t.Fatal("a full fallback must not claim complete dependencies")
			}
			if !contains(plan.ChangedInputs, test.wantChanged) {
				t.Fatalf("changed inputs = %v, want %q", plan.ChangedInputs, test.wantChanged)
			}
			requireAction(t, findCheck(t, plan, checkBandwidth), auditext.ImpactRecompute, "input_changed")
		})
	}
}

func TestPlanSnapshotSetChangeRecomputesRecordSetDependents(t *testing.T) {
	base := frozen()
	target := frozen()
	target.SnapshotSetID = "set-v2"

	baseBandwidth := bandwidthCheck("rev-1", "set-v1")
	targetBandwidth := bandwidthCheck("rev-1", "set-v2")
	basePayment := checkResult(checkAdvancePayment, true, mappingDependency("binding-v1"))
	targetPayment := checkResult(checkAdvancePayment, true, mappingDependency("binding-v1"))

	plan := planNow(t, request(
		base,
		target,
		[]auditext.CheckResult{baseBandwidth, basePayment},
		[]auditext.CheckResult{targetBandwidth, targetPayment},
	))

	if plan.Fallback != auditext.FallbackNone || !plan.DependencyComplete {
		t.Fatalf("plan fallback=%q complete=%v, want none/true", plan.Fallback, plan.DependencyComplete)
	}
	if !contains(plan.ChangedInputs, "external.snapshot_set") {
		t.Fatalf("changed inputs = %v", plan.ChangedInputs)
	}
	requireAction(t, findCheck(t, plan, checkBandwidth), auditext.ImpactRecompute, "dependent_field_changed")
	requireAction(t, findCheck(t, plan, checkAdvancePayment), auditext.ImpactReuseCandidate, "unchanged_complete_dependencies")
}

func TestPlanSnapshotSetChangeWithoutRecordSetDependencyKeepsCandidate(t *testing.T) {
	base := frozen()
	target := frozen()
	target.SnapshotSetID = "set-v2"
	baseCheck := checkResult(checkAdvancePayment, true, mappingDependency("binding-v1"))
	targetCheck := checkResult(checkAdvancePayment, true, mappingDependency("binding-v1"))

	plan := planNow(t, request(base, target, []auditext.CheckResult{baseCheck}, []auditext.CheckResult{targetCheck}))

	if plan.Fallback != auditext.FallbackNone {
		t.Fatalf("fallback = %q, want none", plan.Fallback)
	}
	if !contains(plan.ChangedInputs, "external.snapshot_set") {
		t.Fatalf("changed inputs = %v", plan.ChangedInputs)
	}
	requireAction(t, findCheck(t, plan, checkAdvancePayment), auditext.ImpactReuseCandidate, "unchanged_complete_dependencies")
}

func TestPlanNewCheckRecomputesOnlyItself(t *testing.T) {
	base := frozen()
	target := frozen()
	baseCheck := bandwidthCheck("rev-1", "set-v1")
	newCheck := checkResult(checkAdvancePayment, true, mappingDependency("binding-v1"))

	plan := planNow(t, request(base, target, []auditext.CheckResult{baseCheck}, []auditext.CheckResult{baseCheck, newCheck}))

	if plan.Fallback != auditext.FallbackNone || !plan.DependencyComplete {
		t.Fatalf("plan fallback=%q complete=%v, want none/true", plan.Fallback, plan.DependencyComplete)
	}
	requireAction(t, findCheck(t, plan, checkBandwidth), auditext.ImpactReuseCandidate, "unchanged_complete_dependencies")
	requireAction(t, findCheck(t, plan, checkAdvancePayment), auditext.ImpactRecompute, "new_check")
}

func TestPlanNewDependencyForcesFull(t *testing.T) {
	base := frozen()
	target := frozen()
	baseCheck := checkResult(checkBandwidth, true, fieldDependency("rev-1", true))
	targetCheck := checkResult(checkBandwidth, true,
		fieldDependency("rev-1", true),
		recordSetDependency("set-v1"),
	)

	plan := planNow(t, request(base, target, []auditext.CheckResult{baseCheck}, []auditext.CheckResult{targetCheck}))

	if plan.Fallback != auditext.FallbackFull || plan.FallbackReason != "unknown_dependency" {
		t.Fatalf("fallback=%q reason=%q, want full/unknown_dependency", plan.Fallback, plan.FallbackReason)
	}
	if !contains(plan.ChangedInputs, "external.records") {
		t.Fatalf("changed inputs = %v", plan.ChangedInputs)
	}
	requireAction(t, findCheck(t, plan, checkBandwidth), auditext.ImpactRecompute, "unknown_dependency")
}

func TestPlanNewFieldDependencyForcesFull(t *testing.T) {
	base := frozen()
	target := frozen()
	baseCheck := checkResult(checkBandwidth, true, fieldDependency("rev-1", true))
	targetCheck := checkResult(checkBandwidth, true,
		fieldDependency("rev-1", true),
		auditext.Dependency{
			Kind:    auditext.DependencyField,
			Ref:     "external.other_field",
			Version: "other-v1",
			Present: true,
		},
	)

	plan := planNow(t, request(base, target, []auditext.CheckResult{baseCheck}, []auditext.CheckResult{targetCheck}))

	if plan.Fallback != auditext.FallbackFull || plan.FallbackReason != "unknown_dependency" {
		t.Fatalf("fallback=%q reason=%q, want full/unknown_dependency", plan.Fallback, plan.FallbackReason)
	}
	if !contains(plan.ChangedInputs, "external.other_field") {
		t.Fatalf("changed inputs = %v", plan.ChangedInputs)
	}
}

func TestPlanLongFactNameUsesStableIdentifier(t *testing.T) {
	base := frozen()
	target := frozen()
	longName := strings.Repeat("a", 120)
	target.Facts = append(target.Facts, auditext.Fact{
		Name:         longName,
		Value:        auditext.ExactQuantity{Value: "1", Unit: "Mbit/s"},
		SemanticKind: "configured_value",
	})

	plan := planNow(t, request(base, target,
		[]auditext.CheckResult{bandwidthCheck("rev-1", "set-v1")},
		[]auditext.CheckResult{bandwidthCheck("rev-1", "set-v1")}))

	if plan.Fallback != auditext.FallbackFull || plan.FallbackReason != "unknown_dependency" {
		t.Fatalf("fallback=%q reason=%q, want full/unknown_dependency", plan.Fallback, plan.FallbackReason)
	}
	if !contains(plan.ChangedInputs, "contract.fact") {
		t.Fatalf("changed inputs = %v", plan.ChangedInputs)
	}
}

func TestPlanGrantRevocationDenies(t *testing.T) {
	base := frozen()
	target := frozen()
	target.Scope.GrantVersion = "grant-v2"
	baseCheck := bandwidthCheck("rev-1", "set-v1")
	targetCheck := bandwidthCheck("rev-1", "set-v1")

	plan := planNow(t, request(base, target, []auditext.CheckResult{baseCheck}, []auditext.CheckResult{targetCheck}))

	if plan.Fallback != auditext.FallbackDeny {
		t.Fatalf("fallback = %q, want deny", plan.Fallback)
	}
	if plan.FallbackReason != "access_revoked" {
		t.Fatalf("fallback reason = %q", plan.FallbackReason)
	}
	if plan.DependencyComplete {
		t.Fatal("a deny plan must not claim complete dependencies")
	}
	if len(plan.ChangedInputs) != 1 || plan.ChangedInputs[0] != "access.grant_version" {
		t.Fatalf("changed inputs = %v", plan.ChangedInputs)
	}
	requireAction(t, findCheck(t, plan, checkBandwidth), auditext.ImpactDeny, "access_revoked")
}

func TestPlanModes(t *testing.T) {
	base := frozen()
	target := frozen()
	req := request(base, target,
		[]auditext.CheckResult{bandwidthCheck("rev-1", "set-v1")},
		[]auditext.CheckResult{bandwidthCheck("rev-1", "set-v1")})
	req.Mode = auditext.ImpactExplain
	plan := planNow(t, req)
	if plan.Mode != auditext.ImpactExplain {
		t.Fatalf("mode = %q", plan.Mode)
	}
}

func TestPlanInvalidRequests(t *testing.T) {
	base := frozen()
	target := frozen()
	checks := []auditext.CheckResult{bandwidthCheck("rev-1", "set-v1")}

	t.Run("nil context", func(t *testing.T) {
		_, err := reviewimpact.New().Plan(nil, request(base, target, checks, checks))
		if !errors.Is(err, auditext.ErrInvalidRequest) {
			t.Fatalf("error = %v, want ErrInvalidRequest", err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := reviewimpact.New().Plan(ctx, request(base, target, checks, checks))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})

	t.Run("invalid mode", func(t *testing.T) {
		req := request(base, target, checks, checks)
		req.Mode = auditext.ImpactMode("reuse")
		_, err := reviewimpact.New().Plan(context.Background(), req)
		if !errors.Is(err, auditext.ErrInvalidRequest) {
			t.Fatalf("error = %v, want ErrInvalidRequest", err)
		}
	})

	t.Run("invalid evaluation id", func(t *testing.T) {
		req := request(base, target, checks, checks)
		req.BaseEvaluationID = "bad id"
		_, err := reviewimpact.New().Plan(context.Background(), req)
		if !errors.Is(err, auditext.ErrInvalidRequest) {
			t.Fatalf("error = %v, want ErrInvalidRequest", err)
		}
	})

	t.Run("invalid base input", func(t *testing.T) {
		invalid := frozen()
		invalid.BusinessAt = time.Time{}
		_, err := reviewimpact.New().Plan(context.Background(), request(invalid, target, checks, checks))
		if !errors.Is(err, auditext.ErrInvalidRequest) {
			t.Fatalf("error = %v, want ErrInvalidRequest", err)
		}
	})

	t.Run("invalid target input", func(t *testing.T) {
		invalid := frozen()
		invalid.BusinessAt = time.Time{}
		_, err := reviewimpact.New().Plan(context.Background(), request(base, invalid, checks, checks))
		if !errors.Is(err, auditext.ErrInvalidRequest) {
			t.Fatalf("error = %v, want ErrInvalidRequest", err)
		}
	})

	t.Run("tenant mismatch", func(t *testing.T) {
		other := frozen()
		other.Scope.TenantID = "tenant-b"
		_, err := reviewimpact.New().Plan(context.Background(), request(base, other, checks, checks))
		if !errors.Is(err, auditext.ErrUnauthorized) {
			t.Fatalf("error = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("case mismatch", func(t *testing.T) {
		other := frozen()
		other.Scope.CaseID = "case-002"
		_, err := reviewimpact.New().Plan(context.Background(), request(base, other, checks, checks))
		if !errors.Is(err, auditext.ErrUnauthorized) {
			t.Fatalf("error = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("no target checks", func(t *testing.T) {
		_, err := reviewimpact.New().Plan(context.Background(), request(base, target, checks, nil))
		if !errors.Is(err, auditext.ErrInvalidRequest) {
			t.Fatalf("error = %v, want ErrInvalidRequest", err)
		}
	})

	t.Run("duplicate target check ids", func(t *testing.T) {
		duplicate := []auditext.CheckResult{bandwidthCheck("rev-1", "set-v1"), bandwidthCheck("rev-1", "set-v1")}
		_, err := reviewimpact.New().Plan(context.Background(), request(base, target, checks, duplicate))
		if !errors.Is(err, auditext.ErrInvalidRequest) {
			t.Fatalf("error = %v, want ErrInvalidRequest", err)
		}
	})

	t.Run("invalid target check id", func(t *testing.T) {
		invalid := []auditext.CheckResult{checkResult("bad id", true, mappingDependency("binding-v1"))}
		_, err := reviewimpact.New().Plan(context.Background(), request(base, target, checks, invalid))
		if !errors.Is(err, auditext.ErrInvalidRequest) {
			t.Fatalf("error = %v, want ErrInvalidRequest", err)
		}
	})
}
