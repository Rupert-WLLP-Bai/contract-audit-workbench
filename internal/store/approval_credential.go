package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/approval"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

const (
	clmRouteApprovalCredential  = "clm.approval_credential"
	clmRouteApprovalTransfer    = "clm.approval_transfer"
	clmEventApprovalTransferred = "contract.approval_transferred.v1"
)

// ApprovalCredentialClaims is the non-secret projection returned with a
// credential. The token itself is the only bearer value; these claims tell the
// page which task, revision and row versions the token is bound to.
type ApprovalCredentialClaims struct {
	TaskID             string    `json:"task_id"`
	ContractID         string    `json:"contract_id"`
	ApprovalRoundID    string    `json:"approval_round_id"`
	ContractRevisionID string    `json:"contract_revision_id"`
	RevisionNumber     int       `json:"revision_number"`
	SnapshotDigest     string    `json:"snapshot_digest"`
	TaskRowVersion     int64     `json:"task_row_version"`
	ContractRowVersion int64     `json:"contract_row_version"`
	AmountLimit        string    `json:"amount_limit"`
	Currency           string    `json:"currency"`
	Role               string    `json:"role"`
	OrgID              string    `json:"org_id"`
	ExpiresAt          time.Time `json:"expires_at"`
}

// ApprovalCredentialResult is the issuance response. It is an internal
// confirmation credential, not a CA certificate or electronic signature.
type ApprovalCredentialResult struct {
	Credential string                   `json:"credential"`
	Claims     ApprovalCredentialClaims `json:"claims"`
}

// TransferApprovalTaskInput reassigns one live task. The first version has no
// standing delegation: only a tenant administrator may perform a transfer, and
// every transfer is an append-only fact.
type TransferApprovalTaskInput struct {
	TaskID                 string
	ToUserID               string
	Reason                 string
	ExpectedTaskRowVersion int64
	IdempotencyKey         string
}

type TransferApprovalTaskResult struct {
	TransferID    string    `json:"transfer_id"`
	TaskID        string    `json:"task_id"`
	FromUserID    string    `json:"from_user_id"`
	ToUserID      string    `json:"to_user_id"`
	ActorID       string    `json:"actor_id"`
	RowVersion    int64     `json:"row_version"`
	TransferredAt time.Time `json:"transferred_at"`
}

type clmApprovalRevisionScope struct {
	RevisionNumber int
	Amount         string
	Currency       string
	SnapshotDigest string
}

func loadApprovalRevisionScope(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID string) (clmApprovalRevisionScope, error) {
	var scope clmApprovalRevisionScope
	err := tx.QueryRow(ctx, `SELECT revision.revision_number, revision.amount::text, revision.currency,
			seal.snapshot_digest
		FROM clm_contract_revisions revision
		JOIN clm_contract_version_seals seal
		  ON seal.tenant_id=revision.tenant_id AND seal.contract_id=revision.contract_id
		 AND seal.contract_revision_id=revision.contract_revision_id
		WHERE revision.tenant_id=$1 AND revision.contract_id=$2 AND revision.contract_revision_id=$3`,
		tenantID, contractID, revisionID).Scan(&scope.RevisionNumber, &scope.Amount, &scope.Currency,
		&scope.SnapshotDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmApprovalRevisionScope{}, fault.Conflict("该版本没有可核验的冻结封印")
	}
	return scope, err
}

// IssueApprovalCredential binds the current subject, role, organization, task,
// frozen revision and both row versions into a short-lived token. Every fact is
// resolved server-side; the request carries only the task id.
func (s *Store) IssueApprovalCredential(ctx context.Context, user accounts.User, taskID string) (ApprovalCredentialResult, error) {
	id, err := normalizeContractUUID(taskID)
	if err != nil {
		return ApprovalCredentialResult{}, err
	}
	if s.approvalKeyring == nil {
		return ApprovalCredentialResult{}, fault.New(503, "approval_credentials_disabled", "审批内部凭证未启用")
	}
	tx, current, err := s.beginContractLedgerRead(ctx, user)
	if err != nil {
		return ApprovalCredentialResult{}, err
	}
	defer rollbackCLM(tx)
	task, err := loadApprovalTask(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ApprovalCredentialResult{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), task.ContractID)
	if err != nil {
		return ApprovalCredentialResult{}, err
	}
	if !authorizeContract(record, current, access.Read) {
		return ApprovalCredentialResult{}, fault.NotFound("没有找到对应记录")
	}
	if !authorizeContract(record, current, access.Decide) {
		return ApprovalCredentialResult{}, fault.Forbidden("当前授权不能审批此合同")
	}
	if !accounts.CanDecide(current.Role) {
		return ApprovalCredentialResult{}, fault.Forbidden("当前角色不能签发审批凭证")
	}
	round, err := loadApprovalRound(ctx, tx, accounts.Tenant(current), task.ContractID, task.ApprovalRoundID)
	if err != nil {
		return ApprovalCredentialResult{}, err
	}
	if record.AuthorityMode != "native" || record.LifecycleState != "draft" || record.ValidityState != "active" {
		return ApprovalCredentialResult{}, fault.Conflict("合同当前状态不允许签发审批凭证")
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != round.ContractRevisionID {
		return ApprovalCredentialResult{}, fault.Conflict("审批轮次引用的不是当前冻结版本")
	}
	if round.State != "in_review" || task.State != "active" {
		return ApprovalCredentialResult{}, fault.Conflict("审批任务已结束，不能签发凭证")
	}
	assigned, err := approvalTaskAssignedTo(ctx, tx, accounts.Tenant(current), task, current.ID)
	if err != nil {
		return ApprovalCredentialResult{}, err
	}
	if !assigned {
		return ApprovalCredentialResult{}, fault.Forbidden("当前账号不是该会签节点的审批人")
	}
	if !task.Final && round.InitiatedBy == current.ID {
		return ApprovalCredentialResult{}, fault.Forbidden("发起人不能审批自己送审的独立会签节点")
	}
	bound, err := approvalRoleBindingCurrent(ctx, tx, accounts.Tenant(current), task.RoleCode, record.OwnerOrgID, current.ID)
	if err != nil {
		return ApprovalCredentialResult{}, err
	}
	if !bound {
		return ApprovalCredentialResult{}, fault.Forbidden("会签角色绑定已撤销，不能签发凭证")
	}
	scope, err := loadApprovalRevisionScope(ctx, tx, accounts.Tenant(current), task.ContractID, round.ContractRevisionID)
	if err != nil {
		return ApprovalCredentialResult{}, err
	}
	token, claims, err := s.approvalKeyring.Issue(approval.Claims{
		TenantID: accounts.Tenant(current), SubjectID: current.ID, Role: current.Role,
		OrgID: current.OrgID, TaskID: task.ApprovalTaskID, ContractID: task.ContractID,
		ApprovalRoundID: task.ApprovalRoundID, ContractRevisionID: round.ContractRevisionID,
		RevisionNumber: scope.RevisionNumber, SnapshotDigest: scope.SnapshotDigest,
		TaskRowVersion: task.RowVersion, ContractRowVersion: record.RowVersion,
		AmountLimit: scope.Amount, Currency: scope.Currency,
	})
	if err != nil {
		return ApprovalCredentialResult{}, fault.Forbidden("审批凭证签发失败")
	}
	if err := tx.Commit(ctx); err != nil {
		return ApprovalCredentialResult{}, err
	}
	return ApprovalCredentialResult{
		Credential: token,
		Claims: ApprovalCredentialClaims{
			TaskID: task.ApprovalTaskID, ContractID: task.ContractID,
			ApprovalRoundID: task.ApprovalRoundID, ContractRevisionID: round.ContractRevisionID,
			RevisionNumber: scope.RevisionNumber, SnapshotDigest: scope.SnapshotDigest,
			TaskRowVersion: task.RowVersion, ContractRowVersion: record.RowVersion,
			AmountLimit: scope.Amount, Currency: scope.Currency, Role: current.Role,
			OrgID: current.OrgID, ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
		},
	}, nil
}

// verifyApprovalCredential matches a signed token to the locked task, round,
// contract and frozen revision. Any mismatch is a 403: the browser-supplied
// identity and scope are never trusted.
func (s *Store) verifyApprovalCredential(ctx context.Context, tx pgx.Tx, current accounts.User,
	task clmApprovalTaskRecord, record clmContractRecord, round clmApprovalRoundRecord,
	revision clmApprovalRevisionScope, token string) (*approval.Claims, error) {
	if s.approvalKeyring == nil {
		return nil, nil
	}
	if strings.TrimSpace(token) == "" {
		return nil, fault.Forbidden("审批决定必须携带内部身份凭证")
	}
	claims, err := s.approvalKeyring.Verify(token)
	if err != nil {
		return nil, fault.Forbidden("审批身份凭证无效或已过期")
	}
	if claims.TenantID != accounts.Tenant(current) || claims.SubjectID != current.ID ||
		claims.Role != current.Role || claims.OrgID != current.OrgID ||
		claims.TaskID != task.ApprovalTaskID || claims.ContractID != task.ContractID ||
		claims.ApprovalRoundID != task.ApprovalRoundID ||
		claims.ContractRevisionID != round.ContractRevisionID ||
		claims.RevisionNumber != revision.RevisionNumber ||
		claims.SnapshotDigest != revision.SnapshotDigest ||
		claims.TaskRowVersion != task.RowVersion || claims.ContractRowVersion != record.RowVersion ||
		claims.AmountLimit != revision.Amount || claims.Currency != revision.Currency {
		return nil, fault.Forbidden("审批身份凭证与当前任务、版本或范围不匹配")
	}
	return &claims, nil
}

// loadApprovalRound is the non-locking form used by read-only commands such as
// credential issuance. The decision and transfer paths still lock the round.
func loadApprovalRound(ctx context.Context, tx pgx.Tx, tenantID, contractID, roundID string) (clmApprovalRoundRecord, error) {
	var round clmApprovalRoundRecord
	err := tx.QueryRow(ctx, `SELECT approval_round_id,contract_id,contract_revision_id,round_number,state,
			workflow_template_id,workflow_template_version,COALESCE(routing_digest,''),row_version,
			started_at,ended_at,initiated_by
		FROM clm_approval_rounds
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3`,
		tenantID, contractID, roundID).Scan(&round.ApprovalRoundID, &round.ContractID,
		&round.ContractRevisionID, &round.RoundNumber, &round.State, &round.WorkflowTemplateID,
		&round.WorkflowTemplateVersion, &round.RoutingDigest, &round.RowVersion,
		&round.StartedAt, &round.EndedAt, &round.InitiatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmApprovalRoundRecord{}, fault.NotFound("没有找到对应会签轮次")
	}
	return round, err
}

// TransferApprovalTask appends one reassignment fact and one assignment row for
// the new approver. The original task and frozen routing snapshot are not
// rewritten. A second transfer of the same task conflicts.
func (s *Store) TransferApprovalTask(ctx context.Context, user accounts.User, input TransferApprovalTaskInput) (TransferApprovalTaskResult, error) {
	taskID, err := normalizeContractUUID(input.TaskID)
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}
	toUserID := strings.TrimSpace(input.ToUserID)
	reason := strings.TrimSpace(input.Reason)
	if toUserID == "" || reason == "" || runeCount(reason) > 1000 {
		return TransferApprovalTaskResult{}, fault.Invalid("转交必须填写目标账号与理由")
	}
	if input.ExpectedTaskRowVersion < 1 {
		return TransferApprovalTaskResult{}, fault.Invalid("转交必须携带当前任务 row_version")
	}
	if err := validateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return TransferApprovalTaskResult{}, err
	}
	digest, err := clmRequestDigest(clmRouteApprovalTransfer, "POST", "/api/approval-tasks/"+taskID+"/transfers",
		input.ExpectedTaskRowVersion, struct {
			ToUserID string `json:"to_user_id"`
			Reason   string `json:"reason"`
		}{ToUserID: toUserID, Reason: reason})
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能转交审批任务", nil)
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}
	defer rollbackCLM(tx)
	if err := lockWriteReceipt(ctx, tx, current, clmRouteApprovalTransfer, input.IdempotencyKey); err != nil {
		return TransferApprovalTaskResult{}, err
	}
	receipt, found, err := replayCLMWriteReceipt(ctx, tx, current, clmRouteApprovalTransfer, input.IdempotencyKey, digest, "")
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}
	if found {
		var result TransferApprovalTaskResult
		if err := json.Unmarshal(receipt.Response, &result); err != nil {
			return TransferApprovalTaskResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return TransferApprovalTaskResult{}, err
		}
		return result, nil
	}
	task, err := loadApprovalTask(ctx, tx, accounts.Tenant(current), taskID)
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}
	if err := scopedContractLock(ctx, tx, task.ContractID); err != nil {
		return TransferApprovalTaskResult{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), task.ContractID)
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}
	if !authorizeContract(record, current, access.Manage) || current.Role != "admin" {
		return TransferApprovalTaskResult{}, fault.Forbidden("只有流程管理员可以转交审批任务")
	}
	round, err := loadApprovalRoundForUpdate(ctx, tx, accounts.Tenant(current), task.ContractID, task.ApprovalRoundID)
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}
	if round.State != "in_review" {
		return TransferApprovalTaskResult{}, fault.Conflict("审批任务已结束，不能转交")
	}
	task, err = lockApprovalTask(ctx, tx, accounts.Tenant(current), task.ContractID, task.ApprovalRoundID, taskID)
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}
	if task.State != "active" {
		return TransferApprovalTaskResult{}, fault.Conflict("审批任务已结束，不能转交")
	}
	if task.RowVersion != input.ExpectedTaskRowVersion {
		return TransferApprovalTaskResult{}, fault.Conflict("审批任务已变化，请刷新后重试")
	}
	target, ok := accounts.ByID(toUserID)
	if !ok || target.TenantID != accounts.Tenant(current) || !target.Enabled || target.ID == current.ID {
		return TransferApprovalTaskResult{}, fault.Invalid("目标账号不存在、未启用或不是本租户成员")
	}
	bound, err := approvalRoleBindingCurrent(ctx, tx, accounts.Tenant(current), task.RoleCode, record.OwnerOrgID, target.ID)
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}
	if !bound {
		return TransferApprovalTaskResult{}, fault.Invalid("目标账号没有该会签角色的有效绑定")
	}
	fromUserID, err := firstApprovalAssignee(ctx, tx, accounts.Tenant(current), task)
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}
	transferID, err := newContractUUID()
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}
	var transferredAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO clm_approval_task_transfers
		(tenant_id,contract_id,approval_round_id,transfer_id,approval_task_id,from_user_id,to_user_id,
		 to_org_id,reason,actor_id,from_task_row_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING transferred_at`,
		accounts.Tenant(current), task.ContractID, task.ApprovalRoundID, transferID, task.ApprovalTaskID,
		fromUserID, target.ID, target.OrgID, reason, current.ID, task.RowVersion).Scan(&transferredAt)
	if err != nil {
		return TransferApprovalTaskResult{}, clmUniqueViolation(err, "该审批任务已经转交")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_approval_task_assignments
		(tenant_id,contract_id,approval_round_id,approval_task_id,user_id,display_name,role_code,binding_org_scope)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'')
		ON CONFLICT DO NOTHING`, accounts.Tenant(current), task.ContractID, task.ApprovalRoundID,
		task.ApprovalTaskID, target.ID, target.DisplayName, task.RoleCode); err != nil {
		return TransferApprovalTaskResult{}, err
	}
	rowVersion, err := bumpContractRowVersion(ctx, tx, accounts.Tenant(current), record, "")
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}
	if err := insertCLMApprovalEvent(ctx, tx, accounts.Tenant(current), task.ContractID, rowVersion,
		clmEventApprovalTransferred, map[string]any{
			"contract_id": task.ContractID, "approval_round_id": task.ApprovalRoundID,
			"approval_task_id": task.ApprovalTaskID, "transfer_id": transferID,
			"from_user_id": fromUserID, "to_user_id": target.ID, "actor_id": current.ID,
			"reason_digest": sha256Bytes([]byte(reason)),
		}); err != nil {
		return TransferApprovalTaskResult{}, err
	}
	result := TransferApprovalTaskResult{
		TransferID: transferID, TaskID: task.ApprovalTaskID, FromUserID: fromUserID,
		ToUserID: target.ID, ActorID: current.ID, RowVersion: rowVersion, TransferredAt: transferredAt.UTC(),
	}
	response, err := json.Marshal(result)
	if err != nil {
		return TransferApprovalTaskResult{}, err
	}
	if err := insertCLMWriteReceipt(ctx, tx, accounts.Tenant(current), current.ID, clmRouteApprovalTransfer,
		input.IdempotencyKey, digest, task.ContractID, 200, response); err != nil {
		return TransferApprovalTaskResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TransferApprovalTaskResult{}, err
	}
	return result, nil
}

func firstApprovalAssignee(ctx context.Context, tx pgx.Tx, tenantID string, task clmApprovalTaskRecord) (string, error) {
	var userID string
	err := tx.QueryRow(ctx, `SELECT user_id FROM clm_approval_task_assignments
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id=$4
		ORDER BY user_id LIMIT 1`, tenantID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID).
		Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fault.Conflict("审批任务没有受理人，不能转交")
	}
	return userID, err
}

// latestApprovalTransfer returns the transfer target for a task. A transferred
// task can only be decided by the latest target; the original assignees remain
// in the append-only assignment table as history but lose the live right.
func latestApprovalTransfer(ctx context.Context, tx pgx.Tx, tenantID string, task clmApprovalTaskRecord) (string, bool, error) {
	var toUserID string
	err := tx.QueryRow(ctx, `SELECT to_user_id FROM clm_approval_task_transfers
		WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id=$4`,
		tenantID, task.ContractID, task.ApprovalRoundID, task.ApprovalTaskID).Scan(&toUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return toUserID, true, nil
}
