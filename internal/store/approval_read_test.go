package store

import (
	"encoding/base64"
	"testing"
	"time"

	"contractaudit/internal/accounts"
)

func TestApprovalInboxCursorBindsTenantActorAndView(t *testing.T) {
	admin, _ := accounts.ByID("demo-admin")
	reviewer, _ := accounts.ByID("demo-reviewer")
	otherTenant, _ := accounts.ByID("demo-tenant2-admin")
	key := time.Date(2026, 11, 1, 8, 30, 0, 0, time.UTC)
	encoded := encodeApprovalInboxCursor(admin, approvalInboxViewTodo, key, "c6052c81-8b05-49ce-89e9-f6e9c6403c12")
	cursor, err := decodeApprovalInboxCursor(encoded, admin, approvalInboxViewTodo)
	if err != nil || cursor == nil || !cursor.Key.Equal(key) ||
		cursor.ID != "c6052c81-8b05-49ce-89e9-f6e9c6403c12" {
		t.Fatalf("decode own cursor: cursor=%+v err=%v", cursor, err)
	}
	for name, test := range map[string]struct {
		value string
		user  accounts.User
		view  string
	}{
		"other actor":  {encoded, reviewer, approvalInboxViewTodo},
		"other tenant": {encoded, otherTenant, approvalInboxViewTodo},
		"other view":   {encoded, admin, approvalInboxViewDone},
	} {
		if _, err := decodeApprovalInboxCursor(test.value, test.user, test.view); faultStatus(err) != 422 {
			t.Fatalf("%s reuse = %v; want 422", name, err)
		}
	}
	raw, _ := base64.RawURLEncoding.DecodeString(encoded)
	raw[0] ^= 0xff
	if _, err := decodeApprovalInboxCursor(base64.RawURLEncoding.EncodeToString(raw), admin, approvalInboxViewTodo); faultStatus(err) != 422 {
		t.Fatalf("tampered cursor = %v; want 422", err)
	}
	if _, err := decodeApprovalInboxCursor("not-a-cursor", admin, approvalInboxViewTodo); faultStatus(err) != 422 {
		t.Fatalf("malformed cursor = %v; want 422", err)
	}
}

func TestApprovalRoundCursorBindsContractAndActor(t *testing.T) {
	admin, _ := accounts.ByID("demo-admin")
	reviewer, _ := accounts.ByID("demo-reviewer")
	contractID := "c6052c81-8b05-49ce-89e9-f6e9c6403c10"
	encoded := encodeApprovalRoundCursor(admin, contractID, 3)
	cursor, err := decodeApprovalRoundCursor(encoded, admin, contractID)
	if err != nil || cursor == nil || cursor.LastRoundNum != 3 {
		t.Fatalf("decode own cursor: cursor=%+v err=%v", cursor, err)
	}
	if _, err := decodeApprovalRoundCursor(encoded, reviewer, contractID); faultStatus(err) != 422 {
		t.Fatalf("other actor cursor = %v; want 422", err)
	}
	if _, err := decodeApprovalRoundCursor(encoded, admin, "c6052c81-8b05-49ce-89e9-f6e9c6403c11"); faultStatus(err) != 422 {
		t.Fatalf("other contract cursor = %v; want 422", err)
	}
}

func TestApprovalInboxViewAndLimitValidation(t *testing.T) {
	for _, view := range []string{"", "todo", "done", "initiated"} {
		if _, err := normalizeApprovalInboxView(view); err != nil {
			t.Fatalf("view %q rejected: %v", view, err)
		}
	}
	for _, view := range []string{"mine", "TODO", "all"} {
		if _, err := normalizeApprovalInboxView(view); faultStatus(err) != 422 {
			t.Fatalf("view %q = %v; want 422", view, err)
		}
	}
	if limit, err := approvalInboxLimit(0); err != nil || limit != approvalInboxDefaultLimit {
		t.Fatalf("default limit = %d err=%v", limit, err)
	}
	if _, err := approvalInboxLimit(101); faultStatus(err) != 422 {
		t.Fatalf("oversized limit = %v; want 422", err)
	}
	if _, err := approvalHistoryLimit(51); faultStatus(err) != 422 {
		t.Fatalf("oversized round limit = %v; want 422", err)
	}
}

func approvalReadTestRecord(createdBy string) clmContractRecord {
	return clmContractRecord{
		TenantID: accounts.LocalDemoTenant, ContractID: "c6052c81-8b05-49ce-89e9-f6e9c6403c10",
		OwnerOrgID: "demo-org", AuthorityMode: "native", LifecycleState: "draft",
		ValidityState: "active", CurrentRevisionID: new("c6052c81-8b05-49ce-89e9-f6e9c6403c11"),
		RowVersion: 2, CreatedBy: createdBy,
	}
}

func TestApprovalReadAllowedActionsFollowLiveAssignment(t *testing.T) {
	admin, _ := accounts.ByID("demo-admin")
	reviewer, _ := accounts.ByID("demo-reviewer")
	record := approvalReadTestRecord(admin.ID)
	round := approvalRoundRead{
		State: "in_review", InitiatedBy: admin.ID, ContractRevisionID: *record.CurrentRevisionID,
	}
	firstNode := approvalReadTaskRow{
		TaskID: "c6052c81-8b05-49ce-89e9-f6e9c6403c12", NodeOrder: 1,
		RoleCode: "admin", State: "active", AssigneeSubjectID: admin.ID,
	}
	if actions := approvalReadAllowedActions(record, round, &firstNode, admin); len(actions) != 0 {
		t.Fatalf("initiator received independent-node actions: %v", actions)
	}
	finalNode := firstNode
	finalNode.NodeOrder = 7
	if actions := approvalReadAllowedActions(record, round, &finalNode, admin); len(actions) != 2 {
		t.Fatalf("initiator final-node actions = %v", actions)
	}
	reviewerRecord := approvalReadTestRecord(reviewer.ID)
	firstNode.AssigneeSubjectID = reviewer.ID
	firstNode.RoleCode = reviewer.Role
	if actions := approvalReadAllowedActions(reviewerRecord, round, &firstNode, reviewer); len(actions) != 2 {
		t.Fatalf("reviewer actions = %v", actions)
	}
	if actions := approvalReadAllowedActions(reviewerRecord, round, &firstNode, admin); len(actions) != 0 {
		t.Fatalf("non-assignee actions = %v", actions)
	}
	closed := round
	closed.State = "returned"
	if actions := approvalReadAllowedActions(reviewerRecord, closed, &firstNode, reviewer); len(actions) != 0 {
		t.Fatalf("closed round actions = %v", actions)
	}
	imported := reviewerRecord
	imported.AuthorityMode = "external_observed"
	if actions := approvalReadAllowedActions(imported, round, &firstNode, reviewer); len(actions) != 0 {
		t.Fatalf("imported contract actions = %v", actions)
	}
}

func TestApprovalReadRiskAndNameStayExplicit(t *testing.T) {
	state, reason := approvalRiskDisposition([]byte(`{"state":"unknown","reason":"risk_disposition_not_configured"}`))
	if state != "unknown" || reason != "risk_disposition_not_configured" {
		t.Fatalf("risk = %q/%q", state, reason)
	}
	if state, _ := approvalRiskDisposition([]byte(`not-json`)); state != "unknown" {
		t.Fatalf("malformed risk state = %q", state)
	}
	name := approvalContractName([]byte(`{"contract":{"name":"专线采购合同"},"supplier":{"bank_account":"secret"}}`))
	if name != "专线采购合同" {
		t.Fatalf("name = %q", name)
	}
	if name := approvalContractName([]byte(`{"supplier":{"name":"仅供应商"}}`)); name != "未命名合同" {
		t.Fatalf("fallback name = %q", name)
	}
}

func TestApprovalInboxRowKeyIsViewSpecific(t *testing.T) {
	at := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	task := &approvalReadTaskRow{TaskID: "t", ActivatedAt: &at}
	decision := &approvalReadDecisionRow{DecisionID: "d", DecidedAt: at}
	round := approvalRoundRead{ApprovalRoundID: "r", StartedAt: at}
	row := approvalInboxRow{task: task, decision: decision, round: round}
	if key, id := approvalRowKey(approvalInboxViewTodo, row); !key.Equal(at) || id != "t" {
		t.Fatalf("todo key = %v/%q", key, id)
	}
	if key, id := approvalRowKey(approvalInboxViewDone, row); !key.Equal(at) || id != "d" {
		t.Fatalf("done key = %v/%q", key, id)
	}
	if key, id := approvalRowKey(approvalInboxViewInitiated, row); !key.Equal(at) || id != "r" {
		t.Fatalf("initiated key = %v/%q", key, id)
	}
}
