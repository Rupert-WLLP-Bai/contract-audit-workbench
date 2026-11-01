package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func approvalDecisionTestTask(final bool, order int) clmApprovalTaskRecord {
	return clmApprovalTaskRecord{
		ApprovalTaskID:  "c6052c81-8b05-49ce-89e9-f6e9c6403c20",
		ContractID:      "c6052c81-8b05-49ce-89e9-f6e9c6403c21",
		ApprovalRoundID: "c6052c81-8b05-49ce-89e9-f6e9c6403c22",
		NodeCode:        clmCanonicalApprovalNodeCodes[order-1], NodeOrder: order,
		Title: "节点", RoleCode: "role_legal", Final: final, State: "active", RowVersion: 1,
	}
}

func TestApprovalDecisionWorkflowStateIsSequential(t *testing.T) {
	cases := []struct {
		name     string
		task     clmApprovalTaskRecord
		hasNext  bool
		decision string
		want     string
		wantErr  bool
	}{
		{"approve advances", approvalDecisionTestTask(false, 2), true, clmDecisionApprove, "in_review", false},
		{"approve final completes", approvalDecisionTestTask(true, 7), false, clmDecisionApprove, "approved", false},
		{"return ends non-final round", approvalDecisionTestTask(false, 2), true, clmDecisionReturn, "returned", false},
		{"return ends final round", approvalDecisionTestTask(true, 7), false, clmDecisionReturn, "returned", false},
		{"missing next before final", approvalDecisionTestTask(false, 6), false, clmDecisionApprove, "", true},
		{"pending after final", approvalDecisionTestTask(true, 7), true, clmDecisionApprove, "", true},
	}
	for _, item := range cases {
		got, err := approvalDecisionWorkflowState(item.task, item.hasNext, item.decision)
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

func TestApprovalDecisionInputValidationRunsBeforeDatabase(t *testing.T) {
	validTaskID := "c6052c81-8b05-49ce-89e9-f6e9c6403c20"
	cases := []struct {
		name   string
		input  DecideApprovalTaskInput
		status int
		field  string
	}{
		{"invalid task id", DecideApprovalTaskInput{TaskID: "not-a-uuid", Decision: clmDecisionApprove,
			ExpectedRoundRowVersion: 1, IdempotencyKey: "decision-key-0001"}, 404, "没有找到"},
		{"unknown decision", DecideApprovalTaskInput{TaskID: validTaskID, Decision: "abstain",
			ExpectedRoundRowVersion: 1, IdempotencyKey: "decision-key-0001"}, 422, "decision"},
		{"return without reason", DecideApprovalTaskInput{TaskID: validTaskID, Decision: clmDecisionReturn,
			ExpectedRoundRowVersion: 1, IdempotencyKey: "decision-key-0001"}, 422, "理由"},
		{"comment too long", DecideApprovalTaskInput{TaskID: validTaskID, Decision: clmDecisionApprove,
			Comment: strings.Repeat("审", 2001), ExpectedRoundRowVersion: 1, IdempotencyKey: "decision-key-0001"}, 422, "2000"},
		{"missing round version", DecideApprovalTaskInput{TaskID: validTaskID, Decision: clmDecisionApprove,
			IdempotencyKey: "decision-key-0001"}, 422, "row_version"},
		{"missing idempotency", DecideApprovalTaskInput{TaskID: validTaskID, Decision: clmDecisionApprove,
			ExpectedRoundRowVersion: 1}, 422, "Idempotency-Key"},
	}
	empty := &Store{}
	for _, item := range cases {
		_, err := empty.DecideApprovalTask(context.Background(), accounts.User{}, item.input)
		var fe *fault.Error
		if !errors.As(err, &fe) || fe.Status != item.status || !strings.Contains(err.Error(), item.field) {
			t.Fatalf("%s: expected %d mentioning %q, got %v", item.name, item.status, item.field, err)
		}
	}
}

// An approve command is not allowed to infer a completion or skip a node. The
// pure transition test above pins the rules; this test pins the error class so
// a corrupt task skeleton surfaces as a conflict rather than a silent approval.
func TestApprovalDecisionCorruptSequenceIsConflict(t *testing.T) {
	_, err := approvalDecisionWorkflowState(approvalDecisionTestTask(false, 6), false, clmDecisionApprove)
	var fe *fault.Error
	if !errors.As(err, &fe) || fe.Status != 409 {
		t.Fatalf("expected conflict, got %v", err)
	}
}

// The database compatibility range is derived from the immutable numbered
// catalog. Later approval migrations may become head; the decision migration
// must remain a catalog member and the application must advertise the newest
// version it can migrate.
func TestApprovalMigrationsAreCatalogHead(t *testing.T) {
	catalog, err := migrationCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if schemaMaxCompatibleVersion != len(catalog) {
		t.Fatalf("schemaMaxCompatibleVersion=%d catalog=%d", schemaMaxCompatibleVersion, len(catalog))
	}
	head := catalog[len(catalog)-1]
	if head.version != schemaMaxCompatibleVersion {
		t.Fatalf("catalog head %d does not match advertised schema %d", head.version, schemaMaxCompatibleVersion)
	}
	for _, migration := range catalog {
		if migration.version == 24 && migration.name == "approval_decisions" {
			return
		}
	}
	t.Fatalf("approval decision migration missing from catalog head %+v", head)
}
