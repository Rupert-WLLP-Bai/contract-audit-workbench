// File metadata (manifest mirror):
// Purpose: Preserves trusted identity, Case gates and original mutation or read transactions for Subject creation, binding and directory applications.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-21
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"strings"

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

func caseRoleCurrent(st state, head CaseSubjectRole) CaseSubjectRoleVersion {
	return subjectModel.CaseRoleCurrent(st.CaseSubjectRoleVersions, head)
}
