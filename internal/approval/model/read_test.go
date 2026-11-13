package model

import (
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

func faultStatus(err error) int {
	var value *fault.Error
	if errors.As(err, &value) {
		return value.Status
	}
	return 0
}

func TestApprovalInboxCursorBindsTenantActorAndView(t *testing.T) {
	admin, _ := accounts.ByID("demo-admin")
	reviewer, _ := accounts.ByID("demo-reviewer")
	otherTenant, _ := accounts.ByID("demo-tenant2-admin")
	key := time.Date(2026, 11, 1, 8, 30, 0, 0, time.UTC)
	encoded := EncodeInboxCursor(admin, approvalInboxViewTodo, key, "c6052c81-8b05-49ce-89e9-f6e9c6403c12")
	cursor, err := DecodeInboxCursor(encoded, admin, approvalInboxViewTodo)
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
		if _, err := DecodeInboxCursor(test.value, test.user, test.view); faultStatus(err) != 422 {
			t.Fatalf("%s reuse = %v; want 422", name, err)
		}
	}
	raw, _ := base64.RawURLEncoding.DecodeString(encoded)
	raw[0] ^= 0xff
	if _, err := DecodeInboxCursor(base64.RawURLEncoding.EncodeToString(raw), admin, approvalInboxViewTodo); faultStatus(err) != 422 {
		t.Fatalf("tampered cursor = %v; want 422", err)
	}
	if _, err := DecodeInboxCursor("not-a-cursor", admin, approvalInboxViewTodo); faultStatus(err) != 422 {
		t.Fatalf("malformed cursor = %v; want 422", err)
	}
}

func TestApprovalRoundCursorBindsContractAndActor(t *testing.T) {
	admin, _ := accounts.ByID("demo-admin")
	reviewer, _ := accounts.ByID("demo-reviewer")
	contractID := "c6052c81-8b05-49ce-89e9-f6e9c6403c10"
	encoded := EncodeRoundCursor(admin, contractID, 3)
	cursor, err := DecodeRoundCursor(encoded, admin, contractID)
	if err != nil || cursor == nil || cursor.LastRoundNum != 3 {
		t.Fatalf("decode own cursor: cursor=%+v err=%v", cursor, err)
	}
	if _, err := DecodeRoundCursor(encoded, reviewer, contractID); faultStatus(err) != 422 {
		t.Fatalf("other actor cursor = %v; want 422", err)
	}
	if _, err := DecodeRoundCursor(encoded, admin, "c6052c81-8b05-49ce-89e9-f6e9c6403c11"); faultStatus(err) != 422 {
		t.Fatalf("other contract cursor = %v; want 422", err)
	}
}

func TestApprovalInboxViewAndLimitValidation(t *testing.T) {
	for _, view := range []string{"", "todo", "done", "initiated"} {
		if _, err := NormalizeInboxView(view); err != nil {
			t.Fatalf("view %q rejected: %v", view, err)
		}
	}
	for _, view := range []string{"mine", "TODO", "all"} {
		if _, err := NormalizeInboxView(view); faultStatus(err) != 422 {
			t.Fatalf("view %q = %v; want 422", view, err)
		}
	}
	if limit, err := InboxLimit(0); err != nil || limit != approvalInboxDefaultLimit {
		t.Fatalf("default limit = %d err=%v", limit, err)
	}
	if _, err := InboxLimit(101); faultStatus(err) != 422 {
		t.Fatalf("oversized limit = %v; want 422", err)
	}
	if _, err := HistoryLimit(51); faultStatus(err) != 422 {
		t.Fatalf("oversized round limit = %v; want 422", err)
	}
}

func TestApprovalReadRiskAndNameStayExplicit(t *testing.T) {
	state, reason := RiskDisposition([]byte(`{"state":"unknown","reason":"risk_disposition_not_configured"}`))
	if state != "unknown" || reason != "risk_disposition_not_configured" {
		t.Fatalf("risk = %q/%q", state, reason)
	}
	if state, _ := RiskDisposition([]byte(`not-json`)); state != "unknown" {
		t.Fatalf("malformed risk state = %q", state)
	}
	name := ContractName([]byte(`{"contract":{"name":"专线采购合同"},"supplier":{"bank_account":"secret"}}`))
	if name != "专线采购合同" {
		t.Fatalf("name = %q", name)
	}
	if name := ContractName([]byte(`{"supplier":{"name":"仅供应商"}}`)); name != "未命名合同" {
		t.Fatalf("fallback name = %q", name)
	}
}

func TestApprovalInboxRowKeyIsViewSpecific(t *testing.T) {
	at := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	task := &ReadTaskRow{TaskID: "t", ActivatedAt: &at}
	decision := &ReadDecisionRow{DecisionID: "d", DecidedAt: at}
	round := RoundRead{ApprovalRoundID: "r", StartedAt: at}
	row := InboxCandidate{Task: task, Decision: decision, Round: round}
	if key, id := InboxKey(approvalInboxViewTodo, row); !key.Equal(at) || id != "t" {
		t.Fatalf("todo key = %v/%q", key, id)
	}
	if key, id := InboxKey(approvalInboxViewDone, row); !key.Equal(at) || id != "d" {
		t.Fatalf("done key = %v/%q", key, id)
	}
	if key, id := InboxKey(approvalInboxViewInitiated, row); !key.Equal(at) || id != "r" {
		t.Fatalf("initiated key = %v/%q", key, id)
	}
}
