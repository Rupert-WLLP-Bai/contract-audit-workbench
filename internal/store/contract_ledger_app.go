// File metadata (manifest mirror):
// Purpose: Bridges Contract ledger per-row access checks to the existing Store authorization and scoped transaction.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"

	"contractaudit/internal/accounts"
	clmcontract "contractaudit/internal/contract/model"

	"github.com/jackc/pgx/v5"
)

type storeLedgerAccess struct {
	tx   pgx.Tx
	user accounts.User
}

func (a storeLedgerAccess) CanRead(ctx context.Context, record clmcontract.Aggregate) bool {
	return authorizeContractRead(ctx, a.tx, record, a.user)
}
