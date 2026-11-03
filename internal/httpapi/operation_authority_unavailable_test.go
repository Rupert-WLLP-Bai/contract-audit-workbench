package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"contractaudit/internal/model"
	"contractaudit/internal/operationauthority"
	"contractaudit/internal/store"
)

// A real Core router backed by a closed PostgreSQL pool must not assert that
// an actor was revoked. This checks the HTTP/client failure contract only;
// the independent execution-role state transition needs its own PG fixture.
func TestOperationAuthorityCoreDatabaseUnavailableIsNotRevocation(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")) == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	workerKey, gatewayKey := strings.Repeat("w", 40), strings.Repeat("g", 40)
	handler, err := ServiceHandler(st, Options{Durable: true, ExperienceWorkerCredential: workerKey}, "core", gatewayKey)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := operationauthority.NewOperationClient(server.URL, "worker", workerKey, "", "test", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	scope := model.DelegatedScope{Kind: "execution", TenantID: "tenant", OrgID: "org", CaseID: "case", ActorID: "actor", JobID: "job", WorkerID: "worker", Generation: 1, RunID: "run", AttemptID: "attempt"}
	_, err = client.ResolveOperation(context.Background(), scope)
	if err == nil || errors.Is(err, operationauthority.ErrConfirmedRevocation) {
		t.Fatalf("Core DB unavailable classified as revocation: %v", err)
	}
}
