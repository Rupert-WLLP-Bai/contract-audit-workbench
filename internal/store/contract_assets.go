// File metadata (manifest mirror):
// Purpose: 实现 `ContractAssetHandoverInput`, `ContractAssetSource`, `ContractAssetReceiptInput`, `ContractAssetReceipt` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-03
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"

	"contractaudit/internal/accounts"
	assetapp "contractaudit/internal/asset/app"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

const (
	clmRouteCreateAssetHandover = assetapp.HandoverRoute
	clmEventAssetHandover       = assetapp.HandoverEvent
	clmEventAssetReceipt        = assetapp.ReceiptEvent
)

type ContractAssetHandoverInput = assetapp.ContractAssetHandoverInput
type ContractAssetSource = assetapp.ContractAssetSource
type ContractAssetReceiptInput = assetapp.ContractAssetReceiptInput
type ContractAssetReceipt = assetapp.ContractAssetReceipt
type ContractAssetHandover = assetapp.ContractAssetHandover
type ContractAssetPlanSummary = assetapp.ContractAssetPlanSummary
type ContractAssetList = assetapp.ContractAssetList

func (s *Store) CreateContractAssetHandover(ctx context.Context, user accounts.User, input ContractAssetHandoverInput) (ContractAssetHandover, error) {
	ctx = commandContext(ctx, user)
	if !s.HasDatabase() {
		return ContractAssetHandover{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	prepared, err := assetapp.PrepareHandover(input)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能登记资产移交", nil)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	defer rollbackCLM(tx)
	app := newAssetApplication(tx, current)
	result, err := assetapp.CreateHandover(ctx, accounts.Tenant(current), current.ID, prepared, app.contracts, app.repository, app.access, app.acceptance, app.directory, app.events, app.events)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ContractAssetHandover{}, err
	}
	return result, nil
}

func (s *Store) RecordTrustedContractAssetReceipt(ctx context.Context, source ContractAssetSource, input ContractAssetReceiptInput) (ContractAssetReceipt, bool, error) {
	var result ContractAssetReceipt
	if !s.HasDatabase() {
		return result, false, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	if err := s.checkCLMWriteAllowed("资产来源事实当前只读"); err != nil {
		return result, false, err
	}
	prepared, err := assetapp.PrepareReceipt(source, input)
	if err != nil {
		return result, false, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return result, false, err
	}
	defer rollbackCLM(tx)
	if err = setLocalScope(ctx, tx, dbScope{ActorID: "asset-source:" + source.SourceID, TenantID: source.TenantID, OrgID: "", Role: "service"}); err != nil {
		return result, false, err
	}
	app := newAssetApplication(tx, accounts.User{})
	result, replayed, err := assetapp.RecordReceipt(ctx, source, prepared, app.receiptContracts, app.receiptRepository, app.events, app.events)
	if err != nil {
		return result, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ContractAssetReceipt{}, false, err
	}
	return result, replayed, nil
}

func (s *Store) ListContractAssets(ctx context.Context, user accounts.User, contractID string) (ContractAssetList, error) {
	ctx = commandContext(ctx, user)
	if !s.HasDatabase() {
		return ContractAssetList{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return ContractAssetList{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractAssetList{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractAssetList{}, err
	}
	defer rollbackCLM(tx)
	if err = setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractAssetList{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractAssetList{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return ContractAssetList{}, fault.NotFound("没有找到对应记录")
	}
	result, err := loadContractAssetsTx(ctx, tx, current, record, id)
	if err != nil {
		return ContractAssetList{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractAssetList{}, err
	}
	return result, nil
}

func loadContractAssetsTx(ctx context.Context, tx pgx.Tx, current accounts.User, record clmContractRecord, id string) (ContractAssetList, error) {
	app := newAssetApplication(tx, current)
	return assetapp.ListAssets(ctx, record, app.revisions, app.listRepository, app.access)
}
