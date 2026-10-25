package engine_test

import (
	"testing"

	"contractaudit/internal/engine"
)

func TestAmountConflictKeepsTheWrittenUnit(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "采购金额为120万元。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "合同金额为100万元。"},
	}
	ev := engine.EvaluateAmount(docs)
	if ev.Relation != "conflict" || ev.Domain != "amount" || ev.RuleVersion != engine.AmountRuleVersion {
		t.Fatalf("%+v", ev)
	}
	if ev.Reason != "采购金额不一致：采购文件 120万元，拟签合同 100万元。" {
		t.Fatalf("reason %q", ev.Reason)
	}
	drafts := engine.Findings(ev)
	if len(drafts) != 1 || len(drafts[0].Anchors) != 2 {
		t.Fatalf("%+v", drafts)
	}
	for _, anchor := range drafts[0].Anchors {
		assertLocated(t, docs, anchor)
	}
}

func TestAmountWanAndYuanCanAgree(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "采购金额为120万元。"},
		{ID: "b", Role: "award", ParseStatus: "ready", Text: "合同金额为1200000元。"},
	}
	ev := engine.EvaluateAmount(docs)
	if ev.Relation != "compatible" || engine.Findings(ev) != nil {
		t.Fatalf("%+v", ev)
	}
	if ev.Reason != "已解析材料写明的采购金额相同，都是 1200000 元。" {
		t.Fatalf("reason %q", ev.Reason)
	}
}

func TestAmountOnOneSideIsInsufficient(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "采购金额为120万元。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "拟签合同未列总价。"},
	}
	ev := engine.EvaluateAmount(docs)
	if ev.Relation != "insufficient" {
		t.Fatalf("%+v", ev)
	}
	if ev.Reason != "只有采购文件写明了采购金额，还有 1 份已解析材料没有写明，这条比对是信息不足。" {
		t.Fatalf("reason %q", ev.Reason)
	}
}

func TestAmountAbsentIsNotApplicable(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "采购文件要求质保36个月。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "拟签合同约定质保24个月。"},
	}
	ev := engine.EvaluateAmount(docs)
	if ev.Relation != "not_applicable" || engine.Findings(ev) != nil {
		t.Fatalf("%+v", ev)
	}
}

func TestWarrantyDepositIsNotProcurementAmount(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "质保金为5万元。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "质保金为3万元。"},
	}
	ev := engine.EvaluateAmount(docs)
	if ev.Relation != "not_applicable" || engine.Findings(ev) != nil {
		t.Fatalf("%+v", ev)
	}
}
