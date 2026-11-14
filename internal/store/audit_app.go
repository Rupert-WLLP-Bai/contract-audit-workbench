// File metadata (manifest mirror):
// Purpose: Maps Audit compatibility records and service-owner routes into domain ports while preserving caller transaction and Query-only read capabilities.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"errors"

	"contractaudit/internal/audit"
	auditapp "contractaudit/internal/audit/app"
	auditpostgres "contractaudit/internal/audit/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

func auditStreamForOwner(owner string) (auditapp.Stream, error) {
	switch auditTableFor(owner) {
	case "audit_events":
		return auditapp.AuditStream, nil
	case "business_audit_events":
		return auditapp.BusinessAuditStream, nil
	default:
		return 0, errors.New("unknown audit table route")
	}
}

func readAuditRecords(ctx context.Context, query queryer, caseID ...string) ([]AuditRecord, error) {
	filter := auditapp.ReadFilter{}
	if len(caseID) != 0 {
		selected := caseID[0]
		filter.CaseID = &selected
	}
	events, err := (auditapp.Service{Reader: auditpostgres.NewReader(query)}).Read(ctx, filter)
	if err != nil {
		return nil, err
	}
	return auditRecordsFromEvents(events), nil
}

func readAuditHead(ctx context.Context, query queryer, runID string) (audit.Event, bool, error) {
	return (auditapp.Service{Reader: auditpostgres.NewReader(query)}).Head(ctx, auditapp.RunRef{ID: runID})
}

func readAuditCaseHeads(ctx context.Context, query queryer, caseIDs []string) ([]AuditRecord, error) {
	events, err := (auditapp.Service{Reader: auditpostgres.NewReader(query)}).CaseHeads(ctx, caseIDs)
	if err != nil {
		return nil, err
	}
	return auditRecordsFromEvents(events), nil
}

func persistAuditRecords(ctx context.Context, tx pgx.Tx, items []AuditRecord, owner string) error {
	if len(items) == 0 {
		return nil
	}
	stream, err := auditStreamForOwner(owner)
	if err != nil {
		return err
	}
	writer := auditpostgres.NewWriter(platformpostgres.Bind(tx))
	return (auditapp.Service{Writer: writer}).Persist(ctx, stream, auditEventsFromRecords(items))
}

func auditEventsFromRecords(records []AuditRecord) []audit.Event {
	if records == nil {
		return nil
	}
	events := make([]audit.Event, len(records))
	for i, record := range records {
		events[i] = record.event()
	}
	return events
}

func auditRecordsFromEvents(events []audit.Event) []AuditRecord {
	var records []AuditRecord
	for _, event := range events {
		records = append(records, auditRecordFromEvent(event))
	}
	return records
}

func auditRecordFromEvent(event audit.Event) AuditRecord {
	return AuditRecord{
		EventID: event.EventID, RunID: event.RunID, Actor: event.Actor, Seq: event.Seq,
		EventType: event.EventType, PrevHash: event.PrevHash, Hash: event.Hash,
		CorrelationIDs: cloneAuditCorrelationIDs(event.CorrelationIDs),
	}
}

func cloneAuditCorrelationIDs(ids []string) []string {
	if ids == nil {
		return nil
	}
	copyOfIDs := make([]string, len(ids))
	copy(copyOfIDs, ids)
	return copyOfIDs
}
