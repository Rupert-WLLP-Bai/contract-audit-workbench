// File metadata (manifest mirror):
// Purpose: Pins complete affected-case authorization, stale impact fingerprints, trusted-principal separation and approve or reject deltas.
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
	"strings"
	"testing"
	"time"

	"contractaudit/internal/subject/model"
)

type decisionTestClock struct{ calls []string }

func (c *decisionTestClock) NewDecisionID() (string, error) {
	c.calls = append(c.calls, "id")
	return "decision", nil
}
func (c *decisionTestClock) Now() time.Time {
	c.calls = append(c.calls, "now")
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}
func decisionFixture(t *testing.T) (Snapshot, Actor, model.SubjectChangeRequest, DecisionFacts) {
	t.Helper()
	subject := model.Subject{TenantID: "tenant", ID: "subject", CurrentVersion: 1, Status: "pending_verification"}
	before := model.SubjectVersion{TenantID: "tenant", SubjectID: "subject", Version: 1, DisplayName: "before", IdentityClaims: []model.SubjectIdentityClaim{}, Aliases: []model.SubjectAlias{}}
	after := before
	after.Version = 2
	after.DisplayName = "after"
	after.PayloadHash = model.HashSubjectVersion(after)
	payload, err := json.Marshal(model.SubjectAfterPayload{SubjectVersion: after})
	if err != nil {
		t.Fatal(err)
	}
	snap := Snapshot{Collections: model.SubjectCollections{Subjects: []model.Subject{subject}, SubjectVersions: []model.SubjectVersion{before}, CaseSubjectRoles: []model.CaseSubjectRole{{TenantID: "tenant", OrgID: "org", CaseID: "c1", BindingID: "b1", CurrentVersion: 1}, {TenantID: "tenant", OrgID: "org", CaseID: "c2", BindingID: "b2", CurrentVersion: 1}}, CaseSubjectRoleVersions: []model.CaseSubjectRoleVersion{{TenantID: "tenant", OrgID: "org", CaseID: "c1", BindingID: "b1", Version: 1, SubjectID: "subject", SubjectVersion: 1, BindingState: "active"}, {TenantID: "tenant", OrgID: "org", CaseID: "c2", BindingID: "b2", Version: 1, SubjectID: "subject", SubjectVersion: 1, BindingState: "active"}}}}
	bindings := model.AffectedSubjectBindings(snap.Collections.CaseSubjectRoles, snap.Collections.CaseSubjectRoleVersions, "tenant", "subject", "subject", "")
	bindingHash, err := model.CanonicalSubjectHash(bindings)
	if err != nil {
		t.Fatal(err)
	}
	request := model.SubjectChangeRequest{TenantID: "tenant", OrgID: "org", SourceCaseID: "c1", RequestID: "request", TargetKind: "subject", TargetSubjectID: "subject", ExpectedSubjectVersion: 1, CanonicalPayload: payload, CanonicalPayloadHash: "payload", ExpectedBindingsHash: bindingHash, ProposedBy: model.TrustedPrincipalProjection{UserID: "proposer", Issuer: "issuer", Subject: "same"}}
	snap.Requests = []model.SubjectChangeRequest{request}
	actor := Actor{TenantID: "tenant", Principal: model.TrustedPrincipalProjection{UserID: "decider", Issuer: "different", Subject: "user"}}
	facts := DecisionFacts{CanDecideRole: true, Cases: map[string]CaseFact{"c1": {Found: true, Read: true, Manage: true, Decide: true, Writable: true, TenantID: "tenant", CaseID: "c1", Title: "first", Status: "active"}, "c2": {Found: true, Read: true, Decide: true, Writable: true, TenantID: "tenant", CaseID: "c2", Title: "second", Status: "active"}}}
	return snap, actor, request, facts
}
func TestDecisionRequiresAllAffectedCasesAndRechecksImpact(t *testing.T) {
	snap, actor, request, facts := decisionFixture(t)
	impact, err := DecisionImpact(snap, actor, request, facts)
	if err != nil {
		t.Fatal(err)
	}
	input := model.SubjectChangeDecisionInput{Decision: "approve", ExpectedSubjectVersion: 1, ExpectedPayloadHash: "payload", ExpectedImpactETag: impact.ImpactETag, Reason: "通过"}
	clock := &decisionTestClock{}
	denied := facts
	denied.Cases = map[string]CaseFact{"c1": facts.Cases["c1"], "c2": {Found: true, Read: false, Decide: false, Writable: true, CaseID: "c2"}}
	_, _, err = Decide(snap, actor, request, input, "key", "hash", denied, clock)
	if err == nil || !strings.Contains(err.Error(), "全部受影响案件") || len(clock.calls) != 0 {
		t.Fatalf("denied err=%v calls=%v", err, clock.calls)
	}
	changed := facts
	changed.Cases = map[string]CaseFact{"c1": facts.Cases["c1"], "c2": facts.Cases["c2"]}
	updated := changed.Cases["c2"]
	updated.Title = "changed"
	changed.Cases["c2"] = updated
	_, _, err = Decide(snap, actor, request, input, "key", "hash", changed, clock)
	if err == nil || !strings.Contains(err.Error(), "影响预览已变化") || len(clock.calls) != 0 {
		t.Fatalf("stale impact err=%v calls=%v", err, clock.calls)
	}
	delta, result, err := Decide(snap, actor, request, input, "key", "hash", facts, clock)
	if err != nil || result.Decision != "approved" || len(delta.SubjectHeads) != 1 || len(delta.BindingHeads) != 2 || len(delta.BindingVersions) != 2 || len(delta.Decisions) != 1 || len(delta.Receipts) != 1 {
		t.Fatalf("delta=%+v result=%+v err=%v", delta, result, err)
	}
}
func TestDecisionRejectsSameIssuerSubject(t *testing.T) {
	snap, actor, request, facts := decisionFixture(t)
	actor.Principal.Issuer, actor.Principal.Subject = "issuer", "same"
	_, _, err := Decide(snap, actor, request, model.SubjectChangeDecisionInput{ExpectedSubjectVersion: 1, ExpectedPayloadHash: "payload"}, "key", "hash", facts, &decisionTestClock{})
	if err == nil || !strings.Contains(err.Error(), "提出人不能") {
		t.Fatalf("err=%v", err)
	}
}

func TestRejectAppendsOnlyDecisionAndReceipt(t *testing.T) {
	snap, actor, request, facts := decisionFixture(t)
	impact, err := DecisionImpact(snap, actor, request, facts)
	if err != nil {
		t.Fatal(err)
	}
	input := model.SubjectChangeDecisionInput{Decision: "reject", ExpectedSubjectVersion: 1, ExpectedPayloadHash: "payload", ExpectedImpactETag: impact.ImpactETag, Reason: "拒绝"}
	delta, result, err := Decide(snap, actor, request, input, "key", "hash", facts, &decisionTestClock{})
	if err != nil || result.Decision != "rejected" || len(delta.Decisions) != 1 || len(delta.Receipts) != 1 || len(delta.SubjectHeads) != 0 || len(delta.Versions) != 0 || len(delta.BindingHeads) != 0 || len(delta.BindingVersions) != 0 {
		t.Fatalf("delta=%+v result=%+v err=%v", delta, result, err)
	}
}
