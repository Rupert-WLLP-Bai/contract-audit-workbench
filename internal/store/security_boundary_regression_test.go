package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"contractaudit/internal/accounts"
)

// A normal read or write with no tenant must fail before it can access the
// default tenant, including when the missing scope contains only whitespace.
func TestBusinessOperationsRejectMissingTenantScope(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	core := env.openRole(t, "core")
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo administrator missing")
	}
	input := KnowledgeInput{ID: "tenant-scope-control", Version: "1", Title: "Tenant scope control", Body: "Tenant-owned policy", SourceType: "reference"}
	created, err := core.CreateKnowledge(admin, input)
	if err != nil {
		t.Fatalf("create tenant control: %v", err)
	}

	ctx := context.Background()
	for _, tenant := range []string{"", " \t "} {
		scope := userScope(admin)
		scope.TenantID = tenant
		if _, err := core.snapshotScoped(ctx, scope); err == nil {
			t.Fatalf("business read accepted tenant %q", tenant)
		}
		called := false
		err := core.updateState(ctx, scope, "", func(next *state) error {
			called = true
			next.Knowledge = nil
			return nil
		})
		if err == nil || called {
			t.Fatalf("business write accepted tenant %q: callback=%t err=%v", tenant, called, err)
		}
	}
	got, err := core.GetKnowledgeVersion(admin, created.ID, created.VersionID)
	if err != nil || !reflect.DeepEqual(got, created) {
		t.Fatalf("rejected operations altered the tenant control: got=%+v err=%v", got, err)
	}
}

// The same actor, knowledge ID, version and request key can exist in two
// tenants. Switching the trusted directory models two independently resolved
// identities without ever supplying a tenant as a request parameter.
func TestTenantKnowledgeAndIdempotencyKeysDoNotCross(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	core := env.openRole(t, "core")
	first, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo administrator missing")
	}
	var second accounts.User
	for _, user := range accounts.List() {
		if user.Enabled && user.Role == "admin" && user.TenantID != first.TenantID {
			second = user
			break
		}
	}
	if second.ID == "" {
		t.Fatal("second tenant administrator missing")
	}
	first.ID, second.ID = "shared-security-admin", "shared-security-admin"
	first.Issuer, second.Issuer = "https://directory.example.invalid", "https://directory.example.invalid"
	first.Subject, second.Subject = "shared-security-admin", "shared-security-admin"

	paths := make([]string, 2)
	for i, user := range []accounts.User{first, second} {
		body, err := json.Marshal(struct {
			Users []accounts.User `json:"users"`
		}{Users: []accounts.User{user}})
		if err != nil {
			t.Fatal(err)
		}
		paths[i] = filepath.Join(t.TempDir(), "directory.json")
		if err := os.WriteFile(paths[i], body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if err := accounts.ConfigureDirectory(""); err != nil {
			t.Errorf("restore demo directory: %v", err)
		}
	})
	activate := func(i int) {
		t.Helper()
		if err := accounts.ConfigureDirectory(paths[i]); err != nil {
			t.Fatalf("activate tenant %d directory: %v", i, err)
		}
	}

	users := []accounts.User{first, second}
	var created [2]GovernedKnowledgeVersion
	var inputs [2]CreateGovernedKnowledgeInput
	for i, user := range users {
		activate(i)
		pack := publishGovernedPackStore(t, core, user, "1", "100")
		content := knowledgeFixtureContent(pack, "shared-policy", "1", "policy")
		content.Title = []string{"First tenant policy", "Second tenant policy"}[i]
		inputs[i] = CreateGovernedKnowledgeInput{Content: content, IdempotencyKey: "shared-create-key"}
		var err error
		created[i], err = core.CreateGovernedKnowledge(user, inputs[i])
		if err != nil {
			t.Fatalf("create tenant %d policy with shared key: %v", i, err)
		}
		if created[i].Content.Title != content.Title {
			t.Fatalf("tenant %d received another policy: %+v", i, created[i])
		}
	}
	if created[0].Ref.VersionID == created[1].Ref.VersionID {
		t.Fatal("two tenants shared a governed knowledge version identity")
	}
	for i, user := range users {
		activate(i)
		replayed, err := core.CreateGovernedKnowledge(user, inputs[i])
		if err != nil || !reflect.DeepEqual(replayed, created[i]) {
			t.Fatalf("tenant %d replay returned another tenant's result: got=%+v err=%v", i, replayed, err)
		}
		got, err := core.GetGovernedKnowledgeVersion(user, created[i].Ref.KnowledgeID, created[i].Ref.VersionID)
		if err != nil || !reflect.DeepEqual(got, created[i]) {
			t.Fatalf("tenant %d could not read its own policy: got=%+v err=%v", i, got, err)
		}
		other := created[1-i].Ref
		if _, err := core.GetGovernedKnowledgeVersion(user, other.KnowledgeID, other.VersionID); err == nil {
			t.Fatalf("tenant %d read the other tenant's exact version", i)
		}
	}
}
