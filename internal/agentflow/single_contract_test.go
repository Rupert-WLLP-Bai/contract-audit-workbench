// File metadata (manifest mirror):
// Purpose: Exercises the single-contract planner tool boundary: only the published payment-sum capability is exposed, and its execution requires one draft contract.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package agentflow

import (
	"context"
	"errors"
	"testing"

	"contractaudit/internal/engine"
)

func singleContractInput() ToolInput {
	return ToolInput{
		TenantID: "tenant-a", CaseID: "case-a", ActorID: "alice",
		Gap:      Gap{ID: "gap-1", Domain: Commercial, Capability: SingleContractPaymentCapability},
		Evidence: []Evidence{{ID: "draft", Version: "1", Role: "draft_contract", Text: "合同约定预付款为合同总额的 30%。"}},
	}
}

func TestSingleContractToolsExposeOnlyThePublishedCapability(t *testing.T) {
	tools := SingleContractTools()
	if len(tools) != 1 {
		t.Fatalf("single-contract scope exposes %d tools", len(tools))
	}
	spec := tools[0].Spec()
	if spec.ID != SingleContractPaymentCapability || spec.Domain != Commercial || spec.Version != "1" || spec.Effect != "read" ||
		len(spec.Capabilities) != 1 || spec.Capabilities[0] != SingleContractPaymentCapability {
		t.Fatalf("single-contract tool spec is %+v", spec)
	}
}

func TestSingleContractToolRejectsInvalidGapAndEvidence(t *testing.T) {
	tool := SingleContractTools()[0]
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Execute(cancelled, singleContractInput()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled execution returned %v", err)
	}
	wrongCapability := singleContractInput()
	wrongCapability.Gap.Capability = "single_contract.other"
	if _, err := tool.Execute(context.Background(), wrongCapability); !errors.Is(err, ErrInput) {
		t.Fatalf("wrong capability returned %v", err)
	}
	wrongDomain := singleContractInput()
	wrongDomain.Gap.Domain = Technical
	if _, err := tool.Execute(context.Background(), wrongDomain); !errors.Is(err, ErrInput) {
		t.Fatalf("wrong domain returned %v", err)
	}
	duplicate := singleContractInput()
	duplicate.Evidence = append(duplicate.Evidence, Evidence{ID: "draft-2", Role: "draft_contract", Text: "另一份合同。"})
	if _, err := tool.Execute(context.Background(), duplicate); !errors.Is(err, ErrInput) {
		t.Fatalf("duplicate draft contract returned %v", err)
	}
}

func TestSingleContractToolReportsMissingPrimaryAndEvaluation(t *testing.T) {
	tool := SingleContractTools()[0]
	missing := singleContractInput()
	missing.Evidence = []Evidence{{ID: "procurement", Role: "procurement", Text: "采购依据。"}}
	findings, err := tool.Execute(context.Background(), missing)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Relation != engine.RelInsufficient || findings[0].ReasonCode != "baseline_evidence_insufficient" ||
		findings[0].Capability != SingleContractPaymentCapability || findings[0].Domain != Commercial || findings[0].RuleID != engine.SingleContractPrecheckRule ||
		findings[0].Citations == nil {
		t.Fatalf("missing primary produced %+v", findings)
	}
	findings, err = tool.Execute(context.Background(), singleContractInput())
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Relation == "" || findings[0].Summary == "" || findings[0].Citations == nil {
		t.Fatalf("evaluated draft produced %+v", findings)
	}
	for _, citation := range findings[0].Citations {
		if citation.EvidenceID != "draft" || citation.Version != "1" {
			t.Fatalf("citation lost its evidence identity: %+v", citation)
		}
	}
}
