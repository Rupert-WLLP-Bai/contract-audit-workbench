package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"contractaudit/internal/model"
)

func TestFrozenDocumentConfigurationUsesPersistedSelection(t *testing.T) {
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required")
	}
	st := environment.harness
	admin := revisionAdmin(t)
	models := newParseConcurrencyModel()
	t.Cleanup(models.unblockAll)
	st.UseModels(models)
	ctx := context.Background()
	caseID, err := st.CreateCase(admin, "frozen document provider", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	documentID, err := st.AddDocument(ctx, admin, caseID, "procurement", "purchase.png", "image/png", []byte("image"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(ctx, admin, caseID, "draft_contract", "draft.txt", "text/plain", []byte("质保24个月")); err != nil {
		t.Fatal(err)
	}
	accepted, err := st.StartReviewAsync(ctx, admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimNextJob(ctx, "document-identity-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	runID := accepted.RunID
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = st.RunClaimedJob(ctx, claim)
	}()
	t.Cleanup(func() {
		models.unblockAll()
		<-done
	})
	waitParseConcurrencyTranscription(t, models.entered, "durable OCR")
	var selection AttemptParseSelection
	for _, item := range st.snapshot().AttemptParseSelections {
		if item.RunID == runID && item.DocumentID == documentID {
			selection = item
			break
		}
	}
	if selection.ParsedDocumentID == "" {
		t.Fatal("missing frozen selection")
	}
	var doc Document
	snapshot := st.snapshot()
	for _, item := range snapshot.documents(caseID) {
		if item.ID == documentID {
			doc = item
		}
	}
	if doc.ID == "" {
		t.Fatal("missing document")
	}
	scope := model.DelegatedScope{Kind: "document", RunID: runID, AttemptID: selection.AttemptID, DocumentID: documentID, ParseSelectionID: selection.ParsedDocumentID, CaseID: caseID, TenantID: doc.TenantID, OrgID: doc.OrgID, ActorID: claim.ActorID, JobID: claim.JobID, WorkerID: claim.WorkerID, Generation: claim.Generation}
	modelPool, err := newPool(ctx, environment.roleDSNs["model"], st.schema)
	if err != nil {
		t.Fatal(err)
	}
	defer modelPool.Close()
	tx, err := modelPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "model-gateway", Role: "model", TenantID: scope.TenantID, OrgID: scope.OrgID}); err != nil {
		t.Fatal(err)
	}
	if err := authorizeFrozenDocumentConfiguration(ctx, tx, scope, models.ConfigurationID()); err != nil {
		t.Fatalf("frozen provider rejected: %v", err)
	}
	if err := authorizeFrozenDocumentConfiguration(ctx, tx, scope, "other-provider"); !errors.Is(err, ErrModelConfigurationChanged) {
		t.Fatalf("changed provider: %v", err)
	}
	if err := authorizeFrozenDocumentConfiguration(ctx, tx, scope, ""); !errors.Is(err, ErrModelConfigurationChanged) {
		t.Fatalf("empty provider: %v", err)
	}
	wrongScope := scope
	wrongScope.ParseSelectionID = "wrong-selection"
	if err := authorizeFrozenDocumentConfiguration(ctx, tx, wrongScope, models.ConfigurationID()); !errors.Is(err, ErrModelScopeUnavailable) {
		t.Fatalf("wrong selection: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	modelStore := &Store{pool: modelPool, schema: st.schema}
	scope.ConfigurationID = models.ConfigurationID()
	if _, err := modelStore.ReserveModelChatCall(ctx, "document", scope, "ocr", 1, 20, 20, modelBudgetTestLimits(), "other-provider"); !errors.Is(err, ErrModelConfigurationChanged) {
		t.Fatalf("changed provider reservation: %v", err)
	}
	if _, err := modelStore.ReserveModelChatCall(ctx, "document", scope, "ocr", 1, 20, 20, modelBudgetTestLimits(), models.ConfigurationID()); err != nil {
		t.Fatalf("frozen provider reservation: %v", err)
	}
	standalone := model.DelegatedScope{Kind: "document", TenantID: scope.TenantID, OrgID: scope.OrgID, CaseID: scope.CaseID, ActorID: scope.ActorID, DocumentID: scope.DocumentID}
	if _, err := modelStore.ReserveModelChatCall(ctx, "document", standalone, "ocr", 1, 20, 20, modelBudgetTestLimits(), "current-provider"); err != nil {
		t.Fatalf("standalone current provider reservation: %v", err)
	}
}
