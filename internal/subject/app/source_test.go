// File metadata (manifest mirror):
// Purpose: Pins controlled-source proposal-only matching, ambiguity and version-content conflicts, and initialized results on later identifier failures.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"contractaudit/internal/subject/model"
)

type sourceTestClock struct {
	now         time.Time
	calls       []string
	snapshotErr error
	requestErr  error
}

func (c *sourceTestClock) Now() time.Time { c.calls = append(c.calls, "now"); return c.now }
func (c *sourceTestClock) NewSnapshotID() (string, error) {
	c.calls = append(c.calls, "snapshot")
	return "snap", c.snapshotErr
}
func (c *sourceTestClock) NewRequestID() (string, error) {
	c.calls = append(c.calls, "request")
	return "req", c.requestErr
}
func TestSourceExactMatchOnlyCreatesProposalAndAmbiguityOnlyReceipt(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := &sourceTestClock{now: now}
	actor := Actor{TenantID: "tenant", Principal: model.TrustedPrincipalProjection{UserID: "actor"}}
	caseID := CaseIdentity{TenantID: "tenant", OrgID: "org", CaseID: "case"}
	manifest := &model.SubjectSourceManifest{SourceID: model.SyntheticSubjectSourceID, SourceVersion: 1, VerificationScope: "synthetic_controlled", Records: []model.SubjectSourceRecord{{DisplayName: "企业", USCC: "91350100M000100Y43", SourceAsOf: now, ValidFrom: now}}}
	input := model.SubjectSourceSnapshotCreateInput{SourceID: manifest.SourceID, ExpectedSourceVersion: 1}
	delta, _, err := CreateSourceSnapshot(Snapshot{}, actor, caseID, input, manifest, "source-key", "hash", clock)
	if err != nil {
		t.Fatal(err)
	}
	snap := Snapshot{Sources: delta.Sources, Collections: model.SubjectCollections{Subjects: []model.Subject{{TenantID: "tenant", ID: "subject", CurrentVersion: 1, Status: "pending_verification", CreatedByUser: "actor"}}, SubjectVersions: []model.SubjectVersion{{TenantID: "tenant", SubjectID: "subject", Version: 1, DisplayName: "企业", IdentityClaims: []model.SubjectIdentityClaim{{Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: "91350100M000100Y43"}}}}, CaseSubjectRoles: []model.CaseSubjectRole{{TenantID: "tenant", OrgID: "org", CaseID: "case", BindingID: "binding", CurrentVersion: 1}}, CaseSubjectRoleVersions: []model.CaseSubjectRoleVersion{{TenantID: "tenant", OrgID: "org", CaseID: "case", BindingID: "binding", Version: 1, SubjectID: "subject", SubjectVersion: 1, BindingState: "active"}}}}
	prepared, err := PrepareSourceVerification(snap, actor, caseID, "snap")
	if err != nil {
		t.Fatal(err)
	}
	clock.calls = nil
	sentinel := errors.New("request id failure")
	clock.requestErr = sentinel
	_, partial, failed := VerifySource(snap, actor, caseID, prepared, model.SubjectSourceVerifyInput{SubjectID: "subject", ExpectedSubjectVersion: 1}, map[string]bool{"case": true}, "verify-key", "hash", clock)
	if !errors.Is(failed, sentinel) || partial.MatchOutcome != "exact_match" || partial.MatchedRecord == nil || partial.State != "not_proposed" {
		t.Fatalf("partial=%+v err=%v", partial, failed)
	}
	clock.requestErr = nil
	out, result, err := VerifySource(snap, actor, caseID, prepared, model.SubjectSourceVerifyInput{SubjectID: "subject", ExpectedSubjectVersion: 1}, map[string]bool{"case": true}, "verify-key", "hash", clock)
	if err != nil || result.MatchOutcome != "exact_match" || result.State != "pending_decision" || len(out.Requests) != 1 || len(out.Receipts) != 1 || out.Receipts[0].HTTPStatus != 202 || len(out.Subjects) != 0 || len(out.Versions) != 0 || len(out.Bindings) != 0 || len(out.BindingVersions) != 0 {
		t.Fatalf("delta=%+v result=%+v err=%v", out, result, err)
	}
	var after model.SubjectAfterPayload
	if err := json.Unmarshal(out.Requests[0].CanonicalPayload, &after); err != nil || after.SubjectVersion.VerificationState != "verified" || snap.Collections.Subjects[0].CurrentVersion != 1 {
		t.Fatalf("candidate=%+v err=%v", after, err)
	}
	prepared.Manifest.Records = append(prepared.Manifest.Records, prepared.Manifest.Records[0])
	out, result, err = VerifySource(snap, actor, caseID, prepared, model.SubjectSourceVerifyInput{SubjectID: "subject", ExpectedSubjectVersion: 1}, map[string]bool{"case": true}, "ambiguous-key", "hash", clock)
	if err != nil || result.MatchOutcome != "ambiguous" || len(out.Requests) != 0 || len(out.Receipts) != 1 || out.Receipts[0].HTTPStatus != 200 {
		t.Fatalf("ambiguous=%+v result=%+v err=%v", out, result, err)
	}
	prepared.Manifest.Records = prepared.Manifest.Records[:1]
	prepared.Manifest.Records[0].DisplayName = "不同名称"
	out, result, err = VerifySource(snap, actor, caseID, prepared, model.SubjectSourceVerifyInput{SubjectID: "subject", ExpectedSubjectVersion: 1}, map[string]bool{"case": true}, "no-match-key", "hash", clock)
	if err != nil || result.MatchOutcome != "no_match" || len(out.Requests) != 0 || len(out.Receipts) != 1 || out.Receipts[0].HTTPStatus != 200 {
		t.Fatalf("no match=%+v result=%+v err=%v", out, result, err)
	}
}

func TestSourceSameVersionRejectsChangedDigest(t *testing.T) {
	actor := Actor{TenantID: "tenant", Principal: model.TrustedPrincipalProjection{UserID: "actor"}}
	clock := &sourceTestClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	manifest := &model.SubjectSourceManifest{SourceID: model.SyntheticSubjectSourceID, SourceVersion: 1, DisplayName: "new", VerificationScope: "synthetic_controlled"}
	snapshot := Snapshot{Sources: []model.SubjectSourceSnapshot{{TenantID: "tenant", SourceCatalogID: manifest.SourceID, SourceVersion: 1, SourceDigest: "different"}}}
	_, _, err := CreateSourceSnapshot(snapshot, actor, CaseIdentity{}, model.SubjectSourceSnapshotCreateInput{SourceID: manifest.SourceID, ExpectedSourceVersion: 1}, manifest, "key", "hash", clock)
	if err == nil || err.Error() != "同一受控来源版本已有不同内容，请提升来源版本" || len(clock.calls) != 0 {
		t.Fatalf("calls=%v err=%v", clock.calls, err)
	}
}

func TestSourceSnapshotIDErrorPreservesInitializedResult(t *testing.T) {
	sentinel := errors.New("id failure")
	clock := &sourceTestClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), snapshotErr: sentinel}
	live := &model.SubjectSourceManifest{SourceID: model.SyntheticSubjectSourceID, SourceVersion: 1, VerificationScope: "synthetic_controlled"}
	delta, result, err := CreateSourceSnapshot(Snapshot{}, Actor{}, CaseIdentity{}, model.SubjectSourceSnapshotCreateInput{SourceID: live.SourceID, ExpectedSourceVersion: 1}, live, "key", "hash", clock)
	if !errors.Is(err, sentinel) || result.SourceID != live.SourceID || result.SourceDigest == "" || result.CapturedAt.IsZero() || len(delta.Sources) != 0 {
		t.Fatalf("result=%+v delta=%+v err=%v", result, delta, err)
	}
}
