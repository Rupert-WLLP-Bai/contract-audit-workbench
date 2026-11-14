// File metadata (manifest mirror):
// Purpose: Defines Subject input, patch, identifier, alias, projection and canonical-hash rules without Store or persistence dependencies.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

func NormalizeSubjectCreateInput(input SubjectCreateInput) (SubjectCreateInput, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" || len([]rune(input.DisplayName)) > 200 || len([]rune(input.LegalName)) > 200 || len(input.Identifiers) > 20 || len(input.Aliases) > 50 {
		return SubjectCreateInput{}, errors.New("主体名称或材料数量无效")
	}
	input.LegalName = strings.TrimSpace(input.LegalName)
	claims := make([]SubjectIdentityClaimInput, 0, len(input.Identifiers))
	for _, item := range input.Identifiers {
		claim, err := UserAssertedClaim(item)
		if err != nil {
			return SubjectCreateInput{}, err
		}
		claims = append(claims, SubjectIdentityClaimInput{Namespace: claim.Namespace, IdentifierType: claim.IdentifierType, Value: claim.Value})
	}
	aliases := make([]SubjectAliasInput, 0, len(input.Aliases))
	for _, item := range input.Aliases {
		alias, err := UserAssertedAlias(item)
		if err != nil {
			return SubjectCreateInput{}, err
		}
		aliases = append(aliases, SubjectAliasInput{Value: alias.Value, Kind: alias.Kind})
	}
	input.Identifiers, input.Aliases = claims, aliases
	return input, nil
}

func NewSubjectAggregate(tenantID, subjectID string, principal TrustedPrincipalProjection, input SubjectCreateInput, now time.Time) (Subject, SubjectVersion, error) {
	version := SubjectVersion{TenantID: tenantID, SubjectID: subjectID, Version: 1, DisplayName: input.DisplayName, LegalName: input.LegalName,
		VerificationState: "pending_verification", SourceKind: "user_asserted", BasisSummary: "用户提交，身份待核验", Aliases: []SubjectAlias{},
		CreatedByUser: principal.UserID, CreatedByIssuer: principal.Issuer, CreatedBySubject: principal.Subject, CreatedAt: now}
	for _, item := range input.Identifiers {
		claim, err := UserAssertedClaim(item)
		if err != nil {
			return Subject{}, SubjectVersion{}, err
		}
		version.IdentityClaims = append(version.IdentityClaims, claim)
	}
	if version.IdentityClaims == nil {
		version.IdentityClaims = []SubjectIdentityClaim{}
	}
	for _, item := range input.Aliases {
		alias, err := UserAssertedAlias(item)
		if err != nil {
			return Subject{}, SubjectVersion{}, err
		}
		version.Aliases = append(version.Aliases, alias)
	}
	version.PayloadHash = HashSubjectVersion(version)
	subject := Subject{TenantID: tenantID, ID: subjectID, CurrentVersion: 1, Status: "pending_verification", CreatedByUser: principal.UserID,
		CreatedByIssuer: principal.Issuer, CreatedBySubject: principal.Subject, CreatedAt: now}
	return subject, version, nil
}

func ApplySubjectPatch(current SubjectVersion, raw json.RawMessage) (SubjectVersion, error) {
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
		InvalidateSubjectIdentityVerification(&current)
	}
	if field, ok := fields["legal_name"]; ok {
		value, err := decodeStringField(field, false)
		if err != nil {
			return SubjectVersion{}, errors.New("legal_name must be a string; use an empty string to clear")
		}
		current.LegalName = value
		InvalidateSubjectIdentityVerification(&current)
	}
	if field, ok := fields["identity_claims"]; ok {
		var inputs []SubjectIdentityClaimInput
		if err := decodeStrictJSON(field, &inputs); err != nil || inputs == nil {
			return SubjectVersion{}, errors.New("identity_claims must be an array")
		}
		claims := make([]SubjectIdentityClaim, 0, len(inputs))
		for _, input := range inputs {
			claim, err := UserAssertedClaim(input)
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
			alias, err := UserAssertedAlias(input)
			if err != nil {
				return SubjectVersion{}, err
			}
			aliases = append(aliases, alias)
		}
		current.Aliases = aliases
	}
	return current, nil
}

func ParseCaseSubjectRolePatch(raw json.RawMessage, currentSubjectID string) (CaseRolePatch, error) {
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
		if err != nil || !ValidSubjectRole(value) {
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

func UserAssertedClaim(input SubjectIdentityClaimInput) (SubjectIdentityClaim, error) {
	input.Value = strings.ToUpper(strings.TrimSpace(input.Value))
	if input.Namespace != "cn.gov.samr.uscc" || input.IdentifierType != "unified_social_credit_code" || !ValidUSCCFormat(input.Value) {
		return SubjectIdentityClaim{}, errors.New("identifier namespace, type or format is unsupported")
	}
	claim := SubjectIdentityClaim{Namespace: input.Namespace, IdentifierType: input.IdentifierType, Value: input.Value, VerificationState: "pending_verification", SourceKind: "user_asserted"}
	claim.PayloadHash = HashSubjectClaim(claim)
	return claim, nil
}

func InvalidateSubjectIdentityVerification(version *SubjectVersion) {
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

func UserAssertedAlias(input SubjectAliasInput) (SubjectAlias, error) {
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

func ValidUSCCFormat(value string) bool {
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

func ValidSubjectRole(role string) bool {
	switch role {
	case SubjectRoleCustomer, SubjectRoleSupplier, SubjectRoleContractor, SubjectRoleServiceProvider, SubjectRoleOther:
		return true
	default:
		return false
	}
}

func ProjectSubjectVersion(version SubjectVersion, revealIdentifiers bool) SubjectVersionProjection {
	identityClaims := make([]SubjectIdentityClaimProjection, 0, len(version.IdentityClaims))
	for _, claim := range version.IdentityClaims {
		identityClaims = append(identityClaims, ProjectIdentityClaim(claim, revealIdentifiers))
	}
	aliases := make([]SubjectAlias, len(version.Aliases))
	copy(aliases, version.Aliases)
	return SubjectVersionProjection{
		SubjectID: version.SubjectID, Version: version.Version, DisplayName: version.DisplayName,
		LegalName: NullableSubjectName(version.LegalName), VerificationState: version.VerificationState,
		VerificationScope: version.VerificationScope, SourceKind: version.SourceKind, BasisSummary: version.BasisSummary,
		PayloadHash: version.PayloadHash, IdentityClaims: identityClaims, Aliases: aliases,
		CreatedBy: TrustedPrincipalProjection{UserID: version.CreatedByUser, Issuer: version.CreatedByIssuer, Subject: version.CreatedBySubject},
		CreatedAt: version.CreatedAt,
	}
}

func ProjectIdentityClaim(claim SubjectIdentityClaim, reveal bool) SubjectIdentityClaimProjection {
	projected := SubjectIdentityClaimProjection{
		Namespace: claim.Namespace, IdentifierType: claim.IdentifierType,
		VerificationState: claim.VerificationState, VerificationScope: claim.VerificationScope,
		SourceKind: claim.SourceKind, SourceTime: claim.SourceTime, PayloadHash: ClaimPayloadHash(claim),
	}
	if reveal {
		projected.Value = claim.Value
	} else {
		projected.MaskedValue = MaskUSCC(claim.Value)
	}
	return projected
}

func MaskUSCC(value string) string {
	canonical := strings.ToUpper(strings.TrimSpace(value))
	if len(canonical) != 18 {
		return ""
	}
	return canonical[:4] + "••••••••••" + canonical[14:]
}

func NullableSubjectName(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func ClaimPayloadHash(claim SubjectIdentityClaim) string {
	if claim.PayloadHash != "" {
		return claim.PayloadHash
	}
	return HashSubjectClaim(claim)
}

func HashSubjectClaim(claim SubjectIdentityClaim) string {
	canonical, _ := json.Marshal(struct {
		Namespace      string `json:"namespace"`
		IdentifierType string `json:"identifier_type"`
		Value          string `json:"value"`
	}{claim.Namespace, claim.IdentifierType, claim.Value})
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:])
}

func HashSubjectVersion(version SubjectVersion) string {
	version.PayloadHash = ""
	version.CreatedByUser, version.CreatedByIssuer, version.CreatedBySubject = "", "", ""
	version.CreatedAt = time.Time{}
	encoded, _ := json.Marshal(version)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func CanonicalSubjectHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func SamePrincipal(a, b TrustedPrincipalProjection) bool {
	return a.UserID == b.UserID || (a.Issuer != "" && a.Issuer == b.Issuer && a.Subject == b.Subject)
}

func SameOptionalInt(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
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
