// File metadata (manifest mirror):
// Purpose: 实现 `ContractDeliveryInput`, `ContractDelivery`, `ContractFulfillmentItemSummary`, `ContractDeliveryList` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-13
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"math/big"
	"time"

	"contractaudit/internal/accounts"
	contractapp "contractaudit/internal/contract/app"
	clmcontract "contractaudit/internal/contract/model"
	contractpostgres "contractaudit/internal/contract/postgres"
	"contractaudit/internal/fault"
	fulfillmentapp "contractaudit/internal/fulfillment/app"
	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

const (
	clmRouteCreateDelivery        = fulfillmentapp.CreateDeliveryRoute
	clmRouteCreateAcceptance      = fulfillmentapp.CreateAcceptanceRoute
	clmRouteCreateRectification   = fulfillmentapp.CreateRectificationRoute
	clmEventDeliveryRecorded      = fulfillmentapp.DeliveryRecordedEvent
	clmEventAcceptanceRecorded    = fulfillmentapp.AcceptanceRecordedEvent
	clmEventRectificationRecorded = fulfillmentapp.RectificationRecordedEvent
)

type ContractDeliveryInput = fulfillmentapp.ContractDeliveryInput
type ContractDelivery = fulfillmentapp.ContractDelivery
type ContractFulfillmentItemSummary = fulfillmentapp.ContractFulfillmentItemSummary
type ContractDeliveryList = fulfillmentapp.ContractDeliveryList
type ContractAcceptanceDefectInput = fulfillmentapp.ContractAcceptanceDefectInput
type ContractAcceptanceDefect = fulfillmentapp.ContractAcceptanceDefect
type ContractAcceptanceInput = fulfillmentapp.ContractAcceptanceInput
type ContractAcceptance = fulfillmentapp.ContractAcceptance
type ContractAcceptanceList = fulfillmentapp.ContractAcceptanceList
type ContractRectificationInput = fulfillmentapp.ContractRectificationInput
type ContractRectification = fulfillmentapp.ContractRectification
type ContractRectificationList = fulfillmentapp.ContractRectificationList

// The public Store methods remain the transaction owner. Command preparation
// and fulfillment rules run in app; adapters receive only this authorized tx.
func (s *Store) CreateContractDelivery(ctx context.Context, user accounts.User, input ContractDeliveryInput) (ContractDelivery, error) {
	ctx = commandContext(ctx, user)
	prepared, err := fulfillmentapp.PrepareDelivery(input)
	if err != nil {
		return ContractDelivery{}, err
	}
	if !s.HasDatabase() {
		return ContractDelivery{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能记录合同交付", nil)
	if err != nil {
		return ContractDelivery{}, err
	}
	defer rollbackCLM(tx)
	services := newFulfillmentApplication(tx, current)
	result, err := fulfillmentapp.CreateContractDelivery(ctx, services.principal, prepared, services.contracts,
		services.repo, services.access, services.events, services.outbox)
	if err != nil {
		return ContractDelivery{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractDelivery{}, err
	}
	return result, nil
}

func (s *Store) ListContractDeliveries(ctx context.Context, user accounts.User, contractID string) (ContractDeliveryList, error) {
	ctx = commandContext(ctx, user)
	id, err := clmcontract.NormalizeContractUUID(contractID)
	if err != nil {
		return ContractDeliveryList{}, err
	}
	if s.pool == nil {
		return ContractDeliveryList{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractDeliveryList{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractDeliveryList{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractDeliveryList{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractDeliveryList{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return ContractDeliveryList{}, fault.NotFound("没有找到对应记录")
	}
	services := newFulfillmentApplication(tx, current)
	result, err := fulfillmentapp.ListContractDeliveries(ctx, record, services.contracts, services.repo, services.access)
	if err != nil {
		return ContractDeliveryList{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractDeliveryList{}, err
	}
	return result, nil
}

func (s *Store) CreateContractAcceptance(ctx context.Context, user accounts.User, input ContractAcceptanceInput) (ContractAcceptance, error) {
	ctx = commandContext(ctx, user)
	prepared, err := fulfillmentapp.PrepareAcceptance(input)
	if err != nil {
		return ContractAcceptance{}, err
	}
	if !s.HasDatabase() {
		return ContractAcceptance{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能登记合同验收", nil)
	if err != nil {
		return ContractAcceptance{}, err
	}
	defer rollbackCLM(tx)
	services := newFulfillmentApplication(tx, current)
	result, err := fulfillmentapp.CreateContractAcceptance(ctx, services.principal, prepared, services.contracts,
		services.repo, services.access, services.events, services.outbox)
	if err != nil {
		return ContractAcceptance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractAcceptance{}, err
	}
	return result, nil
}

func (s *Store) RecordContractRectification(ctx context.Context, user accounts.User, input ContractRectificationInput) (ContractRectification, error) {
	ctx = commandContext(ctx, user)
	prepared, err := fulfillmentapp.PrepareRectification(input)
	if err != nil {
		return ContractRectification{}, err
	}
	if !s.HasDatabase() {
		return ContractRectification{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能登记合同整改事实", nil)
	if err != nil {
		return ContractRectification{}, err
	}
	defer rollbackCLM(tx)
	services := newFulfillmentApplication(tx, current)
	result, err := fulfillmentapp.RecordContractRectification(ctx, services.principal, prepared, services.contracts,
		services.repo, services.access, services.events, services.outbox)
	if err != nil {
		return ContractRectification{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractRectification{}, err
	}
	return result, nil
}

func (s *Store) ListContractAcceptances(ctx context.Context, user accounts.User, contractID string) (ContractAcceptanceList, error) {
	ctx = commandContext(ctx, user)
	id, err := clmcontract.NormalizeContractUUID(contractID)
	if err != nil {
		return ContractAcceptanceList{}, err
	}
	if s.pool == nil {
		return ContractAcceptanceList{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractAcceptanceList{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractAcceptanceList{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractAcceptanceList{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractAcceptanceList{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return ContractAcceptanceList{}, fault.NotFound("没有找到对应记录")
	}
	services := newFulfillmentApplication(tx, current)
	result, err := fulfillmentapp.ListContractAcceptances(ctx, record, services.contracts, services.repo, services.access)
	if err != nil {
		return ContractAcceptanceList{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractAcceptanceList{}, err
	}
	return result, nil
}

// These private loaders remain transaction-local compatibility seams for the
// existing lifecycle, Payment and Asset readers until their own slices move.
func loadContractDeliveriesTx(ctx context.Context, tx pgx.Tx, current accounts.User, record clmContractRecord, id string) (ContractDeliveryList, error) {
	services := newFulfillmentApplication(tx, current)
	record.ContractID = id
	return fulfillmentapp.ListContractDeliveries(ctx, record, services.contracts, services.repo, services.access)
}

func loadContractAcceptancesTx(ctx context.Context, tx pgx.Tx, current accounts.User, record clmContractRecord, id string) (ContractAcceptanceList, error) {
	services := newFulfillmentApplication(tx, current)
	record.ContractID = id
	return fulfillmentapp.ListContractAcceptances(ctx, record, services.contracts, services.repo, services.access)
}

// Narrow compatibility helpers used by the not-yet-migrated Payment and Asset
// commands delegate rules or Contract-owned SQL to their assigned modules.
func parseFulfillmentTime(field, raw string) (time.Time, error) {
	return fulfillmentapp.ParseTime(field, raw)
}

func fulfillmentDecimal(field, raw string, allowZero bool) (*big.Rat, error) {
	return fulfillmentapp.ParseDecimal(field, raw, allowZero)
}

func ratString(value *big.Rat) string { return fulfillmentapp.RationalString(value) }

func fulfillmentResult(quantity, accepted string) (string, *big.Rat, *big.Rat, error) {
	return fulfillmentapp.ResultForQuantity(quantity, accepted)
}

func validateFulfillmentText(field, value string, max int, required bool) error {
	return fulfillmentapp.ValidateText(field, value, max, required)
}

func resolveFulfillmentEvidence(ctx context.Context, tx pgx.Tx, current accounts.User, refs []MaterialRef, purpose string) ([]ContractVersionMaterial, error) {
	return fulfillmentapp.ResolveEvidence(ctx, refs, purpose, storeFulfillmentAccess{tx: tx, user: current})
}

func requireCLMFulfillmentManage(record clmContractRecord, current accounts.User) error {
	return fulfillmentapp.RequireFulfillmentManage(context.Background(), record,
		storeFulfillmentAccess{user: current})
}

func loadFrozenFulfillmentDraft(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID string) (DraftContent, error) {
	revision, err := contractpostgres.NewFulfillmentContracts(platformpostgres.Bind(tx)).LoadFulfillmentRevision(ctx, tenantID, contractID, revisionID)
	if err != nil {
		return DraftContent{}, err
	}
	return contractapp.DecodeFulfillmentSnapshot(revision)
}

func fulfillmentWriteRecord(ctx context.Context, tx pgx.Tx, current accounts.User, contractID string) (clmContractRecord, error) {
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), contractID)
	if err != nil {
		return clmContractRecord{}, err
	}
	if err := fulfillmentapp.RequireFulfillmentManage(ctx, record, storeFulfillmentAccess{tx: tx, user: current}); err != nil {
		return clmContractRecord{}, err
	}
	return record, nil
}

func bumpFulfillmentRowVersion(ctx context.Context, tx pgx.Tx, record clmContractRecord, expected int64) (int64, error) {
	advanced, err := contractpostgres.NewFulfillmentContracts(platformpostgres.Bind(tx)).AdvanceFulfillmentAggregate(ctx, record, expected)
	return advanced.RowVersion, err
}

func fulfillmentMaterialSnapshot(ctx context.Context, tx pgx.Tx, current accounts.User, raw []byte) ([]ContractVersionMaterial, bool, error) {
	return fulfillmentapp.ProjectEvidenceSnapshot(ctx, raw, storeFulfillmentAccess{tx: tx, user: current})
}

func insertFrozenContractRevisionItems(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID string, items []DraftItem) error {
	return contractpostgres.NewVersionCommands(platformpostgres.Bind(tx)).InsertFrozenRevisionItems(ctx,
		tenantID, contractID, revisionID, clmcontract.FrozenRevisionPlanItems(items))
}
