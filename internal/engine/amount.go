package engine

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"contractaudit/internal/labels"
)

var amountRE = regexp.MustCompile(`(?:采购金额|合同金额|合同总价)(?:为|是|[:：])?\s*(\d+(?:\.\d+)?)\s*(万元|元)`)

type amountHit struct {
	doc     Source
	surface string
	fen     int64
	anchor  Anchor
}

// EvaluateAmount compares procurement amounts. 万元 and 元 are normalized
// before the comparison; a conflict still quotes the unit that was written.
func EvaluateAmount(docs []Source) Evaluation {
	hits, invalid := collectAmounts(docs)
	ev := Evaluation{Domain: "amount", RuleVersion: AmountRuleVersion, Missing: missingAnchors(docs)}
	ev.Missing = append(ev.Missing, invalid...)
	if len(hits) == 0 {
		if len(invalid) > 0 {
			ev.Relation = RelInsufficient
			ev.Reason = "材料中的金额超出支持的精度或范围，这条比对是信息不足。"
			return ev
		}
		return absence(ev, docs, "采购金额")
	}
	ev.Facts = amountFacts(hits)
	if !sameAmount(hits) {
		ev.Relation = RelConflict
		ev.Reason = amountList(hits)
		return ev
	}
	seen := map[string]bool{}
	for _, hit := range hits {
		seen[hit.doc.ID] = true
	}
	if covered(docs, seen) {
		ev.Relation = RelCompatible
		ev.Reason = fmt.Sprintf("已解析材料写明的采购金额相同，都是 %s 元。", formatYuan(hits[0].fen))
		return ev
	}
	ev.Relation = RelInsufficient
	ev.Reason = partialAmount(hits, docs)
	return ev
}

func collectAmounts(docs []Source) ([]amountHit, []Anchor) {
	var hits []amountHit
	var invalid []Anchor
	for _, doc := range docs {
		if doc.ParseStatus != "ready" {
			continue
		}
		if hit, ok := firstAmount(doc); ok {
			hits = append(hits, hit)
		} else if amountRE.MatchString(doc.ir().NormalizedText) {
			invalid = append(invalid, Anchor{DocumentID: doc.ID, Status: "missing", Reason: "金额数值超出支持的精度或范围，不能作为已核验金额。"})
		}
	}
	return hits, invalid
}

func amountFacts(hits []amountHit) []Fact {
	var facts []Fact
	for _, hit := range hits {
		value, unit := int(hit.fen/100), "元"
		if hit.fen%100 != 0 {
			value, unit = int(hit.fen), "分"
		}
		facts = append(facts, Fact{
			DocumentID: hit.doc.ID, Value: value, Unit: unit, Anchor: hit.anchor,
		})
	}
	return facts
}

func firstAmount(doc Source) (amountHit, bool) {
	ir := doc.ir()
	text := ir.NormalizedText
	if text == "" {
		text = doc.Text
	}
	loc := amountRE.FindStringSubmatchIndex(text)
	if loc == nil {
		return amountHit{}, false
	}
	quote := text[loc[0]:loc[1]]
	num := text[loc[2]:loc[3]]
	unit := text[loc[4]:loc[5]]
	fen, ok := amountFen(num, unit)
	if !ok {
		return amountHit{}, false
	}
	anchor, ok := bindSpan(doc, quote, loc[0])
	if !ok {
		return amountHit{}, false
	}
	return amountHit{doc: doc, surface: num + unit, fen: fen, anchor: anchor}, true
}

func amountFen(num, unit string) (int64, bool) {
	whole, frac, _ := strings.Cut(num, ".")
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w < 0 {
		return 0, false
	}
	var scale int64
	var places int
	switch unit {
	case "元":
		scale, places = 100, 2
	case "万元":
		scale, places = 1000000, 6
	default:
		return 0, false
	}
	frac = strings.TrimRight(frac, "0")
	if len(frac) > places {
		return 0, false // Never silently round an amount.
	}
	frac += strings.Repeat("0", places-len(frac))
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil || w > (math.MaxInt64-f)/scale {
		return 0, false // Check BEFORE multiplication; overflow is not a value.
	}
	return w*scale + f, true
}

func sameAmount(hits []amountHit) bool {
	return allValuesEqual(hits, func(item amountHit) int64 { return item.fen })
}

func amountList(hits []amountHit) string {
	parts := make([]string, len(hits))
	for i, hit := range hits {
		parts[i] = labels.Role(hit.doc.Role) + " " + hit.surface
	}
	return "采购金额不一致：" + strings.Join(parts, "，") + "。"
}

func partialAmount(hits []amountHit, docs []Source) string {
	if len(readyIDs(docs)) < 2 && len(failedDocs(docs)) == 0 {
		return "仅有一份独立材料写明采购金额，无法完成跨文件比对，这条比对是信息不足。"
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
		return fmt.Sprintf("只有%s写明了采购金额，还有 %d 份已解析材料没有写明，这条比对是信息不足。", strings.Join(named, "、"), missing)
	}
	return "已解析材料写明的采购金额可以对照，但仍有材料未能解析，这条比对是信息不足。"
}

func formatYuan(fen int64) string {
	if fen%100 == 0 {
		return strconv.FormatInt(fen/100, 10)
	}
	return fmt.Sprintf("%d.%02d", fen/100, fen%100)
}
