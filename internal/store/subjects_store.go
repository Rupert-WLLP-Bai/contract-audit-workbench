// File metadata (manifest mirror):
// Purpose: Creates or binds subjects, reads case-scoped subject summaries and records identity corrections in PostgreSQL; every write checks case membership, tenant scope and expected revision.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	subjectApp "contractaudit/internal/subject/app"
	subjectModel "contractaudit/internal/subject/model"
)

func (s *Store) CreateUnboundSubject(ctx context.Context, user accounts.User, input SubjectCreateInput, key string) (CreateSubjectResult, bool, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return CreateSubjectResult{}, false, err
	}
	if s.ReadOnly() {
		return CreateSubjectResult{}, false, fault.New(503, "read_only", "只读模式下不能创建主体")
	}
	current, err := trustedUser(user)
	if err != nil {
		return CreateSubjectResult{}, false, err
	}
	input, err = subjectApp.NormalizeUnbound(input)
	if err != nil {
		return CreateSubjectResult{}, false, err
	}
	if err := validateIdempotencyKey(key); err != nil {
		return CreateSubjectResult{}, false, err
	}
	requestHash, err := subjectApp.RequestHash(input)
	if err != nil {
		return CreateSubjectResult{}, false, err
	}
	var result CreateSubjectResult
	replayed := false
	err = s.updateAsContext(ctx, current, func(st *state) error {
		snapshot, actor := subjectCommandSnapshot(st), subjectCommandActor(current)
		var err error
		result, replayed, err = subjectApp.ReplayUnbound(snapshot, actor, key, requestHash)
		if err != nil || replayed {
			return err
		}
		delta, created, err := subjectApp.CreateUnbound(actor, input, key, requestHash, subjectCommandClock{})
		if err != nil {
			return err
		}
		result = created
		applySubjectCommandDelta(st, delta)
		return nil
	})
	return result, replayed, err
}

func (s *Store) CreateCaseSubject(ctx context.Context, user accounts.User, caseID string, input CaseSubjectCreateInput, key string) (CreateCaseSubjectResult, bool, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return CreateCaseSubjectResult{}, false, err
	}
	if s.ReadOnly() {
		return CreateCaseSubjectResult{}, false, fault.New(503, "read_only", "只读模式下不能创建案件主体")
	}
	current, err := trustedUser(user)
	if err != nil {
		return CreateCaseSubjectResult{}, false, err
	}
	caseID, input, err = subjectApp.NormalizeCaseCreate(caseID, input)
	if err != nil {
		return CreateCaseSubjectResult{}, false, err
	}
	if err := validateIdempotencyKey(key); err != nil {
		return CreateCaseSubjectResult{}, false, err
	}
	requestHash, err := subjectApp.RequestHash(input)
	if err != nil {
		return CreateCaseSubjectResult{}, false, err
	}
	var result CreateCaseSubjectResult
	replayed := false
	err = s.scopedCaseWrite(ctx, current, caseID, "", "", true, false, nil, func(st *state) error {
		caseItem, err := s.writableCase(st, current, caseID, access.Manage)
		if err != nil {
			return err
		}
		snapshot, actor := subjectCommandSnapshot(st), subjectCommandActor(current)
		result, replayed, err = subjectApp.ReplayCase(snapshot, actor, caseID, key, requestHash)
		if err != nil || replayed {
			return err
		}
		var profile subjectApp.LegacyProfile
		if input.LegacyBinding != nil {
			p, ok := profileFor(*st, *caseItem)
			profile = subjectApp.LegacyProfile{Found: ok, Version: p.Version, Customer: p.Customer, Supplier: p.Supplier}
		}
		delta, created, err := subjectApp.CreateCase(actor, subjectApp.CaseIdentity{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID}, input, profile, key, requestHash, subjectCommandClock{})
		if err != nil {
			return err
		}
		result = created
		applySubjectCommandDelta(st, delta)
		return nil
	})
	return result, replayed, err
}

func (s *Store) BindExistingSubject(ctx context.Context, user accounts.User, caseID string, input BindExistingSubjectInput, key string) (BindExistingSubjectResult, bool, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return BindExistingSubjectResult{}, false, err
	}
	if s.ReadOnly() {
		return BindExistingSubjectResult{}, false, fault.New(503, "read_only", "只读模式下不能绑定案件主体")
	}
	current, err := trustedUser(user)
	if err != nil {
		return BindExistingSubjectResult{}, false, err
	}
	caseID, input, err = subjectApp.NormalizeBind(caseID, input)
	if err != nil {
		return BindExistingSubjectResult{}, false, err
	}
	if err := validateIdempotencyKey(key); err != nil {
		return BindExistingSubjectResult{}, false, err
	}
	requestHash, err := subjectApp.RequestHash(input)
	if err != nil {
		return BindExistingSubjectResult{}, false, err
	}
	var result BindExistingSubjectResult
	replayed := false
	err = s.updateCaseAsContext(ctx, current, caseID, func(st *state) error {
		caseItem, err := s.writableCase(st, current, caseID, access.Manage)
		if err != nil {
			return err
		}
		snapshot, actor := subjectCommandSnapshot(st), subjectCommandActor(current)
		result, replayed, err = subjectApp.ReplayBinding(snapshot, actor, caseID, key, requestHash)
		if err != nil || replayed {
			return err
		}
		readable := subjectReadableBindingCases(st, current)
		delta, created, err := subjectApp.BindExisting(snapshot, actor, subjectApp.CaseIdentity{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID}, input, readable, key, requestHash, subjectCommandClock{})
		if err != nil {
			return err
		}
		result = created
		applySubjectCommandDelta(st, delta)
		return nil
	})
	return result, replayed, err
}

func (s *Store) ListCaseSubjects(ctx context.Context, user accounts.User, caseID string) (CaseSubjectListProjection, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return CaseSubjectListProjection{}, err
	}
	st, err := s.snapshotAsContext(ctx, user)
	if err != nil {
		return CaseSubjectListProjection{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return CaseSubjectListProjection{}, err
	}
	caseItem, err := s.visibleCase(&st, current, caseID)
	if err != nil {
		return CaseSubjectListProjection{}, err
	}
	if err := requireCaseAction(caseItem, current, access.Read); err != nil {
		return CaseSubjectListProjection{}, err
	}
	profile, found := profileFor(st, caseItem)
	legacy := subjectApp.LegacyProfile{}
	if found {
		legacy = subjectApp.LegacyProfile{Found: true, Version: profile.Version, Customer: profile.Customer, Supplier: profile.Supplier}
	}
	snapshot := subjectCommandSnapshot(&st)
	facts := subjectReadFacts(&st, current, s.ReadOnly(), caseItem)
	return subjectApp.ProjectCaseSubjects(snapshot, subjectCommandActor(current), facts,
		subjectApp.CaseIdentity{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseItem.ID}, legacy), nil
}

func caseSubjectCanManage(caseItem Case, user accounts.User, readOnly bool) bool {
	return subjectApp.CaseCanManage(subjectApp.CaseFact{Found: true, Manage: authorizeCase(caseItem, user, access.Manage), Writable: businessWritable(caseItem) == nil}, readOnly)
}

func (s *Store) GetSubjectDetail(ctx context.Context, user accounts.User, subjectID string) (SubjectDetailResult, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return SubjectDetailResult{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectDetailResult{}, err
	}
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectDetailResult{}, err
	}
	return subjectApp.GetSubjectDetail(subjectCommandSnapshot(&st), subjectCommandActor(current), strings.TrimSpace(subjectID), subjectReadFacts(&st, current, s.ReadOnly()))
}

func (s *Store) GetSubjectHistory(ctx context.Context, user accounts.User, subjectID string) (SubjectHistoryResult, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return SubjectHistoryResult{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectHistoryResult{}, err
	}
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectHistoryResult{}, err
	}
	return subjectApp.GetSubjectHistory(subjectCommandSnapshot(&st), subjectCommandActor(current), strings.TrimSpace(subjectID), subjectReadFacts(&st, current, s.ReadOnly()))
}

func (s *Store) ListSubjectCaseLinks(ctx context.Context, user accounts.User, subjectID, cursor string, limit int) (SubjectCaseLinksResult, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return SubjectCaseLinksResult{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectCaseLinksResult{}, err
	}
	subjectID = strings.TrimSpace(subjectID)
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectCaseLinksResult{}, err
	}
	return subjectApp.ListSubjectCaseLinks(subjectCommandSnapshot(&st), subjectCommandActor(current), subjectID, cursor, limit, subjectReadFacts(&st, current, s.ReadOnly()))
}

func (s *Store) ListSubjects(ctx context.Context, user accounts.User, caseID, query, cursor string, limit int) (SubjectListResult, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return SubjectListResult{}, err
	}
	limit, err := subjectApp.NormalizeSubjectListLimit(limit)
	if err != nil {
		return SubjectListResult{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectListResult{}, err
	}
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectListResult{}, err
	}
	if caseID != "" {
		item, err := s.visibleCase(&st, current, caseID)
		if err != nil {
			return SubjectListResult{}, err
		}
		if err := requireCaseAction(item, current, access.Manage); err != nil {
			return SubjectListResult{}, err
		}
	}
	return subjectApp.ListSubjects(subjectCommandSnapshot(&st), subjectCommandActor(current), subjectReadFacts(&st, current, s.ReadOnly()), query, cursor, limit), nil
}

func paginateSubjectSummaries(items []SubjectSummary, cursor string, limit int) SubjectListResult {
	return subjectApp.PaginateSubjectSummaries(items, cursor, limit)
}

func normalizeSubjectCreateInput(input SubjectCreateInput) (SubjectCreateInput, error) {
	return subjectModel.NormalizeSubjectCreateInput(input)
}

func newSubjectAggregate(actor accounts.User, input SubjectCreateInput, now time.Time) (Subject, SubjectVersion, error) {
	subjectID, err := newID("sub_")
	if err != nil {
		return Subject{}, SubjectVersion{}, err
	}
	principal := TrustedPrincipalProjection{UserID: actor.ID, Issuer: actor.Issuer, Subject: actor.Subject}
	return subjectModel.NewSubjectAggregate(actor.TenantID, subjectID, principal, input, now)
}

func hashSubjectVersion(version SubjectVersion) string {
	return subjectModel.HashSubjectVersion(version)
}

func validateIdempotencyKey(key string) error {
	if len(key) < 1 || len(key) > 200 {
		return fault.Invalid("Idempotency-Key长度必须是1到200字符")
	}
	for _, char := range key {
		if char < 0x21 || char > 0x7e {
			return fault.Invalid("Idempotency-Key只能包含可见ASCII字符")
		}
	}
	return nil
}

func canonicalSubjectHash(value any) (string, error) {
	return subjectModel.CanonicalSubjectHash(value)
}

func readSubjectIdempotency(st state, actor accounts.User, operation, targetID, key, payloadHash string) (SubjectIdempotencyRecord, bool, error) {
	for _, item := range st.SubjectIdempotency {
		if item.TenantID != actor.TenantID || item.ActorUserID != actor.ID || item.Operation != operation || item.TargetID != targetID || item.Key != key {
			continue
		}
		if item.PayloadHash != payloadHash {
			return SubjectIdempotencyRecord{}, false, fault.Conflict("Idempotency-Key已用于不同请求内容")
		}
		return item, true, nil
	}
	return SubjectIdempotencyRecord{}, false, nil
}

func appendSubjectIdempotency(st *state, actor accounts.User, operation, targetID, key, payloadHash string, status int, response any, now time.Time) error {
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	st.SubjectIdempotency = append(st.SubjectIdempotency, SubjectIdempotencyRecord{TenantID: actor.TenantID, ActorUserID: actor.ID, Operation: operation, TargetID: targetID, Key: key, PayloadHash: payloadHash, Response: body, HTTPStatus: status, CreatedAt: now})
	return nil
}

func subjectVisibleToUser(st state, user accounts.User, subjectID string) bool {
	return subjectApp.SubjectVisible(subjectCommandSnapshot(&st), subjectCommandActor(user), subjectID, subjectReadFacts(&st, user, false))
}

func subjectHasAnyBinding(st state, subjectID string) bool {
	return subjectModel.SubjectHasAnyBinding(st.CaseSubjectRoleVersions, subjectID)
}

func visibleSubject(st state, user accounts.User, subjectID string) (Subject, SubjectVersion, bool) {
	return subjectApp.VisibleSubjectWithFacts(subjectCommandSnapshot(&st), subjectCommandActor(user), subjectID, subjectReadFacts(&st, user, false))
}

func subjectBinding(st state, caseID, subjectID string, requireCurrent bool) (CaseSubjectRoleVersion, bool) {
	return subjectModel.SubjectBinding(st.CaseSubjectRoleVersions, st.CaseSubjectRoles, caseID, subjectID, requireCurrent)
}

func projectCaseSubjects(st state, user accounts.User, caseItem Case) CaseSubjectListProjection {
	profile, found := profileFor(st, caseItem)
	legacy := subjectApp.LegacyProfile{}
	if found {
		legacy = subjectApp.LegacyProfile{Found: true, Version: profile.Version, Customer: profile.Customer, Supplier: profile.Supplier}
	}
	facts := subjectReadFacts(&st, user, false, caseItem)
	return subjectApp.ProjectCaseSubjects(subjectCommandSnapshot(&st), subjectCommandActor(user), facts,
		subjectApp.CaseIdentity{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseItem.ID}, legacy)
}

func caseRoleCurrent(st state, head CaseSubjectRole) CaseSubjectRoleVersion {
	return subjectModel.CaseRoleCurrent(st.CaseSubjectRoleVersions, head)
}

func visibleSubjectEvents(st state, user accounts.User, subjectID string) []SubjectChangeEventSummary {
	return subjectApp.SubjectEvents(subjectCommandSnapshot(&st), subjectID, subjectReadFacts(&st, user, false))
}
