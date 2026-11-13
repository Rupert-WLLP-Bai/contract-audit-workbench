package model

import (
	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
	"sort"
	"strings"
	"time"
)

const (
	clmRoutingSnapshotSchema = "clm-routing-snapshot/v1"
)

type RoleBindingRow struct {
	RoleCode string
	OrgScope string
	UserID   string
}

// approvalPrincipal mirrors the local-demo case binding for contract owners
// without consulting the directory again. Enterprise grants pass through
// unchanged, so this never widens a real grant.
func approvalPrincipal(record contract.Aggregate, user accounts.User) access.Principal {
	principal := accounts.Principal(user)
	if user.Issuer != "" || user.TenantID != record.TenantID ||
		(user.TenantID != accounts.LocalDemoTenant && user.TenantID != accounts.LocalDemoTenant2) {
		return principal
	}
	if record.CreatedBy != user.ID {
		return principal
	}
	for i := range principal.Grants {
		if principal.Grants[i].Scope == "case" && principal.Grants[i].CaseID == "" {
			principal.Grants[i].CaseID = record.ContractID
		}
	}
	return principal
}

func approvalBindingMatchesScope(orgScope, ownerOrgID string) bool {
	return orgScope == "" || orgScope == ownerOrgID
}

// ResolveApprovalRouting is pure: it takes the directory users and binding rows
// observed inside the submission transaction and returns the frozen snapshot.
// It refuses to invent an approver when a role has no current, in-scope user,
// and excludes the initiator from every independent node (the final check may
// be performed by the initiator).
func ResolveApprovalRouting(record contract.Aggregate, initiatorID, templateID, templateVersion string,
	nodes []WorkflowTemplateNodeInput, bindings []RoleBindingRow, users []accounts.User, now time.Time) (RoutingSnapshot, error) {
	snapshot := RoutingSnapshot{
		Schema: clmRoutingSnapshotSchema, TemplateID: templateID, TemplateVersion: templateVersion,
		Execution: clmWorkflowExecution, ContractOrgID: record.OwnerOrgID, InitiatorID: initiatorID,
		Nodes: make([]WorkflowNodeSnapshot, 0, len(nodes)),
	}
	usersByID := make(map[string]accounts.User, len(users))
	for _, user := range users {
		if user.TenantID == record.TenantID && user.Enabled && user.ID != "" {
			usersByID[user.ID] = user
		}
	}
	missing := []string{}
	for i, node := range nodes {
		selected := map[string]RoutingAssignee{}
		for _, binding := range bindings {
			if binding.RoleCode != node.RoleCode || !approvalBindingMatchesScope(binding.OrgScope, record.OwnerOrgID) {
				continue
			}
			user, ok := usersByID[binding.UserID]
			if !ok {
				continue
			}
			if !node.Final && user.ID == initiatorID {
				continue
			}
			resource := access.Resource{TenantID: record.TenantID, CaseID: record.ContractID,
				OrgID: record.OwnerOrgID, OwnerID: record.CreatedBy}
			if !access.Check(access.Decide, approvalPrincipal(record, user), resource, now).Allowed {
				continue
			}
			displayName := strings.TrimSpace(user.DisplayName)
			if displayName == "" {
				displayName = user.ID
			}
			current, exists := selected[user.ID]
			// Prefer the exact-organization binding over a tenant-wide one for
			// an understandable routing record; both grant the same action.
			if exists && current.BindingOrgScope != "" {
				continue
			}
			selected[user.ID] = RoutingAssignee{UserID: user.ID, DisplayName: displayName, BindingOrgScope: binding.OrgScope}
		}
		assignees := make([]RoutingAssignee, 0, len(selected))
		for _, assignee := range selected {
			assignees = append(assignees, assignee)
		}
		sort.Slice(assignees, func(left, right int) bool { return assignees[left].UserID < assignees[right].UserID })
		if len(assignees) == 0 {
			missing = append(missing, node.RoleCode)
		}
		snapshot.Nodes = append(snapshot.Nodes, WorkflowNodeSnapshot{
			NodeCode: node.NodeCode, NodeOrder: i + 1, Title: node.Title,
			RoleCode: node.RoleCode, Final: node.Final, Assignees: assignees,
		})
	}
	if len(missing) > 0 {
		return RoutingSnapshot{}, fault.Conflict("以下会签角色没有可用的本租户审批人：" + strings.Join(missing, "、"))
	}
	return snapshot, nil
}

// NodeAssignees flattens one node's frozen assignee list.
func NodeAssignees(node WorkflowNodeSnapshot) []string {
	out := make([]string, 0, len(node.Assignees))
	for _, assignee := range node.Assignees {
		out = append(out, assignee.UserID)
	}
	return out
}
