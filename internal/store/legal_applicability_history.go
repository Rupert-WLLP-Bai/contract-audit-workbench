// File metadata (manifest mirror):
// Purpose: Adapts Legal fact history to the existing scoped read transaction, current case grants, and final authorization fence.
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

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	legalApp "contractaudit/internal/legal/app"
	legalModel "contractaudit/internal/legal/model"
	legalPostgres "contractaudit/internal/legal/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type LegalApplicabilityHistoryInput = legalModel.LegalApplicabilityHistoryInput
type LegalApplicabilityFactHistory = legalModel.LegalApplicabilityFactHistory

func (s *Store) ListLegalApplicabilityFactHistory(ctx context.Context, user accounts.User, caseID, dimension string, input LegalApplicabilityHistoryInput) (LegalApplicabilityFactHistory, error) {
	ctx = commandContext(ctx, user)
	if err := s.legalReady(false); err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return LegalApplicabilityFactHistory{}, fault.Forbidden("只有 Core 服务可以读取法律适用事实历史")
	}
	if ctx == nil {
		return LegalApplicabilityFactHistory{}, fault.Invalid("法律适用事实历史读取需要请求上下文")
	}
	current, err := trustedUser(user)
	if err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	limit, err := legalApp.ValidateHistoryInput(caseID, dimension, input)
	if err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	defer tx.Rollback(ctx)
	scope := userScope(current)
	scope.CaseID = caseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	if err := scopedCaseLock(ctx, tx, caseID); err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	caseItem, err := loadKnowledgeCase(ctx, tx, current, caseID)
	if err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	if !authorizeCase(caseItem, current, access.Read) {
		return LegalApplicabilityFactHistory{}, fault.NotFound("没有找到对应案件")
	}
	result, err := legalApp.ReadApplicabilityHistory(ctx, legalPostgres.NewApplicability(platformpostgres.Bind(tx)), legalApp.RecordIdentity{TenantID: accounts.Tenant(current), OrgID: caseItem.OwnerOrgID, CaseID: caseID}, dimension, input, limit)
	if err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	current, err = trustedUser(current)
	if err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	if !authorizeCase(caseItem, current, access.Read) {
		return LegalApplicabilityFactHistory{}, fault.NotFound("没有找到对应案件")
	}
	canManage := !s.ReadOnly() && authorizeCase(caseItem, current, access.Manage) && businessWritable(caseItem) == nil
	if err := tx.Commit(ctx); err != nil {
		return LegalApplicabilityFactHistory{}, err
	}
	result.CanManage = canManage
	return result, nil
}
