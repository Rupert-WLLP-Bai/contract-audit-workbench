package model

import "testing"

func validApprovalTemplate() PublishWorkflowTemplateInput {
	roles := []string{"role_business_owner", "role_domain", "role_control", "role_counsel", "role_legal", "role_leader", "role_final"}
	nodes := make([]WorkflowTemplateNodeInput, 0, len(roles))
	for i, code := range CanonicalApprovalNodeCodes() {
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
	base := NormalizeWorkflowTemplateInput(validApprovalTemplate())
	if err := ValidateWorkflowTemplate(base); err != nil {
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
		input := NormalizeWorkflowTemplateInput(validApprovalTemplate())
		item.mutate(&input)
		if err := ValidateWorkflowTemplate(input); err == nil {
			t.Fatalf("%s: template must be rejected", item.name)
		}
	}
}
