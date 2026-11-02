package store

import (
	"context"
	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
	"encoding/json"
	"strconv"

	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// beginContextTx is the common Store transaction seam. Machine facts with no
// human admission remain independent; human commands carry a private proof.
func (s *Store) beginContextTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	if _, ok := contextProof(ctx); !ok && options.AccessMode != pgx.ReadOnly {
		if scope, delegated := model.DelegatedScopeFromContext(ctx); delegated && scope.Kind != "" {
			user, err := s.operationUser(ctx, scope)
			if err != nil {
				return nil, err
			}
			ctx = commandContext(ctx, user)
		}
	}
	tx, err := s.pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	proof, ok := contextProof(ctx)
	if !ok {
		if _, required := ctx.Value(commandProofKey{}).(commandProof); required {
			_ = tx.Rollback(ctx)
			return nil, fault.New(401, "session_invalid", "缺少原始会话授权")
		}
		return tx, nil
	}
	if options.AccessMode == pgx.ReadOnly {
		return tx, nil
	}
	user, _ := ctx.Value(commandUserKey{}).(accounts.User)
	f := &contextTx{Tx: tx, proof: proof, user: user}
	if err = f.recheck(ctx); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return f, nil
}

type contextTx struct {
	pgx.Tx
	proof commandProof
	user  accounts.User
}

func (t *contextTx) recheck(ctx context.Context) error {
	if len(t.proof.operation) > 0 {
		var scope model.DelegatedScope
		if json.Unmarshal(t.proof.operation, &scope) != nil {
			return fault.Forbidden("执行授权范围无效")
		}
		_, err := t.Tx.Exec(ctx, `SELECT tenant_operation_fence($1::jsonb,$2,$3)`, t.proof.operation, identityTokenDigest(scope.OperationTicket), t.proof.revision)
		return contextError(err)
	}
	version, err := strconv.ParseInt(t.proof.version, 10, 64)
	if err != nil || version < 1 {
		return fault.New(428, "context_required", "上下文版本不可用")
	}
	var support any
	if t.proof.support != "" {
		support = t.proof.support
	}
	_, err = t.Tx.Exec(ctx, `SELECT tenant_context_fence($1,$2,$3,NULL,$4::uuid)`, t.proof.digest, version, t.proof.revision, support)
	if err != nil {
		return contextError(err)
	}
	if t.proof.supportRead {
		_, err = t.Tx.Exec(ctx, `SELECT tenant_support_validate($1,$2::uuid)`, t.proof.digest, t.proof.support)
	}
	return contextError(err)
}
func (t *contextTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if err := t.recheck(ctx); err != nil {
		return pgconn.CommandTag{}, err
	}
	tag, err := t.Tx.Exec(ctx, sql, args...)
	return tag, contextError(err)
}
func (t *contextTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if err := t.recheck(ctx); err != nil {
		return nil, err
	}
	rows, err := t.Tx.Query(ctx, sql, args...)
	return rows, contextError(err)
}

type contextErrorRow struct{ err error }

func (r contextErrorRow) Scan(...any) error { return r.err }
func (t *contextTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if err := t.recheck(ctx); err != nil {
		return contextErrorRow{err}
	}
	return t.Tx.QueryRow(ctx, sql, args...)
}
func (t *contextTx) Commit(ctx context.Context) error {
	// This is also after any blocking DML's internal row locks. Expiry or a lost
	// capability rolls back facts, receipts and Outbox together, never only a view.
	if err := t.recheck(ctx); err != nil {
		_ = t.Tx.Rollback(ctx)
		return err
	}
	return contextError(t.Tx.Commit(ctx))
}
func (t *contextTx) Begin(ctx context.Context) (pgx.Tx, error) {
	if err := t.recheck(ctx); err != nil {
		return nil, err
	}
	nested, err := t.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &contextTx{Tx: nested, proof: t.proof, user: t.user}, nil
}

// All human SQL is checked, not just text beginning with INSERT: writable CTEs
// and SECURITY DEFINER SELECT commands pass through the same seam.
var _ pgx.Tx = (*contextTx)(nil)

func (t *contextTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, fault.Forbidden("围栏事务不允许直接COPY")
}
func (t *contextTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return rejectedContextBatch{}
}

type rejectedContextBatch struct{}

func (rejectedContextBatch) Exec() (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fault.Forbidden("围栏事务不允许批量旁路")
}
func (rejectedContextBatch) Query() (pgx.Rows, error) {
	return nil, fault.Forbidden("围栏事务不允许批量旁路")
}
func (rejectedContextBatch) QueryRow() pgx.Row {
	return contextErrorRow{fault.Forbidden("围栏事务不允许批量旁路")}
}
func (rejectedContextBatch) Close() error {
	return fault.Forbidden("围栏事务不允许批量旁路")
}

// No current Store consumer uses these APIs. pgx.Tx fixes their return types,
// so a zero handle/nil connection deliberately exposes no underlying authority.
func (t *contextTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }
func (t *contextTx) Conn() *pgx.Conn                { return nil }

// Lease cleanup and already-incurred accounting are exact machine facts. This
// private classifier never admits a new human action or changes its identity.
func machineFactContext(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, commandProofKey{}, struct{}{})
	return model.WithDelegatedScope(ctx, model.DelegatedScope{})
}
