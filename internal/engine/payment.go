package engine

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"contractaudit/internal/labels"
)

// 20%预付款, 预付款百分之20, 预付款20%, 验收通过后支付70%, 质保期满10%.
var paymentRE = regexp.MustCompile(`(\d+)\s*%\s*预付款|预付款百分之(\d+)|预付款\s*(\d+)\s*%|(验收通过后|质保期满后|质保期满|设备到货后|到货后)支付\s*(\d+)\s*%|(验收通过后|质保期满后|质保期满|设备到货后|到货后)\s*(\d+)\s*%`)

type paymentPart struct {
	doc     Source
	event   string
	percent int
	anchor  Anchor
}

type paymentGroup struct {
	doc   Source
	total int
	parts []paymentPart
}

// EvaluatePaymentSum compares the total of stated payment percents.
// A sum difference is a separate finding from a milestone difference.
func EvaluatePaymentSum(docs []Source) Evaluation {
	groups := paymentGroups(docs)
	ev := Evaluation{Domain: "payment_sum", RuleVersion: PaymentSumRuleVersion, Facts: paymentFacts(groups), Missing: missingAnchors(docs)}
	return finishGroups(ev, docs, groups, "付款比例", sumReason, func(groups []paymentGroup) bool {
		for _, item := range groups[1:] {
			if item.total != groups[0].total {
				return false
			}
		}
		return true
	}, func(groups []paymentGroup) string {
		return fmt.Sprintf("已解析材料的付款比例合计相同，都是 %d%%。", groups[0].total)
	})
}

// EvaluatePaymentCondition compares milestone and percent pairs.
func EvaluatePaymentCondition(docs []Source) Evaluation {
	groups := paymentGroups(docs)
	ev := Evaluation{Domain: "payment_condition", RuleVersion: PaymentConditionRuleVersion, Facts: paymentFacts(groups), Missing: missingAnchors(docs)}
	return finishGroups(ev, docs, groups, "付款条件", conditionReason, func(groups []paymentGroup) bool {
		first := paymentComparisonKey(groups[0].parts)
		for _, item := range groups[1:] {
			if paymentComparisonKey(item.parts) != first {
				return false
			}
		}
		return true
	}, func(groups []paymentGroup) string {
		return "已解析材料的付款条件相同。"
	})
}

func finishGroups(ev Evaluation, docs []Source, groups []paymentGroup, kind string, conflict func([]paymentGroup) string, same func([]paymentGroup) bool, agree func([]paymentGroup) string) Evaluation {
	if len(groups) == 0 {
		return absence(ev, docs, kind)
	}
	if !same(groups) {
		ev.Relation = RelConflict
		ev.Reason = conflict(groups)
		return ev
	}
	seen := map[string]bool{}
	for _, item := range groups {
		seen[item.doc.ID] = true
	}
	if covered(docs, seen) {
		ev.Relation = RelCompatible
		ev.Reason = agree(groups)
		return ev
	}
	ev.Relation = RelInsufficient
	ev.Reason = partialReason(kind, groups, docs)
	return ev
}

func sumReason(groups []paymentGroup) string {
	parts := make([]string, len(groups))
	for i, item := range groups {
		parts[i] = fmt.Sprintf("%s %d%%", labels.Role(item.doc.Role), item.total)
	}
	return "付款比例合计不一致：" + strings.Join(parts, "，") + "。"
}

func conditionReason(groups []paymentGroup) string {
	parts := make([]string, len(groups))
	for i, item := range groups {
		parts[i] = labels.Role(item.doc.Role) + " " + paymentSignature(item.parts)
	}
	return "付款条件不一致：" + strings.Join(parts, "，") + "。"
}

func partialReason(kind string, groups []paymentGroup, docs []Source) string {
	if len(readyIDs(docs)) < 2 && len(failedDocs(docs)) == 0 {
		return "仅有一份独立材料写明" + kind + "，无法完成跨文件比对，这条比对是信息不足。"
	}
	seen := map[string]bool{}
	named := make([]string, 0, len(groups))
	for _, item := range groups {
		if seen[item.doc.ID] {
			continue
		}
		seen[item.doc.ID] = true
		named = append(named, labels.Role(item.doc.Role))
	}
	missing := 0
	for _, doc := range docs {
		if doc.ParseStatus == "ready" && !seen[doc.ID] {
			missing++
		}
	}
	if missing > 0 {
		return fmt.Sprintf("只有%s写明了%s，还有 %d 份已解析材料没有写明，这条比对是信息不足。", strings.Join(named, "、"), kind, missing)
	}
	return "已解析材料写明的" + kind + "可以对照，但仍有材料未能解析，这条比对是信息不足。"
}

func paymentFacts(groups []paymentGroup) []Fact {
	var out []Fact
	for _, group := range groups {
		for _, part := range group.parts {
			out = append(out, Fact{
				DocumentID: part.doc.ID, Value: part.percent, Unit: "%", Anchor: part.anchor,
			})
		}
	}
	return out
}

func paymentSignature(parts []paymentPart) string {
	bits := make([]string, len(parts))
	for i, part := range parts {
		bits[i] = fmt.Sprintf("%s %d%%", part.event, part.percent)
	}
	return strings.Join(bits, "、")
}

func paymentGroups(docs []Source) []paymentGroup {
	var groups []paymentGroup
	index := map[string]int{}
	for _, doc := range docs {
		if doc.ParseStatus != "ready" {
			continue
		}
		ir := doc.ir()
		text := ir.NormalizedText
		if text == "" {
			text = doc.Text
		}
		for _, loc := range paymentRE.FindAllStringSubmatchIndex(text, -1) {
			event, percent, quote, ok := paymentMatch(text, loc)
			if !ok {
				continue
			}
			anchor, ok := bindSpan(doc, quote, loc[0])
			if !ok {
				continue
			}
			part := paymentPart{doc: doc, event: event, percent: percent, anchor: anchor}
			i, found := index[doc.ID]
			if !found {
				index[doc.ID] = len(groups)
				groups = append(groups, paymentGroup{doc: doc})
				i = len(groups) - 1
			}
			groups[i].total += percent
			groups[i].parts = append(groups[i].parts, part)
		}
	}
	return groups
}

func paymentMatch(text string, loc []int) (event string, percent int, quote string, ok bool) {
	if len(loc) < 16 || loc[0] < 0 {
		return "", 0, "", false
	}
	quote = text[loc[0]:loc[1]]
	switch {
	case loc[2] >= 0:
		event = "预付款"
		percent, ok = atoiSpan(text, loc[2], loc[3])
	case loc[4] >= 0:
		event = "预付款"
		percent, ok = atoiSpan(text, loc[4], loc[5])
	case loc[6] >= 0:
		event = "预付款"
		percent, ok = atoiSpan(text, loc[6], loc[7])
	case loc[8] >= 0:
		event = paymentEvent(text[loc[8]:loc[9]])
		percent, ok = atoiSpan(text, loc[10], loc[11])
	case loc[12] >= 0:
		event = paymentEvent(text[loc[12]:loc[13]])
		percent, ok = atoiSpan(text, loc[14], loc[15])
	}
	return event, percent, quote, ok
}

func paymentEvent(raw string) string {
	switch raw {
	case "验收通过后":
		return "验收通过"
	case "质保期满", "质保期满后":
		return "质保期满"
	case "设备到货后", "到货后":
		return "到货"
	default:
		return raw
	}
}

func atoiSpan(text string, start, end int) (int, bool) {
	if start < 0 || end < start || end > len(text) {
		return 0, false
	}
	value, err := strconv.Atoi(text[start:end])
	if err != nil {
		return 0, false
	}
	return value, true
}

// Milestones form a multiset. Reordering the same clauses is not a change to
// their conditions. Keep paymentSignature in source order for readable claims.
func paymentComparisonKey(parts []paymentPart) string {
	bits := make([]string, len(parts))
	for i, part := range parts {
		bits[i] = fmt.Sprintf("%s:%d", part.event, part.percent)
	}
	sort.Strings(bits)
	return strings.Join(bits, "|")
}
