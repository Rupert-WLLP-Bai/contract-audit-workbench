package store_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/audit"
	"contractaudit/internal/config"
	"contractaudit/internal/engine"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

func TestOpenRefusesDevAndProductionDatabases(t *testing.T) {
	for _, dsn := range []string{
		"postgres://contractaudit:contractaudit@127.0.0.1:5432/contract_audit_dev?sslmode=disable",
		"postgres://contractaudit:contractaudit@127.0.0.1:5432/contract_audit_prod?sslmode=disable",
		"postgres://contractaudit:contractaudit@10.1.1.9:5432/contract_audit_prod?sslmode=disable",
	} {
		_, err := store.OpenTest(t.TempDir(), dsn)
		if !errors.Is(err, config.ErrRefusedDatabase) {
			t.Fatalf("dsn %s err %v", dsn, err)
		}
	}
}

func TestOpenRefusesConfiguredDevAndUnsetProduction(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("DEV_DATABASE_URL", "postgres://contractaudit:contractaudit@127.0.0.1:5432/contract_audit_dev?sslmode=disable")
	t.Setenv("TEST_DATABASE_URL", "postgres://contractaudit:contractaudit@127.0.0.1:5432/contract_audit_dev?sslmode=disable")
	t.Setenv("PROD_DATABASE_URL", "")
	if _, err := store.Open(t.TempDir()); !errors.Is(err, config.ErrRefusedDatabase) {
		t.Fatalf("dev %v", err)
	}
	t.Setenv("APP_ENV", "production")
	t.Setenv("PROD_DATABASE_URL", "")
	if _, err := store.Open(t.TempDir()); !errors.Is(err, config.ErrProductionUnset) {
		t.Fatalf("prod %v", err)
	}
}

func TestLoginOriginalAndRetryStayInPostgres(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir)
	token, user, err := st.Login("demo-admin", "admin-demo")
	if err != nil || user.ID != "demo-admin" {
		t.Fatal(err, user)
	}
	held, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	if got, err := held.User(token); err != nil || got.ID != user.ID {
		t.Fatal(err, got)
	}
	if err := held.Logout(token); err != nil {
		t.Fatal(err)
	}
	if _, err := held.User(token); err == nil {
		t.Fatal("session survived logout")
	}

	admin := mustUser("demo-admin")
	caseID, err := held.CreateCase(admin, "登录与重试", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("采购文件要求质保36个月。")
	docID, err := held.AddDocument(context.Background(), admin, caseID, "procurement", "purchase.txt", "text/plain", body)
	if err != nil {
		t.Fatal(err)
	}
	doc, got, err := held.Original(admin, caseID, docID)
	if err != nil || doc.ID != docID || !bytes.Equal(got, body) {
		t.Fatalf("original %v %q", err, got)
	}
	detail, err := held.GetCase(admin, caseID)
	if err != nil || len(detail.Documents) != 1 || !detail.Documents[0].Ready {
		t.Fatalf("case %+v %v", detail, err)
	}
	if err := held.AddMember(admin, caseID, "nobody"); err == nil {
		t.Fatal("unknown member was accepted")
	}
	if err := held.AddMember(admin, caseID, "demo-reviewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := held.GetCase(mustUser("demo-viewer"), caseID); err == nil {
		t.Fatal("viewer saw the case")
	}
	if _, err := held.GetCase(mustUser("demo-reviewer"), caseID); err != nil {
		t.Fatal(err)
	}
	if _, err := held.AddDocument(context.Background(), admin, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。")); err != nil {
		t.Fatal(err)
	}
	if err := held.Reparse(context.Background(), admin, caseID, docID); err != nil {
		t.Fatal(err)
	}

	held.UseModels(badModels{})
	runID, err := held.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	ws := awaitReview(t, held, admin, caseID, runID)
	if ws.View.State != "partial" || len(ws.Findings) != 1 || ws.Findings[0].Origin != "rule" || !strings.Contains(ws.View.PartialReason, "无法解析") {
		t.Fatalf("degraded run %+v findings %d", ws.View, len(ws.Findings))
	}
	if ws.Snapshot.RuleSetHash != engine.RuleSetHash() {
		t.Fatalf("snapshot %+v", ws.Snapshot)
	}
	held.UseModels(scriptModels{})
	if err := held.Retry(context.Background(), admin, caseID, runID); err == nil {
		t.Fatal("a different model overwrote a frozen run")
	}
	newID, err := held.StartReview(context.Background(), admin, caseID)
	if err != nil || newID == runID {
		t.Fatalf("changed model needs a new run: %s %v", newID, err)
	}
	ws = awaitReview(t, held, admin, caseID, newID)
	if ws.View.State != "awaiting_human" || ws.Snapshot.RuleSetHash != engine.RuleSetHash() || len(ws.Snapshot.DocumentIDs) != 2 {
		t.Fatalf("retried %+v snapshot %+v", ws.View, ws.Snapshot)
	}
	if err := held.Retry(context.Background(), admin, caseID, newID); err != nil {
		t.Fatal(err)
	}
	ws = awaitReview(t, held, admin, caseID, newID)
	if err := audit.Verify(asEvents(ws.Audit)); err != nil {
		t.Fatal(err)
	}
	foundRetry := false
	for _, event := range ws.Audit {
		foundRetry = foundRetry || event.EventType == "review_retried"
	}
	if !foundRetry {
		t.Fatalf("retry audit missing: %+v", ws.Audit)
	}
	events, err := held.EventsAfter(admin, caseID, newID, 0)
	if err != nil || len(events) == 0 || events[0].Type == "" {
		t.Fatalf("events %+v %v", events, err)
	}
}

func TestStoredReviewSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir)
	st.UseModels(scriptModels{})
	admin := mustUser("demo-admin")

	caseID, err := st.CreateCase(admin, "付款与质保", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "purchase.txt", "text/plain", []byte(
		"采购文件要求质保36个月。采购方案约定20%预付款、验收通过后支付70%、质保期满10%。采购金额为120万元。",
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, caseID, "draft_contract", "draft.txt", "text/plain", []byte(
		"拟签合同约定质保24个月。合同约定设备到货后支付90%。合同金额为100万元。",
	)); err != nil {
		t.Fatal(err)
	}
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	ws := awaitReview(t, st, admin, caseID, runID)
	assertExecutionSequence(t, ws.Execution.Events)
	if ws.View.State != "awaiting_human" {
		t.Fatalf("state %s", ws.View.State)
	}
	var payment, warranty *store.Finding
	for i := range ws.Findings {
		switch ws.Findings[i].Domain {
		case "payment_sum":
			payment = &ws.Findings[i]
		case "warranty":
			warranty = &ws.Findings[i]
		}
	}
	if payment == nil || payment.Relation != "conflict" || warranty == nil || warranty.Relation != "conflict" {
		t.Fatalf("findings %+v", ws.Findings)
	}
	if ws.Snapshot.RuleSetHash != engine.RuleSetHash() || len(ws.Snapshot.DocumentIDs) != 2 {
		t.Fatalf("snapshot %+v", ws.Snapshot)
	}
	if err := audit.Verify(asEvents(ws.Audit)); err != nil {
		t.Fatal(err)
	}
	if err := st.Decide(admin, caseID, runID, payment.ID, "pay-1", "confirm_risk", "付款比例合计不同", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.RewriteAudit(runID, ws.Audit[0].EventID, "intruder"); !errors.Is(err, audit.ErrBrokenChain) {
		t.Fatalf("rewrite %v", err)
	}

	again, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = again.Close() })
	ws, err = again.Workspace(admin, caseID, runID)
	if err != nil {
		t.Fatal(err)
	}
	assertExecutionSequence(t, ws.Execution.Events)
	if ws.Snapshot.RuleSetHash != engine.RuleSetHash() || ws.Snapshot.ComparatorRef != engine.ComparatorRef {
		t.Fatalf("reopened snapshot %+v", ws.Snapshot)
	}
	if err := audit.Verify(asEvents(ws.Audit)); err != nil {
		t.Fatal(err)
	}
	foundDecision := false
	for _, event := range ws.Audit {
		foundDecision = foundDecision || event.EventType == "human_decision"
	}
	if !foundDecision {
		t.Fatalf("decision audit missing: %+v", ws.Audit)
	}
	decisions := again.DecisionsFor(payment.ID)
	if len(decisions) != 1 || decisions[0].Action != "confirm_risk" || decisions[0].NewDisposition != "confirmed" {
		t.Fatalf("decisions %+v", decisions)
	}
	fresh := openTestStore(t, t.TempDir())
	if cases := fresh.ListCases(admin); len(cases) != 0 {
		t.Fatalf("another test schema saw %d cases", len(cases))
	}
}

func openTestStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	st, err := store.OpenTest(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func mustUser(id string) accounts.User {
	user, ok := accounts.ByID(id)
	if !ok {
		panic(id)
	}
	return user
}

func asEvents(records []store.AuditRecord) []audit.Event {
	out := make([]audit.Event, len(records))
	for i, rec := range records {
		out[i] = audit.Event{
			EventID: rec.EventID, RunID: rec.RunID, Actor: rec.Actor, Seq: rec.Seq,
			EventType: rec.EventType, PrevHash: rec.PrevHash, Hash: rec.Hash,
			CorrelationIDs: append([]string(nil), rec.CorrelationIDs...),
		}
	}
	return out
}

type badModels struct{}

func (badModels) Name() string              { return "bad-model" }
func (m badModels) ConfigurationID() string { return m.Name() }

func (badModels) Warranty(context.Context, []model.Material) (model.Warranty, error) {
	return model.Warranty{}, model.ErrUnusable
}

func (badModels) Transcribe(context.Context, string, []byte) (string, error) {
	return "", model.ErrUnusable
}

type scriptModels struct{}

func (scriptModels) Name() string              { return "script-model" }
func (m scriptModels) ConfigurationID() string { return m.Name() }

func (scriptModels) Warranty(_ context.Context, docs []model.Material) (model.Warranty, error) {
	sources := make([]engine.Source, len(docs))
	for i, doc := range docs {
		status := "failed"
		if doc.Ready {
			status = "ready"
		}
		sources[i] = engine.Source{ID: doc.ID, Role: doc.Role, ParseStatus: status, Text: doc.Text}
	}
	drafts := engine.Compare(sources)
	if len(drafts) == 0 {
		return model.Warranty{Claim: "质保月数一致", Relation: "compatible", Disposition: "false_positive"}, nil
	}
	var quotes []model.Quote
	for _, anchor := range drafts[0].Anchors {
		if anchor.Status == "located" && anchor.Quote != "" {
			quotes = append(quotes, model.Quote{DocumentID: anchor.DocumentID, Quote: anchor.Quote})
		}
	}
	disposition := "insufficient_evidence"
	if drafts[0].Relation == "conflict" {
		disposition = "confirm_risk"
	}
	return model.Warranty{
		Claim: "模型建议：" + drafts[0].Claim, Relation: drafts[0].Relation,
		Disposition: disposition, Quotes: quotes,
	}, nil
}

func (scriptModels) Transcribe(context.Context, string, []byte) (string, error) {
	return "", model.ErrUnusable
}

func awaitReview(t *testing.T, st *store.Store, user accounts.User, caseID, runID string) store.Workspace {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		workspace, err := st.Workspace(user, caseID, runID)
		if err != nil {
			t.Fatal(err)
		}
		if workspace.Execution != nil && workspace.Execution.Attempt != nil && !activeAttempt(workspace.Execution.Attempt.State) {
			return workspace
		}
		select {
		case <-deadline.C:
			t.Fatalf("review %s did not reach a terminal attempt", runID)
		case <-ticker.C:
		}
	}
}

func activeAttempt(state string) bool { return state == "accepted" || state == "running" }

func assertExecutionSequence(t *testing.T, events []store.ExecutionEvent) {
	t.Helper()
	for i, event := range events {
		if event.Seq != i+1 {
			t.Fatalf("execution event sequence is not contiguous: index=%d seq=%d type=%s phase=%s", i, event.Seq, event.EventType, event.Phase)
		}
	}
}
