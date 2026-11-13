// File metadata (manifest mirror):
// Purpose: Exercises the `store` package behavior through TestApprovalInboxCursorBindsTenantActorAndView, TestApprovalRoundCursorBindsContractAndActor, TestApprovalInboxViewAndLimitValidation, TestApprovalReadAllowedActionsFollowLiveAssignment, TestApprovalReadRiskAndNameStayExplicit; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-01
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"testing"

	"contractaudit/internal/accounts"
)

func approvalReadTestRecord(createdBy string) clmContractRecord {
	return clmContractRecord{
		TenantID: accounts.LocalDemoTenant, ContractID: "c6052c81-8b05-49ce-89e9-f6e9c6403c10",
		OwnerOrgID: "demo-org", AuthorityMode: "native", LifecycleState: "draft",
		ValidityState: "active", CurrentRevisionID: new("c6052c81-8b05-49ce-89e9-f6e9c6403c11"),
		RowVersion: 2, CreatedBy: createdBy,
	}
}

func TestApprovalReadAllowedActionsFollowLiveAssignment(t *testing.T) {
	admin, _ := accounts.ByID("demo-admin")
	reviewer, _ := accounts.ByID("demo-reviewer")
	record := approvalReadTestRecord(admin.ID)
	round := approvalRoundRead{
		State: "in_review", InitiatedBy: admin.ID, ContractRevisionID: *record.CurrentRevisionID,
	}
	firstNode := approvalReadTaskRow{
		TaskID: "c6052c81-8b05-49ce-89e9-f6e9c6403c12", NodeOrder: 1,
		RoleCode: "admin", State: "active", AssigneeSubjectID: admin.ID,
	}
	if actions := approvalReadAllowedActions(record, round, &firstNode, admin); len(actions) != 0 {
		t.Fatalf("initiator received independent-node actions: %v", actions)
	}
	finalNode := firstNode
	finalNode.NodeOrder = 7
	if actions := approvalReadAllowedActions(record, round, &finalNode, admin); len(actions) != 2 {
		t.Fatalf("initiator final-node actions = %v", actions)
	}
	reviewerRecord := approvalReadTestRecord(reviewer.ID)
	firstNode.AssigneeSubjectID = reviewer.ID
	firstNode.RoleCode = reviewer.Role
	if actions := approvalReadAllowedActions(reviewerRecord, round, &firstNode, reviewer); len(actions) != 2 {
		t.Fatalf("reviewer actions = %v", actions)
	}
	if actions := approvalReadAllowedActions(reviewerRecord, round, &firstNode, admin); len(actions) != 0 {
		t.Fatalf("non-assignee actions = %v", actions)
	}
	closed := round
	closed.State = "returned"
	if actions := approvalReadAllowedActions(reviewerRecord, closed, &firstNode, reviewer); len(actions) != 0 {
		t.Fatalf("closed round actions = %v", actions)
	}
	imported := reviewerRecord
	imported.AuthorityMode = "external_observed"
	if actions := approvalReadAllowedActions(imported, round, &firstNode, reviewer); len(actions) != 0 {
		t.Fatalf("imported contract actions = %v", actions)
	}
}
