// File metadata (manifest mirror):
// Purpose: Provides a non-owning SQL executor for trusted PostgreSQL adapters while preserving the original transaction wrapper, scope, authority fences and caller-owned commit or rollback.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-03
// Updated: 2026-11-03
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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
