// File metadata (manifest mirror):
// Purpose: Reads and inserts immutable Contract ledger export artifacts and idempotency locks through the caller-owned transaction executor.
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
	"errors"

	"contractaudit/internal/contract/ledger"
	platform "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

// LedgerExports persists and reads immutable Contract ledger export artifacts
// through the caller's transaction. It does not own transaction lifetime,
// scope, authorization or Contract aggregate reads.
type LedgerExports struct{ tx platform.Executor }

func NewLedgerExports(tx platform.Executor) *LedgerExports { return &LedgerExports{tx: tx} }

func (r *LedgerExports) LockIdempotencyKey(ctx context.Context, identity ledger.ExportIdentity, key string) error {
	_, err := r.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()
		|| ':clm-export:' || $1 || ':' || $2 || ':' || $3, 0))`, identity.TenantID, identity.ActorID, key)
	return err
}

func (r *LedgerExports) LoadByIdempotencyKey(ctx context.Context, identity ledger.ExportIdentity,
	key string) (ledger.ExportReplay, bool, error) {
	var replay ledger.ExportReplay
	err := r.tx.QueryRow(ctx, `SELECT export_id,format,columns,row_count,created_at,expires_at,request_digest
		FROM clm_contract_exports WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3`,
		identity.TenantID, identity.ActorID, key).Scan(&replay.Export.ExportID, &replay.Export.Format,
		&replay.ColumnsJSON, &replay.Export.RowCount, &replay.Export.CreatedAt, &replay.Export.ExpiresAt,
		&replay.RequestDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.ExportReplay{}, false, nil
	}
	if err != nil {
		return ledger.ExportReplay{}, false, err
	}
	return replay, true, nil
}

func (r *LedgerExports) Insert(ctx context.Context, item ledger.ExportInsert) (ledger.Export, error) {
	result := ledger.Export{
		ExportID: item.ExportID, State: "ready", Format: item.Format,
		RowCount: item.RowCount, DownloadAvailable: true,
	}
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_contract_exports
		(tenant_id,export_id,actor_id,idempotency_key,request_digest,filter,columns,format,
		 contract_ids,rows,row_count,policy_version,grants_digest,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8,$9::jsonb,$10::jsonb,$11,$12,$13,
			clock_timestamp()+interval '24 hours')
		RETURNING created_at,expires_at`,
		item.TenantID, item.ExportID, item.ActorID, item.IdempotencyKey, item.RequestDigest,
		string(item.FilterJSON), string(item.ColumnsJSON), item.Format, string(item.ContractIDs),
		string(item.RowsJSON), item.RowCount, item.PolicyVersion, item.GrantsDigest).
		Scan(&result.CreatedAt, &result.ExpiresAt)
	if err != nil {
		return ledger.Export{}, err
	}
	return result, nil
}

func (r *LedgerExports) Load(ctx context.Context, identity ledger.ExportIdentity,
	exportID string) (ledger.ExportArtifact, bool, error) {
	var artifact ledger.ExportArtifact
	err := r.tx.QueryRow(ctx, `SELECT actor_id,format,columns,contract_ids,rows,row_count,policy_version,expires_at
		FROM clm_contract_exports WHERE tenant_id=$1 AND export_id=$2`, identity.TenantID, exportID).
		Scan(&artifact.ActorID, &artifact.Format, &artifact.ColumnsJSON, &artifact.ContractIDs,
			&artifact.RowsJSON, &artifact.RowCount, &artifact.PolicyVersion, &artifact.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.ExportArtifact{}, false, nil
	}
	if err != nil {
		return ledger.ExportArtifact{}, false, err
	}
	return artifact, true, nil
}
