// File metadata (manifest mirror):
// Purpose: Characterizes service-role INSERT routing and serialized cross-owner hash-chain continuation through existing case locks.
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
	"testing"
	"time"

	"contractaudit/internal/audit"
	"contractaudit/internal/audit/app"
)

func TestAuditOwnerAppendSerializesAcrossServiceRoles(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated service-role checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var coreAuditInsert, coreBusinessInsert bool
	if err := fixture.core.pool.QueryRow(ctx, `SELECT
		has_table_privilege(current_user,current_schema()||'.audit_events','INSERT'),
		has_table_privilege(current_user,current_schema()||'.business_audit_events','INSERT')`).Scan(&coreAuditInsert, &coreBusinessInsert); err != nil {
		t.Fatal(err)
	}
	if coreAuditInsert || !coreBusinessInsert {
		t.Fatalf("Core audit table privileges: audit_events=%v business_audit_events=%v", coreAuditInsert, coreBusinessInsert)
	}
	var executionAuditInsert, executionBusinessInsert bool
	if err := fixture.execution.pool.QueryRow(ctx, `SELECT
		has_table_privilege(current_user,current_schema()||'.audit_events','INSERT'),
		has_table_privilege(current_user,current_schema()||'.business_audit_events','INSERT')`).Scan(&executionAuditInsert, &executionBusinessInsert); err != nil {
		t.Fatal(err)
	}
	if !executionAuditInsert || executionBusinessInsert {
		t.Fatalf("Execution audit table privileges: audit_events=%v business_audit_events=%v", executionAuditInsert, executionBusinessInsert)
	}

	coreTx, err := fixture.core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer coreTx.Rollback(context.Background())
	if err := setLocalScope(ctx, coreTx, userScope(fixture.admin)); err != nil {
		t.Fatal(err)
	}
	if err := scopedCaseLock(ctx, coreTx, fixture.caseID); err != nil {
		t.Fatal(err)
	}
	initial, found, err := readAuditHead(ctx, coreTx, fixture.runID)
	if err != nil || !found {
		t.Fatalf("read initial execution audit head: found=%v err=%v", found, err)
	}
	coreEvent := testNextAuditRecord(t, initial, found, fixture.runID, fixture.admin.ID, "test_core_audit")
	if err := syncAudits(ctx, coreTx, []AuditRecord{coreEvent}, fixture.core.serviceOwner); err != nil {
		t.Fatalf("Core append through its routed stream: %v", err)
	}

	executionTx, err := fixture.execution.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer executionTx.Rollback(context.Background())
	if err := setLocalScope(ctx, executionTx, userScope(fixture.admin)); err != nil {
		t.Fatal(err)
	}
	lockStarted := make(chan struct{})
	lockResult := make(chan error, 1)
	go func() {
		close(lockStarted)
		lockResult <- scopedCaseLock(ctx, executionTx, fixture.caseID)
	}()
	<-lockStarted
	select {
	case err := <-lockResult:
		if err == nil {
			t.Fatal("Execution acquired the case lock while Core still held it")
		}
		t.Fatalf("Execution case-lock wait failed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if err := coreTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-lockResult:
		if err != nil {
			t.Fatalf("Execution did not acquire the case lock after Core committed: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("Execution stayed blocked after Core commit: %v", ctx.Err())
	}
	latest, found, err := readAuditHead(ctx, executionTx, fixture.runID)
	if err != nil || !found || latest.EventID != coreEvent.EventID {
		t.Fatalf("Execution did not observe the committed Core audit head: head=%+v found=%v err=%v", latest, found, err)
	}
	executionEvent := testNextAuditRecord(t, latest, found, fixture.runID, fixture.admin.ID, "test_execution_audit")
	if err := syncAudits(ctx, executionTx, []AuditRecord{executionEvent}, fixture.execution.serviceOwner); err != nil {
		t.Fatalf("Execution append through its routed stream: %v", err)
	}
	if err := executionTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	readTx, err := fixture.environment.harness.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer readTx.Rollback(context.Background())
	if err := setLocalScope(ctx, readTx, userScope(fixture.admin)); err != nil {
		t.Fatal(err)
	}
	var executionRows, businessRows int
	if err := readTx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM audit_events WHERE run_id=$1),
		(SELECT count(*) FROM business_audit_events WHERE run_id=$1)`, fixture.runID).Scan(&executionRows, &businessRows); err != nil {
		t.Fatal(err)
	}
	if executionRows != 2 || businessRows != 1 {
		t.Fatalf("owner streams contain unexpected rows: audit_events=%d business_audit_events=%d", executionRows, businessRows)
	}
	records, err := loadAudits(ctx, readTx, fixture.caseID)
	if err != nil {
		t.Fatal(err)
	}
	events := auditEventsFromRecords(records)
	if len(events) != 3 || events[0].Seq != 1 || events[1].Seq != 2 || events[2].Seq != 3 {
		t.Fatalf("merged audit sequence = %+v", events)
	}
	if events[1].EventID != coreEvent.EventID || events[2].EventID != executionEvent.EventID {
		t.Fatalf("merged streams lost owner append order: %+v", events)
	}
	if err := audit.Verify(events); err != nil {
		t.Fatalf("cross-owner append chain failed verification: %v", err)
	}
}

func testNextAuditRecord(t *testing.T, head audit.Event, found bool, runID, actor, eventType string) AuditRecord {
	t.Helper()
	id, err := newID("evt_")
	if err != nil {
		t.Fatal(err)
	}
	event, err := app.PrepareAppend(head, found, app.Draft{
		EventID: id, RunID: runID, Actor: actor, EventType: eventType,
		CorrelationIDs: []string{"test_case"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return auditRecordFromEvent(event)
}
