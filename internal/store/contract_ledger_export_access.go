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
