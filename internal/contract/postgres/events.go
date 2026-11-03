// File metadata (manifest mirror):
// Purpose: Appends Contract-owned aggregate business events through the existing authorized transaction shared with related business and Outbox writes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-03
// Updated: 2026-11-03
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package postgres persists Contract-owned business events.
package postgres

import (
	"context"
	platform "contractaudit/internal/platform/postgres"
)

type BusinessEvent struct {
	TenantID         string
	EventID          string
	ContractID       string
	AggregateVersion int64
	EventType        string
	Payload          []byte
}

// Append runs inside the caller's already-authorized transaction.
func Append(ctx context.Context, tx platform.Executor, event BusinessEvent) error {
	_, err := tx.Exec(ctx, `INSERT INTO clm_business_events
        (tenant_id,event_id,contract_id,aggregate_version,event_type,schema_version,payload)
        VALUES ($1,$2,$3,$4,$5,1,$6::jsonb)`,
		event.TenantID, event.EventID, event.ContractID, event.AggregateVersion, event.EventType, string(event.Payload))
	return err
}
