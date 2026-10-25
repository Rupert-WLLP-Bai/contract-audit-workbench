package store

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"contractaudit/internal/fault"
	"contractaudit/internal/model"
)

func TestSameModelNameAtDifferentEndpointRequiresNewRun(t *testing.T) {
	st, admin := openExecutionStore(t)
	var firstCalls, secondCalls atomic.Int32
	provider := func(calls *atomic.Int32) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"relation\":\"compatible\"}"}}]}`))
		}))
		t.Cleanup(server.Close)
		return server
	}
	first, second := provider(&firstCalls), provider(&secondCalls)
	st.UseModels(model.New(model.Config{BaseURL: first.URL, ChatModel: "same-route"}))
	caseID := createExecutionCase(t, st, admin)
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	before := waitTerminalReview(t, st, admin, caseID, runID)
	st.UseModels(model.New(model.Config{BaseURL: second.URL, ChatModel: "same-route"}))
	detail, err := st.GetCase(admin, caseID)
	if err != nil || !detail.ConfigurationPending {
		t.Fatalf("endpoint replacement with unchanged model name was hidden: pending=%v err=%v", detail.ConfigurationPending, err)
	}
	var conflict *fault.Error
	if err := st.Retry(context.Background(), admin, caseID, runID); !errors.As(err, &conflict) || conflict.Status != 409 || secondCalls.Load() != 0 {
		t.Fatalf("old run admitted a changed provider: err=%v second_calls=%d", err, secondCalls.Load())
	}
	nextID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil || nextID == runID {
		t.Fatalf("new endpoint did not receive a new run: id=%s err=%v", nextID, err)
	}
	next := waitTerminalReview(t, st, admin, caseID, nextID)
	old, err := st.Workspace(admin, caseID, runID)
	if err != nil || old.Execution.Attempt.ID != before.Execution.Attempt.ID || *old.Execution.Attempt.OutputHash != *before.Execution.Attempt.OutputHash {
		t.Fatalf("provider replacement changed history: err=%v", err)
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 || next.PreviousRunID != runID || next.RevisionComparisonReason == "" || len(next.Revisions) != 0 {
		t.Fatalf("provider boundary mixed calls or invented revision attribution: first=%d second=%d reason=%q", firstCalls.Load(), secondCalls.Load(), next.RevisionComparisonReason)
	}
}
