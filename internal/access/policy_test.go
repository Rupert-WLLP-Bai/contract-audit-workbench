package access

import (
	"testing"
	"time"
)

func TestAuthorizationScopesAndRoles(t *testing.T) {
	now := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	base := Resource{TenantID: "branch-a", CaseID: "case-a", OrgID: "sales/one", OwnerID: "alice"}
	cases := []struct {
		name    string
		action  Action
		grant   Grant
		change  func(*Principal, *Resource)
		allowed bool
	}{
		{"owner self", Manage, Grant{Role: "case_owner", Scope: "self"}, nil, true},
		{"owner other person", Read, Grant{Role: "case_owner", Scope: "self"}, func(p *Principal, _ *Resource) { p.UserID = "bob" }, false},
		{"reviewer case", Review, Grant{Role: "reviewer", Scope: "case", CaseID: "case-a"}, nil, true},
		{"wrong case", Read, Grant{Role: "reviewer", Scope: "case", CaseID: "case-b"}, nil, false},
		{"empty case grant", Read, Grant{Role: "reviewer", Scope: "case"}, nil, false},
		{"viewer cannot decide", Decide, Grant{Role: "viewer", Scope: "tenant"}, nil, false},
		{"viewer can read", Read, Grant{Role: "viewer", Scope: "tenant"}, nil, true},
		{"reviewer cannot export", Export, Grant{Role: "reviewer", Scope: "tenant"}, nil, false},
		{"explicit exporter", Export, Grant{Role: "exporter", Scope: "tenant"}, nil, true},
		{"exporter cannot review", Review, Grant{Role: "exporter", Scope: "tenant"}, nil, false},
		{"same org", Read, Grant{Role: "viewer", Scope: "org", OrgID: "sales/one"}, nil, true},
		{"org prefix is not inheritance", Read, Grant{Role: "viewer", Scope: "org", OrgID: "sales"}, nil, false},
		{"resolved descendant", Decide, Grant{Role: "reviewer", Scope: "org_subtree", ResolvedOrgIDs: []string{"sales/one"}}, nil, true},
		{"unresolved subtree", Read, Grant{Role: "viewer", Scope: "org_subtree", OrgID: "sales"}, nil, false},
		{"subtree exact ids", Read, Grant{Role: "viewer", Scope: "org_subtree", ResolvedOrgIDs: []string{"sales"}}, nil, false},
		{"cross tenant despite tenant grant", Read, Grant{Role: "case_owner", Scope: "tenant"}, func(p *Principal, _ *Resource) { p.TenantID = "branch-b" }, false},
		{"disabled account", Read, Grant{Role: "case_owner", Scope: "tenant"}, func(p *Principal, _ *Resource) { p.Enabled = false }, false},
		{"empty user", Read, Grant{Role: "case_owner", Scope: "tenant"}, func(p *Principal, _ *Resource) { p.UserID = "" }, false},
		{"empty tenant", Read, Grant{Role: "case_owner", Scope: "tenant"}, func(p *Principal, _ *Resource) { p.TenantID = "" }, false},
		{"empty resource tenant", Read, Grant{Role: "case_owner", Scope: "tenant"}, func(_ *Principal, r *Resource) { r.TenantID = "" }, false},
		{"empty resource case", Read, Grant{Role: "case_owner", Scope: "tenant"}, func(_ *Principal, r *Resource) { r.CaseID = "" }, false},
		{"expiry boundary", Read, Grant{Role: "viewer", Scope: "tenant", ExpiresAt: now}, nil, false},
		{"unexpired grant", Read, Grant{Role: "viewer", Scope: "tenant", ExpiresAt: now.Add(time.Minute)}, nil, true},
		{"tenant admin cannot read", Read, Grant{Role: "tenant_admin", Scope: "tenant"}, nil, false},
		{"platform admin cannot read", Read, Grant{Role: "platform_admin", Scope: "tenant"}, nil, false},
		{"unknown action denied", Action("case.delete"), Grant{Role: "case_owner", Scope: "tenant"}, nil, false},
		{"unknown scope denied", Read, Grant{Role: "case_owner", Scope: "*"}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Principal{UserID: "alice", TenantID: "branch-a", Enabled: true, Grants: []Grant{tc.grant}}
			r := base
			if tc.change != nil {
				tc.change(&p, &r)
			}
			got := Check(tc.action, p, r, now)
			if got.Allowed != tc.allowed || got.PolicyVersion != PolicyVersion {
				t.Fatalf("authorization = %+v; want allowed=%v", got, tc.allowed)
			}
		})
	}
}

func TestMissingClockAndRevocationFailClosed(t *testing.T) {
	p := Principal{UserID: "a", TenantID: "t", Enabled: true, Grants: []Grant{{Role: "viewer", Scope: "tenant"}}}
	r := Resource{TenantID: "t", CaseID: "c"}
	if Check(Read, p, r, time.Time{}).Allowed {
		t.Fatal("missing trusted clock allowed")
	}
	p.Grants = nil
	if Check(Read, p, r, time.Now()).Allowed {
		t.Fatal("revoked grants allowed")
	}
}
