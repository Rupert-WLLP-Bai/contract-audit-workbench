// File metadata (manifest mirror):
// Purpose: Exercises authorized case-list profile projection, same-name records, business-number edits, tenant isolation, read-only access and revoked grants against isolated PostgreSQL fixtures.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
)

func TestCaseListIdentityUsesAuthorizedProfilesAndStableCaseID(t *testing.T) {
	st, _ := openExecutionStore(t)
	actors := workbenchBoundaryDirectory(t)
	for _, tenant := range []string{actors.owner.TenantID, actors.foreign.TenantID} {
		if _, err := st.pool.Exec(context.Background(), `INSERT INTO identity_tenants(tenant_id,display_name,status) VALUES($1,$1,'active') ON CONFLICT DO NOTHING`, tenant); err != nil {
			t.Fatal(err)
		}
	}
	caseID := workbenchBoundaryCase(t, st, actors.owner, "同名案件")
	missingID := workbenchBoundaryCase(t, st, actors.owner, "同名案件")
	foreignID := workbenchBoundaryCase(t, st, actors.foreign, "同名案件")
	input := CaseProfileInput{Number: "CASE-001", Customer: "客户甲", Supplier: "供应商甲", ContractCategory: "服务", OwnerID: actors.owner.ID}
	profile, err := st.UpdateCaseProfile(actors.owner, caseID, input)
	if err != nil {
		t.Fatal(err)
	}
	foreign := input
	foreign.Number, foreign.Customer, foreign.OwnerID = "FOREIGN-001", "跨租户客户", actors.foreign.ID
	if _, err := st.UpdateCaseProfile(actors.foreign, foreignID, foreign); err != nil {
		t.Fatal(err)
	}
	find := func(actor accounts.User, id string) *CaseSummary {
		t.Helper()
		rows, err := st.ListCasesChecked(actor)
		if err != nil {
			t.Fatal(err)
		}
		for i := range rows {
			if rows[i].Case.ID == id {
				return &rows[i]
			}
		}
		return nil
	}
	row := find(actors.owner, caseID)
	if row == nil || row.Identity == nil || row.Identity.Number != input.Number || row.Identity.Customer != input.Customer || row.Identity.Supplier != input.Supplier || row.Identity.OwnerName != actors.owner.DisplayName {
		t.Fatalf("wrong visible identity: %+v", row)
	}
	if row := find(actors.owner, missingID); row == nil || row.Identity != nil {
		t.Fatalf("missing profile was invented: %+v", row)
	}
	if find(actors.owner, foreignID) != nil || find(actors.foreign, caseID) != nil {
		t.Fatal("profile list crossed tenant boundary")
	}
	input.Number, input.ExpectedVersion = "CASE-002", profile.Version
	if _, err := st.UpdateCaseProfile(actors.owner, caseID, input); err != nil {
		t.Fatal(err)
	}
	if row := find(actors.owner, caseID); row == nil || row.Case.ID != caseID || row.Identity.Number != "CASE-002" {
		t.Fatalf("number edit changed identity or was not projected: %+v", row)
	}
	workbenchBoundaryGrant(t, actors.clerk.ID, caseID)
	st.UseReadOnly(true)
	if row := find(actors.clerk, caseID); row == nil || row.Identity == nil || row.Identity.Number != "CASE-002" {
		t.Fatalf("authorized read-only identity missing: %+v", row)
	}
	users := accounts.List()
	for i := range users {
		if users[i].ID == actors.clerk.ID {
			users[i].Grants = []access.Grant{{Role: "case_owner", Scope: "self"}}
		}
	}
	body, err := json.Marshal(map[string]any{"users": users})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "revoked-directory.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	if find(actors.clerk, caseID) != nil {
		t.Fatal("revoked reader still sees case identity")
	}
}

func TestCaseListIdentityWithCoreRoleAndRevokedMembership(t *testing.T) {
	approvalDecisionTestDirectory(t)
	fixture, ok := newDurableCaseFixture(t, initializeApprovalSession)
	if !ok {
		t.Skip("isolated PostgreSQL role fixture unavailable")
	}
	input := CaseProfileInput{Number: "CORE-001", Customer: "授权客户", Supplier: "授权供应商", ContractCategory: "服务", OwnerID: fixture.admin.ID}
	if _, err := fixture.core.UpdateCaseProfile(fixture.admin, fixture.caseID, input); err != nil {
		t.Fatal(err)
	}
	viewer, ok := accounts.ByID("demo-viewer")
	if !ok {
		t.Fatal("viewer missing")
	}
	login, err := fixture.core.ResolveVerifiedOIDC(context.Background(), viewer, "client")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := fixture.core.ListCasesChecked(login.User)
	if err != nil || len(rows) != 1 || rows[0].Identity == nil || rows[0].Identity.Number != input.Number || rows[0].Identity.Customer != input.Customer {
		t.Fatalf("core reader projection missing or incorrect: %+v, %v", rows, err)
	}
	if _, err := fixture.environment.harness.pool.Exec(context.Background(), `UPDATE identity_memberships SET grants='[]' WHERE membership_id=$1::uuid`, login.User.MembershipID); err != nil {
		t.Fatal(err)
	}
	current, _, err := fixture.core.AuthenticateSession(context.Background(), login.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = fixture.core.ListCasesChecked(current)
	if err != nil || len(rows) != 0 {
		t.Fatalf("revoked core reader retained case data: %+v, %v", rows, err)
	}
}
