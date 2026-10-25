package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"contractaudit/internal/audit"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

type unavailableModels struct{ err error }

func (unavailableModels) Name() string              { return "unavailable-model" }
func (m unavailableModels) ConfigurationID() string { return m.Name() }
func (m unavailableModels) Warranty(context.Context, []model.Material) (model.Warranty, error) {
	return model.Warranty{}, m.err
}
func (m unavailableModels) Transcribe(context.Context, string, []byte) (string, error) {
	return "", m.err
}

func TestRuleResultsSurviveMissingOrUnusableModel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		models store.Models
		reason string
	}{
		{"not-configured", nil, "未配置"},
		{"upstream-unavailable", unavailableModels{model.ErrRequest}, "调用审查模型失败"},
		{"malformed-completion", unavailableModels{model.ErrUnusable}, "无法解析"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, t.TempDir())
			st.UseModels(tc.models)
			admin := mustUser("demo-admin")
			caseID, err := st.CreateCase(admin, "规则结果不可因模型故障丢失", "procurement_ict")
			if err != nil {
				t.Fatal(err)
			}
			for role, body := range map[string]string{
				"procurement":    "采购文件要求质保36个月。\n合同总价为100万元。",
				"draft_contract": "拟签合同约定质保24个月。\n合同总价为120万元。",
			} {
				if _, err := st.AddDocument(context.Background(), admin, caseID, role, role+".txt", "text/plain", []byte(body)); err != nil {
					t.Fatal(err)
				}
			}
			runID, err := st.StartReview(context.Background(), admin, caseID)
			if err != nil {
				t.Fatal(err)
			}
			ws := awaitReview(t, st, admin, caseID, runID)
			if ws.View.State != "partial" || !strings.Contains(ws.View.PartialReason, tc.reason) {
				t.Fatalf("degraded run %+v", ws.View)
			}
			domains := map[string]bool{}
			for _, f := range ws.Findings {
				domains[f.Domain] = true
				if f.Origin != "rule" || len(f.Anchors) == 0 {
					t.Fatalf("ungrounded fallback %+v", f)
				}
				for _, a := range f.Anchors {
					if a.Status != "located" {
						t.Fatalf("anchor %+v", a)
					}
				}
			}
			if !domains["warranty"] || !domains["amount"] {
				t.Fatalf("deterministic domains missing: %v", domains)
			}
			if len(ws.Evaluations) != 2 {
				t.Fatalf("expected both applicable rule evaluations, got %d", len(ws.Evaluations))
			}
			f := ws.Findings[0]
			if err := st.Decide(admin, caseID, runID, f.ID, "fallback-1", "confirm_risk", "核对原文后确认", 1); err != nil {
				t.Fatal(err)
			}
			originalAttemptID := ws.Execution.Attempt.ID
			if err := st.Retry(context.Background(), admin, caseID, runID); err != nil {
				t.Fatal(err)
			}
			latest := awaitReview(t, st, admin, caseID, runID)
			if latest.Execution.Attempt.ID == originalAttemptID || len(latest.Execution.Attempts) != 2 {
				t.Fatalf("retry did not append an attempt: %+v", latest.Execution)
			}
			old, err := st.Workspace(admin, caseID, runID, originalAttemptID)
			if err != nil {
				t.Fatal(err)
			}
			if len(st.DecisionsFor(f.ID)) != 1 || len(old.Findings) != len(ws.Findings) || len(old.Execution.Events) != len(ws.Execution.Events) {
				t.Fatalf("retry changed old results, events, or decision: old=%+v latest=%+v", old.Execution, latest.Execution)
			}
			if latest.View.State != "partial" || len(st.DecisionsFor(f.ID)) != 1 || len(latest.Findings) != len(ws.Findings) {
				t.Fatalf("degraded review became complete or lost a decision: %+v", latest.View)
			}
			if err := audit.Verify(asEvents(latest.Audit)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnavailableModelWithNoRuleFindingIsStillPartial(t *testing.T) {
	st := openTestStore(t, t.TempDir())
	admin := mustUser("demo-admin")
	caseID, err := st.CreateCase(admin, "无发现不代表模型已完成", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"procurement", "draft_contract"} {
		if _, err := st.AddDocument(context.Background(), admin, caseID, role, role+".txt", "text/plain", []byte("质保期为36个月。")); err != nil {
			t.Fatal(err)
		}
	}
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	ws := awaitReview(t, st, admin, caseID, runID)
	if len(ws.Findings) != 0 || ws.View.State != "partial" || ws.View.PartialReason == "" {
		t.Fatalf("false completion %+v", ws.View)
	}
}

func TestCanceledReviewDoesNotCreateAnUnobservableRun(t *testing.T) {
	st := openTestStore(t, t.TempDir())
	admin := mustUser("demo-admin")
	caseID, err := st.CreateCase(admin, "取消请求", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.StartReview(ctx, admin, caseID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if err := st.Retry(ctx, admin, caseID, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("retry cancel: %v", err)
	}
	if detail, err := st.GetCase(admin, caseID); err != nil || detail.Run != nil {
		t.Fatalf("run created after cancel: %+v %v", detail, err)
	}
}
