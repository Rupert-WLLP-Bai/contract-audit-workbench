// Package postgres persists Platform-owned contract-event delivery state.
package postgres

import (
	"context"
	platform "contractaudit/internal/platform/postgres"
)

// Enqueue must share the transaction that inserted the referenced event.
func Enqueue(ctx context.Context, tx platform.Executor, tenantID, eventID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO clm_outbox_delivery (tenant_id,event_id) VALUES ($1,$2)`, tenantID, eventID)
	return err
}
