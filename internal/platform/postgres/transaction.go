// Package postgres defines a non-owning SQL handle for transaction-aware adapters.
package postgres

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Executor is a transaction-bound SQL capability. Its holder cannot start or
// finish a transaction or retrieve a connection through this interface.
// Only trusted in-process adapters may receive this handle. It provides no
// scope or identity API; authorization and transaction-local scope remain the
// responsibility of the command that owns the transaction.
type Executor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type executor struct{ tx pgx.Tx }

// Bind preserves the exact transaction wrapper, including per-statement and
// commit-time fences. The caller remains responsible for authorization, scope,
// transaction lifetime, rollback and commit.
func Bind(tx pgx.Tx) Executor { return executor{tx: tx} }

func (e executor) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return e.tx.Exec(ctx, sql, args...)
}
