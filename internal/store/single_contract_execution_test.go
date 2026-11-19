// File metadata (manifest mirror):
// Purpose: Checks that the current single-contract capability stores exact payment observations while preserving the findings boundary.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-19
// Updated: 2026-11-19
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"testing"

	"contractaudit/internal/docir"
	"contractaudit/internal/engine"
)

func TestSingleContractV2StoresCoverageWithoutCreatingFindings(t *testing.T) {
	mapping, err := engine.PublishedSingleContractMapping(engine.SingleContractRuleSetHash())
	if err != nil {
		t.Fatal(err)
	}
	ir := docir.Build("😀双方按实际使用量结算。收到发票后十个工作日内支付。税率6%。")
	source := Interpreted{
		Document: Document{ID: "doc-current", Role: "draft_contract"},
		Ready:    true, Text: ir.NormalizedText, IR: ir,
		ParsedDocument: &ParsedDocument{ID: "parse-current", NormalizedDigest: ir.NormalizedDigest},
	}
	evaluation, findings, err := singleContractRuleOutput(mapping, source, "case", "run", "attempt")
	if err != nil || evaluation.ReasonCode != engine.PaymentTermsObserved ||
		evaluation.RuleVersion != engine.SingleContractPaymentTermsRule || evaluation.FindingID != "" ||
		evaluation.Relation != engine.RelInsufficient || len(findings) != 0 || len(evaluation.Evidence) < 2 {
		t.Fatalf("invalid coverage result: %+v; findings=%d; err=%v", evaluation, len(findings), err)
	}
	for _, item := range evaluation.Evidence {
		ref := item.Ref
		actual, ok := docir.Slice(ir.NormalizedText, ref.Start, ref.End)
		if !ok || actual != ref.Quote || ref.ParsedDocumentID != "parse-current" ||
			ref.NormalizedDigest != ir.NormalizedDigest || item.FactID == "" {
			t.Fatal("coverage citation did not preserve the selected parse identity")
		}
	}
	unrecognized := docir.Build("本合同税率6%。")
	source.Text, source.IR = unrecognized.NormalizedText, unrecognized
	evaluation, findings, err = singleContractRuleOutput(mapping, source, "case", "run", "attempt")
	if err != nil || evaluation.ReasonCode != engine.PaymentTermsUnrecognized || len(evaluation.Evidence) != 0 || len(findings) != 0 {
		t.Fatal("an unrecognized term must remain a coverage item")
	}
}
