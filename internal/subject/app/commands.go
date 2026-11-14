// File metadata (manifest mirror):
// Purpose: Coordinates normalized Subject creation and binding, typed receipt replay and immutable append deltas using server-owned identity and clock capabilities.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package app coordinates Subject commands against typed locked snapshots.
package app

import (
	"encoding/json"
	"strings"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/subject/model"
)

type Actor struct {
	TenantID  string
	Principal model.TrustedPrincipalProjection
}
type CaseIdentity struct{ TenantID, OrgID, CaseID string }
type LegacyProfile struct {
	Found              bool
	Version            int
	Customer, Supplier string
}
type Snapshot struct {
	Collections model.SubjectCollections
	Receipts    []model.SubjectIdempotencyRecord
	Sources     []model.SubjectSourceSnapshot
	Requests    []model.SubjectChangeRequest
	Decisions   []model.SubjectChangeDecision
}
type SubjectHeadUpdate struct {
	Expected model.Subject
	Next     model.Subject
}
type BindingHeadUpdate struct {
	Expected model.CaseSubjectRole
	Next     model.CaseSubjectRole
}
type Delta struct {
	SubjectHeads    []SubjectHeadUpdate
	BindingHeads    []BindingHeadUpdate
	Decisions       []model.SubjectChangeDecision
	Subjects        []model.Subject
	Versions        []model.SubjectVersion
	Bindings        []model.CaseSubjectRole
	BindingVersions []model.CaseSubjectRoleVersion
	Receipts        []model.SubjectIdempotencyRecord
	Sources         []model.SubjectSourceSnapshot
	Requests        []model.SubjectChangeRequest
}
type IDsClock interface {
	Now() time.Time
	NewSubjectID() (string, error)
	NewBindingID() (string, error)
}

func NormalizeUnbound(input model.SubjectCreateInput) (model.SubjectCreateInput, error) {
	normalized, err := model.NormalizeSubjectCreateInput(input)
	if err != nil {
		return model.SubjectCreateInput{}, fault.Invalid(err.Error())
	}
	return normalized, nil
}
func RequestHash(input any) (string, error) { return model.CanonicalSubjectHash(input) }

func NormalizeCaseCreate(caseID string, input model.CaseSubjectCreateInput) (string, model.CaseSubjectCreateInput, error) {
	caseID = strings.TrimSpace(caseID)
	input.Role = strings.TrimSpace(input.Role)
	subject, err := model.NormalizeSubjectCreateInput(input.Subject)
	input.Subject = subject
	if caseID == "" || !model.ValidSubjectRole(input.Role) || err != nil {
		return "", model.CaseSubjectCreateInput{}, fault.Invalid("案件主体字段无效")
	}
	if input.LegacyBinding != nil {
		input.LegacyBinding.Field = strings.TrimSpace(input.LegacyBinding.Field)
		if input.LegacyBinding.Value == "" || input.LegacyBinding.ExpectedProfileVersion < 1 || (input.LegacyBinding.Field != "customer" && input.LegacyBinding.Field != "supplier") || input.LegacyBinding.Field != input.Role {
			return "", model.CaseSubjectCreateInput{}, fault.Invalid("legacy_binding字段无效或与主体角色不一致")
		}
	}
	return caseID, input, nil
}
func NormalizeBind(caseID string, input model.BindExistingSubjectInput) (string, model.BindExistingSubjectInput, error) {
	caseID, input.SubjectID, input.Role = strings.TrimSpace(caseID), strings.TrimSpace(input.SubjectID), strings.TrimSpace(input.Role)
	if caseID == "" || input.SubjectID == "" || input.ExpectedSubjectVersion < 1 || !model.ValidSubjectRole(input.Role) {
		return "", model.BindExistingSubjectInput{}, fault.Invalid("主体绑定字段无效")
	}
	return caseID, input, nil
}
func replay(snapshot Snapshot, actor Actor, operation, target, key, hash string, out any) (bool, error) {
	for _, item := range snapshot.Receipts {
		if item.TenantID != actor.TenantID || item.ActorUserID != actor.Principal.UserID || item.Operation != operation || item.TargetID != target || item.Key != key {
			continue
		}
		if item.PayloadHash != hash {
			return false, fault.Conflict("Idempotency-Key已用于不同请求内容")
		}
		return true, json.Unmarshal(item.Response, out)
	}
	return false, nil
}
func ReplayUnbound(snapshot Snapshot, actor Actor, key, hash string) (model.CreateSubjectResult, bool, error) {
	var out model.CreateSubjectResult
	found, err := replay(snapshot, actor, "create_subject", "unbound", key, hash, &out)
	return out, found, err
}
func ReplayCase(snapshot Snapshot, actor Actor, caseID, key, hash string) (model.CreateCaseSubjectResult, bool, error) {
	var out model.CreateCaseSubjectResult
	found, err := replay(snapshot, actor, "create_case_subject", caseID, key, hash, &out)
	return out, found, err
}
func ReplayBinding(snapshot Snapshot, actor Actor, caseID, key, hash string) (model.BindExistingSubjectResult, bool, error) {
	var out model.BindExistingSubjectResult
	found, err := replay(snapshot, actor, "bind_existing_subject", caseID, key, hash, &out)
	return out, found, err
}
func receipt(actor Actor, operation, target, key, hash string, result any, now time.Time) (model.SubjectIdempotencyRecord, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return model.SubjectIdempotencyRecord{}, err
	}
	return model.SubjectIdempotencyRecord{TenantID: actor.TenantID, ActorUserID: actor.Principal.UserID, Operation: operation, TargetID: target, Key: key, PayloadHash: hash, Response: raw, HTTPStatus: 201, CreatedAt: now}, nil
}
func CreateUnbound(actor Actor, input model.SubjectCreateInput, key, hash string, clock IDsClock) (Delta, model.CreateSubjectResult, error) {
	now := clock.Now().UTC()
	id, err := clock.NewSubjectID()
	if err != nil {
		return Delta{}, model.CreateSubjectResult{}, err
	}
	head, version, err := model.NewSubjectAggregate(actor.TenantID, id, actor.Principal, input, now)
	if err != nil {
		return Delta{}, model.CreateSubjectResult{}, err
	}
	result := model.CreateSubjectResult{SubjectID: head.ID, CurrentVersion: 1, SubjectVersion: model.ProjectSubjectVersion(version, false), CreatedAt: head.CreatedAt}
	saved, err := receipt(actor, "create_subject", "unbound", key, hash, result, clock.Now().UTC())
	if err != nil {
		return Delta{}, model.CreateSubjectResult{}, err
	}
	return Delta{Subjects: []model.Subject{head}, Versions: []model.SubjectVersion{version}, Receipts: []model.SubjectIdempotencyRecord{saved}}, result, nil
}
func CreateCase(actor Actor, caseID CaseIdentity, input model.CaseSubjectCreateInput, profile LegacyProfile, key, hash string, clock IDsClock) (Delta, model.CreateCaseSubjectResult, error) {
	if input.LegacyBinding != nil {
		if !profile.Found || profile.Version != input.LegacyBinding.ExpectedProfileVersion {
			return Delta{}, model.CreateCaseSubjectResult{}, fault.Conflict("案件旧资料版本已变化，请刷新后重试")
		}
		value := profile.Customer
		if input.LegacyBinding.Field == "supplier" {
			value = profile.Supplier
		}
		if value == "" || value != input.LegacyBinding.Value {
			return Delta{}, model.CreateCaseSubjectResult{}, fault.Conflict("案件旧资料已变化，请刷新后重试")
		}
	}
	now := clock.Now().UTC()
	id, err := clock.NewSubjectID()
	if err != nil {
		return Delta{}, model.CreateCaseSubjectResult{}, err
	}
	subject, version, err := model.NewSubjectAggregate(actor.TenantID, id, actor.Principal, input.Subject, now)
	if err != nil {
		return Delta{}, model.CreateCaseSubjectResult{}, err
	}
	bindingID, err := clock.NewBindingID()
	if err != nil {
		return Delta{}, model.CreateCaseSubjectResult{}, err
	}
	source := "user_asserted"
	var legacy *string
	if input.LegacyBinding != nil {
		source = "legacy_label"
		value := input.LegacyBinding.Value
		legacy = &value
	}
	head := model.CaseSubjectRole{TenantID: caseID.TenantID, OrgID: caseID.OrgID, CaseID: caseID.CaseID, BindingID: bindingID, CurrentVersion: 1, CreatedByUser: actor.Principal.UserID, CreatedAt: now}
	binding := model.CaseSubjectRoleVersion{TenantID: caseID.TenantID, OrgID: caseID.OrgID, CaseID: caseID.CaseID, BindingID: bindingID, Version: 1, SubjectID: subject.ID, SubjectVersion: 1, Role: input.Role, BindingState: "pending_confirmation", SourceKind: source, CreatedByUser: actor.Principal.UserID, CreatedByIssuer: actor.Principal.Issuer, CreatedBySubject: actor.Principal.Subject, CreatedAt: now}
	if legacy != nil {
		binding.LegacyLabel = *legacy
	}
	result := model.CreateCaseSubjectResult{BindingID: bindingID, BindingVersion: 1, SubjectID: subject.ID, SubjectVersion: 1, Role: input.Role, DisplayName: version.DisplayName, VerificationState: version.VerificationState, SourceKind: source, LegacyLabel: legacy, BindingState: binding.BindingState, Subject: model.ProjectSubjectVersion(version, false)}
	saved, err := receipt(actor, "create_case_subject", caseID.CaseID, key, hash, result, now)
	if err != nil {
		return Delta{}, model.CreateCaseSubjectResult{}, err
	}
	return Delta{Subjects: []model.Subject{subject}, Versions: []model.SubjectVersion{version}, Bindings: []model.CaseSubjectRole{head}, BindingVersions: []model.CaseSubjectRoleVersion{binding}, Receipts: []model.SubjectIdempotencyRecord{saved}}, result, nil
}
func VisibleSubject(snapshot Snapshot, actor Actor, subjectID string, readableCaseIDs map[string]bool) (model.Subject, model.SubjectVersion, bool) {
	readable := make(map[string]CaseFact, len(readableCaseIDs))
	for caseID, canRead := range readableCaseIDs {
		readable[caseID] = CaseFact{Found: canRead, Read: canRead, CaseID: caseID}
	}
	return VisibleSubjectWithFacts(snapshot, actor, subjectID, ReadFacts{Cases: readable})
}
func BindExisting(snapshot Snapshot, actor Actor, caseID CaseIdentity, input model.BindExistingSubjectInput, readable map[string]bool, key, hash string, clock IDsClock) (Delta, model.BindExistingSubjectResult, error) {
	subject, version, visible := VisibleSubject(snapshot, actor, input.SubjectID, readable)
	if !visible {
		return Delta{}, model.BindExistingSubjectResult{}, fault.NotFound("没有找到对应记录")
	}
	if subject.CurrentVersion != input.ExpectedSubjectVersion {
		return Delta{}, model.BindExistingSubjectResult{}, fault.Conflict("主体版本已变化，请刷新后重试")
	}
	now := clock.Now().UTC()
	id, err := clock.NewBindingID()
	if err != nil {
		return Delta{}, model.BindExistingSubjectResult{}, err
	}
	head := model.CaseSubjectRole{TenantID: caseID.TenantID, OrgID: caseID.OrgID, CaseID: caseID.CaseID, BindingID: id, CurrentVersion: 1, CreatedByUser: actor.Principal.UserID, CreatedAt: now}
	versionRow := model.CaseSubjectRoleVersion{TenantID: caseID.TenantID, OrgID: caseID.OrgID, CaseID: caseID.CaseID, BindingID: id, Version: 1, SubjectID: subject.ID, SubjectVersion: version.Version, Role: input.Role, BindingState: "active", SourceKind: "user_asserted", CreatedByUser: actor.Principal.UserID, CreatedByIssuer: actor.Principal.Issuer, CreatedBySubject: actor.Principal.Subject, CreatedAt: now}
	result := model.BindExistingSubjectResult{BindingID: id, BindingVersion: 1, SubjectID: subject.ID, SubjectVersion: version.Version, Role: input.Role, DisplayName: version.DisplayName, VerificationState: version.VerificationState, BindingState: "active", CreatedAt: now, SourceKind: "user_asserted"}
	saved, err := receipt(actor, "bind_existing_subject", caseID.CaseID, key, hash, result, now)
	if err != nil {
		return Delta{}, model.BindExistingSubjectResult{}, err
	}
	return Delta{Bindings: []model.CaseSubjectRole{head}, BindingVersions: []model.CaseSubjectRoleVersion{versionRow}, Receipts: []model.SubjectIdempotencyRecord{saved}}, result, nil
}
