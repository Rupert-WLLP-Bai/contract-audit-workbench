// File metadata (manifest mirror):
// Purpose: 实现 `TestEmbeddedPreparationExpiredTicketSettlesOnlyExactAttemptPG` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
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
	"errors"
	"testing"

	"contractaudit/internal/model"
)

func TestEmbeddedPreparationExpiredTicketSettlesOnlyExactAttemptPG(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Fatal("PG fixture unavailable")
	}
	ctx := context.Background()
	s := fixture.environment.harness
	for _, table := range []string{"worker_claim_identities", "outbox_events", "worker_jobs"} {
		column := "job_id"
		if table == "worker_jobs" {
			column = "id"
		}
		if _, err := s.pool.Exec(ctx, "DELETE FROM "+table+" WHERE "+column+"=$1", fixture.jobID); err != nil {
			t.Fatal(err)
		}
	}
	item := Case{ID: fixture.caseID, TenantID: fixture.actor.TenantID, OwnerOrgID: fixture.actor.OrgID}
	scope, err := s.prepareEmbeddedAuthority(ctx, fixture.actor, item, fixture.runID, fixture.attemptID)
	if err != nil {
		t.Fatal(err)
	}
	claim := JobClaim{TenantID: scope.TenantID, OrgID: scope.OrgID, CaseID: scope.CaseID, ActorID: scope.ActorID, RunID: scope.RunID, AttemptID: scope.AttemptID, Kind: "review"}
	s.setClaim(claim)
	s.claimMu.Lock()
	if s.operationContexts == nil {
		s.operationContexts = map[string]model.DelegatedScope{}
	}
	s.operationContexts[scope.AttemptID] = scope
	s.claimMu.Unlock()
	defer s.clearClaim(claim)
	if _, err = s.pool.Exec(ctx, `DELETE FROM operation_authority_tickets WHERE ticket_digest=$1`, identityTokenDigest(scope.OperationTicket)); err != nil {
		t.Fatal(err)
	}
	forged := scope
	forged.ActorID = "different"
	if err = s.settleExpiredEmbeddedPreparation(model.WithDelegatedScope(ctx, forged), forged, "failed", "preparation_failed"); err == nil {
		t.Fatal("forged cleanup succeeded")
	}
	work := model.WithDelegatedScope(ctx, scope)
	s.settleEmbeddedPreparation(ctx, work, fixture.runID, fixture.attemptID, errors.New("expired"))
	var state, history string
	var inputHash, outputHash *string
	if err = s.pool.QueryRow(ctx, `SELECT state,history_kind,input_hash,output_hash FROM review_attempts WHERE id=$1`, scope.AttemptID).Scan(&state, &history, &inputHash, &outputHash); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || history != "preparing" || inputHash != nil || outputHash != nil {
		t.Fatalf("invalid cleanup: state=%s history=%s input=%v output=%v", state, history, inputHash, outputHash)
	}
	var findings, inputs int
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM findings WHERE attempt_id=$1),(SELECT count(*) FROM review_attempt_inputs WHERE attempt_id=$1)`, scope.AttemptID).Scan(&findings, &inputs); err != nil {
		t.Fatal(err)
	}
	if findings != 0 || inputs != 0 {
		t.Fatalf("cleanup created output: findings=%d inputs=%d", findings, inputs)
	}
}
