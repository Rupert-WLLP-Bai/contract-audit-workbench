// File metadata (manifest mirror):
// Purpose: Owns service-object identity, immutable association and management values with exact citation, canonical digest and explicit-confirmation rules.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package model owns service object values and pure association rules.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"contractaudit/internal/rulepack"
)

type ServiceObjectType string

const (
	ServiceObjectDedicatedLine ServiceObjectType = "dedicated_line"
	ServiceObjectService       ServiceObjectType = "service"
)

type ServiceObjectState string

const (
	ServiceObjectPendingConfirmation ServiceObjectState = "pending_confirmation"
	ServiceObjectConfirmed           ServiceObjectState = "confirmed"
	ServiceObjectInactive            ServiceObjectState = "inactive"
)

// ServiceObjectHead contains only identity and version pointers. The selected
// immutable version is the single authority for object type and state.
type ServiceObjectHead struct {
	ID                        string `json:"service_object_id"`
	CurrentVersion            int    `json:"current_version"`
	CurrentAssociationVersion int    `json:"current_association_version"`
}

type ServiceObjectVersion struct {
	ServiceObjectID  string               `json:"service_object_id"`
	Version          int                  `json:"version"`
	ObjectType       ServiceObjectType    `json:"object_type"`
	DisplayName      string               `json:"display_name"`
	BusinessCode     string               `json:"business_code"`
	Aliases          []ServiceObjectAlias `json:"aliases"`
	State            ServiceObjectState   `json:"state"`
	BasisRefs        json.RawMessage      `json:"basis_refs"`
	BasisSummary     string               `json:"basis_summary"`
	ChangeDecisionID string               `json:"change_decision_id,omitempty"`
	CreatedBy        string               `json:"created_by"`
	CreatedAt        time.Time            `json:"created_at"`
	EffectiveFrom    time.Time            `json:"effective_from"`
	PayloadSHA256    string               `json:"payload_sha256"`
}

type ServiceObjectAlias struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// ServiceObjectFactRef is resolved by the server from one frozen parse. The
// values are immutable evidence inputs, not caller-supplied thresholds.
type ServiceObjectFactRef struct {
	Fact                rulepack.Fact `json:"fact"`
	DocumentID          string        `json:"document_id"`
	ParsedDocumentID    string        `json:"parsed_document_id"`
	CanonicalFactSHA256 string        `json:"canonical_fact_sha256"`
}

type ServiceObjectContractAssociation struct {
	ServiceObjectID          string                 `json:"service_object_id"`
	AssociationVersion       int                    `json:"association_version"`
	ObjectVersion            int                    `json:"object_version"`
	MaterialID               string                 `json:"material_id"`
	MaterialVersion          int                    `json:"material_version"`
	DocumentID               string                 `json:"document_id"`
	ParsedDocumentID         string                 `json:"parsed_document_id"`
	SourceRunID              string                 `json:"source_run_id"`
	SourceAttemptID          string                 `json:"source_attempt_id"`
	ObjectRef                string                 `json:"object_ref"`
	FactRefs                 []ServiceObjectFactRef `json:"facts"`
	BasisRefs                json.RawMessage        `json:"basis_refs"`
	ConfirmedBy              string                 `json:"confirmed_by"`
	ConfirmedAt              time.Time              `json:"confirmed_at"`
	IdentityChangeDecisionID string                 `json:"identity_change_decision_id,omitempty"`
	FactsCanonicalJSON       []byte                 `json:"-"`
	FactsSHA256              string                 `json:"facts_sha256"`
	CreatedAt                time.Time              `json:"created_at"`
	PayloadSHA256            string                 `json:"payload_sha256"`
}

type ServiceObjectAssociationCandidate struct {
	ExpectedObjectVersion      int                              `json:"expected_object_version"`
	ExpectedAssociationVersion int                              `json:"expected_association_version"`
	Confirmed                  bool                             `json:"confirmed"`
	Association                ServiceObjectContractAssociation `json:"association"`
}

type ServiceObjectAssociationPlan struct {
	Head          ServiceObjectHead                `json:"head"`
	ObjectVersion ServiceObjectVersion             `json:"object_version"`
	Association   ServiceObjectContractAssociation `json:"association"`
}

type ServiceObjectAssociationPreviewInput struct {
	RunID            string `json:"run_id"`
	AttemptID        string `json:"attempt_id"`
	MaterialID       string `json:"material_id"`
	MaterialVersion  int    `json:"material_version"`
	DocumentID       string `json:"document_id"`
	ParsedDocumentID string `json:"parsed_document_id"`
}

type ServiceObjectAssociationConfirmInput struct {
	Confirmed                  bool                                 `json:"confirmed"`
	ExpectedObjectVersion      int                                  `json:"expected_object_version"`
	ExpectedAssociationVersion int                                  `json:"expected_association_version"`
	Source                     ServiceObjectAssociationPreviewInput `json:"source"`
	ObjectRef                  string                               `json:"object_ref"`
	FactsSHA256                string                               `json:"facts_sha256"`
	CandidatesSHA256           string                               `json:"candidate_sha256"`
}

type ServiceObjectCandidateDigestPin struct {
	ObjectRef   string `json:"object_ref"`
	FactsSHA256 string `json:"facts_sha256"`
}

type ServiceObjectCandidateDigestInput struct {
	ServiceObjectID            string
	ExpectedObjectVersion      int
	ExpectedAssociationVersion int
	RunID                      string
	AttemptID                  string
	MaterialID                 string
	MaterialVersion            int
	DocumentID                 string
	ParsedDocumentID           string
	Candidates                 []ServiceObjectCandidateDigestPin
}

func ServiceObjectCandidatesSHA256(input ServiceObjectCandidateDigestInput) (string, error) {
	payload := struct {
		ServiceObjectID            string                            `json:"service_object_id"`
		ExpectedObjectVersion      int                               `json:"expected_object_version"`
		ExpectedAssociationVersion int                               `json:"expected_association_version"`
		Confirmed                  bool                              `json:"confirmed"`
		RunID                      string                            `json:"run_id"`
		AttemptID                  string                            `json:"attempt_id"`
		MaterialID                 string                            `json:"material_id"`
		MaterialVersion            int                               `json:"material_version"`
		DocumentID                 string                            `json:"document_id"`
		ParsedDocumentID           string                            `json:"parsed_document_id"`
		Candidates                 []ServiceObjectCandidateDigestPin `json:"candidates"`
	}{input.ServiceObjectID, input.ExpectedObjectVersion, input.ExpectedAssociationVersion, true,
		input.RunID, input.AttemptID, input.MaterialID, input.MaterialVersion, input.DocumentID, input.ParsedDocumentID, input.Candidates}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

var ErrInvalidServiceObjectAssociation = errors.New("invalid service object association")

// PlanServiceObjectAssociation validates the CAS and produces the immutable
// records for an explicit association confirmation. It has no side effects.
func PlanServiceObjectAssociation(head ServiceObjectHead, current ServiceObjectVersion, candidate ServiceObjectAssociationCandidate, confirmerID string, now time.Time) (ServiceObjectAssociationPlan, error) {
	if head.ID == "" || head.ID != current.ServiceObjectID || head.CurrentVersion < 1 ||
		head.CurrentVersion != current.Version ||
		candidate.ExpectedObjectVersion != head.CurrentVersion || candidate.ExpectedAssociationVersion != head.CurrentAssociationVersion ||
		now.IsZero() || strings.TrimSpace(confirmerID) == "" || current.State == ServiceObjectInactive || !candidate.Confirmed {
		return ServiceObjectAssociationPlan{}, ErrInvalidServiceObjectAssociation
	}
	if (head.CurrentAssociationVersion == 0 && head.CurrentVersion != 1) || head.CurrentAssociationVersion < 0 {
		return ServiceObjectAssociationPlan{}, ErrInvalidServiceObjectAssociation
	}
	if current.ObjectType != ServiceObjectDedicatedLine && current.ObjectType != ServiceObjectService {
		return ServiceObjectAssociationPlan{}, ErrInvalidServiceObjectAssociation
	}
	if current.State != ServiceObjectPendingConfirmation && current.State != ServiceObjectConfirmed {
		return ServiceObjectAssociationPlan{}, ErrInvalidServiceObjectAssociation
	}
	association := candidate.Association
	association.FactRefs = CloneServiceObjectFactRefs(association.FactRefs)
	if strings.TrimSpace(association.MaterialID) == "" || association.MaterialVersion < 1 ||
		strings.TrimSpace(association.DocumentID) == "" || strings.TrimSpace(association.ParsedDocumentID) == "" ||
		strings.TrimSpace(association.SourceRunID) == "" || strings.TrimSpace(association.SourceAttemptID) == "" ||
		strings.TrimSpace(association.ObjectRef) == "" || len(association.FactRefs) == 0 || len(association.FactRefs) > 100 {
		return ServiceObjectAssociationPlan{}, ErrInvalidServiceObjectAssociation
	}
	if len(association.BasisRefs) == 0 {
		association.BasisRefs = json.RawMessage(`[]`)
	} else {
		var refs []any
		if err := json.Unmarshal(association.BasisRefs, &refs); err != nil || refs == nil || len(refs) > 100 {
			return ServiceObjectAssociationPlan{}, ErrInvalidServiceObjectAssociation
		}
		association.BasisRefs, _ = json.Marshal(refs)
	}
	if err := ValidateServiceObjectFactRefs(association); err != nil {
		return ServiceObjectAssociationPlan{}, err
	}

	objectVersion := current
	if current.State == ServiceObjectPendingConfirmation {
		var err error
		objectVersion, err = NormalizeServiceObjectVersion(current)
		if err != nil {
			return ServiceObjectAssociationPlan{}, err
		}
		objectVersion.Version++
		objectVersion.State = ServiceObjectConfirmed
		objectVersion.CreatedBy = confirmerID
		objectVersion.CreatedAt = now.UTC()
		objectVersion.EffectiveFrom = now.UTC()
		objectVersion.PayloadSHA256 = ""
	}

	association.ServiceObjectID = head.ID
	association.AssociationVersion = head.CurrentAssociationVersion + 1
	association.ObjectVersion = objectVersion.Version
	association.ConfirmedBy = confirmerID
	association.ConfirmedAt = now.UTC()
	association.CreatedAt = now.UTC()
	factsJSON, factsSHA, err := CanonicalServiceObjectFacts(association.FactRefs)
	if err != nil {
		return ServiceObjectAssociationPlan{}, ErrInvalidServiceObjectAssociation
	}
	association.FactsCanonicalJSON = factsJSON
	association.FactsSHA256 = factsSHA
	association.PayloadSHA256 = ""

	head.CurrentVersion = objectVersion.Version
	head.CurrentAssociationVersion = association.AssociationVersion
	return ServiceObjectAssociationPlan{Head: head, ObjectVersion: objectVersion, Association: association}, nil
}

func CanonicalServiceObjectIdentitySHA256(version ServiceObjectVersion) (string, error) {
	normalized, err := NormalizeServiceObjectVersion(version)
	if err != nil {
		return "", err
	}
	payload := struct {
		ObjectType   ServiceObjectType    `json:"object_type"`
		DisplayName  string               `json:"display_name"`
		BusinessCode string               `json:"business_code"`
		Aliases      []ServiceObjectAlias `json:"aliases"`
		State        ServiceObjectState   `json:"state"`
		EffectiveAt  time.Time            `json:"effective_from"`
		BasisRefs    json.RawMessage      `json:"basis_refs"`
		BasisSummary string               `json:"basis_summary"`
	}{normalized.ObjectType, normalized.DisplayName, normalized.BusinessCode, normalized.Aliases, normalized.State, normalized.EffectiveFrom.UTC(), normalized.BasisRefs, normalized.BasisSummary}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func CanonicalServiceObjectFacts(facts []ServiceObjectFactRef) ([]byte, string, error) {
	encoded, err := json.Marshal(facts)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(digest[:]), nil
}

func NormalizeServiceObjectVersion(input ServiceObjectVersion) (ServiceObjectVersion, error) {
	item := input
	item.DisplayName = strings.TrimSpace(item.DisplayName)
	item.BusinessCode = strings.TrimSpace(item.BusinessCode)
	item.BasisSummary = strings.TrimSpace(item.BasisSummary)
	if len(item.BasisRefs) == 0 {
		item.BasisRefs = json.RawMessage(`[]`)
	} else {
		var refs []any
		if err := json.Unmarshal(item.BasisRefs, &refs); err != nil || refs == nil || len(refs) > 100 {
			return ServiceObjectVersion{}, ErrInvalidServiceObjectAssociation
		}
		canonicalRefs, err := json.Marshal(refs)
		if err != nil {
			return ServiceObjectVersion{}, ErrInvalidServiceObjectAssociation
		}
		item.BasisRefs = canonicalRefs
	}
	if !item.EffectiveFrom.IsZero() {
		item.EffectiveFrom = item.EffectiveFrom.UTC().Truncate(time.Microsecond)
	}
	if item.DisplayName == "" || len([]rune(item.DisplayName)) > 256 || len([]rune(item.BusinessCode)) > 256 || len([]rune(item.BasisSummary)) > 1000 {
		return ServiceObjectVersion{}, ErrInvalidServiceObjectAssociation
	}
	if item.ObjectType != ServiceObjectDedicatedLine && item.ObjectType != ServiceObjectService {
		return ServiceObjectVersion{}, ErrInvalidServiceObjectAssociation
	}
	switch item.State {
	case ServiceObjectPendingConfirmation, ServiceObjectConfirmed, ServiceObjectInactive:
	default:
		return ServiceObjectVersion{}, ErrInvalidServiceObjectAssociation
	}
	if len(item.Aliases) > 50 {
		return ServiceObjectVersion{}, ErrInvalidServiceObjectAssociation
	}
	item.Aliases = append([]ServiceObjectAlias{}, item.Aliases...)
	seen := make(map[ServiceObjectAlias]struct{}, len(item.Aliases))
	for index := range item.Aliases {
		item.Aliases[index].Kind = strings.TrimSpace(item.Aliases[index].Kind)
		item.Aliases[index].Value = strings.TrimSpace(item.Aliases[index].Value)
		alias := item.Aliases[index]
		if alias.Kind == "" || alias.Value == "" || len([]rune(alias.Kind)) > 64 || len([]rune(alias.Value)) > 256 {
			return ServiceObjectVersion{}, ErrInvalidServiceObjectAssociation
		}
		if _, exists := seen[alias]; exists {
			return ServiceObjectVersion{}, ErrInvalidServiceObjectAssociation
		}
		seen[alias] = struct{}{}
	}
	sort.Slice(item.Aliases, func(i, j int) bool {
		if item.Aliases[i].Kind != item.Aliases[j].Kind {
			return item.Aliases[i].Kind < item.Aliases[j].Kind
		}
		return item.Aliases[i].Value < item.Aliases[j].Value
	})
	return item, nil
}

func ValidateServiceObjectFactRefs(association ServiceObjectContractAssociation) error {
	seen := make(map[string]struct{}, len(association.FactRefs))
	for _, fact := range association.FactRefs {
		ruleFact := fact.Fact
		factSHA, err := CanonicalRuleFactSHA256(ruleFact)
		if strings.TrimSpace(ruleFact.ID) == "" || strings.TrimSpace(ruleFact.RuleID) == "" || strings.TrimSpace(ruleFact.Dimension) == "" ||
			ruleFact.ObjectRef != association.ObjectRef || strings.TrimSpace(ruleFact.Value) == "" || strings.TrimSpace(ruleFact.Unit) == "" || strings.TrimSpace(ruleFact.SourceRole) == "" ||
			fact.DocumentID != association.DocumentID || fact.ParsedDocumentID != association.ParsedDocumentID ||
			len(ruleFact.Citations) == 0 || !ValidServiceObjectSHA256(fact.CanonicalFactSHA256) || err != nil || factSHA != fact.CanonicalFactSHA256 {
			return ErrInvalidServiceObjectAssociation
		}
		if _, ok := seen[ruleFact.ID]; ok {
			return ErrInvalidServiceObjectAssociation
		}
		seen[ruleFact.ID] = struct{}{}
		for _, citation := range ruleFact.Citations {
			if citation.SourceID != fact.DocumentID || citation.SourceVersion != fact.ParsedDocumentID || citation.Start < 0 || citation.End <= citation.Start ||
				citation.End-citation.Start != utf8.RuneCountInString(citation.Quote) || strings.TrimSpace(citation.Quote) == "" ||
				!ValidServiceObjectSHA256(citation.SourceDigest) {
				return ErrInvalidServiceObjectAssociation
			}
		}
	}
	return nil
}

func CanonicalRuleFactSHA256(fact rulepack.Fact) (string, error) {
	encoded, err := json.Marshal(fact)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func CloneServiceObjectFactRefs(input []ServiceObjectFactRef) []ServiceObjectFactRef {
	cloned := make([]ServiceObjectFactRef, len(input))
	for index, ref := range input {
		cloned[index] = ref
		cloned[index].Fact.Exclusions = append([]string(nil), ref.Fact.Exclusions...)
		cloned[index].Fact.Citations = append([]rulepack.Citation(nil), ref.Fact.Citations...)
		if ref.Fact.Applicable != nil {
			applicable := *ref.Fact.Applicable
			cloned[index].Fact.Applicable = &applicable
		}
	}
	return cloned
}

func ValidServiceObjectSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

type ServiceObjectCreateInput struct {
	ObjectType   ServiceObjectType    `json:"object_type"`
	DisplayName  string               `json:"display_name"`
	BusinessCode string               `json:"business_code,omitempty"`
	Aliases      []ServiceObjectAlias `json:"aliases"`
	BasisSummary string               `json:"basis_summary,omitempty"`
}

type ServiceObjectMutationResult struct {
	ServiceObjectID    string `json:"service_object_id"`
	Version            int    `json:"version"`
	AssociationVersion int    `json:"association_version"`
}

type ServiceObjectIdentityHistory struct {
	Head         ServiceObjectHead                  `json:"head"`
	Versions     []ServiceObjectVersion             `json:"versions"`
	Associations []ServiceObjectContractAssociation `json:"associations"`
}

type ServiceObjectAssociationPin struct {
	AssociationVersion int       `json:"association_version"`
	ObjectVersion      int       `json:"object_version"`
	MaterialID         string    `json:"material_id"`
	MaterialVersion    int       `json:"material_version"`
	DocumentID         string    `json:"document_id"`
	ParsedDocumentID   string    `json:"parsed_document_id"`
	SourceRunID        string    `json:"source_run_id"`
	SourceAttemptID    string    `json:"source_attempt_id"`
	ObjectRef          string    `json:"object_ref"`
	FactsSHA256        string    `json:"facts_sha256"`
	ConfirmedBy        string    `json:"confirmed_by"`
	ConfirmedAt        time.Time `json:"confirmed_at"`
}

type ServiceObjectSummary struct {
	ServiceObjectID           string                       `json:"service_object_id"`
	ObjectType                ServiceObjectType            `json:"object_type"`
	State                     ServiceObjectState           `json:"state"`
	CurrentVersion            int                          `json:"current_version"`
	CurrentAssociationVersion int                          `json:"current_association_version"`
	DisplayName               string                       `json:"display_name"`
	BusinessCode              string                       `json:"business_code"`
	Aliases                   []ServiceObjectAlias         `json:"aliases"`
	CurrentAssociation        *ServiceObjectAssociationPin `json:"current_association"`
	CanManage                 bool                         `json:"can_manage"`
	NeedsReassociation        bool                         `json:"needs_reassociation"`
}

type ServiceObjectMappingPin struct {
	MappingID          string                 `json:"mapping_id"`
	MappingVersion     int64                  `json:"mapping_version"`
	BindingVersion     string                 `json:"binding_version"`
	ObjectVersion      int                    `json:"object_version"`
	AssociationVersion int                    `json:"association_version"`
	ExternalObject     OperatorExternalObject `json:"external_object"`
	EffectiveFrom      time.Time              `json:"effective_from"`
	EffectiveUntil     time.Time              `json:"effective_until"`
}

// ServiceObjectMaterialHead is the narrow current-material fact used to show
// whether a frozen association still points at the selected material version.
type ServiceObjectMaterialHead struct {
	ID                string
	CurrentVersion    int
	CurrentDocumentID string
}

// ServiceObjectMaterialSelectionFacts contains only the case-local inputs
// needed by NeedsReassociation; it is not a grant or a completeness claim.
type ServiceObjectMaterialSelectionFacts struct {
	Pending             bool
	SelectedDocumentIDs []string
	MaterialHeads       []ServiceObjectMaterialHead
}

func NeedsReassociation(association *ServiceObjectAssociationPin, facts ServiceObjectMaterialSelectionFacts) bool {
	if association == nil || facts.Pending {
		return true
	}
	selected := false
	for _, documentID := range facts.SelectedDocumentIDs {
		if documentID == association.DocumentID {
			selected = true
			break
		}
	}
	if !selected {
		return true
	}
	for _, material := range facts.MaterialHeads {
		if material.ID == association.MaterialID {
			return material.CurrentVersion != association.MaterialVersion || material.CurrentDocumentID != association.DocumentID
		}
	}
	return true
}

// ServiceObjectMappingBindingPayload is the public JSON shape accepted from
// the legacy binding response body. The private request digest stays in Store.
type ServiceObjectMappingBindingPayload struct {
	CaseID             string                 `json:"case_id"`
	BindingID          string                 `json:"binding_id"`
	Version            string                 `json:"version"`
	Status             string                 `json:"status"`
	ContractObject     OperatorContractObject `json:"contract_object"`
	ExternalObject     OperatorExternalObject `json:"external_object"`
	SourceSystem       string                 `json:"source_system"`
	ConfirmedBy        string                 `json:"confirmed_by"`
	ConfirmedAt        time.Time              `json:"confirmed_at"`
	EffectiveFrom      time.Time              `json:"effective_from"`
	EffectiveUntil     time.Time              `json:"effective_until"`
	CreatedAt          time.Time              `json:"created_at"`
	MappingID          string                 `json:"mapping_id,omitempty"`
	MappingVersion     int64                  `json:"mapping_version,omitempty"`
	ServiceObjectID    string                 `json:"service_object_id,omitempty"`
	ObjectVersion      int                    `json:"object_version,omitempty"`
	AssociationVersion int                    `json:"association_version,omitempty"`
}

type ServiceObjectDetail struct {
	ServiceObjectID    string                            `json:"service_object_id"`
	CurrentVersion     int                               `json:"current_version"`
	Identity           ServiceObjectVersion              `json:"identity"`
	Association        *ServiceObjectContractAssociation `json:"association"`
	Mapping            *ServiceObjectMappingPin          `json:"mapping"`
	CanManage          bool                              `json:"can_manage"`
	NeedsReassociation bool                              `json:"needs_reassociation"`
}

type ServiceObjectListResult struct {
	Items      []ServiceObjectSummary `json:"items"`
	NextCursor *string                `json:"next_cursor"`
	CanManage  bool                   `json:"can_manage"`
}

type ServiceObjectVersionPage struct {
	Items      []ServiceObjectVersion `json:"items"`
	NextCursor *string                `json:"next_cursor"`
}

type ServiceObjectAssociationPage struct {
	Items      []ServiceObjectContractAssociation `json:"items"`
	NextCursor *string                            `json:"next_cursor"`
}

type ServiceObjectAssociationOption struct {
	ObjectRef   string                 `json:"object_ref"`
	Facts       []ServiceObjectFactRef `json:"facts"`
	FactsSHA256 string                 `json:"facts_sha256"`
	BasisRefs   json.RawMessage        `json:"basis_refs"`
}

type ServiceObjectAssociationPreview struct {
	ServiceObjectID            string                           `json:"service_object_id"`
	ExpectedObjectVersion      int                              `json:"expected_object_version"`
	ExpectedAssociationVersion int                              `json:"expected_association_version"`
	RunID                      string                           `json:"run_id"`
	AttemptID                  string                           `json:"attempt_id"`
	MaterialID                 string                           `json:"material_id"`
	MaterialVersion            int                              `json:"material_version"`
	DocumentID                 string                           `json:"document_id"`
	ParsedDocumentID           string                           `json:"parsed_document_id"`
	CandidatesSHA256           string                           `json:"candidate_sha256"`
	Options                    []ServiceObjectAssociationOption `json:"options"`
}

type OperatorContractObject struct {
	ObjectType string  `json:"object_type"`
	ObjectID   string  `json:"object_id"`
	Label      *string `json:"label"`
}

type OperatorExternalObject struct {
	ConnectorID      string `json:"connector_id"`
	ExternalObjectID string `json:"external_object_id"`
}

func AssociationPin(item ServiceObjectContractAssociation) ServiceObjectAssociationPin {
	return ServiceObjectAssociationPin{AssociationVersion: item.AssociationVersion, ObjectVersion: item.ObjectVersion,
		MaterialID: item.MaterialID, MaterialVersion: item.MaterialVersion, DocumentID: item.DocumentID,
		ParsedDocumentID: item.ParsedDocumentID, SourceRunID: item.SourceRunID, SourceAttemptID: item.SourceAttemptID,
		ObjectRef: item.ObjectRef, FactsSHA256: item.FactsSHA256, ConfirmedBy: item.ConfirmedBy, ConfirmedAt: item.ConfirmedAt}
}

func AssociationPinPointer(item *ServiceObjectContractAssociation) *ServiceObjectAssociationPin {
	if item == nil {
		return nil
	}
	pin := AssociationPin(*item)
	return &pin
}

func CanonicalServiceObjectAssociationSHA256(association ServiceObjectContractAssociation) (string, error) {
	var basis []any
	if len(association.BasisRefs) == 0 {
		basis = []any{}
	} else if err := json.Unmarshal(association.BasisRefs, &basis); err != nil || basis == nil {
		return "", errors.New("service object association basis refs are invalid")
	}
	basisJSON, err := json.Marshal(basis)
	if err != nil {
		return "", err
	}
	payload := struct {
		ObjectID           string          `json:"service_object_id"`
		AssociationVersion int             `json:"association_version"`
		ObjectVersion      int             `json:"object_version"`
		MaterialID         string          `json:"material_id"`
		MaterialVersion    int             `json:"material_version"`
		DocumentID         string          `json:"document_id"`
		ParsedDocumentID   string          `json:"parsed_document_id"`
		RunID              string          `json:"source_run_id"`
		AttemptID          string          `json:"source_attempt_id"`
		ObjectRef          string          `json:"object_ref"`
		FactsSHA256        string          `json:"facts_sha256"`
		BasisRefs          json.RawMessage `json:"basis_refs"`
		IdentityDecisionID string          `json:"identity_change_decision_id,omitempty"`
		ConfirmedBy        string          `json:"confirmed_by"`
		ConfirmedAt        time.Time       `json:"confirmed_at"`
		CreatedAt          time.Time       `json:"created_at"`
	}{association.ServiceObjectID, association.AssociationVersion, association.ObjectVersion, association.MaterialID, association.MaterialVersion,
		association.DocumentID, association.ParsedDocumentID, association.SourceRunID, association.SourceAttemptID, association.ObjectRef,
		association.FactsSHA256, basisJSON, association.IdentityChangeDecisionID, association.ConfirmedBy,
		association.ConfirmedAt.UTC().Truncate(time.Microsecond), association.CreatedAt.UTC().Truncate(time.Microsecond)}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func ServiceObjectBasisRefs(refs []ServiceObjectFactRef) (json.RawMessage, error) {
	type basisRef struct {
		DocumentID       string `json:"document_id"`
		ParsedDocumentID string `json:"parsed_document_id"`
		Start            int    `json:"start"`
		End              int    `json:"end"`
		QuoteSHA256      string `json:"quote_sha256"`
	}
	items := make([]basisRef, 0)
	seen := map[basisRef]struct{}{}
	for _, ref := range refs {
		for _, citation := range ref.Fact.Citations {
			quoteDigest := sha256.Sum256([]byte(citation.Quote))
			item := basisRef{DocumentID: ref.DocumentID, ParsedDocumentID: ref.ParsedDocumentID, Start: citation.Start, End: citation.End, QuoteSHA256: hex.EncodeToString(quoteDigest[:])}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			items = append(items, item)
		}
	}
	encoded, err := json.Marshal(items)
	return encoded, err
}
