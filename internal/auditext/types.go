// Package auditext defines the versioned DTOs shared by the operator audit
// extension modules. It is deliberately independent of HTTP and storage.
//
// AuthorizedScope is not an authorization mechanism. Consumers must construct it
// from the server-side session and case grant; it must never be decoded from a
// client request body.
package auditext

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	// HostContractVersion is the only host API version accepted by capability
	// pack manifests in this release.
	HostContractVersion = "auditext/v1"

	CapabilityPackSchemaVersion = "audit-capability-pack/v1"
	ExternalObservationSchema   = "external-observation/v1"
	ImpactPlanSchema            = "impact-plan/v1"
	DataClassSynthetic          = "synthetic"
)

var (
	ErrInvalidRequest       = errors.New("auditext: invalid request")
	ErrUnauthorized         = errors.New("auditext: unauthorized")
	ErrUnsupported          = errors.New("auditext: unsupported")
	ErrUnavailable          = errors.New("auditext: unavailable")
	ErrDependencyIncomplete = errors.New("auditext: dependency incomplete")
	ErrVersionMismatch      = errors.New("auditext: version mismatch")
)

// Relation is the deterministic business relation. It intentionally matches the
// existing review vocabulary; no new "automatically approved" state exists.
type Relation string

const (
	RelationCompatible    Relation = "compatible"
	RelationConflict      Relation = "conflict"
	RelationInsufficient  Relation = "insufficient"
	RelationNotApplicable Relation = "not_applicable"
)

func (r Relation) Valid() bool {
	switch r {
	case RelationCompatible, RelationConflict, RelationInsufficient, RelationNotApplicable:
		return true
	default:
		return false
	}
}

// MachineStatus describes execution, separately from the business relation.
type MachineStatus string

const (
	MachineCompleted MachineStatus = "completed"
	MachinePartial   MachineStatus = "partial"
	MachineFailed    MachineStatus = "failed"
	MachineCancelled MachineStatus = "cancelled"
)

func (s MachineStatus) Valid() bool {
	switch s {
	case MachineCompleted, MachinePartial, MachineFailed, MachineCancelled:
		return true
	default:
		return false
	}
}

// Availability describes whether an external observation can be used as
// evidence. It is not a business relation.
type Availability string

const (
	AvailabilityAvailable   Availability = "available"
	AvailabilityUnavailable Availability = "unavailable"
	AvailabilityStale       Availability = "stale"
	AvailabilityRejected    Availability = "rejected"
)

func (a Availability) Valid() bool {
	switch a {
	case AvailabilityAvailable, AvailabilityUnavailable, AvailabilityStale, AvailabilityRejected:
		return true
	default:
		return false
	}
}

// EvidenceKind separates contract text from an external snapshot. An API
// response must never be turned into a synthetic document citation.
type EvidenceKind string

const (
	EvidenceDocument         EvidenceKind = "document"
	EvidenceExternalSnapshot EvidenceKind = "external_snapshot"
)

// ExactQuantity keeps the lexical decimal value so callers can normalize units
// without floating point rounding.
type ExactQuantity struct {
	Value string `json:"value"`
	Unit  string `json:"unit"`
}

// PackRef pins a capability pack version.
type PackRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// AuthorizedScope is constructed by the server after session and case
// authorization. GrantVersion lets a later check notice that authorization was
// revoked between collection and acceptance.
type AuthorizedScope struct {
	TenantID     string
	CaseID       string
	ActorID      string
	GrantVersion string
}

// BindingStatus records whether a human-confirmed contract-to-external-object
// mapping existed when the evaluation input was frozen. Missing and ambiguous
// mappings are evidence-insufficient, never similarity-matched.
type BindingStatus string

const (
	BindingConfirmed BindingStatus = "confirmed"
	BindingMissing   BindingStatus = "missing"
	BindingAmbiguous BindingStatus = "ambiguous"
)

func (s BindingStatus) Valid() bool {
	switch s {
	case BindingConfirmed, BindingMissing, BindingAmbiguous:
		return true
	default:
		return false
	}
}

// Fact is a contract-bound value with its document evidence. Facts are frozen
// into the evaluation input; callers must not fill them from a model response.
type Fact struct {
	Name         string        `json:"name"`
	Value        ExactQuantity `json:"value"`
	Evidence     []EvidenceRef `json:"evidence"`
	Uncertain    bool          `json:"uncertain"`
	OutOfScope   bool          `json:"out_of_scope"`
	SemanticKind string        `json:"semantic_kind"`
}

// ReadRequest asks a registered connector for a bounded, read-only record.
// ExternalObjectID is an identifier, never a URL.
type ReadRequest struct {
	Scope            AuthorizedScope
	ConnectorID      string
	BindingVersion   string
	ExternalObjectID string
	BusinessAt       time.Time
}

// Field is a normalized source field with its JSON Pointer in the raw payload.
type Field struct {
	Name          string `json:"name"`
	Value         string `json:"value"`
	Unit          string `json:"unit"`
	SourcePointer string `json:"source_pointer"`
	SemanticKind  string `json:"semantic_kind"`
}

// Observation is an immutable normalized snapshot of one external record.
type Observation struct {
	SchemaVersion        string       `json:"schema_version"`
	SnapshotID           string       `json:"snapshot_id"`
	TenantID             string       `json:"tenant_id"`
	CaseID               string       `json:"case_id"`
	ConnectorID          string       `json:"connector_id"`
	SourceSystem         string       `json:"source_system"`
	ObjectBindingVersion string       `json:"object_binding_version"`
	ExternalObjectID     string       `json:"external_object_id"`
	RecordID             string       `json:"record_id"`
	RecordVersion        *string      `json:"record_version"`
	ObservedAt           time.Time    `json:"observed_at"`
	SourceUpdatedAt      *time.Time   `json:"source_updated_at"`
	EffectiveFrom        time.Time    `json:"effective_from"`
	EffectiveUntil       time.Time    `json:"effective_until"`
	Availability         Availability `json:"availability"`
	ReasonCode           string       `json:"reason_code"`
	PayloadRef           *string      `json:"payload_ref"`
	Fields               []Field      `json:"fields"`
	DataClass            string       `json:"data_class"`
}

// FieldByName returns the first field with the given normalized name.
func (o Observation) FieldByName(name string) (Field, bool) {
	for _, field := range o.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return Field{}, false
}

// EffectiveAt reports whether the observation's declared validity interval
// covers the business instant.
func (o Observation) EffectiveAt(at time.Time) bool {
	return !o.EffectiveFrom.After(at) && at.Before(o.EffectiveUntil)
}

// ReadConnector is the only external read surface. Implementations must use a
// deployment-controlled endpoint and must not accept arbitrary URLs or
// credentials from callers.
type ReadConnector interface {
	ID() string
	Read(context.Context, ReadRequest) (Observation, error)
}

// EvidenceRef is a union of contract-document evidence and external-snapshot
// evidence. MarshalJSON emits exactly one shape so the JSON contract stays
// closed.
type EvidenceRef struct {
	Kind              EvidenceKind
	DocumentVersionID string
	ParsedVersionID   string
	Start             int
	End               int
	Quote             string
	SnapshotID        string
	RecordID          string
	FieldPointer      string
	DisplayValue      string
	ObservedAt        time.Time
}

// UnmarshalJSON accepts the same closed evidence shapes. The struct has no
// snake_case field tags because callers use named Go fields; persisted JSON is
// re-hydrated through this method.
func (e *EvidenceRef) UnmarshalJSON(data []byte) error {
	if e == nil {
		return ErrInvalidRequest
	}
	var wire struct {
		Kind              string    `json:"kind"`
		DocumentVersionID string    `json:"document_version_id"`
		ParsedVersionID   string    `json:"parsed_version_id"`
		Start             int       `json:"start"`
		End               int       `json:"end"`
		Quote             string    `json:"quote"`
		SnapshotID        string    `json:"snapshot_id"`
		RecordID          string    `json:"record_id"`
		FieldPointer      string    `json:"field_pointer"`
		DisplayValue      string    `json:"display_value"`
		ObservedAt        time.Time `json:"observed_at"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	evidence := EvidenceRef{
		Kind: EvidenceKind(wire.Kind), DocumentVersionID: wire.DocumentVersionID,
		ParsedVersionID: wire.ParsedVersionID, Start: wire.Start, End: wire.End, Quote: wire.Quote,
		SnapshotID: wire.SnapshotID, RecordID: wire.RecordID, FieldPointer: wire.FieldPointer,
		DisplayValue: wire.DisplayValue, ObservedAt: wire.ObservedAt,
	}
	switch evidence.Kind {
	case EvidenceDocument:
		evidence.SnapshotID, evidence.RecordID, evidence.FieldPointer = "", "", ""
		evidence.DisplayValue, evidence.ObservedAt = "", time.Time{}
	case EvidenceExternalSnapshot:
		evidence.DocumentVersionID, evidence.ParsedVersionID = "", ""
		evidence.Start, evidence.End, evidence.Quote = 0, 0, ""
	default:
		return ErrInvalidRequest
	}
	*e = evidence
	return nil
}

func (e EvidenceRef) MarshalJSON() ([]byte, error) {
	switch e.Kind {
	case EvidenceDocument:
		return json.Marshal(struct {
			Kind              string `json:"kind"`
			DocumentVersionID string `json:"document_version_id"`
			ParsedVersionID   string `json:"parsed_version_id"`
			Start             int    `json:"start"`
			End               int    `json:"end"`
			Quote             string `json:"quote"`
		}{string(EvidenceDocument), e.DocumentVersionID, e.ParsedVersionID, e.Start, e.End, e.Quote})
	case EvidenceExternalSnapshot:
		return json.Marshal(struct {
			Kind         string    `json:"kind"`
			SnapshotID   string    `json:"snapshot_id"`
			RecordID     string    `json:"record_id"`
			FieldPointer string    `json:"field_pointer"`
			DisplayValue string    `json:"display_value"`
			ObservedAt   time.Time `json:"observed_at"`
		}{string(EvidenceExternalSnapshot), e.SnapshotID, e.RecordID, e.FieldPointer, e.DisplayValue, e.ObservedAt})
	default:
		return nil, ErrInvalidRequest
	}
}

// DependencyKind identifies one input that a deterministic check read, including
// negative reads and set membership.
type DependencyKind string

const (
	DependencyField          DependencyKind = "field"
	DependencyRecordSet      DependencyKind = "record_set"
	DependencyMapping        DependencyKind = "mapping"
	DependencyRulePack       DependencyKind = "rule_pack"
	DependencyCapabilityPack DependencyKind = "capability_pack"
	DependencyDocument       DependencyKind = "document"
	DependencyParser         DependencyKind = "parser"
	DependencyModel          DependencyKind = "model"
	DependencyPolicy         DependencyKind = "policy"
)

// Dependency is a single declared input. Version and Present are recorded even
// when a record or field was absent: a later addition can invalidate reuse.
type Dependency struct {
	Kind    DependencyKind `json:"kind"`
	Ref     string         `json:"ref"`
	Version string         `json:"version"`
	Present bool           `json:"present"`
}

// DependencySet is the complete dependency closure for one check.
type DependencySet struct {
	Complete bool         `json:"complete"`
	Items    []Dependency `json:"items"`
}

// FrozenInput pins every version and source used by one evaluation. It must be
// assembled inside the server-side acceptance transaction.
type FrozenInput struct {
	Scope                  AuthorizedScope
	Pack                   PackRef
	ContractVersion        string
	RulePackVersion        string
	BindingStatus          BindingStatus
	BindingVersion         string
	SnapshotSetID          string
	ExternalObjectID       string
	SourceSystem           string
	ModelConfigurationID   string
	PlannerConfigurationID string
	ParserConfigurationID  string
	BusinessAt             time.Time
	Facts                  []Fact
}

// FactByName returns the first contract fact with the given name.
func (f FrozenInput) FactByName(name string) (Fact, bool) {
	for _, fact := range f.Facts {
		if fact.Name == name {
			return fact, true
		}
	}
	return Fact{}, false
}

// CheckResult is the machine output for one deterministic check. Human
// decisions remain in the existing decision flow and are never stored here.
type CheckResult struct {
	CheckID             string        `json:"check_id"`
	Relation            Relation      `json:"relation"`
	ReasonCode          string        `json:"reason_code"`
	MachineStatus       MachineStatus `json:"machine_status"`
	Evidence            []EvidenceRef `json:"evidence"`
	RequiresHumanReview bool          `json:"requires_human_review"`
	Limitations         []string      `json:"limitations"`

	// Dependencies is internal provenance for the impact planner. It is not part
	// of the check-result JSON contract.
	Dependencies DependencySet `json:"-"`
}

// ImpactMode is the conservative incremental-review mode. Reuse is deliberately
// absent from this release.
type ImpactMode string

const (
	ImpactExplain ImpactMode = "explain"
	ImpactShadow  ImpactMode = "shadow"
)

func (m ImpactMode) Valid() bool {
	return m == ImpactExplain || m == ImpactShadow
}

// ImpactAction is the per-check plan.
type ImpactAction string

const (
	ImpactRecompute      ImpactAction = "recompute"
	ImpactReuseCandidate ImpactAction = "reuse_candidate"
	ImpactDeny           ImpactAction = "deny"
)

func (a ImpactAction) Valid() bool {
	switch a {
	case ImpactRecompute, ImpactReuseCandidate, ImpactDeny:
		return true
	default:
		return false
	}
}

// Fallback is the safe outcome when dependency completeness cannot be proven.
type Fallback string

const (
	FallbackNone Fallback = "none"
	FallbackFull Fallback = "full"
	FallbackDeny Fallback = "deny"
)

func (f Fallback) Valid() bool {
	switch f {
	case FallbackNone, FallbackFull, FallbackDeny:
		return true
	default:
		return false
	}
}

// ImpactCheck is one check's conservative plan.
type ImpactCheck struct {
	CheckID string       `json:"check_id"`
	Action  ImpactAction `json:"action"`
	Reasons []string     `json:"reasons"`
}

// ImpactPlan never supersedes the full business result.
type ImpactPlan struct {
	SchemaVersion        string        `json:"schema_version"`
	BaseEvaluationID     string        `json:"base_evaluation_id"`
	TargetEvaluationID   string        `json:"target_evaluation_id"`
	Mode                 ImpactMode    `json:"mode"`
	DependencyComplete   bool          `json:"dependency_complete"`
	Fallback             Fallback      `json:"fallback"`
	FallbackReason       string        `json:"fallback_reason"`
	ChangedInputs        []string      `json:"changed_inputs"`
	Checks               []ImpactCheck `json:"checks"`
	BusinessResultSource string        `json:"business_result_source"`
	DataClass            string        `json:"data_class"`
}

// ImpactRequest bundles the two frozen evaluations and their check provenance.
// Evaluation identifiers are assigned by the caller's acceptance transaction.
type ImpactRequest struct {
	BaseEvaluationID   string
	TargetEvaluationID string
	Base               FrozenInput
	Target             FrozenInput
	BaseChecks         []CheckResult
	TargetChecks       []CheckResult
	Mode               ImpactMode
}

// Evaluator is a pure deterministic check runner.
type Evaluator interface {
	Evaluate(context.Context, FrozenInput, []Observation) ([]CheckResult, error)
}

// ImpactPlanner compares two frozen inputs and emits explain/shadow plans.
type ImpactPlanner interface {
	Plan(context.Context, ImpactRequest) (ImpactPlan, error)
}
