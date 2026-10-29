package auditext

import (
	"fmt"
	"regexp"
)

var (
	idPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}$`)
	versionPattern  = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	quantityPattern = regexp.MustCompile(`^\d+(\.\d+)?$`)
)

// ValidateID checks the identifier shape shared by the capability pack,
// observation and impact-plan schemas.
func ValidateID(value string) error {
	if !idPattern.MatchString(value) {
		return fmt.Errorf("%w: identifier", ErrInvalidRequest)
	}
	return nil
}

// ValidateVersion checks the three-part capability pack version.
func ValidateVersion(value string) error {
	if !versionPattern.MatchString(value) {
		return fmt.Errorf("%w: version", ErrInvalidRequest)
	}
	return nil
}

// ValidateQuantity checks an exact non-negative decimal with a non-empty unit.
func ValidateQuantity(quantity ExactQuantity) error {
	if !quantityPattern.MatchString(quantity.Value) || len(quantity.Value) > 64 {
		return fmt.Errorf("%w: quantity value", ErrInvalidRequest)
	}
	if quantity.Unit == "" || len(quantity.Unit) > 64 {
		return fmt.Errorf("%w: quantity unit", ErrInvalidRequest)
	}
	return nil
}

func (s AuthorizedScope) Validate() error {
	if ValidateID(s.TenantID) != nil || ValidateID(s.CaseID) != nil || ValidateID(s.ActorID) != nil {
		return fmt.Errorf("%w: authorized scope", ErrInvalidRequest)
	}
	if s.GrantVersion == "" || len(s.GrantVersion) > 128 {
		return fmt.Errorf("%w: grant version", ErrInvalidRequest)
	}
	return nil
}

func (p PackRef) Validate() error {
	if ValidateID(p.ID) != nil || ValidateVersion(p.Version) != nil {
		return fmt.Errorf("%w: pack reference", ErrInvalidRequest)
	}
	return nil
}

func (f Field) Validate() error {
	if err := validateFieldIdentity(f); err != nil {
		return err
	}
	if err := validateFieldSource(f); err != nil {
		return err
	}
	return nil
}

// validateFieldIdentity checks the field name and lexical value.
func validateFieldIdentity(f Field) error {
	if ValidateID(f.Name) != nil {
		return fmt.Errorf("%w: field name", ErrInvalidRequest)
	}
	if !quantityPattern.MatchString(f.Value) || len(f.Value) > 64 {
		return fmt.Errorf("%w: field value", ErrInvalidRequest)
	}
	return nil
}

// validateFieldSource checks the unit, JSON pointer and semantic kind of the
// field's source binding.
func validateFieldSource(f Field) error {
	if f.Unit != "Mbit/s" && f.Unit != "Gbit/s" {
		return fmt.Errorf("%w: field unit", ErrInvalidRequest)
	}
	if len(f.SourcePointer) < 1 || f.SourcePointer[0] != '/' || len(f.SourcePointer) > 512 {
		return fmt.Errorf("%w: field pointer", ErrInvalidRequest)
	}
	if f.SemanticKind != "configured_value" {
		return fmt.Errorf("%w: field semantic kind", ErrInvalidRequest)
	}
	return nil
}

// Validate checks one immutable external observation against the server-bound
// scope. A tenant or case mismatch is an authorization failure, not a shape
// error, so callers do not leak snapshot content.
func (o Observation) Validate(scope AuthorizedScope) error {
	if err := validateObservationHeader(o, scope); err != nil {
		return err
	}
	if err := validateIDGroup("observation identity", o.SnapshotID, o.ConnectorID, o.SourceSystem, o.ObjectBindingVersion, o.ExternalObjectID, o.RecordID); err != nil {
		return err
	}
	if err := validateObservationRecordVersion(o); err != nil {
		return err
	}
	if err := validateObservationTimes(o); err != nil {
		return err
	}
	if err := validateObservationPayload(o); err != nil {
		return err
	}
	if err := validateBounded(o.Fields, 32, "too many fields", Field.Validate); err != nil {
		return err
	}
	if o.DataClass != DataClassSynthetic {
		return fmt.Errorf("%w: data class", ErrInvalidRequest)
	}
	return nil
}

// validateObservationHeader checks the schema, the bound scope and whether the
// observation belongs to that scope.
func validateObservationHeader(o Observation, scope AuthorizedScope) error {
	if o.SchemaVersion != ExternalObservationSchema {
		return fmt.Errorf("%w: observation schema", ErrInvalidRequest)
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	if o.TenantID != scope.TenantID || o.CaseID != scope.CaseID {
		return ErrUnauthorized
	}
	return nil
}

// validateIDGroup checks a group of identifiers and reports the section that
// rejected them.
func validateIDGroup(section string, values ...string) error {
	for _, value := range values {
		if ValidateID(value) != nil {
			return fmt.Errorf("%w: %s", ErrInvalidRequest, section)
		}
	}
	return nil
}

// validateBounded checks a slice limit and every element through validate.
func validateBounded[T any](items []T, limit int, section string, validate func(T) error) error {
	if len(items) > limit {
		return fmt.Errorf("%w: %s", ErrInvalidRequest, section)
	}
	for _, item := range items {
		if err := validate(item); err != nil {
			return err
		}
	}
	return nil
}

// validateObservationRecordVersion checks the optional source record version.
func validateObservationRecordVersion(o Observation) error {
	if o.RecordVersion != nil && (len(*o.RecordVersion) == 0 || len(*o.RecordVersion) > 128) {
		return fmt.Errorf("%w: record version", ErrInvalidRequest)
	}
	return nil
}

// validateObservationTimes checks observation instants, the effective interval
// and the availability vocabulary.
func validateObservationTimes(o Observation) error {
	if o.ObservedAt.IsZero() || o.EffectiveFrom.IsZero() || o.EffectiveUntil.IsZero() {
		return fmt.Errorf("%w: observation time", ErrInvalidRequest)
	}
	if !o.EffectiveFrom.Before(o.EffectiveUntil) {
		return fmt.Errorf("%w: effective interval", ErrInvalidRequest)
	}
	if !o.Availability.Valid() {
		return fmt.Errorf("%w: availability", ErrInvalidRequest)
	}
	return nil
}

// validateObservationPayload checks the reason code, payload reference and the
// payload/field requirement of an available observation.
func validateObservationPayload(o Observation) error {
	if ValidateID(o.ReasonCode) != nil {
		return fmt.Errorf("%w: reason code", ErrInvalidRequest)
	}
	if o.PayloadRef != nil && len(*o.PayloadRef) > 512 {
		return fmt.Errorf("%w: payload reference", ErrInvalidRequest)
	}
	if o.Availability == AvailabilityAvailable {
		if o.PayloadRef == nil || *o.PayloadRef == "" {
			return fmt.Errorf("%w: available observation requires a payload", ErrInvalidRequest)
		}
		if len(o.Fields) == 0 {
			return fmt.Errorf("%w: available observation requires fields", ErrInvalidRequest)
		}
	}
	return nil
}

func (f Fact) Validate() error {
	if ValidateID(f.Name) != nil {
		return fmt.Errorf("%w: fact name", ErrInvalidRequest)
	}
	if err := ValidateQuantity(f.Value); err != nil {
		return err
	}
	if len(f.Evidence) > 16 {
		return fmt.Errorf("%w: too much fact evidence", ErrInvalidRequest)
	}
	for _, evidence := range f.Evidence {
		if err := evidence.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks a frozen evaluation input. It verifies shape and scope only;
// accepting the input remains a server-side transaction decision.
func (f FrozenInput) Validate() error {
	if err := f.Scope.Validate(); err != nil {
		return err
	}
	if err := f.Pack.Validate(); err != nil {
		return err
	}
	if err := validateIDGroup("frozen input identity", f.ContractVersion, f.SnapshotSetID, f.ModelConfigurationID, f.PlannerConfigurationID, f.ParserConfigurationID); err != nil {
		return err
	}
	if err := validateFrozenBinding(f); err != nil {
		return err
	}
	if f.RulePackVersion == "" || len(f.RulePackVersion) > 128 {
		return fmt.Errorf("%w: rule pack version", ErrInvalidRequest)
	}
	if f.BusinessAt.IsZero() {
		return fmt.Errorf("%w: business time", ErrInvalidRequest)
	}
	if err := validateFrozenFacts(f); err != nil {
		return err
	}
	return nil
}

// validateFrozenBinding checks the binding status and the identities that the
// status requires.
func validateFrozenBinding(f FrozenInput) error {
	if err := validateFrozenBindingStatus(f); err != nil {
		return err
	}
	if err := validateFrozenBindingIdentity(f); err != nil {
		return err
	}
	return nil
}

// validateFrozenBindingStatus checks the binding status vocabulary.
func validateFrozenBindingStatus(f FrozenInput) error {
	if !f.BindingStatus.Valid() {
		return fmt.Errorf("%w: binding status", ErrInvalidRequest)
	}
	return nil
}

// validateFrozenBindingIdentity checks the binding fields that an unconfirmed
// or confirmed status allows.
func validateFrozenBindingIdentity(f FrozenInput) error {
	if f.BindingStatus == BindingConfirmed {
		return validateConfirmedBindingIdentity(f)
	}
	return validateUnconfirmedBindingIdentity(f)
}

// validateConfirmedBindingIdentity requires every confirmed binding identity.
func validateConfirmedBindingIdentity(f FrozenInput) error {
	if ValidateID(f.BindingVersion) != nil || ValidateID(f.ExternalObjectID) != nil || ValidateID(f.SourceSystem) != nil {
		return fmt.Errorf("%w: confirmed binding", ErrInvalidRequest)
	}
	return nil
}

// validateUnconfirmedBindingIdentity allows empty optional identities but
// still checks any that are present.
func validateUnconfirmedBindingIdentity(f FrozenInput) error {
	if (f.BindingVersion != "" && ValidateID(f.BindingVersion) != nil) ||
		(f.ExternalObjectID != "" && ValidateID(f.ExternalObjectID) != nil) ||
		(f.SourceSystem != "" && ValidateID(f.SourceSystem) != nil) {
		return fmt.Errorf("%w: unconfirmed binding", ErrInvalidRequest)
	}
	return nil
}

// validateFrozenFacts checks the fact limit, uniqueness and each fact.
func validateFrozenFacts(f FrozenInput) error {
	if len(f.Facts) > 64 {
		return fmt.Errorf("%w: too many facts", ErrInvalidRequest)
	}
	seen := make(map[string]bool, len(f.Facts))
	for _, fact := range f.Facts {
		if seen[fact.Name] {
			return fmt.Errorf("%w: duplicate fact", ErrInvalidRequest)
		}
		seen[fact.Name] = true
		if err := fact.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (e EvidenceRef) Validate() error {
	switch e.Kind {
	case EvidenceDocument:
		if err := validateDocumentEvidenceIdentity(e); err != nil {
			return err
		}
		if err := validateDocumentEvidenceContent(e); err != nil {
			return err
		}
	case EvidenceExternalSnapshot:
		if err := validateSnapshotEvidenceIdentity(e); err != nil {
			return err
		}
		if err := validateSnapshotEvidenceContent(e); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: evidence kind", ErrInvalidRequest)
	}
	return nil
}

// validateDocumentEvidenceIdentity checks the document version pair and span.
func validateDocumentEvidenceIdentity(e EvidenceRef) error {
	if ValidateID(e.DocumentVersionID) != nil || ValidateID(e.ParsedVersionID) != nil {
		return fmt.Errorf("%w: document evidence identity", ErrInvalidRequest)
	}
	if e.Start < 0 || e.End <= e.Start {
		return fmt.Errorf("%w: document evidence range", ErrInvalidRequest)
	}
	return nil
}

// validateDocumentEvidenceContent checks the quote and rejects snapshot fields
// on document evidence.
func validateDocumentEvidenceContent(e EvidenceRef) error {
	if e.Quote == "" || len(e.Quote) > 4096 {
		return fmt.Errorf("%w: document evidence quote", ErrInvalidRequest)
	}
	if e.SnapshotID != "" || e.RecordID != "" || e.FieldPointer != "" || e.DisplayValue != "" || !e.ObservedAt.IsZero() {
		return fmt.Errorf("%w: mixed document evidence", ErrInvalidRequest)
	}
	return nil
}

// validateSnapshotEvidenceIdentity checks the snapshot identity, pointer, value
// and observation time.
func validateSnapshotEvidenceIdentity(e EvidenceRef) error {
	if ValidateID(e.SnapshotID) != nil || ValidateID(e.RecordID) != nil {
		return fmt.Errorf("%w: snapshot evidence identity", ErrInvalidRequest)
	}
	if len(e.FieldPointer) < 1 || e.FieldPointer[0] != '/' || len(e.FieldPointer) > 512 {
		return fmt.Errorf("%w: snapshot evidence pointer", ErrInvalidRequest)
	}
	if e.DisplayValue == "" || len(e.DisplayValue) > 256 {
		return fmt.Errorf("%w: snapshot evidence value", ErrInvalidRequest)
	}
	if e.ObservedAt.IsZero() {
		return fmt.Errorf("%w: snapshot evidence time", ErrInvalidRequest)
	}
	return nil
}

// validateSnapshotEvidenceContent rejects document fields on snapshot evidence.
func validateSnapshotEvidenceContent(e EvidenceRef) error {
	if e.DocumentVersionID != "" || e.ParsedVersionID != "" || e.Start != 0 || e.End != 0 || e.Quote != "" {
		return fmt.Errorf("%w: mixed snapshot evidence", ErrInvalidRequest)
	}
	return nil
}

func (r CheckResult) Validate() error {
	if err := validateCheckResultIdentity(r); err != nil {
		return err
	}
	if err := validateBounded(r.Evidence, 16, "too much check evidence", EvidenceRef.Validate); err != nil {
		return err
	}
	if err := validateCheckResultLimitations(r); err != nil {
		return err
	}
	return nil
}

// validateCheckResultIdentity checks the check identifier, relation, reason and
// machine status.
func validateCheckResultIdentity(r CheckResult) error {
	if ValidateID(r.CheckID) != nil {
		return fmt.Errorf("%w: check id", ErrInvalidRequest)
	}
	if !r.Relation.Valid() {
		return fmt.Errorf("%w: relation", ErrInvalidRequest)
	}
	if ValidateID(r.ReasonCode) != nil {
		return fmt.Errorf("%w: reason code", ErrInvalidRequest)
	}
	if !r.MachineStatus.Valid() {
		return fmt.Errorf("%w: machine status", ErrInvalidRequest)
	}
	return nil
}

// validateCheckResultLimitations checks the limitation limit and each text.
func validateCheckResultLimitations(r CheckResult) error {
	if len(r.Limitations) > 16 {
		return fmt.Errorf("%w: too many limitations", ErrInvalidRequest)
	}
	for _, limitation := range r.Limitations {
		if limitation == "" || len(limitation) > 256 {
			return fmt.Errorf("%w: limitation", ErrInvalidRequest)
		}
	}
	return nil
}

func (p ImpactPlan) Validate() error {
	if err := validateImpactPlanHeader(p); err != nil {
		return err
	}
	if err := validateImpactPlanFallback(p); err != nil {
		return err
	}
	if err := validateImpactChangedInputs(p.ChangedInputs); err != nil {
		return err
	}
	if err := validateImpactChecks(p.Checks); err != nil {
		return err
	}
	if p.BusinessResultSource != "full" {
		return fmt.Errorf("%w: business result source", ErrInvalidRequest)
	}
	if p.DataClass != DataClassSynthetic {
		return fmt.Errorf("%w: data class", ErrInvalidRequest)
	}
	return nil
}

// validateImpactPlanHeader checks the schema, evaluation identifiers and the
// mode/fallback vocabulary.
func validateImpactPlanHeader(p ImpactPlan) error {
	if p.SchemaVersion != ImpactPlanSchema {
		return fmt.Errorf("%w: impact plan schema", ErrInvalidRequest)
	}
	if ValidateID(p.BaseEvaluationID) != nil || ValidateID(p.TargetEvaluationID) != nil {
		return fmt.Errorf("%w: impact plan evaluation", ErrInvalidRequest)
	}
	if !p.Mode.Valid() || !p.Fallback.Valid() {
		return fmt.Errorf("%w: impact plan mode", ErrInvalidRequest)
	}
	return nil
}

// validateImpactPlanFallback checks dependency completeness against the
// fallback and the fallback reason.
func validateImpactPlanFallback(p ImpactPlan) error {
	if p.DependencyComplete && p.Fallback != FallbackNone {
		return fmt.Errorf("%w: complete dependencies require no fallback", ErrInvalidRequest)
	}
	if !p.DependencyComplete && p.Fallback == FallbackNone {
		return fmt.Errorf("%w: incomplete dependencies require a fallback", ErrInvalidRequest)
	}
	if p.Fallback == FallbackNone {
		if p.FallbackReason != "" {
			return fmt.Errorf("%w: unused fallback reason", ErrInvalidRequest)
		}
	} else if ValidateID(p.FallbackReason) != nil {
		return fmt.Errorf("%w: fallback reason", ErrInvalidRequest)
	}
	return nil
}

// validateImpactChangedInputs checks the changed-input limit and identifiers.
func validateImpactChangedInputs(inputs []string) error {
	if len(inputs) > 128 {
		return fmt.Errorf("%w: too many changed inputs", ErrInvalidRequest)
	}
	for _, input := range inputs {
		if ValidateID(input) != nil {
			return fmt.Errorf("%w: changed input", ErrInvalidRequest)
		}
	}
	return nil
}

// validateImpactChecks checks the check limit and every check.
func validateImpactChecks(checks []ImpactCheck) error {
	if len(checks) == 0 || len(checks) > 128 {
		return fmt.Errorf("%w: impact checks", ErrInvalidRequest)
	}
	for _, check := range checks {
		if ValidateID(check.CheckID) != nil || !check.Action.Valid() {
			return fmt.Errorf("%w: impact check", ErrInvalidRequest)
		}
		if err := validateImpactCheckReasons(check.Reasons); err != nil {
			return err
		}
	}
	return nil
}

// validateImpactCheckReasons checks the reason limit and identifiers of one
// planned check.
func validateImpactCheckReasons(reasons []string) error {
	if len(reasons) == 0 || len(reasons) > 32 {
		return fmt.Errorf("%w: impact reasons", ErrInvalidRequest)
	}
	for _, reason := range reasons {
		if ValidateID(reason) != nil {
			return fmt.Errorf("%w: impact reason", ErrInvalidRequest)
		}
	}
	return nil
}

// ValidateDependencySet checks the internal provenance shape used by the impact
// planner. It is separate from the public check-result schema.
func ValidateDependencySet(set DependencySet) error {
	if len(set.Items) > 256 {
		return fmt.Errorf("%w: too many dependencies", ErrInvalidRequest)
	}
	for _, item := range set.Items {
		switch item.Kind {
		case DependencyField, DependencyRecordSet, DependencyMapping, DependencyRulePack,
			DependencyCapabilityPack, DependencyDocument, DependencyParser, DependencyModel, DependencyPolicy:
		default:
			return fmt.Errorf("%w: dependency kind", ErrInvalidRequest)
		}
		if ValidateID(item.Ref) != nil {
			return fmt.Errorf("%w: dependency reference", ErrInvalidRequest)
		}
	}
	return nil
}
