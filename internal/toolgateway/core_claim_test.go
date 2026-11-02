package toolgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"contractaudit/internal/store"
)

func TestCoreClaimResolverUsesBoundedToolCredentialAndRebuildsUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != coreToolClaimAuthorityPath || r.Header.Get("Authorization") != "Bearer tool-core-authority-test-token-000000000" {
			t.Errorf("Core claim request method=%q path=%q authorization=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode claim request: %v", err)
		}
		for _, forbidden := range []string{"actor_id", "principal_id", "membership_id", "grants"} {
			if _, exists := body[forbidden]; exists {
				t.Errorf("caller supplied authority field %q", forbidden)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(coreClaimProjection{
			JobID: "job", CaseID: "case", RunID: "run", AttemptID: "attempt", Audience: "worker_claim",
			MembershipBound: true, UserOrgID: "org-a", ActorID: "actor", DisplayName: "Directory label", Role: "reviewer",
			TenantID: "database-tenant", OrgID: "database-org", Enabled: true,
			Issuer: "https://issuer.example", Subject: "subject", PrincipalID: "principal", MembershipID: "membership",
		})
	}))
	defer server.Close()
	resolver, err := NewCoreClaimResolver(server.URL, "", true, "test", "tool-core-authority-test-token-000000000", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request := store.ToolInvocationContext{TenantID: "database-tenant", OrgID: "database-org", JobID: "job", WorkerID: "worker",
		Generation: 4, CaseID: "case", RunID: "run", AttemptID: "attempt"}
	authority, err := resolver.AuthorizeToolResolution(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !authority.User.HasResolvedMembership() || authority.User.TenantID != "database-tenant" || authority.User.Role != "reviewer" ||
		authority.User.OrgID != "org-a" || authority.OrgID != "database-org" || authority.User.PrincipalID != "principal" || authority.User.MembershipID != "membership" || authority.CaseID != "case" {
		t.Fatalf("Core claim projection was not reconstructed: %+v", authority)
	}
	if _, err := NewCoreClaimResolver(server.URL, "", true, "test", "short", time.Second); err == nil {
		t.Fatal("short Tool Core authority credential was accepted")
	}
}
