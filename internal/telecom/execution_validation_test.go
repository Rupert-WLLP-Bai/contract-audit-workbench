package telecom_test

import (
	"context"
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
	for _, citation := range sla.Citations {
		seen[citation.SourceID] = true
	}
	for _, id := range []string{"draft", "annex", "procurement"} {
		if !seen[id] {
			t.Errorf("SLA result lost %s citation: %+v", id, sla.Citations)
		}
	}
}
