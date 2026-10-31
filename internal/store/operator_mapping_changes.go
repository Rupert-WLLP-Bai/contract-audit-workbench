package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/auditext"
)

const (
	OperatorMappingReplace                   = "replace_mapping"
	OperatorMappingRetire                    = "retire_mapping"
	OperatorMappingReactivate                = "reactivate_mapping"
	OperatorMappingCorrectIdentity           = "correct_identity"
	OperatorMappingCorrectIdentityAndMapping = "correct_identity_and_mapping"
	OperatorMappingRetireServiceObject       = "retire_service_object"
)

// OperatorMappingHead is only the stable lineage identity and current pointers.
// The binding payload remains in immutable operator_object_bindings rows.
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
	Kind           string                        `json:"kind"`
	ExternalObject *OperatorExternalObject       `json:"external_object,omitempty"`
	Identity       *OperatorServiceIdentityPatch `json:"identity,omitempty"`
	ContractAssociation *ServiceObjectAssociationConfirmInput `json:"contract_association,omitempty"`
	EffectiveFrom  time.Time                     `json:"effective_from"`
	EffectiveUntil time.Time                     `json:"effective_until"`
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
	requestSHA256              string
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

type operatorMappingCanonicalState struct {
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

type operatorMappingApprovedAfter struct {
	Mapping  operatorMappingCanonicalState `json:"mapping"`
	Identity *operatorMappingIdentityAfter `json:"identity,omitempty"`
	Association *operatorMappingAssociationAfter `json:"association,omitempty"`
}

type operatorMappingIdentityAfter struct {
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

// operatorMappingAssociationAfter freezes the complete proposed source and
// canonical facts without inventing the later approval actor or timestamp.
type operatorMappingAssociationAfter struct {
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

// PlanOperatorMappingChange applies only a typed, CAS-bound mapping change.
// The caller must first load and authorize the mapping, validate the SOD request,
// basis and complete impact hash, and provide a server-generated binding ID.
// Approval and persistence happen in the service transaction, not here.
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
	if !validMappingInstant(patch.EffectiveFrom) || !validMappingInstant(patch.EffectiveUntil) || !patch.EffectiveFrom.Before(patch.EffectiveUntil) {
		return empty, errors.New("operator mapping effective interval is invalid")
	}
	if patch.ContractAssociation != nil {
		target := patch.ContractAssociation
		if patch.Kind != OperatorMappingReplace || patch.Identity != nil || !target.Confirmed ||
			target.ExpectedObjectVersion != cas.ExpectedObjectVersion || target.ExpectedAssociationVersion != cas.ExpectedAssociationVersion ||
			strings.TrimSpace(target.ObjectRef) != target.ObjectRef || target.ObjectRef == "" ||
			!validServiceObjectSHA256(target.FactsSHA256) || !validServiceObjectSHA256(target.CandidatesSHA256) {
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
		if err := validateOperatorExternalObject(*patch.ExternalObject); err != nil {
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
			if err := validateOperatorExternalObject(*patch.ExternalObject); err != nil {
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
			if patch.ExternalObject == nil || validateOperatorExternalObject(*patch.ExternalObject) != nil {
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
	before := canonicalOperatorMappingState(current)
	afterVersion := OperatorMappingVersion{
		MappingID: head.MappingID, MappingVersion: current.MappingVersion + 1, ServiceObjectID: head.ServiceObjectID,
		ObjectVersion: current.ObjectVersion, AssociationVersion: current.AssociationVersion, Binding: nextBinding,
	}
	if patch.ContractAssociation != nil {
		afterVersion.AssociationVersion++
		afterVersion.Binding.AssociationVersion = afterVersion.AssociationVersion
	}
	after := canonicalOperatorMappingState(afterVersion)
	var identityAfter *operatorMappingIdentityAfter
	if patch.Identity != nil {
		afterVersion.ObjectVersion = current.ObjectVersion + 1
		afterVersion.AssociationVersion = current.AssociationVersion + 1
		after = canonicalOperatorMappingState(afterVersion)
		proposed, err := proposedOperatorIdentityVersion(head.ServiceObjectID, current.ObjectVersion, *patch.Identity, patch.EffectiveFrom)
		if err != nil {
			return empty, err
		}
		identitySHA, err := canonicalServiceObjectIdentitySHA256(proposed)
		if err != nil {
			return empty, err
		}
		identityAfter = &operatorMappingIdentityAfter{ObjectType: proposed.ObjectType, DisplayName: proposed.DisplayName,
			BusinessCode: proposed.BusinessCode, Aliases: proposed.Aliases, State: proposed.State, EffectiveAt: proposed.EffectiveFrom,
			BasisRefs: proposed.BasisRefs, BasisSummary: proposed.BasisSummary, PayloadSHA: identitySHA}
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return empty, err
	}
	beforeHash, err := operatorMappingDigest(before)
	if err != nil {
		return empty, err
	}
	afterHash, err := operatorMappingApprovedDigest(after, identityAfter, nil)
	if err != nil {
		return empty, err
	}
	patchHash := sha256.Sum256(patchBytes)
	return OperatorMappingChangePlan{
		NextHead: OperatorMappingHead{CaseID: head.CaseID, MappingID: head.MappingID, ServiceObjectID: head.ServiceObjectID,
			CurrentVersion: head.CurrentVersion + 1, CurrentBindingVersion: newBindingVersion, CreatedBy: head.CreatedBy,
			CreatedAt: head.CreatedAt, UpdatedAt: now.UTC().Truncate(time.Microsecond)},
		NextVersion:  afterVersion,
		BeforeSHA256: beforeHash, AfterSHA256: afterHash, PatchSHA256: hex.EncodeToString(patchHash[:]),
	}, nil
}

func operatorMappingApprovedDigest(mapping operatorMappingCanonicalState, identity *operatorMappingIdentityAfter, association *operatorMappingAssociationAfter) (string, error) {
	if identity == nil && association == nil {
		return operatorMappingDigest(mapping)
	}
	encoded, err := json.Marshal(operatorMappingApprovedAfter{Mapping: mapping, Identity: identity, Association: association})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func canonicalOperatorMappingState(version OperatorMappingVersion) operatorMappingCanonicalState {
	binding := version.Binding
	return operatorMappingCanonicalState{MappingID: version.MappingID, MappingVersion: version.MappingVersion,
		ServiceObjectID: version.ServiceObjectID, ObjectVersion: version.ObjectVersion,
		AssociationVersion: version.AssociationVersion, BindingVersion: binding.Version, Status: binding.Status,
		SourceSystem: binding.SourceSystem, ContractObject: binding.ContractObject, ExternalObject: binding.ExternalObject,
		EffectiveFrom: binding.EffectiveFrom.UTC().Truncate(time.Microsecond), EffectiveUntil: binding.EffectiveUntil.UTC().Truncate(time.Microsecond)}
}

func operatorMappingDigest(value operatorMappingCanonicalState) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validateOperatorExternalObject(value OperatorExternalObject) error {
	if auditext.ValidateID(strings.TrimSpace(value.ConnectorID)) != nil || auditext.ValidateID(strings.TrimSpace(value.ExternalObjectID)) != nil {
		return errors.New("operator mapping external identity is invalid")
	}
	if value.ConnectorID != strings.TrimSpace(value.ConnectorID) || value.ExternalObjectID != strings.TrimSpace(value.ExternalObjectID) {
		return errors.New("operator mapping external identity is not canonical")
	}
	return nil
}

func validMappingInstant(value time.Time) bool {
	return !value.IsZero() && value.Equal(value.UTC()) && value.Nanosecond()%1000 == 0
}

// operatorMappingDecisionIndependent reuses the P2 principal comparison:
// account aliases and a changed account with the same issuer+subject remain the
// same principal. Role/impact authorization is checked by the service method.
func operatorMappingDecisionIndependent(request OperatorMappingChangeRequest, decider TrustedPrincipalProjection) bool {
	return !subjectPrincipalSame(request.ProposedBy, decider)
}
