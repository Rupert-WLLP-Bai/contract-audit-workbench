package store

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"contractaudit/internal/audit"
	"contractaudit/internal/telemetry"
)

func TestExporterFailurePreservesReviewDecisionAndAuditAfterReload(t *testing.T) {
	var requests atomic.Int64
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"message":"collector unavailable"}`))
	}))
	defer collector.Close()
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	exporter, err := telemetry.New(telemetry.Config{Endpoint: collector.URL, Service: "audit.test", SampleRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	st.UseTelemetry(exporter)
	st.UseModels(revisionScript{})
	admin := revisionAdmin(t)
	caseID, err := st.CreateCase(admin, "Exporter isolation", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []struct{ role, text string }{
		{"procurement", "采购文件要求质保36个月。"},
		{"draft_contract", "拟签合同约定质保24个月。"},
	} {
		if _, err := st.AddDocument(context.Background(), admin, caseID, document.role, document.role+".txt", "text/plain", []byte(document.text)); err != nil {
			t.Fatal(err)
		}
	}
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	workspace := waitTerminalReview(t, st, admin, caseID, runID)
	if workspace.Execution.Attempt.State != "completed" || len(workspace.Findings) != 1 || workspace.Findings[0].Relation != "conflict" {
		t.Fatalf("collector failure damaged the business review: %+v", workspace)
	}
	if err := st.Decide(admin, caseID, runID, workspace.Findings[0].ID, "exporter-decision", "confirm_risk", "两份材料质保月数不同", 1); err != nil {
		t.Fatal(err)
	}
	before, err := st.Workspace(admin, caseID, runID)
	if err != nil {
		t.Fatal(err)
	}
	originalSnapshot, originalFindings, originalDecisions := snapshotRow(t, st, runID), findingRow(t, st, runID), decisionRow(t, st, runID)
	st.UseTelemetry(nil)
	stats := exporter.Stats()
	if requests.Load() == 0 || stats.FailedRequests == 0 || stats.Exported != 0 || stats.Dropped == 0 {
		t.Fatalf("the collector failure was not exercised: requests=%d stats=%+v", requests.Load(), stats)
	}
	reloaded, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	history, err := reloaded.Workspace(admin, caseID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotRow(t, reloaded, runID) != originalSnapshot || findingRow(t, reloaded, runID) != originalFindings ||
		decisionRow(t, reloaded, runID) != originalDecisions || !reflect.DeepEqual(before.Audit, history.Audit) ||
		history.Execution.Attempt.State != "completed" {
		t.Fatal("failed technical delivery altered persisted review history, decisions or audit")
	}
	chain := make([]audit.Event, len(history.Audit))
	for i, event := range history.Audit {
		chain[i] = event.event()
	}
	if err := audit.Verify(chain); err != nil {
		t.Fatal(err)
	}
}
