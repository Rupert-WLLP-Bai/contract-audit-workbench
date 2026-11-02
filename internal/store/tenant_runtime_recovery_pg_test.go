// File metadata (manifest mirror):
// Purpose: 实现 `TestTenantRuntimeRoleClaimAndReadmissionPG` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
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
	"time"
)

// The execution login has no tenant GUC during scheduling; a valid review job
// must still be claimable, while an old admission cannot revive after restart.
func TestTenantRuntimeRoleClaimAndReadmissionPG(t *testing.T) {
	setup, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("isolated test database and migrator required")
	}
	ctx := WithAdmissionSessionToken(context.Background(), setup.sessionToken)
	tenant := setup.admin.TenantID
	// The demo principal keeps its real directory identity. Register a tenant so
	// the runtime row can be suspended and its epoch advanced across restoration.
	if _, err := setup.environment.harness.pool.Exec(ctx, `INSERT INTO identity_tenants(tenant_id,display_name,status) VALUES($1,'runtime test','active')`, tenant); err != nil {
		t.Fatal(err)
	}
	var canRead, canInsert bool
	if err := setup.core.pool.QueryRow(ctx, `SELECT has_column_privilege(current_user,'intake_authority_tickets','tenant_live_epoch','SELECT'),has_column_privilege(current_user,'intake_authority_tickets','tenant_live_epoch','INSERT')`).Scan(&canRead, &canInsert); err != nil || !canRead || !canInsert {
		t.Fatalf("core intake epoch column privileges: read=%v insert=%v err=%v", canRead, canInsert, err)
	}
	execution := setup.environment.openRole(t, "execution")
	execution.UseOperationAuthority(durableTestOperationAuthority{core: setup.core})
	execution.UseDocumentPreparer(setup.document)
	execution.UseModels(&frozenOCRModel{})
	first, err := execution.StartReviewAsync(ctx, setup.admin, setup.caseID)
	if err != nil {
		t.Fatalf("execution role admission: %v", err)
	}
	claim, err := execution.ClaimNextJob(context.Background(), "epoch-role-worker", time.Minute)
	if err != nil || claim.JobID != first.JobID || claim.TenantLiveEpoch != 1 {
		t.Fatalf("execution role claim: %+v err=%v", claim, err)
	}
	// A second in-flight review has already frozen its input and can be retried
	// after restoration without rewriting its original attempt.
	frozenCase, err := setup.core.CreateCase(setup.admin, "frozen retry", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	for _, material := range []struct{ role, body string }{{"procurement", "采购文件质保36个月"}, {"draft_contract", "拟签合同质保24个月"}} {
		if _, err = setup.document.AddDocument(ctx, setup.admin, frozenCase, material.role, material.role+".txt", "text/plain", []byte(material.body)); err != nil {
			t.Fatal(err)
		}
	}
	frozenRun, err := execution.StartReviewAsync(ctx, setup.admin, frozenCase)
	if err != nil {
		t.Fatal(err)
	}
	frozenClaim, err := execution.ClaimNextJob(context.Background(), "frozen-epoch-worker", time.Minute)
	if err != nil || frozenClaim.JobID != frozenRun.JobID {
		t.Fatalf("frozen review claim: %+v %v", frozenClaim, err)
	}
	work := withExecutionClaim(context.Background(), frozenClaim)
	if err = execution.markAttemptStarted(work, frozenRun.RunID, frozenRun.AttemptID); err != nil {
		t.Fatalf("start frozen review: %v", err)
	}
	if _, err = execution.prepareAttempt(work, frozenRun.RunID, frozenRun.AttemptID, execution.currentModels()); err != nil {
		t.Fatalf("freeze original input: %v", err)
	}
	if _, err = setup.environment.harness.pool.Exec(ctx, `UPDATE identity_tenants SET status='suspended',live_epoch=live_epoch+1 WHERE tenant_id=$1`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = setup.environment.harness.pool.Exec(ctx, `UPDATE identity_tenants SET status='active',live_epoch=live_epoch+1 WHERE tenant_id=$1`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = execution.RetryAsync(ctx, setup.admin, setup.caseID, first.RunID); err == nil {
		t.Fatal("stale preparing attempt accepted a retry without frozen input")
	}
	retry, err := execution.RetryAsync(ctx, setup.admin, frozenCase, frozenRun.RunID)
	if err != nil || retry.RunID != frozenRun.RunID || retry.AttemptID == frozenRun.AttemptID {
		t.Fatalf("frozen stale retry: old=%+v retry=%+v err=%v", frozenRun, retry, err)
	}
	var inputMatches bool
	if err = setup.environment.harness.pool.QueryRow(ctx, `SELECT old.input_hash=new.input_hash AND old.tenant_live_epoch=1 AND new.tenant_live_epoch=3 AND new.retry_of_attempt_id=old.id FROM review_attempts old JOIN review_attempts new ON new.id=$2 WHERE old.id=$1`, frozenRun.AttemptID, retry.AttemptID).Scan(&inputMatches); err != nil || !inputMatches {
		t.Fatalf("frozen retry identity changed: %v %v", inputMatches, err)
	}
	second, err := execution.StartReviewAsync(ctx, setup.admin, setup.caseID)
	if err != nil {
		t.Fatalf("readmit after restoration: %v", err)
	}
	if second.RunID == first.RunID || second.AttemptID == first.AttemptID || second.JobID == first.JobID {
		t.Fatalf("reused stale active work: first=%+v second=%+v", first, second)
	}
	var oldState string
	var oldEpoch, newEpoch int64
	if err = setup.environment.harness.pool.QueryRow(ctx, `SELECT a.state,a.tenant_live_epoch,b.tenant_live_epoch FROM review_attempts a JOIN review_attempts b ON b.id=$2 WHERE a.id=$1`, first.AttemptID, second.AttemptID).Scan(&oldState, &oldEpoch, &newEpoch); err != nil || oldState != "accepted" || oldEpoch != 1 || newEpoch != 3 {
		t.Fatalf("admission history changed: state=%s old=%d new=%d err=%v", oldState, oldEpoch, newEpoch, err)
	}
}
