package store

import (
	"context"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

// TenantModelUsage uses the same rolling window, release exclusions, and
// conservative charges as model_budget_reserve. An expired un-settled lease
// still reserves its window tokens, but is not an active call.
type TenantModelUsage struct {
	ObservedAt        string `json:"observed_at"`
	WindowStart       string `json:"window_start"`
	WindowSeconds     int64  `json:"window_seconds"`
	ActiveCalls       int64  `json:"active_calls"`
	WindowCalls       int64  `json:"window_calls"`
	ChargedTokens     int64  `json:"charged_tokens"`
	ReservedTokens    int64  `json:"reserved_tokens"`
	UnknownUsageCalls int64  `json:"unknown_usage_calls"`
}

func (s *Store) ReadTenantModelUsage(ctx context.Context, user accounts.User) (TenantModelUsage, error) {
	current, err := trustedUser(user)
	if err != nil {
		return TenantModelUsage{}, err
	}
	if current.Role != "admin" || current.TenantID == "" {
		return TenantModelUsage{}, fault.Forbidden("只有当前租户管理员可以读取模型用量")
	}
	if !s.HasDatabase() {
		return TenantModelUsage{}, fault.New(503, "model_usage_unavailable", "模型用量账本不可用")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return TenantModelUsage{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return TenantModelUsage{}, err
	}
	var observed time.Time
	out := TenantModelUsage{WindowSeconds: 60}
	err = tx.QueryRow(ctx, `
        WITH clock AS (SELECT statement_timestamp() AS observed),
        settled AS (
            SELECT count(*) AS calls,
                COALESCE(sum(COALESCE(charged_tokens, total_tokens, COALESCE(input_tokens,0)+COALESCE(output_tokens,0))),0) AS tokens,
                count(*) FILTER (WHERE total_tokens IS NULL AND input_tokens IS NULL AND output_tokens IS NULL) AS unknown_calls
            FROM model_usage, clock
            WHERE tenant_id=$1 AND COALESCE(reserved_at,occurred_at)>=clock.observed-interval '1 minute' AND NOT budget_released
        ),
        reserved AS (
            SELECT count(*) FILTER (WHERE expires_at>clock.observed) AS active,
                count(*) FILTER (WHERE acquired_at>=clock.observed-interval '1 minute') AS calls,
                COALESCE(sum(reserved_input_tokens+reserved_output_tokens) FILTER (WHERE acquired_at>=clock.observed-interval '1 minute'),0) AS tokens
            FROM model_call_leases, clock WHERE tenant_id=$1 AND (expires_at>clock.observed OR acquired_at>=clock.observed-interval '1 minute')
        )
        SELECT clock.observed, reserved.active, settled.calls+reserved.calls, settled.tokens, reserved.tokens, settled.unknown_calls
        FROM clock CROSS JOIN settled CROSS JOIN reserved`, current.TenantID).
		Scan(&observed, &out.ActiveCalls, &out.WindowCalls, &out.ChargedTokens, &out.ReservedTokens, &out.UnknownUsageCalls)
	if err != nil {
		return TenantModelUsage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantModelUsage{}, err
	}
	out.ObservedAt = observed.UTC().Format(time.RFC3339Nano)
	out.WindowStart = observed.Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	return out, nil
}
