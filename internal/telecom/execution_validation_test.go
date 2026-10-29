package telecom_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"contractaudit/internal/rulepack"
	"contractaudit/internal/telecom"
)

func TestMissingSLAAttachmentWithContractFactsIsInsufficient(t *testing.T) {
	req := request("100 Mbit/s", "99.9%", "30%")
	sources := req.Sources[:0]
	for _, source := range req.Sources {
		if source.Role != "attachment" {
			sources = append(sources, source)
		}
	}
	req.Sources = sources
	report, err := telecom.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := result(t, report, "ENT-TECH-SLA-001")
	if got.Relation != rulepack.RelationInsufficient || got.ReasonCode != "sla_annex_missing" {
		t.Fatalf("missing annex result = %s (%s)", got.Relation, got.ReasonCode)
	}
}

func TestExecutePreservesSLAFailurePriorityAndAllCitations(t *testing.T) {
	req := request("100 Mbit/s", "99.5%", "30%")
	for index, src := range req.Sources {
		if src.ID != "annex" {
			continue
		}
		text := strings.Replace(src.Text, "99.5%", "99.9%", 1)
		req.Sources[index] = source(src.ID, src.Role, strings.Replace(text, "无排除项", "排除项：planned_maintenance", 1))
	}

	report, err := telecom.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	sla := result(t, report, "ENT-TECH-SLA-001")
	if sla.Relation != rulepack.RelationInsufficient || sla.ReasonCode != "sla_annex_basis_incomparable" {
		t.Fatalf("simultaneous SLA failures = %s (%s), want basis incomparability to take precedence", sla.Relation, sla.ReasonCode)
	}
	seen := map[string]bool{}
	var order []string
	for _, citation := range sla.Citations {
		seen[citation.SourceID] = true
		order = append(order, citation.SourceID)
	}
	for _, id := range []string{"draft", "annex", "procurement"} {
		if !seen[id] {
			t.Errorf("SLA result lost %s citation: %+v", id, sla.Citations)
		}
	}
	if want := []string{"draft", "annex", "procurement"}; !slices.Equal(order, want) {
		t.Fatalf("SLA citation order = %v, want fact, annex, then baseline authority %v", order, want)
	}
}

func TestExecutePreservesAmountConversionAndFailurePriority(t *testing.T) {
	t.Run("万元 converts to yuan consistently", func(t *testing.T) {
		req := request("100 Mbit/s", "99.9%", "20%")
		replaceSource(&req, "procurement", "合同总额 100000 元，含税", "合同总额 10 万元，含税")
		replaceSource(&req, "draft", "合同总额 100000 元，含税", "合同总额 10 万元，含税")
		report, err := telecom.Execute(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if got := result(t, report, "ENT-COM-ADV-001"); got.Relation != rulepack.RelationCompatible {
			t.Fatalf("equivalent yuan and wan-yuan amounts = %s (%s), want compatible", got.Relation, got.ReasonCode)
		}
	})

	t.Run("amount mismatch reason follows object mismatch", func(t *testing.T) {
		req := request("100 Mbit/s", "99.9%", "20%")
		replaceSource(&req, "draft", "LINE-001", "LINE-002")
		replaceSource(&req, "draft", "合同总额 100000 元", "合同总额 90000 元")
		report, err := telecom.Execute(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		got := result(t, report, "ENT-COM-ADV-001")
		if got.Relation != rulepack.RelationInsufficient || got.ReasonCode != "amount_or_tax_basis_incomparable" {
			t.Fatalf("simultaneous object and amount mismatch = %s (%s), want final amount/tax reason", got.Relation, got.ReasonCode)
		}
	})

	t.Run("conflicting authority tax bases are ambiguous", func(t *testing.T) {
		req := request("100 Mbit/s", "99.9%", "20%")
		req.Sources = append(req.Sources, source("other-approval", "award", "电路 LINE-001：审批依据，预付款上限 30%，预付款以合同总额为基础，不含税。"))
		report, err := telecom.Execute(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		got := result(t, report, "ENT-COM-ADV-001")
		if got.Relation != rulepack.RelationInsufficient || got.ReasonCode != "contractual_authority_ambiguous" {
			t.Fatalf("conflicting authority tax basis = %s (%s), want ambiguous", got.Relation, got.ReasonCode)
		}
	})
}
