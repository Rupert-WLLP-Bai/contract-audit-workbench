// File metadata (manifest mirror):
// Purpose: PartyContextItem freezes the subject and binding version used by one review. Synthetic verification provenance is carried explicitly so reports cannot collapse it into an official… Related `store` operations include applySubjectPatch, parseCaseSubjectRolePatch, userAssertedClaim, invalidateSubjectIdentityVerification.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"contractaudit/internal/accounts"
	subjectApp "contractaudit/internal/subject/app"
	subjectModel "contractaudit/internal/subject/model"
)

func applySubjectPatch(current SubjectVersion, raw json.RawMessage) (SubjectVersion, error) {
	return subjectModel.ApplySubjectPatch(current, raw)
}

func parseCaseSubjectRolePatch(raw json.RawMessage, currentSubjectID string) (CaseRolePatch, error) {
	return subjectModel.ParseCaseSubjectRolePatch(raw, currentSubjectID)
}

func userAssertedClaim(input SubjectIdentityClaimInput) (SubjectIdentityClaim, error) {
	return subjectModel.UserAssertedClaim(input)
}

func invalidateSubjectIdentityVerification(version *SubjectVersion) {
	subjectModel.InvalidateSubjectIdentityVerification(version)
}

func userAssertedAlias(input SubjectAliasInput) (SubjectAlias, error) {
	return subjectModel.UserAssertedAlias(input)
}

func validUSCCFormat(value string) bool {
	return subjectModel.ValidUSCCFormat(value)
}

func validSubjectRole(role string) bool {
	return subjectModel.ValidSubjectRole(role)
}

func projectSubjectVersion(version SubjectVersion, revealIdentifiers bool) SubjectVersionProjection {
	return subjectModel.ProjectSubjectVersion(version, revealIdentifiers)
}

// projectSubjectVersionForUser keeps free-form correction rationale scoped to
// the case where it was submitted. Subject versions are shared across cases,
// so a user who can read a different binding must not inherit another case's
// private basis note through the version projection.
func projectSubjectVersionForUser(st state, user accounts.User, version SubjectVersion, revealIdentifiers bool) SubjectVersionProjection {
	return subjectApp.ProjectSubjectVersionForActor(subjectCommandSnapshot(&st), subjectCommandActor(user), subjectReadFacts(&st, user, false), version, revealIdentifiers)
}

func projectIdentityClaim(claim SubjectIdentityClaim, reveal bool) SubjectIdentityClaimProjection {
	return subjectModel.ProjectIdentityClaim(claim, reveal)
}

func projectSubjectSummary(st state, user accounts.User, subject Subject, version SubjectVersion) SubjectSummary {
	return subjectApp.ProjectSubjectSummary(subjectCommandSnapshot(&st), subjectCommandActor(user), subjectReadFacts(&st, user, false), subject, version)
}

func maskUSCC(value string) string {
	return subjectModel.MaskUSCC(value)
}

func nullableSubjectName(value string) *string {
	return subjectModel.NullableSubjectName(value)
}

func claimPayloadHash(claim SubjectIdentityClaim) string {
	return subjectModel.ClaimPayloadHash(claim)
}

func hashSubjectClaim(claim SubjectIdentityClaim) string {
	return subjectModel.HashSubjectClaim(claim)
}

func partyContextForCase(st state, caseID string) ([]PartyContextItem, string, error) {
	return subjectModel.PartyContextForCase(subjectModel.SubjectCollections{
		Subjects: st.Subjects, SubjectVersions: st.SubjectVersions,
		CaseSubjectRoles: st.CaseSubjectRoles, CaseSubjectRoleVersions: st.CaseSubjectRoleVersions,
	}, caseID)
}

func subjectContextChangedSinceRun(st state, caseID string, frozen RunSnapshot) bool {
	_, currentHash, err := partyContextForCase(st, caseID)
	return err != nil || currentHash != frozen.PartyContextHash
}

func subjectVersionKey(subjectID string, version int64) string {
	return subjectModel.SubjectVersionKey(subjectID, version)
}

func subjectVersionAt(versions []SubjectVersion, subjectID string, version int64) (SubjectVersion, bool) {
	return subjectModel.SubjectVersionAt(versions, subjectID, version)
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
