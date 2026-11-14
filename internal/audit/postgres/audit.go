// File metadata (manifest mirror):
// Purpose: Reads merged Audit streams and per-run/case heads and appends through caller-bound capabilities with original dual-owner SQL semantics.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package postgres adapts the Audit application contract to caller-owned SQL
// capabilities. It never owns a connection or transaction.
package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"contractaudit/internal/audit/app"
	platform "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

type Queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type Reader struct {
	query Queryer
}

func NewReader(query Queryer) Reader { return Reader{query: query} }

func (r Reader) ReadEvents(ctx context.Context, filter app.ReadFilter) ([]app.Event, error) {
	var rows pgx.Rows
	var err error
	if filter.CaseID == nil {
		rows, err = r.query.Query(ctx, `SELECT event_id,run_id,actor,seq,event_type,prev_hash,hash,correlation_ids FROM audit_events UNION ALL SELECT event_id,run_id,actor,seq,event_type,prev_hash,hash,correlation_ids FROM business_audit_events ORDER BY seq,event_id`)
	} else {
		rows, err = r.query.Query(ctx, `SELECT event_id,run_id,actor,seq,event_type,prev_hash,hash,correlation_ids FROM (SELECT event_id,run_id,actor,seq,event_type,prev_hash,hash,correlation_ids FROM audit_events UNION ALL SELECT event_id,run_id,actor,seq,event_type,prev_hash,hash,correlation_ids FROM business_audit_events) AS audit_rows WHERE run_id IN (SELECT id FROM review_runs WHERE case_id=$1) ORDER BY seq,event_id`, *filter.CaseID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func (r Reader) ReadHead(ctx context.Context, ref app.RunRef) (app.Event, bool, error) {
	rows, err := r.query.Query(ctx, `SELECT * FROM (
		(SELECT event_id,run_id,actor,seq,event_type,prev_hash,hash,correlation_ids FROM audit_events WHERE run_id=$1 ORDER BY seq DESC,event_id DESC LIMIT 1)
		UNION ALL
		(SELECT event_id,run_id,actor,seq,event_type,prev_hash,hash,correlation_ids FROM business_audit_events WHERE run_id=$1 ORDER BY seq DESC,event_id DESC LIMIT 1)
	) heads ORDER BY seq DESC,event_id DESC LIMIT 1`, ref.ID)
	if err != nil {
		return app.Event{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return app.Event{}, false, rows.Err()
	}
	event, err := scanEvent(rows)
	if err != nil {
		return app.Event{}, false, err
	}
	rows.Next()
	return event, true, rows.Err()
}

func (r Reader) ReadCaseHeads(ctx context.Context, caseIDs []string) ([]app.Event, error) {
	if len(caseIDs) == 0 {
		return nil, nil
	}
	rows, err := r.query.Query(ctx, `SELECT h.event_id,h.run_id,h.actor,h.seq,h.event_type,h.prev_hash,h.hash,h.correlation_ids
		FROM review_runs r CROSS JOIN LATERAL (
			SELECT * FROM (
				(SELECT event_id,run_id,actor,seq,event_type,prev_hash,hash,correlation_ids FROM audit_events WHERE run_id=r.id ORDER BY seq DESC,event_id DESC LIMIT 1)
				UNION ALL
				(SELECT event_id,run_id,actor,seq,event_type,prev_hash,hash,correlation_ids FROM business_audit_events WHERE run_id=r.id ORDER BY seq DESC,event_id DESC LIMIT 1)
			) candidates ORDER BY seq DESC,event_id DESC LIMIT 1
		) h WHERE r.case_id=ANY($1) ORDER BY h.seq,h.event_id`, caseIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

type Writer struct {
	executor platform.Executor
}

func NewWriter(executor platform.Executor) Writer { return Writer{executor: executor} }

func (w Writer) Append(ctx context.Context, stream app.Stream, event app.Event) error {
	table, err := tableFor(stream)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(event.CorrelationIDs)
	if err != nil {
		return err
	}
	_, err = w.executor.Exec(ctx, `INSERT INTO `+table+` (event_id, run_id, actor, seq, event_type, prev_hash, hash, correlation_ids) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		event.EventID, event.RunID, event.Actor, event.Seq, event.EventType, event.PrevHash, event.Hash, string(raw))
	return err
}

func tableFor(stream app.Stream) (string, error) {
	switch stream {
	case app.AuditStream:
		return "audit_events", nil
	case app.BusinessAuditStream:
		return "business_audit_events", nil
	default:
		return "", errors.New("unknown audit stream")
	}
}

func scanEvents(rows pgx.Rows) ([]app.Event, error) {
	var events []app.Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func scanEvent(row pgx.Row) (app.Event, error) {
	var event app.Event
	var raw string
	if err := row.Scan(&event.EventID, &event.RunID, &event.Actor, &event.Seq, &event.EventType,
		&event.PrevHash, &event.Hash, &raw); err != nil {
		return app.Event{}, err
	}
	if err := json.Unmarshal([]byte(raw), &event.CorrelationIDs); err != nil {
		return app.Event{}, err
	}
	return event, nil
}
