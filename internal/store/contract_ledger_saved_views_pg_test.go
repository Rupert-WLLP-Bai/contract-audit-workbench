// File metadata (manifest mirror):
// Purpose: Checks saved-view persistence through the Store service role, including owner isolation, duplicate names, mutation CAS errors, ordering and deletion.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"testing"

	"contractaudit/internal/accounts"
)

func TestContractSavedViewsAreOwnerScopedAndUseRowVersionCASPostgres(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required")
	}
	core := env.openRole(t, "core")
	ctx := context.Background()
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("synthetic admin fixture missing")
	}
	adminToken, err := core.EstablishSession(admin, "client")
	if err != nil {
		t.Fatal(err)
	}
	admin, _, err = core.AuthenticateSession(ctx, adminToken)
	if err != nil {
		t.Fatal(err)
	}
	other, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("synthetic reviewer fixture missing")
	}
	otherToken, err := core.EstablishSession(other, "client")
	if err != nil {
		t.Fatal(err)
	}
	other, _, err = core.AuthenticateSession(ctx, otherToken)
	if err != nil {
		t.Fatal(err)
	}

	created, err := core.CreateContractSavedView(ctx, admin, ContractSavedViewInput{
		Name: "  个人保存视图  ", Filter: ContractLedgerFilter{Text: " 合同 "},
	})
	if err != nil {
		t.Fatalf("create saved view: %v", err)
	}
	if created.Name != "个人保存视图" || created.Filter.Text != "合同" || created.RowVersion != 1 || len(created.Columns) == 0 {
		t.Fatalf("created saved view was not normalized: %+v", created)
	}
	adminViews, err := core.ListContractSavedViews(ctx, admin)
	if err != nil || len(adminViews) != 1 || adminViews[0].ViewID != created.ViewID {
		t.Fatalf("owner list = %+v, err=%v", adminViews, err)
	}
	otherViews, err := core.ListContractSavedViews(ctx, other)
	if err != nil || len(otherViews) != 0 {
		t.Fatalf("other owner list = %+v, err=%v", otherViews, err)
	}
	otherCreated, err := core.CreateContractSavedView(ctx, other, ContractSavedViewInput{Name: "个人保存视图"})
	if err != nil {
		t.Fatalf("same name for another owner: %v", err)
	}
	otherViews, err = core.ListContractSavedViews(ctx, other)
	if err != nil || len(otherViews) != 1 || otherViews[0].ViewID != otherCreated.ViewID {
		t.Fatalf("other owner did not retain a separate same-name view: %+v, err=%v", otherViews, err)
	}
	secondAdminView, err := core.CreateContractSavedView(ctx, admin, ContractSavedViewInput{Name: "另一个视图"})
	if err != nil {
		t.Fatalf("create second owner view: %v", err)
	}

	_, err = core.CreateContractSavedView(ctx, admin, ContractSavedViewInput{Name: "个人保存视图"})
	if faultStatus(err) != 409 {
		t.Fatalf("duplicate owner name = %v; want 409", err)
	}
	_, err = core.ReplaceContractSavedView(ctx, other, ContractSavedViewUpdateInput{
		ViewID: created.ViewID, ExpectedRowVersion: created.RowVersion, Name: "越权替换",
	})
	if faultStatus(err) != 404 {
		t.Fatalf("other owner replace = %v; want 404", err)
	}
	if err := core.DeleteContractSavedView(ctx, other, created.ViewID, created.RowVersion); faultStatus(err) != 404 {
		t.Fatalf("other owner delete = %v; want 404", err)
	}

	replaced, err := core.ReplaceContractSavedView(ctx, admin, ContractSavedViewUpdateInput{
		ViewID: created.ViewID, ExpectedRowVersion: created.RowVersion, Name: "更新后的视图",
	})
	if err != nil || replaced.RowVersion != created.RowVersion+1 || replaced.Name != "更新后的视图" {
		t.Fatalf("replace saved view = %+v, err=%v", replaced, err)
	}
	adminViews, err = core.ListContractSavedViews(ctx, admin)
	if err != nil || len(adminViews) != 2 || adminViews[0].ViewID != created.ViewID {
		t.Fatalf("updated_at ordering = %+v, err=%v", adminViews, err)
	}
	_, err = core.ReplaceContractSavedView(ctx, admin, ContractSavedViewUpdateInput{
		ViewID: created.ViewID, ExpectedRowVersion: created.RowVersion, Name: "过期替换",
	})
	if faultStatus(err) != 409 {
		t.Fatalf("stale replace = %v; want 409", err)
	}
	if err := core.DeleteContractSavedView(ctx, admin, created.ViewID, created.RowVersion); faultStatus(err) != 409 {
		t.Fatalf("stale delete = %v; want 409", err)
	}

	if err := core.DeleteContractSavedView(ctx, admin, created.ViewID, replaced.RowVersion); err != nil {
		t.Fatalf("delete current row version: %v", err)
	}
	adminViews, err = core.ListContractSavedViews(ctx, admin)
	if err != nil || len(adminViews) != 1 || adminViews[0].ViewID != secondAdminView.ViewID {
		t.Fatalf("deleted view remained visible: %+v, err=%v", adminViews, err)
	}
	if err := core.DeleteContractSavedView(ctx, admin, created.ViewID, replaced.RowVersion); faultStatus(err) != 404 {
		t.Fatalf("delete missing saved view = %v; want 404", err)
	}
	if err := core.DeleteContractSavedView(ctx, admin, secondAdminView.ViewID, secondAdminView.RowVersion); err != nil {
		t.Fatalf("delete second admin view: %v", err)
	}
	otherViews, err = core.ListContractSavedViews(ctx, other)
	if err != nil || len(otherViews) != 1 || otherViews[0].ViewID != otherCreated.ViewID {
		t.Fatalf("deleting the first owner's view changed the other owner list: %+v, err=%v", otherViews, err)
	}
}
