// File metadata (manifest mirror):
// Purpose: Checks exact frozen excerpts, explicit installment percentages, separate payment conditions and the historical single-contract rule mapping.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-19
// Updated: 2026-11-19
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package engine

import (
	"strings"
	"testing"

	"contractaudit/internal/docir"
)

func TestSingleContractPaymentTermsWithExactFrozenText(t *testing.T) {
	text := "😀项目说明。\n\n双方按实际调用数量及约定单价计费，并据实结算。每月对账。确认账单并收到发票后，甲方在十个工作日内以银行转账支付。\n\n税率6%；逾期付款的违约金为0.5%。"
	ir := docir.Build(text)
	observation := ObserveSingleContractPaymentTerms(Source{ID: "primary", ParseStatus: "ready", IR: ir})
	if observation.ReasonCode != PaymentTermsObserved || !strings.Contains(observation.Reason, "本规则未定位明示分期付款比例") ||
		!strings.Contains(observation.Reason, "付款期限") || !strings.Contains(observation.Reason, "计价与结算口径") {
		t.Fatalf("unexpected result: %+v", observation)
	}
	if len(observation.Anchors) != 3 {
		t.Fatalf("expected distinct evidence for calculation, reconciliation and payment: %d", len(observation.Anchors))
	}
	for _, anchor := range observation.Anchors {
		actual, ok := docir.Slice(ir.NormalizedText, anchor.Start, anchor.End)
		if !ok || actual != anchor.Quote || anchor.Status != "located" || anchor.DocumentID != "primary" {
			t.Fatalf("invalid citation: %+v", anchor)
		}
	}
}

func TestSingleContractPaymentTermsKeepOnlyExplicitInstallmentRatios(t *testing.T) {
	for _, test := range []struct {
		name  string
		text  string
		code  string
		ratio bool
	}{
		{"tax only", "本合同费用增值税税率为13%，以账单为准。", PaymentTermsUnrecognized, false},
		{"payment adjacent to penalty", "逾期付款按未付价款的0.5%支付违约金。", PaymentTermsUnrecognized, false},
		{"explicit stage", "尾款比例为20%。", PaymentTermsObserved, true},
		{"explicit amount basis", "甲方支付合同总价款的３０％。", PaymentTermsObserved, true},
		{"explicit stage beside tax", "税率6%，尾款比例为20%。", PaymentTermsObserved, true},
		{"template", "以下示例：预付款比例为40%。", PaymentTermsUnrecognized, false},
		{"empty", "", PaymentTermsNoText, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation := ObserveSingleContractPaymentTerms(Source{ID: "doc", ParseStatus: "ready", Text: test.text})
			if observation.ReasonCode != test.code || strings.Contains(observation.Reason, "原文中的明示分期比例") != test.ratio {
				t.Fatalf("unexpected result %+v", observation)
			}
		})
	}
}

func TestSingleContractPaymentTermsPreserveDistinctInstallments(t *testing.T) {
	text := "😀首期款比例为20%。验收通过后支付70%。质保期满后10%。尾款比例为20%。首期款比例为20%。逾期付款按未付价款的2%支付违约金。"
	observation := ObserveSingleContractPaymentTerms(Source{ID: "primary", ParseStatus: "ready", Text: text})
	var ratios []string
	for _, reference := range observation.Anchors {
		if len(reference.TermKinds) == 1 && reference.TermKinds[0] == paymentRatioTerm {
			ratios = append(ratios, reference.Quote)
		}
	}
	want := "首期款比例为20%|验收通过后支付70%|质保期满后10%|尾款比例为20%"
	if strings.Join(ratios, "|") != want || strings.Contains(observation.Reason, "合计") {
		t.Fatalf("incorrect installment observations: %+v", observation)
	}
}

func TestSingleContractMappingsRetainTheHistoricalPair(t *testing.T) {
	legacy, err := PublishedSingleContractMapping(singleContractV1RuleHash)
	if err != nil || legacy.Version != "1" || legacy.Rules[0].ID != SingleContractPrecheckRule ||
		!SingleContractPlaybookMatches(singleContractV1RuleHash, singleContractV1PlayHash) {
		t.Fatal("version 1 can no longer be resolved")
	}
	current, err := PublishedSingleContractMapping(SingleContractRuleSetHash())
	if err != nil || current.Version != "2" || current.Rules[0].ID != SingleContractPaymentTermsRule ||
		!SingleContractPlaybookMatches(SingleContractRuleSetHash(), SingleContractPlaybookHash()) ||
		SingleContractPlaybookMatches(singleContractV1RuleHash, SingleContractPlaybookHash()) {
		t.Fatal("version 2 selection or mixed-pair rejection failed")
	}
}
