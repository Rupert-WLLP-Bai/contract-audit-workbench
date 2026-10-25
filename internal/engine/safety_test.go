package engine_test

import (
	"strings"
	"testing"

	"contractaudit/internal/engine"
)

func TestCrossFileAgreementRequiresIndependentSources(t *testing.T) {
	tests := []struct {
		name string
		text string
		eval func([]engine.Source) engine.Evaluation
	}{
		{"warranty", "质保36个月。", engine.Evaluate},
		{"payment-sum", "预付款20%。", engine.EvaluatePaymentSum},
		{"payment-condition", "预付款20%。", engine.EvaluatePaymentCondition},
		{"schedule", "交付后30日完成验收。", engine.EvaluateSchedule},
		{"amount", "采购金额为120万元。", engine.EvaluateAmount},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			one := engine.Source{ID: "a", ParseStatus: "ready", Text: tc.text}
			for _, docs := range [][]engine.Source{{one}, {one, one}, nil, {{ID: "a", ParseStatus: "failed"}, {ID: "b", ParseStatus: "pending"}}} {
				got := tc.eval(docs)
				if got.Relation != engine.RelInsufficient {
					t.Fatalf("incomplete sources classified %s: %+v", got.Relation, docs)
				}
				if len(docs) == 2 && docs[0].ParseStatus == "failed" && len(got.Missing) != 2 {
					t.Fatalf("parse failures lost their explicit evidence state: %+v", got)
				}
			}
		})
	}
}

func TestPaymentOrderDoesNotCreateConflict(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", ParseStatus: "ready", Text: "预付款20%，验收通过后支付70%，质保期满10%。"},
		{ID: "b", ParseStatus: "ready", Text: "质保期满10%，20%预付款，验收通过后支付70%。"},
	}
	got := engine.EvaluatePaymentCondition(docs)
	if got.Relation != engine.RelCompatible || len(got.Facts) != 6 {
		t.Fatalf("reordered equal milestones: %+v", got)
	}
}

func TestAmountOverflowIsExplicitlyInsufficient(t *testing.T) {
	for _, text := range []string{"合同金额为9223372036854775807万元。", "合同金额为92233720368547758.08元。", "合同金额为0.001元。"} {
		docs := []engine.Source{{ID: "a", ParseStatus: "ready", Text: text}, {ID: "b", ParseStatus: "ready", Text: text}}
		got := engine.EvaluateAmount(docs)
		if got.Relation != engine.RelInsufficient || len(got.Facts) != 0 || len(got.Missing) != 2 {
			t.Fatalf("unsupported amount became a confirmed value: %+v", got)
		}
		for _, a := range got.Missing {
			if a.Start != 0 || a.End != 0 || a.Page != nil || a.Reason == "" {
				t.Fatalf("unsupported value fabricated evidence: %+v", a)
			}
		}
	}
}

func TestFractionalAmountKeepsExactFactAndClaim(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", ParseStatus: "ready", Text: "采购金额为0.000001万元。"},
		{ID: "b", ParseStatus: "ready", Text: "合同金额为0.01元。"},
	}
	got := engine.EvaluateAmount(docs)
	if got.Relation != engine.RelCompatible || !strings.Contains(got.Reason, "0.01 元") {
		t.Fatalf("fractional amount truncated: %+v", got)
	}
	for _, fact := range got.Facts {
		if fact.Value != 1 || fact.Unit != "分" {
			t.Fatalf("fractional fact truncated: %+v", fact)
		}
	}
}
