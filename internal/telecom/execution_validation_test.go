package telecom_test

import (
	"context"
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
