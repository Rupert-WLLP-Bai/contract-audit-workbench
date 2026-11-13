// File metadata (manifest mirror):
// Purpose: CLM-07 fixes the countersign skeleton: submission is a start action, not an approval node, and the seven nodes execute sequentially. Templates are versioned, never mutated in… Related `store` operations include normalizeWorkflowTemplateInput, validateWorkflowTemplate, approvalPrincipal, approvalBindingMatchesScope.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-01
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"time"

	"contractaudit/internal/accounts"
	approvalmodel "contractaudit/internal/approval/model"
)

// CLM-07 fixes the countersign skeleton: submission is a start action, not an
// approval node, and the seven nodes execute sequentially. Templates are
// versioned, never mutated in place, and every submitted round freezes the
// template version plus the resolved routing snapshot it actually used.
const (
	clmRouteSubmitApproval    = "clm.submit_approval"
	clmEventApprovalSubmitted = "contract.approval_submitted.v1"
	clmCommandSubmitApproval  = "submit_for_approval"

	clmWorkflowStartAction = "submit_contract"
	clmWorkflowExecution   = "sequential"
	clmApprovalNodeCount   = 7

	clmRoutingSnapshotSchema         = "clm-routing-snapshot/v1"
	clmRiskDispositionSnapshotSchema = "clm-risk-disposition-snapshot/v1"
)

// clmCanonicalApprovalNodeCodes is the audited seven-node order. Template
// versions may change titles and role codes, but not reorder, remove or add
// approval nodes: a changed skeleton would be a different workflow contract.
var clmCanonicalApprovalNodeCodes = approvalmodel.CanonicalApprovalNodeCodes()

type PublishWorkflowTemplateInput = approvalmodel.PublishWorkflowTemplateInput
type WorkflowTemplateNodeInput = approvalmodel.WorkflowTemplateNodeInput
type WorkflowTemplateNode = approvalmodel.WorkflowTemplateNode
type WorkflowTemplateSummary = approvalmodel.WorkflowTemplateSummary
type WorkflowRoleBindingInput = approvalmodel.WorkflowRoleBindingInput
type ReplaceWorkflowRoleInput = approvalmodel.ReplaceWorkflowRoleInput
type WorkflowRoleBindingSummary = approvalmodel.WorkflowRoleBindingSummary
type WorkflowRoleSummary = approvalmodel.WorkflowRoleSummary
type SubmitContractApprovalInput = approvalmodel.SubmitContractApprovalInput
type ApprovalTaskSummary = approvalmodel.ApprovalTaskSummary
type ApprovalRoundSummary = approvalmodel.ApprovalRoundSummary
type SubmitContractApprovalResult = approvalmodel.SubmitContractApprovalResult
type clmWorkflowNodeSnapshot = approvalmodel.WorkflowNodeSnapshot
type clmRoutingAssignee = approvalmodel.RoutingAssignee
type clmRoutingSnapshot = approvalmodel.RoutingSnapshot

// clmRiskDispositionSnapshot records that CLM-07 did not evaluate risk. It is
// deliberately not a credibility claim; a later risk step may append its own
// disposition evidence.
type clmRiskDispositionSnapshot struct {
	Schema string `json:"schema"`
	Status string `json:"status"`
	Items  []any  `json:"items"`
}

// clmRoleBindingRow is one persisted role binding used by routing resolution.
type clmRoleBindingRow = approvalmodel.RoleBindingRow

func normalizeWorkflowTemplateInput(input PublishWorkflowTemplateInput) PublishWorkflowTemplateInput {
	return approvalmodel.NormalizeWorkflowTemplateInput(input)
}
func validateWorkflowTemplate(input PublishWorkflowTemplateInput) error {
	return approvalmodel.ValidateWorkflowTemplate(input)
}

func resolveApprovalRouting(record clmContractRecord, initiatorID, templateID, templateVersion string, nodes []WorkflowTemplateNodeInput, bindings []clmRoleBindingRow, users []accounts.User, now time.Time) (clmRoutingSnapshot, error) {
	return approvalmodel.ResolveApprovalRouting(record, initiatorID, templateID, templateVersion, nodes, bindings, users, now)
}
func clmNodeAssignees(node clmWorkflowNodeSnapshot) []string {
	return approvalmodel.NodeAssignees(node)
}
