// File metadata (manifest mirror):
// Purpose: Adapts existing current Contract Read and Export authorization to ledger export application ports within the authenticated transaction.
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

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	clmcontract "contractaudit/internal/contract/model"

	"github.com/jackc/pgx/v5"
)

// storeContractExportAccess delegates current per-contract authorization to
// the existing Store policy boundary while retaining the caller's transaction.
type storeContractExportAccess struct {
	tx   pgx.Tx
	user accounts.User
}

func (a storeContractExportAccess) CanRead(ctx context.Context, aggregate clmcontract.Aggregate) bool {
	return authorizeContractRead(ctx, a.tx, aggregate, a.user)
}

func (a storeContractExportAccess) CanExport(_ context.Context, aggregate clmcontract.Aggregate) bool {
	return authorizeContract(aggregate, a.user, access.Export)
}
