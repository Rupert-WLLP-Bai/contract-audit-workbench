package engine_test

import (
	"testing"

	"contractaudit/internal/docir"
	"contractaudit/internal/engine"
)

func TestPaymentSumAndConditionConflictSeparately(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "采购方案约定20%预付款、验收通过后支付70%、质保期满10%。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "合同约定设备到货后支付90%。"},
	}
	sum := engine.EvaluatePaymentSum(docs)
	if sum.Relation != "conflict" || sum.Domain != "payment_sum" || sum.RuleVersion != engine.PaymentSumRuleVersion {
		t.Fatalf("%+v", sum)
	}
	if sum.Reason != "付款比例合计不一致：采购文件 100%，拟签合同 90%。" {
		t.Fatalf("reason %q", sum.Reason)
	}
	cond := engine.EvaluatePaymentCondition(docs)
	if cond.Relation != "conflict" || cond.Domain != "payment_condition" {
		t.Fatalf("%+v", cond)
	}
	if cond.Reason != "付款条件不一致：采购文件 预付款 20%、验收通过 70%、质保期满 10%，拟签合同 到货 90%。" {
		t.Fatalf("reason %q", cond.Reason)
	}
	sumDrafts := engine.Findings(sum)
	condDrafts := engine.Findings(cond)
	if len(sumDrafts) != 1 || len(condDrafts) != 1 {
		t.Fatalf("drafts %d %d", len(sumDrafts), len(condDrafts))
	}
	if len(sumDrafts[0].Anchors) != 4 {
		t.Fatalf("anchors %+v", sumDrafts[0].Anchors)
	}
	for _, anchor := range sumDrafts[0].Anchors {
		assertLocated(t, docs, anchor)
	}
}

func TestPaymentSynonymIsNotAConflict(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "预付款20%。"},
		{ID: "b", Role: "award", ParseStatus: "ready", Text: "预付款百分之20。"},
	}
	sum := engine.EvaluatePaymentSum(docs)
	cond := engine.EvaluatePaymentCondition(docs)
	if sum.Relation != "compatible" || cond.Relation != "compatible" {
		t.Fatalf("sum %+v cond %+v", sum, cond)
	}
	if sum.Reason != "已解析材料的付款比例合计相同，都是 20%。" {
		t.Fatalf("sum reason %q", sum.Reason)
	}
	if cond.Reason != "已解析材料的付款条件相同。" {
		t.Fatalf("cond reason %q", cond.Reason)
	}
	if engine.Findings(sum) != nil || engine.Findings(cond) != nil {
		t.Fatal("agreement became a finding")
	}
}

func TestPaymentOnOneSideIsInsufficient(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "预付款20%。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "拟签合同未写付款。"},
	}
	sum := engine.EvaluatePaymentSum(docs)
	if sum.Relation != "insufficient" {
		t.Fatalf("%+v", sum)
	}
	if sum.Reason != "只有采购文件写明了付款比例，还有 1 份已解析材料没有写明，这条比对是信息不足。" {
		t.Fatalf("reason %q", sum.Reason)
	}
	if len(engine.Findings(sum)) != 1 {
		t.Fatal("a one-sided payment schedule must stay a finding")
	}
}

func TestPaymentAbsentIsNotApplicable(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "采购文件要求质保36个月。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "拟签合同约定质保24个月。"},
	}
	sum := engine.EvaluatePaymentSum(docs)
	if sum.Relation != "not_applicable" || engine.Findings(sum) != nil {
		t.Fatalf("%+v", sum)
	}
}

func assertLocated(t *testing.T, docs []engine.Source, anchor engine.Anchor) {
	t.Helper()
	if anchor.Status != "located" || anchor.Page != nil {
		t.Fatalf("anchor %+v", anchor)
	}
	var text string
	for _, doc := range docs {
		if doc.ID == anchor.DocumentID {
			text = doc.Text
		}
	}
	quote, ok := docir.Slice(text, anchor.Start, anchor.End)
	if !ok || quote != anchor.Quote {
		t.Fatalf("quote %q slice %q", anchor.Quote, quote)
	}
}
