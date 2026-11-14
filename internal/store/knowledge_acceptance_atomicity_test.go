// File metadata (manifest mirror):
// Purpose: Pins Execution-role review acceptance rollback after deferred Outbox and receipt writes, together with frozen identity error precedence.
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
	"reflect"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
)

// Reads committed rows under the same execution role and case scope as admission.
func acceptanceAtomicSnapshot(t *testing.T, execution *Store, actor accounts.User, caseID string) map[string]string {
	t.Helper()
	ctx := context.Background()
	tx, err := execution.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scope := userScope(actor)
	scope.CaseID = caseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		t.Fatal(err)
	}
	queries := map[string]string{
		"case":      `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM cases t WHERE id=$1`,
		"runs":      `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM review_runs t WHERE case_id=$1`,
		"attempts":  `SELECT COALESCE(jsonb_agg(to_jsonb(a) ORDER BY to_jsonb(a)::text),'[]'::jsonb)::text FROM review_attempts a JOIN review_runs r ON r.id=a.run_id WHERE r.case_id=$1`,
		"snapshots": `SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY to_jsonb(s)::text),'[]'::jsonb)::text FROM review_run_snapshots s JOIN review_runs r ON r.id=s.run_id WHERE r.case_id=$1`,
		"jobs":      `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM worker_jobs t WHERE case_id=$1`,
		"outbox":    `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM outbox_events t WHERE case_id=$1`,
		"audit":     `SELECT COALESCE(jsonb_agg(to_jsonb(a) ORDER BY to_jsonb(a)::text),'[]'::jsonb)::text FROM audit_events a JOIN review_runs r ON r.id=a.run_id WHERE r.case_id=$1`,
		"receipts":  `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM execution_write_receipts t WHERE case_id=$1`,
	}
	result := make(map[string]string, len(queries))
	for name, query := range queries {
		var value string
		if err := tx.QueryRow(ctx, query, caseID).Scan(&value); err != nil {
			t.Fatalf("snapshot %s: %v", name, err)
		}
		result[name] = value
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return result
}

func acceptanceAtomicReceipt(t *testing.T, execution *Store, actor accounts.User, caseID, key string) (reviewWriteReceipt, bool) {
	t.Helper()
	ctx := context.Background()
	tx, err := execution.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scope := userScope(actor)
	scope.CaseID = caseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		t.Fatal(err)
	}
	route := "POST /api/cases/" + caseID + "/reviews"
	receipt, found, err := loadWriteReceipt(ctx, tx, executionWriteReceipts, actor, route, key)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		return reviewWriteReceipt{}, false
	}
	if receipt.CaseID != caseID {
		t.Fatalf("receipt case=%s", receipt.CaseID)
	}
	value, err := decodeReviewWriteReceipt(receipt.Response)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return value, true
}

func TestKnowledgeAcceptanceDeferredOutboxFailureRollsBackExecutionRole(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated role checks")
	}
	core, documents, execution := env.openRole(t, "core"), env.openRole(t, "document"), env.openRole(t, "execution")
	actor, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	pack := publishSyntheticAcceptancePack(t, core, actor)
	publishSyntheticAcceptancePolicy(t, core, actor, pack)
	caseID := acceptanceKnowledgeCase(t, core, documents, actor)
	execution.UseDocumentPreparer(documents)
	execution.UseModels(&frozenOCRModel{})
	ctx := context.Background()
	const firstKey = "c305-original"
	const nextKey = "c305-deferred"
	first, err := execution.StartReviewAsyncAt(WithReviewIdempotency(ctx, firstKey), actor, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatalf("valid initial acceptance: %v", err)
	}
	initialReceipt, found := acceptanceAtomicReceipt(t, execution, actor, caseID, firstKey)
	if !found || !initialReceipt.Durable || initialReceipt.Acceptance != first {
		t.Fatalf("initial receipt=%+v found=%v", initialReceipt, found)
	}
	if _, found := acceptanceAtomicReceipt(t, execution, actor, caseID, nextKey); found {
		t.Fatal("new key already has a receipt")
	}
	before := acceptanceAtomicSnapshot(t, execution, actor, caseID)
	if !strings.Contains(before["receipts"], firstKey) {
		t.Fatalf("baseline snapshot omitted original receipt: %s", before["receipts"])
	}
	const ddl = `CREATE FUNCTION c305_acceptance_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM outbox_events o JOIN worker_jobs j ON j.id=o.job_id AND j.tenant_id=o.tenant_id AND j.case_id=o.case_id JOIN review_runs r ON r.id=j.run_id AND r.case_id=j.case_id JOIN review_attempts a ON a.id=j.attempt_id AND a.run_id=r.id JOIN review_run_snapshots s ON s.run_id=r.id
 WHERE o.id=NEW.id AND o.job_id=NEW.job_id AND o.case_id=NEW.case_id AND j.state='queued' AND j.actor_id=current_setting('app.actor_id',true) AND s.governed_knowledge IS NOT NULL AND EXISTS (SELECT 1 FROM jsonb_array_elements(convert_from(s.governed_knowledge,'UTF8')::jsonb->'required_policies') p WHERE p->'ref'->>'knowledge_id'='acceptance-demo-policy') AND EXISTS (SELECT 1 FROM execution_write_receipts receipt WHERE receipt.tenant_id=NEW.tenant_id AND receipt.org_id=NEW.org_id AND receipt.case_id=NEW.case_id AND receipt.actor_id=j.actor_id AND receipt.route='POST /api/cases/'||NEW.case_id||'/reviews' AND receipt.idempotency_key='c305-deferred' AND receipt.response_json->>'durable'='true' AND receipt.response_json->'acceptance'->>'id'=r.id AND receipt.response_json->'acceptance'->>'attempt_id'=a.id AND receipt.response_json->'acceptance'->>'job_id'=j.id)) THEN RAISE EXCEPTION 'C305_OUTBOX_PRECONDITION'; END IF;
 RAISE EXCEPTION 'C305_OUTBOX_FORCED'; END $$;
 CREATE CONSTRAINT TRIGGER c305_acceptance_deferred AFTER INSERT ON outbox_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION c305_acceptance_fail()`
	if _, err := env.harness.pool.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.harness.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS c305_acceptance_deferred ON outbox_events; DROP FUNCTION IF EXISTS c305_acceptance_fail()`)
	})
	const nextTime = "2026-02-03T01:05:06.987654322Z"
	if _, err := execution.StartReviewAsyncAt(WithReviewIdempotency(ctx, nextKey), actor, caseID, nextTime); err == nil || !strings.Contains(err.Error(), "C305_OUTBOX_FORCED") {
		t.Fatalf("expected C305_OUTBOX_FORCED, got %v", err)
	}
	after := acceptanceAtomicSnapshot(t, execution, actor, caseID)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("deferred failure changed durable rows: before=%v after=%v", before, after)
	}
	if _, found := acceptanceAtomicReceipt(t, execution, actor, caseID, nextKey); found {
		t.Fatal("failed start left a receipt")
	}
	if _, err := env.harness.pool.Exec(ctx, `DROP TRIGGER c305_acceptance_deferred ON outbox_events; DROP FUNCTION c305_acceptance_fail()`); err != nil {
		t.Fatal(err)
	}
	retry, err := execution.RetryAsync(ctx, actor, caseID, first.RunID)
	if err != nil || retry.RunID != first.RunID || retry.AttemptID != first.AttemptID || retry.JobID != first.JobID {
		t.Fatalf("active retry replaced old attempt after rollback: %+v %v", retry, err)
	}
	next, err := execution.StartReviewAsyncAt(WithReviewIdempotency(ctx, nextKey), actor, caseID, nextTime)
	if err != nil || next.RunID == first.RunID {
		t.Fatalf("same start after rollback: %+v %v", next, err)
	}
	newReceipt, found := acceptanceAtomicReceipt(t, execution, actor, caseID, nextKey)
	if !found || !newReceipt.Durable || newReceipt.Acceptance != next {
		t.Fatalf("new receipt=%+v found=%v acceptance=%+v", newReceipt, found, next)
	}
	old := readWorkspace(t, execution, actor, caseID, first.RunID)
	if old.Execution == nil || old.Execution.Attempt == nil || old.Execution.Attempt.ID != first.AttemptID {
		t.Fatalf("old attempt replaced: %+v", old.Execution)
	}
}

func TestFrozenIdentityFailsBeforeActorOrDatabase(t *testing.T) {
	err := ValidateFrozenGovernedKnowledgeSnapshotTx(context.Background(), nil, accounts.User{}, "missing", ReviewKnowledgeSnapshot{})
	if err == nil || err.Error() != "本次审查保存的制度记录无效" {
		t.Fatalf("identity did not fail first: %v", err)
	}
}
