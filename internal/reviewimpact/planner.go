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
	if !req.Mode.Valid() {
		return auditext.ImpactPlan{}, fmt.Errorf("%w: impact mode", auditext.ErrInvalidRequest)
	}
	if err := auditext.ValidateID(req.BaseEvaluationID); err != nil {
		return auditext.ImpactPlan{}, fmt.Errorf("%w: base evaluation id", auditext.ErrInvalidRequest)
	}
	if err := auditext.ValidateID(req.TargetEvaluationID); err != nil {
		return auditext.ImpactPlan{}, fmt.Errorf("%w: target evaluation id", auditext.ErrInvalidRequest)
	}
	if err := req.Base.Validate(); err != nil {
		return auditext.ImpactPlan{}, err
	}
	if err := req.Target.Validate(); err != nil {
		return auditext.ImpactPlan{}, err
	}
	if req.Base.Scope.TenantID != req.Target.Scope.TenantID || req.Base.Scope.CaseID != req.Target.Scope.CaseID {
		return auditext.ImpactPlan{}, auditext.ErrUnauthorized
	}
	if len(req.TargetChecks) == 0 {
		return auditext.ImpactPlan{}, fmt.Errorf("%w: target checks", auditext.ErrInvalidRequest)
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

func buildPlan(req auditext.ImpactRequest, baseByID map[string]auditext.CheckResult) (auditext.ImpactPlan, error) {
	base := req.Base
	target := req.Target
	changed := make(map[string]struct{})
	addChange := func(name string) { changed[name] = struct{}{} }

	forcedFull := false
	if base.Pack.ID != target.Pack.ID {
		addChange(changedCapabilityPackID)
		forcedFull = true
	}
	if base.Pack.Version != target.Pack.Version {
		addChange(changedCapabilityPackVersion)
		forcedFull = true
	}
	if base.ContractVersion != target.ContractVersion {
		addChange(changedContractVersion)
		forcedFull = true
	}
	if base.RulePackVersion != target.RulePackVersion {
		addChange(changedRulePackVersion)
		forcedFull = true
	}
	if base.BindingStatus != target.BindingStatus {
		addChange(changedObjectBinding)
		forcedFull = true
	}
	if base.BindingVersion != target.BindingVersion {
		addChange(changedObjectBinding)
		forcedFull = true
	}
	if base.ExternalObjectID != target.ExternalObjectID {
		addChange(changedExternalObject)
		forcedFull = true
	}
	if base.SourceSystem != target.SourceSystem {
		addChange(changedSourceSystem)
		forcedFull = true
	}
	if base.ModelConfigurationID != target.ModelConfigurationID {
		addChange(changedModelConfiguration)
		forcedFull = true
	}
	if base.PlannerConfigurationID != target.PlannerConfigurationID {
		addChange(changedPlannerConfiguration)
		forcedFull = true
	}
	if base.ParserConfigurationID != target.ParserConfigurationID {
		addChange(changedParserConfiguration)
		forcedFull = true
	}
	if !base.BusinessAt.Equal(target.BusinessAt) {
		addChange(changedBusinessAt)
		forcedFull = true
	}

	snapshotSetChanged := base.SnapshotSetID != target.SnapshotSetID
	if snapshotSetChanged {
		addChange(changedExternalSnapshotSet)
	}

	factSetChanged, factChanged, factRefs := compareFacts(base, target)
	for _, ref := range factRefs {
		addChange(ref)
	}

	complete := checksHaveCompleteDependencies(req.BaseChecks) && checksHaveCompleteDependencies(req.TargetChecks)

	unknownDependency := false
	otherDependencyChanged := false
	recordSetMembershipChanged := false
	evidenceRebindRequired := false
	for _, targetCheck := range req.TargetChecks {
		baseCheck, ok := baseByID[targetCheck.CheckID]
		if !ok {
			continue
		}
		baseDeps := dependencyIndex(baseCheck.Dependencies.Items)
		targetDeps := dependencyIndex(targetCheck.Dependencies.Items)
		for key, item := range targetDeps {
			before, exists := baseDeps[key]
			if !exists {
				unknownDependency = true
				addDependencyChange(addChange, item)
				continue
			}
			if before.Version == item.Version && before.Present == item.Present {
				continue
			}
			switch item.Kind {
			case auditext.DependencyField:
				addDependencyChange(addChange, item)
			case auditext.DependencyRecordSet:
				if before.Present != item.Present {
					// A record set that was empty (or non-empty) changing
					// membership can invalidate negative and existential
					// checks, so the whole evaluation is recomputed.
					recordSetMembershipChanged = true
					addDependencyChange(addChange, item)
				}
				if target.SnapshotSetID != "" && item.Version != target.SnapshotSetID {
					// The check still declares the old snapshot set. Its
					// evidence cannot be proven to belong to the target set.
					evidenceRebindRequired = true
				}
			default:
				// Field and record-set moves are the supported incremental
				// scope. Everything else already forces a full plan through
				// the frozen input identities above; if it still reaches this
				// branch, treat it as unknown rather than reusing.
				otherDependencyChanged = true
			}
		}
		for key, before := range baseDeps {
			if _, exists := targetDeps[key]; !exists {
				unknownDependency = true
				addDependencyChange(addChange, before)
			}
		}
		if snapshotSetChanged && !externalEvidenceRebound(baseCheck, targetCheck) {
			evidenceRebindRequired = true
		}
	}

	fallback := auditext.FallbackNone
	fallbackReason := ""
	switch {
	case !complete:
		fallback = auditext.FallbackFull
		fallbackReason = reasonDependencyIncomplete
	case factSetChanged:
		fallback = auditext.FallbackFull
		fallbackReason = reasonUnknownDependency
	case factChanged:
		fallback = auditext.FallbackFull
		fallbackReason = reasonContractFactChanged
	case forcedFull:
		fallback = auditext.FallbackFull
		fallbackReason = reasonInputChanged
	case recordSetMembershipChanged:
		fallback = auditext.FallbackFull
		fallbackReason = reasonRecordSetChanged
	case evidenceRebindRequired:
		fallback = auditext.FallbackFull
		fallbackReason = reasonEvidenceRebindRequired
	case unknownDependency || otherDependencyChanged:
		fallback = auditext.FallbackFull
		fallbackReason = reasonUnknownDependency
	}

	// A candidate may only be offered when the full target result is
	// semantically identical to the base result. Anything else keeps the full
	// path and disables reuse for this plan.
	if fallback == auditext.FallbackNone {
		for _, targetCheck := range req.TargetChecks {
			baseCheck, ok := baseByID[targetCheck.CheckID]
			if !ok {
				continue
			}
			if dependentChanged(baseCheck, targetCheck, snapshotSetChanged) {
				continue
			}
			if !sameCheckResult(baseCheck, targetCheck) {
				fallback = auditext.FallbackFull
				fallbackReason = reasonCandidateMismatch
				break
			}
		}
	}

	checks := make([]auditext.ImpactCheck, 0, len(req.TargetChecks))
	if fallback == auditext.FallbackFull {
		for _, targetCheck := range req.TargetChecks {
			checks = append(checks, auditext.ImpactCheck{
				CheckID: targetCheck.CheckID,
				Action:  auditext.ImpactRecompute,
				Reasons: []string{fallbackReason},
			})
		}
	} else {
		for _, targetCheck := range req.TargetChecks {
			baseCheck, ok := baseByID[targetCheck.CheckID]
			if !ok {
				checks = append(checks, auditext.ImpactCheck{
					CheckID: targetCheck.CheckID,
					Action:  auditext.ImpactRecompute,
					Reasons: []string{reasonNewCheck},
				})
				continue
			}
			action := auditext.ImpactReuseCandidate
			reason := reasonUnchangedDependencies
			if dependentChanged(baseCheck, targetCheck, snapshotSetChanged) {
				action = auditext.ImpactRecompute
				reason = reasonDependentFieldChanged
			}
			checks = append(checks, auditext.ImpactCheck{
				CheckID: targetCheck.CheckID,
				Action:  action,
				Reasons: []string{reason},
			})
		}
	}

	changedInputs := make([]string, 0, len(changed))
	for name := range changed {
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
		FallbackReason:       fallbackReason,
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
	if base.CheckID != target.CheckID ||
		base.Relation != target.Relation ||
		base.ReasonCode != target.ReasonCode ||
		base.MachineStatus != target.MachineStatus ||
		base.RequiresHumanReview != target.RequiresHumanReview ||
		len(base.Limitations) != len(target.Limitations) {
		return false
	}
	for index := range base.Limitations {
		if base.Limitations[index] != target.Limitations[index] {
			return false
		}
	}
	if len(base.Evidence) != len(target.Evidence) {
		return false
	}
	for index := range base.Evidence {
		if !sameEvidence(base.Evidence[index], target.Evidence[index]) {
			return false
		}
	}
	return true
}

func sameEvidence(base, target auditext.EvidenceRef) bool {
	if base.Kind != target.Kind {
		return false
	}
	switch base.Kind {
	case auditext.EvidenceDocument:
		return base.DocumentVersionID == target.DocumentVersionID &&
			base.ParsedVersionID == target.ParsedVersionID &&
			base.Start == target.Start &&
			base.End == target.End &&
			base.Quote == target.Quote
	case auditext.EvidenceExternalSnapshot:
		return base.SnapshotID == target.SnapshotID &&
			base.RecordID == target.RecordID &&
			base.FieldPointer == target.FieldPointer &&
			base.DisplayValue == target.DisplayValue &&
			base.ObservedAt.Equal(target.ObservedAt)
	default:
		return false
	}
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
