package engine_test

import (
	"strings"
	"testing"

	"contractaudit/internal/docir"
	"contractaudit/internal/engine"
)

func TestWarrantyConflict(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "采购文件要求质保36个月。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "拟签合同约定质保24个月。"},
	}
	got := engine.Compare(docs)
	if len(got) != 1 || got[0].Relation != "conflict" {
		t.Fatalf("%+v", got)
	}
	if len(got[0].Anchors) != 2 {
		t.Fatalf("anchors %+v", got[0].Anchors)
	}
	for _, anchor := range got[0].Anchors {
		if anchor.Status != "located" {
			t.Fatal(anchor.Status)
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
}

func TestEqualWarrantyIsNotAFinding(t *testing.T) {
	got := engine.Compare([]engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "质保36个月。"},
		{ID: "b", Role: "award", ParseStatus: "ready", Text: "质量保证期为36个月。"},
	})
	if len(got) != 0 {
		t.Fatalf("agreement should not become a risk: %+v", got)
	}
}

func TestSingleSourceIsInsufficient(t *testing.T) {
	got := engine.Compare([]engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "质保36个月。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "本合同未写质保期限。"},
	})
	if len(got) != 1 || got[0].Relation != "insufficient" {
		t.Fatalf("%+v", got)
	}
}

func TestFailedDocumentIsNotSilence(t *testing.T) {
	got := engine.Compare([]engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "质保36个月。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "质保36个月。"},
		{ID: "c", Role: "attachment", ParseStatus: "failed"},
	})
	if len(got) != 1 || got[0].Relation != "insufficient" {
		t.Fatalf("%+v", got)
	}
	var missing int
	for _, anchor := range got[0].Anchors {
		if anchor.Status == "missing" {
			missing++
			if anchor.Start != 0 || anchor.End != 0 || anchor.Quote != "" {
				t.Fatalf("missing anchor has coordinates: %+v", anchor)
			}
		}
	}
	if missing != 1 || got[0].UnverifiedReason == "" {
		t.Fatalf("%+v", got[0])
	}
}

func TestOCRQuoteWithoutMatchHasNoOffset(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", FromOCR: true, Text: "质保期为36个月。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "拟签合同约定质保24个月。"},
	}
	_, got, err := engine.AcceptWarranty(docs, engine.Proposal{
		Claim: "月数不一致", Relation: "conflict", Disposition: "confirm_risk",
		Quotes: []engine.QuoteRef{
			{DocumentID: "a", Quote: "质保99个月"},
			{DocumentID: "b", Quote: "拟签合同约定质保24个月。"},
		},
	})
	if err != nil || len(got) != 1 || got[0].Origin != "rule" || got[0].Relation != "conflict" {
		t.Fatalf("%v %+v", err, got)
	}
	var bad engine.Anchor
	for _, anchor := range got[0].Anchors {
		if anchor.Quote == "质保99个月" {
			bad = anchor
		}
	}
	if bad.Status != "unlocatable" || bad.Start != 0 || bad.End != 0 || bad.Page != nil || bad.Reason == "" {
		t.Fatalf("ocr quote invented a coordinate: %+v", bad)
	}
	var located int
	for _, anchor := range got[0].Anchors {
		if anchor.Status != "located" {
			continue
		}
		located++
		var text string
		for _, doc := range docs {
			if doc.ID == anchor.DocumentID {
				text = doc.Text
			}
		}
		quote, ok := docir.Slice(text, anchor.Start, anchor.End)
		if !ok || quote != anchor.Quote {
			t.Fatalf("located %+v %q", anchor, quote)
		}
	}
	if located != 2 {
		t.Fatalf("located %d %+v", located, got[0].Anchors)
	}
}

func TestModelAgreementCannotHideAConflict(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "采购文件要求质保36个月。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "拟签合同约定质保24个月。"},
	}
	eval, got, err := engine.AcceptWarranty(docs, engine.Proposal{
		Claim: "没有冲突", Relation: "compatible", Disposition: "false_positive",
	})
	if err != nil || eval.Relation != "conflict" || len(got) != 1 || got[0].Relation != "conflict" {
		t.Fatalf("%v %+v %+v", err, eval, got)
	}
	if strings.Contains(got[0].Claim, "没有冲突") || got[0].Disposition != "" {
		t.Fatalf("model wording became the finding: %+v", got[0])
	}
}

func TestModelConflictCannotInventAFinding(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "质保36个月。"},
		{ID: "b", Role: "award", ParseStatus: "ready", Text: "质量保证期为36个月。"},
	}
	eval, got, err := engine.AcceptWarranty(docs, engine.Proposal{
		Claim: "质保冲突", Relation: "conflict", Disposition: "confirm_risk",
		Quotes: []engine.QuoteRef{{DocumentID: "a", Quote: "质保99个月"}},
	})
	if err != nil || eval.Relation != "compatible" || len(got) != 0 {
		t.Fatalf("%v %+v %+v", err, eval, got)
	}
}

func TestModelAgreementCannotRenameInsufficient(t *testing.T) {
	docs := []engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "质保36个月。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "本合同未写质保期限。"},
	}
	eval, got, err := engine.AcceptWarranty(docs, engine.Proposal{
		Claim: "没有冲突", Relation: "compatible", Disposition: "false_positive",
	})
	if err != nil || eval.Relation != "insufficient" || len(got) != 1 || got[0].Relation != "insufficient" {
		t.Fatalf("%v %+v %+v", err, eval, got)
	}
	if strings.Contains(got[0].Claim, "没有冲突") || strings.Contains(got[0].Claim, "无冲突") {
		t.Fatalf("insufficient was called no conflict: %s", got[0].Claim)
	}
}

func TestDepositIsNotWarranty(t *testing.T) {
	got := engine.Compare([]engine.Source{
		{ID: "a", Role: "procurement", ParseStatus: "ready", Text: "质保金36个月后返还。"},
		{ID: "b", Role: "draft_contract", ParseStatus: "ready", Text: "质保24个月。"},
	})
	if len(got) != 1 || got[0].Relation != "insufficient" {
		t.Fatalf("deposit must not count as warranty: %+v", got)
	}
}
