package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/contractmoney"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

// PublishWorkflowTemplate appends one immutable template version and points new
// submissions at it. Re-publishing the exact same version is a no-op; reusing
// a version string with different content is a conflict, never an overwrite.
func (s *Store) PublishWorkflowTemplate(ctx context.Context, user accounts.User, input PublishWorkflowTemplateInput) (WorkflowTemplateSummary, error) {
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
	digest, err := canonicalHash(input)
	if err != nil {
		return WorkflowTemplateSummary{}, err
	}
	var summary WorkflowTemplateSummary
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		ctx := context.Background()
		existing, found, err := loadWorkflowTemplateVersion(ctx, tx, accounts.Tenant(admin), input.TemplateID, input.TemplateVersion)
		if err != nil {
			return err
		}
		if found {
			if existing.TemplateDigest != digest {
				return fault.Conflict("该会签模板版本已存在且内容不同，请发布新版本")
			}
			summary = existing
			return nil
		}
		var publishedAt time.Time
		if err := tx.QueryRow(ctx, `INSERT INTO clm_workflow_templates
			(tenant_id,template_id,template_version,name,start_action,start_action_is_approval_node,
			 execution,node_count,template_digest,published_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING published_at`,
			accounts.Tenant(admin), input.TemplateID, input.TemplateVersion, input.Name,
			input.StartAction, input.StartActionIsApprovalNode, input.Execution,
			len(input.Nodes), digest, admin.ID).Scan(&publishedAt); err != nil {
			return clmUniqueViolation(err, "该会签模板版本已被他人发布")
		}
		summary = WorkflowTemplateSummary{
			TemplateID: input.TemplateID, TemplateVersion: input.TemplateVersion, Name: input.Name,
			StartAction: input.StartAction, StartActionIsApprovalNode: input.StartActionIsApprovalNode,
			Execution: input.Execution, TemplateDigest: digest, PublishedBy: admin.ID, PublishedAt: publishedAt,
			Nodes: make([]WorkflowTemplateNode, 0, len(input.Nodes)),
		}
		for i, node := range input.Nodes {
			if _, err := tx.Exec(ctx, `INSERT INTO clm_workflow_template_nodes
				(tenant_id,template_id,template_version,node_code,node_order,title,role_code,is_final)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				accounts.Tenant(admin), input.TemplateID, input.TemplateVersion, node.NodeCode,
				i+1, node.Title, node.RoleCode, node.Final); err != nil {
				return err
			}
			summary.Nodes = append(summary.Nodes, WorkflowTemplateNode{
				NodeCode: node.NodeCode, NodeOrder: i + 1, Title: node.Title,
				RoleCode: node.RoleCode, Final: node.Final,
			})
		}
		if _, err := tx.Exec(ctx, `INSERT INTO clm_workflow_template_pointers
			(tenant_id,pointer_key,template_id,template_version,updated_by)
			VALUES ($1,'default',$2,$3,$4)
			ON CONFLICT (tenant_id,pointer_key) DO UPDATE
			SET template_id=EXCLUDED.template_id, template_version=EXCLUDED.template_version,
			    updated_by=EXCLUDED.updated_by, updated_at=clock_timestamp()`,
			accounts.Tenant(admin), input.TemplateID, input.TemplateVersion, admin.ID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return WorkflowTemplateSummary{}, err
	}
	return summary, nil
}

func loadWorkflowTemplateVersion(ctx context.Context, tx pgx.Tx, tenantID, templateID, version string) (WorkflowTemplateSummary, bool, error) {
	var summary WorkflowTemplateSummary
	err := tx.QueryRow(ctx, `SELECT name,start_action,start_action_is_approval_node,execution,
			template_digest,published_by,published_at
		FROM clm_workflow_templates
		WHERE tenant_id=$1 AND template_id=$2 AND template_version=$3`,
		tenantID, templateID, version).Scan(&summary.Name, &summary.StartAction,
		&summary.StartActionIsApprovalNode, &summary.Execution, &summary.TemplateDigest,
		&summary.PublishedBy, &summary.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkflowTemplateSummary{}, false, nil
	}
	if err != nil {
		return WorkflowTemplateSummary{}, false, err
	}
	summary.TemplateID = templateID
	summary.TemplateVersion = version
	rows, err := tx.Query(ctx, `SELECT node_code,node_order,title,role_code,is_final
		FROM clm_workflow_template_nodes
		WHERE tenant_id=$1 AND template_id=$2 AND template_version=$3 ORDER BY node_order`,
		tenantID, templateID, version)
	if err != nil {
		return WorkflowTemplateSummary{}, false, err
	}
	defer rows.Close()
	summary.Nodes = []WorkflowTemplateNode{}
	for rows.Next() {
		var node WorkflowTemplateNode
		if err := rows.Scan(&node.NodeCode, &node.NodeOrder, &node.Title, &node.RoleCode, &node.Final); err != nil {
			return WorkflowTemplateSummary{}, false, err
		}
		summary.Nodes = append(summary.Nodes, node)
	}
	if err := rows.Err(); err != nil {
		return WorkflowTemplateSummary{}, false, err
	}
	return summary, true, nil
}

// ReplaceWorkflowRoleBindings replaces every binding of one role. The write is
// serialized tenant-wide by the admin transaction; the returned row_version
// lets an operator detect that another administrator changed the role after
// reading it. In-flight rounds are unaffected because they own a snapshot.
func (s *Store) ReplaceWorkflowRoleBindings(ctx context.Context, user accounts.User, input ReplaceWorkflowRoleInput) (WorkflowRoleSummary, error) {
	if !s.HasDatabase() {
		return WorkflowRoleSummary{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	roleCode := strings.TrimSpace(input.RoleCode)
	if !clmRoleCodePattern.MatchString(roleCode) {
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
		var rowVersion int64
		err := tx.QueryRow(ctx, `SELECT row_version FROM clm_workflow_roles
			WHERE tenant_id=$1 AND role_code=$2 FOR UPDATE`, accounts.Tenant(admin), roleCode).Scan(&rowVersion)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			rowVersion = 1
			if _, err := tx.Exec(ctx, `INSERT INTO clm_workflow_roles
				(tenant_id,role_code,row_version,updated_by) VALUES ($1,$2,1,$3)`,
				accounts.Tenant(admin), roleCode, admin.ID); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			rowVersion++
			if _, err := tx.Exec(ctx, `UPDATE clm_workflow_roles
				SET row_version=$3, updated_by=$4, updated_at=clock_timestamp()
				WHERE tenant_id=$1 AND role_code=$2`, accounts.Tenant(admin), roleCode, rowVersion, admin.ID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM clm_workflow_role_bindings
			WHERE tenant_id=$1 AND role_code=$2`, accounts.Tenant(admin), roleCode); err != nil {
			return err
		}
		for _, binding := range bindings {
			for _, userID := range binding.UserIDs {
				if _, err := tx.Exec(ctx, `INSERT INTO clm_workflow_role_bindings
					(tenant_id,role_code,org_scope,user_id) VALUES ($1,$2,$3,$4)`,
					accounts.Tenant(admin), roleCode, binding.OrgScope, userID); err != nil {
					return err
				}
			}
		}
		var updatedAt time.Time
		if err := tx.QueryRow(ctx, `SELECT updated_at FROM clm_workflow_roles
			WHERE tenant_id=$1 AND role_code=$2`, accounts.Tenant(admin), roleCode).Scan(&updatedAt); err != nil {
			return err
		}
		summary = WorkflowRoleSummary{RoleCode: roleCode, RowVersion: rowVersion,
			Bindings: bindings, UpdatedBy: admin.ID, UpdatedAt: updatedAt}
		return nil
	})
	if err != nil {
		return WorkflowRoleSummary{}, err
	}
	return summary, nil
}

// normalizeWorkflowRoleBindings validates bound identities against the trusted
// directory. A role may be cleared, but it can never silently point at a user
// from another tenant or at a disabled account.
func normalizeWorkflowRoleBindings(admin accounts.User, input []WorkflowRoleBindingInput) ([]WorkflowRoleBindingSummary, error) {
	seenScopes := map[string]bool{}
	out := make([]WorkflowRoleBindingSummary, 0, len(input))
	for _, binding := range input {
		scope := strings.TrimSpace(binding.OrgScope)
		if seenScopes[scope] {
			return nil, fault.Invalid("同一角色的绑定范围不能重复")
		}
		seenScopes[scope] = true
		seenUsers := map[string]bool{}
		users := make([]string, 0, len(binding.UserIDs))
		for _, raw := range binding.UserIDs {
			userID := strings.TrimSpace(raw)
			if userID == "" || seenUsers[userID] {
				continue
			}
			current, ok := accounts.ByID(userID)
			if !ok || !current.Enabled || current.TenantID != accounts.Tenant(admin) {
				return nil, fault.Invalid("角色绑定只能指向本租户内已启用的目录账号")
			}
			seenUsers[userID] = true
			users = append(users, userID)
		}
		sort.Strings(users)
		out = append(out, WorkflowRoleBindingSummary{OrgScope: scope, UserIDs: users})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OrgScope < out[j].OrgScope })
	return out, nil
}

// loadActiveWorkflowTemplate reads the tenant's default template version. It
// never follows a newer pointer for an existing round; only submission calls
// this function.
func loadActiveWorkflowTemplate(ctx context.Context, tx pgx.Tx, tenantID, requestedID string) (clmWorkflowTemplate, error) {
	var templateID, version string
	err := tx.QueryRow(ctx, `SELECT template_id,template_version FROM clm_workflow_template_pointers
		WHERE tenant_id=$1 AND pointer_key='default'`, tenantID).Scan(&templateID, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmWorkflowTemplate{}, fault.Conflict("尚未发布会签模板，请先由管理员发布")
	}
	if err != nil {
		return clmWorkflowTemplate{}, err
	}
	if requestedID != "" && requestedID != templateID {
		return clmWorkflowTemplate{}, fault.Conflict("指定模板不是当前生效的会签模板")
	}
	summary, found, err := loadWorkflowTemplateVersion(ctx, tx, tenantID, templateID, version)
	if err != nil {
		return clmWorkflowTemplate{}, err
	}
	if !found {
		return clmWorkflowTemplate{}, fault.Conflict("当前生效的会签模板版本不存在")
	}
	input := PublishWorkflowTemplateInput{
		TemplateID: summary.TemplateID, TemplateVersion: summary.TemplateVersion, Name: summary.Name,
		StartAction: summary.StartAction, StartActionIsApprovalNode: summary.StartActionIsApprovalNode,
		Execution: summary.Execution,
		Nodes:     make([]WorkflowTemplateNodeInput, 0, len(summary.Nodes)),
	}
	for _, node := range summary.Nodes {
		input.Nodes = append(input.Nodes, WorkflowTemplateNodeInput{
			NodeCode: node.NodeCode, Title: node.Title, RoleCode: node.RoleCode, Final: node.Final,
		})
	}
	if err := validateWorkflowTemplate(normalizeWorkflowTemplateInput(input)); err != nil {
		return clmWorkflowTemplate{}, fault.Conflict("当前生效的会签模板内容不合法")
	}
	return clmWorkflowTemplate{WorkflowTemplateSummary: summary, Nodes: input.Nodes}, nil
}

// clmWorkflowTemplate is one loaded template version plus its ordered input
// nodes for routing resolution.
type clmWorkflowTemplate struct {
	WorkflowTemplateSummary
	Nodes []WorkflowTemplateNodeInput
}

func loadWorkflowRoleBindings(ctx context.Context, tx pgx.Tx, tenantID string) ([]clmRoleBindingRow, error) {
	rows, err := tx.Query(ctx, `SELECT role_code,org_scope,user_id FROM clm_workflow_role_bindings
		WHERE tenant_id=$1 ORDER BY role_code,org_scope,user_id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := []clmRoleBindingRow{}
	for rows.Next() {
		var binding clmRoleBindingRow
		if err := rows.Scan(&binding.RoleCode, &binding.OrgScope, &binding.UserID); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bindings, nil
}

// SubmitContractApproval starts one sequential countersign round on the frozen
// current revision. It re-proves that the editable draft still matches the
// #81 seal before creating the round, so a changed draft can never be reviewed
// under an older frozen version.
func (s *Store) SubmitContractApproval(ctx context.Context, user accounts.User, input SubmitContractApprovalInput) (SubmitContractApprovalResult, error) {
	id, err := normalizeContractUUID(input.ContractID)
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	if input.ExpectedRowVersion < 1 {
		return SubmitContractApprovalResult{}, fault.Invalid("送审必须携带当前合同 row_version")
	}
	if err := validateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return SubmitContractApprovalResult{}, err
	}
	requestedTemplate := strings.TrimSpace(input.TemplateID)
	if requestedTemplate != "" && !clmTemplateIDPattern.MatchString(requestedTemplate) {
		return SubmitContractApprovalResult{}, fault.Invalid("template_id 格式不正确")
	}
	digest, err := clmRequestDigest(clmRouteSubmitApproval, "POST", "/api/contracts/"+id+"/submissions",
		input.ExpectedRowVersion, struct {
			TemplateID string `json:"template_id,omitempty"`
		}{TemplateID: requestedTemplate})
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能送审合同", nil)
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	defer rollbackCLM(tx)
	if err := lockWriteReceipt(ctx, tx, current, clmRouteSubmitApproval, input.IdempotencyKey); err != nil {
		return SubmitContractApprovalResult{}, err
	}
	receipt, found, err := replayCLMWriteReceipt(ctx, tx, current, clmRouteSubmitApproval, input.IdempotencyKey, digest, id)
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	if found {
		record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
		if err != nil {
			return SubmitContractApprovalResult{}, err
		}
		if !authorizeContract(record, current, access.Read) {
			return SubmitContractApprovalResult{}, fault.NotFound("没有找到对应记录")
		}
		var result SubmitContractApprovalResult
		if err := json.Unmarshal(receipt.Response, &result); err != nil {
			return SubmitContractApprovalResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return SubmitContractApprovalResult{}, err
		}
		return result, nil
	}
	if err := scopedContractLock(ctx, tx, id); err != nil {
		return SubmitContractApprovalResult{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	if !authorizeContract(record, current, access.Read) {
		return SubmitContractApprovalResult{}, fault.NotFound("没有找到对应记录")
	}
	if !authorizeContract(record, current, access.Manage) {
		return SubmitContractApprovalResult{}, fault.Forbidden("当前授权不能送审此合同")
	}
	if record.AuthorityMode != "native" {
		return SubmitContractApprovalResult{}, fault.Forbidden("外部权威合同不允许在本地发起会签")
	}
	if record.LifecycleState != "draft" || record.ValidityState != "active" {
		return SubmitContractApprovalResult{}, fault.Conflict("合同当前状态不允许送审")
	}
	if record.RowVersion != input.ExpectedRowVersion {
		return SubmitContractApprovalResult{}, fault.Conflict("合同内容已被他人修改，请刷新后确认")
	}
	var hasActive bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM clm_approval_rounds
		WHERE tenant_id=$1 AND contract_id=$2 AND state='in_review')`,
		accounts.Tenant(current), id).Scan(&hasActive); err != nil {
		return SubmitContractApprovalResult{}, err
	}
	if hasActive {
		return SubmitContractApprovalResult{}, fault.Conflict("合同已有进行中的会签轮次")
	}
	if record.CurrentRevisionID == nil {
		return SubmitContractApprovalResult{}, fault.Conflict("请先冻结合同版本再送审")
	}
	revisionID := *record.CurrentRevisionID
	frozen, err := loadFrozenContractVersion(ctx, tx, accounts.Tenant(current), id, revisionID)
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	seal, err := loadContractVersionSeal(ctx, tx, accounts.Tenant(current), id, revisionID)
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	if err := s.verifyFrozenDraftForSubmission(ctx, tx, current, id, frozen, seal); err != nil {
		return SubmitContractApprovalResult{}, err
	}
	templateVersion, err := loadActiveWorkflowTemplate(ctx, tx, accounts.Tenant(current), requestedTemplate)
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	bindings, err := loadWorkflowRoleBindings(ctx, tx, accounts.Tenant(current))
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	routing, err := resolveApprovalRouting(record, current.ID, templateVersion.TemplateID,
		templateVersion.TemplateVersion, templateVersion.Nodes, bindings, accounts.List(), time.Now().UTC())
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	routingDigest, err := canonicalHash(routing)
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	routingJSON, err := json.Marshal(routing)
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	riskJSON, err := json.Marshal(clmRiskDispositionSnapshot{
		Schema: clmRiskDispositionSnapshotSchema, Status: "not_evaluated", Items: []any{},
	})
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	roundID, err := newContractUUID()
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	var roundNumber int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(round_number),0)+1 FROM clm_approval_rounds
		WHERE tenant_id=$1 AND contract_id=$2`, accounts.Tenant(current), id).Scan(&roundNumber); err != nil {
		return SubmitContractApprovalResult{}, err
	}
	var roundRowVersion int64
	var startedAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO clm_approval_rounds
		(tenant_id,contract_id,approval_round_id,contract_revision_id,round_number,state,
		 workflow_template_id,workflow_template_version,routing_snapshot,routing_digest,
		 risk_disposition_snapshot,initiated_by)
		VALUES ($1,$2,$3,$4,$5,'in_review',$6,$7,$8::jsonb,$9,$10::jsonb,$11)
		RETURNING row_version,started_at`,
		accounts.Tenant(current), id, roundID, revisionID, roundNumber,
		templateVersion.TemplateID, templateVersion.TemplateVersion,
		string(routingJSON), routingDigest, string(riskJSON), current.ID).Scan(&roundRowVersion, &startedAt)
	if err != nil {
		return SubmitContractApprovalResult{}, clmUniqueViolation(err, "合同已有进行中的会签轮次")
	}
	tasks := make([]ApprovalTaskSummary, 0, len(routing.Nodes))
	for i, node := range routing.Nodes {
		taskID, err := newContractUUID()
		if err != nil {
			return SubmitContractApprovalResult{}, err
		}
		state := "pending"
		if i == 0 {
			state = "active"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO clm_approval_tasks
			(tenant_id,contract_id,approval_round_id,approval_task_id,node_code,node_order,
			 title,role_code,is_final,state,activated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,
			 CASE WHEN $10='active' THEN clock_timestamp() ELSE NULL END)`,
			accounts.Tenant(current), id, roundID, taskID, node.NodeCode, node.NodeOrder,
			node.Title, node.RoleCode, node.Final, state); err != nil {
			return SubmitContractApprovalResult{}, err
		}
		for _, assignee := range node.Assignees {
			if _, err := tx.Exec(ctx, `INSERT INTO clm_approval_task_assignments
				(tenant_id,contract_id,approval_round_id,approval_task_id,user_id,display_name,role_code,binding_org_scope)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				accounts.Tenant(current), id, roundID, taskID, assignee.UserID,
				assignee.DisplayName, node.RoleCode, assignee.BindingOrgScope); err != nil {
				return SubmitContractApprovalResult{}, err
			}
		}
		tasks = append(tasks, ApprovalTaskSummary{
			ApprovalTaskID: taskID, NodeCode: node.NodeCode, NodeOrder: node.NodeOrder,
			Title: node.Title, RoleCode: node.RoleCode, Final: node.Final, State: state,
			AssigneeIDs: clmNodeAssignees(node),
		})
	}
	var rowVersion int64
	err = tx.QueryRow(ctx, `UPDATE clm_contracts
		SET row_version=row_version+1, updated_at=clock_timestamp()
		WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$3
			AND authority_mode='native' AND lifecycle_state='draft' AND validity_state='active'
			AND current_revision_id=$4
		RETURNING row_version`, accounts.Tenant(current), id, record.RowVersion, revisionID).Scan(&rowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return SubmitContractApprovalResult{}, fault.Conflict("合同状态或版本已变化，请刷新后确认")
	}
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	if err := insertCLMApprovalEvent(ctx, tx, accounts.Tenant(current), id, rowVersion,
		clmEventApprovalSubmitted, map[string]any{
			"contract_id": id, "approval_round_id": roundID, "contract_revision_id": revisionID,
			"round_number": roundNumber, "workflow_template_id": templateVersion.TemplateID,
			"workflow_template_version": templateVersion.TemplateVersion,
			"routing_digest":            routingDigest, "task_count": len(tasks), "initiated_by": current.ID,
		}); err != nil {
		return SubmitContractApprovalResult{}, err
	}
	result := SubmitContractApprovalResult{
		Round: ApprovalRoundSummary{
			ApprovalRoundID: roundID, ContractRevisionID: revisionID, RoundNumber: roundNumber,
			State: "in_review", WorkflowTemplateID: templateVersion.TemplateID,
			WorkflowTemplateVersion: templateVersion.TemplateVersion, RoutingDigest: routingDigest,
			RowVersion: roundRowVersion, StartedAt: startedAt, EndedAt: nil,
		},
		Tasks: tasks, ContractState: record.LifecycleState, WorkflowState: "in_review",
		RowVersion: rowVersion, AllowedCommands: []string{},
	}
	response, err := json.Marshal(result)
	if err != nil {
		return SubmitContractApprovalResult{}, err
	}
	if err := insertCLMWriteReceipt(ctx, tx, accounts.Tenant(current), current.ID, clmRouteSubmitApproval,
		input.IdempotencyKey, digest, id, 201, response); err != nil {
		return SubmitContractApprovalResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SubmitContractApprovalResult{}, err
	}
	return result, nil
}

// verifyFrozenDraftForSubmission ties the #81 freeze seal to the draft being
// submitted. It re-runs #78 StageFinal on both the frozen snapshot and the
// editable draft, and requires the seals, material digests and restricted-field
// fingerprints of the draft to equal the frozen revision.
func (s *Store) verifyFrozenDraftForSubmission(ctx context.Context, tx pgx.Tx, current accounts.User,
	contractID string, frozen frozenContractVersion, seal *contractVersionSeal) error {
	if seal == nil {
		return fault.Conflict("送审必须使用已封印的冻结版本")
	}
	if !frozen.AmountsReconciled {
		return fault.Conflict("冻结版本未通过金额恒等核对，不能送审")
	}
	if sha256Bytes(frozen.Snapshot) != seal.SnapshotDigest {
		return fault.Conflict("冻结版本摘要与封印不一致")
	}
	frozenPlaintext, err := unsealStoredDraft(frozen.Snapshot)
	if err != nil {
		return err
	}
	if err := validateDraftContent(&frozenPlaintext); err != nil {
		return err
	}
	if err := validateDraftAmounts(&frozenPlaintext, contractmoney.StageFinal); err != nil {
		return err
	}
	draftBytes, err := loadContractDraftBytes(ctx, tx, accounts.Tenant(current), contractID)
	if err != nil {
		return err
	}
	draftPlaintext, err := unsealStoredDraft(draftBytes)
	if err != nil {
		return err
	}
	if err := validateDraftContent(&draftPlaintext); err != nil {
		return err
	}
	if err := validateDraftAmounts(&draftPlaintext, contractmoney.StageFinal); err != nil {
		return err
	}
	anchor, err := loadFrozenApprovalAnchor(ctx, tx, accounts.Tenant(current), contractID, frozen.RevisionID)
	if err != nil {
		return err
	}
	for _, candidate := range []DraftContent{frozenPlaintext, draftPlaintext} {
		materials, err := resolveApprovalMaterialSummary(ctx, tx, current, anchor, candidate.Materials)
		if err != nil {
			return err
		}
		materialsDigest, err := canonicalHash(materials)
		if err != nil {
			return err
		}
		if materialsDigest != seal.MaterialsDigest {
			return fault.Conflict("合同材料在冻结后已变化，请重新冻结版本后再送审")
		}
		fingerprints, err := computeRestrictedFingerprints(&candidate)
		if err != nil {
			return err
		}
		contentFingerprint, err := canonicalHash(struct {
			Fields     map[string]any              `json:"fields"`
			Restricted clmRestrictedFingerprintSet `json:"restricted"`
		}{Fields: flattenContractDraft(candidate), Restricted: fingerprints})
		if err != nil {
			return err
		}
		if contentFingerprint != seal.ContentFingerprint {
			return fault.Conflict("合同内容在冻结后已变化，请重新冻结版本后再送审")
		}
	}
	return nil
}

// loadFrozenApprovalAnchor recovers the anchor case identity captured by the
// freeze transaction. Native drafts have no clm_contract_cases link, so the
// frozen material rows are the durable record of which case every reference
// was checked against. A version with no materials has no case identity to
// recover; an empty projection matches an empty seal either way.
func loadFrozenApprovalAnchor(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID string) (Case, error) {
	var orgID, caseID string
	err := tx.QueryRow(ctx, `SELECT case_org_id, case_id FROM clm_contract_version_materials
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3
		ORDER BY material_id LIMIT 1`, tenantID, contractID, revisionID).Scan(&orgID, &caseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Case{}, nil
	}
	if err != nil {
		return Case{}, err
	}
	return Case{ID: caseID, OwnerOrgID: orgID}, nil
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
	if _, err := tx.Exec(ctx, `INSERT INTO clm_business_events
		(tenant_id,event_id,contract_id,aggregate_version,event_type,schema_version,payload)
		VALUES ($1,$2,$3,$4,$5,1,$6::jsonb)`,
		tenantID, eventID, contractID, aggregateVersion, eventType, string(body)); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO clm_outbox_delivery (tenant_id,event_id) VALUES ($1,$2)`,
		tenantID, eventID)
	return err
}
