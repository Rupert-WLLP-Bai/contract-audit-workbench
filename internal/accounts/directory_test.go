package accounts

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"contractaudit/internal/access"
)

func TestEnterpriseDirectoryBindsSubjectAndRevokesCurrentAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "directory.json")
	valid := `{"users":[{"id":"alice","display_name":"经办人","role":"reviewer","tenant_id":"TA","org_id":"sales","enabled":true,"issuer":"https://identity.example/realms/a","subject":"subject-a","grants":[{"role":"reviewer","scope":"org","org_id":"sales"}]}]}`
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ConfigureDirectory("") })
	user, ok := BySubject("https://identity.example/realms/a", "subject-a")
	if !ok || user.TenantID != "TA" || user.OrgID != "sales" {
		t.Fatalf("unbound subject: %+v", user)
	}
	if _, ok := BySubject("https://wrong.example", "subject-a"); ok {
		t.Fatal("issuer mismatch accepted")
	}
	if _, ok := Authenticate("demo-admin", "admin-demo"); ok {
		t.Fatal("enterprise mode accepted demo password")
	}
	resource := access.Resource{TenantID: "TA", CaseID: "case-a", OrgID: "sales", OwnerID: "bob"}
	if !access.Check(access.Review, Principal(user), resource, time.Now()).Allowed {
		t.Fatal("explicit organization review grant rejected")
	}
	resource.TenantID = "TB"
	if access.Check(access.Read, Principal(user), resource, time.Now()).Allowed {
		t.Fatal("cross-tenant content grant accepted")
	}
	user.Grants[0].Role = "case_owner"
	if current, ok := ByID("alice"); !ok || current.Grants[0].Role != "reviewer" {
		t.Fatal("caller changed trusted directory grants")
	}
	if err := os.WriteFile(path, []byte(`{"users":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ByID("alice"); ok {
		t.Fatal("removed identity retained authorization")
	}
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ByID("alice"); !ok {
		t.Fatal("valid directory restoration not observed")
	}
	if err := os.WriteFile(path, []byte(`{"users":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ByID("alice"); ok {
		t.Fatal("corrupt directory retained stale authorization")
	}
}
