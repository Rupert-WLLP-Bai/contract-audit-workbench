package model

import (
	"encoding/json"
	"time"
)

const (
	SubjectRoleCustomer        = "customer"
	SubjectRoleSupplier        = "supplier"
	SubjectRoleContractor      = "contractor"
	SubjectRoleServiceProvider = "service_provider"
	SubjectRoleOther           = "other"
	SyntheticSubjectSourceID   = "synthetic.uscc.demo"
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

type SubjectSourceSnapshotListResult struct {
	Items      []SubjectSourceSnapshotResult `json:"items"`
	NextCursor *string                       `json:"next_cursor"`
}

type SubjectSourceSnapshotDetail struct {
	SubjectSourceSnapshotResult
	CaseID string `json:"case_id"`
}

type CaseRolePatch struct {
	SubjectID                 *string
	ExpectedNewSubjectVersion *int64
	Role                      *string
	BindingState              *string
}

// SubjectCollections contains only Subject-owned collections. Case,
// Document and authorization data stay outside the model.
type SubjectCollections struct {
	Subjects                []Subject
	SubjectVersions         []SubjectVersion
	CaseSubjectRoles        []CaseSubjectRole
	CaseSubjectRoleVersions []CaseSubjectRoleVersion
}

type SubjectCreateInput struct {
	DisplayName string                      `json:"display_name"`
	LegalName   string                      `json:"legal_name,omitempty"`
	Identifiers []SubjectIdentityClaimInput `json:"identifiers,omitempty"`
	Aliases     []SubjectAliasInput         `json:"aliases,omitempty"`
}

type LegacyBindingInput struct {
	Field                  string `json:"field"`
	Value                  string `json:"value"`
	ExpectedProfileVersion int    `json:"expected_profile_version"`
}

type CaseSubjectCreateInput struct {
	Role          string              `json:"role"`
	Subject       SubjectCreateInput  `json:"subject"`
	LegacyBinding *LegacyBindingInput `json:"legacy_binding,omitempty"`
}

type CreateSubjectResult struct {
	SubjectID      string                   `json:"subject_id"`
	CurrentVersion int64                    `json:"current_version"`
	SubjectVersion SubjectVersionProjection `json:"subject_version"`
	CreatedAt      time.Time                `json:"created_at"`
}

type CreateCaseSubjectResult struct {
	BindingID         string                   `json:"binding_id"`
	BindingVersion    int64                    `json:"binding_version"`
	SubjectID         string                   `json:"subject_id"`
	SubjectVersion    int64                    `json:"subject_version"`
	Role              string                   `json:"role"`
	DisplayName       string                   `json:"display_name"`
	VerificationState string                   `json:"verification_state"`
	SourceKind        string                   `json:"source_kind"`
	LegacyLabel       *string                  `json:"legacy_label"`
	BindingState      string                   `json:"binding_state"`
	Subject           SubjectVersionProjection `json:"subject"`
}

type BindExistingSubjectInput struct {
	Role                   string `json:"role"`
	SubjectID              string `json:"subject_id"`
	ExpectedSubjectVersion int64  `json:"expected_subject_version"`
}

type BindExistingSubjectResult struct {
	BindingID         string    `json:"binding_id"`
	BindingVersion    int64     `json:"binding_version"`
	SubjectID         string    `json:"subject_id"`
	SubjectVersion    int64     `json:"subject_version"`
	Role              string    `json:"role"`
	DisplayName       string    `json:"display_name"`
	VerificationState string    `json:"verification_state"`
	BindingState      string    `json:"binding_state"`
	CreatedAt         time.Time `json:"created_at"`
	SourceKind        string    `json:"source_kind"`
}

type SubjectListResult struct {
	Items      []SubjectSummary `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

type SubjectDetailResult struct {
	SubjectID      string                           `json:"subject_id"`
	CurrentVersion int64                            `json:"current_version"`
	Current        SubjectVersionProjection         `json:"current"`
	Identifiers    []SubjectIdentityClaimProjection `json:"identifiers"`
}

type SubjectChangeEventSummary struct {
	EventID   string                     `json:"event_id"`
	Kind      string                     `json:"kind"`
	RequestID string                     `json:"request_id"`
	CaseID    string                     `json:"case_id"`
	Actor     TrustedPrincipalProjection `json:"actor"`
	CreatedAt time.Time                  `json:"created_at"`
}

type SubjectHistoryResult struct {
	Versions []SubjectVersionProjection  `json:"versions"`
	Events   []SubjectChangeEventSummary `json:"events"`
}

type SubjectCaseLinksResult struct {
	Items        []SubjectCaseLinkProjection `json:"items"`
	VisibleCount int                         `json:"visible_count"`
	NextCursor   *string                     `json:"next_cursor"`
}

type SubjectCaseLinkProjection struct {
	BindingID      string                     `json:"binding_id"`
	BindingVersion int64                      `json:"binding_version"`
	CaseID         string                     `json:"case_id"`
	CaseTitle      string                     `json:"case_title"`
	BusinessStatus string                     `json:"business_status"`
	Role           string                     `json:"role"`
	BindingState   string                     `json:"binding_state"`
	SourceKind     string                     `json:"source_kind"`
	LegacyLabel    *string                    `json:"legacy_label"`
	CreatedBy      TrustedPrincipalProjection `json:"created_by"`
	Subject        SubjectVersionProjection   `json:"subject"`
	CreatedAt      time.Time                  `json:"created_at"`
}

type SubjectChangeCreateInput struct {
	Target                 SubjectChangeTarget `json:"target"`
	ExpectedSubjectVersion int64               `json:"expected_subject_version"`
	ExpectedBindingVersion *int64              `json:"expected_binding_version,omitempty"`
	Patch                  json.RawMessage     `json:"patch"`
	BasisNote              string              `json:"basis_note"`
	BasisRefs              json.RawMessage     `json:"basis_refs"`
}

type SubjectChangeCreateResult struct {
	RequestID              string  `json:"request_id"`
	TargetKind             string  `json:"target_kind"`
	ExpectedSubjectVersion int64   `json:"expected_subject_version"`
	ExpectedBindingVersion *int64  `json:"expected_binding_version"`
	PayloadHash            string  `json:"payload_hash"`
	State                  string  `json:"state"`
	ProposedByDisplayName  string  `json:"proposed_by_display_name"`
	CanDecide              bool    `json:"can_decide"`
	DecisionBlockedReason  *string `json:"decision_blocked_reason"`
}

type SubjectChangeSummary struct {
	RequestID              string                     `json:"request_id"`
	TargetKind             string                     `json:"target_kind"`
	TargetSubjectID        string                     `json:"target_subject_id"`
	TargetBindingID        *string                    `json:"target_binding_id"`
	ExpectedSubjectVersion int64                      `json:"expected_subject_version"`
	ExpectedBindingVersion *int64                     `json:"expected_binding_version"`
	PayloadHash            string                     `json:"payload_hash"`
	State                  string                     `json:"state"`
	ProposedBy             TrustedPrincipalProjection `json:"proposed_by"`
	ProposedByDisplayName  string                     `json:"proposed_by_display_name"`
	CanDecide              bool                       `json:"can_decide"`
	DecisionBlockedReason  *string                    `json:"decision_blocked_reason"`
	CreatedAt              time.Time                  `json:"created_at"`
}

type SubjectChangeListResult struct {
	Items      []SubjectChangeSummary `json:"items"`
	NextCursor *string                `json:"next_cursor"`
}

type SubjectChangeDetailResult struct {
	Summary               SubjectChangeSummary `json:"summary"`
	Before                any                  `json:"before"`
	After                 any                  `json:"after"`
	BasisNote             string               `json:"basis_note"`
	BasisRefs             json.RawMessage      `json:"basis_refs"`
	ProposedByDisplayName string               `json:"proposed_by_display_name"`
	CanDecide             bool                 `json:"can_decide"`
	DecisionBlockedReason *string              `json:"decision_blocked_reason"`
}

type SubjectChangeDecisionInput struct {
	Decision               string `json:"decision"`
	ExpectedSubjectVersion int64  `json:"expected_subject_version"`
	ExpectedBindingVersion *int64 `json:"expected_binding_version,omitempty"`
	ExpectedPayloadHash    string `json:"expected_payload_hash"`
	ExpectedImpactETag     string `json:"expected_impact_etag"`
	Reason                 string `json:"reason"`
}

type SubjectChangeDecisionResult struct {
	RequestID               string `json:"request_id"`
	Decision                string `json:"decision"`
	AppliedSubjectVersion   *int64 `json:"applied_subject_version"`
	BindingVersionsAppended int    `json:"binding_versions_appended"`
}

type SubjectChangeDecisionResultWithReplay struct {
	Result   SubjectChangeDecisionResult
	Replayed bool
}

type SubjectChangeImpactResult struct {
	RequestID             string                        `json:"request_id"`
	TargetVersion         int64                         `json:"target_version"`
	PayloadHash           string                        `json:"payload_hash"`
	Before                any                           `json:"before"`
	After                 any                           `json:"after"`
	BasisRefs             json.RawMessage               `json:"basis_refs"`
	ImpactETag            string                        `json:"impact_etag"`
	VisibleAffectedCount  int                           `json:"visible_affected_count"`
	AuthorizedImpactCases []SubjectImpactCaseProjection `json:"authorized_impact_cases"`
	ProposedByDisplayName string                        `json:"proposed_by_display_name"`
	CanDecide             bool                          `json:"can_decide"`
	DecisionBlockedReason *string                       `json:"decision_blocked_reason"`
}

type SubjectImpactCaseProjection struct {
	CaseID string `json:"case_id"`
	Title  string `json:"case_title"`
	Status string `json:"business_status"`
}

type SubjectAfterPayload struct {
	SubjectVersion SubjectVersion `json:"subject_version"`
}

type BindingAfterPayload struct {
	SubjectID      string `json:"subject_id"`
	SubjectVersion int64  `json:"subject_version"`
	Role           string `json:"role"`
	BindingState   string `json:"binding_state"`
	SourceKind     string `json:"source_kind"`
	LegacyLabel    string `json:"legacy_label,omitempty"`
}
