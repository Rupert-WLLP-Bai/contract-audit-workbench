package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

type workbenchBoundaryActors struct {
	owner, clerk, reviewer, viewer accounts.User
	otherOrg, foreign, disabled    accounts.User
}

func workbenchBoundaryDirectory(t *testing.T) workbenchBoundaryActors {
	t.Helper()
	lookup := func(id string) accounts.User {
		user, ok := accounts.ByID(id)
		if !ok {
			t.Fatalf("missing test actor %q", id)
		}
		return user
	}
	actors := workbenchBoundaryActors{
		owner: lookup("demo-admin"), clerk: lookup("demo-clerk"),
		reviewer: lookup("demo-reviewer"), viewer: lookup("demo-viewer"),
	}
	users := accounts.List()
	for i := range users {
		users[i].Issuer, users[i].Subject = "https://workbench-directory.invalid", users[i].ID
		role, scope := "case_owner", "self"
		if users[i].Role == "reviewer" || users[i].Role == "viewer" {
			role, scope = users[i].Role, "tenant"
		}
		users[i].Grants = []access.Grant{{Role: role, Scope: scope}}
	}
	for _, spec := range []struct {
		id, tenant, org string
		enabled         bool
	}{
		{"workbench-other-org", actors.owner.TenantID, "other-branch", true},
		{"workbench-foreign", "workbench-other-tenant", "foreign-branch", true},
		{"workbench-disabled", actors.owner.TenantID, actors.owner.OrgID, false},
	} {
		users = append(users, accounts.User{
			ID: spec.id, DisplayName: spec.id, Role: "clerk", TenantID: spec.tenant, OrgID: spec.org,
			Enabled: spec.enabled, Issuer: "https://workbench-directory.invalid", Subject: spec.id,
			Grants: []access.Grant{{Role: "case_owner", Scope: "self"}},
		})
	}
	body, err := json.Marshal(map[string]any{"users": users})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "directory.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := accounts.ConfigureDirectory(""); err != nil {
			t.Errorf("restore demo directory: %v", err)
		}
	})
	actors.otherOrg = lookup("workbench-other-org")
	actors.foreign = lookup("workbench-foreign")
	actors.disabled = users[len(users)-1]
	return actors
}

func workbenchBoundaryCase(t *testing.T, st *Store, user accounts.User, title string) string {
	t.Helper()
	id, err := st.CreateCase(user, title, "procurement_ict")
	if err != nil {
		t.Fatalf("create %s: %v", title, err)
	}
	return id
}

func workbenchBoundaryState(t *testing.T, st *Store, user accounts.User) state {
	t.Helper()
	loaded, err := st.snapshotAs(user)
	if err != nil {
		t.Fatalf("load persisted workbench: %v", err)
	}
	return loaded
}

func workbenchBoundaryFault(t *testing.T, err error, status int, code string) {
	t.Helper()
	var apiErr *fault.Error
	if !errors.As(err, &apiErr) || apiErr.Status != status || apiErr.Code != code {
		t.Fatalf("error = %v, want %d %s", err, status, code)
	}
}

func workbenchBoundaryUnchanged(t *testing.T, st *Store, user accounts.User, before state) {
	t.Helper()
	after := workbenchBoundaryState(t, st, user)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected operation changed persisted business data or appended activity/audit")
	}
}

func TestWorkbenchProfileTenantNumberOwnershipAndVersionBoundaries(t *testing.T) {
	st, _ := openExecutionStore(t)
	actors := workbenchBoundaryDirectory(t)
	caseID := workbenchBoundaryCase(t, st, actors.owner, "资料所属案件")
	otherID := workbenchBoundaryCase(t, st, actors.otherOrg, "同租户其他部门案件")
	foreignID := workbenchBoundaryCase(t, st, actors.foreign, "其他租户案件")
	if err := st.AddMember(actors.owner, caseID, actors.reviewer.ID); err != nil {
		t.Fatal(err)
	}
	due := " 2026-11-01T08:30:00+08:00 "
	input := CaseProfileInput{
		Number: " WB-Shared-42 ", Customer: " 采购方 ", Supplier: " 服务商 ",
		ContractCategory: " 专线服务 ", OwnerID: " " + actors.reviewer.ID + " ", DueAt: &due,
	}
	profile, err := st.UpdateCaseProfile(actors.owner, " "+caseID+" ", input)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Number != "WB-Shared-42" || profile.Customer != "采购方" || profile.Supplier != "服务商" ||
		profile.ContractCategory != "专线服务" || profile.OwnerID != actors.reviewer.ID || profile.Version != 1 ||
		profile.DueAt == nil || *profile.DueAt != "2026-11-01T00:30:00Z" {
		t.Fatalf("profile did not normalize and bind its owner: %+v", profile)
	}
	before := workbenchBoundaryState(t, st, actors.owner)
	if len(before.Profiles) != 1 || !reflect.DeepEqual(before.Profiles[0].Profile, profile) ||
		before.Profiles[0].TenantID != actors.owner.TenantID || before.Profiles[0].OrgID != actors.owner.OrgID {
		t.Fatalf("profile persisted with wrong contents or scope: %+v", before.Profiles)
	}
	detail, err := st.GetCase(actors.owner, caseID)
	if err != nil || detail.Case.OwnerID != actors.owner.ID {
		t.Fatalf("profile responsibility changed case authorization ownership: detail=%+v err=%v", detail, err)
	}

	duplicate := input
	duplicate.Number, duplicate.OwnerID = "wb-shared-42", actors.otherOrg.ID
	_, err = st.UpdateCaseProfile(actors.otherOrg, otherID, duplicate)
	workbenchBoundaryFault(t, err, 409, "conflict")
	workbenchBoundaryUnchanged(t, st, actors.owner, before)

	foreign := duplicate
	foreign.OwnerID = actors.foreign.ID
	if _, err := st.UpdateCaseProfile(actors.foreign, foreignID, foreign); err != nil {
		t.Fatalf("same number must remain available in another tenant: %v", err)
	}
	before = workbenchBoundaryState(t, st, actors.owner)
	if len(before.Profiles) != 2 {
		t.Fatalf("tenant-local number allocation lost a profile: %+v", before.Profiles)
	}

	for _, tc := range []struct {
		name   string
		actor  accounts.User
		change func(*CaseProfileInput)
		status int
		code   string
	}{
		{"stale create", actors.owner, func(in *CaseProfileInput) { in.ExpectedVersion = 0; in.Customer = "覆盖旧客户" }, 409, "conflict"},
		{"future version", actors.owner, func(in *CaseProfileInput) { in.ExpectedVersion = 9 }, 409, "conflict"},
		{"nonmember owner", actors.owner, func(in *CaseProfileInput) { in.OwnerID = actors.clerk.ID }, 422, "invalid"},
		{"disabled owner", actors.owner, func(in *CaseProfileInput) { in.OwnerID = actors.disabled.ID }, 422, "invalid"},
		{"foreign owner", actors.owner, func(in *CaseProfileInput) { in.OwnerID = actors.foreign.ID }, 422, "invalid"},
		{"reader cannot manage", actors.reviewer, func(in *CaseProfileInput) {}, 403, "forbidden"},
		{"nonmember cannot read", actors.clerk, func(in *CaseProfileInput) {}, 404, "not_found"},
		{"invalid due time", actors.owner, func(in *CaseProfileInput) { in.DueAt = stringPointer("next week") }, 422, "invalid"},
		{"negative version", actors.owner, func(in *CaseProfileInput) { in.ExpectedVersion = -1 }, 422, "invalid"},
		{"missing customer", actors.owner, func(in *CaseProfileInput) { in.Customer = " " }, 422, "invalid"},
		{"oversized number", actors.owner, func(in *CaseProfileInput) { in.Number = strings.Repeat("案", 65) }, 422, "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := input
			candidate.ExpectedVersion = 1
			tc.change(&candidate)
			_, err := st.UpdateCaseProfile(tc.actor, caseID, candidate)
			workbenchBoundaryFault(t, err, tc.status, tc.code)
			workbenchBoundaryUnchanged(t, st, actors.owner, before)
		})
	}
	missing := input
	missing.OwnerID, missing.ExpectedVersion = actors.otherOrg.ID, 1
	_, err = st.UpdateCaseProfile(actors.otherOrg, otherID, missing)
	workbenchBoundaryFault(t, err, 409, "conflict")
	workbenchBoundaryUnchanged(t, st, actors.owner, before)

	input.ExpectedVersion, input.Customer, input.DueAt = 1, "变更后的采购方", stringPointer(" \t ")
	updated, err := st.UpdateCaseProfile(actors.owner, caseID, input)
	if err != nil {
		t.Fatal(err)
	}
	loaded := workbenchBoundaryState(t, st, actors.owner)
	if updated.Version != 2 || updated.DueAt != nil || len(loaded.Profiles) != 2 ||
		!reflect.DeepEqual(loaded.Profiles[0].Profile, updated) || loaded.Profiles[0].Profile.Customer != "变更后的采购方" {
		t.Fatalf("successful versioned profile update not persisted: %+v", loaded.Profiles)
	}
}

func TestWorkbenchTaskAuthorizationReferencesAndOptimisticUpdates(t *testing.T) {
	st, _ := openExecutionStore(t)
	actors := workbenchBoundaryDirectory(t)
	caseID := workbenchBoundaryCase(t, st, actors.owner, "任务归属案件")
	otherID := workbenchBoundaryCase(t, st, actors.owner, "其他任务案件")
	for _, member := range []accounts.User{actors.reviewer, actors.viewer} {
		if err := st.AddMember(actors.owner, caseID, member.ID); err != nil {
			t.Fatal(err)
		}
	}
	workbenchBoundaryRun(t, st, actors.owner, caseID, "task-run", "completed", "2026-10-01T00:00:00Z",
		Attempt{ID: "task-attempt", Ordinal: 1, State: "completed"})
	workbenchBoundaryRun(t, st, actors.owner, otherID, "other-task-run", "completed", "2026-10-01T00:00:00Z",
		Attempt{ID: "other-task-attempt", Ordinal: 1, State: "completed"})
	workbenchBoundaryFinding(t, st, actors.owner, caseID, "task-run", "task-attempt", "task-finding", stringPointer("high"))
	workbenchBoundaryFinding(t, st, actors.owner, otherID, "other-task-run", "other-task-attempt", "foreign-case-finding", nil)
	input := WorkbenchTaskInput{Title: " 复核付款条款 ", Kind: " review ", AssigneeID: " " + actors.reviewer.ID + " ",
		Status: " open ", Priority: " high ", DueAt: stringPointer(" 2026-11-02T09:00:00+08:00 "), FindingID: stringPointer(" task-finding ")}
	created, err := st.CreateWorkbenchTask(actors.owner, " "+caseID+" ", input)
	if err != nil {
		t.Fatal(err)
	}
	if created.Title != "复核付款条款" || created.Kind != "review" || created.Status != "open" || created.Priority != "high" ||
		created.AssigneeID != actors.reviewer.ID || created.Version != 1 || created.CreatedBy == nil || *created.CreatedBy != actors.owner.ID ||
		created.DueAt == nil || *created.DueAt != "2026-11-02T01:00:00Z" || created.FindingID == nil || *created.FindingID != "task-finding" {
		t.Fatalf("task normalization or identity is incorrect: %+v", created)
	}
	before := workbenchBoundaryState(t, st, actors.owner)
	if len(before.Tasks) != 1 || !reflect.DeepEqual(before.Tasks[0].Task, created) {
		t.Fatalf("created task not persisted: %+v", before.Tasks)
	}
	for _, tc := range []struct {
		name   string
		actor  accounts.User
		change func(*WorkbenchTaskInput)
		status int
		code   string
	}{
		{"nonmember actor", actors.clerk, func(in *WorkbenchTaskInput) {}, 404, "not_found"},
		{"reviewer actor", actors.reviewer, func(in *WorkbenchTaskInput) {}, 403, "forbidden"},
		{"viewer actor", actors.viewer, func(in *WorkbenchTaskInput) {}, 403, "forbidden"},
		{"disabled actor", actors.disabled, func(in *WorkbenchTaskInput) {}, 401, "unauthenticated"},
		{"nonmember assignee", actors.owner, func(in *WorkbenchTaskInput) { in.AssigneeID = actors.clerk.ID }, 422, "invalid"},
		{"disabled assignee", actors.owner, func(in *WorkbenchTaskInput) { in.AssigneeID = actors.disabled.ID }, 422, "invalid"},
		{"foreign assignee", actors.owner, func(in *WorkbenchTaskInput) { in.AssigneeID = actors.foreign.ID }, 422, "invalid"},
		{"missing finding", actors.owner, func(in *WorkbenchTaskInput) { in.FindingID = stringPointer("missing-finding") }, 422, "invalid"},
		{"finding from other case", actors.owner, func(in *WorkbenchTaskInput) { in.FindingID = stringPointer("foreign-case-finding") }, 422, "invalid"},
		{"invalid title", actors.owner, func(in *WorkbenchTaskInput) { in.Title = " " }, 422, "invalid"},
		{"invalid kind", actors.owner, func(in *WorkbenchTaskInput) { in.Kind = "upload" }, 422, "invalid"},
		{"invalid status", actors.owner, func(in *WorkbenchTaskInput) { in.Status = "cancelled" }, 422, "invalid"},
		{"invalid priority", actors.owner, func(in *WorkbenchTaskInput) { in.Priority = "urgent" }, 422, "invalid"},
		{"invalid due time", actors.owner, func(in *WorkbenchTaskInput) { in.DueAt = stringPointer("tomorrow") }, 422, "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := input
			tc.change(&candidate)
			_, err := st.CreateWorkbenchTask(tc.actor, caseID, candidate)
			workbenchBoundaryFault(t, err, tc.status, tc.code)
			workbenchBoundaryUnchanged(t, st, actors.owner, before)
			candidate.ExpectedVersion = 1
			_, err = st.UpdateWorkbenchTask(tc.actor, caseID, created.ID, candidate)
			workbenchBoundaryFault(t, err, tc.status, tc.code)
			workbenchBoundaryUnchanged(t, st, actors.owner, before)
		})
	}
	input.ExpectedVersion = 1
	_, err = st.CreateWorkbenchTask(actors.owner, caseID, input)
	workbenchBoundaryFault(t, err, 422, "invalid")
	for _, tc := range []struct {
		caseID, taskID string
		version        int
		status         int
		code           string
	}{
		{caseID, created.ID, 0, 422, "invalid"},
		{caseID, " ", 1, 422, "invalid"},
		{caseID, "missing-task", 1, 404, "not_found"},
		{otherID, created.ID, 1, 404, "not_found"},
		{caseID, created.ID, 7, 409, "conflict"},
	} {
		candidate := input
		candidate.ExpectedVersion, candidate.Title = tc.version, "不得覆盖原任务"
		_, err := st.UpdateWorkbenchTask(actors.owner, tc.caseID, tc.taskID, candidate)
		workbenchBoundaryFault(t, err, tc.status, tc.code)
		workbenchBoundaryUnchanged(t, st, actors.owner, before)
	}
	for i, step := range []struct{ status, kind, priority string }{
		{"in_progress", "decision", "medium"}, {"completed", "remediation", "low"}, {"open", "general", "high"},
	} {
		input.ExpectedVersion, input.Status, input.Kind, input.Priority = i+1, step.status, step.kind, step.priority
		input.DueAt, input.FindingID = stringPointer(" "), stringPointer(" \t ")
		updated, err := st.UpdateWorkbenchTask(actors.owner, caseID, " "+created.ID+" ", input)
		if err != nil {
			t.Fatal(err)
		}
		loaded := workbenchBoundaryState(t, st, actors.owner)
		if len(loaded.Tasks) != 1 || !reflect.DeepEqual(loaded.Tasks[0].Task, updated) || updated.Version != i+2 ||
			updated.Status != step.status || updated.Kind != step.kind || updated.Priority != step.priority || updated.DueAt != nil || updated.FindingID != nil ||
			updated.CreatedAt != created.CreatedAt || updated.CreatedBy == nil || *updated.CreatedBy != actors.owner.ID {
			t.Fatalf("versioned task update lost fields or immutable provenance: %+v", loaded.Tasks)
		}
		stale := input
		stale.Title, stale.Status = "过期版本覆盖", "completed"
		_, err = st.UpdateWorkbenchTask(actors.owner, caseID, created.ID, stale)
		workbenchBoundaryFault(t, err, 409, "conflict")
		workbenchBoundaryUnchanged(t, st, actors.owner, loaded)
	}
}

func workbenchBoundaryGrant(t *testing.T, userID, caseID string) {
	t.Helper()
	users := accounts.List()
	found := false
	for i := range users {
		if users[i].ID == userID {
			users[i].Grants = append(users[i].Grants, access.Grant{Role: "case_owner", Scope: "case", CaseID: caseID})
			found = true
		}
	}
	if !found {
		t.Fatalf("cannot grant case access to missing actor %q", userID)
	}
	body, err := json.Marshal(map[string]any{"users": users})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "granted-directory.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
}

func TestWorkbenchRelationsRequireBothVisibleEndpointsAndRejectReverseDuplicates(t *testing.T) {
	st, _ := openExecutionStore(t)
	actors := workbenchBoundaryDirectory(t)
	left := workbenchBoundaryCase(t, st, actors.owner, "关联起点")
	right := workbenchBoundaryCase(t, st, actors.owner, "关联终点")
	private := workbenchBoundaryCase(t, st, actors.clerk, "不可见案件")
	foreign := workbenchBoundaryCase(t, st, actors.foreign, "跨租户案件")
	before := workbenchBoundaryState(t, st, actors.owner)
	for _, tc := range []struct {
		name   string
		actor  accounts.User
		source string
		input  CaseRelationInput
		status int
		code   string
	}{
		{"unauthorized source", actors.clerk, left, CaseRelationInput{RelatedCaseID: private, Reason: "同一项目"}, 404, "not_found"},
		{"unauthorized target", actors.owner, left, CaseRelationInput{RelatedCaseID: private, Reason: "同一项目"}, 404, "not_found"},
		{"foreign target", actors.owner, left, CaseRelationInput{RelatedCaseID: foreign, Reason: "跨租户项目"}, 404, "not_found"},
		{"foreign source", actors.owner, foreign, CaseRelationInput{RelatedCaseID: left, Reason: "跨租户项目"}, 404, "not_found"},
		{"reader cannot manage", actors.viewer, left, CaseRelationInput{RelatedCaseID: right, Reason: "同一项目"}, 403, "forbidden"},
		{"self relation", actors.owner, left, CaseRelationInput{RelatedCaseID: left, Reason: "自关联"}, 422, "invalid"},
		{"blank reason", actors.owner, left, CaseRelationInput{RelatedCaseID: right, Reason: " "}, 422, "invalid"},
		{"missing target", actors.owner, left, CaseRelationInput{RelatedCaseID: "missing-case", Reason: "同一项目"}, 404, "not_found"},
		{"disabled actor", actors.disabled, left, CaseRelationInput{RelatedCaseID: right, Reason: "同一项目"}, 401, "unauthenticated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := st.CreateCaseRelation(tc.actor, tc.source, tc.input)
			workbenchBoundaryFault(t, err, tc.status, tc.code)
			workbenchBoundaryUnchanged(t, st, actors.owner, before)
		})
	}
	relation, err := st.CreateCaseRelation(actors.owner, " "+left+" ", CaseRelationInput{RelatedCaseID: " " + right + " ", Reason: " 同一采购项目 "})
	if err != nil {
		t.Fatal(err)
	}
	before = workbenchBoundaryState(t, st, actors.owner)
	if len(before.Relations) != 1 || !reflect.DeepEqual(before.Relations[0].Relation, relation) ||
		relation.CaseID != left || relation.RelatedCaseID != right || relation.Reason != "同一采购项目" || relation.CreatedBy != actors.owner.ID {
		t.Fatalf("relation was not persisted with both endpoints: %+v", before.Relations)
	}
	for _, pair := range [][2]string{{left, right}, {right, left}} {
		_, err := st.CreateCaseRelation(actors.owner, pair[0], CaseRelationInput{RelatedCaseID: pair[1], Reason: "重复项目"})
		workbenchBoundaryFault(t, err, 409, "conflict")
		workbenchBoundaryUnchanged(t, st, actors.owner, before)
	}
	for _, tc := range []struct {
		actor  accounts.User
		source string
		id     string
		status int
		code   string
	}{
		{actors.owner, right, relation.ID, 404, "not_found"},
		{actors.owner, left, "missing-relation", 404, "not_found"},
		{actors.owner, left, " ", 422, "invalid"},
		{actors.owner, " ", relation.ID, 422, "invalid"},
		{actors.clerk, left, relation.ID, 404, "not_found"},
		{actors.viewer, left, relation.ID, 403, "forbidden"},
		{actors.disabled, left, relation.ID, 401, "unauthenticated"},
	} {
		err := st.DeleteCaseRelation(tc.actor, tc.source, tc.id)
		workbenchBoundaryFault(t, err, tc.status, tc.code)
		workbenchBoundaryUnchanged(t, st, actors.owner, before)
	}

	workbenchBoundaryGrant(t, actors.clerk.ID, left)
	clerkView, err := st.WorkbenchSnapshot(context.Background(), actors.clerk, "week", WorkbenchModel{})
	if err != nil {
		t.Fatal(err)
	}
	if len(clerkView.Relations) != 0 {
		t.Fatalf("relation disclosed an unreadable target: %+v", clerkView.Relations)
	}
	err = st.DeleteCaseRelation(actors.clerk, left, relation.ID)
	workbenchBoundaryFault(t, err, 404, "not_found")
	workbenchBoundaryUnchanged(t, st, actors.owner, before)
	workbenchBoundaryGrant(t, actors.clerk.ID, right)
	clerkView, err = st.WorkbenchSnapshot(context.Background(), actors.clerk, "week", WorkbenchModel{})
	if err != nil || len(clerkView.Relations) != 1 || clerkView.Relations[0].ID != relation.ID {
		t.Fatalf("authorized relation not projected: relations=%+v err=%v", clerkView.Relations, err)
	}
	if err := st.DeleteCaseRelation(actors.clerk, " "+left+" ", " "+relation.ID+" "); err != nil {
		t.Fatal(err)
	}
	after := workbenchBoundaryState(t, st, actors.owner)
	if len(after.Relations) != 0 || len(after.WorkbenchAudit) != len(before.WorkbenchAudit)+1 ||
		after.WorkbenchAudit[len(after.WorkbenchAudit)-1].Item.Action != "case.relation.deleted" {
		t.Fatalf("authorized deletion did not remove exactly the relation and record its audit: %+v", after.Relations)
	}
	_, err = st.CreateCaseRelation(actors.owner, right, CaseRelationInput{RelatedCaseID: left, Reason: "删除后重新关联"})
	if err != nil {
		t.Fatalf("deleted pair remained reserved: %v", err)
	}
	if loaded := workbenchBoundaryState(t, st, actors.owner); len(loaded.Relations) != 1 || loaded.Relations[0].Relation.CaseID != right {
		t.Fatalf("reverse recreation not persisted: %+v", loaded.Relations)
	}
}

func TestWorkbenchMutationsPreserveStateWhenReadOnlyOrArchived(t *testing.T) {
	st, owner := openExecutionStore(t)
	caseID := workbenchBoundaryCase(t, st, owner, "冻结工作台案件")
	relatedID := workbenchBoundaryCase(t, st, owner, "冻结关联案件")
	profileInput := CaseProfileInput{Number: "WB-FROZEN", Customer: "采购方", Supplier: "服务商", ContractCategory: "服务", OwnerID: owner.ID}
	if _, err := st.UpdateCaseProfile(owner, caseID, profileInput); err != nil {
		t.Fatal(err)
	}
	taskInput := WorkbenchTaskInput{Title: "待处理任务", Kind: "general", AssigneeID: owner.ID, Status: "open", Priority: "medium"}
	task, err := st.CreateWorkbenchTask(owner, caseID, taskInput)
	if err != nil {
		t.Fatal(err)
	}
	relation, err := st.CreateCaseRelation(owner, caseID, CaseRelationInput{RelatedCaseID: relatedID, Reason: "同一项目"})
	if err != nil {
		t.Fatal(err)
	}
	profileInput.ExpectedVersion, profileInput.Customer = 1, "冻结后不得更新"
	updateInput := taskInput
	updateInput.ExpectedVersion, updateInput.Status = 1, "completed"
	operations := []struct {
		name string
		run  func() error
	}{
		{"profile", func() error { _, err := st.UpdateCaseProfile(owner, caseID, profileInput); return err }},
		{"task create", func() error { _, err := st.CreateWorkbenchTask(owner, caseID, taskInput); return err }},
		{"task update", func() error { _, err := st.UpdateWorkbenchTask(owner, caseID, task.ID, updateInput); return err }},
		{"relation create", func() error {
			_, err := st.CreateCaseRelation(owner, caseID, CaseRelationInput{RelatedCaseID: relatedID, Reason: "冻结后关联"})
			return err
		}},
		{"relation delete", func() error { return st.DeleteCaseRelation(owner, caseID, relation.ID) }},
	}
	before := workbenchBoundaryState(t, st, owner)
	st.UseReadOnly(true)
	for _, operation := range operations {
		t.Run("read only/"+operation.name, func(t *testing.T) {
			workbenchBoundaryFault(t, operation.run(), 503, "read_only")
			workbenchBoundaryUnchanged(t, st, owner, before)
		})
	}
	st.UseReadOnly(false)
	if _, err := st.pool.Exec(context.Background(), `UPDATE cases SET business_status=$2, archived_at=$3, entity_version=entity_version+1 WHERE id=$1`,
		caseID, "archived", "2026-10-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	before = workbenchBoundaryState(t, st, owner)
	for _, operation := range operations {
		t.Run("archived/"+operation.name, func(t *testing.T) {
			workbenchBoundaryFault(t, operation.run(), 409, "conflict")
			workbenchBoundaryUnchanged(t, st, owner, before)
		})
	}
	snapshot, err := st.WorkbenchSnapshot(context.Background(), owner, "month", WorkbenchModel{})
	if err != nil || len(snapshot.Profiles) != 1 || len(snapshot.Tasks) != 1 || len(snapshot.Relations) != 1 || snapshot.Tasks[0].Status != "open" {
		t.Fatalf("frozen business data must remain readable: snapshot=%+v err=%v", snapshot, err)
	}
}

func TestWorkbenchKnowledgeVersionsAreImmutableIdempotentAndTenantScoped(t *testing.T) {
	st, _ := openExecutionStore(t)
	actors := workbenchBoundaryDirectory(t)
	older := Knowledge{ID: "shared-policy", Title: "旧制度", Version: "99.0", Body: "旧版本保留原始内容。",
		SourceType: "reference", CreatedBy: actors.owner.ID, CreatedAt: "2020-01-01T00:00:00.000Z"}
	body, err := json.Marshal(older)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(context.Background(), `INSERT INTO business_knowledge (tenant_id,org_id,id,version,source_type,record_json) VALUES ($1,$2,$3,$4,$5,$6)`,
		actors.owner.TenantID, actors.owner.OrgID, older.ID, older.Version, older.SourceType, body); err != nil {
		t.Fatal(err)
	}
	var legacyBytes string
	if err := st.pool.QueryRow(context.Background(), `SELECT record_json::text FROM business_knowledge WHERE tenant_id=$1 AND id=$2 AND version=$3`,
		actors.owner.TenantID, older.ID, older.Version).Scan(&legacyBytes); err != nil {
		t.Fatal(err)
	}
	input := KnowledgeInput{ID: " shared-policy ", Title: " 当前制度 ", Version: " 2.0 ", Body: "应保留采购审批依据。",
		SourceType: "synthetic_policy", EffectiveAt: stringPointer(" 2026-11-01T08:00:00+08:00 ")}
	created, err := st.CreateKnowledge(actors.owner, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "shared-policy" || created.Title != "当前制度" || created.Version != "2.0" ||
		created.VersionID != "6661823983d620b839dcde99484faf9adc49f445d2fd822f03457568355e911e" ||
		created.EffectiveAt == nil || *created.EffectiveAt != "2026-11-01T00:00:00Z" || created.CreatedBy != actors.owner.ID {
		t.Fatalf("knowledge normalization or provenance incorrect: %+v", created)
	}
	before := workbenchBoundaryState(t, st, actors.owner)
	if len(before.Knowledge) != 2 || !reflect.DeepEqual(before.Knowledge[0].Item, older) || !reflect.DeepEqual(before.Knowledge[1].Item, created.Knowledge) {
		t.Fatalf("creating a version rewrote or lost history: %+v", before.Knowledge)
	}
	idempotent := input
	idempotent.EffectiveAt = stringPointer("2026-11-01T00:00:00Z")
	duplicate, err := st.CreateKnowledge(actors.otherOrg, idempotent)
	if err != nil || !reflect.DeepEqual(duplicate, created) {
		t.Fatalf("identical content from another author must return the original version: got=%+v err=%v", duplicate, err)
	}
	workbenchBoundaryUnchanged(t, st, actors.owner, before)
	olderID := "ec6b8e8fbe8559aee0352f8d2fcf6560a763d971aaaebffc6550a28b5a2e5480"
	olderView, err := st.GetKnowledgeVersion(actors.otherOrg, older.ID, olderID)
	if err != nil || olderView.VersionID != olderID || !reflect.DeepEqual(olderView.Knowledge, older) {
		t.Fatalf("exact older version not returned across the tenant: got=%+v err=%v", olderView, err)
	}
	newerView, err := st.GetKnowledgeVersion(actors.owner, created.ID, created.VersionID)
	if err != nil || !reflect.DeepEqual(newerView, created) {
		t.Fatalf("exact newer version not returned: got=%+v err=%v", newerView, err)
	}
	olderInput := KnowledgeInput{ID: older.ID, Title: older.Title, Version: older.Version, Body: older.Body, SourceType: older.SourceType}
	olderDuplicate, err := st.CreateKnowledge(actors.owner, olderInput)
	if err != nil || !reflect.DeepEqual(olderDuplicate, olderView) {
		t.Fatalf("duplicate of an older version returned the wrong version: got=%+v err=%v", olderDuplicate, err)
	}
	workbenchBoundaryUnchanged(t, st, actors.owner, before)
	for _, invalid := range []string{"", "not-a-version-id", strings.ToUpper(created.VersionID), strings.Repeat("0", 64)} {
		_, err := st.GetKnowledgeVersion(actors.owner, created.ID, invalid)
		workbenchBoundaryFault(t, err, 404, "not_found")
	}
	_, err = st.GetKnowledgeVersion(actors.owner, "other-policy", created.VersionID)
	workbenchBoundaryFault(t, err, 404, "not_found")
	_, err = st.GetKnowledgeVersion(actors.disabled, created.ID, created.VersionID)
	workbenchBoundaryFault(t, err, 401, "unauthenticated")
	for _, mutate := range []func(*KnowledgeInput){
		func(in *KnowledgeInput) { in.Title = "不同标题" },
		func(in *KnowledgeInput) { in.Body = "不得覆盖历史正文。" },
		func(in *KnowledgeInput) { in.SourceType = "reference" },
		func(in *KnowledgeInput) { in.EffectiveAt = nil },
	} {
		candidate := input
		mutate(&candidate)
		_, err := st.CreateKnowledge(actors.otherOrg, candidate)
		workbenchBoundaryFault(t, err, 409, "conflict")
		workbenchBoundaryUnchanged(t, st, actors.owner, before)
	}
	latest, err := st.GetKnowledge(actors.otherOrg, " shared-policy ")
	if err != nil || !reflect.DeepEqual(latest, created) {
		t.Fatalf("latest knowledge must follow creation time across tenant organizations, not lexical version: got=%+v err=%v", latest, err)
	}
	_, err = st.GetKnowledge(actors.foreign, created.ID)
	workbenchBoundaryFault(t, err, 404, "not_found")
	foreign := input
	foreign.Body = "另一租户独立的审批制度。"
	foreignKnowledge, err := st.CreateKnowledge(actors.foreign, foreign)
	if err != nil {
		t.Fatalf("another tenant cannot use its own ID and version: %v", err)
	}
	if foreignKnowledge.VersionID == created.VersionID {
		t.Fatal("the same knowledge ID and display version shared an identity across tenants")
	}
	_, err = st.GetKnowledgeVersion(actors.foreign, created.ID, created.VersionID)
	workbenchBoundaryFault(t, err, 404, "not_found")
	_, err = st.GetKnowledgeVersion(actors.owner, created.ID, foreignKnowledge.VersionID)
	workbenchBoundaryFault(t, err, 404, "not_found")
	foreignExact, err := st.GetKnowledgeVersion(actors.foreign, foreignKnowledge.ID, foreignKnowledge.VersionID)
	if err != nil || !reflect.DeepEqual(foreignExact, foreignKnowledge) {
		t.Fatalf("foreign tenant could not read its own version: got=%+v err=%v", foreignExact, err)
	}
	forged := actors.owner
	forged.TenantID = actors.foreign.TenantID
	_, err = st.GetKnowledgeVersion(forged, created.ID, foreignKnowledge.VersionID)
	workbenchBoundaryFault(t, err, 401, "unauthenticated")
	for _, actor := range []accounts.User{actors.owner, actors.otherOrg, actors.foreign} {
		got, err := st.GetKnowledge(actor, created.ID)
		want := created
		if actor.ID == actors.foreign.ID {
			want = foreignKnowledge
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("tenant knowledge selection leaked another tenant: actor=%s got=%+v err=%v", actor.ID, got, err)
		}
		snapshot, err := st.WorkbenchSnapshot(context.Background(), actor, "week", WorkbenchModel{})
		if err != nil {
			t.Fatal(err)
		}
		if actor.ID == actors.foreign.ID {
			if len(snapshot.Knowledge) != 1 || !reflect.DeepEqual(snapshot.Knowledge[0], foreignKnowledge) {
				t.Fatalf("foreign tenant snapshot leaked knowledge: %+v", snapshot.Knowledge)
			}
		} else if len(snapshot.Knowledge) != 2 || !reflect.DeepEqual(snapshot.Knowledge[0], created) || !reflect.DeepEqual(snapshot.Knowledge[1], olderView) {
			t.Fatalf("knowledge history is not scoped and ordered by creation time: %+v", snapshot.Knowledge)
		}
	}
	before = workbenchBoundaryState(t, st, actors.owner)
	_, err = st.GetKnowledge(actors.owner, " \t ")
	workbenchBoundaryFault(t, err, 422, "invalid")
	_, err = st.GetKnowledge(actors.owner, "absent-policy")
	workbenchBoundaryFault(t, err, 404, "not_found")
	_, err = st.GetKnowledge(actors.disabled, created.ID)
	workbenchBoundaryFault(t, err, 401, "unauthenticated")
	_, err = st.CreateKnowledge(actors.viewer, input)
	workbenchBoundaryFault(t, err, 403, "forbidden")
	_, err = st.CreateKnowledge(actors.disabled, input)
	workbenchBoundaryFault(t, err, 401, "unauthenticated")
	for _, mutate := range []func(*KnowledgeInput){
		func(in *KnowledgeInput) { in.ID = "policy/path" },
		func(in *KnowledgeInput) { in.Version = "two words" },
		func(in *KnowledgeInput) { in.Title = " " },
		func(in *KnowledgeInput) { in.Body = " \n " },
		func(in *KnowledgeInput) { in.SourceType = "untrusted" },
		func(in *KnowledgeInput) { in.EffectiveAt = stringPointer("tomorrow") },
	} {
		candidate := input
		mutate(&candidate)
		_, err := st.CreateKnowledge(actors.owner, candidate)
		workbenchBoundaryFault(t, err, 422, "invalid")
		workbenchBoundaryUnchanged(t, st, actors.owner, before)
	}
	st.UseReadOnly(true)
	_, err = st.CreateKnowledge(actors.owner, input)
	workbenchBoundaryFault(t, err, 503, "read_only")
	workbenchBoundaryUnchanged(t, st, actors.owner, before)
	if got, err := st.GetKnowledge(actors.owner, created.ID); err != nil || !reflect.DeepEqual(got, created) {
		t.Fatalf("read-only mode prevented reading immutable knowledge: got=%+v err=%v", got, err)
	}
	st.UseReadOnly(false)
	generated := input
	generated.ID, generated.EffectiveAt = " ", stringPointer(" \t ")
	auto, err := st.CreateKnowledge(actors.owner, generated)
	if err != nil || !strings.HasPrefix(auto.ID, "knowledge_") || auto.EffectiveAt != nil {
		t.Fatalf("auto ID or absent effective date not normalized: got=%+v err=%v", auto, err)
	}
	if got, err := st.GetKnowledge(actors.owner, auto.ID); err != nil || !reflect.DeepEqual(got, auto) {
		t.Fatalf("generated knowledge ID did not address persisted content: got=%+v err=%v", got, err)
	}
	var preservedBytes string
	if err := st.pool.QueryRow(context.Background(), `SELECT record_json::text FROM business_knowledge WHERE tenant_id=$1 AND id=$2 AND version=$3`,
		actors.owner.TenantID, older.ID, older.Version).Scan(&preservedBytes); err != nil {
		t.Fatal(err)
	}
	if preservedBytes != legacyBytes || strings.Contains(preservedBytes, `"version_id"`) {
		t.Fatalf("legacy knowledge JSON changed: before=%s after=%s", legacyBytes, preservedBytes)
	}
}

func TestWorkbenchCalendarWindowsUseShanghaiMidnightAndMonday(t *testing.T) {
	for _, tc := range []struct{ period, now, want string }{
		{"day", "2026-04-14T15:59:59Z", "2026-04-13T16:00:00Z"},
		{"day", "2026-04-14T16:00:00Z", "2026-04-14T16:00:00Z"},
		{"week", "2026-04-12T15:59:59Z", "2026-04-05T16:00:00Z"},
		{"week", "2026-04-12T16:00:00Z", "2026-04-12T16:00:00Z"},
		{"month", "2026-04-30T15:59:59Z", "2026-03-31T16:00:00Z"},
		{"month", "2026-04-30T16:00:00Z", "2026-04-30T16:00:00Z"},
	} {
		t.Run(tc.period+"/"+tc.now, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			if got := workbenchPeriodStart(now, tc.period).UTC().Format(time.RFC3339); got != tc.want {
				t.Fatalf("period begins at %s, want %s", got, tc.want)
			}
		})
	}
}

func TestWorkbenchDashboardUsesPersistedLatestAttemptsAndTimeWindows(t *testing.T) {
	st, owner := openExecutionStore(t)
	clerk, ok := accounts.ByID("demo-clerk")
	if !ok {
		t.Fatal("demo clerk missing")
	}
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("demo reviewer missing")
	}
	now := time.Date(2026, 4, 15, 4, 0, 0, 0, time.UTC)
	fixtures := []struct {
		name, runState, attemptState string
		started, finished            *string
	}{
		{"day", "completed", "completed", stringPointer("2026-04-14T15:59:58Z"), stringPointer("2026-04-14T16:00:00Z")},
		{"week", "completed", "completed", stringPointer("2026-04-12T15:59:56Z"), stringPointer("2026-04-12T16:00:00Z")},
		{"month", "completed", "completed", stringPointer("2026-03-31T15:59:54Z"), stringPointer("2026-03-31T16:00:00Z")},
		{"before-month", "completed", "completed", stringPointer("2026-03-31T15:59:51Z"), stringPointer("2026-03-31T15:59:59Z")},
		{"no-start", "completed", "completed", nil, stringPointer("2026-04-15T00:00:00Z")},
		{"backwards", "completed", "completed", stringPointer("2026-04-15T00:00:02Z"), stringPointer("2026-04-15T00:00:00Z")},
		{"bad-start", "completed", "completed", stringPointer("legacy-unknown"), stringPointer("2026-04-15T00:00:00Z")},
		{"bad-finish", "completed", "completed", stringPointer("2026-04-15T00:00:00Z"), stringPointer("legacy-unknown")},
		{"no-finish", "completed", "completed", nil, nil},
		{"future", "completed", "completed", stringPointer("2026-04-15T04:00:00Z"), stringPointer("2026-04-15T04:00:01Z")},
		{"at-now", "completed", "completed", stringPointer("2026-04-15T03:59:58Z"), stringPointer("2026-04-15T04:00:00Z")},
		{"accepted", "accepted", "accepted", nil, nil},
		{"running", "running", "running", stringPointer("2026-04-15T00:00:00Z"), nil},
		{"human", "awaiting_human", "", nil, nil},
		{"empty", "", "", nil, nil},
	}
	caseIDs := make(map[string]string, len(fixtures))
	for _, fixture := range fixtures {
		caseIDs[fixture.name] = workbenchBoundaryCase(t, st, owner, "统计案件 "+fixture.name)
	}
	hidden := workbenchBoundaryCase(t, st, clerk, "统计不可见案件")
	if err := st.AddMember(owner, caseIDs["day"], reviewer.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(owner, caseIDs["month"], reviewer.ID); err != nil {
		t.Fatal(err)
	}
	for _, task := range []struct {
		caseID, assignee, status string
		dueAt                    *string
	}{
		{caseIDs["day"], owner.ID, "open", stringPointer("2026-04-15T03:59:59Z")},
		{caseIDs["day"], owner.ID, "in_progress", nil},
		{caseIDs["day"], reviewer.ID, "open", stringPointer("2026-04-15T04:00:00Z")},
		{caseIDs["day"], owner.ID, "completed", stringPointer("2026-04-14T00:00:00Z")},
		{caseIDs["month"], reviewer.ID, "open", stringPointer("2026-04-16T00:00:00Z")},
		{hidden, clerk.ID, "open", stringPointer("2026-04-14T00:00:00Z")},
	} {
		actor := owner
		if task.caseID == hidden {
			actor = clerk
		}
		if _, err := st.CreateWorkbenchTask(actor, task.caseID, WorkbenchTaskInput{
			Title: "统计任务", Kind: "general", AssigneeID: task.assignee, Status: task.status, Priority: "medium", DueAt: task.dueAt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.UpdateCaseProfile(owner, caseIDs["day"], CaseProfileInput{
		Number: "WB-DASHBOARD", Customer: "采购方", Supplier: "服务商", ContractCategory: "服务", OwnerID: owner.ID,
	}); err != nil {
		t.Fatal(err)
	}
	// SQL fixtures retain legacy missing timestamps and old attempts exactly as a
	// persisted dashboard consumer can encounter them, without running a worker.
	workbenchBoundaryRun(t, st, owner, caseIDs["day"], "dashboard-old-run", "completed", "2026-02-01T00:00:00Z",
		Attempt{ID: "dashboard-old-attempt", Ordinal: 1, State: "completed", StartedAt: stringPointer("2026-04-15T00:00:00Z"), FinishedAt: stringPointer("2026-04-15T00:01:30Z")})
	workbenchBoundaryFinding(t, st, owner, caseIDs["day"], "dashboard-old-run", "dashboard-old-attempt", "old-run-risk", stringPointer("high"))
	for _, fixture := range fixtures {
		if fixture.runState == "" {
			continue
		}
		attempts := []Attempt{}
		if fixture.attemptState != "" {
			attempts = append(attempts, Attempt{ID: "dashboard-" + fixture.name + "-attempt", Ordinal: 1,
				State: fixture.attemptState, StartedAt: fixture.started, FinishedAt: fixture.finished})
		}
		if fixture.name == "accepted" {
			attempts[0].Ordinal = 2
			attempts = append(attempts, Attempt{ID: "dashboard-obsolete-attempt", Ordinal: 1, State: "completed",
				StartedAt: stringPointer("2026-04-15T00:00:00Z"), FinishedAt: stringPointer("2026-04-15T00:02:00Z")})
		}
		workbenchBoundaryRun(t, st, owner, caseIDs[fixture.name], "dashboard-"+fixture.name+"-run", fixture.runState, "2026-03-01T00:00:00Z", attempts...)
	}
	workbenchBoundaryRun(t, st, clerk, hidden, "dashboard-hidden-run", "completed", "2026-03-01T00:00:00Z",
		Attempt{ID: "dashboard-hidden-attempt", Ordinal: 1, State: "completed", StartedAt: stringPointer("2026-04-15T00:00:00Z"), FinishedAt: stringPointer("2026-04-15T00:10:00Z")})
	for i, severity := range []string{"high", "low", "info"} {
		workbenchBoundaryFinding(t, st, owner, caseIDs["day"], "dashboard-day-run", "dashboard-day-attempt", fmt.Sprintf("day-risk-%d", i), stringPointer(severity))
	}
	workbenchBoundaryFinding(t, st, owner, caseIDs["accepted"], "dashboard-accepted-run", "dashboard-obsolete-attempt", "old-attempt-risk", stringPointer("high"))
	for i, severity := range []*string{stringPointer("medium"), nil, stringPointer("legacy-unrated")} {
		workbenchBoundaryFinding(t, st, owner, caseIDs["accepted"], "dashboard-accepted-run", "dashboard-accepted-attempt", fmt.Sprintf("current-risk-%d", i), severity)
	}
	workbenchBoundaryFinding(t, st, owner, caseIDs["no-start"], "dashboard-no-start-run", "dashboard-no-start-attempt", "unrated-risk", nil)
	workbenchBoundaryFinding(t, st, clerk, hidden, "dashboard-hidden-run", "dashboard-hidden-attempt", "hidden-risk", stringPointer("high"))
	workbenchBoundaryRemediation(t, st, owner, caseIDs["day"], "dashboard-day-run", "dashboard-day-attempt", "open-one", "open")
	workbenchBoundaryRemediation(t, st, owner, caseIDs["day"], "dashboard-day-run", "dashboard-day-attempt", "open-two", "open")
	workbenchBoundaryRemediation(t, st, owner, caseIDs["month"], "dashboard-month-run", "dashboard-month-attempt", "closed-one", "closed")
	workbenchBoundaryRemediation(t, st, clerk, hidden, "dashboard-hidden-run", "dashboard-hidden-attempt", "hidden-open", "open")

	loaded := workbenchBoundaryState(t, st, owner)
	visible := make([]Case, 0, len(fixtures))
	for _, item := range loaded.Cases {
		if caseReadAllowed(item, owner) {
			visible = append(visible, item)
		}
	}
	if len(loaded.Cases) != 16 || len(loaded.Tasks) != 6 || len(loaded.Remediations) != 4 || len(visible) != 15 {
		t.Fatalf("persisted aggregate fixtures missing: cases=%d tasks=%d remediations=%d visible=%d", len(loaded.Cases), len(loaded.Tasks), len(loaded.Remediations), len(visible))
	}
	for _, tc := range []struct {
		period string
		count  int
		meanMS int64
	}{
		{"day", 4, 2000}, {"week", 5, 3000}, {"month", 6, 4000},
	} {
		t.Run(tc.period, func(t *testing.T) {
			dashboard := buildWorkbenchDashboard(loaded, visible, owner.ID, now, workbenchPeriodStart(now, tc.period), tc.period)
			if dashboard.VisibleCases != 15 || dashboard.MyPendingTasks != 2 || dashboard.OverdueTasks != 1 ||
				dashboard.PendingReviewCases != 3 || dashboard.RemediationCases != 1 || dashboard.CompletedReviews != tc.count ||
				dashboard.AverageDurationMS == nil || *dashboard.AverageDurationMS != tc.meanMS {
				t.Fatalf("dashboard counted hidden, obsolete, out-of-window, or missing-duration data: %+v", dashboard)
			}
			if !reflect.DeepEqual(dashboard.Progress, []WorkbenchCount{{Status: "active", Count: 15}}) ||
				!reflect.DeepEqual(dashboard.Risks, []WorkbenchCount{
					{Severity: "high", Count: 1}, {Severity: "medium", Count: 1}, {Severity: "low", Count: 1},
					{Severity: "info", Count: 1}, {Severity: "unrated", Count: 3},
				}) {
				t.Fatalf("dashboard status or latest-attempt risk buckets incorrect: progress=%+v risks=%+v", dashboard.Progress, dashboard.Risks)
			}
		})
	}
	snapshot, err := st.WorkbenchSnapshot(context.Background(), owner, "month", WorkbenchModel{Name: " review-model ", OCRName: " ocr-model "})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Cases) != 15 || len(snapshot.Tasks) != 5 || len(snapshot.Remediations) != 3 || len(snapshot.Runs) != 15 ||
		snapshot.Model.Name != "review-model" || snapshot.Model.OCRName != "ocr-model" || snapshot.Model.Status != "configured" {
		t.Fatalf("workbench projection omitted visible data or exposed hidden rows: %+v", snapshot)
	}
	byCase := make(map[string]WorkbenchCase, len(snapshot.Cases))
	for _, item := range snapshot.Cases {
		byCase[item.ID] = item
	}
	if item := byCase[caseIDs["day"]]; item.Severity == nil || *item.Severity != "high" || item.Progress == nil || *item.Progress != 25 ||
		item.Profile == nil || item.Profile.Number != "WB-DASHBOARD" || item.LatestRun == nil {
		t.Fatalf("case summary lost latest severity, completion percentage, or profile: %+v", item)
	}
	if item := byCase[caseIDs["month"]]; item.Progress == nil || *item.Progress != 0 {
		t.Fatalf("existing unfinished tasks must show zero rather than unknown progress: %+v", item)
	}
	if item := byCase[caseIDs["accepted"]]; item.Severity == nil || *item.Severity != "medium" {
		t.Fatalf("superseded attempt severity leaked into current case: %+v", item)
	}
	if item := byCase[caseIDs["no-start"]]; item.Severity == nil || *item.Severity != "unrated" {
		t.Fatalf("a finding without severity must remain explicitly unrated: %+v", item)
	}
	if item := byCase[caseIDs["human"]]; item.Severity != nil {
		t.Fatalf("a run without an attempt fabricated a severity: %+v", item)
	}
	if item := byCase[caseIDs["empty"]]; item.Profile != nil || item.Severity != nil || item.Progress != nil || item.LatestRun != nil {
		t.Fatalf("missing business data must remain null: %+v", item)
	}
	for _, run := range snapshot.Runs {
		if run.CaseID == hidden {
			t.Fatalf("hidden run leaked: %+v", run)
		}
		if run.ID == "dashboard-human-run" && (run.AttemptID != nil || run.StartedAt != nil || run.FinishedAt != nil || run.Usage != nil) {
			t.Fatalf("run without an attempt fabricated execution metadata: %+v", run)
		}
		if run.ID == "dashboard-accepted-run" && (run.AttemptID == nil || *run.AttemptID != "dashboard-accepted-attempt" || run.FinishedAt != nil) {
			t.Fatalf("run projection selected an obsolete attempt: %+v", run)
		}
	}
	for _, audit := range snapshot.Audit {
		if audit.CaseID != nil && *audit.CaseID == hidden {
			t.Fatalf("hidden case activity leaked through audit projection: %+v", audit)
		}
	}
}

func TestWorkbenchEmptySnapshotKeepsUnknownMetricsAndValidatesPeriod(t *testing.T) {
	st, owner := openExecutionStore(t)
	before := workbenchBoundaryState(t, st, owner)
	for _, period := range []string{"", "year", " Week "} {
		_, err := st.WorkbenchSnapshot(context.Background(), owner, period, WorkbenchModel{})
		workbenchBoundaryFault(t, err, 422, "invalid")
	}
	_, err := st.WorkbenchSnapshot(context.Background(), accounts.User{ID: "unregistered-actor"}, "day", WorkbenchModel{})
	workbenchBoundaryFault(t, err, 401, "unauthenticated")
	for _, period := range []string{"day", "week", "month"} {
		snapshot, err := st.WorkbenchSnapshot(context.Background(), owner, period, WorkbenchModel{})
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Dashboard.VisibleCases != 0 || snapshot.Dashboard.CompletedReviews != 0 || snapshot.Dashboard.AverageDurationMS != nil ||
			snapshot.Dashboard.MyPendingTasks != 0 || snapshot.Dashboard.OverdueTasks != 0 || snapshot.Dashboard.PendingReviewCases != 0 ||
			snapshot.Dashboard.RemediationCases != 0 || len(snapshot.Dashboard.Progress) != 0 || len(snapshot.Dashboard.Risks) != 5 ||
			snapshot.Cases == nil || len(snapshot.Cases) != 0 || snapshot.Tasks == nil || len(snapshot.Tasks) != 0 || snapshot.Tools == nil || len(snapshot.Tools) != 0 ||
			snapshot.Model.Status != "unconfigured" {
			t.Fatalf("empty workbench fabricated measurements or omitted empty collections: %+v", snapshot)
		}
		for _, risk := range snapshot.Dashboard.Risks {
			if risk.Count != 0 {
				t.Fatalf("empty workbench fabricated risk count: %+v", risk)
			}
		}
	}
	workbenchBoundaryUnchanged(t, st, owner, before)
}

func workbenchBoundaryRun(t *testing.T, st *Store, user accounts.User, caseID, runID, runState, created string, attempts ...Attempt) {
	t.Helper()
	ctx := context.Background()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(user)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO review_runs (id,tenant_id,org_id,case_id,state,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$6)`,
		runID, user.TenantID, user.OrgID, caseID, runState, created); err != nil {
		t.Fatalf("seed persisted review run: %v", err)
	}
	for _, attempt := range attempts {
		if _, err := tx.Exec(ctx, `INSERT INTO review_attempts (id,tenant_id,org_id,run_id,ordinal,state,accepted_at,started_at,finished_at,history_kind,actor_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			attempt.ID, user.TenantID, user.OrgID, runID, attempt.Ordinal, attempt.State, created, attempt.StartedAt, attempt.FinishedAt, "legacy_unfrozen", user.ID); err != nil {
			t.Fatalf("seed persisted review attempt: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func workbenchBoundaryFinding(t *testing.T, st *Store, user accounts.User, caseID, runID, attemptID, findingID string, severity *string) {
	t.Helper()
	if _, err := st.pool.Exec(context.Background(), `INSERT INTO findings
		(id,tenant_id,org_id,case_id,run_id,attempt_id,origin,domain,claim,relation,version,severity,anchors,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		findingID, user.TenantID, user.OrgID, caseID, runID, attemptID, "comparator", "commercial", "需复核合同条款。", "conflict", 1, severity, "[]", "2026-04-01T00:00:00Z"); err != nil {
		t.Fatalf("seed persisted finding: %v", err)
	}
}

func workbenchBoundaryRemediation(t *testing.T, st *Store, user accounts.User, caseID, runID, attemptID, id, status string) {
	t.Helper()
	ctx := context.Background()
	record := Remediation{ID: id, CaseID: caseID, RunID: runID, AttemptID: attemptID, FindingIDs: []string{}, DocumentIDs: []string{},
		Measure: "补充审查依据", Status: status, Version: 1, CreatedAt: "2026-04-01T00:00:00Z", UpdatedAt: "2026-04-01T00:00:00Z"}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO business_remediations
		(id,tenant_id,org_id,case_id,run_id,attempt_id,measure,status,version,reason,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)`,
		id, user.TenantID, user.OrgID, caseID, runID, attemptID, record.Measure, status, 1, "", record.CreatedAt); err != nil {
		t.Fatalf("seed persisted remediation: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO business_remediation_revisions
		(id,tenant_id,org_id,case_id,remediation_id,version,recorded_at,record_json) VALUES ($1,$2,$3,$4,$1,$5,$6,$7)`,
		id, user.TenantID, user.OrgID, caseID, 1, record.CreatedAt, body); err != nil {
		t.Fatalf("seed persisted remediation history: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
