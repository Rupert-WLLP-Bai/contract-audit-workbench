package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

// CLM-08 advances a submitted round one active task at a time. Decisions are
// append-only evidence; the round and task state updates happen in the same
// transaction, and a newer round never rewrites an older round's decisions.
const (
	clmRouteApprovalDecision = "clm.approval_decide"

	clmEventApprovalAdvanced  = "contract.approval_advanced.v1"
	clmEventApprovalReturned  = "contract.approval_returned.v1"
	clmEventApprovalCompleted = "contract.approval_completed.v1"

	clmDecisionApprove = "approve"
	clmDecisionReturn  = "return"
)

// DecideApprovalTaskInput is the typed decision command. TaskID comes from the
// path and idempotency from the header; tenant and actor from the identity.
type DecideApprovalTaskInput struct {
	TaskID                  string `json:"-"`
	Decision                string `json:"decision"`
	Comment                 string `json:"comment,omitempty"`
	ExpectedRoundRowVersion int64  `json:"expected_round_row_version"`
	Credential              string `json:"credential,omitempty"`
	IdempotencyKey          string `json:"-"`
}

// ApprovalDecisionResult is the idempotent decision response. NextTask is set
// only when an approval advances a non-final node.
type ApprovalDecisionResult struct {
	DecisionID    string               `json:"decision_id"`
	Decision      string               `json:"decision"`
	Round         ApprovalRoundSummary `json:"round"`
	Task          ApprovalTaskSummary  `json:"task"`
	NextTask      *ApprovalTaskSummary `json:"next_task"`
	ContractState string               `json:"contract_state"`
	WorkflowState string               `json:"workflow_state"`
	RowVersion    int64                `json:"row_version"`
}

type clmApprovalRoundRecord struct {
	ApprovalRoundID         string
	ContractID              string
	ContractRevisionID      string
	RoundNumber             int
	State                   string
	WorkflowTemplateID      string
	WorkflowTemplateVersion string
	RoutingDigest           string
	RowVersion              int64
	StartedAt               time.Time
	EndedAt                 *time.Time
	InitiatedBy             string
}

type clmApprovalTaskRecord struct {
	ApprovalTaskID  string
	ContractID      string
	ApprovalRoundID string
	NodeCode        string
	NodeOrder       int
	Title           string
	RoleCode        string
	Final           bool
	State           string
	RowVersion      int64
	ActivatedAt     *time.Time
}

// DecideApprovalTask appends one approve/return decision, then advances the
// sequential round or closes it. The contract advisory lock is always taken
// before the round and task row locks; two concurrent decisions therefore
// produce one success and one 409, never two answers for the same node.
func (s *Store) DecideApprovalTask(ctx context.Context, user accounts.User, input DecideApprovalTaskInput) (ApprovalDecisionResult, error) {
	taskID, err := normalizeContractUUID(input.TaskID)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	input.Decision = strings.TrimSpace(input.Decision)
	input.Comment = strings.TrimSpace(input.Comment)
	if input.Decision != clmDecisionApprove && input.Decision != clmDecisionReturn {
		return ApprovalDecisionResult{}, fault.Invalid("decision 必须是 approve 或 return")
	}
	if input.Decision == clmDecisionReturn && input.Comment == "" {
		return ApprovalDecisionResult{}, fault.Invalid("退回必须填写理由")
	}
	if runeCount(input.Comment) > 2000 {
		return ApprovalDecisionResult{}, fault.Invalid("审批意见不能超过 2000 个字符")
	}
	if input.ExpectedRoundRowVersion < 1 {
		return ApprovalDecisionResult{}, fault.Invalid("审批决定必须携带当前轮次 row_version")
	}
	if err := validateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return ApprovalDecisionResult{}, err
	}
	digest, err := clmRequestDigest(clmRouteApprovalDecision, "POST", "/api/approval-tasks/"+taskID+"/decisions",
		input.ExpectedRoundRowVersion, struct {
			Decision string `json:"decision"`
			Comment  string `json:"comment"`
		}{Decision: input.Decision, Comment: input.Comment})
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能提交审批决定", nil)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	defer rollbackCLM(tx)
	// The credential envelope must name this tenant, subject and task before a
	// stored receipt can be replayed. The full row-version and snapshot match
	// follows after the task and round locks.
	if s.approvalKeyring != nil {
		claims, err := s.approvalKeyring.Verify(input.Credential)
		if err != nil {
			return ApprovalDecisionResult{}, fault.Forbidden("审批身份凭证无效或已过期")
		}
		if claims.TenantID != accounts.Tenant(current) || claims.SubjectID != current.ID ||
			claims.TaskID != taskID {
			return ApprovalDecisionResult{}, fault.Forbidden("审批身份凭证与当前任务、版本或范围不匹配")
		}
	}
	if err := lockWriteReceipt(ctx, tx, current, clmRouteApprovalDecision, input.IdempotencyKey); err != nil {
		return ApprovalDecisionResult{}, err
	}
	receipt, found, err := replayCLMWriteReceipt(ctx, tx, current, clmRouteApprovalDecision, input.IdempotencyKey, digest, "")
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	if found {
		var result ApprovalDecisionResult
		if err := json.Unmarshal(receipt.Response, &result); err != nil {
			return ApprovalDecisionResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ApprovalDecisionResult{}, err
		}
		return result, nil
	}
	tenantID := accounts.Tenant(current)
	// Resolve the task's contract for the advisory lock without trusting the
	// path to grant any object access.
	task, err := loadApprovalTask(ctx, tx, tenantID, taskID)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	if err := scopedContractLock(ctx, tx, task.ContractID); err != nil {
		return ApprovalDecisionResult{}, err
	}
	record, err := loadCLMContract(ctx, tx, tenantID, task.ContractID)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return ApprovalDecisionResult{}, fault.NotFound("没有找到对应记录")
	}
	if record.AuthorityMode != "native" {
		return ApprovalDecisionResult{}, fault.Forbidden("外部权威合同不能在本地提交审批决定")
	}
	if !authorizeContract(record, current, access.Decide) {
		return ApprovalDecisionResult{}, fault.Forbidden("当前授权不能审批此合同")
	}
	if !accounts.CanDecide(current.Role) {
		return ApprovalDecisionResult{}, fault.Forbidden("当前角色不能审批合同")
	}
	if record.ValidityState != "active" || record.LifecycleState != "draft" {
		return ApprovalDecisionResult{}, fault.Conflict("合同当前状态不允许继续会签")
	}
	round, err := loadApprovalRoundForUpdate(ctx, tx, tenantID, task.ContractID, task.ApprovalRoundID)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	if round.State != "in_review" {
		return ApprovalDecisionResult{}, fault.Conflict("会签轮次已结束")
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != round.ContractRevisionID {
		return ApprovalDecisionResult{}, fault.Conflict("审批轮次引用的不是当前冻结版本")
	}
	if round.RowVersion != input.ExpectedRoundRowVersion {
		return ApprovalDecisionResult{}, fault.Conflict("审批轮次已变化，请刷新待办后重试")
	}
	task, err = lockApprovalTask(ctx, tx, tenantID, task.ContractID, task.ApprovalRoundID, taskID)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	if task.State != "active" {
		return ApprovalDecisionResult{}, fault.Conflict("审批任务已处理或不是当前待办节点")
	}
	assigned, err := approvalTaskAssignedTo(ctx, tx, tenantID, task, current.ID)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	if !assigned {
		return ApprovalDecisionResult{}, fault.Forbidden("当前账号不是该会签节点的审批人")
	}
	if !task.Final && round.InitiatedBy == current.ID {
		return ApprovalDecisionResult{}, fault.Forbidden("发起人不能审批自己送审的独立会签节点")
	}
	// Role bindings are rechecked live: a revoked binding or organization
	// scope change takes effect immediately even though the task assignment is
	// a historical snapshot.
	bound, err := approvalRoleBindingCurrent(ctx, tx, tenantID, task.RoleCode, record.OwnerOrgID, current.ID)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	if !bound {
		return ApprovalDecisionResult{}, fault.Forbidden("会签角色绑定已撤销，不能继续审批")
	}
	// A transferred task belongs to the latest target only. Assignment rows are
	// append-only history, so the transfer fact is what removes the live right
	// from the original approver.
	transferTarget, transferred, err := latestApprovalTransfer(ctx, tx, tenantID, task)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	if transferred && transferTarget != current.ID {
		return ApprovalDecisionResult{}, fault.Forbidden("审批任务已转交，原受理人不能再作决定")
	}
	scope, err := loadApprovalRevisionScope(ctx, tx, tenantID, task.ContractID, round.ContractRevisionID)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	claims, err := s.verifyApprovalCredential(ctx, tx, current, task, record, round, scope, input.Credential)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	var actorOrgID, credentialID, credentialKeyID, credentialDigest, snapshotDigest, amountLimit, currency any
	var credentialIssuedAt, credentialExpiresAt any
	var contractRowVersion any
	if claims != nil {
		actorOrgID = claims.OrgID
		credentialID = claims.CredentialID
		credentialKeyID = claims.KeyID
		credentialDigest = sha256Bytes([]byte(input.Credential))
		credentialIssuedAt = time.Unix(claims.IssuedAt, 0).UTC()
		credentialExpiresAt = time.Unix(claims.ExpiresAt, 0).UTC()
		snapshotDigest = claims.SnapshotDigest
		amountLimit = claims.AmountLimit
		currency = claims.Currency
		contractRowVersion = claims.ContractRowVersion
	}
	nextTask, err := lockNextPendingApprovalTask(ctx, tx, tenantID, task)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	workflowState, err := approvalDecisionWorkflowState(task, nextTask != nil, input.Decision)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	decisionID, err := newContractUUID()
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_approval_decisions
		(tenant_id,decision_id,contract_id,approval_round_id,approval_task_id,contract_revision_id,
		 node_code,node_order,role_code,decision,comment,decided_by,round_row_version_after,workflow_state_after,
		 actor_org_id,credential_id,credential_key_id,credential_digest,credential_issued_at,
		 credential_expires_at,snapshot_digest,amount_limit,currency,contract_row_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,
		 $15,$16,$17,$18,$19,$20,$21,$22::text::numeric,$23,$24)`,
		tenantID, decisionID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID,
		round.ContractRevisionID, task.NodeCode, task.NodeOrder, task.RoleCode, input.Decision,
		input.Comment, current.ID, round.RowVersion+1, workflowState,
		actorOrgID, credentialID, credentialKeyID, credentialDigest, credentialIssuedAt,
		credentialExpiresAt, snapshotDigest, amountLimit, currency, contractRowVersion); err != nil {
		return ApprovalDecisionResult{}, clmUniqueViolation(err, "该审批节点已有决定")
	}
	if input.Decision == clmDecisionReturn {
		if err := closeApprovalTask(ctx, tx, tenantID, task, "returned", current.ID); err != nil {
			return ApprovalDecisionResult{}, err
		}
		if err := cancelPendingApprovalTasks(ctx, tx, tenantID, task, current.ID); err != nil {
			return ApprovalDecisionResult{}, err
		}
		var endedAt time.Time
		var roundVersion int64
		err := tx.QueryRow(ctx, `UPDATE clm_approval_rounds
			SET state='returned', row_version=row_version+1, ended_at=clock_timestamp()
			WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3
				AND state='in_review' AND row_version=$4
			RETURNING ended_at,row_version`,
			tenantID, task.ContractID, task.ApprovalRoundID, round.RowVersion).Scan(&endedAt, &roundVersion)
		if errors.Is(err, pgx.ErrNoRows) {
			return ApprovalDecisionResult{}, fault.Conflict("审批轮次已变化，请刷新待办后重试")
		}
		if err != nil {
			return ApprovalDecisionResult{}, err
		}
		rowVersion, err := bumpContractRowVersion(ctx, tx, tenantID, record, "")
		if err != nil {
			return ApprovalDecisionResult{}, err
		}
		if err := insertCLMApprovalEvent(ctx, tx, tenantID, task.ContractID, rowVersion,
			clmEventApprovalReturned, map[string]any{
				"contract_id": task.ContractID, "approval_round_id": task.ApprovalRoundID,
				"approval_task_id": task.ApprovalTaskID, "decision_id": decisionID,
				"contract_revision_id": round.ContractRevisionID, "node_code": task.NodeCode,
				"node_order": task.NodeOrder, "comment_digest": sha256Bytes([]byte(input.Comment)),
				"workflow_state": workflowState,
			}); err != nil {
			return ApprovalDecisionResult{}, err
		}
		round.State = "returned"
		round.RowVersion = roundVersion
		round.EndedAt = &endedAt
		task.State = "returned"
		taskSummary, err := loadApprovalTaskSummary(ctx, tx, tenantID, task)
		if err != nil {
			return ApprovalDecisionResult{}, err
		}
		result := ApprovalDecisionResult{
			DecisionID: decisionID, Decision: input.Decision,
			Round: approvalRoundSummary(round), Task: taskSummary,
			NextTask: nil, ContractState: record.LifecycleState, WorkflowState: workflowState,
			RowVersion: rowVersion,
		}
		return finishApprovalDecision(ctx, tx, current, tenantID, task.ContractID, input, digest, result)
	}
	if err := closeApprovalTask(ctx, tx, tenantID, task, "approved", current.ID); err != nil {
		return ApprovalDecisionResult{}, err
	}
	task.State = "approved"
	taskSummary, err := loadApprovalTaskSummary(ctx, tx, tenantID, task)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	if nextTask != nil {
		if err := activateApprovalTask(ctx, tx, tenantID, nextTask); err != nil {
			return ApprovalDecisionResult{}, err
		}
		var roundVersion int64
		err := tx.QueryRow(ctx, `UPDATE clm_approval_rounds
			SET row_version=row_version+1
			WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3
				AND state='in_review' AND row_version=$4
			RETURNING row_version`,
			tenantID, task.ContractID, task.ApprovalRoundID, round.RowVersion).Scan(&roundVersion)
		if errors.Is(err, pgx.ErrNoRows) {
			return ApprovalDecisionResult{}, fault.Conflict("审批轮次已变化，请刷新待办后重试")
		}
		if err != nil {
			return ApprovalDecisionResult{}, err
		}
		rowVersion, err := bumpContractRowVersion(ctx, tx, tenantID, record, "")
		if err != nil {
			return ApprovalDecisionResult{}, err
		}
		nextSummary, err := loadApprovalTaskSummary(ctx, tx, tenantID, *nextTask)
		if err != nil {
			return ApprovalDecisionResult{}, err
		}
		if err := insertCLMApprovalEvent(ctx, tx, tenantID, task.ContractID, rowVersion,
			clmEventApprovalAdvanced, map[string]any{
				"contract_id": task.ContractID, "approval_round_id": task.ApprovalRoundID,
				"approval_task_id": task.ApprovalTaskID, "decision_id": decisionID,
				"next_approval_task_id": nextTask.ApprovalTaskID, "next_node_order": nextTask.NodeOrder,
				"contract_revision_id": round.ContractRevisionID, "node_code": task.NodeCode,
				"node_order": task.NodeOrder, "comment_digest": sha256Bytes([]byte(input.Comment)),
				"workflow_state": workflowState,
			}); err != nil {
			return ApprovalDecisionResult{}, err
		}
		round.RowVersion = roundVersion
		result := ApprovalDecisionResult{
			DecisionID: decisionID, Decision: input.Decision,
			Round: approvalRoundSummary(round), Task: taskSummary,
			NextTask: &nextSummary, ContractState: record.LifecycleState, WorkflowState: workflowState,
			RowVersion: rowVersion,
		}
		return finishApprovalDecision(ctx, tx, current, tenantID, task.ContractID, input, digest, result)
	}
	var endedAt time.Time
	var roundVersion int64
	err = tx.QueryRow(ctx, `UPDATE clm_approval_rounds
		SET state='approved', row_version=row_version+1, ended_at=clock_timestamp()
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3
			AND state='in_review' AND row_version=$4
		RETURNING ended_at,row_version`,
		tenantID, task.ContractID, task.ApprovalRoundID, round.RowVersion).Scan(&endedAt, &roundVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ApprovalDecisionResult{}, fault.Conflict("审批轮次已变化，请刷新待办后重试")
	}
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	rowVersion, err := bumpContractRowVersion(ctx, tx, tenantID, record, "ready_to_sign")
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	if err := insertCLMApprovalEvent(ctx, tx, tenantID, task.ContractID, rowVersion,
		clmEventApprovalCompleted, map[string]any{
			"contract_id": task.ContractID, "approval_round_id": task.ApprovalRoundID,
			"approval_task_id": task.ApprovalTaskID, "decision_id": decisionID,
			"contract_revision_id": round.ContractRevisionID, "node_code": task.NodeCode,
			"node_order": task.NodeOrder, "comment_digest": sha256Bytes([]byte(input.Comment)),
			"workflow_state": workflowState,
		}); err != nil {
		return ApprovalDecisionResult{}, err
	}
	round.State = "approved"
	round.RowVersion = roundVersion
	round.EndedAt = &endedAt
	result := ApprovalDecisionResult{
		DecisionID: decisionID, Decision: input.Decision,
		Round: approvalRoundSummary(round), Task: taskSummary,
		NextTask: nil, ContractState: "ready_to_sign", WorkflowState: workflowState,
		RowVersion: rowVersion,
	}
	return finishApprovalDecision(ctx, tx, current, tenantID, task.ContractID, input, digest, result)
}

// approvalDecisionWorkflowState is the pure sequential transition. A return
// always ends the round; an approval advances to the next pending node and
// only the final node completes it. A missing next task before the final node,
// or a remaining task after it, is data corruption and is refused.
func approvalDecisionWorkflowState(task clmApprovalTaskRecord, hasNext bool, decision string) (string, error) {
	if decision == clmDecisionReturn {
		return "returned", nil
	}
	if task.Final {
		if hasNext {
			return "", fault.Conflict("会签顺序不一致：最终节点后仍有待办任务")
		}
		return "approved", nil
	}
	if !hasNext {
		return "", fault.Conflict("会签顺序不一致：非最终节点后没有待办任务")
	}
	return "in_review", nil
}

// finishApprovalDecision stores and returns the idempotent response. The
// receipt commits with the decision, task, round and contract rows.
func finishApprovalDecision(ctx context.Context, tx pgx.Tx, current accounts.User,
	tenantID, contractID string, input DecideApprovalTaskInput, digest string,
	result ApprovalDecisionResult) (ApprovalDecisionResult, error) {
	response, err := json.Marshal(result)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	if err := insertCLMWriteReceipt(ctx, tx, tenantID, current.ID, clmRouteApprovalDecision,
		input.IdempotencyKey, digest, contractID, 201, response); err != nil {
		return ApprovalDecisionResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApprovalDecisionResult{}, err
	}
	return result, nil
}

func loadApprovalTask(ctx context.Context, tx pgx.Tx, tenantID, taskID string) (clmApprovalTaskRecord, error) {
	var task clmApprovalTaskRecord
	err := tx.QueryRow(ctx, `SELECT approval_task_id,contract_id,approval_round_id,node_code,node_order,
			title,role_code,is_final,state,row_version,activated_at
		FROM clm_approval_tasks WHERE tenant_id=$1 AND approval_task_id=$2`,
		tenantID, taskID).Scan(&task.ApprovalTaskID, &task.ContractID, &task.ApprovalRoundID,
		&task.NodeCode, &task.NodeOrder, &task.Title, &task.RoleCode, &task.Final,
		&task.State, &task.RowVersion, &task.ActivatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmApprovalTaskRecord{}, fault.NotFound("没有找到对应审批任务")
	}
	return task, err
}

func lockApprovalTask(ctx context.Context, tx pgx.Tx, tenantID, contractID, roundID, taskID string) (clmApprovalTaskRecord, error) {
	var task clmApprovalTaskRecord
	err := tx.QueryRow(ctx, `SELECT approval_task_id,contract_id,approval_round_id,node_code,node_order,
			title,role_code,is_final,state,row_version,activated_at
		FROM clm_approval_tasks
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id=$4
		FOR UPDATE`, tenantID, contractID, roundID, taskID).Scan(&task.ApprovalTaskID, &task.ContractID,
		&task.ApprovalRoundID, &task.NodeCode, &task.NodeOrder, &task.Title, &task.RoleCode,
		&task.Final, &task.State, &task.RowVersion, &task.ActivatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmApprovalTaskRecord{}, fault.NotFound("没有找到对应审批任务")
	}
	return task, err
}

func loadApprovalRoundForUpdate(ctx context.Context, tx pgx.Tx, tenantID, contractID, roundID string) (clmApprovalRoundRecord, error) {
	var round clmApprovalRoundRecord
	err := tx.QueryRow(ctx, `SELECT approval_round_id,contract_id,contract_revision_id,round_number,state,
			workflow_template_id,workflow_template_version,COALESCE(routing_digest,''),row_version,
			started_at,ended_at,initiated_by
		FROM clm_approval_rounds
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 FOR UPDATE`,
		tenantID, contractID, roundID).Scan(&round.ApprovalRoundID, &round.ContractID,
		&round.ContractRevisionID, &round.RoundNumber, &round.State, &round.WorkflowTemplateID,
		&round.WorkflowTemplateVersion, &round.RoutingDigest, &round.RowVersion,
		&round.StartedAt, &round.EndedAt, &round.InitiatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmApprovalRoundRecord{}, fault.NotFound("没有找到对应会签轮次")
	}
	return round, err
}

func approvalTaskAssignedTo(ctx context.Context, tx pgx.Tx, tenantID string, task clmApprovalTaskRecord, userID string) (bool, error) {
	var assigned bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM clm_approval_task_assignments
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id=$4 AND user_id=$5)`,
		tenantID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID, userID).Scan(&assigned)
	return assigned, err
}

// approvalRoleBindingCurrent applies the live role-binding check at decision
// time. The frozen assignment proves who was routed at submission; this proves
// the operator has not since revoked that role for the contract organization.
func approvalRoleBindingCurrent(ctx context.Context, tx pgx.Tx, tenantID, roleCode, ownerOrgID, userID string) (bool, error) {
	var bound bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM clm_workflow_role_bindings binding
		JOIN clm_workflow_roles role ON role.tenant_id=binding.tenant_id AND role.role_code=binding.role_code
		WHERE binding.tenant_id=$1 AND binding.role_code=$2 AND binding.user_id=$3
			AND (binding.org_scope='' OR binding.org_scope=$4))`,
		tenantID, roleCode, userID, ownerOrgID).Scan(&bound)
	return bound, err
}

func lockNextPendingApprovalTask(ctx context.Context, tx pgx.Tx, tenantID string, current clmApprovalTaskRecord) (*clmApprovalTaskRecord, error) {
	var task clmApprovalTaskRecord
	err := tx.QueryRow(ctx, `SELECT approval_task_id,contract_id,approval_round_id,node_code,node_order,
			title,role_code,is_final,state,row_version,activated_at
		FROM clm_approval_tasks
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3
			AND state='pending' AND node_order > $4
		ORDER BY node_order LIMIT 1 FOR UPDATE`,
		tenantID, current.ContractID, current.ApprovalRoundID, current.NodeOrder).Scan(
		&task.ApprovalTaskID, &task.ContractID, &task.ApprovalRoundID, &task.NodeCode,
		&task.NodeOrder, &task.Title, &task.RoleCode, &task.Final, &task.State,
		&task.RowVersion, &task.ActivatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func closeApprovalTask(ctx context.Context, tx pgx.Tx, tenantID string, task clmApprovalTaskRecord, state, actorID string) error {
	tag, err := tx.Exec(ctx, `UPDATE clm_approval_tasks
		SET state=$5, row_version=row_version+1, decided_at=clock_timestamp(), decided_by=$6
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id=$4
			AND state='active' AND row_version=$7`,
		tenantID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID,
		state, actorID, task.RowVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict("审批任务已变化，请刷新待办后重试")
	}
	return nil
}

func activateApprovalTask(ctx context.Context, tx pgx.Tx, tenantID string, task *clmApprovalTaskRecord) error {
	tag, err := tx.Exec(ctx, `UPDATE clm_approval_tasks
		SET state='active', row_version=row_version+1, activated_at=clock_timestamp()
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id=$4
			AND state='pending' AND row_version=$5`,
		tenantID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID, task.RowVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict("下一审批任务已变化，请刷新待办后重试")
	}
	task.State = "active"
	task.RowVersion++
	return nil
}

func cancelPendingApprovalTasks(ctx context.Context, tx pgx.Tx, tenantID string, task clmApprovalTaskRecord, actorID string) error {
	_, err := tx.Exec(ctx, `UPDATE clm_approval_tasks
		SET state='cancelled', row_version=row_version+1, decided_at=clock_timestamp(), decided_by=$5
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id<>$4
			AND state='pending'`,
		tenantID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID, actorID)
	return err
}

// bumpContractRowVersion advances the aggregate version for every decision.
// A final approval is the only transition that changes the lifecycle state,
// and it stops at ready_to_sign: local approval never registers or signs.
func bumpContractRowVersion(ctx context.Context, tx pgx.Tx, tenantID string, record clmContractRecord, lifecycleState string) (int64, error) {
	var rowVersion int64
	var err error
	if lifecycleState == "" {
		err = tx.QueryRow(ctx, `UPDATE clm_contracts
			SET row_version=row_version+1, updated_at=clock_timestamp()
			WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$3
				AND lifecycle_state='draft' AND validity_state='active'
			RETURNING row_version`,
			tenantID, record.ContractID, record.RowVersion).Scan(&rowVersion)
	} else {
		err = tx.QueryRow(ctx, `UPDATE clm_contracts
			SET lifecycle_state=$4, row_version=row_version+1, updated_at=clock_timestamp()
			WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$3
				AND lifecycle_state='draft' AND validity_state='active'
			RETURNING row_version`,
			tenantID, record.ContractID, record.RowVersion, lifecycleState).Scan(&rowVersion)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fault.Conflict("合同状态或版本已变化，请刷新后确认")
	}
	return rowVersion, err
}

func loadApprovalTaskSummary(ctx context.Context, tx pgx.Tx, tenantID string, task clmApprovalTaskRecord) (ApprovalTaskSummary, error) {
	rows, err := tx.Query(ctx, `SELECT user_id FROM clm_approval_task_assignments
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id=$4
		ORDER BY user_id`, tenantID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID)
	if err != nil {
		return ApprovalTaskSummary{}, err
	}
	defer rows.Close()
	assignees := []string{}
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return ApprovalTaskSummary{}, err
		}
		assignees = append(assignees, userID)
	}
	if err := rows.Err(); err != nil {
		return ApprovalTaskSummary{}, err
	}
	return approvalTaskSummary(task, assignees), nil
}

func approvalTaskSummary(task clmApprovalTaskRecord, assignees []string) ApprovalTaskSummary {
	if assignees == nil {
		assignees = []string{}
	}
	return ApprovalTaskSummary{
		ApprovalTaskID: task.ApprovalTaskID, NodeCode: task.NodeCode, NodeOrder: task.NodeOrder,
		Title: task.Title, RoleCode: task.RoleCode, Final: task.Final, State: task.State,
		AssigneeIDs: assignees,
	}
}

func approvalRoundSummary(round clmApprovalRoundRecord) ApprovalRoundSummary {
	return ApprovalRoundSummary{
		ApprovalRoundID: round.ApprovalRoundID, ContractRevisionID: round.ContractRevisionID,
		RoundNumber: round.RoundNumber, State: round.State,
		WorkflowTemplateID: round.WorkflowTemplateID, WorkflowTemplateVersion: round.WorkflowTemplateVersion,
		RoutingDigest: round.RoutingDigest, RowVersion: round.RowVersion,
		StartedAt: round.StartedAt, EndedAt: round.EndedAt,
	}
}
