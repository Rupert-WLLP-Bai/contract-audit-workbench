package engine

import (
	"fmt"
	"regexp"
	"strings"

	"contractaudit/internal/labels"
)

var scheduleRE = regexp.MustCompile(`(交付后|乙方申请后|到货后|验收后)(\d+)\s*(个工作日|个自然日|个日历日|工作日|日|天)`)

type scheduleHit struct {
	doc     Source
	trigger string
	days    int
	class   string
	anchor  Anchor
}

// EvaluateSchedule compares the first acceptance deadline in each ready document.
// Different trigger events have no common start and stay insufficient.
// The same trigger with a different count or calendar-versus-workday is a conflict.
func EvaluateSchedule(docs []Source) Evaluation {
	hits := collectSchedules(docs)
	ev := Evaluation{Domain: "schedule", RuleVersion: ScheduleRuleVersion, Missing: missingAnchors(docs)}
	if len(hits) == 0 {
		return absence(ev, docs, "验收时限")
	}
	for _, hit := range hits {
		ev.Facts = append(ev.Facts, Fact{
			DocumentID: hit.doc.ID, Value: hit.days, Unit: hit.class, Anchor: hit.anchor,
		})
	}
	if !sameTrigger(hits) {
		ev.Relation = RelInsufficient
		ev.Reason = scheduleList("验收时限没有共同起点：", hits) + "。这条比对是信息不足。"
		return ev
	}
	if !sameSchedule(hits) {
		ev.Relation = RelConflict
		ev.Reason = scheduleList("验收时限不一致：", hits) + "。"
		return ev
	}
	seen := map[string]bool{}
	for _, hit := range hits {
		seen[hit.doc.ID] = true
	}
	if covered(docs, seen) {
		ev.Relation = RelCompatible
		ev.Reason = "已解析材料的验收时限相同，都是" + scheduleDisplay(hits[0]) + "。"
		return ev
	}
	ev.Relation = RelInsufficient
	ev.Reason = partialSchedule(hits, docs)
	return ev
}

func collectSchedules(docs []Source) []scheduleHit {
	var hits []scheduleHit
	for _, doc := range docs {
		if doc.ParseStatus != "ready" {
			continue
		}
		if hit, ok := firstSchedule(doc); ok {
			hits = append(hits, hit)
		}
	}
	return hits
}

func firstSchedule(doc Source) (scheduleHit, bool) {
	ir := doc.ir()
	text := ir.NormalizedText
	if text == "" {
		text = doc.Text
	}
	loc := scheduleRE.FindStringSubmatchIndex(text)
	if loc == nil {
		return scheduleHit{}, false
	}
	quote := text[loc[0]:loc[1]]
	days, ok := atoiSpan(text, loc[4], loc[5])
	if !ok {
		return scheduleHit{}, false
	}
	anchor, ok := bindSpan(doc, quote, loc[0])
	if !ok {
		return scheduleHit{}, false
	}
	return scheduleHit{
		doc: doc, trigger: text[loc[2]:loc[3]], days: days, class: dayClass(text[loc[6]:loc[7]]), anchor: anchor,
	}, true
}

func dayClass(unit string) string {
	switch unit {
	case "个工作日", "工作日":
		return "工作日"
	default:
		return "日历日"
	}
}

func scheduleDisplay(hit scheduleHit) string {
	return fmt.Sprintf("%s %d 个%s", hit.trigger, hit.days, hit.class)
}

func scheduleList(prefix string, hits []scheduleHit) string {
	parts := make([]string, len(hits))
	for i, hit := range hits {
		parts[i] = labels.Role(hit.doc.Role) + " " + scheduleDisplay(hit)
	}
	return prefix + strings.Join(parts, "，")
}

func sameTrigger(hits []scheduleHit) bool {
	return allValuesEqual(hits, func(item scheduleHit) string { return item.trigger })
}

func sameSchedule(hits []scheduleHit) bool {
	return allValuesEqual(hits, func(item scheduleHit) struct {
		days  int
		class string
	} { return struct {
		days  int
		class string
	}{item.days, item.class} })
}

func partialSchedule(hits []scheduleHit, docs []Source) string {
	if len(readyIDs(docs)) < 2 && len(failedDocs(docs)) == 0 {
		return "仅有一份独立材料写明验收时限，无法完成跨文件比对，这条比对是信息不足。"
	}
	seen := map[string]bool{}
	named := make([]string, 0, len(hits))
	for _, hit := range hits {
		if seen[hit.doc.ID] {
			continue
		}
		seen[hit.doc.ID] = true
		named = append(named, labels.Role(hit.doc.Role))
	}
	missing := 0
	for _, doc := range docs {
		if doc.ParseStatus == "ready" && !seen[doc.ID] {
			missing++
		}
	}
	if missing > 0 {
		return fmt.Sprintf("只有%s写明了验收时限，还有 %d 份已解析材料没有写明，这条比对是信息不足。", strings.Join(named, "、"), missing)
	}
	return "已解析材料的验收时限可以对照，但仍有材料未能解析，这条比对是信息不足。"
}
