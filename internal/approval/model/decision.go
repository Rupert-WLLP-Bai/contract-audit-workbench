package model

import (
	"contractaudit/internal/fault"
	"time"
)

const (
	DecisionApprove = "approve"
	DecisionReturn  = "return"
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

type ApprovalRoundRecord struct {
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

type ApprovalTaskRecord struct {
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

func DecisionWorkflowState(task ApprovalTaskRecord, hasNext bool, decision string) (string, error) {
	if decision == DecisionReturn {
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
