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
