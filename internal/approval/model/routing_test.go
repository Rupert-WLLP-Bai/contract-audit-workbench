// File metadata (manifest mirror):
// Purpose: Preserves existing routing, initiator separation, organization scope and stable digest characterization directly in the Approval model.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	contract "contractaudit/internal/contract/model"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func approvalTestRecord(ownerOrg string) contract.Aggregate {
	return contract.Aggregate{
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

func approvalTestBindings(roleCodes []string) []RoleBindingRow {
	bindings := []RoleBindingRow{}
	for _, role := range roleCodes[:6] {
		bindings = append(bindings,
			RoleBindingRow{RoleCode: role, OrgScope: "", UserID: "reviewer-a"},
			RoleBindingRow{RoleCode: role, OrgScope: "org-a", UserID: "reviewer-b"},
			RoleBindingRow{RoleCode: role, OrgScope: "", UserID: "viewer"},
			RoleBindingRow{RoleCode: role, OrgScope: "", UserID: "disabled"},
			RoleBindingRow{RoleCode: role, OrgScope: "", UserID: "foreign"},
			RoleBindingRow{RoleCode: role, OrgScope: "", UserID: "owner"},
		)
	}
	bindings = append(bindings,
		RoleBindingRow{RoleCode: roleCodes[6], OrgScope: "", UserID: "reviewer-a"},
		RoleBindingRow{RoleCode: roleCodes[6], OrgScope: "org-a", UserID: "reviewer-b"},
		RoleBindingRow{RoleCode: roleCodes[6], OrgScope: "", UserID: "owner"},
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
	template := NormalizeWorkflowTemplateInput(validApprovalTemplate())
	record := approvalTestRecord("org-a")
	roles := approvalTestRoleCodes(template)
	snapshot, err := ResolveApprovalRouting(record, "owner", template.TemplateID, template.TemplateVersion,
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
		got := NodeAssignees(node)
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
	template := NormalizeWorkflowTemplateInput(validApprovalTemplate())
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
	_, err := ResolveApprovalRouting(record, "owner", template.TemplateID, template.TemplateVersion,
		template.Nodes, kept, approvalTestUsers(), time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), missingRole) {
		t.Fatalf("missing role must block submission with the role name, got %v", err)
	}
}

func TestApprovalRoutingMatchesExactOwnerOrganization(t *testing.T) {
	template := NormalizeWorkflowTemplateInput(validApprovalTemplate())
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
	snapshot, err := ResolveApprovalRouting(other, "owner", template.TemplateID, template.TemplateVersion,
		template.Nodes, bindings, users, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range snapshot.Nodes {
		for _, userID := range NodeAssignees(node) {
			if userID == "reviewer-b" {
				t.Fatalf("org-scoped binding leaked into node %s for another owner organization", node.NodeCode)
			}
		}
	}
}

func TestRoutingSnapshotDigestIsStableAndBindingSensitive(t *testing.T) {
	template := NormalizeWorkflowTemplateInput(validApprovalTemplate())
	roles := approvalTestRoleCodes(template)
	record := approvalTestRecord("org-a")
	first, err := ResolveApprovalRouting(record, "owner", template.TemplateID, template.TemplateVersion,
		template.Nodes, approvalTestBindings(roles), approvalTestUsers(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResolveApprovalRouting(record, "owner", template.TemplateID, template.TemplateVersion,
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
	third, err := ResolveApprovalRouting(record, "owner", template.TemplateID, template.TemplateVersion,
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

func canonicalHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
