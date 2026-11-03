// File metadata (manifest mirror):
// Purpose: Coordinates one authorized read-only repeatable-read contract lifecycle snapshot through the existing private loaders under the documented transitional boundary.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-03
// Updated: 2026-11-03
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"contractaudit/internal/accounts"
	contract "contractaudit/internal/contract/lifecycle"
	"contractaudit/internal/fault"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

// ContractLifecycleOverview owns one authorized read-only snapshot; the existing
// loaders retain their material visibility rules and old public entry points.
func (s *Store) ContractLifecycleOverview(ctx context.Context, user accounts.User, contractID string) (contract.LifecycleOverview, error) {
	var out contract.LifecycleOverview
	ctx = commandContext(ctx, user)
	if !s.HasDatabase() {
		return out, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return out, err
	}
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return out, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer rollbackCLM(tx)
	if err = setLocalScope(ctx, tx, userScope(current)); err != nil {
		return out, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return out, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return out, fault.NotFound("没有找到对应记录")
	}
	if err = tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&out.ObservedAt); err != nil {
		return out, err
	}
	out.Contract = contract.LifecycleContract{ContractID: id, CurrentRevisionID: record.CurrentRevisionID, RowVersion: record.RowVersion, AuthorityMode: record.AuthorityMode}
	// Preserve the existing source observation's local recording time. It is
	// independent of this read and does not assert external real-time freshness.
	var sourceRecordedAt time.Time
	if record.AuthorityMode == "external" {
		err = tx.QueryRow(ctx, `SELECT recorded_at FROM clm_contract_source_observations WHERE tenant_id=$1 AND contract_id=$2 ORDER BY recorded_at DESC,observation_id DESC LIMIT 1`, record.TenantID, id).Scan(&sourceRecordedAt)
	} else if record.SourceSystem != nil {
		err = tx.QueryRow(ctx, `SELECT imported_at FROM clm_legacy_imports WHERE tenant_id=$1 AND contract_id=$2`, record.TenantID, id).Scan(&sourceRecordedAt)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if !sourceRecordedAt.IsZero() {
		out.SourceObservedAt.ContractRecordedAt = &sourceRecordedAt
	}
	out.Completeness = contract.LifecycleCompleteness{Plans: "unknown", Deliveries: "known", Acceptances: "known", Payments: "known", Assets: "known", Disputes: "unknown", Warranty: "unknown", OtherObligations: "unknown"}
	if record.AuthorityMode == "native" && record.CurrentRevisionID != nil && record.LifecycleState != "draft" {
		var snapshot []byte
		if err = tx.QueryRow(ctx, `SELECT snapshot FROM clm_contract_revisions WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`, record.TenantID, id, *record.CurrentRevisionID).Scan(&snapshot); err != nil {
			return out, err
		}
		// Select only public plan fields. Legacy malformed fields leave the plan
		// projection unknown without changing the other authorized fact loaders.
		var plans struct {
			DeliveryPlans   []DraftDeliveryPlan   `json:"delivery_plans"`
			AcceptancePlans []DraftAcceptancePlan `json:"acceptance_plans"`
			Payments        []DraftPaymentPlan    `json:"payments"`
			Assets          []DraftAssetPlan      `json:"assets"`
		}
		valid := json.Unmarshal(snapshot, &plans) == nil
		seen := map[int]bool{}
		sequence := func(n int) {
			if n < 1 || seen[n] {
				valid = false
			}
			seen[n] = true
		}
		for _, plan := range plans.DeliveryPlans {
			sequence(plan.Sequence)
		}
		seen = map[int]bool{}
		for _, plan := range plans.AcceptancePlans {
			sequence(plan.Sequence)
		}
		seen = map[int]bool{}
		for _, plan := range plans.Payments {
			sequence(plan.Sequence)
		}
		assetIDs := map[string]bool{}
		for _, plan := range plans.Assets {
			assetID, err := normalizeContractUUID(plan.AssetPlanID)
			if err != nil || assetIDs[assetID] {
				valid = false
			}
			assetIDs[assetID] = true
		}
		if valid {
			out.Plans = &contract.LifecyclePlans{DeliveryPlans: plans.DeliveryPlans, AcceptancePlans: plans.AcceptancePlans, Payments: plans.Payments, Assets: plans.Assets}
			out.Completeness.Plans = "known"
		}
	}
	deliveries, err := loadContractDeliveriesTx(ctx, tx, current, record, id)
	if err != nil {
		return out, err
	}
	acceptances, err := loadContractAcceptancesTx(ctx, tx, current, record, id)
	if err != nil {
		return out, err
	}
	payments, err := loadContractPaymentsTx(ctx, tx, current, record, id)
	if err != nil {
		return out, err
	}
	for _, payment := range payments {
		if payment.PaymentSequence < 1 || payment.ContractID != id {
			payments = nil
			out.Completeness.Payments = "unknown"
			break
		}
	}
	assets, err := loadContractAssetsTx(ctx, tx, current, record, id)
	if err != nil {
		return out, err
	}
	out.Deliveries, out.Acceptances, out.Payments, out.Assets = deliveries, acceptances, payments, assets
	if out.Completeness.Payments == "unknown" {
		out.Payments = nil
	}
	if err = tx.Commit(ctx); err != nil {
		return contract.LifecycleOverview{}, err
	}
	return out, nil
}
