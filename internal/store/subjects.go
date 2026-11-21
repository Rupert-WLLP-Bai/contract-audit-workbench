// File metadata (manifest mirror):
// Purpose: Forwards compatible Subject projection, patch, hash and frozen-party helpers to owned rules while retaining the shared strict JSON decoder.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-21
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

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

func projectSubjectVersion(version SubjectVersion, revealIdentifiers bool) SubjectVersionProjection {
	return subjectModel.ProjectSubjectVersion(version, revealIdentifiers)
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
