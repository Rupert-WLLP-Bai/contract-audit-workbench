package engine

import (
	"errors"
	"fmt"
	"strings"

	"contractaudit/internal/docir"
)

// ErrRejected means the proposal is not a usable warranty finding.
var ErrRejected = errors.New("warranty proposal rejected")

// QuoteRef is one verbatim span from a model finding.
type QuoteRef struct {
	DocumentID string
	Quote      string
}

// Proposal is a model warranty finding before quotes are located.
// Relation and Claim do not replace Evaluation.
type Proposal struct {
	Claim       string
	Relation    string
	Disposition string
	Quotes      []QuoteRef
}

// LocateQuote finds quote in text and returns Unicode offsets.
// A quote that is not an exact substring is not given coordinates.
func LocateQuote(text, quote string) (start, end int, ok bool) {
	bound := docir.Locate(docir.Build(text), strings.TrimSpace(quote))
	if bound.Status != docir.StatusLocated {
		return 0, 0, false
	}
	return bound.Start, bound.End, true
}

// AcceptWarranty keeps the constraint evaluation when the model output is usable.
// A failed or unreadable model call never reaches this function. A usable call
// that disagrees does not relabel compatible, conflict, or insufficient.
func AcceptWarranty(docs []Source, proposal Proposal) (Evaluation, []Draft, error) {
	relation, ok := canonicalRelation(proposal.Relation)
	if !ok {
		return Evaluation{}, nil, fmt.Errorf("%w: relation", ErrRejected)
	}
	for _, quote := range proposal.Quotes {
		if strings.TrimSpace(quote.DocumentID) == "" || strings.TrimSpace(quote.Quote) == "" {
			return Evaluation{}, nil, fmt.Errorf("%w: empty quote", ErrRejected)
		}
	}
	eval := Evaluate(docs)
	if eval.Relation != RelConflict && eval.Relation != RelInsufficient {
		return eval, nil, nil
	}
	drafts := Findings(eval)
	if len(drafts) != 1 {
		return Evaluation{}, nil, fmt.Errorf("%w: evaluation", ErrRejected)
	}
	draft := drafts[0]
	if disposition, ok := canonicalDisposition(proposal.Disposition); ok && suggestionAgrees(eval.Relation, disposition) {
		draft.Disposition = disposition
	}
	var notes []string
	if draft.UnverifiedReason != "" {
		notes = append(notes, draft.UnverifiedReason)
	}
	if relation != eval.Relation {
		notes = append(notes, "模型写成"+relationWord(relation)+"，约束核对仍是"+relationWord(eval.Relation)+"。")
	}
	byID := map[string]Source{}
	for _, doc := range docs {
		byID[doc.ID] = doc
	}
	seen := map[string]bool{}
	for _, anchor := range draft.Anchors {
		if anchor.Status == "located" {
			seen[anchor.DocumentID+"\n"+anchor.Quote] = true
		}
	}
	missed := false
	for _, quote := range proposal.Quotes {
		text := strings.TrimSpace(quote.Quote)
		doc, found := byID[strings.TrimSpace(quote.DocumentID)]
		if !found {
			if !missed {
				notes = append(notes, "有模型引用无法定位，这些证据没有坐标。")
				missed = true
			}
			draft.Anchors = append(draft.Anchors, Anchor{
				DocumentID: strings.TrimSpace(quote.DocumentID), Quote: text, Status: "unlocatable",
				Reason: "引用没有对应到案件材料，不能编造坐标。",
			})
			continue
		}
		if seen[doc.ID+"\n"+text] {
			continue
		}
		if doc.ParseStatus != "ready" {
			if !hasAnchor(draft.Anchors, doc.ID) {
				draft.Anchors = append(draft.Anchors, missingDocument(doc))
			}
			continue
		}
		bound := docir.Locate(doc.ir(), text)
		if bound.Status == docir.StatusLocated {
			continue
		}
		if !missed {
			notes = append(notes, "有模型引用无法定位，这些证据没有坐标。")
			missed = true
		}
		draft.Anchors = append(draft.Anchors, unlocatableQuote(doc, text, bound.Reason))
	}
	draft.UnverifiedReason = strings.Join(notes, "")
	return eval, []Draft{draft}, nil
}

func unlocatableQuote(doc Source, quote, reason string) Anchor {
	if doc.FromOCR {
		reason = "转写正文里对不上这句引用，不能编造图片坐标。"
	}
	return Anchor{DocumentID: doc.ID, Quote: quote, Status: "unlocatable", Reason: reason}
}

func missingDocument(doc Source) Anchor {
	return Anchor{
		DocumentID: doc.ID, Status: "missing",
		Reason: "这份材料没有解析成正文，不能编造坐标，也不能把它当成无风险。",
	}
}

func hasAnchor(anchors []Anchor, id string) bool {
	for _, anchor := range anchors {
		if anchor.DocumentID == id {
			return true
		}
	}
	return false
}

func suggestionAgrees(relation, disposition string) bool {
	switch relation {
	case RelConflict:
		return disposition == "confirm_risk" || disposition == "insufficient_evidence"
	case RelInsufficient:
		return disposition == "insufficient_evidence" || disposition == "confirm_risk"
	default:
		return false
	}
}

func canonicalRelation(value string) (string, bool) {
	switch strings.TrimSpace(value) {
	case RelConflict, "冲突":
		return RelConflict, true
	case RelInsufficient, "信息不足":
		return RelInsufficient, true
	case RelCompatible, "一致":
		return RelCompatible, true
	default:
		return "", false
	}
}

func canonicalDisposition(value string) (string, bool) {
	switch strings.TrimSpace(value) {
	case "confirm_risk", "确认风险":
		return "confirm_risk", true
	case "false_positive", "误报":
		return "false_positive", true
	case "insufficient_evidence", "证据不足":
		return "insufficient_evidence", true
	case "accept_residual", "接受残余风险":
		return "accept_residual", true
	default:
		return "", false
	}
}

func relationWord(value string) string {
	switch value {
	case RelConflict:
		return "冲突"
	case RelInsufficient:
		return "信息不足"
	case RelCompatible:
		return "一致"
	case RelNotApplicable:
		return "不适用"
	default:
		return value
	}
}
