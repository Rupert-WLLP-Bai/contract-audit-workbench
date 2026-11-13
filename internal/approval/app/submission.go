package app

import (
	"context"
	"contractaudit/internal/accounts"
	"contractaudit/internal/approval/model"
	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

type SubmissionRepository interface {
	NextRoundNumber(context.Context, string, string) (int, error)
	InsertRound(context.Context, string, string, string, string, int, string, string, []byte, string, []byte, string) (int64, time.Time, error)
	InsertTask(context.Context, string, string, string, string, model.WorkflowNodeSnapshot, string) error
	InsertAssignment(context.Context, string, string, string, string, string, model.RoutingAssignee) error
	InsertMaterial(context.Context, string, string, string, string, string, string, string) error
}
type SubmissionFacts struct {
	RoundID     string
	RoundNumber int
	RowVersion  int64
	StartedAt   time.Time
	Tasks       []model.ApprovalTaskSummary
}

func CreateSubmissionFacts(ctx context.Context, repo SubmissionRepository, tenantID, contractID, revisionID, actorID string, routing model.RoutingSnapshot, routingJSON []byte, digest string, riskJSON []byte, materials []contract.FrozenVersionMaterial) (SubmissionFacts, error) {
	var out SubmissionFacts
	id, err := contract.NewContractUUID()
	if err != nil {
		return out, err
	}
	out.RoundID = id
	out.RoundNumber, err = repo.NextRoundNumber(ctx, tenantID, contractID)
	if err != nil {
		return out, err
	}
	out.RowVersion, out.StartedAt, err = repo.InsertRound(ctx, tenantID, contractID, id, revisionID, out.RoundNumber, routing.TemplateID, routing.TemplateVersion, routingJSON, digest, riskJSON, actorID)
	if err != nil {
		return out, err
	}
	out.Tasks = make([]model.ApprovalTaskSummary, 0, len(routing.Nodes))
	for i, node := range routing.Nodes {
		taskID, err := contract.NewContractUUID()
		if err != nil {
			return out, err
		}
		state := "pending"
		if i == 0 {
			state = "active"
		}
		if err = repo.InsertTask(ctx, tenantID, contractID, id, taskID, node, state); err != nil {
			return out, err
		}
		for _, assignee := range node.Assignees {
			if err = repo.InsertAssignment(ctx, tenantID, contractID, id, taskID, node.RoleCode, assignee); err != nil {
				return out, err
			}
		}
		for _, m := range materials {
			if err = repo.InsertMaterial(ctx, tenantID, contractID, id, taskID, revisionID, m.MaterialID, m.MaterialVersionID); err != nil {
				return out, err
			}
		}
		out.Tasks = append(out.Tasks, model.ApprovalTaskSummary{ApprovalTaskID: taskID, NodeCode: node.NodeCode, NodeOrder: node.NodeOrder, Title: node.Title, RoleCode: node.RoleCode, Final: node.Final, State: state, AssigneeIDs: model.NodeAssignees(node)})
	}
	return out, nil
}

const SubmitRoute = "clm.submit_approval"

type PreparedSubmission struct {
	Input                          model.SubmitContractApprovalInput
	ContractID, TemplateID, Digest string
}

func PrepareSubmission(input model.SubmitContractApprovalInput) (PreparedSubmission, error) {
	id, err := contract.NormalizeContractUUID(input.ContractID)
	if err != nil {
		return PreparedSubmission{}, err
	}
	if input.ExpectedRowVersion < 1 {
		return PreparedSubmission{}, fault.Invalid("送审必须携带当前合同 row_version")
	}
	if err = contract.ValidateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return PreparedSubmission{}, err
	}
	requested := strings.TrimSpace(input.TemplateID)
	if requested != "" && !model.ValidTemplateID(requested) {
		return PreparedSubmission{}, fault.Invalid("template_id 格式不正确")
	}
	digest, err := contract.RequestDigest(SubmitRoute, "POST", "/api/contracts/"+id+"/submissions", input.ExpectedRowVersion, struct {
		TemplateID string `json:"template_id,omitempty"`
	}{requested})
	return PreparedSubmission{Input: input, ContractID: id, TemplateID: requested, Digest: digest}, err
}

type SubmissionContractRepository interface {
	LockWriteReceipt(context.Context, string, string, string, string) error
	LoadWriteReceipt(context.Context, string, string, string, string) (contract.WriteReceipt, bool, error)
	InsertWriteReceipt(context.Context, string, string, string, string, string, string, int, []byte) error
	LockContract(context.Context, string) error
	LoadContract(context.Context, string, string) (contract.Aggregate, error)
	CASSubmission(context.Context, string, string, int64, string) (int64, error)
}
type SubmissionDirectory interface{ Users() []accounts.User }
type SubmissionAccess interface {
	CanRead(context.Context, contract.Aggregate) bool
	CanManage(contract.Aggregate) bool
}
type SubmissionFreeze interface {
	VerifyFreeze(context.Context, string, string, string) ([]contract.FrozenVersionMaterial, error)
}
type SubmissionEvents interface {
	AppendContractEvent(context.Context, string, string, string, int64, string, []byte) error
	EnqueueContractEvent(context.Context, string, string) error
}
type SubmissionRepositoryFull interface {
	SubmissionRepository
	HasActiveRound(context.Context, string, string) (bool, error)
}

func Submit(ctx context.Context, p PreparedSubmission, tenantID, actorID string, directory SubmissionDirectory, contracts SubmissionContractRepository, access SubmissionAccess, freeze SubmissionFreeze, repo SubmissionRepositoryFull, governance ActiveTemplateRepository, events SubmissionEvents) (model.SubmitContractApprovalResult, error) {
	var empty model.SubmitContractApprovalResult
	if err := contracts.LockWriteReceipt(ctx, tenantID, actorID, SubmitRoute, p.Input.IdempotencyKey); err != nil {
		return empty, err
	}
	receipt, found, err := contracts.LoadWriteReceipt(ctx, tenantID, actorID, SubmitRoute, p.Input.IdempotencyKey)
	if err != nil {
		return empty, err
	}
	if found {
		if receipt.RequestDigest != p.Digest {
			return empty, fault.Conflict("Idempotency-Key已用于不同请求载荷")
		}
		if receipt.ContractID != p.ContractID {
			return empty, fault.Conflict("Idempotency-Key已用于其他合同")
		}
		for i := 0; i < 2; i++ {
			record, err := contracts.LoadContract(ctx, tenantID, p.ContractID)
			if err != nil {
				return empty, err
			}
			if !access.CanRead(ctx, record) {
				return empty, fault.NotFound("没有找到对应记录")
			}
		}
		var result model.SubmitContractApprovalResult
		if err = json.Unmarshal(receipt.Response, &result); err != nil {
			return empty, err
		}
		return result, nil
	}
	if err = contracts.LockContract(ctx, p.ContractID); err != nil {
		return empty, err
	}
	record, err := contracts.LoadContract(ctx, tenantID, p.ContractID)
	if err != nil {
		return empty, err
	}
	if !access.CanRead(ctx, record) {
		return empty, fault.NotFound("没有找到对应记录")
	}
	if !access.CanManage(record) {
		return empty, fault.Forbidden("当前授权不能送审此合同")
	}
	if record.AuthorityMode != "native" {
		return empty, fault.Forbidden("外部权威合同不允许在本地发起会签")
	}
	if record.LifecycleState != "draft" || record.ValidityState != "active" {
		return empty, fault.Conflict("合同当前状态不允许送审")
	}
	if record.RowVersion != p.Input.ExpectedRowVersion {
		return empty, fault.Conflict("合同内容已被他人修改，请刷新后确认")
	}
	active, err := repo.HasActiveRound(ctx, tenantID, p.ContractID)
	if err != nil {
		return empty, err
	}
	if active {
		return empty, fault.Conflict("合同已有进行中的会签轮次")
	}
	if record.CurrentRevisionID == nil {
		return empty, fault.Conflict("请先冻结合同版本再送审")
	}
	revisionID := *record.CurrentRevisionID
	materials, err := freeze.VerifyFreeze(ctx, tenantID, p.ContractID, revisionID)
	if err != nil {
		return empty, err
	}
	template, err := LoadActiveTemplate(ctx, governance, tenantID, p.TemplateID)
	if err != nil {
		return empty, err
	}
	bindings, err := governance.LoadRoleBindings(ctx, tenantID)
	if err != nil {
		return empty, err
	}
	routing, err := model.ResolveApprovalRouting(record, actorID, template.Summary.TemplateID, template.Summary.TemplateVersion, template.Nodes, bindings, directory.Users(), time.Now().UTC())
	if err != nil {
		return empty, err
	}
	routingJSON, err := json.Marshal(routing)
	if err != nil {
		return empty, err
	}
	h := sha256.Sum256(routingJSON)
	routingDigest := hex.EncodeToString(h[:])
	riskJSON, err := json.Marshal(struct {
		Schema string `json:"schema"`
		Status string `json:"status"`
		Items  []any  `json:"items"`
	}{"clm-risk-disposition-snapshot/v1", "not_evaluated", []any{}})
	if err != nil {
		return empty, err
	}
	facts, err := CreateSubmissionFacts(ctx, repo, tenantID, p.ContractID, revisionID, actorID, routing, routingJSON, routingDigest, riskJSON, materials)
	if err != nil {
		return empty, err
	}
	version, err := contracts.CASSubmission(ctx, tenantID, p.ContractID, record.RowVersion, revisionID)
	if err != nil {
		return empty, err
	}
	eventID, err := contract.NewContractUUID()
	if err != nil {
		return empty, err
	}
	payload, err := json.Marshal(map[string]any{"contract_id": p.ContractID, "approval_round_id": facts.RoundID, "contract_revision_id": revisionID, "round_number": facts.RoundNumber, "workflow_template_id": template.Summary.TemplateID, "workflow_template_version": template.Summary.TemplateVersion, "routing_digest": routingDigest, "task_count": len(facts.Tasks), "initiated_by": actorID})
	if err != nil {
		return empty, err
	}
	if err = events.AppendContractEvent(ctx, tenantID, eventID, p.ContractID, version, "contract.approval_submitted.v1", payload); err != nil {
		return empty, err
	}
	if err = events.EnqueueContractEvent(ctx, tenantID, eventID); err != nil {
		return empty, err
	}
	result := model.SubmitContractApprovalResult{Round: model.ApprovalRoundSummary{ApprovalRoundID: facts.RoundID, ContractRevisionID: revisionID, RoundNumber: facts.RoundNumber, State: "in_review", WorkflowTemplateID: template.Summary.TemplateID, WorkflowTemplateVersion: template.Summary.TemplateVersion, RoutingDigest: routingDigest, RowVersion: facts.RowVersion, StartedAt: facts.StartedAt}, Tasks: facts.Tasks, ContractState: record.LifecycleState, WorkflowState: "in_review", RowVersion: version, AllowedCommands: []string{}}
	raw, err := json.Marshal(result)
	if err != nil {
		return empty, err
	}
	if err = contracts.InsertWriteReceipt(ctx, tenantID, actorID, SubmitRoute, p.Input.IdempotencyKey, p.Digest, p.ContractID, 201, raw); err != nil {
		return empty, err
	}
	return result, nil
}
