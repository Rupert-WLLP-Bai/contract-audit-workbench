package app

import (
	"context"
	"contractaudit/internal/accounts"
	"contractaudit/internal/approval"
	"contractaudit/internal/approval/model"
	contractapp "contractaudit/internal/contract/app"
	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
	"strings"
	"time"
)

type CredentialRepository interface {
	LoadApprovalTask(context.Context, string, string) (model.ApprovalTaskRecord, error)
	LoadApprovalRound(context.Context, string, string, string) (model.ApprovalRoundRecord, error)
	ApprovalTaskAssignedTo(context.Context, string, model.ApprovalTaskRecord, string) (bool, error)
	ApprovalRoleBindingCurrent(context.Context, string, string, string, string) (bool, error)
}
type CredentialContractRepository interface {
	LoadContract(context.Context, string, string) (contract.Aggregate, error)
	LoadApprovalRevisionScope(context.Context, string, string, string) (contractapp.ApprovalRevisionScope, error)
}
type CredentialAccess interface {
	CanReadDirect(contract.Aggregate) bool
	CanDecide(contract.Aggregate) bool
}

func IssueCredential(ctx context.Context, current accounts.User, id string, keyring *approval.Keyring, repo CredentialRepository, contracts CredentialContractRepository, access CredentialAccess) (model.ApprovalCredentialResult, error) {
	var empty model.ApprovalCredentialResult
	tenantID := accounts.Tenant(current)
	task, err := repo.LoadApprovalTask(ctx, tenantID, id)
	if err != nil {
		return empty, err
	}
	record, err := contracts.LoadContract(ctx, tenantID, task.ContractID)
	if err != nil {
		return empty, err
	}
	if !access.CanReadDirect(record) {
		return empty, fault.NotFound("没有找到对应记录")
	}
	if !access.CanDecide(record) {
		return empty, fault.Forbidden("当前授权不能审批此合同")
	}
	if !accounts.CanDecide(current.Role) {
		return empty, fault.Forbidden("当前角色不能签发审批凭证")
	}
	round, err := repo.LoadApprovalRound(ctx, tenantID, task.ContractID, task.ApprovalRoundID)
	if err != nil {
		return empty, err
	}
	if record.AuthorityMode != "native" || record.LifecycleState != "draft" || record.ValidityState != "active" {
		return empty, fault.Conflict("合同当前状态不允许签发审批凭证")
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != round.ContractRevisionID {
		return empty, fault.Conflict("审批轮次引用的不是当前冻结版本")
	}
	if round.State != "in_review" || task.State != "active" {
		return empty, fault.Conflict("审批任务已结束，不能签发凭证")
	}
	assigned, err := repo.ApprovalTaskAssignedTo(ctx, tenantID, task, current.ID)
	if err != nil {
		return empty, err
	}
	if !assigned {
		return empty, fault.Forbidden("当前账号不是该会签节点的审批人")
	}
	if !task.Final && round.InitiatedBy == current.ID {
		return empty, fault.Forbidden("发起人不能审批自己送审的独立会签节点")
	}
	bound, err := repo.ApprovalRoleBindingCurrent(ctx, tenantID, task.RoleCode, record.OwnerOrgID, current.ID)
	if err != nil {
		return empty, err
	}
	if !bound {
		return empty, fault.Forbidden("会签角色绑定已撤销，不能签发凭证")
	}
	scope, err := contracts.LoadApprovalRevisionScope(ctx, tenantID, task.ContractID, round.ContractRevisionID)
	if err != nil {
		return empty, err
	}
	token, claims, err := keyring.Issue(approval.Claims{TenantID: tenantID, SubjectID: current.ID, Role: current.Role, OrgID: current.OrgID, TaskID: task.ApprovalTaskID, ContractID: task.ContractID, ApprovalRoundID: task.ApprovalRoundID, ContractRevisionID: round.ContractRevisionID, RevisionNumber: scope.RevisionNumber, SnapshotDigest: scope.SnapshotDigest, TaskRowVersion: task.RowVersion, ContractRowVersion: record.RowVersion, AmountLimit: scope.Amount, Currency: scope.Currency})
	if err != nil {
		return empty, fault.Forbidden("审批凭证签发失败")
	}
	return model.ApprovalCredentialResult{Credential: token, Claims: model.ApprovalCredentialClaims{TaskID: task.ApprovalTaskID, ContractID: task.ContractID, ApprovalRoundID: task.ApprovalRoundID, ContractRevisionID: round.ContractRevisionID, RevisionNumber: scope.RevisionNumber, SnapshotDigest: scope.SnapshotDigest, TaskRowVersion: task.RowVersion, ContractRowVersion: record.RowVersion, AmountLimit: scope.Amount, Currency: scope.Currency, Role: current.Role, OrgID: current.OrgID, ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC()}}, nil
}
func VerifyCredential(keyring *approval.Keyring, current accounts.User, task model.ApprovalTaskRecord, record contract.Aggregate, round model.ApprovalRoundRecord, revision contractapp.ApprovalRevisionScope, token string) (*approval.Claims, error) {
	if keyring == nil {
		return nil, nil
	}
	if strings.TrimSpace(token) == "" {
		return nil, fault.Forbidden("审批决定必须携带内部身份凭证")
	}
	claims, err := keyring.Verify(token)
	if err != nil {
		return nil, fault.Forbidden("审批身份凭证无效或已过期")
	}
	if claims.TenantID != accounts.Tenant(current) || claims.SubjectID != current.ID || claims.Role != current.Role || claims.OrgID != current.OrgID || claims.TaskID != task.ApprovalTaskID || claims.ContractID != task.ContractID || claims.ApprovalRoundID != task.ApprovalRoundID || claims.ContractRevisionID != round.ContractRevisionID || claims.RevisionNumber != revision.RevisionNumber || claims.SnapshotDigest != revision.SnapshotDigest || claims.TaskRowVersion != task.RowVersion || claims.ContractRowVersion != record.RowVersion || claims.AmountLimit != revision.Amount || claims.Currency != revision.Currency {
		return nil, fault.Forbidden("审批身份凭证与当前任务、版本或范围不匹配")
	}
	return &claims, nil
}
