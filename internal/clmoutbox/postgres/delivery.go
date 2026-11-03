// File metadata (manifest mirror):
// Purpose: Enqueues Platform-owned contract event delivery records in the caller-owned transaction without changing business event ownership or transaction lifetime.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-03
// Updated: 2026-11-03
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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
