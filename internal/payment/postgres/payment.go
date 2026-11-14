// File metadata (manifest mirror):
// Purpose: Implements Payment-owned source event locks and eligibility/receipt facts through a caller-bound executor without querying Contract or Fulfillment tables.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package postgres

import (
	"context"
	payment "contractaudit/internal/payment/app"
	platform "contractaudit/internal/platform/postgres"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

type Repository struct{ tx platform.Executor }

func NewRepository(tx platform.Executor) *Repository { return &Repository{tx: tx} }
func (r *Repository) LockSourceEvent(ctx context.Context, tenant, source, event string) error {
	raw, err := json.Marshal([]string{tenant, source, event})
	if err != nil {
		return err
	}
	h := sha256.Sum256(raw)
	_, err = r.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':clm-payment-event:' || $1,0))`, hex.EncodeToString(h[:]))
	return err
}
func (r *Repository) LoadReceipt(ctx context.Context, tenant, source, event string) (payment.ContractPaymentReceipt, bool, error) {
	var v payment.ContractPaymentReceipt
	err := r.tx.QueryRow(ctx, `SELECT receipt_id::text,contract_id::text,contract_revision_id::text,payment_sequence,source_id,source_key_id,source_event_id,event_type,COALESCE(original_source_event_id,''),amount::text,currency,occurred_at,recorded_at,payload_digest FROM clm_contract_payment_receipts WHERE tenant_id=$1 AND source_id=$2 AND source_event_id=$3`, tenant, source, event).Scan(&v.ReceiptID, &v.ContractID, &v.ContractRevisionID, &v.PaymentSequence, &v.SourceID, &v.SourceKeyID, &v.SourceEventID, &v.EventType, &v.OriginalSourceEventID, &v.Amount, &v.Currency, &v.OccurredAt, &v.RecordedAt, &v.PayloadDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}
func (r *Repository) SumReversals(ctx context.Context, tenant, source, event string) (string, error) {
	var value string
	err := r.tx.QueryRow(ctx, `SELECT COALESCE(sum(amount),0)::text FROM clm_contract_payment_receipts WHERE tenant_id=$1 AND source_id=$2 AND original_source_event_id=$3 AND event_type='reversal'`, tenant, source, event).Scan(&value)
	return value, err
}
func (r *Repository) InsertReceipt(ctx context.Context, tenant string, v payment.ContractPaymentReceipt) (time.Time, error) {
	var at time.Time
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_contract_payment_receipts(tenant_id,contract_id,contract_revision_id,receipt_id,payment_sequence,source_id,source_key_id,source_event_id,event_type,original_source_event_id,amount,currency,occurred_at,payload_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11::numeric,$12,$13,$14) RETURNING recorded_at`, tenant, v.ContractID, v.ContractRevisionID, v.ReceiptID, v.PaymentSequence, v.SourceID, v.SourceKeyID, v.SourceEventID, v.EventType, v.OriginalSourceEventID, v.Amount, v.Currency, v.OccurredAt, v.PayloadDigest).Scan(&at)
	return at, err
}
func (r *Repository) InsertEligibility(ctx context.Context, tenant, actor string, v payment.ContractPaymentEligibility) (time.Time, error) {
	reasons, err := json.Marshal(v.Reasons)
	if err != nil {
		return time.Time{}, err
	}
	evidence, err := json.Marshal(v.Evidence)
	if err != nil {
		return time.Time{}, err
	}
	var at time.Time
	err = r.tx.QueryRow(ctx, `INSERT INTO clm_contract_payment_eligibility_checks (tenant_id,contract_id,contract_revision_id,check_id,payment_sequence,status,reasons,evidence,checked_by,contract_row_version) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9,$10) RETURNING checked_at`, tenant, v.ContractID, v.ContractRevisionID, v.CheckID, v.PaymentSequence, v.Status, string(reasons), string(evidence), actor, v.ContractRowVersion).Scan(&at)
	return at, err
}
func (r *Repository) ListReceipts(ctx context.Context, tenant, contract, revision string, sequence int) ([]payment.ContractPaymentReceipt, error) {
	rows, err := r.tx.Query(ctx, `SELECT receipt_id::text,contract_id::text,contract_revision_id::text,payment_sequence,source_id,source_key_id,source_event_id,event_type,COALESCE(original_source_event_id,''),amount::text,currency,occurred_at,recorded_at,payload_digest FROM clm_contract_payment_receipts WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND payment_sequence=$4 ORDER BY occurred_at,receipt_id`, tenant, contract, revision, sequence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []payment.ContractPaymentReceipt{}
	for rows.Next() {
		var v payment.ContractPaymentReceipt
		if err := rows.Scan(&v.ReceiptID, &v.ContractID, &v.ContractRevisionID, &v.PaymentSequence, &v.SourceID, &v.SourceKeyID, &v.SourceEventID, &v.EventType, &v.OriginalSourceEventID, &v.Amount, &v.Currency, &v.OccurredAt, &v.RecordedAt, &v.PayloadDigest); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
