package reviewimpact_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"contractaudit/internal/auditext"
)

func TestPlanFallbackPrecedence(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*auditext.ImpactRequest)
		reason string
	}{
		{"incomplete before input", func(r *auditext.ImpactRequest) {
			r.BaseChecks[0].Dependencies.Complete = false
			r.Target.RulePackVersion = "2.0.0"
		}, "dependency_incomplete"},
		{"fact set before changed fact", func(r *auditext.ImpactRequest) {
			r.Target.Facts[0].Value.Value = "200"
			r.Target.Facts = append(r.Target.Facts, auditext.Fact{Name: "new_fact", Value: auditext.ExactQuantity{Value: "1", Unit: "Mbit/s"}})
		}, "unknown_dependency"},
		{"fact before input", func(r *auditext.ImpactRequest) {
			r.Target.Facts[0].Value.Value = "200"
			r.Target.RulePackVersion = "2.0.0"
		}, "contract_fact_changed"},
		{"input before membership", func(r *auditext.ImpactRequest) {
			r.Target.RulePackVersion = "2.0.0"
			r.TargetChecks[0].Dependencies.Items[1].Present = false
		}, "input_changed"},
		{"membership before rebind", func(r *auditext.ImpactRequest) {
			r.TargetChecks[0].Dependencies.Items[1].Present = false
			r.TargetChecks[0].Dependencies.Items[1].Version = "old-set"
		}, "record_set_changed"},
		{"rebind before unknown", func(r *auditext.ImpactRequest) {
			r.TargetChecks[0].Dependencies.Items[1].Version = "old-set"
			r.TargetChecks[0].Dependencies.Items = append(r.TargetChecks[0].Dependencies.Items, fieldDependency("new", true))
			r.TargetChecks[0].Dependencies.Items[3].Ref = "external.other"
		}, "evidence_rebind_required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := request(frozen(), frozen(), []auditext.CheckResult{bandwidthCheck("rev-1", "set-v1")}, []auditext.CheckResult{bandwidthCheck("rev-1", "set-v1")})
			test.mutate(&r)
			before, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			plan := planNow(t, r)
			if plan.Fallback != auditext.FallbackFull || plan.FallbackReason != test.reason {
				t.Fatalf("fallback = %s/%s, want full/%s", plan.Fallback, plan.FallbackReason, test.reason)
			}
			requireAction(t, findCheck(t, plan, checkBandwidth), auditext.ImpactRecompute, test.reason)
			after, err := json.Marshal(r)
			if err != nil || string(before) != string(after) {
				t.Fatal("Plan mutated its frozen request")
			}
		})
	}
}

func TestPlanEvidenceIdentityAndResultChangesForceFull(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*auditext.CheckResult)
	}{
		{"relation", func(c *auditext.CheckResult) { c.Relation = auditext.RelationConflict }},
		{"reason", func(c *auditext.CheckResult) { c.ReasonCode = "changed" }},
		{"machine status", func(c *auditext.CheckResult) { c.MachineStatus = auditext.MachineStatus("partial") }},
		{"human review", func(c *auditext.CheckResult) { c.RequiresHumanReview = false }},
		{"limitation count", func(c *auditext.CheckResult) { c.Limitations = append(c.Limitations, "additional") }},
		{"limitation", func(c *auditext.CheckResult) { c.Limitations[0] = "changed" }},
		{"evidence count", func(c *auditext.CheckResult) { c.Evidence = c.Evidence[:1] }},
		{"document", func(c *auditext.CheckResult) { c.Evidence[0].DocumentVersionID = "doc-v2" }},
		{"parser", func(c *auditext.CheckResult) { c.Evidence[0].ParsedVersionID = "parse-v2" }},
		{"start", func(c *auditext.CheckResult) { c.Evidence[0].Start++ }},
		{"end", func(c *auditext.CheckResult) { c.Evidence[0].End++ }},
		{"quote", func(c *auditext.CheckResult) { c.Evidence[0].Quote = "changed" }},
		{"kind", func(c *auditext.CheckResult) { c.Evidence[0].Kind = auditext.EvidenceExternalSnapshot }},
		{"snapshot", func(c *auditext.CheckResult) { c.Evidence[1].SnapshotID = "snapshot-v2" }},
		{"record", func(c *auditext.CheckResult) { c.Evidence[1].RecordID = "record-v2" }},
		{"field", func(c *auditext.CheckResult) { c.Evidence[1].FieldPointer = "/other" }},
		{"display", func(c *auditext.CheckResult) { c.Evidence[1].DisplayValue = "200 Mbit/s" }},
		{"observed at", func(c *auditext.CheckResult) { c.Evidence[1].ObservedAt = businessAt.Add(time.Second) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := evidencedCheck()
			target := evidencedCheck()
			test.mutate(&target)
			plan := planNow(t, request(frozen(), frozen(), []auditext.CheckResult{base}, []auditext.CheckResult{target}))
			if plan.Fallback != auditext.FallbackFull || plan.FallbackReason != "candidate_mismatch" {
				t.Fatalf("evidence/result changed but got fallback %s/%s", plan.Fallback, plan.FallbackReason)
			}
		})
	}
}

func TestPlanEvidenceSameInstantAndCheckOrder(t *testing.T) {
	base := evidencedCheck()
	target := evidencedCheck()
	target.Evidence[1].ObservedAt = businessAt.In(time.FixedZone("local", 8*60*60))
	second := checkResult(checkAdvancePayment, true, mappingDependency("binding-v1"))
	plan := planNow(t, request(frozen(), frozen(), []auditext.CheckResult{base, second}, []auditext.CheckResult{second, target}))
	if plan.Fallback != auditext.FallbackNone {
		t.Fatalf("equal instant changed the result: %+v", plan)
	}
	if got := []string{plan.Checks[0].CheckID, plan.Checks[1].CheckID}; !reflect.DeepEqual(got, []string{checkAdvancePayment, checkBandwidth}) {
		t.Fatalf("target order changed: %v", got)
	}
	for _, check := range plan.Checks {
		requireAction(t, check, auditext.ImpactReuseCandidate, "unchanged_complete_dependencies")
	}
}

func evidencedCheck() auditext.CheckResult {
	check := bandwidthCheck("rev-1", "set-v1")
	check.Limitations = []string{"configured value only"}
	check.Evidence = []auditext.EvidenceRef{documentEvidence("100"), {
		Kind: auditext.EvidenceExternalSnapshot, SnapshotID: "snapshot-v1", RecordID: "record-v1",
		FieldPointer: "/configured_bandwidth", DisplayValue: "100 Mbit/s", ObservedAt: businessAt,
	}}
	return check
}

func TestPlanSnapshotRebindingAndUnchangedDependencyVersions(t *testing.T) {
	for _, test := range []struct {
		name     string
		mutate   func(*auditext.CheckResult, *auditext.CheckResult)
		fallback auditext.Fallback
		reason   string
	}{
		{"reused snapshot", func(_, _ *auditext.CheckResult) {}, auditext.FallbackFull, "evidence_rebind_required"},
		{"new snapshot", func(_, target *auditext.CheckResult) {
			target.Evidence[1].SnapshotID = "snapshot-v2"
		}, auditext.FallbackNone, ""},
		{"documents only", func(base, target *auditext.CheckResult) {
			base.Evidence = base.Evidence[:1]
			target.Evidence = target.Evidence[:1]
		}, auditext.FallbackNone, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, target := evidencedCheck(), evidencedCheck()
			test.mutate(&base, &target)
			input := frozen()
			input.SnapshotSetID = "set-v2"
			// Identical dependency versions still need recomputation when
			// their snapshot-set identity changes.
			plan := planNow(t, request(frozen(), input, []auditext.CheckResult{base}, []auditext.CheckResult{target}))
			if plan.Fallback != test.fallback || plan.FallbackReason != test.reason {
				t.Fatalf("fallback = %s/%s, want %s/%s", plan.Fallback, plan.FallbackReason, test.fallback, test.reason)
			}
			if test.fallback == auditext.FallbackNone {
				requireAction(t, findCheck(t, plan, checkBandwidth), auditext.ImpactRecompute, "dependent_field_changed")
			}
		})
	}
}

func TestPlanUnknownEvidenceCannotBeReused(t *testing.T) {
	base, target := evidencedCheck(), evidencedCheck()
	base.Evidence[0].Kind = auditext.EvidenceKind("future_evidence")
	target.Evidence[0].Kind = auditext.EvidenceKind("future_evidence")
	plan := planNow(t, request(frozen(), frozen(), []auditext.CheckResult{base}, []auditext.CheckResult{target}))
	if plan.Fallback != auditext.FallbackFull || plan.FallbackReason != "candidate_mismatch" {
		t.Fatalf("unknown evidence became a candidate: %+v", plan)
	}
}
