// File metadata (manifest mirror):
// Purpose: Pins proposal clock and basis order, immutable proposal-only behavior and inactive-binding restoration to the exact current Subject version.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"contractaudit/internal/subject/model"
)

type proposalTestPorts struct {
	calls    []string
	basisErr error
}

func (p *proposalTestPorts) Now() time.Time {
	p.calls = append(p.calls, "now")
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}
func (p *proposalTestPorts) NewRequestID() (string, error) {
	p.calls = append(p.calls, "id")
	return "request", nil
}
func (p *proposalTestPorts) VerifyProposalBasis(context.Context, json.RawMessage) (json.RawMessage, error) {
	p.calls = append(p.calls, "basis")
	return json.RawMessage(`[{}]`), p.basisErr
}
func (p *proposalTestPorts) ProposalActorPresentation() (string, bool) {
	p.calls = append(p.calls, "presentation")
	return "提出人", true
}
func TestSubjectProposalSamplesCandidateBeforeBasisAndDoesNotMutateSubject(t *testing.T) {
	snap := Snapshot{Collections: model.SubjectCollections{Subjects: []model.Subject{{TenantID: "t", ID: "s", CurrentVersion: 1, Status: "pending_verification", CreatedByUser: "u"}}, SubjectVersions: []model.SubjectVersion{{TenantID: "t", SubjectID: "s", Version: 1, DisplayName: "原名"}}, CaseSubjectRoles: []model.CaseSubjectRole{{TenantID: "t", OrgID: "o", CaseID: "c", BindingID: "b", CurrentVersion: 1}}, CaseSubjectRoleVersions: []model.CaseSubjectRoleVersion{{TenantID: "t", OrgID: "o", CaseID: "c", BindingID: "b", Version: 1, SubjectID: "s", SubjectVersion: 1, BindingState: "active"}}}}
	input := model.SubjectChangeCreateInput{Target: model.SubjectChangeTarget{Kind: "subject", SubjectID: "s"}, ExpectedSubjectVersion: 1, Patch: json.RawMessage(`{"display_name":"新名"}`), BasisNote: "更正", BasisRefs: json.RawMessage(`[{}]`)}
	actor := Actor{TenantID: "t", Principal: model.TrustedPrincipalProjection{UserID: "u"}}
	caseID := CaseIdentity{TenantID: "t", OrgID: "o", CaseID: "c"}
	ports := &proposalTestPorts{basisErr: errors.New("invalid basis")}
	_, _, err := Propose(context.Background(), snap, actor, caseID, input, map[string]bool{"c": true}, "key", "hash", ports, ports, ports)
	if err == nil || !reflect.DeepEqual(ports.calls, []string{"now", "basis"}) {
		t.Fatalf("calls=%v err=%v", ports.calls, err)
	}
	ports.calls = nil
	ports.basisErr = nil
	delta, result, err := Propose(context.Background(), snap, actor, caseID, input, map[string]bool{"c": true}, "key", "hash", ports, ports, ports)
	if err != nil || !reflect.DeepEqual(ports.calls, []string{"now", "basis", "id", "now", "presentation"}) || len(delta.Requests) != 1 || len(delta.Receipts) != 1 || len(delta.Versions) != 0 || snap.Collections.SubjectVersions[0].DisplayName != "原名" || result.CanDecide {
		t.Fatalf("delta=%+v result=%+v calls=%v err=%v", delta, result, ports.calls, err)
	}
}

func TestInactiveBindingFallbackRepinsCurrentSubjectVersion(t *testing.T) {
	expected := int64(1)
	snap := Snapshot{Collections: model.SubjectCollections{Subjects: []model.Subject{{TenantID: "t", ID: "s", CurrentVersion: 2, Status: "pending_verification", CreatedByUser: "u"}}, SubjectVersions: []model.SubjectVersion{{TenantID: "t", SubjectID: "s", Version: 2, DisplayName: "主体"}}, CaseSubjectRoles: []model.CaseSubjectRole{{TenantID: "t", OrgID: "o", CaseID: "c", BindingID: "b", CurrentVersion: 1}}, CaseSubjectRoleVersions: []model.CaseSubjectRoleVersion{{TenantID: "t", OrgID: "o", CaseID: "c", BindingID: "b", Version: 1, SubjectID: "s", SubjectVersion: 1, BindingState: "inactive", Role: "customer"}}}}
	input := model.SubjectChangeCreateInput{Target: model.SubjectChangeTarget{Kind: "case_subject_role", SubjectID: "s", BindingID: "b"}, ExpectedSubjectVersion: 2, ExpectedBindingVersion: &expected, Patch: json.RawMessage(`{"binding_state":"active"}`), BasisNote: "恢复", BasisRefs: json.RawMessage(`[{}]`)}
	ports := &proposalTestPorts{}
	delta, _, err := Propose(context.Background(), snap, Actor{TenantID: "t", Principal: model.TrustedPrincipalProjection{UserID: "u"}}, CaseIdentity{TenantID: "t", OrgID: "o", CaseID: "c"}, input, nil, "key", "hash", ports, ports, ports)
	if err != nil || len(delta.Requests) != 1 {
		t.Fatalf("delta=%+v err=%v", delta, err)
	}
	var payload model.BindingAfterPayload
	if err := json.Unmarshal(delta.Requests[0].CanonicalPayload, &payload); err != nil || payload.SubjectVersion != 2 || payload.BindingState != "active" {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}
}
