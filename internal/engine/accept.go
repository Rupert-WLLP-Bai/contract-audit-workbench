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
	if err := validateProposalQuotes(proposal.Quotes); err != nil {
		return Evaluation{}, nil, err
	}
	eval := Evaluate(docs)
	if eval.Relation != RelConflict && eval.Relation != RelInsufficient {
		return eval, nil, nil
	}
	draft := Findings(eval)[0]
	draft.Disposition = warrantyDisposition(proposal.Disposition)
	var notes []string
	if draft.UnverifiedReason != "" {
		notes = append(notes, draft.UnverifiedReason)
	}
	if relation != eval.Relation {
		notes = append(notes, "模型写成"+relationWord(relation)+"，约束核对仍是"+relationWord(eval.Relation)+"。")
	}
	if appendProposalQuotes(&draft, docs, proposal.Quotes) {
		notes = append(notes, "有模型引用无法定位，这些证据没有坐标。")
	}
	draft.UnverifiedReason = strings.Join(notes, "")
	return eval, []Draft{draft}, nil
}

func validateProposalQuotes(quotes []QuoteRef) error {
	for _, quote := range quotes {
		if strings.TrimSpace(quote.DocumentID) == "" || strings.TrimSpace(quote.Quote) == "" {
			return fmt.Errorf("%w: empty quote", ErrRejected)
		}
	}
	return nil
}

func appendProposalQuotes(draft *Draft, docs []Source, quotes []QuoteRef) bool {
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
	for _, quote := range quotes {
		missed = appendProposalQuote(draft, byID, seen, quote) || missed
	}
	return missed
}

func appendProposalQuote(draft *Draft, docs map[string]Source, seen map[string]bool, quote QuoteRef) bool {
	text, id := strings.TrimSpace(quote.Quote), strings.TrimSpace(quote.DocumentID)
	doc, found := docs[id]
	if !found {
		draft.Anchors = append(draft.Anchors, Anchor{DocumentID: id, Quote: text, Status: "unlocatable",
			Reason: "引用没有对应到案件材料，不能编造坐标。"})
		return true
	}
	if seen[doc.ID+"\n"+text] {
		return false
	}
	if doc.ParseStatus != "ready" {
		// Evaluate already placed every unreadable source in the draft.
		return false
	}
	bound := docir.Locate(doc.ir(), text)
	if bound.Status == docir.StatusLocated {
		return false
	}
	draft.Anchors = append(draft.Anchors, unlocatableQuote(doc, text, bound.Reason))
	return true
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

func warrantyDisposition(value string) string {
	switch strings.TrimSpace(value) {
	case "confirm_risk", "确认风险":
		return "confirm_risk"
	case "insufficient_evidence", "证据不足":
		return "insufficient_evidence"
	default:
		return ""
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
