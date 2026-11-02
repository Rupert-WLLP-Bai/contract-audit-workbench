package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/coreauthority"
	"contractaudit/internal/store"
)

func TestCoreWorkerClaimAuthoritySendsOnlyClaimTupleAndChecksProjection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != workerClaimAuthorityPath || r.Header.Get("Authorization") != "Bearer worker-core-authority-test-token-000000" {
			t.Errorf("Core worker request method=%q path=%q authorization=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode worker claim: %v", err)
		}
		for _, forbidden := range []string{"actor_id", "principal_id", "membership_id", "grants"} {
			if _, exists := body[forbidden]; exists {
				t.Errorf("worker caller supplied authority field %q", forbidden)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(workerClaimAuthorityResponse{
			JobID: "job", CaseID: "case", RunID: "run", AttemptID: "attempt", Audience: "worker_claim",
			MembershipBound: true, UserOrgID: "org-a", ActorID: "actor", DisplayName: "Core display", Grants: []access.Grant{{Role: "reviewer", Scope: "case", CaseID: "case"}}, Role: "reviewer", TenantID: "tenant", OrgID: "org", Enabled: true,
			Issuer: "https://issuer.example", Subject: "subject", PrincipalID: "principal", MembershipID: "membership",
		})
	}))
	defer server.Close()
	endpoint, client, err := newWorkerTestTransport(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &coreWorkerClaimAuthority{endpoint: endpoint, http: client, token: "worker-core-authority-test-token-000000"}
	claim := store.JobClaim{JobID: "job", TenantID: "tenant", OrgID: "org", CaseID: "case", RunID: "run", AttemptID: "attempt",
		ActorID: "actor", WorkerID: "worker", Generation: 4}
	if err := resolver.Resolve(context.Background(), claim); err != nil {
		t.Fatal(err)
	}
}

func newWorkerTestTransport(origin string) (string, *http.Client, error) {
	return coreauthority.NewTransport(origin, workerClaimAuthorityPath, "", "test", true, time.Second)
}
