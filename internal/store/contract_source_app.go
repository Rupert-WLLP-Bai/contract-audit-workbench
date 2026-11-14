// File metadata (manifest mirror):
// Purpose: Adapts exact configured case authorization and active-case row locking plus object-bound raw source sealing to Contract source application ports.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"errors"
	"time"

	contractapp "contractaudit/internal/contract/app"
	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

type storeSourceObservationCaseAccess struct{ tx pgx.Tx }

func (a storeSourceObservationCaseAccess) LockActiveSourceCase(ctx context.Context,
	authority clmcontract.ContractSourceAuthority, orgID, caseID string) error {
	if !clmcontract.AllowsContractSourceCase(authority.AllowedCases, orgID, caseID) {
		return fault.NotFound("没有找到对应记录")
	}
	var archivedAt *time.Time
	if err := a.tx.QueryRow(ctx, `SELECT archived_at FROM cases WHERE tenant_id=$1 AND org_id=$2 AND id=$3 FOR KEY SHARE`,
		authority.TenantID, orgID, caseID).Scan(&archivedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.NotFound("没有找到对应记录")
		}
		return err
	}
	if archivedAt != nil {
		return fault.Conflict("来源绑定的案件已归档")
	}
	return nil
}

type storeSourceObservationRawCodec struct{}

func (storeSourceObservationRawCodec) SealSourceObservation(ctx context.Context, raw []byte,
	tenantID, sourceID, eventID, observationID string) (string, error) {
	return sealCLMSourceObservation(ctx, raw, tenantID, sourceID, eventID, observationID)
}

var _ contractapp.SourceObservationCaseAccess = storeSourceObservationCaseAccess{}
var _ contractapp.SourceObservationRawCodec = storeSourceObservationRawCodec{}
