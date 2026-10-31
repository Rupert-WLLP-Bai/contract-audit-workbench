package store

import (
	"bytes"
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

type serviceObjectCandidateDigestPin struct {
	ObjectRef   string `json:"object_ref"`
	FactsSHA256 string `json:"facts_sha256"`
}

type serviceObjectCandidateDigestInput struct {
	ServiceObjectID            string
	ExpectedObjectVersion      int
	ExpectedAssociationVersion int
	RunID                      string
	AttemptID                  string
	MaterialID                 string
	MaterialVersion            int
	DocumentID                 string
	ParsedDocumentID           string
	Candidates                 []serviceObjectCandidateDigestPin
}

func serviceObjectCandidatesSHA256(input serviceObjectCandidateDigestInput) (string, error) {
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
		Candidates                 []serviceObjectCandidateDigestPin `json:"candidates"`
	}{input.ServiceObjectID, input.ExpectedObjectVersion, input.ExpectedAssociationVersion, true,
		input.RunID, input.AttemptID, input.MaterialID, input.MaterialVersion, input.DocumentID, input.ParsedDocumentID, input.Candidates}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// verifiedServiceObjectIdentityChange is produced only after the store's
// decision service has loaded an approved SOD decision and verified its
// before-version CAS and complete after-payload hash in the same transaction.
// The planner receives this trusted pin instead of accepting an arbitrary
// decision ID from an HTTP request.
type verifiedServiceObjectIdentityChange struct {
	decisionID             string
	expectedObjectVersion  int
	expectedAssociation    int
	beforePayloadSHA256    string
	afterPayloadSHA256     string
	effectiveFrom          time.Time
	basisRefs              json.RawMessage
	validatedByDecisionSvc bool
}

var errInvalidServiceObjectAssociation = errors.New("invalid service object association")

// PlanServiceObjectAssociation validates the CAS and produces the immutable
// records for an explicit association confirmation. It has no side effects.
func PlanServiceObjectAssociation(head ServiceObjectHead, current ServiceObjectVersion, candidate ServiceObjectAssociationCandidate, confirmerID string, now time.Time) (ServiceObjectAssociationPlan, error) {
	if head.ID == "" || head.ID != current.ServiceObjectID || head.CurrentVersion < 1 ||
		head.CurrentVersion != current.Version ||
		candidate.ExpectedObjectVersion != head.CurrentVersion || candidate.ExpectedAssociationVersion != head.CurrentAssociationVersion ||
		now.IsZero() || strings.TrimSpace(confirmerID) == "" || current.State == ServiceObjectInactive || !candidate.Confirmed {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	if (head.CurrentAssociationVersion == 0 && head.CurrentVersion != 1) || head.CurrentAssociationVersion < 0 {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	if current.ObjectType != ServiceObjectDedicatedLine && current.ObjectType != ServiceObjectService {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	if current.State != ServiceObjectPendingConfirmation && current.State != ServiceObjectConfirmed {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	association := candidate.Association
	association.FactRefs = cloneServiceObjectFactRefs(association.FactRefs)
	if strings.TrimSpace(association.MaterialID) == "" || association.MaterialVersion < 1 ||
		strings.TrimSpace(association.DocumentID) == "" || strings.TrimSpace(association.ParsedDocumentID) == "" ||
		strings.TrimSpace(association.SourceRunID) == "" || strings.TrimSpace(association.SourceAttemptID) == "" ||
		strings.TrimSpace(association.ObjectRef) == "" || len(association.FactRefs) == 0 || len(association.FactRefs) > 100 {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	if len(association.BasisRefs) == 0 {
		association.BasisRefs = json.RawMessage(`[]`)
	} else {
		var refs []any
		if err := json.Unmarshal(association.BasisRefs, &refs); err != nil || refs == nil || len(refs) > 100 {
			return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
		}
		association.BasisRefs, _ = json.Marshal(refs)
	}
	if err := validateServiceObjectFactRefs(association); err != nil {
		return ServiceObjectAssociationPlan{}, err
	}

	objectVersion := current
	if current.State == ServiceObjectPendingConfirmation {
		var err error
		objectVersion, err = normalizeServiceObjectVersion(current)
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
	factsJSON, factsSHA, err := canonicalServiceObjectFacts(association.FactRefs)
	if err != nil {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	association.FactsCanonicalJSON = factsJSON
	association.FactsSHA256 = factsSHA
	association.PayloadSHA256 = ""

	head.CurrentVersion = objectVersion.Version
	head.CurrentAssociationVersion = association.AssociationVersion
	return ServiceObjectAssociationPlan{Head: head, ObjectVersion: objectVersion, Association: association}, nil
}

// PlanServiceObjectIdentityChange creates the next identity and association
// versions after an independently approved SOD decision. It deliberately
// carries forward the exact frozen fact bytes and digest from the prior
// association; it never consults a newer parser result.
func PlanServiceObjectIdentityChange(head ServiceObjectHead, current ServiceObjectVersion, currentAssociation ServiceObjectContractAssociation, proposed ServiceObjectVersion, approval verifiedServiceObjectIdentityChange, approverID string, now time.Time) (ServiceObjectAssociationPlan, error) {
	if head.ID == "" || head.ID != current.ServiceObjectID || head.CurrentVersion != current.Version ||
		head.CurrentVersion != approval.expectedObjectVersion || head.CurrentAssociationVersion != approval.expectedAssociation ||
		currentAssociation.ServiceObjectID != head.ID || currentAssociation.ObjectVersion != current.Version ||
		currentAssociation.AssociationVersion != head.CurrentAssociationVersion || head.CurrentAssociationVersion < 1 ||
		current.State != ServiceObjectConfirmed || strings.TrimSpace(approval.decisionID) == "" || !approval.validatedByDecisionSvc ||
		strings.TrimSpace(approverID) == "" || now.IsZero() ||
		len(currentAssociation.FactsCanonicalJSON) == 0 || !validServiceObjectSHA256(currentAssociation.FactsSHA256) {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	priorDigest := sha256.Sum256(currentAssociation.FactsCanonicalJSON)
	if hex.EncodeToString(priorDigest[:]) != currentAssociation.FactsSHA256 {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	canonicalFacts, canonicalFactsSHA, err := canonicalServiceObjectFacts(currentAssociation.FactRefs)
	if err != nil || !bytes.Equal(canonicalFacts, currentAssociation.FactsCanonicalJSON) || canonicalFactsSHA != currentAssociation.FactsSHA256 {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	version, err := normalizeServiceObjectVersion(proposed)
	if err != nil || version.ServiceObjectID != head.ID || (version.State != ServiceObjectConfirmed && version.State != ServiceObjectInactive) {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	beforeHash, err := canonicalServiceObjectIdentitySHA256(current)
	if err != nil || beforeHash != current.PayloadSHA256 || beforeHash != approval.beforePayloadSHA256 {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	afterHash, err := canonicalServiceObjectIdentitySHA256(version)
	if err != nil || afterHash != approval.afterPayloadSHA256 || !version.EffectiveFrom.UTC().Equal(approval.effectiveFrom) || !bytes.Equal(version.BasisRefs, approval.basisRefs) {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	version.Version = current.Version + 1
	version.ChangeDecisionID = strings.TrimSpace(approval.decisionID)
	version.CreatedBy = approverID
	version.CreatedAt = now.UTC()
	version.PayloadSHA256 = afterHash

	association := currentAssociation
	association.FactRefs = cloneServiceObjectFactRefs(currentAssociation.FactRefs)
	association.FactsCanonicalJSON = append([]byte(nil), currentAssociation.FactsCanonicalJSON...)
	association.AssociationVersion = currentAssociation.AssociationVersion + 1
	association.ObjectVersion = version.Version
	association.IdentityChangeDecisionID = strings.TrimSpace(approval.decisionID)
	association.ConfirmedBy = approverID
	association.ConfirmedAt = now.UTC()
	association.CreatedAt = now.UTC()
	association.PayloadSHA256 = ""

	head.CurrentVersion = version.Version
	head.CurrentAssociationVersion = association.AssociationVersion
	return ServiceObjectAssociationPlan{Head: head, ObjectVersion: version, Association: association}, nil
}

func canonicalServiceObjectIdentitySHA256(version ServiceObjectVersion) (string, error) {
	normalized, err := normalizeServiceObjectVersion(version)
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

func canonicalServiceObjectFacts(facts []ServiceObjectFactRef) ([]byte, string, error) {
	encoded, err := json.Marshal(facts)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(digest[:]), nil
}

func normalizeServiceObjectVersion(input ServiceObjectVersion) (ServiceObjectVersion, error) {
	item := input
	item.DisplayName = strings.TrimSpace(item.DisplayName)
	item.BusinessCode = strings.TrimSpace(item.BusinessCode)
	item.BasisSummary = strings.TrimSpace(item.BasisSummary)
	if len(item.BasisRefs) == 0 {
		item.BasisRefs = json.RawMessage(`[]`)
	} else {
		var refs []any
		if err := json.Unmarshal(item.BasisRefs, &refs); err != nil || refs == nil || len(refs) > 100 {
			return ServiceObjectVersion{}, errInvalidServiceObjectAssociation
		}
		canonicalRefs, err := json.Marshal(refs)
		if err != nil {
			return ServiceObjectVersion{}, errInvalidServiceObjectAssociation
		}
		item.BasisRefs = canonicalRefs
	}
	if !item.EffectiveFrom.IsZero() {
		item.EffectiveFrom = item.EffectiveFrom.UTC().Truncate(time.Microsecond)
	}
	if item.DisplayName == "" || len([]rune(item.DisplayName)) > 256 || len([]rune(item.BusinessCode)) > 256 || len([]rune(item.BasisSummary)) > 1000 {
		return ServiceObjectVersion{}, errInvalidServiceObjectAssociation
	}
	if item.ObjectType != ServiceObjectDedicatedLine && item.ObjectType != ServiceObjectService {
		return ServiceObjectVersion{}, errInvalidServiceObjectAssociation
	}
	switch item.State {
	case ServiceObjectPendingConfirmation, ServiceObjectConfirmed, ServiceObjectInactive:
	default:
		return ServiceObjectVersion{}, errInvalidServiceObjectAssociation
	}
	if len(item.Aliases) > 50 {
		return ServiceObjectVersion{}, errInvalidServiceObjectAssociation
	}
	item.Aliases = append([]ServiceObjectAlias{}, item.Aliases...)
	seen := make(map[ServiceObjectAlias]struct{}, len(item.Aliases))
	for index := range item.Aliases {
		item.Aliases[index].Kind = strings.TrimSpace(item.Aliases[index].Kind)
		item.Aliases[index].Value = strings.TrimSpace(item.Aliases[index].Value)
		alias := item.Aliases[index]
		if alias.Kind == "" || alias.Value == "" || len([]rune(alias.Kind)) > 64 || len([]rune(alias.Value)) > 256 {
			return ServiceObjectVersion{}, errInvalidServiceObjectAssociation
		}
		if _, exists := seen[alias]; exists {
			return ServiceObjectVersion{}, errInvalidServiceObjectAssociation
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

func validateServiceObjectFactRefs(association ServiceObjectContractAssociation) error {
	seen := make(map[string]struct{}, len(association.FactRefs))
	for _, fact := range association.FactRefs {
		ruleFact := fact.Fact
		factSHA, err := canonicalRuleFactSHA256(ruleFact)
		if strings.TrimSpace(ruleFact.ID) == "" || strings.TrimSpace(ruleFact.RuleID) == "" || strings.TrimSpace(ruleFact.Dimension) == "" ||
			ruleFact.ObjectRef != association.ObjectRef || strings.TrimSpace(ruleFact.Value) == "" || strings.TrimSpace(ruleFact.Unit) == "" || strings.TrimSpace(ruleFact.SourceRole) == "" ||
			fact.DocumentID != association.DocumentID || fact.ParsedDocumentID != association.ParsedDocumentID ||
			len(ruleFact.Citations) == 0 || !validServiceObjectSHA256(fact.CanonicalFactSHA256) || err != nil || factSHA != fact.CanonicalFactSHA256 {
			return errInvalidServiceObjectAssociation
		}
		if _, ok := seen[ruleFact.ID]; ok {
			return errInvalidServiceObjectAssociation
		}
		seen[ruleFact.ID] = struct{}{}
		for _, citation := range ruleFact.Citations {
			if citation.SourceID != fact.DocumentID || citation.SourceVersion != fact.ParsedDocumentID || citation.Start < 0 || citation.End <= citation.Start ||
				citation.End-citation.Start != utf8.RuneCountInString(citation.Quote) || strings.TrimSpace(citation.Quote) == "" ||
				!validServiceObjectSHA256(citation.SourceDigest) {
				return errInvalidServiceObjectAssociation
			}
		}
	}
	return nil
}

func canonicalRuleFactSHA256(fact rulepack.Fact) (string, error) {
	encoded, err := json.Marshal(fact)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func cloneServiceObjectFactRefs(input []ServiceObjectFactRef) []ServiceObjectFactRef {
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

func validServiceObjectSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
