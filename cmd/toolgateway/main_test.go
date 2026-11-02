package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/store"
)

func TestSessionResolverFromEnvRequiresBoundedAuthorityTransport(t *testing.T) {
	for _, name := range []string{"CORE_AUTHORITY_URL", "CORE_AUTHORITY_CA_FILE", "CORE_AUTHORITY_ALLOW_INSECURE_DEV", "APP_ENV"} {
		t.Setenv(name, "")
	}
	if _, err := sessionResolverFromEnv(); err == nil {
		t.Fatal("missing Core authority URL was accepted")
	}
	t.Setenv("APP_ENV", "test")
	t.Setenv("CORE_AUTHORITY_URL", "http://127.0.0.1:8081")
	t.Setenv("CORE_AUTHORITY_ALLOW_INSECURE_DEV", "true")
	if _, err := sessionResolverFromEnv(); err != nil {
		t.Fatalf("explicit bounded test authority was rejected: %v", err)
	}
	t.Setenv("APP_ENV", "production")
	if _, err := sessionResolverFromEnv(); err == nil {
		t.Fatal("development HTTP authority was accepted in production")
	}
	t.Setenv("CORE_AUTHORITY_ALLOW_INSECURE_DEV", "false")
	t.Setenv("CORE_AUTHORITY_URL", "https://core.example")
	if _, err := sessionResolverFromEnv(); err != nil {
		t.Fatalf("secure HTTPS authority was rejected: %v", err)
	}
}

const (
	edgeCredential   = "edge-credential-0123456789-abcdef"
	workerCredential = "worker-credential-0123456789-abcdef"
)

// fakeBackend implements the trusted tool boundary plus the claim resolver, so
// the assembled handler can be exercised without a database.
type fakeBackend struct {
	resolutions atomic.Int64
}

func (b *fakeBackend) AuthenticateSession(context.Context, string) (accounts.User, string, error) {
	return accounts.User{ID: "demo-admin", Role: "admin", TenantID: "local-demo", OrgID: "demo-org", Enabled: true}, "client", nil
}

func (b *fakeBackend) WorkspaceAttempt(accounts.User, string, string, string) (store.Workspace, error) {
	return store.Workspace{}, nil
}

func (b *fakeBackend) ReserveToolCall(context.Context, accounts.User, store.ToolCallRequest) (string, error) {
	return "call_1", nil
}

func (b *fakeBackend) FinishToolCall(context.Context, accounts.User, string, string, int64) error {
	return nil
}

func (b *fakeBackend) AuthorizeToolResolution(context.Context, store.ToolInvocationContext) (store.ToolAuthority, error) {
	b.resolutions.Add(1)
	return store.ToolAuthority{User: accounts.User{ID: "demo-admin", TenantID: "local-demo", OrgID: "demo-org", Enabled: true}}, nil
}

func workerRequest(t *testing.T, serviceKey string, jobContext string) *http.Request {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": "1", "method": "tools/call",
		"params": map[string]any{
			"_meta": map[string]any{
				"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
				"io.modelcontextprotocol/clientCapabilities": map[string]any{},
				"io.modelcontextprotocol/clientInfo":         map[string]string{"name": "test", "version": "1"},
			},
			"name": "frozen_evidence_read",
			"arguments": map[string]any{
				"case_id": "case_1", "run_id": "run_1", "attempt_id": "att_1",
				"capability_id": "dedicated_line.minimum_bandwidth",
				"deadline":      time.Now().UTC().Add(30 * time.Second).Format(time.RFC3339),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2026-07-28")
	request.Header.Set("Mcp-Method", "tools/call")
	request.Header.Set("Mcp-Name", "frozen_evidence_read")
	if serviceKey != "" {
		request.Header.Set("X-Contract-Service-Key", serviceKey)
	}
	if jobContext != "" {
		request.Header.Set("X-Contract-Job-Context", jobContext)
	}
	return request
}

const jobContext = `{"tenant_id":"local-demo","org_id":"demo-org","job_id":"job_1","worker_id":"worker_1","generation":1,"attempt_id":"att_1"}`

// The middleware and the MCP handler are assembled here exactly as the binary
// assembles them: a worker credential with a job context must reach the claim
// resolver, while the edge credential without one must never take the worker
// branch, and a mismatched pair must be refused before the handler runs.
func TestAssembledToolGatewayDiscriminatesCallerBranches(t *testing.T) {
	backend := &fakeBackend{}
	handler := mcpHandler(backend, nil, edgeCredential, workerCredential)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, workerRequest(t, workerCredential, jobContext))
	if response.Code == http.StatusUnauthorized {
		t.Fatalf("a valid worker call was refused: %d %s", response.Code, response.Body.String())
	}
	if backend.resolutions.Load() != 1 {
		t.Fatalf("the worker call did not reach the claim resolver: %s", response.Body.String())
	}

	for name, request := range map[string]*http.Request{
		"worker credential without a job context":   workerRequest(t, workerCredential, ""),
		"edge credential with a job context":        workerRequest(t, edgeCredential, jobContext),
		"no credential with a job context":          workerRequest(t, "", jobContext),
		"edge credential with an empty job context": workerRequest(t, edgeCredential, ""),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if name == "edge credential with an empty job context" {
			if response.Code == http.StatusUnauthorized {
				continue // the public branch without a session is expected to refuse
			}
		}
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s answered %d instead of 401", name, response.Code)
		}
	}
	if backend.resolutions.Load() != 1 {
		t.Fatalf("a refused call still reached the resolver: %d", backend.resolutions.Load())
	}
}
