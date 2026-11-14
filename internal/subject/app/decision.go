// File metadata (manifest mirror):
// Purpose: Plans independent cross-case Subject decisions, complete impact checks, visible impact fingerprints and typed head or immutable-version deltas.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/subject/model"
)

type CaseFact struct {
	Found, Read, Manage, Decide, Writable  bool
	TenantID, OrgID, CaseID, Title, Status string
}
type DecisionFacts struct {
	Cases               map[string]CaseFact
	CanDecideRole       bool
	ProposerDisplayName string
}
type DecisionClock interface {
	NewDecisionID() (string, error)
	Now() time.Time
}

func ValidateDecisionInput(input model.SubjectChangeDecisionInput) error {
	if input.Decision != "approve" && input.Decision != "reject" || input.ExpectedSubjectVersion < 1 || strings.TrimSpace(input.ExpectedPayloadHash) == "" || strings.TrimSpace(input.ExpectedImpactETag) == "" || strings.TrimSpace(input.Reason) == "" {
		return fault.Invalid("更正决定和预期版本、摘要、影响标识及理由均为必填项")
	}
	return nil
}
func DecisionLockTargets(snapshot Snapshot, request model.SubjectChangeRequest) ([]string, []string) {
	cases := model.AffectedSubjectCaseIDs(snapshot.Collections, request)
	subjects := []string{request.TargetSubjectID}
	if request.TargetKind == "case_subject_role" {
		var p model.BindingAfterPayload
		if json.Unmarshal(request.CanonicalPayload, &p) == nil && p.SubjectID != "" {
			subjects = append(subjects, p.SubjectID)
		}
	}
	return subjects, cases
}
func DecisionRequestHash(caseID, requestID string, input model.SubjectChangeDecisionInput) (string, error) {
	return model.CanonicalSubjectHash(struct {
		CaseID, RequestID string
		Input             model.SubjectChangeDecisionInput
	}{caseID, requestID, input})
}
func DecisionSummary(snapshot Snapshot, actor Actor, request model.SubjectChangeRequest, facts DecisionFacts) model.SubjectChangeSummary {
	state := "pending_decision"
	for _, decision := range snapshot.Decisions {
		if decision.RequestID == request.RequestID {
			state = "rejected"
			if decision.Decision == "approve" {
				state = "approved"
			}
		}
	}
	var bindingID *string
	if request.TargetBindingID != "" {
		value := request.TargetBindingID
		bindingID = &value
	}
	allowed := false
	reason := ""
	if state != "pending_decision" {
		reason = "该更正已完成决定"
	} else if !facts.CanDecideRole {
		reason = "当前角色没有更正决定权限"
	} else if model.SamePrincipal(request.ProposedBy, actor.Principal) {
		reason = "更正提出人不能批准或拒绝同一更正"
	} else {
		allowed = true
		for _, id := range model.AffectedSubjectCaseIDs(snapshot.Collections, request) {
			c := facts.Cases[id]
			if !c.Found || !c.Writable || !c.Decide {
				allowed = false
				break
			}
		}
		if !allowed {
			reason = "当前授权不足以决定全部受影响案件"
		}
	}
	var blocked *string
	if reason != "" {
		blocked = &reason
	}
	display := facts.ProposerDisplayName
	if display == "" {
		display = request.ProposedBy.UserID
	}
	return model.SubjectChangeSummary{RequestID: request.RequestID, TargetKind: request.TargetKind, TargetSubjectID: request.TargetSubjectID, TargetBindingID: bindingID, ExpectedSubjectVersion: request.ExpectedSubjectVersion, ExpectedBindingVersion: request.ExpectedBindingVersion, PayloadHash: request.CanonicalPayloadHash, State: state, ProposedBy: request.ProposedBy, ProposedByDisplayName: display, CanDecide: allowed, DecisionBlockedReason: blocked, CreatedAt: request.CreatedAt}
}
func DecisionImpact(snapshot Snapshot, actor Actor, request model.SubjectChangeRequest, facts DecisionFacts) (model.SubjectChangeImpactResult, error) {
	source := facts.Cases[request.SourceCaseID]
	if !source.Found || source.TenantID != actor.TenantID || !source.Read {
		return model.SubjectChangeImpactResult{}, fault.NotFound("没有找到对应记录")
	}
	bindings := model.AffectedSubjectBindings(snapshot.Collections.CaseSubjectRoles, snapshot.Collections.CaseSubjectRoleVersions, request.TenantID, request.TargetSubjectID, request.TargetKind, request.TargetBindingID)
	decisionScope := facts.CanDecideRole
	for _, id := range model.AffectedSubjectCaseIDs(snapshot.Collections, request) {
		c := facts.Cases[id]
		if !c.Found || !c.Writable || !c.Decide {
			decisionScope = false
		}
	}
	full := source.Manage || (!model.SamePrincipal(request.ProposedBy, actor.Principal) && decisionScope)
	before, after, err := model.SubjectChangePayloads(snapshot.Collections, request, full)
	if err != nil {
		return model.SubjectChangeImpactResult{}, err
	}
	visible := make([]model.SubjectImpactCaseProjection, 0)
	visibleBindings := make([]model.CaseSubjectRoleVersion, 0)
	for _, binding := range bindings {
		c := facts.Cases[binding.CaseID]
		if !c.Found || !c.Read {
			continue
		}
		visible = append(visible, model.SubjectImpactCaseProjection{CaseID: c.CaseID, Title: c.Title, Status: c.Status})
		visibleBindings = append(visibleBindings, binding)
	}
	if !full {
		before = model.MaskSubjectChangeProjection(before)
		after = model.MaskSubjectChangeProjection(after)
	}
	encoded, err := json.Marshal(struct {
		RequestID     string                              `json:"request_id"`
		TargetVersion int64                               `json:"target_version"`
		PayloadHash   string                              `json:"payload_hash"`
		Before        any                                 `json:"before"`
		After         any                                 `json:"after"`
		Bindings      []model.CaseSubjectRoleVersion      `json:"bindings"`
		Cases         []model.SubjectImpactCaseProjection `json:"cases"`
	}{request.RequestID, request.ExpectedSubjectVersion, request.CanonicalPayloadHash, before, after, visibleBindings, visible})
	if err != nil {
		return model.SubjectChangeImpactResult{}, err
	}
	sum := sha256.Sum256(encoded)
	summary := DecisionSummary(snapshot, actor, request, facts)
	return model.SubjectChangeImpactResult{RequestID: request.RequestID, TargetVersion: request.ExpectedSubjectVersion, PayloadHash: request.CanonicalPayloadHash, Before: before, After: after, BasisRefs: append(json.RawMessage(nil), request.BasisRefs...), ImpactETag: hex.EncodeToString(sum[:]), VisibleAffectedCount: len(visibleBindings), AuthorizedImpactCases: visible, ProposedByDisplayName: summary.ProposedByDisplayName, CanDecide: summary.CanDecide, DecisionBlockedReason: summary.DecisionBlockedReason}, nil
}
func ReplayDecision(snapshot Snapshot, actor Actor, caseID, requestID, key, hash string) (model.SubjectChangeRequest, model.SubjectChangeDecisionResult, bool, error) {
	prior, ok := model.FindSubjectChangeRequest(snapshot.Requests, actor.TenantID, caseID, requestID)
	if !ok {
		return model.SubjectChangeRequest{}, model.SubjectChangeDecisionResult{}, false, fault.NotFound("没有找到对应记录")
	}
	var result model.SubjectChangeDecisionResult
	found, err := replay(snapshot, actor, "decide_subject_change", requestID, key, hash, &result)
	return prior, result, found, err
}
func Decide(snapshot Snapshot, actor Actor, prior model.SubjectChangeRequest, input model.SubjectChangeDecisionInput, key, hash string, facts DecisionFacts, clock DecisionClock) (Delta, model.SubjectChangeDecisionResult, error) {
	requestID := prior.RequestID
	var result model.SubjectChangeDecisionResult
	if prior.ExpectedSubjectVersion != input.ExpectedSubjectVersion || prior.CanonicalPayloadHash != input.ExpectedPayloadHash || !model.SameOptionalInt(prior.ExpectedBindingVersion, input.ExpectedBindingVersion) {
		return Delta{}, result, fault.Conflict("主体更正版本或内容已变化，请刷新影响预览")
	}
	for _, decision := range snapshot.Decisions {
		if decision.RequestID == requestID {
			return Delta{}, result, fault.Conflict("该更正已由其他决定完成")
		}
	}
	if model.SamePrincipal(prior.ProposedBy, actor.Principal) {
		return Delta{}, result, fault.Forbidden("更正提出人不能批准或拒绝同一更正")
	}
	c := snapshot.Collections
	bindings := model.AffectedSubjectBindings(c.CaseSubjectRoles, c.CaseSubjectRoleVersions, actor.TenantID, prior.TargetSubjectID, prior.TargetKind, prior.TargetBindingID)
	affectedHash, err := model.CanonicalSubjectHash(bindings)
	if err != nil {
		return Delta{}, result, err
	}
	if affectedHash != prior.ExpectedBindingsHash {
		return Delta{}, result, fault.Conflict("主体关联影响范围已变化，请重新查看")
	}
	for _, binding := range bindings {
		f := facts.Cases[binding.CaseID]
		if !f.Found || !f.Writable || !f.Decide {
			return Delta{}, result, fault.Conflict("当前授权范围不足以决定全部受影响案件，请重新查看")
		}
	}
	impact, err := DecisionImpact(snapshot, actor, prior, facts)
	if err != nil {
		return Delta{}, result, err
	}
	if impact.ImpactETag != input.ExpectedImpactETag {
		return Delta{}, result, fault.Conflict("主体更正影响预览已变化，请刷新后重试")
	}
	decisionID, err := clock.NewDecisionID()
	if err != nil {
		return Delta{}, result, err
	}
	now := clock.Now().UTC()
	var delta Delta
	var applied *int64
	appended := 0
	if input.Decision == "approve" {
		delta, applied, appended, err = planApproved(snapshot, actor, prior, bindings, now)
		if err != nil {
			return Delta{}, result, err
		}
	}
	decision := model.SubjectChangeDecision{TenantID: actor.TenantID, DecisionID: decisionID, RequestID: requestID, Decision: input.Decision, Actor: actor.Principal, ExpectedSubjectVersion: input.ExpectedSubjectVersion, ExpectedBindingVersion: input.ExpectedBindingVersion, CanonicalPayloadHash: input.ExpectedPayloadHash, AffectedBindingsHash: affectedHash, Reason: strings.TrimSpace(input.Reason), AppliedSubjectVersion: applied, BindingVersionsAppended: appended, CreatedAt: now}
	name := "rejected"
	if input.Decision == "approve" {
		name = "approved"
	}
	result = model.SubjectChangeDecisionResult{RequestID: requestID, Decision: name, AppliedSubjectVersion: applied, BindingVersionsAppended: appended}
	saved, err := sourceReceipt(actor, "decide_subject_change", requestID, key, hash, 200, result, now)
	if err != nil {
		return Delta{}, result, err
	}
	delta.Decisions = append(delta.Decisions, decision)
	delta.Receipts = append(delta.Receipts, saved)
	return delta, result, nil
}

func planApproved(snapshot Snapshot, actor Actor, request model.SubjectChangeRequest, bindings []model.CaseSubjectRoleVersion, now time.Time) (Delta, *int64, int, error) {
	c := snapshot.Collections
	var delta Delta
	if request.TargetKind == "subject" {
		var subject model.Subject
		found := false
		for _, item := range c.Subjects {
			if item.TenantID == actor.TenantID && item.ID == request.TargetSubjectID {
				subject = item
				found = true
				break
			}
		}
		if !found || subject.CurrentVersion != request.ExpectedSubjectVersion {
			return Delta{}, nil, 0, fault.Conflict("主体版本已变化")
		}
		var after model.SubjectAfterPayload
		if err := json.Unmarshal(request.CanonicalPayload, &after); err != nil {
			return Delta{}, nil, 0, err
		}
		version := after.SubjectVersion
		if version.Version != request.ExpectedSubjectVersion+1 || version.SubjectID != request.TargetSubjectID || version.PayloadHash != model.HashSubjectVersion(version) {
			return Delta{}, nil, 0, errors.New("更正主体版本摘要无效")
		}
		nextHead := subject
		nextHead.CurrentVersion = version.Version
		nextHead.Status = version.VerificationState
		delta.SubjectHeads = append(delta.SubjectHeads, SubjectHeadUpdate{Expected: subject, Next: nextHead})
		delta.Versions = append(delta.Versions, version)
		applied := version.Version
		for _, current := range bindings {
			for _, head := range c.CaseSubjectRoles {
				if head.BindingID != current.BindingID || head.CaseID != current.CaseID || head.CurrentVersion != current.Version {
					continue
				}
				nextHead := head
				nextHead.CurrentVersion++
				next := current
				next.Version = nextHead.CurrentVersion
				next.SubjectVersion = version.Version
				next.CreatedByUser, next.CreatedByIssuer, next.CreatedBySubject = actor.Principal.UserID, actor.Principal.Issuer, actor.Principal.Subject
				next.CreatedAt = now
				delta.BindingHeads = append(delta.BindingHeads, BindingHeadUpdate{Expected: head, Next: nextHead})
				delta.BindingVersions = append(delta.BindingVersions, next)
			}
		}
		return delta, &applied, len(bindings), nil
	}
	if len(bindings) != 1 {
		return Delta{}, nil, 0, errors.New("binding correction target cardinality changed")
	}
	current := bindings[0]
	var after model.BindingAfterPayload
	if err := json.Unmarshal(request.CanonicalPayload, &after); err != nil {
		return Delta{}, nil, 0, err
	}
	if after.SubjectID != current.SubjectID {
		found := false
		for _, target := range c.Subjects {
			if target.TenantID == actor.TenantID && target.ID == after.SubjectID {
				found = target.CurrentVersion == after.SubjectVersion
				break
			}
		}
		if !found {
			return Delta{}, nil, 0, fault.Conflict("目标主体版本已变化")
		}
	}
	for _, head := range c.CaseSubjectRoles {
		if head.BindingID != current.BindingID || head.CaseID != current.CaseID || head.CurrentVersion != current.Version {
			continue
		}
		nextHead := head
		nextHead.CurrentVersion++
		next := current
		next.Version = nextHead.CurrentVersion
		next.SubjectID, next.SubjectVersion = after.SubjectID, after.SubjectVersion
		next.Role, next.BindingState, next.SourceKind, next.LegacyLabel = after.Role, after.BindingState, after.SourceKind, after.LegacyLabel
		next.CreatedByUser, next.CreatedByIssuer, next.CreatedBySubject = actor.Principal.UserID, actor.Principal.Issuer, actor.Principal.Subject
		next.CreatedAt = now
		delta.BindingHeads = append(delta.BindingHeads, BindingHeadUpdate{Expected: head, Next: nextHead})
		delta.BindingVersions = append(delta.BindingVersions, next)
		return delta, nil, 1, nil
	}
	return Delta{}, nil, 0, fault.Conflict("主体关联版本已变化")
}
