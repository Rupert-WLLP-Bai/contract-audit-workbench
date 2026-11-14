// File metadata (manifest mirror):
// Purpose: Preserves trusted Case gates and original mapping transactions while adapting narrow material or frozen-source proofs and owned application records.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-14
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	operatorApp "contractaudit/internal/operator/app"
	operatorModel "contractaudit/internal/operator/model"
	operatorPostgres "contractaudit/internal/operator/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

func bindMappingAssociation(plan *OperatorMappingChangePlan, association *operatorMappingAssociationAfter) error {
	return operatorModel.BindOperatorMappingAssociation(plan, association)
}

// PreviewOperatorMappingChange returns the exact current tuple and proposed
// after state. The mutation method recomputes every field under the case lock.
func (s *Store) PreviewOperatorMappingChange(ctx context.Context, user accounts.User, caseID, mappingID string,
	cas OperatorMappingChangeCAS, patch OperatorMappingChangePatch, basisRefs json.RawMessage) (OperatorMappingChangePreview, error) {
	var out OperatorMappingChangePreview
	current, _, _, _, _, err := s.operatorCaseState(ctx, user, caseID, access.Manage)
	if err != nil {
		return out, err
	}
	basisRefs, err = operatorApp.NormalizeMappingBasis(basisRefs)
	if err != nil {
		return out, err
	}
	err = s.withOperatorTx(ctx, current, caseID, false, func(tx pgx.Tx) error {
		fresh, err := loadState(ctx, tx, false)
		if err != nil {
			return err
		}
		freshCase, err := s.visibleCase(&fresh, current, caseID)
		if err != nil {
			return err
		}
		if !authorizeCase(freshCase, current, access.Manage) {
			return fault.Forbidden("当前授权不能维护对象映射")
		}
		if err := businessWritable(freshCase); err != nil {
			return err
		}
		out, err = operatorApp.PreviewMapping[Document](ctx, mappingRepo(tx), mappingFacts(s, tx, fresh, current, freshCase), mappingBasisFacts{st: fresh, item: freshCase}, mappingScope(freshCase), current.ID, mappingID, cas, patch, basisRefs, mappingClock{})
		return err
	})
	return out, err
}

// ProposeOperatorMappingChange stores an immutable two-person proposal. It
// never mutates the lineage head; approval is a separate CAS-bound decision.
func (s *Store) ProposeOperatorMappingChange(ctx context.Context, user accounts.User, caseID, mappingID string,
	input OperatorMappingChangeCreateInput, key string) (OperatorMappingChangeRequest, bool, error) {
	var empty OperatorMappingChangeRequest
	if s.ReadOnly() {
		return empty, false, fault.New(503, "read_only", "只读模式下不能提交对象映射更正")
	}
	current, err := trustedUser(user)
	if err != nil {
		return empty, false, err
	}
	if _, _, _, _, _, err := s.operatorCaseState(ctx, current, caseID, access.Manage); err != nil {
		return empty, false, err
	}
	if err := validateIdempotencyKey(key); err != nil {
		return empty, false, err
	}
	identity := operatorApp.CreateIdentity{TenantID: current.TenantID, CaseID: caseID, ActorID: current.ID}
	prepared, err := operatorApp.PrepareProposal(identity, mappingID, key, input)
	if err != nil {
		return empty, false, err
	}
	caseID = prepared.Identity.CaseID
	var result OperatorMappingChangeRequest
	replayed := false
	err = s.withOperatorTx(ctx, current, caseID, true, func(tx pgx.Tx) error {
		fresh, err := loadState(ctx, tx, true)
		if err != nil {
			return err
		}
		caseItem, err := s.visibleCase(&fresh, current, caseID)
		if err != nil {
			return err
		}
		if !authorizeCase(caseItem, current, access.Manage) {
			return fault.Forbidden("当前授权不能维护对象映射")
		}
		if err := businessWritable(caseItem); err != nil {
			return err
		}
		prepared.Identity.OrgID = caseItem.OwnerOrgID
		result, replayed, err = operatorApp.ProposeMapping[Document](ctx, mappingRepo(tx), mappingFacts(s, tx, fresh, current, caseItem), mappingBasisFacts{st: fresh, item: caseItem}, mappingScope(caseItem), mappingPrincipal(current), mappingClock{}, prepared)
		return err
	})
	return result, replayed, err
}

func (s *Store) GetOperatorMappingChange(ctx context.Context, user accounts.User, caseID, requestID string) (OperatorMappingChangeRequest, *OperatorMappingChangeDecision, error) {
	ctx = commandContext(ctx, user)
	current, err := trustedUser(user)
	if err != nil {
		return OperatorMappingChangeRequest{}, nil, err
	}
	if _, _, _, _, _, err := s.operatorCaseState(ctx, current, caseID, access.Read); err != nil {
		return OperatorMappingChangeRequest{}, nil, err
	}
	var request OperatorMappingChangeRequest
	var decision *OperatorMappingChangeDecision
	err = s.withOperatorTx(ctx, current, caseID, false, func(tx pgx.Tx) error {
		fresh, err := loadState(ctx, tx, false)
		if err != nil {
			return err
		}
		caseItem, err := s.visibleCase(&fresh, current, caseID)
		if err != nil {
			return err
		}
		if !authorizeCase(caseItem, current, access.Read) {
			return fault.NotFound("没有找到对象映射更正")
		}
		request, decision, err = operatorApp.GetMappingChange(ctx, mappingRepo(tx), operatorApp.ReadIdentity{TenantID: current.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID}, requestID)
		return err
	})
	return request, decision, err
}

// DecideOperatorMappingChange appends exactly one trusted-principal decision.
// Approval appends the next immutable mapping version and advances the lineage
// head with the same CAS; rejection leaves that head untouched.
func (s *Store) DecideOperatorMappingChange(ctx context.Context, user accounts.User, caseID, requestID string,
	input OperatorMappingChangeDecisionInput, key string) (OperatorMappingChangeDecisionResult, bool, error) {
	var empty OperatorMappingChangeDecisionResult
	if s.ReadOnly() {
		return empty, false, fault.New(503, "read_only", "只读模式下不能决定对象映射更正")
	}
	current, err := trustedUser(user)
	if err != nil {
		return empty, false, err
	}
	if !accounts.CanDecide(current.Role) {
		return empty, false, fault.Forbidden("当前角色没有对象映射更正决定权限")
	}
	if _, _, _, _, _, err := s.operatorCaseState(ctx, current, caseID, access.Decide); err != nil {
		return empty, false, err
	}
	if err := validateIdempotencyKey(key); err != nil {
		return empty, false, err
	}
	prepared, err := operatorApp.PrepareMappingDecision(caseID, requestID, input)
	if err != nil {
		return empty, false, err
	}
	var result OperatorMappingChangeDecisionResult
	replayed := false
	err = s.withOperatorTx(ctx, current, caseID, true, func(tx pgx.Tx) error {
		fresh, err := loadState(ctx, tx, true)
		if err != nil {
			return err
		}
		caseItem, err := s.visibleCase(&fresh, current, caseID)
		if err != nil {
			return err
		}
		if !authorizeCase(caseItem, current, access.Decide) {
			return fault.NotFound("没有找到对象映射更正")
		}
		if err := businessWritable(caseItem); err != nil {
			return err
		}
		id := operatorApp.CreateIdentity{TenantID: current.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID, ActorID: current.ID}
		result, replayed, err = operatorApp.DecideMapping[Document](ctx, mappingRepo(tx), mappingFacts(s, tx, fresh, current, caseItem), mappingBasisFacts{st: fresh, item: caseItem}, mappingScope(caseItem), mappingPrincipal(current), mappingClock{}, id, requestID, key, prepared)
		return err
	})
	return result, replayed, err
}

func loadOperatorBindingRecordByVersion(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, bindingVersion string) (operatorObjectBindingRecord, error) {
	return operatorApp.ReadBindingVersion(ctx, operatorPostgres.NewReads(platformpostgres.Bind(tx)), operatorApp.ReadIdentity{TenantID: tenantID, OrgID: orgID, CaseID: caseID}, bindingVersion)
}

func loadOperatorBindingByVersion(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, bindingVersion string) (OperatorObjectBinding, error) {
	record, err := loadOperatorBindingRecordByVersion(ctx, tx, tenantID, orgID, caseID, bindingVersion)
	return record.Value, err
}
