package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/accounts"
)

const (
	SubjectRoleCustomer        = "customer"
	SubjectRoleSupplier        = "supplier"
	SubjectRoleContractor      = "contractor"
	SubjectRoleServiceProvider = "service_provider"
	SubjectRoleOther           = "other"
)

type Subject struct {
	TenantID         string    `json:"tenant_id"`
	ID               string    `json:"subject_id"`
	CurrentVersion   int64     `json:"current_version"`
	Status           string    `json:"status"`
	CreatedByUser    string    `json:"created_by_user_id"`
	CreatedByIssuer  string    `json:"created_by_issuer"`
	CreatedBySubject string    `json:"created_by_subject"`
	CreatedAt        time.Time `json:"created_at"`
}

type SubjectIdentityClaimInput struct {
	Namespace      string `json:"namespace"`
	IdentifierType string `json:"identifier_type"`
	Value          string `json:"value"`
}

type SubjectIdentityClaim struct {
	Namespace         string     `json:"namespace"`
	IdentifierType    string     `json:"identifier_type"`
	Value             string     `json:"value"`
	VerificationState string     `json:"verification_state"`
	VerificationScope string     `json:"verification_scope"`
	SourceKind        string     `json:"source_kind"`
	SourceTime        *time.Time `json:"source_time,omitempty"`
	SourceCatalogID   string     `json:"source_catalog_id,omitempty"`
	SourceSnapshotID  string     `json:"source_snapshot_id,omitempty"`
	SourceDigest      string     `json:"source_digest,omitempty"`
	SourceValidFrom   *time.Time `json:"source_valid_from,omitempty"`
	SourceValidUntil  *time.Time `json:"source_valid_until,omitempty"`
	PayloadHash       string     `json:"payload_hash"`
}

type SubjectAliasInput struct {
	Value string `json:"value"`
	Kind  string `json:"kind"`
}

type SubjectAlias struct {
	Value      string `json:"value"`
	Kind       string `json:"kind"`
	SourceKind string `json:"source_kind"`
}

type SubjectVersion struct {
	TenantID          string                 `json:"tenant_id"`
	SubjectID         string                 `json:"subject_id"`
	Version           int64                  `json:"version"`
	DisplayName       string                 `json:"display_name"`
	LegalName         string                 `json:"legal_name,omitempty"`
	VerificationState string                 `json:"verification_state"`
	VerificationScope string                 `json:"verification_scope,omitempty"`
	SourceKind        string                 `json:"source_kind"`
	BasisSummary      string                 `json:"basis_summary"`
	PayloadHash       string                 `json:"payload_hash"`
	IdentityClaims    []SubjectIdentityClaim `json:"identity_claims"`
	Aliases           []SubjectAlias         `json:"aliases"`
	CreatedByUser     string                 `json:"created_by_user_id"`
	CreatedByIssuer   string                 `json:"created_by_issuer"`
	CreatedBySubject  string                 `json:"created_by_subject"`
	CreatedAt         time.Time              `json:"created_at"`
}

type CaseSubjectRole struct {
	TenantID       string    `json:"tenant_id"`
	OrgID          string    `json:"org_id"`
	CaseID         string    `json:"case_id"`
	BindingID      string    `json:"binding_id"`
	CurrentVersion int64     `json:"current_version"`
	CreatedByUser  string    `json:"created_by_user_id"`
	CreatedAt      time.Time `json:"created_at"`
}

type CaseSubjectRoleVersion struct {
	TenantID         string    `json:"tenant_id"`
	OrgID            string    `json:"org_id"`
	CaseID           string    `json:"case_id"`
	BindingID        string    `json:"binding_id"`
	Version          int64     `json:"version"`
	SubjectID        string    `json:"subject_id"`
	SubjectVersion   int64     `json:"subject_version"`
	Role             string    `json:"role"`
	BindingState     string    `json:"binding_state"`
	SourceKind       string    `json:"source_kind"`
	LegacyLabel      string    `json:"legacy_label,omitempty"`
	CreatedByUser    string    `json:"created_by_user_id"`
	CreatedByIssuer  string    `json:"created_by_issuer"`
	CreatedBySubject string    `json:"created_by_subject"`
	CreatedAt        time.Time `json:"created_at"`
}

type SubjectIdentityClaimProjection struct {
	Namespace         string     `json:"namespace"`
	IdentifierType    string     `json:"identifier_type"`
	Value             string     `json:"value,omitempty"`
	MaskedValue       string     `json:"masked_value,omitempty"`
	VerificationState string     `json:"verification_state"`
	VerificationScope string     `json:"verification_scope,omitempty"`
	SourceKind        string     `json:"source_kind"`
	SourceTime        *time.Time `json:"source_time"`
	PayloadHash       string     `json:"payload_hash"`
}

type TrustedPrincipalProjection struct {
	UserID  string `json:"user_id"`
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
}

type SubjectSummary struct {
	SubjectID         string                           `json:"subject_id"`
	CurrentVersion    int64                            `json:"current_version"`
	DisplayName       string                           `json:"display_name"`
	LegalName         *string                          `json:"legal_name"`
	VerificationState string                           `json:"verification_state"`
	VerificationScope string                           `json:"verification_scope,omitempty"`
	Aliases           []SubjectAlias                   `json:"aliases"`
	Identifiers       []SubjectIdentityClaimProjection `json:"identifiers"`
	CreatedBy         TrustedPrincipalProjection       `json:"created_by"`
	CreatedAt         time.Time                        `json:"created_at"`
}

type SubjectVersionProjection struct {
	SubjectID         string                           `json:"subject_id"`
	Version           int64                            `json:"version"`
	DisplayName       string                           `json:"display_name"`
	LegalName         *string                          `json:"legal_name"`
	VerificationState string                           `json:"verification_state"`
	VerificationScope string                           `json:"verification_scope,omitempty"`
	SourceKind        string                           `json:"source_kind"`
	BasisSummary      string                           `json:"basis_summary"`
	PayloadHash       string                           `json:"payload_hash"`
	IdentityClaims    []SubjectIdentityClaimProjection `json:"identity_claims"`
	Aliases           []SubjectAlias                   `json:"aliases"`
	CreatedBy         TrustedPrincipalProjection       `json:"created_by"`
	CreatedAt         time.Time                        `json:"created_at"`
}

type CaseSubjectBindingProjection struct {
	BindingID      string                     `json:"binding_id"`
	BindingVersion int64                      `json:"binding_version"`
	CaseID         string                     `json:"case_id"`
	Role           string                     `json:"role"`
	BindingState   string                     `json:"binding_state"`
	SourceKind     string                     `json:"source_kind"`
	LegacyLabel    *string                    `json:"legacy_label"`
	Subject        SubjectVersionProjection   `json:"subject"`
	CreatedBy      TrustedPrincipalProjection `json:"created_by"`
	CreatedAt      time.Time                  `json:"created_at"`
}

type LegacySubjectProfileProjection struct {
	Version  int     `json:"version"`
	Customer *string `json:"customer"`
	Supplier *string `json:"supplier"`
}

type CaseSubjectListProjection struct {
	Items         []CaseSubjectBindingProjection  `json:"items"`
	LegacyProfile *LegacySubjectProfileProjection `json:"legacy_profile"`
	CanManage     bool                            `json:"can_manage"`
}

type SubjectChangeTarget struct {
	Kind      string `json:"kind"`
	SubjectID string `json:"subject_id"`
	BindingID string `json:"binding_id,omitempty"`
}

type SubjectChangeRequest struct {
	TenantID               string                     `json:"tenant_id"`
	OrgID                  string                     `json:"org_id"`
	RequestID              string                     `json:"request_id"`
	SourceCaseID           string                     `json:"source_case_id"`
	TargetKind             string                     `json:"target_kind"`
	TargetSubjectID        string                     `json:"target_subject_id"`
	TargetBindingID        string                     `json:"target_binding_id,omitempty"`
	ExpectedSubjectVersion int64                      `json:"expected_subject_version"`
	ExpectedBindingVersion *int64                     `json:"expected_binding_version,omitempty"`
	CanonicalPayload       json.RawMessage            `json:"canonical_payload"`
	CanonicalPayloadHash   string                     `json:"canonical_payload_hash"`
	BasisNote              string                     `json:"basis_note"`
	BasisRefs              json.RawMessage            `json:"basis_refs"`
	ExpectedBindingsHash   string                     `json:"expected_bindings_hash"`
	ProposedBy             TrustedPrincipalProjection `json:"proposed_by"`
	CreatedAt              time.Time                  `json:"created_at"`
}

// PartyContextItem freezes the subject and binding version used by one review.
// Synthetic verification provenance is carried explicitly so reports cannot
// collapse it into an official verification label.
type PartyContextItem struct {
	BindingID         string     `json:"binding_id"`
	BindingVersion    int64      `json:"binding_version"`
	Role              string     `json:"role"`
	BindingState      string     `json:"binding_state"`
	SubjectID         string     `json:"subject_id"`
	SubjectVersion    int64      `json:"subject_version"`
	DisplayName       string     `json:"display_name"`
	VerificationState string     `json:"verification_state"`
	VerificationScope string     `json:"verification_scope,omitempty"`
	SourceCatalogID   string     `json:"source_catalog_id,omitempty"`
	SourceSnapshotID  string     `json:"source_snapshot_id,omitempty"`
	SourceDigest      string     `json:"source_digest,omitempty"`
	SourceTime        *time.Time `json:"source_time,omitempty"`
	SourceValidFrom   *time.Time `json:"source_valid_from,omitempty"`
	SourceValidUntil  *time.Time `json:"source_valid_until,omitempty"`
}

type SubjectChangeDecision struct {
	TenantID                string                     `json:"tenant_id"`
	DecisionID              string                     `json:"decision_id"`
	RequestID               string                     `json:"request_id"`
	Decision                string                     `json:"decision"`
	Actor                   TrustedPrincipalProjection `json:"actor"`
	ExpectedSubjectVersion  int64                      `json:"expected_subject_version"`
	ExpectedBindingVersion  *int64                     `json:"expected_binding_version,omitempty"`
	CanonicalPayloadHash    string                     `json:"canonical_payload_hash"`
	AffectedBindingsHash    string                     `json:"affected_bindings_hash"`
	Reason                  string                     `json:"reason"`
	AppliedSubjectVersion   *int64                     `json:"applied_subject_version,omitempty"`
	BindingVersionsAppended int                        `json:"binding_versions_appended"`
	CreatedAt               time.Time                  `json:"created_at"`
}

type ControlledSourceBasisRef struct {
	Kind            string     `json:"kind"`
	SnapshotID      string     `json:"snapshot_id"`
	SourceCatalogID string     `json:"source_catalog_id"`
	SourceVersion   int        `json:"source_version"`
	SourceDigest    string     `json:"source_digest"`
	SourceScope     string     `json:"source_scope"`
	SourceAsOf      time.Time  `json:"source_as_of"`
	ValidFrom       time.Time  `json:"valid_from"`
	ValidUntil      *time.Time `json:"valid_until"`
	CapturedAt      time.Time  `json:"captured_at"`
}

type SubjectIdempotencyRecord struct {
	TenantID    string          `json:"tenant_id"`
	ActorUserID string          `json:"actor_user_id"`
	Operation   string          `json:"operation"`
	TargetID    string          `json:"target_id"`
	Key         string          `json:"key"`
	PayloadHash string          `json:"payload_hash"`
	Response    json.RawMessage `json:"response"`
	HTTPStatus  int             `json:"http_status"`
	CreatedAt   time.Time       `json:"created_at"`
}

// SubjectSourceSnapshot freezes the canonical bytes from one configured
// synthetic controlled catalog for case-scoped evidence lookup.
type SubjectSourceSnapshot struct {
	TenantID         string    `json:"tenant_id"`
	OrgID            string    `json:"org_id"`
	CaseID           string    `json:"case_id"`
	SnapshotID       string    `json:"snapshot_id"`
	SourceCatalogID  string    `json:"source_catalog_id"`
	SourceVersion    int       `json:"source_version"`
	SourcePayload    string    `json:"source_payload"`
	SourceDigest     string    `json:"source_digest"`
	SourceScope      string    `json:"source_scope"`
	CapturedAt       time.Time `json:"captured_at"`
	CreatedByUser    string    `json:"created_by_user_id"`
	CreatedByIssuer  string    `json:"created_by_issuer"`
	CreatedBySubject string    `json:"created_by_subject"`
}

type SubjectSourceRecord struct {
	RecordID    string     `json:"record_id"`
	DisplayName string     `json:"display_name"`
	USCC        string     `json:"uscc"`
	SourceAsOf  time.Time  `json:"source_as_of"`
	ValidFrom   time.Time  `json:"valid_from"`
	ValidUntil  *time.Time `json:"valid_until"`
}

type SubjectSourceManifest struct {
	SourceID          string                `json:"source_id"`
	SourceVersion     int                   `json:"source_version"`
	DisplayName       string                `json:"display_name"`
	VerificationScope string                `json:"verification_scope"`
	Records           []SubjectSourceRecord `json:"records"`
}

type SubjectSourceDirectoryItem struct {
	SourceID          string `json:"source_id"`
	SourceVersion     int    `json:"source_version"`
	DisplayName       string `json:"display_name"`
	VerificationScope string `json:"verification_scope"`
}

type SubjectSourceDirectoryResult struct {
	Items []SubjectSourceDirectoryItem `json:"items"`
}

type SubjectSourceSnapshotCreateInput struct {
	SourceID              string `json:"source_catalog_id"`
	ExpectedSourceVersion int    `json:"expected_source_version"`
}

type SubjectSourceSnapshotResult struct {
	SnapshotID    string    `json:"snapshot_id"`
	SourceID      string    `json:"source_catalog_id"`
	SourceVersion int       `json:"source_version"`
	SourceDigest  string    `json:"source_digest"`
	SourceScope   string    `json:"source_scope"`
	CapturedAt    time.Time `json:"captured_at"`
	RecordCount   int       `json:"record_count"`
	Label         string    `json:"label"`
}

type SubjectSourceVerifyInput struct {
	SubjectID              string `json:"subject_id"`
	ExpectedSubjectVersion int64  `json:"expected_subject_version"`
}

type SubjectSourceMatchedRecord struct {
	DisplayName      string `json:"display_name"`
	MaskedIdentifier string `json:"masked_identifier"`
}

type SubjectSourceVerifyResult struct {
	RequestID       *string                     `json:"request_id"`
	State           string                      `json:"state"`
	MatchOutcome    string                      `json:"match_outcome"`
	SnapshotID      string                      `json:"snapshot_id"`
	SourceCatalogID string                      `json:"source_catalog_id"`
	SourceVersion   int                         `json:"source_version"`
	SourceDigest    string                      `json:"source_digest"`
	SourceScope     string                      `json:"source_scope"`
	SourceAsOf      *time.Time                  `json:"source_as_of"`
	ValidFrom       *time.Time                  `json:"valid_from"`
	ValidUntil      *time.Time                  `json:"valid_until"`
	MatchedRecord   *SubjectSourceMatchedRecord `json:"matched_record"`
}

type CaseRolePatch struct {
	SubjectID                 *string
	ExpectedNewSubjectVersion *int64
	Role                      *string
	BindingState              *string
}

func applySubjectPatch(current SubjectVersion, raw json.RawMessage) (SubjectVersion, error) {
	// Copy slice-backed fields before materializing a new immutable version.
	current.IdentityClaims = append([]SubjectIdentityClaim(nil), current.IdentityClaims...)
	current.Aliases = append([]SubjectAlias(nil), current.Aliases...)
	fields, err := decodeFlatObject(raw)
	if err != nil {
		return SubjectVersion{}, err
	}
	if len(fields) == 0 {
		return SubjectVersion{}, errors.New("subject patch must change at least one field")
	}
	for key := range fields {
		switch key {
		case "display_name", "legal_name", "identity_claims", "aliases":
		default:
			return SubjectVersion{}, fmt.Errorf("field %q is not allowed for a subject target", key)
		}
	}
	if field, ok := fields["display_name"]; ok {
		value, err := decodeStringField(field, false)
		if err != nil || strings.TrimSpace(value) == "" {
			return SubjectVersion{}, errors.New("display_name must be a non-empty string")
		}
		current.DisplayName = strings.TrimSpace(value)
		invalidateSubjectIdentityVerification(&current)
	}
	if field, ok := fields["legal_name"]; ok {
		value, err := decodeStringField(field, false)
		if err != nil {
			return SubjectVersion{}, errors.New("legal_name must be a string; use an empty string to clear")
		}
		current.LegalName = value
		invalidateSubjectIdentityVerification(&current)
	}
	if field, ok := fields["identity_claims"]; ok {
		var inputs []SubjectIdentityClaimInput
		if err := decodeStrictJSON(field, &inputs); err != nil || inputs == nil {
			return SubjectVersion{}, errors.New("identity_claims must be an array")
		}
		claims := make([]SubjectIdentityClaim, 0, len(inputs))
		for _, input := range inputs {
			claim, err := userAssertedClaim(input)
			if err != nil {
				return SubjectVersion{}, err
			}
			claims = append(claims, claim)
		}
		current.IdentityClaims = claims
		current.VerificationState = "pending_verification"
		current.VerificationScope = ""
	}
	if field, ok := fields["aliases"]; ok {
		var inputs []SubjectAliasInput
		if err := decodeStrictJSON(field, &inputs); err != nil || inputs == nil {
			return SubjectVersion{}, errors.New("aliases must be an array")
		}
		aliases := make([]SubjectAlias, 0, len(inputs))
		for _, input := range inputs {
			alias, err := userAssertedAlias(input)
			if err != nil {
				return SubjectVersion{}, err
			}
			aliases = append(aliases, alias)
		}
		current.Aliases = aliases
	}
	return current, nil
}

func parseCaseSubjectRolePatch(raw json.RawMessage, currentSubjectID string) (CaseRolePatch, error) {
	fields, err := decodeFlatObject(raw)
	if err != nil {
		return CaseRolePatch{}, err
	}
	if len(fields) == 0 {
		return CaseRolePatch{}, errors.New("binding patch must change at least one field")
	}
	for key := range fields {
		switch key {
		case "subject_id", "expected_new_subject_version", "role", "binding_state":
		default:
			return CaseRolePatch{}, fmt.Errorf("field %q is not allowed for a case_subject_role target", key)
		}
	}
	var out CaseRolePatch
	if field, ok := fields["subject_id"]; ok {
		value, err := decodeStringField(field, false)
		if err != nil || strings.TrimSpace(value) == "" {
			return CaseRolePatch{}, errors.New("subject_id must be a non-empty string")
		}
		if value == currentSubjectID {
			return CaseRolePatch{}, errors.New("subject_id already names the current binding subject")
		}
		out.SubjectID = &value
	}
	if field, ok := fields["expected_new_subject_version"]; ok {
		var value int64
		if err := decodeStrictJSON(field, &value); err != nil || value < 1 {
			return CaseRolePatch{}, errors.New("expected_new_subject_version must be a positive integer")
		}
		out.ExpectedNewSubjectVersion = &value
	}
	if (out.SubjectID == nil) != (out.ExpectedNewSubjectVersion == nil) {
		return CaseRolePatch{}, errors.New("a subject rebind requires subject_id and expected_new_subject_version together")
	}
	if field, ok := fields["role"]; ok {
		value, err := decodeStringField(field, false)
		if err != nil || !validSubjectRole(value) {
			return CaseRolePatch{}, errors.New("role is invalid")
		}
		out.Role = &value
	}
	if field, ok := fields["binding_state"]; ok {
		value, err := decodeStringField(field, false)
		if err != nil || (value != "active" && value != "inactive" && value != "pending_confirmation") {
			return CaseRolePatch{}, errors.New("binding_state is invalid")
		}
		out.BindingState = &value
	}
	return out, nil
}

func userAssertedClaim(input SubjectIdentityClaimInput) (SubjectIdentityClaim, error) {
	input.Value = strings.ToUpper(strings.TrimSpace(input.Value))
	if input.Namespace != "cn.gov.samr.uscc" || input.IdentifierType != "unified_social_credit_code" || !validUSCCFormat(input.Value) {
		return SubjectIdentityClaim{}, errors.New("identifier namespace, type or format is unsupported")
	}
	claim := SubjectIdentityClaim{Namespace: input.Namespace, IdentifierType: input.IdentifierType, Value: input.Value, VerificationState: "pending_verification", SourceKind: "user_asserted"}
	claim.PayloadHash = hashSubjectClaim(claim)
	return claim, nil
}

func invalidateSubjectIdentityVerification(version *SubjectVersion) {
	version.VerificationState = "pending_verification"
	version.VerificationScope = ""
	for i := range version.IdentityClaims {
		claim := &version.IdentityClaims[i]
		claim.VerificationState = "pending_verification"
		claim.VerificationScope = ""
		claim.SourceKind = "authorized_correction"
		claim.SourceTime = nil
		claim.SourceCatalogID = ""
		claim.SourceSnapshotID = ""
		claim.SourceDigest = ""
		claim.SourceValidFrom = nil
		claim.SourceValidUntil = nil
	}
}

func userAssertedAlias(input SubjectAliasInput) (SubjectAlias, error) {
	value := strings.TrimSpace(input.Value)
	switch input.Kind {
	case "legal", "trade", "former", "other":
	default:
		return SubjectAlias{}, errors.New("alias kind is invalid")
	}
	if value == "" || len([]rune(value)) > 200 {
		return SubjectAlias{}, errors.New("alias value is invalid")
	}
	return SubjectAlias{Value: value, Kind: input.Kind, SourceKind: "user_asserted"}, nil
}

func validUSCCFormat(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 18 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789ABCDEFGHJKLMNPQRTUWXY", char) {
			return false
		}
	}
	return true
}

func validSubjectRole(role string) bool {
	switch role {
	case SubjectRoleCustomer, SubjectRoleSupplier, SubjectRoleContractor, SubjectRoleServiceProvider, SubjectRoleOther:
		return true
	default:
		return false
	}
}

func projectSubjectVersion(version SubjectVersion, revealIdentifiers bool) SubjectVersionProjection {
	identityClaims := make([]SubjectIdentityClaimProjection, 0, len(version.IdentityClaims))
	for _, claim := range version.IdentityClaims {
		identityClaims = append(identityClaims, projectIdentityClaim(claim, revealIdentifiers))
	}
	aliases := append([]SubjectAlias(nil), version.Aliases...)
	return SubjectVersionProjection{
		SubjectID: version.SubjectID, Version: version.Version, DisplayName: version.DisplayName,
		LegalName: nullableSubjectName(version.LegalName), VerificationState: version.VerificationState,
		VerificationScope: version.VerificationScope, SourceKind: version.SourceKind, BasisSummary: version.BasisSummary,
		PayloadHash: version.PayloadHash, IdentityClaims: identityClaims, Aliases: aliases,
		CreatedBy: TrustedPrincipalProjection{UserID: version.CreatedByUser, Issuer: version.CreatedByIssuer, Subject: version.CreatedBySubject},
		CreatedAt: version.CreatedAt,
	}
}

// projectSubjectVersionForUser keeps free-form correction rationale scoped to
// the case where it was submitted. Subject versions are shared across cases,
// so a user who can read a different binding must not inherit another case's
// private basis note through the version projection.
func projectSubjectVersionForUser(st state, user accounts.User, version SubjectVersion, revealIdentifiers bool) SubjectVersionProjection {
	projected := projectSubjectVersion(version, revealIdentifiers)
	if version.SourceKind != "authorized_correction" || version.Version < 2 {
		return projected
	}
	for _, request := range st.SubjectChangeRequests {
		if request.TargetKind != "subject" || request.TargetSubjectID != version.SubjectID || request.ExpectedSubjectVersion+1 != version.Version {
			continue
		}
		approved := false
		for _, decision := range st.SubjectChangeDecisions {
			if decision.RequestID == request.RequestID && decision.Decision == "approve" {
				approved = true
				break
			}
		}
		if !approved {
			continue
		}
		if source, ok := st.caseByID(request.SourceCaseID); ok && caseReadAllowed(source, user) {
			return projected
		}
		projected.BasisSummary = "经审批更正"
		return projected
	}
	return projected
}

func projectIdentityClaim(claim SubjectIdentityClaim, reveal bool) SubjectIdentityClaimProjection {
	projected := SubjectIdentityClaimProjection{
		Namespace: claim.Namespace, IdentifierType: claim.IdentifierType,
		VerificationState: claim.VerificationState, VerificationScope: claim.VerificationScope,
		SourceKind: claim.SourceKind, SourceTime: claim.SourceTime, PayloadHash: claimPayloadHash(claim),
	}
	if reveal {
		projected.Value = claim.Value
	} else {
		projected.MaskedValue = maskUSCC(claim.Value)
	}
	return projected
}

func projectSubjectSummary(st state, user accounts.User, subject Subject, version SubjectVersion) SubjectSummary {
	projected := projectSubjectVersionForUser(st, user, version, false)
	return SubjectSummary{
		SubjectID: subject.ID, CurrentVersion: subject.CurrentVersion, DisplayName: version.DisplayName,
		LegalName: nullableSubjectName(version.LegalName), VerificationState: version.VerificationState,
		VerificationScope: version.VerificationScope, Aliases: projected.Aliases,
		Identifiers: projected.IdentityClaims,
		CreatedBy:   TrustedPrincipalProjection{UserID: subject.CreatedByUser, Issuer: subject.CreatedByIssuer, Subject: subject.CreatedBySubject},
		CreatedAt:   subject.CreatedAt,
	}
}

func maskUSCC(value string) string {
	canonical := strings.ToUpper(strings.TrimSpace(value))
	if len(canonical) != 18 {
		return ""
	}
	return canonical[:4] + "••••••••••" + canonical[14:]
}

func nullableSubjectName(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func claimPayloadHash(claim SubjectIdentityClaim) string {
	if claim.PayloadHash != "" {
		return claim.PayloadHash
	}
	return hashSubjectClaim(claim)
}

func hashSubjectClaim(claim SubjectIdentityClaim) string {
	canonical, _ := json.Marshal(struct {
		Namespace      string `json:"namespace"`
		IdentifierType string `json:"identifier_type"`
		Value          string `json:"value"`
	}{claim.Namespace, claim.IdentifierType, claim.Value})
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:])
}

func partyContextForCase(st state, caseID string) ([]PartyContextItem, string, error) {
	entities := make(map[string]Subject, len(st.Subjects))
	for _, subject := range st.Subjects {
		entities[subject.ID] = subject
	}
	versions := make(map[string]SubjectVersion, len(st.SubjectVersions))
	for _, version := range st.SubjectVersions {
		versions[subjectVersionKey(version.SubjectID, version.Version)] = version
	}
	currentBindings := make(map[string]CaseSubjectRoleVersion)
	for _, head := range st.CaseSubjectRoles {
		if head.CaseID != caseID {
			continue
		}
		binding := caseRoleCurrent(st, head)
		if binding.BindingID == "" || binding.Version != head.CurrentVersion || binding.TenantID != head.TenantID || binding.OrgID != head.OrgID || binding.CaseID != head.CaseID {
			return nil, "", fmt.Errorf("current subject binding %s has a missing or mismatched head version", head.BindingID)
		}
		currentBindings[head.BindingID] = binding
	}
	items := make([]PartyContextItem, 0, len(currentBindings))
	for bindingID, binding := range currentBindings {
		if binding.BindingState == "inactive" {
			continue
		}
		if _, ok := entities[binding.SubjectID]; !ok {
			return nil, "", fmt.Errorf("current subject binding %s references a missing subject", bindingID)
		}
		subjectVersion, ok := versions[subjectVersionKey(binding.SubjectID, binding.SubjectVersion)]
		if !ok {
			return nil, "", fmt.Errorf("current subject binding %s references a missing subject version", bindingID)
		}
		item := PartyContextItem{
			BindingID: binding.BindingID, BindingVersion: binding.Version, Role: binding.Role,
			BindingState: binding.BindingState, SubjectID: binding.SubjectID,
			SubjectVersion: binding.SubjectVersion, DisplayName: subjectVersion.DisplayName,
			VerificationState: subjectVersion.VerificationState, VerificationScope: subjectVersion.VerificationScope,
		}
		for _, claim := range subjectVersion.IdentityClaims {
			if claim.VerificationState == "verified" && claim.VerificationScope == "synthetic_controlled" {
				item.SourceCatalogID = claim.SourceCatalogID
				item.SourceSnapshotID = claim.SourceSnapshotID
				item.SourceDigest = claim.SourceDigest
				item.SourceTime = claim.SourceTime
				item.SourceValidFrom = claim.SourceValidFrom
				item.SourceValidUntil = claim.SourceValidUntil
				break
			}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].BindingID < items[j].BindingID })
	if len(items) == 0 {
		return nil, "", nil
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	return items, hex.EncodeToString(digest[:]), nil
}

func subjectContextChangedSinceRun(st state, caseID string, frozen RunSnapshot) bool {
	_, currentHash, err := partyContextForCase(st, caseID)
	return err != nil || currentHash != frozen.PartyContextHash
}

func subjectVersionKey(subjectID string, version int64) string {
	return fmt.Sprintf("%s:%d", subjectID, version)
}

func subjectVersionAt(versions []SubjectVersion, subjectID string, version int64) (SubjectVersion, bool) {
	for _, item := range versions {
		if item.SubjectID == subjectID && item.Version == version {
			return item, true
		}
	}
	return SubjectVersion{}, false
}

func decodeFlatObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, errors.New("JSON object is required")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, errors.New("JSON object is required")
	}
	return fields, nil
}

func decodeStringField(raw json.RawMessage, allowNull bool) (string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if allowNull {
			return "", nil
		}
		return "", errors.New("null is not allowed")
	}
	var value string
	if err := decodeStrictJSON(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func decodeStrictJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}
