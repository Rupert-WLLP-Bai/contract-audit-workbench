package store

import (
	"strings"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
)

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

func TestWorkflowTemplateRequiresAuditedSequentialSkeleton(t *testing.T) {
	base := normalizeWorkflowTemplateInput(validApprovalTemplate())
	if err := validateWorkflowTemplate(base); err != nil {
		t.Fatalf("valid template rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*PublishWorkflowTemplateInput)
	}{
		{"submit counted as node", func(in *PublishWorkflowTemplateInput) { in.StartActionIsApprovalNode = true }},
		{"parallel execution", func(in *PublishWorkflowTemplateInput) { in.Execution = "parallel" }},
		{"submit action none", func(in *PublishWorkflowTemplateInput) { in.StartAction = "none" }},
		{"six nodes", func(in *PublishWorkflowTemplateInput) { in.Nodes = in.Nodes[:6] }},
		{"reordered nodes", func(in *PublishWorkflowTemplateInput) {
			in.Nodes[0], in.Nodes[1] = in.Nodes[1], in.Nodes[0]
		}},
		{"duplicate role", func(in *PublishWorkflowTemplateInput) { in.Nodes[1].RoleCode = in.Nodes[0].RoleCode }},
		{"early final", func(in *PublishWorkflowTemplateInput) { in.Nodes[2].Final = true }},
		{"final only at end", func(in *PublishWorkflowTemplateInput) { in.Nodes[6].Final = false }},
		{"bad role", func(in *PublishWorkflowTemplateInput) { in.Nodes[0].RoleCode = "Bad Role" }},
		{"empty title", func(in *PublishWorkflowTemplateInput) { in.Nodes[0].Title = "" }},
	}
	for _, item := range cases {
		input := normalizeWorkflowTemplateInput(validApprovalTemplate())
		item.mutate(&input)
		if err := validateWorkflowTemplate(input); err == nil {
			t.Fatalf("%s: template must be rejected", item.name)
		}
	}
}

func approvalTestRecord(ownerOrg string) clmContractRecord {
	return clmContractRecord{
		TenantID: "tenant-a", ContractID: "11111111-1111-4111-8111-111111111111",
		OwnerOrgID: ownerOrg, AuthorityMode: "native", LifecycleState: "draft",
		ValidityState: "active", CreatedBy: "owner",
	}
}

func approvalTestUsers() []accounts.User {
	return []accounts.User{
		{ID: "owner", TenantID: "tenant-a", OrgID: "org-a", Enabled: true, Grants: []access.Grant{
			{Role: "case_owner", Scope: "case", CaseID: "11111111-1111-4111-8111-111111111111"},
		}},
		{ID: "reviewer-a", DisplayName: "审查员甲", TenantID: "tenant-a", OrgID: "org-a", Enabled: true, Grants: []access.Grant{
			{Role: "reviewer", Scope: "tenant"},
		}},
		{ID: "reviewer-b", DisplayName: "审查员乙", TenantID: "tenant-a", OrgID: "org-b", Enabled: true, Grants: []access.Grant{
			{Role: "reviewer", Scope: "case", CaseID: "11111111-1111-4111-8111-111111111111"},
		}},
		{ID: "viewer", TenantID: "tenant-a", OrgID: "org-a", Enabled: true, Grants: []access.Grant{
			{Role: "viewer", Scope: "tenant"},
		}},
		{ID: "disabled", TenantID: "tenant-a", OrgID: "org-a", Enabled: false, Grants: []access.Grant{
			{Role: "reviewer", Scope: "tenant"},
		}},
		{ID: "foreign", TenantID: "tenant-b", OrgID: "org-a", Enabled: true, Grants: []access.Grant{
			{Role: "reviewer", Scope: "tenant"},
		}},
	}
}

func approvalTestBindings(roleCodes []string) []clmRoleBindingRow {
	bindings := []clmRoleBindingRow{}
	for _, role := range roleCodes[:6] {
		bindings = append(bindings,
			clmRoleBindingRow{RoleCode: role, OrgScope: "", UserID: "reviewer-a"},
			clmRoleBindingRow{RoleCode: role, OrgScope: "org-a", UserID: "reviewer-b"},
			clmRoleBindingRow{RoleCode: role, OrgScope: "", UserID: "viewer"},
			clmRoleBindingRow{RoleCode: role, OrgScope: "", UserID: "disabled"},
			clmRoleBindingRow{RoleCode: role, OrgScope: "", UserID: "foreign"},
			clmRoleBindingRow{RoleCode: role, OrgScope: "", UserID: "owner"},
		)
	}
	bindings = append(bindings,
		clmRoleBindingRow{RoleCode: roleCodes[6], OrgScope: "", UserID: "reviewer-a"},
		clmRoleBindingRow{RoleCode: roleCodes[6], OrgScope: "org-a", UserID: "reviewer-b"},
		clmRoleBindingRow{RoleCode: roleCodes[6], OrgScope: "", UserID: "owner"},
	)
	return bindings
}

func approvalTestRoleCodes(template PublishWorkflowTemplateInput) []string {
	out := make([]string, 0, len(template.Nodes))
	for _, node := range template.Nodes {
		out = append(out, node.RoleCode)
	}
	return out
}

func TestApprovalRoutingFreezesEligibleAssigneesAndInitiatorBoundary(t *testing.T) {
	template := normalizeWorkflowTemplateInput(validApprovalTemplate())
	record := approvalTestRecord("org-a")
	roles := approvalTestRoleCodes(template)
	snapshot, err := resolveApprovalRouting(record, "owner", template.TemplateID, template.TemplateVersion,
		template.Nodes, approvalTestBindings(roles), approvalTestUsers(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Schema != clmRoutingSnapshotSchema || snapshot.TemplateID != template.TemplateID {
		t.Fatalf("snapshot header %+v", snapshot)
	}
	if len(snapshot.Nodes) != clmApprovalNodeCount {
		t.Fatalf("node count %d", len(snapshot.Nodes))
	}
	for i, node := range snapshot.Nodes {
		got := clmNodeAssignees(node)
		if node.Final {
			if len(got) != 3 || got[0] != "owner" || got[1] != "reviewer-a" || got[2] != "reviewer-b" {
				t.Fatalf("final node assignees %v", got)
			}
			continue
		}
		if len(got) != 2 || got[0] != "reviewer-a" || got[1] != "reviewer-b" {
			t.Fatalf("node %d assignees %v: only enabled in-tenant org/case reviewers may be routed", i, got)
		}
		for _, userID := range got {
			if userID == "owner" {
				t.Fatalf("initiator must not be routed to independent node %s", node.NodeCode)
			}
		}
	}

}

func TestApprovalRoutingBlocksMissingRole(t *testing.T) {
	template := normalizeWorkflowTemplateInput(validApprovalTemplate())
	record := approvalTestRecord("org-a")
	roles := approvalTestRoleCodes(template)
	bindings := approvalTestBindings(roles)
	missingRole := template.Nodes[2].RoleCode
	kept := bindings[:0]
	for _, binding := range bindings {
		if binding.RoleCode != missingRole {
			kept = append(kept, binding)
		}
	}
	_, err := resolveApprovalRouting(record, "owner", template.TemplateID, template.TemplateVersion,
		template.Nodes, kept, approvalTestUsers(), time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), missingRole) {
		t.Fatalf("missing role must block submission with the role name, got %v", err)
	}
}

func TestApprovalRoutingMatchesExactOwnerOrganization(t *testing.T) {
	template := normalizeWorkflowTemplateInput(validApprovalTemplate())
	roles := approvalTestRoleCodes(template)
	bindings := approvalTestBindings(roles)
	users := approvalTestUsers()
	// reviewer-b is only bound for org-a and only holds a case grant for the
	// org-a contract; the org-b contract must not route to it.
	other := approvalTestRecord("org-b")
	other.ContractID = "22222222-2222-4222-8222-222222222222"
	for i := range users {
		if users[i].ID == "reviewer-b" || users[i].ID == "owner" {
			users[i].Grants[0].CaseID = other.ContractID
		}
	}
	snapshot, err := resolveApprovalRouting(other, "owner", template.TemplateID, template.TemplateVersion,
		template.Nodes, bindings, users, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range snapshot.Nodes {
		for _, userID := range clmNodeAssignees(node) {
			if userID == "reviewer-b" {
				t.Fatalf("org-scoped binding leaked into node %s for another owner organization", node.NodeCode)
			}
		}
	}
}

func TestRoutingSnapshotDigestIsStableAndBindingSensitive(t *testing.T) {
	template := normalizeWorkflowTemplateInput(validApprovalTemplate())
	roles := approvalTestRoleCodes(template)
	record := approvalTestRecord("org-a")
	first, err := resolveApprovalRouting(record, "owner", template.TemplateID, template.TemplateVersion,
		template.Nodes, approvalTestBindings(roles), approvalTestUsers(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolveApprovalRouting(record, "owner", template.TemplateID, template.TemplateVersion,
		template.Nodes, approvalTestBindings(roles), approvalTestUsers(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, err := canonicalHash(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := canonicalHash(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatal("identical routing facts must produce the same digest")
	}
	changed := approvalTestBindings(roles)
	kept := changed[:0]
	for _, binding := range changed {
		if binding.UserID != "reviewer-b" {
			kept = append(kept, binding)
		}
	}
	changed = kept
	third, err := resolveApprovalRouting(record, "owner", template.TemplateID, template.TemplateVersion,
		template.Nodes, changed, approvalTestUsers(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	thirdDigest, err := canonicalHash(third)
	if err != nil {
		t.Fatal(err)
	}
	if thirdDigest == firstDigest {
		t.Fatal("binding-scope change must change the frozen routing digest")
	}
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
