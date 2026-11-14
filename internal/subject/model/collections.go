// File metadata (manifest mirror):
// Purpose: Defines pure exact-version Subject and binding lookup, affected-binding ordering, correction payload projection and frozen party-context rules.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

func PartyContextForCase(collections SubjectCollections, caseID string) ([]PartyContextItem, string, error) {
	entities := make(map[string]Subject, len(collections.Subjects))
	for _, subject := range collections.Subjects {
		entities[subject.ID] = subject
	}
	versions := make(map[string]SubjectVersion, len(collections.SubjectVersions))
	for _, version := range collections.SubjectVersions {
		versions[SubjectVersionKey(version.SubjectID, version.Version)] = version
	}
	currentBindings := make(map[string]CaseSubjectRoleVersion)
	for _, head := range collections.CaseSubjectRoles {
		if head.CaseID != caseID {
			continue
		}
		binding := CaseRoleCurrent(collections.CaseSubjectRoleVersions, head)
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
		subjectVersion, ok := versions[SubjectVersionKey(binding.SubjectID, binding.SubjectVersion)]
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

func SubjectVersionKey(subjectID string, version int64) string {
	return fmt.Sprintf("%s:%d", subjectID, version)
}

func SubjectVersionAt(versions []SubjectVersion, subjectID string, version int64) (SubjectVersion, bool) {
	for _, item := range versions {
		if item.SubjectID == subjectID && item.Version == version {
			return item, true
		}
	}
	return SubjectVersion{}, false
}

func CaseRoleCurrent(versions []CaseSubjectRoleVersion, head CaseSubjectRole) CaseSubjectRoleVersion {
	for _, item := range versions {
		if item.TenantID == head.TenantID && item.OrgID == head.OrgID && item.CaseID == head.CaseID && item.BindingID == head.BindingID && item.Version == head.CurrentVersion {
			return item
		}
	}
	return CaseSubjectRoleVersion{}
}

func (v CaseSubjectRoleVersion) HasSubject(subjectID string) bool {
	return v.SubjectID == subjectID && v.BindingState != "inactive"
}

func SubjectBinding(versions []CaseSubjectRoleVersion, heads []CaseSubjectRole, caseID, subjectID string, requireCurrent bool) (CaseSubjectRoleVersion, bool) {
	var found CaseSubjectRoleVersion
	for _, item := range versions {
		if item.CaseID != caseID || item.SubjectID != subjectID || item.BindingState == "inactive" {
			continue
		}
		if !requireCurrent {
			return item, true
		}
		for _, head := range heads {
			if head.CaseID == caseID && head.BindingID == item.BindingID && head.CurrentVersion == item.Version {
				found = item
				break
			}
		}
	}
	return found, found.BindingID != ""
}

func SubjectHasAnyBinding(versions []CaseSubjectRoleVersion, subjectID string) bool {
	for _, binding := range versions {
		if binding.SubjectID == subjectID {
			return true
		}
	}
	return false
}

func CurrentSubjectBinding(heads []CaseSubjectRole, versions []CaseSubjectRoleVersion, bindingID string) (CaseSubjectRole, CaseSubjectRoleVersion, bool) {
	for _, head := range heads {
		if head.BindingID == bindingID {
			current := CaseRoleCurrent(versions, head)
			return head, current, current.BindingID != ""
		}
	}
	return CaseSubjectRole{}, CaseSubjectRoleVersion{}, false
}

func CaseHasSubject(heads []CaseSubjectRole, versions []CaseSubjectRoleVersion, caseID, subjectID string) bool {
	for _, head := range heads {
		if head.CaseID != caseID {
			continue
		}
		version := CaseRoleCurrent(versions, head)
		if version.SubjectID == subjectID && version.BindingState != "inactive" {
			return true
		}
	}
	return false
}

func AffectedSubjectBindings(heads []CaseSubjectRole, versions []CaseSubjectRoleVersion, tenantID, subjectID, kind, bindingID string) []CaseSubjectRoleVersion {
	items := make([]CaseSubjectRoleVersion, 0)
	for _, head := range heads {
		if head.TenantID != tenantID {
			continue
		}
		if kind == "case_subject_role" && head.BindingID != bindingID {
			continue
		}
		if kind == "subject" && head.TenantID == "" {
			continue
		}
		version := CaseRoleCurrent(versions, head)
		if (kind == "subject" && (version.BindingState == "inactive" || version.SubjectID != subjectID)) ||
			(kind == "case_subject_role" && version.BindingID != bindingID) {
			continue
		}
		items = append(items, version)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CaseID != items[j].CaseID {
			return items[i].CaseID < items[j].CaseID
		}
		return items[i].BindingID < items[j].BindingID
	})
	return items
}

func AffectedSubjectCaseIDs(collections SubjectCollections, request SubjectChangeRequest) []string {
	bindings := AffectedSubjectBindings(collections.CaseSubjectRoles, collections.CaseSubjectRoleVersions,
		request.TenantID, request.TargetSubjectID, request.TargetKind, request.TargetBindingID)
	ids := []string{request.SourceCaseID}
	for _, binding := range bindings {
		ids = append(ids, binding.CaseID)
	}
	sort.Strings(ids)
	return ids
}

func FindSubjectChangeRequest(requests []SubjectChangeRequest, tenantID, caseID, requestID string) (SubjectChangeRequest, bool) {
	for _, request := range requests {
		if request.TenantID == tenantID && request.SourceCaseID == caseID && request.RequestID == requestID {
			return request, true
		}
	}
	return SubjectChangeRequest{}, false
}

func SubjectBindingVersionAt(versions []CaseSubjectRoleVersion, head CaseSubjectRole, version int64) (CaseSubjectRoleVersion, bool) {
	for _, item := range versions {
		if item.TenantID == head.TenantID && item.OrgID == head.OrgID && item.CaseID == head.CaseID && item.BindingID == head.BindingID && item.Version == version {
			return item, true
		}
	}
	return CaseSubjectRoleVersion{}, false
}

func ProjectSubjectBinding(collections SubjectCollections, head CaseSubjectRole, binding CaseSubjectRoleVersion, full bool) CaseSubjectBindingProjection {
	version, _ := SubjectVersionAt(collections.SubjectVersions, binding.SubjectID, binding.SubjectVersion)
	var label *string
	if binding.LegacyLabel != "" {
		value := binding.LegacyLabel
		label = &value
	}
	return CaseSubjectBindingProjection{BindingID: head.BindingID, BindingVersion: binding.Version, CaseID: head.CaseID, Role: binding.Role,
		BindingState: binding.BindingState, SourceKind: binding.SourceKind, LegacyLabel: label, Subject: ProjectSubjectVersion(version, full),
		CreatedBy: TrustedPrincipalProjection{UserID: binding.CreatedByUser, Issuer: binding.CreatedByIssuer, Subject: binding.CreatedBySubject}, CreatedAt: binding.CreatedAt}
}

func SubjectChangePayloads(collections SubjectCollections, request SubjectChangeRequest, full bool) (any, any, error) {
	if request.TargetKind == "subject" {
		before, ok := SubjectVersionAt(collections.SubjectVersions, request.TargetSubjectID, request.ExpectedSubjectVersion)
		if !ok {
			return nil, nil, errors.New("proposal before subject version is missing")
		}
		var payload SubjectAfterPayload
		if err := json.Unmarshal(request.CanonicalPayload, &payload); err != nil {
			return nil, nil, err
		}
		return ProjectSubjectVersion(before, full), ProjectSubjectVersion(payload.SubjectVersion, full), nil
	}
	head, currentBinding, ok := CurrentSubjectBinding(collections.CaseSubjectRoles, collections.CaseSubjectRoleVersions, request.TargetBindingID)
	if !ok {
		return nil, nil, errors.New("proposal binding is missing")
	}
	if request.ExpectedBindingVersion == nil {
		return nil, nil, errors.New("proposal expected binding version is missing")
	}
	binding, ok := SubjectBindingVersionAt(collections.CaseSubjectRoleVersions, head, *request.ExpectedBindingVersion)
	if !ok {
		return nil, nil, errors.New("proposal before binding version is missing")
	}
	before := ProjectSubjectBinding(collections, head, binding, full)
	var payload BindingAfterPayload
	if err := json.Unmarshal(request.CanonicalPayload, &payload); err != nil {
		return nil, nil, err
	}
	afterVersion, ok := SubjectVersionAt(collections.SubjectVersions, payload.SubjectID, payload.SubjectVersion)
	if !ok {
		return nil, nil, errors.New("proposal target subject version is missing")
	}
	_ = currentBinding
	var label *string
	if payload.LegacyLabel != "" {
		value := payload.LegacyLabel
		label = &value
	}
	after := CaseSubjectBindingProjection{BindingID: head.BindingID, BindingVersion: binding.Version + 1, CaseID: head.CaseID, Role: payload.Role,
		BindingState: payload.BindingState, SourceKind: payload.SourceKind, LegacyLabel: label, Subject: ProjectSubjectVersion(afterVersion, full),
		CreatedBy: request.ProposedBy, CreatedAt: time.Time{}}
	return before, after, nil
}

func MaskSubjectChangeProjection(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var result any
	if json.Unmarshal(encoded, &result) != nil {
		return value
	}
	var mask func(any)
	mask = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if rawClaims, ok := v["identity_claims"].([]any); ok {
				for _, rawClaim := range rawClaims {
					claim, ok := rawClaim.(map[string]any)
					if !ok || claim["namespace"] != "cn.gov.samr.uscc" || claim["identifier_type"] != "unified_social_credit_code" {
						continue
					}
					if raw, ok := claim["value"].(string); ok {
						claim["masked_value"] = MaskUSCC(raw)
						delete(claim, "value")
					}
				}
			}
			for _, child := range v {
				mask(child)
			}
		case []any:
			for _, child := range v {
				mask(child)
			}
		}
	}
	mask(result)
	return result
}

func CloneSubjectSourceManifest(manifest *SubjectSourceManifest) *SubjectSourceManifest {
	if manifest == nil {
		return nil
	}
	copy := *manifest
	copy.Records = append([]SubjectSourceRecord(nil), manifest.Records...)
	for i := range copy.Records {
		if copy.Records[i].ValidUntil != nil {
			value := *copy.Records[i].ValidUntil
			copy.Records[i].ValidUntil = &value
		}
	}
	return &copy
}
