package engine_test

import (
	"testing"

	"contractaudit/internal/docir"
	"contractaudit/internal/engine"
)

func TestQualifiedWarrantyAbstainsAndPreservesEvidence(t *testing.T) {
	for _, clause := range []string{
		"质保不少于24个月", "质保至少为36个月", "最长质保期为36个月",
		"不提供质保24个月", "除电池外，质保36个月", "质保36个月，耗材除外",
		"质保24个月以上", "质保不超过36个月", "质保36个月以下",
	} {
		t.Run(clause, func(t *testing.T) {
			docs := []engine.Source{
				{ID: "a", ParseStatus: "ready", Text: clause + "。"},
				{ID: "b", ParseStatus: "ready", Text: "质保36个月。"},
			}
			ev, findings, err := engine.AcceptWarranty(docs, engine.Proposal{Relation: engine.RelCompatible, Claim: "没有冲突", Disposition: "false_positive"})
			if err != nil || ev.Relation != engine.RelInsufficient || len(ev.Uncertain) != 1 || len(ev.Facts) != 1 || len(findings) != 1 {
				t.Fatalf("qualified clause reduced to equality: ev=%+v findings=%+v err=%v", ev, findings, err)
			}
			anchor := ev.Uncertain[0]
			quote, ok := docir.Slice(docir.Build(docs[0].Text).NormalizedText, anchor.Start, anchor.End)
			if !ok || quote != clause || anchor.Quote != clause || anchor.Status != "located" || anchor.Reason == "" {
				t.Fatalf("qualified evidence was lost or forged: %+v", anchor)
			}
			if len(findings[0].Anchors) != 2 || findings[0].UnverifiedReason == "" || findings[0].Disposition == "false_positive" {
				t.Fatalf("guarded uncertainty lost: %+v", findings)
			}
		})
	}
}

func TestEqualQualifiedClausesStillRequireSemanticReview(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", ParseStatus: "ready", Text: "质保不少于24个月且保修期至多36个月。"},
		{ID: "b", ParseStatus: "ready", Text: "质保不少于24个月且保修期至多36个月。"},
	}
	ev := engine.Evaluate(docs)
	if ev.Relation != engine.RelInsufficient || len(ev.Facts) != 0 || len(ev.Uncertain) != 2 {
		t.Fatalf("equal ranges were incorrectly interpreted or sentence evidence duplicated: %+v", ev)
	}
}

func TestWarrantyQualifierScopeStopsAtSentenceBoundary(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", ParseStatus: "ready", Text: "维修费用不超过100元。质保36个月。最长交付期待定。"},
		{ID: "b", ParseStatus: "ready", Text: "质保36个月"},
	}
	ev := engine.Evaluate(docs)
	if ev.Relation != engine.RelCompatible || len(ev.Uncertain) != 0 {
		t.Fatalf("qualifier leaked across sentence boundary: %+v", ev)
	}
}
