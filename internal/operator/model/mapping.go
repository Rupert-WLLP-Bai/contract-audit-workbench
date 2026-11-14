package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"contractaudit/internal/auditext"
	subjectModel "contractaudit/internal/subject/model"
)

// TrustedPrincipalProjection remains owned by Subject and is reused here so
// operator decisions compare the same authenticated principal representation.
type TrustedPrincipalProjection = subjectModel.TrustedPrincipalProjection

// OperatorObjectBinding is the public binding payload shared with the frozen
// service-object mapping projection. RequestSHA256 belongs to its storage
// record and is deliberately not part of this JSON value.
type OperatorObjectBinding = ServiceObjectMappingBindingPayload

type OperatorObjectBindingInput struct {
	ContractObject OperatorContractObject `json:"contract_object"`
	ExternalObject OperatorExternalObject `json:"external_object"`
	// ServiceObjectID and the two expected versions pin a new operator mapping
	// to the exact confirmed object identity and contract association. They are
	// optional only so an already persisted legacy idempotency request can be
	// replayed byte-for-byte; fresh writes require all three values.
	ServiceObjectID            string    `json:"service_object_id,omitempty"`
	ExpectedObjectVersion      int       `json:"expected_object_version,omitempty"`
	ExpectedAssociationVersion int       `json:"expected_association_version,omitempty"`
	EffectiveFrom              time.Time `json:"effective_from"`
	EffectiveUntil             time.Time `json:"effective_until"`
	Confirmed                  bool      `json:"confirmed"`
}

// OperatorObjectBindingRecord carries the independently stored digest next to
// the complete public response value. It is not a wire DTO.
type OperatorObjectBindingRecord struct {
	Value         ServiceObjectMappingBindingPayload `json:"-"`
	RequestSHA256 string                             `json:"-"`
}

type OperatorMappingHead struct {
	CaseID                string    `json:"case_id"`
	MappingID             string    `json:"mapping_id"`
	ServiceObjectID       string    `json:"service_object_id"`
	CurrentVersion        int64     `json:"current_version"`
	CurrentBindingVersion string    `json:"current_binding_version"`
	CreatedBy             string    `json:"created_by"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// OperatorMappingVersion binds one immutable operator binding row to the exact
// service-object identity and contract association versions it was confirmed
// against. These pins are validated by storage tuple constraints as well as the
// planner.
type OperatorMappingVersion struct {
	MappingID          string                `json:"mapping_id"`
	MappingVersion     int64                 `json:"mapping_version"`
	ServiceObjectID    string                `json:"service_object_id"`
	ObjectVersion      int                   `json:"object_version"`
	AssociationVersion int                   `json:"association_version"`
	Binding            OperatorObjectBinding `json:"binding"`
}

// OperatorMappingChangePatch is a typed proposal. It is not a persisted
// mutation; approved changes create a new immutable OperatorObjectBinding.
type OperatorMappingChangePatch struct {
	Kind                string                                `json:"kind"`
	ExternalObject      *OperatorExternalObject               `json:"external_object,omitempty"`
	Identity            *OperatorServiceIdentityPatch         `json:"identity,omitempty"`
	ContractAssociation *ServiceObjectAssociationConfirmInput `json:"contract_association,omitempty"`
	EffectiveFrom       time.Time                             `json:"effective_from"`
	EffectiveUntil      time.Time                             `json:"effective_until"`
}

// OperatorServiceIdentityPatch is a complete desired identity payload for a
// high-impact correction. Omitted payloads cannot mutate object identity.
type OperatorServiceIdentityPatch struct {
	ObjectType   ServiceObjectType    `json:"object_type"`
	DisplayName  string               `json:"display_name"`
	BusinessCode string               `json:"business_code"`
	Aliases      []ServiceObjectAlias `json:"aliases"`
	BasisSummary string               `json:"basis_summary"`
	BasisRefs    json.RawMessage      `json:"basis_refs"`
	State        ServiceObjectState   `json:"state"`
}

type OperatorMappingChangeCAS struct {
	ExpectedMappingVersion     int64  `json:"expected_mapping_version"`
	ExpectedBindingVersion     string `json:"expected_binding_version"`
	ExpectedObjectVersion      int    `json:"expected_object_version"`
	ExpectedAssociationVersion int    `json:"expected_association_version"`
}

// OperatorMappingChangeRequest is immutable. Its state is projected from the
// append-only decision rows and is never written back to the proposal.
type OperatorMappingChangeRequest struct {
	RequestID                  string                     `json:"request_id"`
	CaseID                     string                     `json:"case_id"`
	MappingID                  string                     `json:"mapping_id"`
	ServiceObjectID            string                     `json:"service_object_id"`
	Kind                       string                     `json:"kind"`
	ExpectedMappingVersion     int64                      `json:"expected_mapping_version"`
	ExpectedBindingVersion     string                     `json:"expected_binding_version"`
	ExpectedObjectVersion      int                        `json:"expected_object_version"`
	ExpectedAssociationVersion int                        `json:"expected_association_version"`
	Before                     json.RawMessage            `json:"before"`
	BeforeSHA256               string                     `json:"before_sha256"`
	After                      json.RawMessage            `json:"after"`
	AfterSHA256                string                     `json:"after_sha256"`
	Patch                      OperatorMappingChangePatch `json:"patch"`
	PatchSHA256                string                     `json:"patch_sha256"`
	BasisRefs                  json.RawMessage            `json:"basis_refs"`
	ImpactSHA256               string                     `json:"impact_sha256"`
	State                      string                     `json:"state"`
	ProposedBy                 TrustedPrincipalProjection `json:"proposed_by"`
	CreatedAt                  time.Time                  `json:"created_at"`
}

type OperatorMappingChangeCreateInput struct {
	CAS          OperatorMappingChangeCAS   `json:"cas"`
	Patch        OperatorMappingChangePatch `json:"patch"`
	BasisRefs    json.RawMessage            `json:"basis_refs"`
	ImpactSHA256 string                     `json:"impact_sha256"`
}

type OperatorMappingChangeDecisionInput struct {
	Decision                   string `json:"decision"`
	ExpectedMappingVersion     int64  `json:"expected_mapping_version"`
	ExpectedBindingVersion     string `json:"expected_binding_version"`
	ExpectedObjectVersion      int    `json:"expected_object_version"`
	ExpectedAssociationVersion int    `json:"expected_association_version"`
	ExpectedAfterSHA256        string `json:"expected_after_sha256"`
	ExpectedImpactSHA256       string `json:"expected_impact_sha256"`
	Reason                     string `json:"reason"`
}

type OperatorMappingChangeDecisionResult struct {
	RequestID      string `json:"request_id"`
	DecisionID     string `json:"decision_id"`
	Decision       string `json:"decision"`
	MappingID      string `json:"mapping_id"`
	MappingVersion int64  `json:"mapping_version"`
	BindingVersion string `json:"binding_version"`
}

// OperatorMappingChangeDecision is an append-only terminal decision for one
// request. A caller must also pass current CanDecide/Decide authorization.
type OperatorMappingChangeDecision struct {
	DecisionID string                     `json:"decision_id"`
	RequestID  string                     `json:"request_id"`
	Decision   string                     `json:"decision"`
	Reason     string                     `json:"reason"`
	DecidedBy  TrustedPrincipalProjection `json:"decided_by"`
	CreatedAt  time.Time                  `json:"created_at"`
}

type OperatorMappingChangePlan struct {
	NextHead     OperatorMappingHead    `json:"next_head"`
	NextVersion  OperatorMappingVersion `json:"next_version"`
	BeforeSHA256 string                 `json:"before_sha256"`
	AfterSHA256  string                 `json:"after_sha256"`
	PatchSHA256  string                 `json:"patch_sha256"`
}

type OperatorMappingChangePreview struct {
	MappingID        string                           `json:"mapping_id"`
	ServiceObjectID  string                           `json:"service_object_id"`
	CAS              OperatorMappingChangeCAS         `json:"cas"`
	Before           OperatorMappingVersion           `json:"before"`
	After            OperatorMappingVersion           `json:"after"`
	IdentityAfter    *ServiceObjectVersion            `json:"identity_after,omitempty"`
	AssociationAfter *OperatorMappingAssociationAfter `json:"association_after,omitempty"`
	BeforeSHA256     string                           `json:"before_sha256"`
	AfterSHA256      string                           `json:"after_sha256"`
	ImpactSHA256     string                           `json:"impact_sha256"`
	BasisRefs        json.RawMessage                  `json:"basis_refs"`
}

// OperatorMappingCanonicalState is the exact before/after mapping digest
// payload. Store keeps a private alias for legacy source compatibility.
type OperatorMappingCanonicalState struct {
	MappingID          string                 `json:"mapping_id"`
	MappingVersion     int64                  `json:"mapping_version"`
	ServiceObjectID    string                 `json:"service_object_id"`
	ObjectVersion      int                    `json:"object_version"`
	AssociationVersion int                    `json:"association_version"`
	BindingVersion     string                 `json:"binding_version"`
	Status             string                 `json:"status"`
	SourceSystem       string                 `json:"source_system"`
	ContractObject     OperatorContractObject `json:"contract_object"`
	ExternalObject     OperatorExternalObject `json:"external_object"`
	EffectiveFrom      time.Time              `json:"effective_from"`
	EffectiveUntil     time.Time              `json:"effective_until"`
}

type OperatorMappingApprovedAfter struct {
	Mapping     OperatorMappingCanonicalState    `json:"mapping"`
	Identity    *OperatorMappingIdentityAfter    `json:"identity,omitempty"`
	Association *OperatorMappingAssociationAfter `json:"association,omitempty"`
}

type OperatorMappingIdentityAfter struct {
	ObjectType   ServiceObjectType    `json:"object_type"`
	DisplayName  string               `json:"display_name"`
	BusinessCode string               `json:"business_code"`
	Aliases      []ServiceObjectAlias `json:"aliases"`
	State        ServiceObjectState   `json:"state"`
	EffectiveAt  time.Time            `json:"effective_from"`
	BasisRefs    json.RawMessage      `json:"basis_refs"`
	BasisSummary string               `json:"basis_summary"`
	PayloadSHA   string               `json:"payload_sha256"`
}

// OperatorMappingAssociationAfter freezes the complete proposed source and
// canonical facts without inventing the later approval actor or timestamp.
type OperatorMappingAssociationAfter struct {
	ServiceObjectID    string                 `json:"service_object_id"`
	ObjectVersion      int                    `json:"object_version"`
	AssociationVersion int                    `json:"association_version"`
	MaterialID         string                 `json:"material_id"`
	MaterialVersion    int                    `json:"material_version"`
	DocumentID         string                 `json:"document_id"`
	ParsedDocumentID   string                 `json:"parsed_document_id"`
	SourceRunID        string                 `json:"source_run_id"`
	SourceAttemptID    string                 `json:"source_attempt_id"`
	ObjectRef          string                 `json:"object_ref"`
	FactRefs           []ServiceObjectFactRef `json:"facts"`
	FactsSHA256        string                 `json:"facts_sha256"`
	BasisRefs          json.RawMessage        `json:"basis_refs"`
}

// OperatorMappingChangeRecord keeps the database request digest separate from
// the public immutable request payload.
type OperatorMappingChangeRecord struct {
	Value         OperatorMappingChangeRequest `json:"-"`
	RequestSHA256 string                       `json:"-"`
}

const (
	OperatorMappingReplace                   = "replace_mapping"
	OperatorMappingRetire                    = "retire_mapping"
	OperatorMappingReactivate                = "reactivate_mapping"
	OperatorMappingCorrectIdentity           = "correct_identity"
	OperatorMappingCorrectIdentityAndMapping = "correct_identity_and_mapping"
	OperatorMappingRetireServiceObject       = "retire_service_object"
)

func OperatorObjectBindingRequestSHA256(caseID string, input OperatorObjectBindingInput, from, until time.Time) (legacy, current string, err error) {
	legacy, err = operatorMappingHash(struct {
		CaseID         string
		ContractObject OperatorContractObject
		ExternalObject OperatorExternalObject
		From, Until    time.Time
		Confirmed      bool
	}{caseID, input.ContractObject, input.ExternalObject, from, until, true})
	if err != nil {
		return "", "", err
	}
	current = legacy
	if input.ServiceObjectID != "" || input.ExpectedObjectVersion != 0 || input.ExpectedAssociationVersion != 0 {
		current, err = operatorMappingHash(struct {
			CaseID                     string
			ContractObject             OperatorContractObject
			ExternalObject             OperatorExternalObject
			From, Until                time.Time
			Confirmed                  bool
			ServiceObjectID            string
			ExpectedObjectVersion      int
			ExpectedAssociationVersion int
		}{caseID, input.ContractObject, input.ExternalObject, from, until, true,
			input.ServiceObjectID, input.ExpectedObjectVersion, input.ExpectedAssociationVersion})
	}
	return legacy, current, err
}

func OperatorMappingChangeCreateRequestSHA256(caseID, mappingID string, input OperatorMappingChangeCreateInput) (string, error) {
	return operatorMappingHash(struct {
		CaseID, MappingID string
		Input             OperatorMappingChangeCreateInput
	}{caseID, mappingID, input})
}

func OperatorMappingChangeDecisionRequestSHA256(caseID, requestID string, input OperatorMappingChangeDecisionInput) (string, error) {
	return operatorMappingHash(struct {
		CaseID, RequestID string
		Input             OperatorMappingChangeDecisionInput
	}{caseID, requestID, input})
}

func PlanOperatorMappingChange(head OperatorMappingHead, current OperatorMappingVersion, cas OperatorMappingChangeCAS,
	patch OperatorMappingChangePatch, newBindingVersion string, actor string, now time.Time) (OperatorMappingChangePlan, error) {
	var empty OperatorMappingChangePlan
	if head.MappingID == "" || head.ServiceObjectID == "" || head.CaseID == "" || head.CurrentVersion < 1 || head.CurrentBindingVersion == "" ||
		current.MappingID != head.MappingID || current.MappingVersion != head.CurrentVersion || current.ServiceObjectID != head.ServiceObjectID ||
		current.Binding.Version != head.CurrentBindingVersion || current.ObjectVersion < 1 || current.AssociationVersion < 1 ||
		cas.ExpectedMappingVersion != head.CurrentVersion || cas.ExpectedBindingVersion != head.CurrentBindingVersion ||
		cas.ExpectedObjectVersion != current.ObjectVersion || cas.ExpectedAssociationVersion != current.AssociationVersion {
		return empty, errors.New("operator mapping compare-and-swap is stale")
	}
	now = now.UTC().Truncate(time.Microsecond)
	if actor == "" || newBindingVersion == "" || newBindingVersion == current.Binding.Version || now.IsZero() {
		return empty, errors.New("operator mapping change actor, version or time is invalid")
	}
	patch.EffectiveFrom = patch.EffectiveFrom.UTC().Truncate(time.Microsecond)
	patch.EffectiveUntil = patch.EffectiveUntil.UTC().Truncate(time.Microsecond)
	if !ValidMappingInstant(patch.EffectiveFrom) || !ValidMappingInstant(patch.EffectiveUntil) || !patch.EffectiveFrom.Before(patch.EffectiveUntil) {
		return empty, errors.New("operator mapping effective interval is invalid")
	}
	if patch.ContractAssociation != nil {
		target := patch.ContractAssociation
		if patch.Kind != OperatorMappingReplace || patch.Identity != nil || !target.Confirmed ||
			target.ExpectedObjectVersion != cas.ExpectedObjectVersion || target.ExpectedAssociationVersion != cas.ExpectedAssociationVersion ||
			strings.TrimSpace(target.ObjectRef) != target.ObjectRef || target.ObjectRef == "" ||
			!ValidServiceObjectSHA256(target.FactsSHA256) || !ValidServiceObjectSHA256(target.CandidatesSHA256) {
			return empty, errors.New("contract association proposal is invalid")
		}
	}
	nextBinding := current.Binding
	nextBinding.CaseID = head.CaseID
	nextBinding.BindingID = newBindingVersion
	nextBinding.Version = newBindingVersion
	nextBinding.CreatedAt = now.UTC().Truncate(time.Microsecond)
	nextBinding.ConfirmedAt = nextBinding.CreatedAt
	nextBinding.ConfirmedBy = actor
	nextBinding.EffectiveFrom = patch.EffectiveFrom
	nextBinding.EffectiveUntil = patch.EffectiveUntil
	switch patch.Kind {
	case OperatorMappingReplace:
		if current.Binding.Status != "confirmed" || patch.ExternalObject == nil {
			return empty, errors.New("active mapping replacement requires a confirmed mapping and external object")
		}
		if err := ValidateOperatorExternalObject(*patch.ExternalObject); err != nil {
			return empty, err
		}
		nextBinding.ExternalObject = *patch.ExternalObject
		nextBinding.Status = "confirmed"
		if patch.ContractAssociation != nil {
			nextBinding.ContractObject.ObjectID = patch.ContractAssociation.ObjectRef
		}
	case OperatorMappingRetire:
		if current.Binding.Status != "confirmed" || patch.ExternalObject != nil {
			return empty, errors.New("retirement requires an active mapping and cannot change its external identity")
		}
		nextBinding.Status = "inactive"
	case OperatorMappingReactivate:
		if current.Binding.Status != "inactive" {
			return empty, errors.New("only an inactive mapping can be reactivated")
		}
		if patch.ExternalObject != nil {
			if err := ValidateOperatorExternalObject(*patch.ExternalObject); err != nil {
				return empty, err
			}
			nextBinding.ExternalObject = *patch.ExternalObject
		}
		nextBinding.Status = "confirmed"
	case OperatorMappingCorrectIdentity, OperatorMappingCorrectIdentityAndMapping:
		if patch.Identity == nil {
			return empty, errors.New("identity correction requires a complete identity payload")
		}
		if patch.Kind == OperatorMappingCorrectIdentityAndMapping {
			if patch.ExternalObject == nil || ValidateOperatorExternalObject(*patch.ExternalObject) != nil {
				return empty, errors.New("combined identity and mapping correction requires a valid external identity")
			}
			nextBinding.ExternalObject = *patch.ExternalObject
		} else if patch.ExternalObject != nil {
			return empty, errors.New("identity-only correction cannot change the external mapping")
		}
		nextBinding.Status = "confirmed"
	case OperatorMappingRetireServiceObject:
		if patch.Identity == nil || patch.Identity.State != ServiceObjectInactive || patch.ExternalObject != nil {
			return empty, errors.New("service object retirement requires an inactive identity payload")
		}
		nextBinding.Status = "inactive"
	default:
		return empty, errors.New("operator mapping change kind is invalid")
	}
	before := CanonicalOperatorMappingState(current)
	afterVersion := OperatorMappingVersion{
		MappingID: head.MappingID, MappingVersion: current.MappingVersion + 1, ServiceObjectID: head.ServiceObjectID,
		ObjectVersion: current.ObjectVersion, AssociationVersion: current.AssociationVersion, Binding: nextBinding,
	}
	if patch.ContractAssociation != nil {
		afterVersion.AssociationVersion++
		afterVersion.Binding.AssociationVersion = afterVersion.AssociationVersion
	}
	after := CanonicalOperatorMappingState(afterVersion)
	var identityAfter *OperatorMappingIdentityAfter
	if patch.Identity != nil {
		afterVersion.ObjectVersion = current.ObjectVersion + 1
		afterVersion.AssociationVersion = current.AssociationVersion + 1
		after = CanonicalOperatorMappingState(afterVersion)
		proposed, err := ProposedOperatorIdentityVersion(head.ServiceObjectID, current.ObjectVersion, *patch.Identity, patch.EffectiveFrom)
		if err != nil {
			return empty, err
		}
		identitySHA, err := CanonicalServiceObjectIdentitySHA256(proposed)
		if err != nil {
			return empty, err
		}
		identityAfter = &OperatorMappingIdentityAfter{ObjectType: proposed.ObjectType, DisplayName: proposed.DisplayName,
			BusinessCode: proposed.BusinessCode, Aliases: proposed.Aliases, State: proposed.State, EffectiveAt: proposed.EffectiveFrom,
			BasisRefs: proposed.BasisRefs, BasisSummary: proposed.BasisSummary, PayloadSHA: identitySHA}
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return empty, err
	}
	beforeHash, err := OperatorMappingDigest(before)
	if err != nil {
		return empty, err
	}
	afterHash, err := OperatorMappingApprovedDigest(after, identityAfter, nil)
	if err != nil {
		return empty, err
	}
	patchHash := sha256.Sum256(patchBytes)
	return OperatorMappingChangePlan{
		NextHead: OperatorMappingHead{CaseID: head.CaseID, MappingID: head.MappingID, ServiceObjectID: head.ServiceObjectID,
			CurrentVersion: head.CurrentVersion + 1, CurrentBindingVersion: newBindingVersion, CreatedBy: head.CreatedBy,
			CreatedAt: head.CreatedAt, UpdatedAt: now.UTC().Truncate(time.Microsecond)},
		NextVersion: afterVersion, BeforeSHA256: beforeHash, AfterSHA256: afterHash, PatchSHA256: hex.EncodeToString(patchHash[:]),
	}, nil
}

func OperatorMappingApprovedDigest(mapping OperatorMappingCanonicalState, identity *OperatorMappingIdentityAfter, association *OperatorMappingAssociationAfter) (string, error) {
	if identity == nil && association == nil {
		return OperatorMappingDigest(mapping)
	}
	// JSON storage may reorder raw evidence object keys without changing their value.
	if identity != nil && len(identity.BasisRefs) > 0 {
		basis, err := canonicalOperatorMappingRawJSON(identity.BasisRefs)
		if err != nil {
			return "", err
		}
		canonical := *identity
		canonical.BasisRefs = basis
		identity = &canonical
	}
	if association != nil && len(association.BasisRefs) > 0 {
		basis, err := canonicalOperatorMappingRawJSON(association.BasisRefs)
		if err != nil {
			return "", err
		}
		canonical := *association
		canonical.BasisRefs = basis
		association = &canonical
	}
	encoded, err := json.Marshal(OperatorMappingApprovedAfter{Mapping: mapping, Identity: identity, Association: association})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func CanonicalOperatorMappingState(version OperatorMappingVersion) OperatorMappingCanonicalState {
	binding := version.Binding
	return OperatorMappingCanonicalState{MappingID: version.MappingID, MappingVersion: version.MappingVersion,
		ServiceObjectID: version.ServiceObjectID, ObjectVersion: version.ObjectVersion,
		AssociationVersion: version.AssociationVersion, BindingVersion: binding.Version, Status: binding.Status,
		SourceSystem: binding.SourceSystem, ContractObject: binding.ContractObject, ExternalObject: binding.ExternalObject,
		EffectiveFrom: binding.EffectiveFrom.UTC().Truncate(time.Microsecond), EffectiveUntil: binding.EffectiveUntil.UTC().Truncate(time.Microsecond)}
}

func OperatorMappingDigest(value OperatorMappingCanonicalState) (string, error) {
	return operatorMappingHash(value)
}

func OperatorMappingCASMatches(head OperatorMappingHead, current OperatorMappingVersion, cas OperatorMappingChangeCAS) bool {
	return head.CurrentVersion == cas.ExpectedMappingVersion && head.CurrentBindingVersion == cas.ExpectedBindingVersion &&
		current.ObjectVersion == cas.ExpectedObjectVersion && current.AssociationVersion == cas.ExpectedAssociationVersion
}

func OperatorMappingImpactSHA256(head OperatorMappingHead, current OperatorMappingVersion, identity ServiceObjectVersion, association ServiceObjectContractAssociation) (string, error) {
	return operatorMappingHash(struct {
		CaseID      string
		Head        OperatorMappingHead
		Current     OperatorMappingCanonicalState
		IdentitySHA string
		AssocSHA    string
	}{head.CaseID, head, CanonicalOperatorMappingState(current), identity.PayloadSHA256, association.PayloadSHA256})
}

func OperatorMappingImpactWithAssociationSHA256(head OperatorMappingHead, current OperatorMappingVersion, identity ServiceObjectVersion,
	association ServiceObjectContractAssociation, target *OperatorMappingAssociationAfter) (string, error) {
	base, err := OperatorMappingImpactSHA256(head, current, identity, association)
	if err != nil || target == nil {
		return base, err
	}
	return operatorMappingHash(struct {
		CurrentImpact string                           `json:"current_impact"`
		Target        *OperatorMappingAssociationAfter `json:"target_association"`
	}{base, target})
}

func BindOperatorMappingAssociation(plan *OperatorMappingChangePlan, association *OperatorMappingAssociationAfter) error {
	if association == nil {
		return nil
	}
	digest, err := OperatorMappingApprovedDigest(CanonicalOperatorMappingState(plan.NextVersion), nil, association)
	if err == nil {
		plan.AfterSHA256 = digest
	}
	return err
}

func OperatorMappingAssociationAfterProjection(association ServiceObjectContractAssociation) *OperatorMappingAssociationAfter {
	return &OperatorMappingAssociationAfter{ServiceObjectID: association.ServiceObjectID, ObjectVersion: association.ObjectVersion,
		AssociationVersion: association.AssociationVersion, MaterialID: association.MaterialID, MaterialVersion: association.MaterialVersion,
		DocumentID: association.DocumentID, ParsedDocumentID: association.ParsedDocumentID, SourceRunID: association.SourceRunID,
		SourceAttemptID: association.SourceAttemptID, ObjectRef: association.ObjectRef, FactRefs: association.FactRefs,
		FactsSHA256: association.FactsSHA256, BasisRefs: append(json.RawMessage(nil), association.BasisRefs...)}
}

func NormalizeOperatorMappingPatch(patch OperatorMappingChangePatch, current OperatorObjectBinding) (OperatorMappingChangePatch, error) {
	patch.Kind = strings.TrimSpace(patch.Kind)
	if patch.EffectiveFrom.IsZero() {
		patch.EffectiveFrom = current.EffectiveFrom
	}
	if patch.EffectiveUntil.IsZero() {
		patch.EffectiveUntil = current.EffectiveUntil
	}
	patch.EffectiveFrom = patch.EffectiveFrom.UTC().Truncate(time.Microsecond)
	patch.EffectiveUntil = patch.EffectiveUntil.UTC().Truncate(time.Microsecond)
	switch patch.Kind {
	case OperatorMappingReplace:
		if patch.ExternalObject == nil || ValidateOperatorExternalObject(*patch.ExternalObject) != nil {
			return patch, errors.New("mapping replacement needs a valid external identity")
		}
	case OperatorMappingRetire, OperatorMappingReactivate:
		if patch.ExternalObject != nil && ValidateOperatorExternalObject(*patch.ExternalObject) != nil {
			return patch, errors.New("mapping external identity is invalid")
		}
	case OperatorMappingCorrectIdentity:
		if patch.Identity == nil || patch.ExternalObject != nil {
			return patch, errors.New("identity correction requires an identity payload only")
		}
		identity, err := NormalizeOperatorIdentityPatch(*patch.Identity, patch.EffectiveFrom)
		if err != nil {
			return patch, err
		}
		if identity.State != ServiceObjectConfirmed {
			return patch, errors.New("identity correction must retain a confirmed object")
		}
		patch.Identity = OperatorIdentityPatchFromVersion(identity)
	case OperatorMappingCorrectIdentityAndMapping:
		if patch.Identity == nil || patch.ExternalObject == nil || ValidateOperatorExternalObject(*patch.ExternalObject) != nil {
			return patch, errors.New("combined correction requires identity and external mapping payloads")
		}
		identity, err := NormalizeOperatorIdentityPatch(*patch.Identity, patch.EffectiveFrom)
		if err != nil {
			return patch, err
		}
		if identity.State != ServiceObjectConfirmed {
			return patch, errors.New("identity correction must retain a confirmed object")
		}
		patch.Identity = OperatorIdentityPatchFromVersion(identity)
	case OperatorMappingRetireServiceObject:
		if patch.Identity == nil || patch.ExternalObject != nil || patch.Identity.State != ServiceObjectInactive {
			return patch, errors.New("service object retirement requires an inactive identity payload")
		}
		if _, err := NormalizeOperatorIdentityPatch(*patch.Identity, patch.EffectiveFrom); err != nil {
			return patch, err
		}
		identity, _ := NormalizeOperatorIdentityPatch(*patch.Identity, patch.EffectiveFrom)
		patch.Identity = OperatorIdentityPatchFromVersion(identity)
	default:
		return patch, errors.New("mapping correction kind is invalid")
	}
	if !ValidMappingInstant(patch.EffectiveFrom) || !ValidMappingInstant(patch.EffectiveUntil) || !patch.EffectiveFrom.Before(patch.EffectiveUntil) {
		return patch, errors.New("mapping effective interval is invalid")
	}
	return patch, nil
}

func ProposedOperatorIdentityVersion(serviceObjectID string, currentVersion int, patch OperatorServiceIdentityPatch, effectiveFrom time.Time) (ServiceObjectVersion, error) {
	version, err := NormalizeOperatorIdentityPatch(patch, effectiveFrom)
	if err != nil || strings.TrimSpace(serviceObjectID) == "" || currentVersion < 1 {
		return ServiceObjectVersion{}, errors.New("identity correction payload is invalid")
	}
	version.ServiceObjectID = serviceObjectID
	version.Version = currentVersion + 1
	version.EffectiveFrom = effectiveFrom.UTC().Truncate(time.Microsecond)
	version.PayloadSHA256, err = CanonicalServiceObjectIdentitySHA256(version)
	if err != nil {
		return ServiceObjectVersion{}, err
	}
	return version, nil
}

func OperatorIdentityAfterProjection(version ServiceObjectVersion) *OperatorMappingIdentityAfter {
	return &OperatorMappingIdentityAfter{ObjectType: version.ObjectType, DisplayName: version.DisplayName,
		BusinessCode: version.BusinessCode, Aliases: version.Aliases, State: version.State, EffectiveAt: version.EffectiveFrom.UTC().Truncate(time.Microsecond),
		BasisRefs: append(json.RawMessage(nil), version.BasisRefs...), BasisSummary: version.BasisSummary, PayloadSHA: version.PayloadSHA256}
}

func OperatorIdentityPatchFromVersion(version ServiceObjectVersion) *OperatorServiceIdentityPatch {
	return &OperatorServiceIdentityPatch{ObjectType: version.ObjectType, DisplayName: version.DisplayName,
		BusinessCode: version.BusinessCode, Aliases: append([]ServiceObjectAlias(nil), version.Aliases...),
		BasisSummary: version.BasisSummary, BasisRefs: append(json.RawMessage(nil), version.BasisRefs...), State: version.State}
}

func NormalizeOperatorIdentityPatch(patch OperatorServiceIdentityPatch, effectiveFrom time.Time) (ServiceObjectVersion, error) {
	if len(patch.BasisRefs) == 0 {
		return ServiceObjectVersion{}, errors.New("identity correction requires pinned basis references")
	}
	canonicalBasis, err := canonicalOperatorMappingRawJSON(patch.BasisRefs)
	if err != nil || string(canonicalBasis) == "null" {
		return ServiceObjectVersion{}, errors.New("identity correction basis is invalid")
	}
	version, err := NormalizeServiceObjectVersion(ServiceObjectVersion{
		ObjectType: patch.ObjectType, DisplayName: patch.DisplayName, BusinessCode: patch.BusinessCode,
		Aliases: patch.Aliases, State: patch.State, BasisRefs: canonicalBasis, BasisSummary: patch.BasisSummary,
		EffectiveFrom: effectiveFrom.UTC().Truncate(time.Microsecond),
	})
	if err != nil || (version.State != ServiceObjectConfirmed && version.State != ServiceObjectInactive) {
		return ServiceObjectVersion{}, errors.New("corrected identity payload is invalid")
	}
	return version, nil
}

func ValidateOperatorExternalObject(value OperatorExternalObject) error {
	if auditext.ValidateID(strings.TrimSpace(value.ConnectorID)) != nil || auditext.ValidateID(strings.TrimSpace(value.ExternalObjectID)) != nil {
		return errors.New("operator mapping external identity is invalid")
	}
	if value.ConnectorID != strings.TrimSpace(value.ConnectorID) || value.ExternalObjectID != strings.TrimSpace(value.ExternalObjectID) {
		return errors.New("operator mapping external identity is not canonical")
	}
	return nil
}

func ValidMappingInstant(value time.Time) bool {
	return !value.IsZero() && value.Equal(value.UTC()) && value.Nanosecond()%1000 == 0
}

func OperatorMappingDecisionIndependent(request OperatorMappingChangeRequest, decider TrustedPrincipalProjection) bool {
	return !subjectModel.SamePrincipal(request.ProposedBy, decider)
}

func ValidOperatorDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

// CanonicalOperatorMappingRawJSON retains the historical strict JSON normalization.
func CanonicalOperatorMappingRawJSON(raw json.RawMessage) (json.RawMessage, error) {
	return canonicalOperatorMappingRawJSON(raw)
}

func canonicalOperatorMappingRawJSON(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	encoded, err := json.Marshal(value)
	return encoded, err
}

func operatorMappingHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
