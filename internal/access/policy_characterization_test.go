package access

// Characterization tests for the authorization policy. They pin the exact
// decision reasons, the role/action matrix and the scope semantics before the
// policy body is split into helpers. Every case calls the exported Check.

import (
	"testing"
	"time"
)

func characterNow() time.Time {
	return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
}

func characterResource() Resource {
	return Resource{TenantID: "tenant-a", CaseID: "case-a", OrgID: "org/sales", OwnerID: "alice"}
}

func checkDecision(t *testing.T, action Action, principal Principal, resource Resource, now time.Time, wantAllowed bool, wantReason string) {
	t.Helper()
	got := Check(action, principal, resource, now)
	if got.Allowed != wantAllowed || got.Reason != wantReason || got.PolicyVersion != PolicyVersion {
		t.Fatalf("Check(%s) = %+v; want allowed=%v reason=%q version=%q", action, got, wantAllowed, wantReason, PolicyVersion)
	}
}

func principalWith(grants ...Grant) Principal {
	return Principal{UserID: "alice", TenantID: "tenant-a", Enabled: true, Grants: grants}
}

func TestCheckRoleActionMatrixPinned(t *testing.T) {
	tests := []struct {
		role    string
		action  Action
		allowed bool
	}{
		{"case_owner", Read, true},
		{"case_owner", Review, true},
		{"case_owner", Decide, true},
		{"case_owner", Export, true},
		{"case_owner", Manage, true},
		{"reviewer", Read, true},
		{"reviewer", Review, true},
		{"reviewer", Decide, true},
		{"reviewer", Export, false},
		{"reviewer", Manage, false},
		{"viewer", Read, true},
		{"viewer", Review, false},
		{"viewer", Decide, false},
		{"viewer", Export, false},
		{"viewer", Manage, false},
		{"exporter", Read, true},
		{"exporter", Review, false},
		{"exporter", Decide, false},
		{"exporter", Export, true},
		{"exporter", Manage, false},
		{"tenant_admin", Read, false},
		{"platform_admin", Read, false},
		{"Case_Owner", Read, false},
		{"", Read, false},
		{"viewer", Action("case.delete"), false},
		{"case_owner", Action("case.delete"), false},
		{"case_owner", Action(""), false},
	}
	for _, tc := range tests {
		t.Run(tc.role+"/"+string(tc.action), func(t *testing.T) {
			reason := "no_matching_grant"
			if tc.allowed {
				reason = "explicit_role_and_scope"
			}
			checkDecision(t, tc.action, principalWith(Grant{Role: tc.role, Scope: "tenant"}), characterResource(), characterNow(), tc.allowed, reason)
		})
	}
}

func TestCheckScopeSemanticsPinned(t *testing.T) {
	now := characterNow()
	tests := []struct {
		name       string
		grant      Grant
		change     func(*Principal, *Resource)
		wantAllow  bool
		wantReason string
	}{
		{"self owner", Grant{Role: "viewer", Scope: "self"}, nil, true, "explicit_role_and_scope"},
		{"self other owner", Grant{Role: "viewer", Scope: "self"}, func(_ *Principal, r *Resource) { r.OwnerID = "bob" }, false, "no_matching_grant"},
		{"self empty owner", Grant{Role: "viewer", Scope: "self"}, func(_ *Principal, r *Resource) { r.OwnerID = "" }, false, "no_matching_grant"},
		{"case match", Grant{Role: "viewer", Scope: "case", CaseID: "case-a"}, nil, true, "explicit_role_and_scope"},
		{"case wrong", Grant{Role: "viewer", Scope: "case", CaseID: "case-b"}, nil, false, "no_matching_grant"},
		{"case empty grant", Grant{Role: "viewer", Scope: "case"}, nil, false, "no_matching_grant"},
		{"org match", Grant{Role: "viewer", Scope: "org", OrgID: "org/sales"}, nil, true, "explicit_role_and_scope"},
		{"org wrong", Grant{Role: "viewer", Scope: "org", OrgID: "org/other"}, nil, false, "no_matching_grant"},
		{"org empty grant", Grant{Role: "viewer", Scope: "org"}, nil, false, "no_matching_grant"},
		{"org empty grant and empty resource", Grant{Role: "viewer", Scope: "org"}, func(_ *Principal, r *Resource) { r.OrgID = "" }, false, "no_matching_grant"},
		{"subtree first match", Grant{Role: "viewer", Scope: "org_subtree", ResolvedOrgIDs: []string{"org/sales", "org/other"}}, nil, true, "explicit_role_and_scope"},
		{"subtree later match", Grant{Role: "viewer", Scope: "org_subtree", ResolvedOrgIDs: []string{"org/other", "org/sales"}}, nil, true, "explicit_role_and_scope"},
		{"subtree no match", Grant{Role: "viewer", Scope: "org_subtree", ResolvedOrgIDs: []string{"org/other"}}, nil, false, "no_matching_grant"},
		{"subtree empty ids", Grant{Role: "viewer", Scope: "org_subtree"}, nil, false, "no_matching_grant"},
		{"subtree empty element before match", Grant{Role: "viewer", Scope: "org_subtree", ResolvedOrgIDs: []string{"", "org/sales"}}, nil, true, "explicit_role_and_scope"},
		{"subtree empty element and empty resource", Grant{Role: "viewer", Scope: "org_subtree", ResolvedOrgIDs: []string{""}}, func(_ *Principal, r *Resource) { r.OrgID = "" }, false, "no_matching_grant"},
		{"tenant scope", Grant{Role: "viewer", Scope: "tenant"}, nil, true, "explicit_role_and_scope"},
		{"unknown scope", Grant{Role: "viewer", Scope: "*"}, nil, false, "no_matching_grant"},
		{"empty scope", Grant{Role: "viewer", Scope: ""}, nil, false, "no_matching_grant"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			principal := principalWith(tc.grant)
			resource := characterResource()
			if tc.change != nil {
				tc.change(&principal, &resource)
			}
			checkDecision(t, Read, principal, resource, now, tc.wantAllow, tc.wantReason)
		})
	}
}

func TestCheckIdentityAndTenantPinned(t *testing.T) {
	now := characterNow()
	tests := []struct {
		name       string
		change     func(*Principal, *Resource)
		now        time.Time
		wantReason string
	}{
		{"disabled", func(p *Principal, _ *Resource) { p.Enabled = false }, now, "invalid_or_disabled_identity"},
		{"empty user", func(p *Principal, _ *Resource) { p.UserID = "" }, now, "invalid_or_disabled_identity"},
		{"empty principal tenant", func(p *Principal, _ *Resource) { p.TenantID = "" }, now, "invalid_or_disabled_identity"},
		{"empty resource tenant", func(_ *Principal, r *Resource) { r.TenantID = "" }, now, "invalid_or_disabled_identity"},
		{"empty resource case", func(_ *Principal, r *Resource) { r.CaseID = "" }, now, "invalid_or_disabled_identity"},
		{"zero clock", func(*Principal, *Resource) {}, time.Time{}, "invalid_or_disabled_identity"},
		{"cross tenant", func(p *Principal, _ *Resource) { p.TenantID = "tenant-b" }, now, "tenant_boundary"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			principal := principalWith(Grant{Role: "case_owner", Scope: "tenant"})
			resource := characterResource()
			tc.change(&principal, &resource)
			checkDecision(t, Read, principal, resource, tc.now, false, tc.wantReason)
		})
	}

	t.Run("identity before tenant boundary", func(t *testing.T) {
		principal := principalWith(Grant{Role: "case_owner", Scope: "tenant"})
		principal.Enabled = false
		principal.TenantID = "tenant-b"
		checkDecision(t, Read, principal, characterResource(), now, false, "invalid_or_disabled_identity")
	})

	t.Run("tenant boundary before grants", func(t *testing.T) {
		principal := principalWith(Grant{Role: "case_owner", Scope: "tenant"})
		principal.TenantID = "tenant-b"
		checkDecision(t, Read, principal, characterResource(), now, false, "tenant_boundary")
	})

	t.Run("no grants", func(t *testing.T) {
		principal := principalWith()
		checkDecision(t, Read, principal, characterResource(), now, false, "no_matching_grant")
	})
}

func TestCheckExpiryPinned(t *testing.T) {
	now := characterNow()
	tests := []struct {
		name      string
		grant     Grant
		wantAllow bool
	}{
		{"no expiry", Grant{Role: "viewer", Scope: "tenant"}, true},
		{"future expiry", Grant{Role: "viewer", Scope: "tenant", ExpiresAt: now.Add(time.Minute)}, true},
		{"expiry at now", Grant{Role: "viewer", Scope: "tenant", ExpiresAt: now}, false},
		{"expired", Grant{Role: "viewer", Scope: "tenant", ExpiresAt: now.Add(-time.Second)}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reason := "no_matching_grant"
			if tc.wantAllow {
				reason = "explicit_role_and_scope"
			}
			checkDecision(t, Read, principalWith(tc.grant), characterResource(), now, tc.wantAllow, reason)
		})
	}
}

func TestCheckGrantOrderingPinned(t *testing.T) {
	now := characterNow()
	tests := []struct {
		name   string
		grants []Grant
	}{
		{"expired then valid", []Grant{
			{Role: "viewer", Scope: "tenant", ExpiresAt: now.Add(-time.Hour)},
			{Role: "viewer", Scope: "tenant"},
		}},
		{"wrong role then valid", []Grant{
			{Role: "reviewer", Scope: "tenant"},
			{Role: "viewer", Scope: "tenant"},
		}},
		{"wrong scope then valid", []Grant{
			{Role: "viewer", Scope: "org", OrgID: "org/other"},
			{Role: "viewer", Scope: "tenant"},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkDecision(t, Read, principalWith(tc.grants...), characterResource(), now, true, "explicit_role_and_scope")
		})
	}

	t.Run("first match wins", func(t *testing.T) {
		checkDecision(t, Read, principalWith(
			Grant{Role: "viewer", Scope: "tenant"},
			Grant{Role: "viewer", Scope: "tenant"},
		), characterResource(), now, true, "explicit_role_and_scope")
	})
}
