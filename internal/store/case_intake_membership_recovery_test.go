// File metadata (manifest mirror):
// Purpose: Verifies intake continuation reads and completion against current membership, organization, tenant, context version and case authority using restricted PostgreSQL roles.
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
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"github.com/jackc/pgx/v5"
)

func TestCaseIntakeRecoveryUsesCurrentMembershipPostgres(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required")
	}
	core := env.openRole(t, "core")
	ctx := context.Background()
	profile := accounts.User{ID: "intake-membership-owner", DisplayName: "Intake owner", Role: "clerk", TenantID: "directory", OrgID: "directory", Enabled: true, Issuer: "https://intake-identity.invalid", Subject: "owner"}
	directory, err := json.Marshal(map[string]any{"users": []accounts.User{profile}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "directory.json")
	if err := os.WriteFile(path, directory, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = accounts.ConfigureDirectory("") })
	const principal = "11700000-0000-4000-8000-000000000001"
	const membershipA = "11700000-0000-4000-8000-000000000002"
	const membershipOtherOrg = "11700000-0000-4000-8000-000000000003"
	const membershipOtherTenant = "11700000-0000-4000-8000-000000000004"
	mapping := IdentityCrosswalk{
		Version:    1,
		Tenants:    []IdentityTenantMap{{ID: "intake-tenant-a", DisplayName: "Tenant A", Status: "active"}, {ID: "intake-tenant-b", DisplayName: "Tenant B", Status: "active"}},
		OrgUnits:   []IdentityOrgUnitMap{{TenantID: "intake-tenant-a", ID: "org-a", DisplayName: "Org A", Status: "active"}, {TenantID: "intake-tenant-a", ID: "org-b", DisplayName: "Org B", Status: "active"}, {TenantID: "intake-tenant-b", ID: "org-c", DisplayName: "Org C", Status: "active"}},
		Principals: []IdentityPrincipalMap{{ID: principal, ActorID: profile.ID, Issuer: profile.Issuer, Subject: profile.Subject, Status: "active"}},
		Memberships: []IdentityMembershipMap{
			{ID: membershipA, ActorID: profile.ID, TenantID: "intake-tenant-a", OrgUnitID: "org-a", RoleCode: "clerk", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"},
			{ID: membershipOtherOrg, ActorID: profile.ID, TenantID: "intake-tenant-a", OrgUnitID: "org-b", RoleCode: "clerk", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"},
			{ID: membershipOtherTenant, ActorID: profile.ID, TenantID: "intake-tenant-b", OrgUnitID: "org-c", RoleCode: "clerk", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"},
		},
	}
	cfg, err := pgx.ParseConfig(env.harness.sel.TestDSN)
	if err != nil {
		t.Fatal(err)
	}
	importMapping := func() {
		t.Helper()
		if _, err := ImportIdentityCrosswalk(ctx, env.harness.sel.TestDSN, env.harness.schema, cfg.Database, mapping, true); err != nil {
			t.Fatalf("import membership fixture: %v", err)
		}
	}
	importMapping()
	login := func(member string) (context.Context, accounts.User, string) {
		t.Helper()
		result, err := core.ResolveVerifiedOIDC(ctx, profile, "client")
		if err != nil {
			t.Fatal(err)
		}
		if result.PendingToken != "" {
			result, err = core.ConsumePendingMembership(ctx, result.PendingToken, member, "client")
			if err != nil {
				t.Fatal(err)
			}
		}
		user, _, err := core.AuthenticateSession(ctx, result.SessionToken)
		if err != nil || user.MembershipID != member {
			t.Fatalf("authenticate chosen membership: %v", err)
		}
		return WithAdmissionSessionToken(ctx, result.SessionToken), user, result.SessionToken
	}
	ctxA, userA, _ := login(membershipA)
	ctxB, userB, tokenB := login(membershipA)
	input := CaseIntakeInput{Title: "受限成员续办", ContractType: "service", BusinessLine: "ENT", Scenario: "cloud_services", Filename: "member.txt", MediaType: "text/plain", Body: []byte("membership bound"), IdempotencyKey: "membership-session-recovery"}
	operation, err := core.BeginCaseIntake(ctxA, userA, input)
	if err != nil {
		t.Fatal(err)
	}
	if resumed, err := core.BeginCaseIntake(ctxB, userB, input); err != nil || resumed.OperationID != operation.OperationID {
		t.Fatalf("current same-membership owner could not resume: %+v %v", resumed, err)
	}
	for _, member := range []string{membershipOtherOrg, membershipOtherTenant} {
		foreignCtx, foreign, token := login(member)
		if _, err := core.GetCaseIntakeContinuation(foreignCtx, foreign, operation.OperationID); err == nil {
			t.Fatal("another organization or tenant read the original request")
		}
		if foreign.TenantID == userA.TenantID {
			if _, err := core.BeginCaseIntake(foreignCtx, foreign, input); err == nil {
				t.Fatal("another organization resumed the original operation")
			}
		}
		if _, err := core.CreateDocumentIntakeTicket(foreignCtx, foreign, token, operation.OperationID, operation.CaseID, operation.UploadKey, input.Body); err == nil {
			t.Fatal("another organization or tenant received upload authority")
		}
	}
	if err := core.SwitchSessionContext(ctx, tokenB, "1", membershipOtherOrg); err != nil {
		t.Fatal(err)
	}
	if _, err := core.BeginCaseIntake(ctxB, userB, input); err == nil {
		t.Fatal("old session context authorized recovery after membership switch")
	}
	if _, err := core.GetCaseIntakeContinuation(ctxB, userB, operation.OperationID); err == nil {
		t.Fatal("old session context read the original continuation after membership switch")
	}
	if _, err := core.ListPendingCaseIntakes(ctxB, userB, 10, ""); err == nil {
		t.Fatal("old session context listed pending requests after membership switch")
	}
	if _, err := core.GetCaseIntakeByKey(ctxB, userB, input.IdempotencyKey); err == nil {
		t.Fatal("old session context read an intake by key after membership switch")
	}
	if _, err := core.GetCaseIntake(ctxB, userB, operation.OperationID); err == nil {
		t.Fatal("old session context read an intake receipt after membership switch")
	}
	if err := core.SwitchSessionContext(ctx, tokenB, "2", membershipA); err != nil {
		t.Fatal(err)
	}
	userB, _, err = core.AuthenticateSession(ctx, tokenB)
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range []access.Grant{
		{Role: "viewer", Scope: "tenant"},
		{Role: "case_owner", Scope: "tenant", ExpiresAt: time.Now().UTC().Add(-time.Hour)},
	} {
		mapping.Memberships[0].Grants = []access.Grant{grant}
		importMapping()
		if _, err := core.BeginCaseIntake(ctxB, userB, input); err == nil {
			t.Fatal("stale authority snapshot resumed after grant change")
		}
		userB, _, err = core.AuthenticateSession(ctx, tokenB)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := core.BeginCaseIntake(ctxB, userB, input); err == nil {
			t.Fatalf("current downgraded or expired grant resumed: %+v", grant)
		}
		if _, err := core.CreateDocumentIntakeTicket(ctxB, userB, tokenB, operation.OperationID, operation.CaseID, operation.UploadKey, input.Body); err == nil {
			t.Fatalf("current downgraded or expired grant obtained upload authority: %+v", grant)
		}
	}
}
