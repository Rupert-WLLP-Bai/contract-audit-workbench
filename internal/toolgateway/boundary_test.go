package toolgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

type gatewayFakeStore struct {
	user             accounts.User
	sessionAudience  string
	workspace        store.Workspace
	workspaceErr     error
	userCalls        int
	workspaceCalls   int
	reserveCalls     int
	finishCalls      int
	reserveRequest   store.ToolCallRequest
	finishStatus     string
	finishOutputSize int64
	cancelRequest    context.CancelFunc
}

func (f *gatewayFakeStore) User(token string) (accounts.User, error) {
	f.userCalls++
	if token != "session-token" {
		return accounts.User{}, fault.Unauthenticated("invalid token")
	}
	return f.user, nil
}

func (f *gatewayFakeStore) SessionAudience(token string) (string, error) {
	if token != "session-token" {
		return "", fault.Unauthenticated("invalid token")
	}
	if f.sessionAudience == "" {
		return "client", nil
	}
	return f.sessionAudience, nil
}

func (f *gatewayFakeStore) WorkspaceAttempt(_ accounts.User, _, _, _ string) (store.Workspace, error) {
	f.workspaceCalls++
	if f.workspaceErr != nil {
		return store.Workspace{}, f.workspaceErr
	}
	return f.workspace, nil
}

func (f *gatewayFakeStore) ReserveToolCall(_ context.Context, _ accounts.User, in store.ToolCallRequest) (string, error) {
	f.reserveCalls++
	f.reserveRequest = in
	if f.cancelRequest != nil {
		f.cancelRequest()
	}
	return "call-1", nil
}

func (f *gatewayFakeStore) FinishToolCall(_ context.Context, _ accounts.User, _ string, status string, outputBytes int64) error {
	f.finishCalls++
	f.finishStatus = status
	f.finishOutputSize = outputBytes
	return nil
}

func TestMCPProfileRejectsMirroredMethodAndToolMismatchBeforeAuthentication(t *testing.T) {
	for _, test := range []struct {
		name       string
		method     string
		headMethod string
		tool       string
		headTool   string
	}{
		{name: "method", method: "tools/list", headMethod: "tools/call"},
		{name: "tool", method: "tools/call", headMethod: "tools/call", tool: ToolName, headTool: "other_tool"},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			request := mcpRequest(test.method, test.tool, map[string]any{}, time.Now().Add(30*time.Second))
			request.Header.Set("Mcp-Method", test.headMethod)
			if test.headTool != "" {
				request.Header.Set("Mcp-Name", test.headTool)
			}
			recorder := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || backend.userCalls != 0 || backend.workspaceCalls != 0 || backend.reserveCalls != 0 {
				t.Fatalf("mismatch executed or was not rejected: status=%d user=%d workspace=%d reserve=%d", recorder.Code, backend.userCalls, backend.workspaceCalls, backend.reserveCalls)
			}
			if got := rpcErrorCode(t, recorder); got != codeHeaderMismatch {
				t.Fatalf("error code = %d, want %d", got, codeHeaderMismatch)
			}
		})
	}
}

func TestMCPProfileRejectsLegacySessionAndVersionBoundaries(t *testing.T) {
	t.Run("protocol header mismatch", func(t *testing.T) {
		backend := testBackend(t)
		request := mcpRequest("tools/list", "", nil, time.Now().Add(30*time.Second))
		request.Header.Set("MCP-Protocol-Version", "2025-11-25")
		recorder := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || rpcErrorCode(t, recorder) != codeHeaderMismatch || backend.userCalls != 0 {
			t.Fatalf("protocol header mismatch status=%d user_calls=%d body=%s", recorder.Code, backend.userCalls, recorder.Body.String())
		}
	})

	t.Run("unsupported profile", func(t *testing.T) {
		backend := testBackend(t)
		meta := testMeta()
		meta["io.modelcontextprotocol/protocolVersion"] = "2025-11-25"
		body, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/list",
			"params": map[string]any{"_meta": meta},
		})
		if err != nil {
			t.Fatal(err)
		}
		request := mcpRequest("tools/list", "", nil, time.Now().Add(30*time.Second))
		request.Body = io.NopCloser(strings.NewReader(string(body)))
		request.Header.Set("MCP-Protocol-Version", "2025-11-25")
		recorder := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || rpcErrorCode(t, recorder) != codeUnsupportedProtocol || backend.userCalls != 0 {
			t.Fatalf("unsupported version status=%d user_calls=%d body=%s", recorder.Code, backend.userCalls, recorder.Body.String())
		}
	})

	t.Run("initialize is not implemented", func(t *testing.T) {
		backend := testBackend(t)
		request := mcpRequest("initialize", "", nil, time.Now().Add(30*time.Second))
		recorder := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound || rpcErrorCode(t, recorder) != codeMethodNotFound || backend.userCalls != 0 {
			t.Fatalf("initialize was served: status=%d user_calls=%d", recorder.Code, backend.userCalls)
		}
	})

	t.Run("session header is rejected", func(t *testing.T) {
		backend := testBackend(t)
		request := mcpRequest("tools/list", "", nil, time.Now().Add(30*time.Second))
		request.Header.Set("MCP-Session-Id", "unexpected-session")
		recorder := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || backend.userCalls != 0 {
			t.Fatalf("session header was accepted: status=%d user_calls=%d", recorder.Code, backend.userCalls)
		}
	})
}

func TestGatewayRejectsInvalidSessionWithoutLeakingDetails(t *testing.T) {
	backend := testBackend(t)
	request := mcpRequest("tools/list", "", nil, time.Now().Add(30*time.Second))
	request.Header.Set("Cookie", "session=invalid-token")
	recorder := httptest.NewRecorder()
	NewHandler(backend, Options{}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized || rpcErrorCode(t, recorder) != codeUnauthenticated || backend.userCalls != 1 {
		t.Fatalf("invalid session status=%d user_calls=%d body=%s", recorder.Code, backend.userCalls, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "invalid token") {
		t.Fatalf("authentication details leaked: %s", recorder.Body.String())
	}
}

func TestGatewayRejectsAdminAudienceSessions(t *testing.T) {
	backend := testBackend(t)
	backend.sessionAudience = "admin"
	request := mcpRequest("tools/list", "", nil, time.Now().Add(30*time.Second))
	recorder := httptest.NewRecorder()
	NewHandler(backend, Options{}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized || rpcErrorCode(t, recorder) != codeUnauthenticated || backend.workspaceCalls != 0 || backend.reserveCalls != 0 {
		t.Fatalf("admin audience was accepted: status=%d workspace=%d reserve=%d", recorder.Code, backend.workspaceCalls, backend.reserveCalls)
	}
}

func TestOriginAllowlistAndBearerAuthentication(t *testing.T) {
	t.Run("unlisted origin", func(t *testing.T) {
		backend := testBackend(t)
		request := mcpRequest("tools/list", "", nil, time.Now().Add(30*time.Second))
		request.Header.Set("Origin", "https://portal.attacker.example")
		recorder := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden || backend.userCalls != 0 {
			t.Fatalf("unlisted origin status=%d user_calls=%d", recorder.Code, backend.userCalls)
		}
	})

	t.Run("allowlisted origin and bearer token", func(t *testing.T) {
		backend := testBackend(t)
		request := mcpRequest("tools/list", "", nil, time.Now().Add(30*time.Second))
		request.Header.Set("Origin", "https://portal.example")
		request.Header.Del("Cookie")
		request.Header.Set("Authorization", "Bearer session-token")
		recorder := httptest.NewRecorder()
		NewHandler(backend, Options{AllowedOrigins: []string{"https://portal.example"}}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || backend.userCalls != 1 {
			t.Fatalf("allowlisted bearer request status=%d user_calls=%d body=%s", recorder.Code, backend.userCalls, recorder.Body.String())
		}
	})
	t.Run("preflight permits only MCP request headers", func(t *testing.T) {
		backend := testBackend(t)
		request := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
		request.Header.Set("Origin", "https://portal.example")
		request.Header.Set("Access-Control-Request-Method", http.MethodPost)
		request.Header.Set("Access-Control-Request-Headers", "content-type, mcp-method, mcp-name, mcp-protocol-version, authorization")
		recorder := httptest.NewRecorder()
		NewHandler(backend, Options{AllowedOrigins: []string{"https://portal.example"}}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent || recorder.Header().Get("Access-Control-Allow-Origin") != "https://portal.example" || recorder.Header().Get("Access-Control-Allow-Credentials") != "true" || backend.userCalls != 0 {
			t.Fatalf("preflight status=%d headers=%v user_calls=%d", recorder.Code, recorder.Header(), backend.userCalls)
		}
	})

	t.Run("preflight rejects unsupported headers", func(t *testing.T) {
		backend := testBackend(t)
		request := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
		request.Header.Set("Origin", "https://portal.example")
		request.Header.Set("Access-Control-Request-Method", http.MethodPost)
		request.Header.Set("Access-Control-Request-Headers", "content-type, x-arbitrary")
		recorder := httptest.NewRecorder()
		NewHandler(backend, Options{AllowedOrigins: []string{"https://portal.example"}}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || backend.userCalls != 0 {
			t.Fatalf("unsupported header preflight status=%d user_calls=%d", recorder.Code, backend.userCalls)
		}
	})
}

func TestMCPProfileRejectsDuplicateTrailingAndUnsupportedArguments(t *testing.T) {
	t.Run("trailing JSON", func(t *testing.T) {
		backend := testBackend(t)
		request := mcpRequest("tools/list", "", nil, time.Now().Add(30*time.Second))
		body, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/list",
			"params": map[string]any{"_meta": testMeta()},
		})
		if err != nil {
			t.Fatal(err)
		}
		request.Body = io.NopCloser(strings.NewReader(string(body) + ` {}`))
		recorder := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || backend.userCalls != 0 {
			t.Fatalf("trailing JSON was accepted: status=%d user_calls=%d", recorder.Code, backend.userCalls)
		}
	})

	t.Run("duplicate key", func(t *testing.T) {
		backend := testBackend(t)
		request := mcpRequest("tools/list", "", nil, time.Now().Add(30*time.Second))
		body, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/list",
			"params": map[string]any{"_meta": testMeta()},
		})
		if err != nil {
			t.Fatal(err)
		}
		duplicate := strings.Replace(string(body), `"jsonrpc":"2.0"`, `"jsonrpc":"2.0","jsonrpc":"2.0"`, 1)
		request.Body = io.NopCloser(strings.NewReader(duplicate))
		recorder := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || backend.userCalls != 0 {
			t.Fatalf("duplicate JSON key was accepted: status=%d user_calls=%d", recorder.Code, backend.userCalls)
		}
	})

	t.Run("unknown argument", func(t *testing.T) {
		backend := testBackend(t)
		request := mcpRequest("tools/call", ToolName, map[string]any{
			"case_id": "case-1", "run_id": "run-1", "attempt_id": "attempt-1",
			"capability_id": "dedicated_line.minimum_bandwidth", "deadline": time.Now().Add(30 * time.Second).Format(time.RFC3339Nano),
			"tenant_id": "attacker-tenant",
		}, time.Now().Add(30*time.Second))
		recorder := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || backend.workspaceCalls != 0 || backend.reserveCalls != 0 {
			t.Fatalf("unsupported argument reached execution: status=%d workspace=%d reserve=%d", recorder.Code, backend.workspaceCalls, backend.reserveCalls)
		}
	})

	t.Run("unknown tool", func(t *testing.T) {
		backend := testBackend(t)
		request := mcpRequest("tools/call", "not_registered", map[string]any{}, time.Now().Add(30*time.Second))
		recorder := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || backend.workspaceCalls != 0 || backend.reserveCalls != 0 || backend.finishCalls != 0 {
			t.Fatalf("unknown tool executed: status=%d workspace=%d reserve=%d finish=%d", recorder.Code, backend.workspaceCalls, backend.reserveCalls, backend.finishCalls)
		}
	})
}

func TestMCPRequiresBothResponseMediaTypesInAccept(t *testing.T) {
	for _, test := range []struct {
		name   string
		accept string
	}{
		{name: "missing", accept: ""},
		{name: "JSON only", accept: "application/json"},
		{name: "JSON quality zero", accept: "application/json;q=0, text/event-stream"},
		{name: "invalid JSON quality", accept: "application/json;q=NaN, text/event-stream"},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			request := mcpRequest("tools/list", "", nil, time.Now().Add(30*time.Second))
			request.Header.Set("Accept", test.accept)
			recorder := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNotAcceptable || backend.userCalls != 0 {
				t.Fatalf("invalid Accept status=%d user_calls=%d", recorder.Code, backend.userCalls)
			}
		})
	}
}

func TestFrozenEvidenceRejectsUnsupportedCapabilityAndDeadline(t *testing.T) {
	for _, test := range []struct {
		name         string
		capabilityID string
		deadline     time.Time
	}{
		{name: "unsupported capability", capabilityID: "dedicated_line.anything", deadline: time.Now().Add(30 * time.Second)},
		{name: "expired deadline", capabilityID: "dedicated_line.minimum_bandwidth", deadline: time.Now().Add(-time.Second)},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			request := mcpRequest("tools/call", ToolName, map[string]any{
				"case_id": "case-1", "run_id": "run-1", "attempt_id": "attempt-1",
				"capability_id": test.capabilityID, "deadline": test.deadline.Format(time.RFC3339Nano),
			}, time.Now().Add(30*time.Second))
			recorder := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || backend.workspaceCalls != 0 || backend.reserveCalls != 0 {
				t.Fatalf("invalid tool arguments reached storage: status=%d workspace=%d reserve=%d", recorder.Code, backend.workspaceCalls, backend.reserveCalls)
			}
		})
	}
}

func TestMCPStatelessDiscoveryAndToolList(t *testing.T) {
	backend := testBackend(t)
	for _, method := range []string{"server/discover", "tools/list"} {
		request := mcpRequest(method, "", nil, time.Now().Add(30*time.Second))
		recorder := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", method, recorder.Code, recorder.Body.String())
		}
		if recorder.Header().Get("MCP-Session-Id") != "" {
			t.Fatalf("%s emitted a protocol session id", method)
		}
		var response map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response["id"] != float64(1) {
			t.Fatalf("%s response id=%v", method, response["id"])
		}
	}
	if backend.userCalls != 2 || backend.reserveCalls != 0 || backend.workspaceCalls != 0 {
		t.Fatalf("read-only protocol methods reached tool execution: user=%d workspace=%d reserve=%d", backend.userCalls, backend.workspaceCalls, backend.reserveCalls)
	}
}

func TestFrozenEvidenceReadReturnsSelectedFrozenCitationAndChargesSerializedContent(t *testing.T) {
	backend := testBackend(t)
	request := mcpRequest("tools/call", ToolName, map[string]any{
		"case_id": "case-1", "run_id": "run-1", "attempt_id": "attempt-1",
		"capability_id": "dedicated_line.minimum_bandwidth", "deadline": time.Now().Add(30 * time.Second).Format(time.RFC3339Nano),
	}, time.Now().Add(30*time.Second))
	recorder := httptest.NewRecorder()
	NewHandler(backend, Options{}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Result struct {
			Content []rpcContent `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Result.Content) != 1 {
		t.Fatalf("content=%+v", response.Result.Content)
	}
	var evidence evidencePayload
	if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Status != "found" || len(evidence.Facts) != 1 || evidence.Facts[0].Value != "100" || evidence.Facts[0].Unit != "Mbit/s" {
		t.Fatalf("unexpected extracted evidence: %+v", evidence)
	}
	cite := evidence.Facts[0].Citations[0]
	if cite.DocumentID != "doc-1" || cite.ParsedDocumentID != "parsed-1" || cite.OriginalDigest != "original-digest" || cite.Quote != "电路 LINE-001：最低保障带宽 100 Mbit/s。" {
		t.Fatalf("citation lost frozen identity: %+v", cite)
	}
	if backend.reserveCalls != 1 || backend.finishCalls != 1 || backend.finishStatus != "completed" {
		t.Fatalf("reservation lifecycle=%d/%d status=%s", backend.reserveCalls, backend.finishCalls, backend.finishStatus)
	}
	if backend.reserveRequest.ToolVersion != ToolVersion || backend.reserveRequest.CapabilityID != evidence.CapabilityID || backend.reserveRequest.Deadline.IsZero() {
		t.Fatalf("reservation did not carry frozen tool identity: %+v", backend.reserveRequest)
	}
	if backend.finishOutputSize != int64(len(response.Result.Content[0].Text)) {
		t.Fatalf("finish bytes=%d, serialized result content=%d", backend.finishOutputSize, len(response.Result.Content[0].Text))
	}
}

func TestFrozenEvidenceReadFinishesCancelledReservation(t *testing.T) {
	backend := testBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	backend.cancelRequest = cancel
	request := mcpRequest("tools/call", ToolName, map[string]any{
		"case_id": "case-1", "run_id": "run-1", "attempt_id": "attempt-1",
		"capability_id": "dedicated_line.minimum_bandwidth", "deadline": time.Now().Add(30 * time.Second).Format(time.RFC3339Nano),
	}, time.Now().Add(30*time.Second))
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()
	NewHandler(backend, Options{}).ServeHTTP(recorder, request)
	if backend.reserveCalls != 1 || backend.finishCalls != 1 || backend.finishStatus != "cancelled" || backend.finishOutputSize != 0 {
		t.Fatalf("cancellation accounting=%d/%d status=%s bytes=%d", backend.reserveCalls, backend.finishCalls, backend.finishStatus, backend.finishOutputSize)
	}
}

func TestStoreFailureDoesNotExposeDatabaseDetails(t *testing.T) {
	backend := testBackend(t)
	backend.workspaceErr = errors.New("postgres password=secret connection details")
	request := mcpRequest("tools/call", ToolName, map[string]any{
		"case_id": "case-1", "run_id": "run-1", "attempt_id": "attempt-1",
		"capability_id": "dedicated_line.minimum_bandwidth", "deadline": time.Now().Add(30 * time.Second).Format(time.RFC3339Nano),
	}, time.Now().Add(30*time.Second))
	recorder := httptest.NewRecorder()
	NewHandler(backend, Options{}).ServeHTTP(recorder, request)
	if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "postgres") {
		t.Fatalf("store internals leaked: %s", recorder.Body.String())
	}
	if recorder.Code != http.StatusInternalServerError || backend.reserveCalls != 0 {
		t.Fatalf("store failure status=%d reserve_calls=%d", recorder.Code, backend.reserveCalls)
	}
}

func testBackend(t *testing.T) *gatewayFakeStore {
	t.Helper()
	user, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo user unavailable")
	}
	text := "电路 LINE-001：最低保障带宽 100 Mbit/s。"
	digest := sha256.Sum256([]byte(text))
	normalizedDigest := hex.EncodeToString(digest[:])
	inputHash := "frozen-input-hash"
	return &gatewayFakeStore{
		user: user,
		workspace: store.Workspace{
			Case:     store.Case{ID: "case-1", TenantID: user.TenantID, BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"},
			Snapshot: store.RunSnapshot{RunID: "run-1"},
			Execution: &store.Execution{Attempt: &store.Attempt{
				ID: "attempt-1", RunID: "run-1", HistoryKind: "frozen", InputHash: &inputHash,
				ParsedDocumentIDs: []string{"parsed-1"},
			}},
			Documents: []store.Interpreted{{
				Document: store.Document{ID: "doc-1", TenantID: user.TenantID, CaseID: "case-1", Role: "draft_contract", Filename: "draft.txt", Digest: "original-digest"},
				Ready:    true, Text: text,
				ParsedDocument: &store.ParsedDocument{
					ID: "parsed-1", DocumentID: "doc-1", OriginalDigest: "original-digest", ParserID: "text-parser", ParserVersion: "1",
					ConfigHash: "config-hash", NormalizationVersion: "norm-v1", NormalizedDigest: normalizedDigest,
					ArtifactDigest: "artifact-digest", Status: "ready", Text: text,
				},
			}},
		},
	}
}

func mcpRequest(method, toolName string, args map[string]any, _ time.Time) *http.Request {
	params := map[string]any{"_meta": testMeta()}
	if method == "tools/call" {
		params["name"] = toolName
		params["arguments"] = args
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		panic(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	request.Header.Set("Mcp-Method", method)
	if method == "tools/call" {
		request.Header.Set("Mcp-Name", toolName)
	}
	request.AddCookie(&http.Cookie{Name: "session", Value: "session-token"})
	return request
}

func testMeta() map[string]any {
	return map[string]any{
		"io.modelcontextprotocol/protocolVersion":    ProtocolVersion,
		"io.modelcontextprotocol/clientInfo":         map[string]string{"name": "boundary-test", "version": "1.0"},
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
	}
}

func rpcErrorCode(t *testing.T, recorder *httptest.ResponseRecorder) int {
	t.Helper()
	var response struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Error.Code
}
