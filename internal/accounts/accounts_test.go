package accounts

import (
	"reflect"
	"testing"
	"time"

	"contractaudit/internal/access"
)

func TestDemoIdentityAndCapabilities(t *testing.T) {
	cases := []struct {
		id, secret, role, tenant, org, grantRole string
		create, start, decide                    bool
	}{
		{"demo-admin", "admin-demo", "admin", "local-demo", "demo-org", "case_owner", true, true, true},
		{"demo-clerk", "clerk-demo", "clerk", "local-demo", "demo-org", "case_owner", true, true, false},
		{"demo-reviewer", "reviewer-demo", "reviewer", "local-demo", "demo-org", "reviewer", false, true, true},
		{"demo-viewer", "viewer-demo", "viewer", "local-demo", "demo-org", "viewer", false, false, false},
		{"demo-otherorg-clerk", "otherorg-clerk-demo", "clerk", "local-demo", "demo-other-org", "case_owner", true, true, false},
		{"demo-tenant2-admin", "tenant2-admin-demo", "admin", "local-demo-2", "demo-org-2", "case_owner", true, true, true},
		{"demo-tenant2-clerk", "tenant2-clerk-demo", "clerk", "local-demo-2", "demo-org-2", "case_owner", true, true, false},
		{"demo-tenant2-reviewer", "tenant2-reviewer-demo", "reviewer", "local-demo-2", "demo-org-2", "reviewer", false, true, true},
		{"demo-tenant2-viewer", "tenant2-viewer-demo", "viewer", "local-demo-2", "demo-org-2", "viewer", false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			user, ok := Authenticate(tc.id, tc.secret)
			if !ok || user.ID != tc.id || user.Role != tc.role || user.TenantID != tc.tenant || user.OrgID != tc.org || !user.Enabled {
				t.Fatalf("identity %+v %v", user, ok)
			}
			if want := []access.Grant{{Role: tc.grantRole, Scope: "case"}}; !reflect.DeepEqual(user.Grants, want) {
				t.Fatalf("grants %+v, want %+v", user.Grants, want)
			}
			if user2, ok := ByID(tc.id); !ok || !reflect.DeepEqual(user2, user) {
				t.Fatalf("directory %+v %v", user2, ok)
			}
			if Tenant(user) != tc.tenant || Principal(user).TenantID != tc.tenant {
				t.Fatal("server-bound tenant was not preserved")
			}
			if CanCreate(user.Role) != tc.create || CanUpload(user.Role) != tc.create || CanStart(user.Role) != tc.start || CanDecide(user.Role) != tc.decide {
				t.Fatalf("capabilities for %s", user.Role)
			}
			for _, other := range cases {
				if other.id != tc.id {
					if _, ok := Authenticate(tc.id, other.secret); ok {
						t.Fatalf("accepted another account's credentials: %s", other.id)
					}
				}
			}
			if _, ok := Authenticate(tc.id, "incorrect"); ok {
				t.Fatal("bad credentials accepted")
			}
			for _, other := range cases {
				if other.id == tc.id {
					continue
				}
				resource := access.Resource{TenantID: other.tenant, OrgID: other.org, CaseID: "other-case", OwnerID: other.id}
				if access.Check(access.Read, Principal(user), resource, time.Now()).Allowed {
					t.Fatalf("default grant permits access to %s's case", other.id)
				}
			}
			// Even an explicit case grant must not cross the tenant boundary.
			principal := Principal(user)
			principal.Grants[0].CaseID = "case-boundary"
			resource := access.Resource{TenantID: tc.tenant, OrgID: tc.org, CaseID: "case-boundary", OwnerID: tc.id}
			if !access.Check(access.Read, principal, resource, time.Now()).Allowed {
				t.Fatal("explicit case grant rejected within tenant")
			}
			if tc.tenant == "local-demo" {
				resource.TenantID = "local-demo-2"
			} else {
				resource.TenantID = "local-demo"
			}
			if access.Check(access.Read, principal, resource, time.Now()).Allowed {
				t.Fatal("explicit case grant crossed tenant boundary")
			}
			user, _ = ByID(tc.id)
			user.Grants[0].Role = "changed"
			if current, ok := ByID(tc.id); !ok || current.Grants[0].Role != tc.grantRole {
				t.Fatal("caller changed directory grants")
			}
		})
	}
	if _, ok := Authenticate("unknown", "admin-demo"); ok {
		t.Fatal("unknown account accepted")
	}
	if _, ok := ByID("unknown"); ok {
		t.Fatal("unknown account exists")
	}
	if CanCreate("unknown") || CanUpload("unknown") || CanStart("unknown") || CanDecide("unknown") {
		t.Fatal("unknown role has privileges")
	}
	items := List()
	if len(items) != len(cases) {
		t.Fatalf("directory count %d, want %d", len(items), len(cases))
	}
	items[0].Role = "changed"
	if List()[0].Role != "admin" {
		t.Fatal("caller mutated the directory")
	}
}

func TestDemoIdentityCannotRunInProduction(t *testing.T) {
	for _, mode := range []string{"", "dev", "development", "test"} {
		if err := ValidateEnvironment(mode); err != nil {
			t.Fatalf("%q: %v", mode, err)
		}
	}
	for _, mode := range []string{"production", "prod", "PRODUCTION", "unknown"} {
		if err := ValidateEnvironment(mode); err == nil {
			t.Fatalf("%q accepted fixed demo passwords", mode)
		}
	}
}
