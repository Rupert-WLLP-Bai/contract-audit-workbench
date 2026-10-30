// Package reviewimpact builds conservative dependency explanations for
// incremental review.
//
// This release never reuses a business result. Plan entries named
// "reuse_candidate" are predictions only: the full result remains the single
// source of truth, and any unknown, incomplete or changed dependency falls
// back to a full recomputation (or a deny plan when authorization was
// revoked).
package reviewimpact

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sort"

	"contractaudit/internal/auditext"
)

const (
	reasonAccessRevoked          = "access_revoked"
	reasonDependencyIncomplete   = "dependency_incomplete"
	reasonUnknownDependency      = "unknown_dependency"
	reasonContractFactChanged    = "contract_fact_changed"
	reasonInputChanged           = "input_changed"
	reasonDependentFieldChanged  = "dependent_field_changed"
	reasonUnchangedDependencies  = "unchanged_complete_dependencies"
	reasonNewCheck               = "new_check"
	changedAccessGrantVersion    = "access.grant_version"
	changedExternalSnapshotSet   = "external.snapshot_set"
	changedExternalConfigured    = "external.configured_bandwidth"
	changedContractFactPrefix    = "contract.fact."
	changedContractFactFallback  = "contract.fact"
	changedCapabilityPackID      = "capability_pack.id"
	changedCapabilityPackVersion = "capability_pack.version"
	changedContractVersion       = "contract.version"
	changedRulePackVersion       = "rule_pack.version"
	changedObjectBinding         = "external.object_binding"
	changedExternalObject        = "external.object"
	changedSourceSystem          = "external.source_system"
	changedModelConfiguration    = "model.configuration"
	changedPlannerConfiguration  = "planner.configuration"
	changedParserConfiguration   = "parser.configuration"
	changedBusinessAt            = "business_at"

	reasonRecordSetChanged       = "record_set_changed"
	reasonEvidenceRebindRequired = "evidence_rebind_required"
	reasonCandidateMismatch      = "candidate_mismatch"
)

// Planner compares two frozen inputs and emits an explain or shadow plan.
// Calling Plan is pure: it does not execute checks or mutate its inputs.
type Planner struct{}

var _ auditext.ImpactPlanner = (*Planner)(nil)

// New returns a stateless conservative planner.
func New() *Planner { return &Planner{} }

// Plan implements auditext.ImpactPlanner.
//
// Validation errors are reported before any plan is produced. A grant-version
// change is an access revocation: every target check is denied instead of
// falling back to a full recomputation, so a revoked caller cannot use the
// planner to observe case data.
func (p *Planner) Plan(ctx context.Context, req auditext.ImpactRequest) (auditext.ImpactPlan, error) {
	if ctx == nil {
		return auditext.ImpactPlan{}, fmt.Errorf("%w: nil context", auditext.ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return auditext.ImpactPlan{}, err
	}
	if err := validateRequest(req); err != nil {
		return auditext.ImpactPlan{}, err
	}
	baseByID, err := checkIndex(req.BaseChecks)
	if err != nil {
		return auditext.ImpactPlan{}, err
	}
	if _, err := checkIndex(req.TargetChecks); err != nil {
		return auditext.ImpactPlan{}, err
	}
	if req.Base.Scope.GrantVersion != req.Target.Scope.GrantVersion {
		return denyPlan(req)
	}
	return buildPlan(req, baseByID)
}

func validateRequest(req auditext.ImpactRequest) error {
	if !req.Mode.Valid() {
		return fmt.Errorf("%w: impact mode", auditext.ErrInvalidRequest)
	}
	if err := auditext.ValidateID(req.BaseEvaluationID); err != nil {
		return fmt.Errorf("%w: base evaluation id", auditext.ErrInvalidRequest)
	}
	if err := auditext.ValidateID(req.TargetEvaluationID); err != nil {
		return fmt.Errorf("%w: target evaluation id", auditext.ErrInvalidRequest)
	}
	if err := req.Base.Validate(); err != nil {
		return err
	}
	if err := req.Target.Validate(); err != nil {
		return err
	}
	if req.Base.Scope.TenantID != req.Target.Scope.TenantID || req.Base.Scope.CaseID != req.Target.Scope.CaseID {
		return auditext.ErrUnauthorized
	}
	if len(req.TargetChecks) == 0 {
		return fmt.Errorf("%w: target checks", auditext.ErrInvalidRequest)
	}
	return nil
}

func buildPlan(req auditext.ImpactRequest, baseByID map[string]auditext.CheckResult) (auditext.ImpactPlan, error) {
	analysis := planAnalysis{changed: make(map[string]struct{})}
	analysis.inputs(req.Base, req.Target)
	if !checksHaveCompleteDependencies(req.BaseChecks) || !checksHaveCompleteDependencies(req.TargetChecks) {
		analysis.force(fallbackIncomplete)
	}

	checks := make([]auditext.ImpactCheck, 0, len(req.TargetChecks))
	snapshotChanged := req.Base.SnapshotSetID != req.Target.SnapshotSetID
	for _, target := range req.TargetChecks {
		base, exists := baseByID[target.CheckID]
		checks = append(checks, analysis.check(base, target, exists, req.Target.SnapshotSetID, snapshotChanged))
	}
	fallback := auditext.FallbackNone
	reason := analysis.cause.reason()
	if analysis.cause != fallbackNone {
		fallback = auditext.FallbackFull
		for i := range checks {
			checks[i].Action = auditext.ImpactRecompute
			checks[i].Reasons = []string{reason}
		}
	}
	changedInputs := make([]string, 0, len(analysis.changed))
	for name := range analysis.changed {
		changedInputs = append(changedInputs, name)
	}
	sort.Strings(changedInputs)

	plan := auditext.ImpactPlan{
		SchemaVersion:        auditext.ImpactPlanSchema,
		BaseEvaluationID:     req.BaseEvaluationID,
		TargetEvaluationID:   req.TargetEvaluationID,
		Mode:                 req.Mode,
		DependencyComplete:   fallback == auditext.FallbackNone,
		Fallback:             fallback,
		FallbackReason:       reason,
		ChangedInputs:        changedInputs,
		Checks:               checks,
		BusinessResultSource: "full",
		DataClass:            auditext.DataClassSynthetic,
	}
	if err := plan.Validate(); err != nil {
		return auditext.ImpactPlan{}, err
	}
	return plan, nil
}

// dependentChanged reports whether a target check reads something whose
// version or presence moved. A snapshot-set change also invalidates every
// check that declared the record-set dependency, even when a caller supplied
// identical versions.
func dependentChanged(baseCheck, targetCheck auditext.CheckResult, snapshotSetChanged bool) bool {
	baseDeps := dependencyIndex(baseCheck.Dependencies.Items)
	for _, item := range targetCheck.Dependencies.Items {
		before, exists := baseDeps[dependencyKey(item)]
		if !exists {
			return true
		}
		if before.Version != item.Version || before.Present != item.Present {
			return true
		}
	}
	if snapshotSetChanged {
		for _, item := range targetCheck.Dependencies.Items {
			if item.Kind == auditext.DependencyRecordSet {
				return true
			}
		}
	}
	return false
}

// externalEvidenceRebound reports whether every external-snapshot evidence
// reference in the target check is new relative to the base check. A reused
// snapshot id after the snapshot set changed is an unproven rebind, so the
// plan must fall back to a full run even though the dependency versions look
// unchanged.
func externalEvidenceRebound(baseCheck, targetCheck auditext.CheckResult) bool {
	baseSnapshots := externalSnapshotIDs(baseCheck)
	for snapshotID := range externalSnapshotIDs(targetCheck) {
		if _, reused := baseSnapshots[snapshotID]; reused {
			return false
		}
	}
	return true
}

func externalSnapshotIDs(check auditext.CheckResult) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, evidence := range check.Evidence {
		if evidence.Kind == auditext.EvidenceExternalSnapshot && evidence.SnapshotID != "" {
			ids[evidence.SnapshotID] = struct{}{}
		}
	}
	return ids
}

// sameCheckResult compares the semantic business result. Run identifiers and
// timing are deliberately ignored; relation, reason, execution state, evidence
// identity and limitations are not.
func sameCheckResult(base, target auditext.CheckResult) bool {
	return base.CheckID == target.CheckID &&
		base.Relation == target.Relation &&
		base.ReasonCode == target.ReasonCode &&
		base.MachineStatus == target.MachineStatus &&
		base.RequiresHumanReview == target.RequiresHumanReview &&
		slices.Equal(base.Limitations, target.Limitations) &&
		slices.EqualFunc(base.Evidence, target.Evidence, sameEvidence)
}

func sameEvidence(base, target auditext.EvidenceRef) bool {
	if base.Kind != target.Kind {
		return false
	}
	switch base.Kind {
	case auditext.EvidenceDocument:
		return sameDocumentEvidence(base, target)
	case auditext.EvidenceExternalSnapshot:
		return sameSnapshotEvidence(base, target)
	default:
		return false
	}
}

func sameDocumentEvidence(base, target auditext.EvidenceRef) bool {
	return base.DocumentVersionID == target.DocumentVersionID &&
		base.ParsedVersionID == target.ParsedVersionID &&
		base.Start == target.Start &&
		base.End == target.End &&
		base.Quote == target.Quote
}

func sameSnapshotEvidence(base, target auditext.EvidenceRef) bool {
	return base.SnapshotID == target.SnapshotID &&
		base.RecordID == target.RecordID &&
		base.FieldPointer == target.FieldPointer &&
		base.DisplayValue == target.DisplayValue &&
		base.ObservedAt.Equal(target.ObservedAt)
}

func checksHaveCompleteDependencies(checks []auditext.CheckResult) bool {
	for _, check := range checks {
		if !check.Dependencies.Complete {
			return false
		}
	}
	return true
}

func checkIndex(checks []auditext.CheckResult) (map[string]auditext.CheckResult, error) {
	index := make(map[string]auditext.CheckResult, len(checks))
	for _, check := range checks {
		if err := auditext.ValidateID(check.CheckID); err != nil {
			return nil, fmt.Errorf("%w: check id", auditext.ErrInvalidRequest)
		}
		if _, exists := index[check.CheckID]; exists {
			return nil, fmt.Errorf("%w: duplicate check id", auditext.ErrInvalidRequest)
		}
		index[check.CheckID] = check
	}
	return index, nil
}

func dependencyIndex(items []auditext.Dependency) map[string]auditext.Dependency {
	index := make(map[string]auditext.Dependency, len(items))
	for _, item := range items {
		index[dependencyKey(item)] = item
	}
	return index
}

func dependencyKey(item auditext.Dependency) string {
	return string(item.Kind) + "\x00" + item.Ref
}

// addDependencyChange records stable identifiers for the only dependency
// changes that have no frozen-input equivalent: external field reads and
// record-set membership. Other dependency kinds are covered by the input
// identity identifiers above.
func addDependencyChange(addChange func(string), item auditext.Dependency) {
	switch item.Kind {
	case auditext.DependencyField:
		if item.Ref == changedExternalConfigured {
			addChange(changedExternalConfigured)
			return
		}
		addChange(item.Ref)
	case auditext.DependencyRecordSet:
		addChange(item.Ref)
	}
}

func compareFacts(base, target auditext.FrozenInput) (setChanged, changed bool, refs []string) {
	baseFacts := factIndex(base.Facts)
	targetFacts := factIndex(target.Facts)
	for name := range targetFacts {
		if _, ok := baseFacts[name]; !ok {
			setChanged = true
			refs = append(refs, contractFactRef(name))
		}
	}
	for name := range baseFacts {
		if _, ok := targetFacts[name]; !ok {
			setChanged = true
			refs = append(refs, contractFactRef(name))
		}
	}
	for name, before := range baseFacts {
		after, ok := targetFacts[name]
		if !ok {
			continue
		}
		if !factsEqual(before, after) {
			changed = true
			refs = append(refs, contractFactRef(name))
		}
	}
	return setChanged, changed, refs
}

func factIndex(facts []auditext.Fact) map[string]auditext.Fact {
	index := make(map[string]auditext.Fact, len(facts))
	for _, fact := range facts {
		index[fact.Name] = fact
	}
	return index
}

// factsEqual is deliberately strict. Contract text, negation markers and
// evidence movement must force a full run; this release makes no assumption
// that a differently evidenced fact means the same thing.
func factsEqual(before, after auditext.Fact) bool {
	return before.Name == after.Name &&
		before.Value == after.Value &&
		before.Uncertain == after.Uncertain &&
		before.OutOfScope == after.OutOfScope &&
		before.SemanticKind == after.SemanticKind &&
		reflect.DeepEqual(before.Evidence, after.Evidence)
}

func contractFactRef(name string) string {
	ref := changedContractFactPrefix + name
	if auditext.ValidateID(ref) != nil {
		return changedContractFactFallback
	}
	return ref
}

// denyPlan is used when the case grant changed between the two frozen
// evaluations. It denies every target check and never offers a full
// recomputation as a fallback, because executing the checks again would still
// read case data for a revoked caller.
func denyPlan(req auditext.ImpactRequest) (auditext.ImpactPlan, error) {
	checks := make([]auditext.ImpactCheck, 0, len(req.TargetChecks))
	for _, check := range req.TargetChecks {
		checks = append(checks, auditext.ImpactCheck{
			CheckID: check.CheckID,
			Action:  auditext.ImpactDeny,
			Reasons: []string{reasonAccessRevoked},
		})
	}
	plan := auditext.ImpactPlan{
		SchemaVersion:        auditext.ImpactPlanSchema,
		BaseEvaluationID:     req.BaseEvaluationID,
		TargetEvaluationID:   req.TargetEvaluationID,
		Mode:                 req.Mode,
		DependencyComplete:   false,
		Fallback:             auditext.FallbackDeny,
		FallbackReason:       reasonAccessRevoked,
		ChangedInputs:        []string{changedAccessGrantVersion},
		Checks:               checks,
		BusinessResultSource: "full",
		DataClass:            auditext.DataClassSynthetic,
	}
	if err := plan.Validate(); err != nil {
		return auditext.ImpactPlan{}, err
	}
	return plan, nil
}
