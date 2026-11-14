// File metadata (manifest mirror):
// Purpose: PublishWorkflowTemplate appends one immutable template version and points new submissions at it. Re-publishing the exact same version is a no-op; reusing a version string with… Related `store` operations include PublishWorkflowTemplate, loadWorkflowTemplateVersion, ReplaceWorkflowRoleBindings, normalizeWorkflowRoleBindings.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-13
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"
	"strings"

	"contractaudit/internal/accounts"
	approvalapp "contractaudit/internal/approval/app"
	approvalmodel "contractaudit/internal/approval/model"
	approvalpg "contractaudit/internal/approval/postgres"
	outboxpg "contractaudit/internal/clmoutbox/postgres"
	contractpg "contractaudit/internal/contract/postgres"
	"contractaudit/internal/fault"
	platformpg "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

// PublishWorkflowTemplate appends one immutable template version and points new
// submissions at it. Re-publishing the exact same version is a no-op; reusing
// a version string with different content is a conflict, never an overwrite.
func (s *Store) PublishWorkflowTemplate(ctx context.Context, user accounts.User, input PublishWorkflowTemplateInput) (WorkflowTemplateSummary, error) {
	ctx = commandContext(ctx, user)
	if !s.HasDatabase() {
		return WorkflowTemplateSummary{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	input = normalizeWorkflowTemplateInput(input)
	if err := validateWorkflowTemplate(input); err != nil {
		return WorkflowTemplateSummary{}, err
	}
	admin, err := trustedAdmin(user)
	if err != nil {
		return WorkflowTemplateSummary{}, err
	}
	var summary WorkflowTemplateSummary
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		var inner error
		summary, inner = approvalapp.PublishTemplate(context.Background(), accounts.Tenant(admin), admin.ID, input, approvalpg.NewGovernance(platformpg.Bind(tx)))
		return inner
	})
	if err != nil {
		return WorkflowTemplateSummary{}, err
	}
	return summary, nil
}

func loadWorkflowTemplateVersion(ctx context.Context, tx pgx.Tx, tenantID, templateID, version string) (WorkflowTemplateSummary, bool, error) {
	return approvalpg.NewGovernance(platformpg.Bind(tx)).LoadTemplate(ctx, tenantID, templateID, version)
}

// ReplaceWorkflowRoleBindings replaces every binding of one role. The write is
// serialized tenant-wide by the admin transaction; the returned row_version
// lets an operator detect that another administrator changed the role after
// reading it. In-flight rounds are unaffected because they own a snapshot.
func (s *Store) ReplaceWorkflowRoleBindings(ctx context.Context, user accounts.User, input ReplaceWorkflowRoleInput) (WorkflowRoleSummary, error) {
	ctx = commandContext(ctx, user)
	if !s.HasDatabase() {
		return WorkflowRoleSummary{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	roleCode := strings.TrimSpace(input.RoleCode)
	if !approvalmodel.ValidRoleCode(roleCode) {
		return WorkflowRoleSummary{}, fault.Invalid("role_code 必须是小写字母开头的 3 到 64 位标识")
	}
	admin, err := trustedAdmin(user)
	if err != nil {
		return WorkflowRoleSummary{}, err
	}
	bindings, err := normalizeWorkflowRoleBindings(admin, input.Bindings)
	if err != nil {
		return WorkflowRoleSummary{}, err
	}
	var summary WorkflowRoleSummary
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		ctx := context.Background()
		var inner error
		summary, inner = approvalpg.NewGovernance(platformpg.Bind(tx)).ReplaceRole(ctx, accounts.Tenant(admin), admin.ID, roleCode, bindings)
		return inner
	})
	if err != nil {
		return WorkflowRoleSummary{}, err
	}
	return summary, nil
}

// normalizeWorkflowRoleBindings validates bound identities against the trusted
// directory. A role may be cleared, but it can never silently point at a user
// from another tenant or at a disabled account.
type approvalDirectory struct{}

func (approvalDirectory) LookupUser(id string) (accounts.User, bool) { return accounts.ByID(id) }
func normalizeWorkflowRoleBindings(admin accounts.User, input []WorkflowRoleBindingInput) ([]WorkflowRoleBindingSummary, error) {
	return approvalapp.NormalizeRoleBindings(accounts.Tenant(admin), input, approvalDirectory{})
}

// SubmitContractApproval starts one sequential countersign round on the frozen
// current revision. It re-proves that the editable draft still matches the
// #81 seal before creating the round, so a changed draft can never be reviewed
// under an older frozen version.
func (s *Store) SubmitContractApproval(ctx context.Context, user accounts.User, input SubmitContractApprovalInput) (SubmitContractApprovalResult, error) {
	ctx = commandContext(ctx, user)
	prepared, err := approvalapp.PrepareSubmission(input)
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}

	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能送审合同", nil)
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	defer rollbackCLM(tx)
	executor := platformpg.Bind(tx)
	result, err := approvalapp.Submit(ctx, prepared, accounts.Tenant(current), current.ID, approvalDirectory{},
		contractpg.NewApprovalCommands(executor), approvalSubmissionAccess{tx: tx, user: current},
		approvalSubmissionFreeze{tx: tx, user: current, executor: executor}, approvalpg.NewSubmission(executor),
		approvalpg.NewGovernance(executor), storeVersionEventAdapter{tx: tx})
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return SubmitContractApprovalResult{}, err
	}
	return result, nil
}

// resolveApprovalMaterialSummary re-resolves a candidate snapshot's materials
// through the same strict resolver and projection the freeze path uses, so the
// resulting digest is directly comparable with the seal. It is a projection
// over the shared resolver, not a second material validator.
func resolveApprovalMaterialSummary(ctx context.Context, tx pgx.Tx, current accounts.User, anchor Case, refs []MaterialRef) ([]frozenVersionMaterial, error) {
	tenantID := accounts.Tenant(current)
	resolved := make([]ContractMaterialReference, 0, len(refs))
	for i := range refs {
		material, err := resolveContractMaterial(ctx, tx, tenantID, anchor, &refs[i], referenceModeFrozen)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, ContractMaterialReference{
			MaterialID: material.MaterialID, MaterialVersionID: material.DocumentID,
			SHA256: material.Digest, Purpose: refs[i].Purpose,
			MaterialVersion: material.Version, MaterialRole: material.MaterialRole,
			Filename: material.Filename,
		})
	}
	return frozenMaterialsFromReferences(anchor, resolved)
}

func insertCLMApprovalEvent(ctx context.Context, tx pgx.Tx, tenantID, contractID string,
	aggregateVersion int64, eventType string, payload map[string]any) error {
	eventID, err := newContractUUID()
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	handle := platformpg.Bind(tx)
	if err := contractpg.Append(ctx, handle, contractpg.BusinessEvent{
		TenantID: tenantID, EventID: eventID, ContractID: contractID,
		AggregateVersion: aggregateVersion, EventType: eventType, Payload: body,
	}); err != nil {
		return err
	}
	return outboxpg.Enqueue(ctx, handle, tenantID, eventID)
}
