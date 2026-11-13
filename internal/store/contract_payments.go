// File metadata (manifest mirror):
// Purpose: Store identity, scope, and transaction compatibility for Payment commands and reads.
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
	"contractaudit/internal/fault"
	payment "contractaudit/internal/payment/app"
	"github.com/jackc/pgx/v5"
)

const clmRoutePaymentEligibility = payment.EligibilityRoute
const clmEventPaymentReceipt = payment.ReceiptEvent

type ContractPaymentEligibilityInput = payment.ContractPaymentEligibilityInput
type ContractPaymentEligibility = payment.ContractPaymentEligibility
type ContractPaymentReceiptInput = payment.ContractPaymentReceiptInput
type TrustedPaymentSource = payment.TrustedPaymentSource
type ContractPaymentReceipt = payment.ContractPaymentReceipt
type ContractPaymentSummary = payment.ContractPaymentSummary

func (s *Store) CheckContractPaymentEligibility(ctx context.Context, user accounts.User, in ContractPaymentEligibilityInput) (ContractPaymentEligibility, error) {
	ctx = commandContext(ctx, user)
	if !s.HasDatabase() {
		return ContractPaymentEligibility{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	in, err := payment.PrepareEligibility(in)
	if err != nil {
		return ContractPaymentEligibility{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "付款核验功能当前只读", nil)
	if err != nil {
		return ContractPaymentEligibility{}, err
	}
	defer rollbackCLM(tx)
	app := newPaymentApplication(tx, current)
	result, err := payment.CheckEligibility(ctx, accounts.Tenant(current), current.ID, in, app.contracts, app.repository, app.access, app.acceptance, app.events, app.events)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ContractPaymentEligibility{}, err
	}
	return result, nil
}
func (s *Store) RecordTrustedContractPaymentReceipt(ctx context.Context, source TrustedPaymentSource, in ContractPaymentReceiptInput) (ContractPaymentReceipt, bool, error) {
	if !s.HasDatabase() {
		return ContractPaymentReceipt{}, false, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	prepared, err := payment.PrepareReceipt(source, in)
	if err != nil {
		return ContractPaymentReceipt{}, false, err
	}
	if err = s.checkCLMWriteAllowed("付款来源事实当前只读"); err != nil {
		return ContractPaymentReceipt{}, false, err
	}
	prepared, err = payment.DigestReceipt(source, prepared)
	if err != nil {
		return ContractPaymentReceipt{}, false, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ContractPaymentReceipt{}, false, err
	}
	defer rollbackCLM(tx)
	if err = setLocalScope(ctx, tx, dbScope{ActorID: "payment-source:" + source.SourceID, TenantID: source.TenantID, OrgID: "", Role: "service"}); err != nil {
		return ContractPaymentReceipt{}, false, err
	}
	app := newPaymentApplication(tx, accounts.User{})
	result, replayed, err := payment.RecordReceipt(ctx, source, prepared, app.contracts, app.repository, app.events, app.events)
	if err != nil {
		return result, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ContractPaymentReceipt{}, false, err
	}
	return result, replayed, nil
}
func (s *Store) ListContractPayments(ctx context.Context, user accounts.User, contractID string) ([]ContractPaymentSummary, error) {
	ctx = commandContext(ctx, user)
	if !s.HasDatabase() {
		return nil, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return nil, err
	}
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return nil, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollbackCLM(tx)
	if err = setLocalScope(ctx, tx, userScope(current)); err != nil {
		return nil, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return nil, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return nil, fault.NotFound("没有找到对应记录")
	}
	result, err := loadContractPaymentsTx(ctx, tx, current, record, id)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
func loadContractPaymentsTx(ctx context.Context, tx pgx.Tx, current accounts.User, record clmContractRecord, id string) ([]ContractPaymentSummary, error) {
	app := newPaymentApplication(tx, current)
	return payment.ListPayments(ctx, record, app.contracts, app.repository)
}
func boundedVisibleText(value string, max int) bool { return payment.BoundedVisibleText(value, max) }
