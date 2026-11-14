// File metadata (manifest mirror):
// Purpose: Provides caller-transaction Contract signing facts, ordered frozen snapshots and a pure row-version CAS shared by payment and trusted asset receipt paths.
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
	contractapp "contractaudit/internal/contract/app"
	platform "contractaudit/internal/platform/postgres"
	"errors"
	"github.com/jackc/pgx/v5"
)

type LifecycleFacts struct{ tx platform.Executor }

func NewLifecycleFacts(tx platform.Executor) *LifecycleFacts { return &LifecycleFacts{tx: tx} }
func (r *LifecycleFacts) AdvanceFactVersion(ctx context.Context, tenant, contract string, expected int64) (int64, error) {
	var version int64
	err := r.tx.QueryRow(ctx, `UPDATE clm_contracts SET row_version=row_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$3 RETURNING row_version`, tenant, contract, expected).Scan(&version)
	return version, err
}
func (r *LifecycleFacts) LoadSigningRegistration(ctx context.Context, tenant, contract, revision string) (string, bool, error) {
	var id string
	err := r.tx.QueryRow(ctx, `SELECT registration_id::text FROM clm_contract_signing_registrations WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`, tenant, contract, revision).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}
func (r *LifecycleFacts) ListRevisionSnapshots(ctx context.Context, tenant, contract string) ([]contractapp.LifecycleRevisionSnapshot, error) {
	rows, err := r.tx.Query(ctx, `SELECT contract_revision_id::text,snapshot FROM clm_contract_revisions WHERE tenant_id=$1 AND contract_id=$2 ORDER BY revision_number DESC`, tenant, contract)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []contractapp.LifecycleRevisionSnapshot
	for rows.Next() {
		var v contractapp.LifecycleRevisionSnapshot
		if err := rows.Scan(&v.RevisionID, &v.Snapshot); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
