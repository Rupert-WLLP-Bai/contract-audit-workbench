package engine_test

import (
	"testing"

	"contractaudit/internal/engine"
)

func TestScheduleWithoutCommonStartIsInsufficient(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "attachment", ParseStatus: "ready", Text: "技术规范要求交付后30日完成初验。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "合同写乙方申请后30个工作日完成初验。"},
	}
	ev := engine.EvaluateSchedule(docs)
	if ev.Relation != "insufficient" || ev.Domain != "schedule" || ev.RuleVersion != engine.ScheduleRuleVersion {
		t.Fatalf("%+v", ev)
	}
	if ev.Reason != "验收时限没有共同起点：技术附件 交付后 30 个日历日，拟签合同 乙方申请后 30 个工作日。这条比对是信息不足。" {
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

func TestScheduleSameStartDifferentDayKindConflicts(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "交付后30日完成初验。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "交付后30个工作日完成初验。"},
	}
	ev := engine.EvaluateSchedule(docs)
	if ev.Relation != "conflict" {
		t.Fatalf("%+v", ev)
	}
	if ev.Reason != "验收时限不一致：采购文件 交付后 30 个日历日，拟签合同 交付后 30 个工作日。" {
		t.Fatalf("reason %q", ev.Reason)
	}
}

func TestScheduleCalendarDaySynonymIsCompatible(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "交付后30日完成初验。"},
		{ID: "b", Role: "award", ParseStatus: "ready", Text: "交付后30天完成初验。"},
	}
	ev := engine.EvaluateSchedule(docs)
	if ev.Relation != "compatible" || engine.Findings(ev) != nil {
		t.Fatalf("%+v", ev)
	}
	if ev.Reason != "已解析材料的验收时限相同，都是交付后 30 个日历日。" {
		t.Fatalf("reason %q", ev.Reason)
	}
}

func TestScheduleAbsentIsNotApplicable(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "采购文件要求质保36个月。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "拟签合同约定质保24个月。"},
	}
	ev := engine.EvaluateSchedule(docs)
	if ev.Relation != "not_applicable" || engine.Findings(ev) != nil {
		t.Fatalf("%+v", ev)
	}
}

func TestSchedulePageComesFromTheFormFeed(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "封面未列期限。\f交付后30日完成初验。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "交付后45日完成初验。"},
	}
	ev := engine.EvaluateSchedule(docs)
	if ev.Relation != "conflict" {
		t.Fatalf("%+v", ev)
	}
	var sawPage bool
	for _, fact := range ev.Facts {
		if fact.DocumentID != "a" {
			continue
		}
		if fact.Anchor.Page == nil || *fact.Anchor.Page != 2 {
			t.Fatalf("page %+v", fact.Anchor)
		}
		sawPage = true
	}
	if !sawPage {
		t.Fatal("paged schedule lost its page")
	}
}
