// File metadata (manifest mirror):
// Purpose: 实现 `TestTenantAttemptEpochDoesNotRevive` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"testing"

	"contractaudit/internal/model"
)

// An admitted attempt is bound to the epoch at acceptance, including across
// suspension and restoration. A pre-migration NULL cannot become demo epoch 0.
func TestTenantAttemptEpochDoesNotRevive(t *testing.T) {
	st := openModelBudgetStore(t)
	ctx := context.Background()
	_, err := st.pool.Exec(ctx, `INSERT INTO identity_tenants(tenant_id,display_name,status) VALUES ('epoch-test','Epoch test','active')`)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := st.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, `SELECT set_config('app.tenant_id','epoch-test',false),set_config('app.org_id','epoch-org',false)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `INSERT INTO cases(id,title,contract_type,owner_id,member_ids,created_at,updated_at,tenant_id,org_id) VALUES ('epoch-case','case','service','actor','[]','now','now','epoch-test','epoch-org')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `INSERT INTO review_runs(id,case_id,state,created_at,updated_at,tenant_id,org_id) VALUES ('epoch-run','epoch-case','running','now','now','epoch-test','epoch-org')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `INSERT INTO review_attempts(id,run_id,ordinal,state,accepted_at,history_kind,actor_id,tenant_id,org_id,tenant_live_epoch) VALUES ('epoch-attempt','epoch-run',1,'running','now','preparing','actor','epoch-test','epoch-org',1),('epoch-legacy','epoch-run',2,'running','now','preparing','actor','epoch-test','epoch-org',NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	scope := model.DelegatedScope{TenantID: "epoch-test", OrgID: "epoch-org", CaseID: "epoch-case", ActorID: "actor", RunID: "epoch-run", AttemptID: "epoch-attempt", TenantLiveEpoch: "1"}
	check := func(s model.DelegatedScope, want bool) {
		t.Helper()
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if got := st.fenceAttemptEpoch(ctx, tx, s) == nil; got != want {
			t.Fatalf("scope epoch %q accepted=%t, want %t", s.TenantLiveEpoch, got, want)
		}
	}
	check(scope, true)
	legacy := scope
	legacy.AttemptID = "epoch-legacy"
	legacy.TenantLiveEpoch = "0"
	check(legacy, false)
	_, err = conn.Exec(ctx, `UPDATE identity_tenants SET status='suspended',live_epoch=live_epoch+1 WHERE tenant_id='epoch-test'`)
	if err != nil {
		t.Fatal(err)
	}
	check(scope, false)
	_, err = conn.Exec(ctx, `UPDATE identity_tenants SET status='active',live_epoch=live_epoch+1 WHERE tenant_id='epoch-test'`)
	if err != nil {
		t.Fatal(err)
	}
	check(scope, false)
	scope.TenantLiveEpoch = "3"
	check(scope, false)
	if _, err = conn.Exec(ctx, `UPDATE review_attempts SET tenant_live_epoch=3 WHERE id='epoch-attempt'`); err == nil {
		t.Fatal("admission epoch was mutable")
	}
}
