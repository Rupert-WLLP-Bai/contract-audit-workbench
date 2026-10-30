// Package engine is the MVP cross-file rule: warranty months stated in the materials.
// It does not call a model and it does not copy the archived rule catalogue.
package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"contractaudit/internal/docir"
	"contractaudit/internal/labels"
)

const (
	WarrantyRuleVersion = "warranty-months-v3"

	RelCompatible    = "compatible"
	RelConflict      = "conflict"
	RelInsufficient  = "insufficient"
	RelNotApplicable = "not_applicable"
)

// Source is one case document after interpretation.
type Source struct {
	ID          string
	Role        string
	ParseStatus string
	Text        string
	FromOCR     bool
	IR          docir.IR
}

// Anchor is a located or explicitly missing citation.
// Page is set only when the text IR observed a page break for that span.
type Anchor struct {
	DocumentID string
	Quote      string
	Status     string
	Start      int
	End        int
	Reason     string
	Page       *int
	ElementID  string
}

// Draft is a finding proposal. It is not a human decision.
type Draft struct {
	Domain           string
	Origin           string
	Relation         string
	Claim            string
	Disposition      string
	UnverifiedReason string
	Anchors          []Anchor
}

// Fact is one warranty month read from a located span.
type Fact struct {
	DocumentID string
	Value      int
	Unit       string
	Anchor     Anchor
}

// Evaluation is the warranty-month constraint. It is not a human decision,
// and a model sentence does not change Relation.
type Evaluation struct {
	Domain      string
	Relation    string
	RuleVersion string
	Reason      string
	Facts       []Fact
	Missing     []Anchor
	// Uncertain quotes are located, but their range/negation/exception semantics
	// cannot be reduced to a verified fixed-month value by this comparator.
	Uncertain []Anchor
}

var warrantyPattern = regexp.MustCompile(`(?:质量保证期|质保期|保修期|质保|保修)[^0-9\n]{0,20}(\d+)\s*个月`)
var warrantyException = regexp.MustCompile(`除.{0,40}外`)

type hit struct {
	doc     Source
	quote   string
	start   int
	end     int
	value   int
	page    *int
	element string
}

func (s Source) ir() docir.IR {
	if s.IR.ExtractionVersion != "" || s.IR.NormalizedText != "" || len(s.IR.Blocks) > 0 {
		return s.IR
	}
	return docir.Build(s.Text)
}

// Evaluate reads ready documents. Equal months across every ready document are
// compatible. A missing side or a failed parse is insufficient, not agreement.
func Evaluate(docs []Source) Evaluation {
	ev, hits, failed := warrantyEvidence(docs)
	if len(hits) == 0 && len(ev.Uncertain) == 0 {
		return absentWarranty(ev, docs, failed)
	}
	if len(ev.Uncertain) > 0 {
		ev.Relation = RelInsufficient
		ev.Reason = "质保条款含范围、否定或例外表达，当前规则仅核对明确的固定月数，需要人工核对；这条比对是信息不足。"
		return ev
	}
	if !sameWarranty(hits) {
		ev.Relation, ev.Reason = RelConflict, conflictClaim(hits)
		return ev
	}
	if len(failed) == 0 && covers(docs, hits) {
		ev.Relation = RelCompatible
		ev.Reason = fmt.Sprintf("已解析材料写明的质保月数相同，都是 %d 个月。", hits[0].value)
		return ev
	}
	ev.Relation, ev.Reason = RelInsufficient, insufficientClaim(hits, failed, docs)
	return ev
}

func warrantyEvidence(docs []Source) (Evaluation, []hit, []Source) {
	ev := Evaluation{Domain: "warranty", RuleVersion: WarrantyRuleVersion}
	var hits []hit
	var failed []Source
	for _, doc := range docs {
		if doc.ParseStatus != "ready" {
			failed = append(failed, doc)
			ev.Missing = append(ev.Missing, missingDocument(doc))
			continue
		}
		found, ambiguous := findWarranty(doc)
		hits = append(hits, found...)
		ev.Uncertain = append(ev.Uncertain, ambiguous...)
	}
	for _, item := range hits {
		ev.Facts = append(ev.Facts, Fact{DocumentID: item.doc.ID, Value: item.value, Unit: "个月",
			Anchor: Anchor{DocumentID: item.doc.ID, Quote: item.quote, Status: "located", Start: item.start,
				End: item.end, Page: item.page, ElementID: item.element}})
	}
	return ev, hits, failed
}

func absentWarranty(ev Evaluation, docs, failed []Source) Evaluation {
	ev.Relation = RelInsufficient
	switch {
	case len(docs) == 0:
		ev.Reason = "没有可比对的材料，这条比对是信息不足。"
	case len(failed) > 0:
		ev.Reason = "有材料未能解析，已解析材料也没有写明质保月数，这条比对是信息不足。"
	default:
		ev.Relation = RelNotApplicable
		ev.Reason = "已解析材料没有写明质保月数，本条规则不适用。"
	}
	return ev
}

func sameWarranty(hits []hit) bool {
	return allValuesEqual(hits, func(item hit) int { return item.value })
}

// Findings turns a conflict or an insufficient evaluation into one proposal.
// Compatible and not-applicable evaluations produce none.
func Findings(ev Evaluation) []Draft {
	if ev.Relation != RelConflict && ev.Relation != RelInsufficient {
		return nil
	}
	anchors := make([]Anchor, 0, len(ev.Facts)+len(ev.Missing)+len(ev.Uncertain))
	for _, fact := range ev.Facts {
		anchors = append(anchors, fact.Anchor)
	}
	anchors = append(anchors, ev.Missing...)
	anchors = append(anchors, ev.Uncertain...)
	draft := Draft{
		Domain: ev.Domain, Origin: "rule", Relation: ev.Relation, Claim: ev.Reason, Anchors: anchors,
	}
	if len(ev.Missing) > 0 {
		draft.UnverifiedReason = "有材料或数值未能完成解析核验，这条线索不能当成已经核对完全。"
	}
	if len(ev.Uncertain) > 0 {
		draft.UnverifiedReason += "已定位含条件表达的原文，但尚未完成其语义核验。"
	}
	return []Draft{draft}
}

// Compare is the finding list for Evaluate.
func Compare(docs []Source) []Draft {
	return Findings(Evaluate(docs))
}

func covers(docs []Source, hits []hit) bool {
	seen := map[string]bool{}
	for _, item := range hits {
		seen[item.doc.ID] = true
	}
	if len(seen) < 2 {
		return false
	}
	for _, doc := range docs {
		if doc.ParseStatus == "ready" && !seen[doc.ID] {
			return false
		}
	}
	return true
}

func findWarranty(doc Source) ([]hit, []Anchor) {
	ir := doc.ir()
	text := ir.NormalizedText
	if text == "" {
		text = doc.Text
	}
	indexes := warrantyPattern.FindAllStringSubmatchIndex(text, -1)
	var out []hit
	var uncertain []Anchor
	seen := map[int]bool{}
	for _, loc := range indexes {
		quote := text[loc[0]:loc[1]]
		if strings.HasPrefix(quote, "质保金") || strings.HasPrefix(quote, "保修金") {
			continue
		}
		clauseStart, clause := warrantyClause(text, loc[0], loc[1])
		if qualifiedWarranty(clause) {
			if anchor, ok := bindSpan(doc, clause, clauseStart); ok && !seen[clauseStart] {
				anchor.Reason = "引用含范围、否定或例外表达，尚不能转换为固定质保月数。"
				uncertain = append(uncertain, anchor)
				seen[clauseStart] = true
			}
			continue
		}
		if item, ok := locatedWarranty(doc, text, loc); ok {
			out = append(out, item)
		}
	}
	return out, uncertain
}

func locatedWarranty(doc Source, text string, loc []int) (hit, bool) {
	value, err := strconv.Atoi(text[loc[2]:loc[3]])
	if err != nil {
		return hit{}, false
	}
	quote := text[loc[0]:loc[1]]
	bound, ok := bindSpan(doc, quote, loc[0])
	if !ok {
		return hit{}, false
	}
	return hit{doc: doc, quote: quote, start: bound.Start, end: bound.End, value: value,
		page: bound.Page, element: bound.ElementID}, true
}

// Inspect the surrounding sentence, including qualifiers before the keyword and
// exceptions after the numeric match. Do not infer interval overlap or legal
// precedence. Sentence-level over-abstention is intentional for this MVP.
func warrantyClause(text string, start, end int) (int, string) {
	if before := strings.LastIndexAny(text[:start], "。；;\n!?！？"); before >= 0 {
		_, width := utf8.DecodeRuneInString(text[before:])
		start = before + width
	} else {
		start = 0
	}
	if after := strings.IndexAny(text[end:], "。；;\n!?！？"); after >= 0 {
		end += after
	} else {
		end = len(text)
	}
	return start, text[start:end]
}

func qualifiedWarranty(clause string) bool {
	for _, qualifier := range []string{
		"至少", "不少于", "不低于", "不得少于", "不超过", "不得超过", "不多于", "至多",
		"最长", "最短", "最低", "最高", "以上", "以下", "以内", "大于", "小于", "超过",
		"不提供", "不享受", "不适用", "不承担", "不包含", "不含", "无需", "免于",
		"除外", "例外", "左右", "约为", "≥", "≤", "±",
	} {
		if strings.Contains(clause, qualifier) {
			return true
		}
	}
	return warrantyException.MatchString(clause)
}

func conflictClaim(hits []hit) string {
	parts := make([]string, 0, len(hits))
	for _, item := range hits {
		parts = append(parts, fmt.Sprintf("%s %d 个月", labels.Role(item.doc.Role), item.value))
	}
	return "质保月数不一致：" + strings.Join(parts, "，") + "。"
}

func insufficientClaim(hits []hit, failed, docs []Source) string {
	if len(readyIDs(docs)) < 2 && len(failed) == 0 {
		return "仅有一份独立材料写明质保月数，无法完成跨文件比对，这条比对是信息不足。"
	}
	if len(failed) > 0 {
		return fmt.Sprintf("已解析材料写明质保 %d 个月，但仍有材料未能解析，这条比对是信息不足。", hits[0].value)
	}
	named := make([]string, 0, len(hits))
	seen := map[string]bool{}
	for _, item := range hits {
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
	return fmt.Sprintf("只有%s写明了质保月数，还有 %d 份已解析材料没有写明，这条比对是信息不足。", strings.Join(named, "、"), missing)
}
