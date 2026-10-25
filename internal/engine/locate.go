package engine

import (
	"unicode/utf8"

	"contractaudit/internal/docir"
)

func bindSpan(doc Source, quote string, byteStart int) (Anchor, bool) {
	ir := doc.ir()
	text := ir.NormalizedText
	if text == "" {
		text = doc.Text
	}
	if byteStart < 0 || byteStart > len(text) {
		return Anchor{}, false
	}
	start := utf8.RuneCountInString(text[:byteStart])
	end := start + utf8.RuneCountInString(quote)
	bound := docir.Bind(ir, quote, start, end)
	if bound.Status != docir.StatusLocated {
		return Anchor{}, false
	}
	return Anchor{
		DocumentID: doc.ID, Quote: quote, Status: "located",
		Start: bound.Start, End: bound.End, Page: bound.Page, ElementID: bound.ElementID,
	}, true
}

func readyIDs(docs []Source) map[string]bool {
	out := map[string]bool{}
	for _, doc := range docs {
		if doc.ParseStatus == "ready" {
			out[doc.ID] = true
		}
	}
	return out
}

func failedDocs(docs []Source) []Source {
	var out []Source
	for _, doc := range docs {
		if doc.ParseStatus != "ready" {
			out = append(out, doc)
		}
	}
	return out
}

func missingAnchors(docs []Source) []Anchor {
	var out []Anchor
	for _, doc := range failedDocs(docs) {
		out = append(out, Anchor{
			DocumentID: doc.ID, Status: "missing",
			Reason: "这份材料没有解析成正文，不能编造坐标，也不能把它当成无风险。",
		})
	}
	return out
}

func covered(docs []Source, seen map[string]bool) bool {
	if len(readyIDs(docs)) < 2 {
		return false
	}
	for id, ready := range readyIDs(docs) {
		if ready && !seen[id] {
			return false
		}
	}
	return len(failedDocs(docs)) == 0
}

// absence distinguishes an unobserved rule from a comparison with no readable
// source. not_applicable means this bounded parser found no supported clause;
// it is never a legal compliance verdict.
func absence(ev Evaluation, docs []Source, kind string) Evaluation {
	if len(readyIDs(docs)) == 0 {
		ev.Relation = RelInsufficient
		ev.Reason = "没有可读取的材料，不能判断" + kind + "，这条比对是信息不足。"
		return ev
	}
	ev.Relation = RelNotApplicable
	ev.Reason = "已解析材料没有写明" + kind + "，本条规则不适用。"
	ev.Facts = nil
	ev.Missing = nil
	return ev
}
