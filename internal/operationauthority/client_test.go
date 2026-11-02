package operationauthority

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
)

func TestOperationAuthorityRestoresOnlyExactAuthenticatedProjection(t *testing.T) {
	scope := model.DelegatedScope{Kind: "execution", TenantID: "tenant-a", OrgID: "org-a", ActorID: "actor-a", CaseID: "case-a", JobID: "job-a", WorkerID: "worker-a", Generation: 3, RunID: "run-a", AttemptID: "attempt-a"}
	mode := "valid"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/model/operation-authority" || r.Header.Get("Authorization") != "Bearer private-model-authority-credential-000000" {
			w.WriteHeader(401)
			return
		}
		var received model.DelegatedScope
		if json.NewDecoder(r.Body).Decode(&received) != nil {
			w.WriteHeader(400)
			return
		}
		p := Projection{Scope: received, MembershipBound: true, PrincipalID: "principal-a", MembershipID: "membership-a", User: accounts.User{ID: "actor-a", Enabled: true, Role: "reviewer", TenantID: "tenant-a", OrgID: "org-a", Issuer: "https://issuer.invalid", Subject: "subject-a", PrincipalID: "principal-a", MembershipID: "membership-a"}}
		switch mode {
		case "other scope":
			p.Scope.Generation++
		case "unbound enterprise":
			p.MembershipBound = false
		case "missing membership":
			p.MembershipID = ""
		}
		_ = json.NewEncoder(w).Encode(p)
	}))
	defer server.Close()
	client, err := NewOperationClient(server.URL, "model", "private-model-authority-credential-000000", "", "test", true)
	if err != nil {
		t.Fatal(err)
	}
	u, err := client.ResolveOperation(context.Background(), scope)
	if err != nil || !u.HasResolvedMembership() || u.MembershipID != "membership-a" {
		t.Fatalf("trusted projection lost: %v", err)
	}
	for _, value := range []string{"other scope", "unbound enterprise", "missing membership"} {
		mode = value
		if _, err := client.ResolveOperation(context.Background(), scope); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}
