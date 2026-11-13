package model

import (
	"contractaudit/internal/fault"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	clmWorkflowStartAction = "submit_contract"
	clmWorkflowExecution   = "sequential"
	clmApprovalNodeCount   = 7
)

var canonicalApprovalNodeCodes = []string{
	"department_head",
	"business_domain",
	"internal_control",
	"legal_counsel",
	"legal_officer",
	"business_leader",
	"handler_final_check",
}

var (
	clmTemplateIDPattern      = regexp.MustCompile(`^[a-z][a-z0-9._-]{2,63}$`)
	clmTemplateVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)
	clmRoleCodePattern        = regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)
)

// PublishWorkflowTemplateInput publishes one immutable template version and
// makes it the tenant's default for later submissions.
type PublishWorkflowTemplateInput struct {
	TemplateID                string                      `json:"template_id"`
	TemplateVersion           string                      `json:"template_version"`
	Name                      string                      `json:"name"`
	StartAction               string                      `json:"start_action"`
	StartActionIsApprovalNode bool                        `json:"start_action_is_approval_node"`
	Execution                 string                      `json:"execution"`
	Nodes                     []WorkflowTemplateNodeInput `json:"nodes"`
}

// WorkflowTemplateNodeInput is one ordered approval node in a template
// publication command. The array order is the sequential execution order.
type WorkflowTemplateNodeInput struct {
	NodeCode string `json:"node_code"`
	Title    string `json:"title"`
	RoleCode string `json:"role_code"`
	Final    bool   `json:"final"`
}

// WorkflowTemplateNode is the persisted shape returned to operators.
type WorkflowTemplateNode struct {
	NodeCode  string `json:"node_code"`
	NodeOrder int    `json:"node_order"`
	Title     string `json:"title"`
	RoleCode  string `json:"role_code"`
	Final     bool   `json:"final"`
}

// WorkflowTemplateSummary describes one published template version.
type WorkflowTemplateSummary struct {
	TemplateID                string                 `json:"template_id"`
	TemplateVersion           string                 `json:"template_version"`
	Name                      string                 `json:"name"`
	StartAction               string                 `json:"start_action"`
	StartActionIsApprovalNode bool                   `json:"start_action_is_approval_node"`
	Execution                 string                 `json:"execution"`
	Nodes                     []WorkflowTemplateNode `json:"nodes"`
	TemplateDigest            string                 `json:"template_digest"`
	PublishedBy               string                 `json:"published_by"`
	PublishedAt               time.Time              `json:"published_at"`
}

// WorkflowRoleBindingInput replaces the users bound to one role at one scope.
// An empty org_scope means tenant-wide; a non-empty value matches the contract
// owner organization exactly.
type WorkflowRoleBindingInput struct {
	OrgScope string   `json:"org_scope"`
	UserIDs  []string `json:"user_ids"`
}

// ReplaceWorkflowRoleInput is the full replacement command for one role.
type ReplaceWorkflowRoleInput struct {
	RoleCode string                     `json:"-"`
	Bindings []WorkflowRoleBindingInput `json:"bindings"`
}

// WorkflowRoleBindingSummary returns one scope and its resolved user IDs.
type WorkflowRoleBindingSummary struct {
	OrgScope string   `json:"org_scope"`
	UserIDs  []string `json:"user_ids"`
}

// WorkflowRoleSummary is the admin projection of one role's bindings.
type WorkflowRoleSummary struct {
	RoleCode   string                       `json:"role_code"`
	RowVersion int64                        `json:"row_version"`
	Bindings   []WorkflowRoleBindingSummary `json:"bindings"`
	UpdatedBy  string                       `json:"updated_by"`
	UpdatedAt  time.Time                    `json:"updated_at"`
}

// SubmitContractApprovalInput submits the current frozen revision for a
// sequential countersign round. ContractID, tenant, actor and organization come
// from the server, never from the payload.
type SubmitContractApprovalInput struct {
	ContractID         string `json:"-"`
	ExpectedRowVersion int64  `json:"expected_row_version"`
	TemplateID         string `json:"template_id,omitempty"`
	IdempotencyKey     string `json:"-"`
}

// ApprovalTaskSummary is one frozen routing node of a round. Assignee IDs are
// the candidates resolved from role bindings and the directory at submission.
type ApprovalTaskSummary struct {
	ApprovalTaskID string   `json:"approval_task_id"`
	NodeCode       string   `json:"node_code"`
	NodeOrder      int      `json:"node_order"`
	Title          string   `json:"title"`
	RoleCode       string   `json:"role_code"`
	Final          bool     `json:"final"`
	State          string   `json:"state"`
	AssigneeIDs    []string `json:"assignee_user_ids"`
}

// ApprovalRoundSummary is the immutable routing envelope plus live round state.
type ApprovalRoundSummary struct {
	ApprovalRoundID         string     `json:"approval_round_id"`
	ContractRevisionID      string     `json:"contract_revision_id"`
	RoundNumber             int        `json:"round_number"`
	State                   string     `json:"state"`
	WorkflowTemplateID      string     `json:"workflow_template_id"`
	WorkflowTemplateVersion string     `json:"workflow_template_version"`
	RoutingDigest           string     `json:"routing_digest"`
	RowVersion              int64      `json:"row_version"`
	StartedAt               time.Time  `json:"started_at"`
	EndedAt                 *time.Time `json:"ended_at"`
}

// SubmitContractApprovalResult is the idempotent submission response.
type SubmitContractApprovalResult struct {
	Round           ApprovalRoundSummary  `json:"round"`
	Tasks           []ApprovalTaskSummary `json:"tasks"`
	ContractState   string                `json:"contract_state"`
	WorkflowState   string                `json:"workflow_state"`
	RowVersion      int64                 `json:"row_version"`
	AllowedCommands []string              `json:"allowed_commands"`
}

// WorkflowNodeSnapshot is one node inside the frozen routing snapshot.
type WorkflowNodeSnapshot struct {
	NodeCode  string            `json:"node_code"`
	NodeOrder int               `json:"node_order"`
	Title     string            `json:"title"`
	RoleCode  string            `json:"role_code"`
	Final     bool              `json:"final"`
	Assignees []RoutingAssignee `json:"assignees"`
}

// RoutingAssignee records the directory identity and the binding scope that
// admitted it. A later rebinding cannot rewrite this row.
type RoutingAssignee struct {
	UserID          string `json:"user_id"`
	DisplayName     string `json:"display_name"`
	BindingOrgScope string `json:"binding_org_scope"`
}

// RoutingSnapshot is the complete routing fact frozen with the round.
type RoutingSnapshot struct {
	Schema          string                 `json:"schema"`
	TemplateID      string                 `json:"template_id"`
	TemplateVersion string                 `json:"template_version"`
	Execution       string                 `json:"execution"`
	ContractOrgID   string                 `json:"contract_org_id"`
	InitiatorID     string                 `json:"initiator_id"`
	Nodes           []WorkflowNodeSnapshot `json:"nodes"`
}

// NormalizeWorkflowTemplateInput trims every user-supplied label before
// validation and hashing, so whitespace cannot create two template versions
// that differ only cosmetically.
func NormalizeWorkflowTemplateInput(input PublishWorkflowTemplateInput) PublishWorkflowTemplateInput {
	input.TemplateID = strings.TrimSpace(input.TemplateID)
	input.TemplateVersion = strings.TrimSpace(input.TemplateVersion)
	input.Name = strings.TrimSpace(input.Name)
	input.StartAction = strings.TrimSpace(input.StartAction)
	input.Execution = strings.TrimSpace(input.Execution)
	nodes := make([]WorkflowTemplateNodeInput, len(input.Nodes))
	for i, node := range input.Nodes {
		nodes[i] = WorkflowTemplateNodeInput{
			NodeCode: strings.TrimSpace(node.NodeCode),
			Title:    strings.TrimSpace(node.Title),
			RoleCode: strings.TrimSpace(node.RoleCode),
			Final:    node.Final,
		}
	}
	input.Nodes = nodes
	return input
}

// ValidateWorkflowTemplate rejects any template that would change the audited
// seven-node sequential skeleton. Role codes stay operator-configurable, but
// must be unique inside one version so routing has one owner per node.
func ValidateWorkflowTemplate(input PublishWorkflowTemplateInput) error {
	if !clmTemplateIDPattern.MatchString(input.TemplateID) {
		return fault.Invalid("template_id 必须是小写字母开头的 3 到 64 位标识")
	}
	if !clmTemplateVersionPattern.MatchString(input.TemplateVersion) {
		return fault.Invalid("template_version 必须是 1 到 32 位字母、数字或 ._-")
	}
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 120 {
		return fault.Invalid("模板名称长度必须在 1 到 120 个字符之间")
	}
	if input.StartAction != clmWorkflowStartAction {
		return fault.Invalid("会签模板的起始动作必须是 submit_contract")
	}
	if input.StartActionIsApprovalNode {
		return fault.Invalid("送审动作不是会签节点，不能计入审批节点")
	}
	if input.Execution != clmWorkflowExecution {
		return fault.Invalid("本版只支持顺序会签（sequential）")
	}
	if len(input.Nodes) != clmApprovalNodeCount {
		return fault.Invalid("会签模板必须包含七个审批节点")
	}
	seenRoles := map[string]bool{}
	for i, node := range input.Nodes {
		if node.NodeCode != canonicalApprovalNodeCodes[i] {
			return fault.Invalid("会签节点顺序必须是既有七节点顺序")
		}
		if node.Title == "" || utf8.RuneCountInString(node.Title) > 120 {
			return fault.Invalid("节点名称长度必须在 1 到 120 个字符之间")
		}
		if !clmRoleCodePattern.MatchString(node.RoleCode) {
			return fault.Invalid("role_code 必须是小写字母开头的 3 到 64 位标识")
		}
		if seenRoles[node.RoleCode] {
			return fault.Invalid("同一会签模板中的 role_code 不能重复")
		}
		seenRoles[node.RoleCode] = true
		if node.Final != (i == len(input.Nodes)-1) {
			return fault.Invalid("只有最后一个会签节点可以标记为 final")
		}
	}
	return nil
}

func CanonicalApprovalNodeCodes() []string {
	return append([]string(nil), canonicalApprovalNodeCodes...)
}
func ValidTemplateID(s string) bool { return clmTemplateIDPattern.MatchString(s) }
func ValidRoleCode(s string) bool   { return clmRoleCodePattern.MatchString(s) }
