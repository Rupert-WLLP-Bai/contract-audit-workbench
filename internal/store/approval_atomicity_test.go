// File metadata (manifest mirror):
// Purpose: Verifies real approval command rollback and commit across Contract events and Outbox, transaction-local scope cleanup, restricted service roles and cross-tenant RLS rejection.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-03
// Updated: 2026-11-03
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
	contractpg "contractaudit/internal/contract/postgres"
	platformpg "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestApprovalDecisionContractEventAndOutboxAtomicity injects a real database
// failure at the second repository and retries the actual authorized command.
func TestApprovalDecisionContractEventAndOutboxAtomicity(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	ctx := context.Background()
	contractID, submitted := submitApprovalForDecisionTest(t, fixture, "atomicity-submit-0001")
	actor := approvalTestUser(t, "demo-reviewer", fixture.core)
	input := DecideApprovalTaskInput{TaskID: submitted.Tasks[0].ApprovalTaskID,
		Decision: clmDecisionApprove, ExpectedRoundRowVersion: submitted.Round.RowVersion,
		IdempotencyKey: "atomicity-decision-0001"}
	counts := func() (int, int, int) {
		t.Helper()
		var decisions, events, delivery int
		tx, err := fixture.core.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rollbackCLM(tx)
		if err := setLocalScope(ctx, tx, userScope(fixture.admin)); err != nil {
			t.Fatal(err)
		}
		handle := platformpg.Bind(tx)
		if _, ok := handle.(interface{ Commit(context.Context) error }); ok {
			t.Fatal("handle exposes commit")
		}
		if _, ok := handle.(interface{ Rollback(context.Context) error }); ok {
			t.Fatal("handle exposes rollback")
		}
		if _, ok := handle.(interface {
			Begin(context.Context) (pgx.Tx, error)
		}); ok {
			t.Fatal("handle exposes nested transaction")
		}
		if _, ok := handle.(interface{ Conn() *pgx.Conn }); ok {
			t.Fatal("handle exposes connection")
		}
		err = tx.QueryRow(ctx, `SELECT
          (SELECT count(*) FROM clm_approval_decisions WHERE tenant_id=$1 AND contract_id=$2),
          (SELECT count(*) FROM clm_business_events WHERE tenant_id=$1 AND contract_id=$2),
          (SELECT count(*) FROM clm_outbox_delivery d JOIN clm_business_events e
             ON e.tenant_id=d.tenant_id AND e.event_id=d.event_id WHERE e.tenant_id=$1 AND e.contract_id=$2)`,
			accounts.Tenant(fixture.admin), contractID).Scan(&decisions, &events, &delivery)
		if err != nil {
			t.Fatal(err)
		}
		return decisions, events, delivery
	}
	snapshot := func() string {
		t.Helper()
		tx, err := fixture.core.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rollbackCLM(tx)
		if err := setLocalScope(ctx, tx, userScope(fixture.admin)); err != nil {
			t.Fatal(err)
		}
		var state string
		err = tx.QueryRow(ctx, `SELECT jsonb_build_object(
          'contract', (SELECT to_jsonb(c) FROM clm_contracts c WHERE c.tenant_id=$1 AND c.contract_id=$2),
          'rounds', (SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.approval_round_id),'[]'::jsonb) FROM clm_approval_rounds r WHERE r.tenant_id=$1 AND r.contract_id=$2),
          'tasks', (SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.approval_task_id),'[]'::jsonb) FROM clm_approval_tasks t WHERE t.tenant_id=$1 AND t.contract_id=$2),
          'decisions', (SELECT coalesce(jsonb_agg(to_jsonb(d) ORDER BY d.decision_id),'[]'::jsonb) FROM clm_approval_decisions d WHERE d.tenant_id=$1 AND d.contract_id=$2),
          'events', (SELECT coalesce(jsonb_agg(to_jsonb(e) ORDER BY e.event_id),'[]'::jsonb) FROM clm_business_events e WHERE e.tenant_id=$1 AND e.contract_id=$2),
          'outbox', (SELECT coalesce(jsonb_agg(to_jsonb(o) ORDER BY o.event_id),'[]'::jsonb) FROM clm_outbox_delivery o JOIN clm_business_events e ON e.tenant_id=o.tenant_id AND e.event_id=o.event_id WHERE e.tenant_id=$1 AND e.contract_id=$2),
          'receipts', (SELECT coalesce(jsonb_agg(to_jsonb(w) ORDER BY w.actor_id,w.route_scope,w.idempotency_key),'[]'::jsonb) FROM clm_contract_write_receipts w WHERE w.tenant_id=$1 AND w.contract_id=$2),
          'review_audit', (SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY a.event_id),'[]'::jsonb) FROM business_audit_events a WHERE a.tenant_id=$1 AND a.org_id=$3)
        )::text`, accounts.Tenant(fixture.admin), contractID, fixture.admin.OrgID).Scan(&state)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := snapshot()
	d0, e0, o0 := counts()
	admin := fixture.environment.harness.pool
	_, err := admin.Exec(ctx, `CREATE FUNCTION it_reject_delivery() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected outbox failure'; END $$;
       CREATE TRIGGER it_reject_delivery BEFORE INSERT ON clm_outbox_delivery FOR EACH ROW EXECUTE FUNCTION it_reject_delivery()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP TRIGGER IF EXISTS it_reject_delivery ON clm_outbox_delivery; DROP FUNCTION IF EXISTS it_reject_delivery()`)
	})
	if _, err := fixture.core.DecideApprovalTask(ctx, actor, input); err == nil || !strings.Contains(err.Error(), "injected outbox failure") {
		t.Fatalf("expected injected outbox failure, got %v", err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("failed command changed task/round/contract/audit/receipt or event/outbox state: before=%s after=%s", before, after)
	}
	d1, e1, o1 := counts()
	if d1 != d0 || e1 != e0 || o1 != o0 {
		t.Fatalf("rollback left decision/event/outbox %d/%d/%d -> %d/%d/%d", d0, e0, o0, d1, e1, o1)
	}
	if _, err := admin.Exec(ctx, `DROP TRIGGER it_reject_delivery ON clm_outbox_delivery; DROP FUNCTION it_reject_delivery()`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.core.DecideApprovalTask(ctx, actor, input); err != nil {
		t.Fatal(err)
	}
	d2, e2, o2 := counts()
	if d2 != d0+1 || e2 != e0+1 || o2 != o0+1 {
		t.Fatalf("commit counts %d/%d/%d -> %d/%d/%d", d0, e0, o0, d2, e2, o2)
	}
}

// Check transaction-local scope on the same physical non-owner connection.
func TestApprovalScopeClearsOnRollbackAndCommit(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	ctx := context.Background()
	contractID, _ := submitApprovalForDecisionTest(t, fixture, "rls-port-submit-0001")
	conn, err := fixture.core.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var super, bypass, owner bool
	if err := conn.QueryRow(ctx, `SELECT rolsuper,rolbypassrls, EXISTS (SELECT 1 FROM pg_class WHERE oid = 'clm_business_events'::regclass AND relowner = current_user::regrole) FROM pg_roles WHERE rolname=current_user`).Scan(&super, &bypass, &owner); err != nil || super || bypass || owner {
		t.Fatalf("unsafe service role: super=%v bypass=%v owner=%v err=%v", super, bypass, owner, err)
	}
	var baselineRole string
	if err := conn.QueryRow(ctx, `SELECT coalesce(current_setting('app.actor_role',true),'')`).Scan(&baselineRole); err != nil {
		t.Fatal(err)
	}
	for _, commit := range []bool{false, true} {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := setLocalScope(ctx, tx, userScope(fixture.admin)); err != nil {
			t.Fatal(err)
		}
		handle := platformpg.Bind(tx)
		if _, ok := handle.(interface{ Commit(context.Context) error }); ok {
			t.Fatal("handle exposes commit")
		}
		if _, ok := handle.(interface{ Rollback(context.Context) error }); ok {
			t.Fatal("handle exposes rollback")
		}
		if _, ok := handle.(interface {
			Begin(context.Context) (pgx.Tx, error)
		}); ok {
			t.Fatal("handle exposes nested transaction")
		}
		if _, ok := handle.(interface{ Conn() *pgx.Conn }); ok {
			t.Fatal("handle exposes connection")
		}
		var tenant, actor, org, role string
		if err := tx.QueryRow(ctx, `SELECT current_setting('app.tenant_id',true),current_setting('app.actor_id',true),current_setting('app.org_id',true),current_setting('app.actor_role',true)`).Scan(&tenant, &actor, &org, &role); err != nil {
			t.Fatal(err)
		}
		if tenant != accounts.Tenant(fixture.admin) || actor != fixture.admin.ID || org != fixture.admin.OrgID || role != "api" {
			t.Fatalf("wrong scope %q/%q/%q", tenant, actor, org)
		}
		var visible int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM clm_contracts WHERE tenant_id=$1 AND contract_id=$2`, accounts.Tenant(fixture.admin), contractID).Scan(&visible); err != nil || visible != 1 {
			t.Fatalf("scoped contract invisible count=%d err=%v", visible, err)
		}
		if commit {
			err = tx.Commit(ctx)
		} else {
			err = tx.Rollback(ctx)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow(ctx, `SELECT coalesce(current_setting('app.tenant_id',true),''),coalesce(current_setting('app.actor_id',true),''),coalesce(current_setting('app.org_id',true),''),coalesce(current_setting('app.actor_role',true),'')`).Scan(&tenant, &actor, &org, &role); err != nil {
			t.Fatal(err)
		}
		if tenant != "" || actor != "" || org != "" || role != baselineRole {
			t.Fatalf("scope survived transaction: %q/%q/%q role=%q (baseline %q)", tenant, actor, org, role, baselineRole)
		}
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM clm_contracts WHERE tenant_id=$1 AND contract_id=$2`, accounts.Tenant(fixture.admin), contractID).Scan(&visible); err != nil || visible != 0 {
			t.Fatalf("contract visible without scope count=%d err=%v", visible, err)
		}
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(fixture.admin)); err != nil {
		t.Fatal(err)
	}
	eventID, err := newContractUUID()
	if err != nil {
		t.Fatal(err)
	}
	err = contractpg.Append(ctx, platformpg.Bind(tx), contractpg.BusinessEvent{
		TenantID: "foreign-tenant", EventID: eventID, ContractID: contractID,
		AggregateVersion: 1, EventType: "test.foreign", Payload: []byte(`{}`),
	})
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
		t.Fatalf("cross-tenant event should be rejected by RLS, got %v", err)
	}
}
