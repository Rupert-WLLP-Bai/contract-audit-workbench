// File metadata (manifest mirror):
// Purpose: 实现 `TestEmbeddedOCRTicketExactSelectionPG`, `TestEmbeddedOCRBudgetPG`, `reservation`, `budgetedOCRFixture` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
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
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/model"
)

func TestEmbeddedOCRTicketExactSelectionPG(t *testing.T) {
	s, profile := openExecutionStore(t)
	ctx := context.Background()
	token, err := s.EstablishSession(profile, "client")
	if err != nil {
		t.Fatal(err)
	}
	user, _, err := s.AuthenticateSession(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithAdmissionSessionToken(ctx, token)
	caseID, err := s.CreateCase(user, "embedded OCR ticket", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	docID, err := s.AddDocument(ctx, user, caseID, "procurement", "image.png", "image/png", model.SampleWarrantyPNG)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddDocument(ctx, user, caseID, "draft_contract", "draft.txt", "text/plain", []byte("质保24个月")); err != nil {
		t.Fatal(err)
	}
	s.UseModels(&frozenOCRModel{})
	accepted, err := s.StartReviewAsync(ctx, user, caseID)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"worker_claim_identities", "outbox_events", "worker_jobs"} {
		column := "job_id"
		if table == "worker_jobs" {
			column = "id"
		}
		if _, err = s.pool.Exec(ctx, "DELETE FROM "+table+" WHERE "+column+"=$1", accepted.JobID); err != nil {
			t.Fatal(err)
		}
	}
	item := Case{ID: caseID, TenantID: user.TenantID, OwnerOrgID: user.OrgID}
	parent, err := s.prepareEmbeddedAuthority(ctx, user, item, accepted.RunID, accepted.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.embeddedAuthorityState(ctx, user, parent)
	if err != nil {
		t.Fatal(err)
	}
	var sel AttemptParseSelection
	for _, candidate := range st.AttemptParseSelections {
		if candidate.DocumentID == docID {
			sel = candidate
		}
	}
	doc, ok := st.documentByIdentity(sel.DocumentID, sel.OriginalDigest)
	if !ok {
		t.Fatal("missing document")
	}
	child, err := s.mintEmbeddedOCRAuthority(ctx, user, parent, doc, sel)
	if err != nil {
		t.Fatalf("mint child: %v", err)
	}
	if _, err = s.ResolveOperationAuthority(ctx, child, "document"); err != nil {
		t.Fatalf("resolve child: %v", err)
	}
	if _, err = s.ResolveOperationAuthority(ctx, child, "worker"); err == nil {
		t.Fatal("worker accepted child")
	}
	for _, mutate := range []func(*model.DelegatedScope){func(v *model.DelegatedScope) { v.InputSHA256 = string([]byte(v.InputSHA256)[1:]) }, func(v *model.DelegatedScope) { v.ParseSelectionID += "x" }, func(v *model.DelegatedScope) { v.ParseConfigSHA256 = "a" }, func(v *model.DelegatedScope) { v.ParentTicketDigest = "a" }, func(v *model.DelegatedScope) { v.AdmissionRevision += "x" }} {
		forged := child
		mutate(&forged)
		if _, err = s.ResolveOperationAuthority(ctx, forged, "model"); err == nil {
			t.Fatal("forged child accepted")
		}
	}
	var parentExpiry, childExpiry time.Time
	if err = s.pool.QueryRow(ctx, `SELECT p.expires_at,c.expires_at FROM operation_authority_tickets p CROSS JOIN operation_authority_tickets c WHERE p.ticket_digest=$1 AND c.ticket_digest=$2`, identityTokenDigest(parent.OperationTicket), identityTokenDigest(child.OperationTicket)).Scan(&parentExpiry, &childExpiry); err != nil {
		t.Fatal(err)
	}
	if childExpiry.After(parentExpiry) {
		t.Fatal("child outlives parent")
	}
	if _, err = s.pool.Exec(ctx, `DELETE FROM operation_authority_tickets WHERE ticket_digest=$1`, identityTokenDigest(parent.OperationTicket)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveOperationAuthority(ctx, child, "model"); err == nil {
		t.Fatal("orphan child accepted")
	}
}

func TestEmbeddedOCRBudgetPG(t *testing.T) {
	s, profile := openExecutionStore(t)
	ctx := context.Background()
	token, err := s.EstablishSession(profile, "client")
	if err != nil {
		t.Fatal(err)
	}
	user, _, err := s.AuthenticateSession(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithAdmissionSessionToken(ctx, token)
	caseID, err := s.CreateCase(user, "embedded image budget", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	docID, err := s.AddDocument(ctx, user, caseID, "procurement", "original.png", "image/png", []byte("original image"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddDocument(ctx, user, caseID, "draft_contract", "draft.txt", "text/plain", []byte("质保24个月")); err != nil {
		t.Fatal(err)
	}
	models := &frozenOCRModel{}
	s.UseModels(models)
	accepted, err := s.StartReviewAsync(ctx, user, caseID)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"worker_claim_identities", "outbox_events", "worker_jobs"} {
		column := "job_id"
		if table == "worker_jobs" {
			column = "id"
		}
		if _, err = s.pool.Exec(ctx, "DELETE FROM "+table+" WHERE "+column+"=$1", accepted.JobID); err != nil {
			t.Fatal(err)
		}
	}
	item := Case{ID: caseID, TenantID: user.TenantID, OwnerOrgID: user.OrgID}
	parent, err := s.prepareEmbeddedAuthority(ctx, user, item, accepted.RunID, accepted.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.embeddedAuthorityState(ctx, user, parent)
	if err != nil {
		t.Fatal(err)
	}
	var sel AttemptParseSelection
	for _, v := range st.AttemptParseSelections {
		if v.DocumentID == docID {
			sel = v
		}
	}
	doc, ok := st.documentByIdentity(docID, sel.OriginalDigest)
	if !ok {
		t.Fatal("selection unavailable")
	}
	child, err := s.mintEmbeddedOCRAuthority(ctx, user, parent, doc, sel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveModelCall(ctx, "document", child, "vision", 1, 20, 20, modelBudgetTestLimits()); err == nil {
		t.Fatal("OCR child accepted by advisory budget")
	}
	// A single priced OCR reservation exactly fills the tenant amount cap.
	// Concurrent attempts must not create a second token lease or money entry.
	if _, err = s.pool.Exec(ctx, `INSERT INTO model_price_snapshots(snapshot_id,provider_config_id,model_ref,currency,minor_unit_scale,input_micro_usd_per_million,output_micro_usd_per_million,published_by,payload_sha256) VALUES('ocr-price',$1,'vision','USD',6,1000000,1000000,'fixture',repeat('a',64))`, models.ConfigurationID()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO model_money_limits(scope,tenant_id,day_limit_minor,revision,updated_by) VALUES('tenant',$1,40,1,'fixture')`, user.TenantID); err != nil {
		t.Fatal(err)
	}
	type reservation struct {
		lease ModelCallLease
		err   error
	}
	results := make(chan reservation, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, e := s.ReserveModelChatCall(ctx, "document", child, "vision", 1, 20, 20, modelBudgetTestLimits(), models.ConfigurationID())
			results <- reservation{lease, e}
		}()
	}
	wg.Wait()
	close(results)
	var lease ModelCallLease
	acceptedCount, denied := 0, 0
	for result := range results {
		if result.err == nil {
			acceptedCount++
			lease = result.lease
		} else if errors.Is(result.err, ErrModelBudgetExceeded) {
			denied++
		} else {
			t.Fatalf("reserve OCR budget: %v", result.err)
		}
	}
	if acceptedCount != 1 || denied != 1 || lease.JobID != "" || lease.Purpose != "document" {
		t.Fatalf("unexpected concurrent reservations: accepted=%d denied=%d lease=%+v", acceptedCount, denied, lease)
	}
	var reservations, moneyRows int
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM model_call_leases WHERE attempt_id=$1),(SELECT count(*) FROM model_money_ledger WHERE lease_id=$2 AND event_kind='reserve')`, accepted.AttemptID, lease.ID).Scan(&reservations, &moneyRows); err != nil || reservations != 1 || moneyRows != 1 {
		t.Fatalf("token and amount did not reserve atomically: leases=%d money=%d err=%v", reservations, moneyRows, err)
	}
	// Once sent, revoking the session must not erase the incurred unknown hold.
	if err = s.Logout(token); err != nil {
		t.Fatal(err)
	}
	result := ModelCallResult{Status: "completed"}
	if err = s.FinishModelCall(context.Background(), lease, result); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishModelCall(context.Background(), lease, result); err != nil {
		t.Fatalf("identical settlement is not idempotent: %v", err)
	}
	var state string
	var reserved string
	if err = s.pool.QueryRow(ctx, `SELECT money_cost_state,money_reserved_minor::text FROM model_usage WHERE lease_id=$1`, lease.ID).Scan(&state, &reserved); err != nil || state != "unknown_hold" || reserved != "40" {
		t.Fatalf("missing incurred unknown hold: state=%s reserve=%s err=%v", state, reserved, err)
	}

	policy, err := s.ReadTenantMoneyPolicy(context.Background(), profile)
	if err != nil || !policy.Configured || policy.PriceState != "unconfigured" {
		t.Fatalf("missing limit or unknown price projection: %+v err=%v", policy, err)
	}
	endpoint := "http://127.0.0.1:9999"
	if _, err = s.pool.Exec(ctx, `INSERT INTO llm_endpoint_settings(id,name,protocol,base_url,models,review_model,ocr_model,updated_at,updated_by) VALUES('active','fixture','openai-compatible',$1,'["chat","ocr"]','chat','ocr','now','fixture')`, endpoint); err != nil {
		t.Fatal(err)
	}
	provider := model.ConfigurationIDFor(endpoint, "chat", "ocr")
	if _, err = s.pool.Exec(ctx, `INSERT INTO model_price_snapshots(snapshot_id,provider_config_id,model_ref,currency,minor_unit_scale,input_micro_usd_per_million,output_micro_usd_per_million,published_by,payload_sha256) VALUES('chat-price',$1,'chat','USD',6,1,1,'fixture',repeat('a',64))`, provider); err != nil {
		t.Fatal(err)
	}
	policy, err = s.ReadTenantMoneyPolicy(context.Background(), profile)
	if err != nil || policy.PriceState != "unconfigured" {
		t.Fatalf("missing OCR price treated as known: %+v err=%v", policy, err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO model_price_snapshots(snapshot_id,provider_config_id,model_ref,currency,minor_unit_scale,input_micro_usd_per_million,output_micro_usd_per_million,published_by,payload_sha256) VALUES('vision-price',$1,'ocr','USD',6,1,1,'fixture',repeat('a',64))`, provider); err != nil {
		t.Fatal(err)
	}
	policy, err = s.ReadTenantMoneyPolicy(context.Background(), profile)
	if err != nil || !policy.Configured || policy.PriceState != "known" {
		t.Fatalf("complete price projection unavailable: %+v err=%v", policy, err)
	}

	// Simulate conversion after a UTC period boundary using a reserve event
	// from the prior month and an already expired lease, then reconcile it.
	if _, err = s.pool.Exec(ctx, `INSERT INTO model_money_ledger(event_id,tenant_id,lease_id,event_kind,period_day,period_month,currency,minor_unit_scale,amount_minor,price_snapshot_id,source_event_id,payload_sha256) VALUES('old-reserve',$1,'old-lease','reserve',date '2020-12-31',date '2020-12-01','USD',6,25,'ocr-price','reserve',repeat('b',64))`, user.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO model_usage(call_id,lease_id,tenant_id,actor_id,case_id,run_id,attempt_id,caller_identity,model_ref,attempt_number,call_status,occurred_at,reserved_at,budget_released) VALUES('old-call','old-lease',$1,$2,$3,$4,$5,'document','vision',1,'failed',now(),now(),false)`, user.TenantID, user.ID, caseID, accepted.RunID, accepted.AttemptID); err != nil {
		t.Fatal(err)
	}
	var oldDay, oldState, oldReserve string
	if err = s.pool.QueryRow(ctx, `SELECT money_period_day::text,money_cost_state,money_reserved_minor::text FROM model_usage WHERE lease_id='old-lease'`).Scan(&oldDay, &oldState, &oldReserve); err != nil || oldDay != "2020-12-31" || oldState != "unknown_hold" || oldReserve != "25" {
		t.Fatalf("cross-period hold drifted: %s %s %s %v", oldDay, oldState, oldReserve, err)
	}
	adjustment := TenantMoneyAdjustmentInput{LeaseID: "old-lease", Kind: "reconciliation", AmountMinor: "17", IdempotencyKey: "billing-1", Reason: "trusted billing invoice"}
	first, err := s.AppendTenantMoneyAdjustment(context.Background(), profile, adjustment)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := s.AppendTenantMoneyAdjustment(context.Background(), profile, adjustment)
	if err != nil || first.EventID != repeated.EventID || !strings.HasPrefix(first.EventID, "mmo_") {
		t.Fatalf("reconciliation id changed on replay: first=%+v repeated=%+v err=%v", first, repeated, err)
	}

}

type budgetedOCRFixture struct {
	store *Store
	calls int
}

func (*budgetedOCRFixture) Name() string            { return "budgeted-chat" }
func (*budgetedOCRFixture) OCRName() string         { return "budgeted-ocr" }
func (*budgetedOCRFixture) ConfigurationID() string { return "budgeted-provider" }
func (*budgetedOCRFixture) Warranty(context.Context, []model.Material) (model.Warranty, error) {
	return model.Warranty{}, nil
}
func (m *budgetedOCRFixture) Transcribe(ctx context.Context, _ string, _ []byte) (string, error) {
	scope, ok := model.DelegatedScopeFromContext(ctx)
	if !ok {
		return "", errors.New("missing child")
	}
	lease, err := m.store.ReserveModelChatCall(ctx, "document", scope, "budgeted-ocr", 1, 20, 20, modelBudgetTestLimits(), m.ConfigurationID())
	if err != nil {
		return "", err
	}
	if err = m.store.FinishModelCall(ctx, lease, ModelCallResult{Status: "completed"}); err != nil {
		return "", err
	}
	m.calls++
	return "采购文件要求质保36个月。", nil
}

func TestEmbeddedOCRPreparePromoteAndLedgerPG(t *testing.T) {
	s, profile := openExecutionStore(t)
	ctx := context.Background()
	token, err := s.EstablishSession(profile, "client")
	if err != nil {
		t.Fatal(err)
	}
	user, _, err := s.AuthenticateSession(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithAdmissionSessionToken(ctx, token)
	caseID, err := s.CreateCase(user, "embedded OCR flow", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddDocument(ctx, user, caseID, "procurement", "original.png", "image/png", []byte("original image")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddDocument(ctx, user, caseID, "draft_contract", "draft.txt", "text/plain", []byte("质保24个月")); err != nil {
		t.Fatal(err)
	}
	models := &budgetedOCRFixture{store: s}
	s.UseModels(models)
	accepted, err := s.StartReviewAsync(ctx, user, caseID)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"worker_claim_identities", "outbox_events", "worker_jobs"} {
		column := "job_id"
		if table == "worker_jobs" {
			column = "id"
		}
		if _, err = s.pool.Exec(ctx, "DELETE FROM "+table+" WHERE "+column+"=$1", accepted.JobID); err != nil {
			t.Fatal(err)
		}
	}
	item := Case{ID: caseID, TenantID: user.TenantID, OwnerOrgID: user.OrgID}
	parent, err := s.prepareEmbeddedAuthority(ctx, user, item, accepted.RunID, accepted.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	claim := JobClaim{TenantID: parent.TenantID, OrgID: parent.OrgID, CaseID: parent.CaseID, ActorID: parent.ActorID, RunID: parent.RunID, AttemptID: parent.AttemptID, Kind: "review"}
	s.setClaim(claim)
	defer s.clearClaim(claim)
	s.claimMu.Lock()
	if s.operationContexts == nil {
		s.operationContexts = map[string]model.DelegatedScope{}
	}
	s.operationContexts[parent.AttemptID] = parent
	s.claimMu.Unlock()
	work := model.WithDelegatedScope(withEmbeddedAdmission(context.Background(), user, true), parent)
	if err = s.markAttemptStarted(work, accepted.RunID, accepted.AttemptID); err != nil {
		t.Fatalf("mark preparing start: %v", err)
	}
	reviewCtx, err := s.prepareAndPromoteEmbedded(work, parent, models)
	if err != nil {
		t.Fatalf("prepare and promote: %v", err)
	}
	reviewScope, ok := model.DelegatedScopeFromContext(reviewCtx)
	if !ok || reviewScope.Kind != "execution" || reviewScope.InputSHA256 == "" || models.calls != 1 {
		t.Fatalf("invalid promoted context or OCR calls: scope=%+v calls=%d", reviewScope, models.calls)
	}
	lease, err := s.ReserveModelChatCall(context.Background(), "worker", reviewScope, "budgeted-chat", 1, 20, 20, modelBudgetTestLimits(), models.ConfigurationID())
	if err != nil {
		t.Fatalf("reserve frozen review: %v", err)
	}
	if err = s.FinishModelCall(context.Background(), lease, ModelCallResult{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	var usage int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM model_usage WHERE attempt_id=$1`, accepted.AttemptID).Scan(&usage); err != nil || usage != 2 {
		t.Fatalf("missing settled OCR/review budget rows: count=%d err=%v", usage, err)
	}
}
