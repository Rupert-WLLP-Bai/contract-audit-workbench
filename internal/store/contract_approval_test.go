// File metadata (manifest mirror):
// Purpose: Exercises the `store` package behavior through TestWorkflowTemplateRequiresAuditedSequentialSkeleton, TestApprovalRoutingFreezesEligibleAssigneesAndInitiatorBoundary, TestApprovalRoutingBlocksMissingRole, TestApprovalRoutingMatchesExactOwnerOrganization, TestRoutingSnapshotDigestIsStableAndBindingSensitive; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-01
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import "testing"

func validApprovalTemplate() PublishWorkflowTemplateInput {
	roles := []string{"role_business_owner", "role_domain", "role_control", "role_counsel", "role_legal", "role_leader", "role_final"}
	nodes := make([]WorkflowTemplateNodeInput, 0, len(roles))
	for i, code := range clmCanonicalApprovalNodeCodes {
		nodes = append(nodes, WorkflowTemplateNodeInput{
			NodeCode: code, Title: "节点" + code, RoleCode: roles[i], Final: i == len(roles)-1,
		})
	}
	return PublishWorkflowTemplateInput{
		TemplateID: "clm-standard", TemplateVersion: "1", Name: "七节点会签模板",
		StartAction: clmWorkflowStartAction, StartActionIsApprovalNode: false,
		Execution: clmWorkflowExecution, Nodes: nodes,
	}
}

func approvalTestRoleCodes(template PublishWorkflowTemplateInput) []string {
	out := make([]string, 0, len(template.Nodes))
	for _, node := range template.Nodes {
		out = append(out, node.RoleCode)
	}
	return out
}

func TestSubmitCommandAppearsOnlyAfterFreeze(t *testing.T) {
	record := clmContractRecord{AuthorityMode: "native", LifecycleState: "draft", ValidityState: "active"}
	if got := clmDraftCommands(record, true); len(got) != 2 || got[1] != clmCommandFreezeVersion {
		t.Fatalf("unfrozen draft commands %v", got)
	}
	revisionID := "33333333-3333-4333-8333-333333333333"
	record.CurrentRevisionID = &revisionID
	got := clmDraftCommands(record, true)
	if len(got) != 3 || got[2] != clmCommandSubmitApproval {
		t.Fatalf("frozen draft must offer submission: %v", got)
	}
}
