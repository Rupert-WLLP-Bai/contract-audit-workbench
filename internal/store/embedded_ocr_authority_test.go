package store

import (
	"context"
	"errors"
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
	lease, err := s.ReserveModelChatCall(ctx, "document", child, "vision", 1, 20, 20, modelBudgetTestLimits(), models.ConfigurationID())
	if err != nil {
		t.Fatalf("reserve OCR budget: %v", err)
	}
	if lease.JobID != "" || lease.Purpose != "document" {
		t.Fatalf("unexpected lease: %+v", lease)
	}
	if err = s.FinishModelCall(ctx, lease, ModelCallResult{Status: "completed"}); err != nil {
		t.Fatal(err)
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
