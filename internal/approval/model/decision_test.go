package model

import (
	"contractaudit/internal/fault"
	"errors"
	"testing"
)

func approvalDecisionTestTask(final bool, order int) ApprovalTaskRecord {
	return ApprovalTaskRecord{
		ApprovalTaskID:  "c6052c81-8b05-49ce-89e9-f6e9c6403c20",
		ContractID:      "c6052c81-8b05-49ce-89e9-f6e9c6403c21",
		ApprovalRoundID: "c6052c81-8b05-49ce-89e9-f6e9c6403c22",
		NodeCode:        canonicalApprovalNodeCodes[order-1], NodeOrder: order,
		Title: "节点", RoleCode: "role_legal", Final: final, State: "active", RowVersion: 1,
	}
}

func TestApprovalDecisionWorkflowStateIsSequential(t *testing.T) {
	cases := []struct {
		name     string
		task     ApprovalTaskRecord
		hasNext  bool
		decision string
		want     string
		wantErr  bool
	}{
		{"approve advances", approvalDecisionTestTask(false, 2), true, DecisionApprove, "in_review", false},
		{"approve final completes", approvalDecisionTestTask(true, 7), false, DecisionApprove, "approved", false},
		{"return ends non-final round", approvalDecisionTestTask(false, 2), true, DecisionReturn, "returned", false},
		{"return ends final round", approvalDecisionTestTask(true, 7), false, DecisionReturn, "returned", false},
		{"missing next before final", approvalDecisionTestTask(false, 6), false, DecisionApprove, "", true},
		{"pending after final", approvalDecisionTestTask(true, 7), true, DecisionApprove, "", true},
	}
	for _, item := range cases {
		got, err := DecisionWorkflowState(item.task, item.hasNext, item.decision)
		if item.wantErr {
			if err == nil {
				t.Fatalf("%s: expected refusal", item.name)
			}
			continue
		}
		if err != nil || got != item.want {
			t.Fatalf("%s: state=%q err=%v, want %q", item.name, got, err, item.want)
		}
	}
}

// An approve command is not allowed to infer a completion or skip a node. The
// pure transition test above pins the rules; this test pins the error class so
// a corrupt task skeleton surfaces as a conflict rather than a silent approval.
func TestApprovalDecisionCorruptSequenceIsConflict(t *testing.T) {
	_, err := DecisionWorkflowState(approvalDecisionTestTask(false, 6), false, DecisionApprove)
	var fe *fault.Error
	if !errors.As(err, &fe) || fe.Status != 409 {
		t.Fatalf("expected conflict, got %v", err)
	}
}
