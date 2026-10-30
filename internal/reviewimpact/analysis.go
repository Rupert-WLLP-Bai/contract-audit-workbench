package reviewimpact

import "contractaudit/internal/auditext"

// Higher causes take precedence regardless of check or map iteration order.
// Fact-set changes deliberately outrank other unknown dependencies.
type fallbackCause uint8

const (
	fallbackNone fallbackCause = iota
	fallbackCandidateMismatch
	fallbackUnknownDependency
	fallbackEvidenceRebind
	fallbackRecordSet
	fallbackInput
	fallbackFact
	fallbackFactSet
	fallbackIncomplete
)

func (cause fallbackCause) reason() string {
	return [...]string{
		fallbackNone:              "",
		fallbackCandidateMismatch: reasonCandidateMismatch,
		fallbackUnknownDependency: reasonUnknownDependency,
		fallbackEvidenceRebind:    reasonEvidenceRebindRequired,
		fallbackRecordSet:         reasonRecordSetChanged,
		fallbackInput:             reasonInputChanged,
		fallbackFact:              reasonContractFactChanged,
		fallbackFactSet:           reasonUnknownDependency,
		fallbackIncomplete:        reasonDependencyIncomplete,
	}[cause]
}

// planAnalysis belongs to one Plan call. It accumulates explanations while
// retaining only the highest-priority reason that requires a full run.
type planAnalysis struct {
	changed map[string]struct{}
	cause   fallbackCause
}

func (a *planAnalysis) addChange(ref string) { a.changed[ref] = struct{}{} }

func (a *planAnalysis) force(cause fallbackCause) {
	a.cause = max(a.cause, cause)
}

func (a *planAnalysis) inputs(base, target auditext.FrozenInput) {
	for _, input := range []struct {
		ref     string
		changed bool
	}{
		{changedCapabilityPackID, base.Pack.ID != target.Pack.ID},
		{changedCapabilityPackVersion, base.Pack.Version != target.Pack.Version},
		{changedContractVersion, base.ContractVersion != target.ContractVersion},
		{changedRulePackVersion, base.RulePackVersion != target.RulePackVersion},
		{changedObjectBinding, base.BindingStatus != target.BindingStatus || base.BindingVersion != target.BindingVersion},
		{changedExternalObject, base.ExternalObjectID != target.ExternalObjectID},
		{changedSourceSystem, base.SourceSystem != target.SourceSystem},
		{changedModelConfiguration, base.ModelConfigurationID != target.ModelConfigurationID},
		{changedPlannerConfiguration, base.PlannerConfigurationID != target.PlannerConfigurationID},
		{changedParserConfiguration, base.ParserConfigurationID != target.ParserConfigurationID},
		{changedBusinessAt, !base.BusinessAt.Equal(target.BusinessAt)},
	} {
		if input.changed {
			a.addChange(input.ref)
			a.force(fallbackInput)
		}
	}
	if base.SnapshotSetID != target.SnapshotSetID {
		a.addChange(changedExternalSnapshotSet)
	}
	setChanged, factChanged, refs := compareFacts(base, target)
	for _, ref := range refs {
		a.addChange(ref)
	}
	if setChanged {
		a.force(fallbackFactSet)
	}
	if factChanged {
		a.force(fallbackFact)
	}
}

func (a *planAnalysis) check(base, target auditext.CheckResult, exists bool, snapshotSet string, snapshotChanged bool) auditext.ImpactCheck {
	result := auditext.ImpactCheck{CheckID: target.CheckID, Action: auditext.ImpactRecompute}
	if !exists {
		result.Reasons = []string{reasonNewCheck}
		return result
	}
	a.dependencies(base, target, snapshotSet)
	if snapshotChanged && !externalEvidenceRebound(base, target) {
		a.force(fallbackEvidenceRebind)
	}
	// Calculate the dependency closure once for both the candidate guard and
	// the emitted action. Full fallbacks override these provisional actions.
	if dependentChanged(base, target, snapshotChanged) {
		result.Reasons = []string{reasonDependentFieldChanged}
		return result
	}
	if !sameCheckResult(base, target) {
		a.force(fallbackCandidateMismatch)
	}
	result.Action = auditext.ImpactReuseCandidate
	result.Reasons = []string{reasonUnchangedDependencies}
	return result
}

func (a *planAnalysis) dependencies(base, target auditext.CheckResult, snapshotSet string) {
	baseDeps := dependencyIndex(base.Dependencies.Items)
	targetDeps := dependencyIndex(target.Dependencies.Items)
	for key, item := range targetDeps {
		before, exists := baseDeps[key]
		if !exists {
			a.unknown(item)
			continue
		}
		if before.Version != item.Version || before.Present != item.Present {
			a.dependencyChange(before, item, snapshotSet)
		}
	}
	for key, item := range baseDeps {
		if _, exists := targetDeps[key]; !exists {
			a.unknown(item)
		}
	}
}

func (a *planAnalysis) unknown(item auditext.Dependency) {
	a.force(fallbackUnknownDependency)
	addDependencyChange(a.addChange, item)
}

func (a *planAnalysis) dependencyChange(before, item auditext.Dependency, snapshotSet string) {
	switch item.Kind {
	case auditext.DependencyField:
		addDependencyChange(a.addChange, item)
	case auditext.DependencyRecordSet:
		if before.Present != item.Present {
			a.force(fallbackRecordSet)
			addDependencyChange(a.addChange, item)
		}
		if snapshotSet != "" && item.Version != snapshotSet {
			a.force(fallbackEvidenceRebind)
		}
	default:
		// Only field and record-set changes support a partial explanation.
		a.force(fallbackUnknownDependency)
	}
}
