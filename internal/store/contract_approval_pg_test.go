package store

import (
	"context"
	"testing"
)

// Durable checks for CLM-07 routing snapshots, template immutability and the
// #81/#78 submission boundary. They require TEST_DATABASE_URL and
// MIGRATOR_DATABASE_URL; without a database they are skipped and reported as
// NOT_RUN, never as passed.

func publishApprovalTemplate(t *testing.T, fixture durableCaseFixture, input PublishWorkflowTemplateInput) WorkflowTemplateSummary {
	t.Helper()
	summary, err := fixture.core.PublishWorkflowTemplate(context.Background(), fixture.admin, input)
	if err != nil {
		t.Fatal(err)
	}
	return summary
}

func TestWorkflowTemplatePublicationIsVersionedAndImmutable(t *testing.T) {
	fixture, _ := newContractDraftTestEnv(t)
	ctx := context.Background()
	input := validApprovalTemplate()

	first := publishApprovalTemplate(t, fixture, input)
	if first.TemplateVersion != "1" || first.TemplateDigest == "" || len(first.Nodes) != 7 {
		t.Fatalf("published template %+v", first)
	}
	replayed := publishApprovalTemplate(t, fixture, input)
	if replayed.TemplateDigest != first.TemplateDigest || !replayed.PublishedAt.Equal(first.PublishedAt) {
		t.Fatal("same template version and content must replay the committed version")
	}
	conflicting := input
	conflicting.Name = "改名的同一版本"
	_, err := fixture.core.PublishWorkflowTemplate(ctx, fixture.admin, conflicting)
	assertFaultStatus(t, err, 409, "template version content conflict")

	upgraded := input
	upgraded.TemplateVersion = "2"
	upgraded.Nodes = append([]WorkflowTemplateNodeInput(nil), input.Nodes...)
	upgraded.Nodes[0].RoleCode = "role_business_owner_v2"
	second := publishApprovalTemplate(t, fixture, upgraded)
	if second.TemplateVersion != "2" || second.Nodes[0].RoleCode != "role_business_owner_v2" {
		t.Fatalf("second template version %+v", second)
	}
	var templateID, version string
	if err := fixture.core.pool.QueryRow(ctx, `SELECT template_id,template_version FROM clm_workflow_template_pointers
		WHERE tenant_id=$1 AND pointer_key='default'`, fixture.admin.TenantID).Scan(&templateID, &version); err != nil {
		t.Fatal(err)
	}
	if templateID != input.TemplateID || version != "2" {
		t.Fatalf("default pointer = %s@%s", templateID, version)
	}
	var oldNodes int
	if err := fixture.core.pool.QueryRow(ctx, `SELECT count(*) FROM clm_workflow_template_nodes
		WHERE tenant_id=$1 AND template_id=$2 AND template_version='1'`, fixture.admin.TenantID, input.TemplateID).Scan(&oldNodes); err != nil {
		t.Fatal(err)
	}
	if oldNodes != 7 {
		t.Fatalf("old template version lost nodes: %d", oldNodes)
	}
}

func TestWorkflowRoleBindingsRequireTenantDirectoryAccounts(t *testing.T) {
	fixture, _ := newContractDraftTestEnv(t)
	ctx := context.Background()
	_, err := fixture.core.ReplaceWorkflowRoleBindings(ctx, fixture.admin, ReplaceWorkflowRoleInput{
		RoleCode: "role_legal", Bindings: []WorkflowRoleBindingInput{{OrgScope: "", UserIDs: []string{"missing-user"}}},
	})
	assertFaultStatus(t, err, 422, "unknown bound user")

	first, err := fixture.core.ReplaceWorkflowRoleBindings(ctx, fixture.admin, ReplaceWorkflowRoleInput{
		RoleCode: "role_legal", Bindings: []WorkflowRoleBindingInput{{OrgScope: "", UserIDs: []string{"demo-reviewer"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.RowVersion != 1 || len(first.Bindings) != 1 || first.Bindings[0].UserIDs[0] != "demo-reviewer" {
		t.Fatalf("first bindings %+v", first)
	}
	second, err := fixture.core.ReplaceWorkflowRoleBindings(ctx, fixture.admin, ReplaceWorkflowRoleInput{
		RoleCode: "role_legal", Bindings: []WorkflowRoleBindingInput{{OrgScope: "demo-other-org", UserIDs: []string{"demo-otherorg-clerk"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.RowVersion != 2 || len(second.Bindings) != 1 || second.Bindings[0].OrgScope != "demo-other-org" {
		t.Fatalf("second bindings %+v", second)
	}
	var count int
	if err := fixture.core.pool.QueryRow(ctx, `SELECT count(*) FROM clm_workflow_role_bindings
		WHERE tenant_id=$1 AND role_code='role_legal'`, fixture.admin.TenantID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("replace must remove old bindings, count=%d", count)
	}
}

func TestApprovalSubmissionRequiresARoutableRoleAndInsertsNoPartialRound(t *testing.T) {
	fixture, _ := newContractDraftTestEnv(t)
	ctx := context.Background()
	contractID, frozen := freezeTestContract(t, fixture)
	input := validApprovalTemplate()
	input.TemplateVersion = "approval-missing-role"
	publishApprovalTemplate(t, fixture, input)

	_, err := fixture.core.SubmitContractApproval(ctx, fixture.admin, SubmitContractApprovalInput{
		ContractID: contractID, ExpectedRowVersion: frozen.RowVersion, IdempotencyKey: "submit-missing-role-0001",
	})
	assertFaultStatus(t, err, 409, "missing role must block submission")
	var rounds, tasks int
	if err := fixture.core.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM clm_approval_rounds WHERE tenant_id=$1 AND contract_id=$2),
		(SELECT count(*) FROM clm_approval_tasks WHERE tenant_id=$1 AND contract_id=$2)`,
		fixture.admin.TenantID, contractID).Scan(&rounds, &tasks); err != nil {
		t.Fatal(err)
	}
	if rounds != 0 || tasks != 0 {
		t.Fatalf("rejected submission inserted round=%d tasks=%d", rounds, tasks)
	}
}

func TestSubmissionRejectsDraftChangedAfterFreeze(t *testing.T) {
	fixture, _ := newContractDraftTestEnv(t)
	ctx := context.Background()
	contractID, frozen := freezeTestContract(t, fixture)
	input := validApprovalTemplate()
	input.TemplateVersion = "approval-draft-change"
	publishApprovalTemplate(t, fixture, input)
	// Bindings deliberately point at the initiating admin only; the draft
	// integrity check must fail before routing and before any round row exists.
	for _, role := range approvalTestRoleCodes(input) {
		if _, err := fixture.core.ReplaceWorkflowRoleBindings(ctx, fixture.admin, ReplaceWorkflowRoleInput{
			RoleCode: role, Bindings: []WorkflowRoleBindingInput{{OrgScope: "", UserIDs: []string{"demo-admin"}}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	changed := freezeCompleteDraft()
	changed.Contract.Amount = "105000.00"
	// Keep the StageFinal identity intact so the test reaches the fingerprint
	// comparison: 10 x 10500.0000 = 105000.00.
	changed.Items[0].UnitPrice = "10500.0000"
	changed.Items[0].LineAmount = "105000.00"
	changed.Funding[0].Amount = "105000.00"
	changed.Payments[0].Amount = "105000.00"
	changed.Payments[0].UnitAmount = "105000.00"
	if _, err := fixture.core.ReplaceContractDraft(ctx, fixture.admin, ReplaceContractDraftInput{
		ContractID: contractID, ExpectedRowVersion: frozen.RowVersion, Draft: changed,
		IdempotencyKey: "submit-draft-change-replace",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.core.SubmitContractApproval(ctx, fixture.admin, SubmitContractApprovalInput{
		ContractID: contractID, ExpectedRowVersion: frozen.RowVersion + 1, IdempotencyKey: "submit-draft-changed-0001",
	})
	assertFaultStatus(t, err, 409, "draft changed after freeze")
	var rounds int
	if err := fixture.core.pool.QueryRow(ctx, `SELECT count(*) FROM clm_approval_rounds
		WHERE tenant_id=$1 AND contract_id=$2`, fixture.admin.TenantID, contractID).Scan(&rounds); err != nil {
		t.Fatal(err)
	}
	if rounds != 0 {
		t.Fatalf("changed draft still created %d rounds", rounds)
	}
}
