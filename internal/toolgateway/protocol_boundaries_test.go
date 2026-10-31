package toolgateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

type protocolBoundaryBackend struct {
	*gatewayFakeStore
	userErr       error
	audienceErr   error
	workspaceRead func(int) (store.Workspace, error)
	reserveID     *string
	reserveErr    error
	finishErr     error
	onReserve     func(context.Context)
}

func (b *protocolBoundaryBackend) AuthenticateSession(ctx context.Context, token string) (accounts.User, string, error) {
	if b.userErr != nil {
		b.userCalls++
		return accounts.User{}, "", b.userErr
	}
	user, audience, err := b.gatewayFakeStore.AuthenticateSession(ctx, token)
	if err == nil && b.audienceErr != nil {
		return accounts.User{}, "", b.audienceErr
	}
	return user, audience, err
}

func (b *protocolBoundaryBackend) WorkspaceAttempt(user accounts.User, caseID, runID, attemptID string) (store.Workspace, error) {
	if b.workspaceRead != nil {
		b.workspaceCalls++
		return b.workspaceRead(b.workspaceCalls)
	}
	return b.gatewayFakeStore.WorkspaceAttempt(user, caseID, runID, attemptID)
}

func (b *protocolBoundaryBackend) ReserveToolCall(ctx context.Context, user accounts.User, in store.ToolCallRequest) (string, error) {
	id, _ := b.gatewayFakeStore.ReserveToolCall(ctx, user, in)
	if b.onReserve != nil {
		b.onReserve(ctx)
	}
	if b.reserveID != nil {
		id = *b.reserveID
	}
	return id, b.reserveErr
}

func (b *protocolBoundaryBackend) FinishToolCall(ctx context.Context, user accounts.User, callID, status string, outputBytes int64) error {
	_ = b.gatewayFakeStore.FinishToolCall(ctx, user, callID, status, outputBytes)
	return b.finishErr
}

type protocolBoundaryResolver struct {
	*gatewayFakeStore
	resolveCalls int
	resolve      func(int, store.ToolInvocationContext) (store.ToolAuthority, error)
}

func (b *protocolBoundaryResolver) AuthorizeToolResolution(_ context.Context, in store.ToolInvocationContext) (store.ToolAuthority, error) {
	b.resolveCalls++
	if b.resolve != nil {
		return b.resolve(b.resolveCalls, in)
	}
	return store.ToolAuthority{User: b.user}, nil
}

type protocolUnreadableBody struct{}

func (protocolUnreadableBody) Read([]byte) (int, error) {
	return 0, errors.New("private-read-failure")
}

func (protocolUnreadableBody) Close() error { return nil }

func protocolArguments() map[string]any {
	return map[string]any{
		"case_id": "case-1", "run_id": "run-1", "attempt_id": "attempt-1",
		"capability_id": "dedicated_line.minimum_bandwidth",
		"deadline":      time.Now().Add(30 * time.Second).Format(time.RFC3339Nano),
	}
}

func protocolRequest(t *testing.T, method string, mutate func(map[string]any)) *http.Request {
	t.Helper()
	request := mcpRequest(method, ToolName, protocolArguments(), time.Time{})
	if mutate == nil {
		return request
	}
	var body map[string]any
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	mutate(body)
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request.Body = io.NopCloser(strings.NewReader(string(encoded)))
	return request
}

func protocolFailure(t *testing.T, response *httptest.ResponseRecorder, status, code int, publicCode string) json.RawMessage {
	t.Helper()
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code    int               `json:"code"`
			Message string            `json:"message"`
			Data    map[string]string `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode failure response: %v", err)
	}
	if response.Code != status || envelope.JSONRPC != "2.0" || envelope.Error == nil || len(envelope.Result) != 0 {
		t.Fatalf("failure envelope status=%d, want %d; envelope=%+v", response.Code, status, envelope)
	}
	if envelope.Error.Code != code || envelope.Error.Message == "" {
		t.Fatalf("RPC failure code=%d, want %d; message=%q", envelope.Error.Code, code, envelope.Error.Message)
	}
	if publicCode != "" && envelope.Error.Data["code"] != publicCode {
		t.Fatalf("public error code=%q, want %q", envelope.Error.Data["code"], publicCode)
	}
	if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("gateway error response must be JSON and must not be cached")
	}
	if strings.Contains(response.Body.String(), "private-") || strings.Contains(response.Body.String(), "session-token") {
		t.Fatal("gateway response exposed private authentication or storage details")
	}
	return envelope.ID
}

func protocolEvidence(t *testing.T, response *httptest.ResponseRecorder) evidencePayload {
	t.Helper()
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		Error   json.RawMessage `json:"error"`
		Result  toolCallResult  `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || envelope.JSONRPC != "2.0" || len(envelope.Error) != 0 ||
		envelope.Result.ResultType != "complete" || envelope.Result.IsError || len(envelope.Result.Content) != 1 || envelope.Result.Content[0].Type != "text" {
		t.Fatalf("unexpected evidence response: status=%d envelope=%+v", response.Code, envelope)
	}
	var evidence evidencePayload
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.SchemaVersion != "frozen-evidence/v1" || evidence.ToolVersion != "frozen-evidence/v1" || evidence.Facts == nil {
		t.Fatalf("invalid evidence contract: %+v", evidence)
	}
	return evidence
}

func protocolSourceText(backend *gatewayFakeStore, text string) {
	item := &backend.workspace.Documents[0]
	item.Text, item.ParsedDocument.Text = text, text
	digest := sha256.Sum256([]byte(text))
	item.ParsedDocument.NormalizedDigest = hex.EncodeToString(digest[:])
}

func TestProtocolBoundaryRejectsHTTPTransportAmbiguity(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*http.Request)
		status int
		code   int
	}{
		{"unsupported verb", func(r *http.Request) { r.Method = http.MethodPut }, 405, codeInvalidRequest},
		{"empty encoding header", func(r *http.Request) { r.Header["content-encoding"] = []string{""} }, 415, codeInvalidRequest},
		{"missing content type", func(r *http.Request) { r.Header.Del("Content-Type") }, 415, codeInvalidRequest},
		{"repeated content type", func(r *http.Request) { r.Header.Add("Content-Type", "application/json") }, 415, codeInvalidRequest},
		{"wrong content type", func(r *http.Request) { r.Header.Set("Content-Type", "text/json") }, 415, codeInvalidRequest},
		{"unsupported charset", func(r *http.Request) { r.Header.Set("Content-Type", "application/json; charset=iso-8859-1") }, 415, codeInvalidRequest},
		{"unsupported media parameter", func(r *http.Request) { r.Header.Set("Content-Type", "application/json; profile=private-profile") }, 415, codeInvalidRequest},
		{"malformed media type", func(r *http.Request) { r.Header.Set("Content-Type", "application/json; charset") }, 415, codeInvalidRequest},
		{"SSE quality out of range", func(r *http.Request) { r.Header.Set("Accept", "application/json, text/event-stream;q=1.1") }, 406, codeInvalidRequest},
		{"wildcard does not advertise SSE", func(r *http.Request) { r.Header.Set("Accept", "application/json, */*") }, 406, codeInvalidRequest},
		{"unreadable body", func(r *http.Request) { r.Body = protocolUnreadableBody{} }, 400, codeParseError},
		{"oversized body", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(strings.Repeat(" ", (64<<10)+1))) }, 413, codeInvalidRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			request := protocolRequest(t, "tools/list", nil)
			test.mutate(request)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, request)
			if id := protocolFailure(t, response, test.status, test.code, ""); len(id) != 0 {
				t.Fatalf("unparsed transport request was assigned an id: %s", id)
			}
			if backend.userCalls != 0 || backend.reserveCalls != 0 {
				t.Fatal("transport rejection reached authentication or tool execution")
			}
			if test.status == http.StatusMethodNotAllowed && response.Header().Get("Allow") != "POST, OPTIONS" {
				t.Fatal("unsupported HTTP method did not advertise supported methods")
			}
		})
	}
}

func TestProtocolBoundaryParsesSplitAcceptAndEncodedToolName(t *testing.T) {
	backend := testBackend(t)
	request := protocolRequest(t, "tools/call", nil)
	request.Header.Set("Content-Type", "Application/JSON; charset=UTF-8")
	request.Header["Accept"] = []string{"broken/media; q=invalid, application/json;q=0.5", "text/event-stream;q=1"}
	request.Header.Set("Mcp-Name", "=?base64?"+base64.StdEncoding.EncodeToString([]byte(ToolName))+"?=")
	response := httptest.NewRecorder()
	NewHandler(backend, Options{}).ServeHTTP(response, request)
	evidence := protocolEvidence(t, response)
	if evidence.Status != "found" || len(evidence.Facts) != 1 || evidence.Facts[0].Value != "100" {
		t.Fatalf("valid encoded mirrored header did not produce the selected evidence: %+v", evidence)
	}
}

func TestProtocolBoundaryRejectsMissingRepeatedAndMalformedMirrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		header string
		values []string
	}{
		{"missing version", "tools/list", "MCP-Protocol-Version", nil},
		{"repeated version", "tools/list", "MCP-Protocol-Version", []string{ProtocolVersion, ProtocolVersion}},
		{"missing method", "tools/list", "Mcp-Method", nil},
		{"repeated method", "tools/list", "Mcp-Method", []string{"tools/list", "tools/list"}},
		{"name on catalog", "tools/list", "Mcp-Name", []string{ToolName}},
		{"missing tool name", "tools/call", "Mcp-Name", nil},
		{"repeated tool name", "tools/call", "Mcp-Name", []string{ToolName, ToolName}},
		{"incomplete encoded name", "tools/call", "Mcp-Name", []string{"=?base64?ZnJvemVu"}},
		{"encoded suffix without prefix", "tools/call", "Mcp-Name", []string{"frozen_evidence_read?="}},
		{"invalid base64 name", "tools/call", "Mcp-Name", []string{"=?base64?%%%?="}},
		{"non UTF8 encoded name", "tools/call", "Mcp-Name", []string{"=?base64?/w==?="}},
		{"untrimmed tool name", "tools/call", "Mcp-Name", []string{" " + ToolName}},
		{"raw unicode tool name", "tools/call", "Mcp-Name", []string{"证据工具"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			request := protocolRequest(t, test.method, nil)
			request.Header[http.CanonicalHeaderKey(test.header)] = test.values
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, request)
			if id := protocolFailure(t, response, 400, codeHeaderMismatch, ""); string(id) != "1" {
				t.Fatalf("mirrored-header refusal lost the validated request id: %s", id)
			}
			var envelope struct {
				Error struct {
					Data map[string]string `json:"data"`
				} `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Data["header"] != test.header || backend.userCalls != 0 {
				t.Fatal("mirrored-header refusal did not identify the mismatch before authentication")
			}
		})
	}
}

func TestProtocolBoundaryRejectsMalformedContainersAndNestedAmbiguity(t *testing.T) {
	for _, test := range []struct{ name, body string }{
		{"empty", ""},
		{"invalid UTF8", "{\"private-\":\"" + string([]byte{0xff}) + "\"}"},
		{"array", "[]"},
		{"null", "null"},
		{"scalar", "12"},
		{"unterminated object", "{"},
		{"duplicate nested key", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"client":{"name":"a","name":"b"}}}}`},
		{"excessive array nesting", `{"params":` + strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33) + "}"},
		{"excessive object nesting", strings.Repeat(`{"x":`, 33) + "0" + strings.Repeat("}", 33)},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			request := protocolRequest(t, "tools/list", nil)
			request.Body = io.NopCloser(strings.NewReader(test.body))
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, request)
			protocolFailure(t, response, 400, codeParseError, "")
			if backend.userCalls != 0 {
				t.Fatal("invalid JSON reached authentication")
			}
		})
	}
}

func TestProtocolBoundaryValidatesEnvelopeTypesAndPreservesOnlyValidIDs(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
		code   int
		wantID string
	}{
		{"unknown envelope field", func(b map[string]any) { b["tenant_id"] = "private-tenant" }, codeInvalidRequest, ""},
		{"missing JSON RPC version", func(b map[string]any) { delete(b, "jsonrpc") }, codeInvalidRequest, ""},
		{"numeric JSON RPC version", func(b map[string]any) { b["jsonrpc"] = 2 }, codeInvalidRequest, ""},
		{"wrong JSON RPC version", func(b map[string]any) { b["jsonrpc"] = "1.0" }, codeInvalidRequest, ""},
		{"missing id", func(b map[string]any) { delete(b, "id") }, codeInvalidRequest, ""},
		{"null id", func(b map[string]any) { b["id"] = nil }, codeInvalidRequest, ""},
		{"boolean id", func(b map[string]any) { b["id"] = true }, codeInvalidRequest, ""},
		{"fractional id", func(b map[string]any) { b["id"] = 1.5 }, codeInvalidRequest, ""},
		{"exponential id", func(b map[string]any) { b["id"] = json.RawMessage("1e2") }, codeInvalidRequest, ""},
		{"object id", func(b map[string]any) { b["id"] = map[string]any{} }, codeInvalidRequest, ""},
		{"oversized id", func(b map[string]any) { b["id"] = strings.Repeat("x", 129) }, codeInvalidRequest, ""},
		{"missing method", func(b map[string]any) { delete(b, "method") }, codeInvalidRequest, "1"},
		{"null method", func(b map[string]any) { b["method"] = nil }, codeInvalidRequest, "1"},
		{"empty method", func(b map[string]any) { b["method"] = "" }, codeInvalidRequest, "1"},
		{"oversized method", func(b map[string]any) { b["method"] = strings.Repeat("x", 129) }, codeInvalidRequest, "1"},
		{"missing params", func(b map[string]any) { delete(b, "params") }, codeInvalidParams, "1"},
		{"null params", func(b map[string]any) { b["params"] = nil }, codeInvalidParams, "1"},
		{"array params", func(b map[string]any) { b["params"] = []any{} }, codeInvalidParams, "1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			request := protocolRequest(t, "tools/list", test.mutate)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, request)
			if id := protocolFailure(t, response, 400, test.code, ""); string(id) != test.wantID {
				t.Fatalf("response id=%s, want %q", id, test.wantID)
			}
			if backend.userCalls != 0 {
				t.Fatal("invalid JSON RPC envelope reached authentication")
			}
		})
	}
	for _, id := range []string{"9007199254740993", "-9007199254740993", `""`, `"` + strings.Repeat("a", 128) + `"`} {
		t.Run("valid id "+id, func(t *testing.T) {
			request := protocolRequest(t, "server/discover", func(b map[string]any) { b["id"] = json.RawMessage(id) })
			response := httptest.NewRecorder()
			NewHandler(testBackend(t), Options{}).ServeHTTP(response, request)
			var envelope struct {
				ID     json.RawMessage `json:"id"`
				Result struct {
					SupportedVersions []string `json:"supportedVersions"`
				} `json:"result"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if response.Code != 200 || string(envelope.ID) != id || len(envelope.Result.SupportedVersions) != 1 || envelope.Result.SupportedVersions[0] != ProtocolVersion {
				t.Fatalf("discovery did not preserve request identity and protocol: status=%d id=%s", response.Code, envelope.ID)
			}
		})
	}
}

func TestProtocolBoundaryRequiresTypedClientMetadata(t *testing.T) {
	const version = "io.modelcontextprotocol/protocolVersion"
	const capabilities = "io.modelcontextprotocol/clientCapabilities"
	const info = "io.modelcontextprotocol/clientInfo"
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing metadata", func(p map[string]any) { delete(p, "_meta") }},
		{"null metadata", func(p map[string]any) { p["_meta"] = nil }},
		{"array metadata", func(p map[string]any) { p["_meta"] = []any{} }},
		{"missing protocol", func(p map[string]any) { delete(p["_meta"].(map[string]any), version) }},
		{"numeric protocol", func(p map[string]any) { p["_meta"].(map[string]any)[version] = 1 }},
		{"empty protocol", func(p map[string]any) { p["_meta"].(map[string]any)[version] = "" }},
		{"missing capabilities", func(p map[string]any) { delete(p["_meta"].(map[string]any), capabilities) }},
		{"null capabilities", func(p map[string]any) { p["_meta"].(map[string]any)[capabilities] = nil }},
		{"array capabilities", func(p map[string]any) { p["_meta"].(map[string]any)[capabilities] = []any{} }},
		{"missing client info", func(p map[string]any) { delete(p["_meta"].(map[string]any), info) }},
		{"null client info", func(p map[string]any) { p["_meta"].(map[string]any)[info] = nil }},
		{"array client info", func(p map[string]any) { p["_meta"].(map[string]any)[info] = []any{} }},
		{"missing client name", func(p map[string]any) { delete(p["_meta"].(map[string]any)[info].(map[string]any), "name") }},
		{"null client name", func(p map[string]any) { p["_meta"].(map[string]any)[info].(map[string]any)["name"] = nil }},
		{"empty client name", func(p map[string]any) { p["_meta"].(map[string]any)[info].(map[string]any)["name"] = "" }},
		{"missing client version", func(p map[string]any) { delete(p["_meta"].(map[string]any)[info].(map[string]any), "version") }},
		{"boolean client version", func(p map[string]any) { p["_meta"].(map[string]any)[info].(map[string]any)["version"] = true }},
		{"empty client version", func(p map[string]any) { p["_meta"].(map[string]any)[info].(map[string]any)["version"] = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := protocolRequest(t, "tools/list", func(b map[string]any) { test.mutate(b["params"].(map[string]any)) })
			backend := testBackend(t)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, request)
			if id := protocolFailure(t, response, 400, codeInvalidParams, ""); string(id) != "1" || backend.userCalls != 0 {
				t.Fatal("invalid request metadata was not rejected with its request id before authentication")
			}
		})
	}
}

func TestProtocolBoundaryDeniesAmbiguousCredentials(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"missing credentials", func(r *http.Request) { r.Header.Del("Cookie") }},
		{"duplicate session cookies", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "session", Value: "session-token"}) }},
		{"oversized session cookie", func(r *http.Request) { r.Header.Set("Cookie", "session="+strings.Repeat("x", 4097)) }},
		{"repeated authorization", func(r *http.Request) {
			r.Header["Authorization"] = []string{"Bearer session-token", "Bearer session-token"}
		}},
		{"wrong authorization scheme", func(r *http.Request) { r.Header.Set("Authorization", "Basic private-basic") }},
		{"missing bearer value", func(r *http.Request) { r.Header.Set("Authorization", "Bearer") }},
		{"extra bearer field", func(r *http.Request) { r.Header.Set("Authorization", "Bearer session-token extra") }},
		{"oversized bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 4097)) }},
		{"conflicting cookie and bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer private-other-token") }},
		{"non ASCII bearer", func(r *http.Request) { r.Header.Del("Cookie"); r.Header.Set("Authorization", "Bearer 令牌") }},
		{"control byte bearer", func(r *http.Request) { r.Header.Del("Cookie"); r.Header.Set("Authorization", "Bearer token\x7f") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			request := protocolRequest(t, "tools/list", nil)
			test.mutate(request)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, request)
			protocolFailure(t, response, 401, codeUnauthenticated, "unauthenticated")
			if backend.userCalls != 0 || backend.reserveCalls != 0 {
				t.Fatal("ambiguous credentials reached the backend")
			}
		})
	}
	t.Run("matching credentials", func(t *testing.T) {
		backend := testBackend(t)
		request := protocolRequest(t, "tools/call", nil)
		request.Header.Set("Authorization", "bEaReR session-token")
		response := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(response, request)
		if evidence := protocolEvidence(t, response); evidence.Status != "found" || len(evidence.Facts) != 1 {
			t.Fatalf("matching credentials did not authorize the frozen read: %+v", evidence)
		}
	})
}

func TestProtocolBoundaryRevalidatesDirectoryIdentityAndAuthenticationFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*protocolBoundaryBackend)
		status int
		code   int
	}{
		{"disabled session user", func(b *protocolBoundaryBackend) { b.user.Enabled = false }, 401, codeUnauthenticated},
		{"missing session identity", func(b *protocolBoundaryBackend) { b.user.ID = "" }, 401, codeUnauthenticated},
		{"unknown directory identity", func(b *protocolBoundaryBackend) { b.user.ID = "private-unknown-user" }, 401, codeUnauthenticated},
		{"tenant mismatch", func(b *protocolBoundaryBackend) { b.user.TenantID = "private-other-tenant" }, 401, codeUnauthenticated},
		{"organization mismatch", func(b *protocolBoundaryBackend) { b.user.OrgID = "private-other-org" }, 401, codeUnauthenticated},
		{"authentication unavailable", func(b *protocolBoundaryBackend) { b.userErr = errors.New("private-auth-storage") }, 503, codeInternalError},
		{"authentication public unauthorized", func(b *protocolBoundaryBackend) { b.userErr = fault.Unauthenticated("private-session-reason") }, 401, codeUnauthenticated},
		{"authentication public forbidden", func(b *protocolBoundaryBackend) { b.userErr = fault.Forbidden("Access is unavailable.") }, 403, codeInternalError},
		{"invalid public status", func(b *protocolBoundaryBackend) {
			b.userErr = fault.New(700, "invalid_status", "Authentication failed.")
		}, 500, codeInternalError},
		{"audience unavailable", func(b *protocolBoundaryBackend) { b.audienceErr = errors.New("private-audience-storage") }, 503, codeInternalError},
		{"audience public unauthorized", func(b *protocolBoundaryBackend) { b.audienceErr = fault.Unauthenticated("private-audience-reason") }, 401, codeUnauthenticated},
		{"audience public forbidden", func(b *protocolBoundaryBackend) { b.audienceErr = fault.Forbidden("Audience access is unavailable.") }, 403, codeInternalError},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &protocolBoundaryBackend{gatewayFakeStore: testBackend(t)}
			test.mutate(backend)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/call", nil))
			protocolFailure(t, response, test.status, test.code, "")
			if backend.workspaceCalls != 0 || backend.reserveCalls != 0 {
				t.Fatal("an invalid session identity reached frozen evidence")
			}
		})
	}
	for _, backend := range []Backend{nil, (*gatewayFakeStore)(nil)} {
		t.Run(fmt.Sprintf("unavailable backend %T", backend), func(t *testing.T) {
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/list", nil))
			protocolFailure(t, response, 503, codeInternalError, "")
		})
	}
}

func TestProtocolBoundaryRejectsUnsafeOriginsAndAmbiguousPreflights(t *testing.T) {
	for _, test := range []struct {
		name           string
		origin         []string
		allowedOrigins []string
	}{
		{"opaque origin", []string{"null"}, []string{"https://portal.example"}},
		{"multiple origins", []string{"https://portal.example", "https://portal.example"}, []string{"https://portal.example"}},
		{"credentials in origin", []string{"https://user@portal.example"}, []string{"https://portal.example"}},
		{"origin path", []string{"https://portal.example/"}, []string{"https://portal.example"}},
		{"origin query", []string{"https://portal.example?scope=all"}, []string{"https://portal.example"}},
		{"origin fragment", []string{"https://portal.example#fragment"}, []string{"https://portal.example"}},
		{"origin whitespace", []string{" https://portal.example"}, []string{"https://portal.example"}},
		{"invalid configured origin", []string{"https://portal.example"}, []string{"https://portal.example", "https://portal.example/"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			request := protocolRequest(t, "tools/list", nil)
			request.Header["Origin"] = test.origin
			response := httptest.NewRecorder()
			NewHandler(backend, Options{AllowedOrigins: test.allowedOrigins}).ServeHTTP(response, request)
			protocolFailure(t, response, 403, codeInvalidRequest, "")
			if response.Header().Get("Access-Control-Allow-Origin") != "" || backend.userCalls != 0 {
				t.Fatal("unsafe origin was reflected or authenticated")
			}
		})
	}
	for _, test := range []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"missing origin", func(r *http.Request) { r.Header.Del("Origin") }},
		{"missing requested method", func(r *http.Request) { r.Header.Del("Access-Control-Request-Method") }},
		{"non POST request", func(r *http.Request) { r.Header.Set("Access-Control-Request-Method", "DELETE") }},
		{"multiple header lists", func(r *http.Request) { r.Header.Add("Access-Control-Request-Headers", "authorization") }},
		{"empty header element", func(r *http.Request) { r.Header.Set("Access-Control-Request-Headers", "accept,,content-type") }},
		{"duplicate header name", func(r *http.Request) { r.Header.Set("Access-Control-Request-Headers", "Accept, aCcEpT") }},
		{"non ASCII header name", func(r *http.Request) { r.Header.Set("Access-Control-Request-Headers", "证据") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			request := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
			request.Header.Set("Origin", "https://PORTAL.example")
			request.Header.Set("Access-Control-Request-Method", "POST")
			request.Header.Set("Access-Control-Request-Headers", "accept, authorization")
			test.mutate(request)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{AllowedOrigins: []string{"https://portal.example"}}).ServeHTTP(response, request)
			protocolFailure(t, response, 400, codeInvalidParams, "")
			if backend.userCalls != 0 || response.Header().Get("Access-Control-Allow-Methods") != "" {
				t.Fatal("invalid preflight advertised permission or reached authentication")
			}
		})
	}
	t.Run("canonical origin and all permitted headers", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
		request.Header.Set("Origin", "https://PORTAL.example")
		request.Header.Set("Access-Control-Request-Method", "POST")
		request.Header.Set("Access-Control-Request-Headers", "accept, authorization, content-type, mcp-method, mcp-name, mcp-protocol-version")
		response := httptest.NewRecorder()
		NewHandler(testBackend(t), Options{AllowedOrigins: []string{"https://portal.example"}}).ServeHTTP(response, request)
		if response.Code != 204 || response.Body.Len() != 0 || response.Header().Get("Access-Control-Allow-Origin") != "https://portal.example" ||
			response.Header().Get("Access-Control-Allow-Methods") != "POST" || response.Header().Get("Access-Control-Allow-Credentials") != "true" {
			t.Fatalf("valid preflight response status=%d", response.Code)
		}
		vary := strings.Join(response.Header().Values("Vary"), ",")
		for _, field := range []string{"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"} {
			if !strings.Contains(vary, field) {
				t.Fatalf("preflight caching does not vary on %s", field)
			}
		}
	})
}

func TestProtocolBoundaryRejectsUnsupportedParametersAndArgumentTypes(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		mutate func(map[string]any)
	}{
		{"discovery parameter", "server/discover", func(p map[string]any) { p["cursor"] = "" }},
		{"catalog unsupported parameter", "tools/list", func(p map[string]any) { p["tenant_id"] = "private-tenant" }},
		{"null cursor", "tools/list", func(p map[string]any) { p["cursor"] = nil }},
		{"numeric cursor", "tools/list", func(p map[string]any) { p["cursor"] = 1 }},
		{"nonempty cursor", "tools/list", func(p map[string]any) { p["cursor"] = "next-page" }},
		{"call unsupported parameter", "tools/call", func(p map[string]any) { p["force"] = true }},
		{"missing tool name", "tools/call", func(p map[string]any) { delete(p, "name") }},
		{"null tool name", "tools/call", func(p map[string]any) { p["name"] = nil }},
		{"missing arguments", "tools/call", func(p map[string]any) { delete(p, "arguments") }},
		{"null arguments", "tools/call", func(p map[string]any) { p["arguments"] = nil }},
		{"array arguments", "tools/call", func(p map[string]any) { p["arguments"] = []any{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := protocolRequest(t, test.method, func(b map[string]any) { test.mutate(b["params"].(map[string]any)) })
			backend := testBackend(t)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, request)
			protocolFailure(t, response, 400, codeInvalidParams, "")
			if backend.workspaceCalls != 0 || backend.reserveCalls != 0 {
				t.Fatal("unsupported parameters reached frozen evidence")
			}
		})
	}
	for _, field := range []string{"case_id", "run_id", "attempt_id", "capability_id", "deadline"} {
		for _, value := range []struct {
			name  string
			value any
		}{
			{"missing", nil}, {"null", nil}, {"number", 123}, {"array", []any{}}, {"empty", ""},
		} {
			t.Run(field+" "+value.name, func(t *testing.T) {
				request := protocolRequest(t, "tools/call", func(b map[string]any) {
					args := b["params"].(map[string]any)["arguments"].(map[string]any)
					if value.name == "missing" {
						delete(args, field)
					} else {
						args[field] = value.value
					}
				})
				backend := testBackend(t)
				response := httptest.NewRecorder()
				NewHandler(backend, Options{}).ServeHTTP(response, request)
				protocolFailure(t, response, 400, codeInvalidParams, "")
				if backend.workspaceCalls != 0 || backend.reserveCalls != 0 {
					t.Fatal("invalid selection type reached frozen evidence")
				}
			})
		}
	}
	for _, test := range []struct{ field, value string }{
		{"case_id", " case-1"}, {"run_id", "run/1"}, {"attempt_id", "尝试一"}, {"case_id", strings.Repeat("x", 129)},
		{"deadline", strings.Repeat("x", 65)}, {"deadline", "tomorrow"},
		{"deadline", time.Now().Add(2 * time.Minute).Format(time.RFC3339Nano)},
	} {
		t.Run("bounded "+test.field+" "+test.value, func(t *testing.T) {
			request := protocolRequest(t, "tools/call", func(b map[string]any) {
				b["params"].(map[string]any)["arguments"].(map[string]any)[test.field] = test.value
			})
			response := httptest.NewRecorder()
			backend := testBackend(t)
			NewHandler(backend, Options{}).ServeHTTP(response, request)
			protocolFailure(t, response, 400, codeInvalidParams, "")
			if backend.reserveCalls != 0 {
				t.Fatal("unbounded identity or deadline consumed a tool reservation")
			}
		})
	}
}

func TestProtocolBoundaryReportsUnknownMethods(t *testing.T) {
	t.Run("unknown method", func(t *testing.T) {
		backend := testBackend(t)
		response := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "resources/list", nil))
		protocolFailure(t, response, 404, codeMethodNotFound, "")
		if backend.workspaceCalls != 0 || backend.reserveCalls != 0 {
			t.Fatal("unknown RPC method executed a tool")
		}
	})
}

func TestProtocolBoundaryRejectsWrongFrozenAttemptBindingsBeforeReservation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*store.Workspace)
		status int
	}{
		{"foreign case", func(w *store.Workspace) { w.Case.ID = "other-case" }, 404},
		{"foreign tenant", func(w *store.Workspace) { w.Case.TenantID = "private-other-tenant" }, 404},
		{"foreign run", func(w *store.Workspace) { w.Snapshot.RunID = "other-run" }, 404},
		{"missing execution", func(w *store.Workspace) { w.Execution = nil }, 404},
		{"missing attempt", func(w *store.Workspace) { w.Execution.Attempt = nil }, 404},
		{"foreign attempt", func(w *store.Workspace) { w.Execution.Attempt.ID = "other-attempt" }, 404},
		{"attempt belongs to another run", func(w *store.Workspace) { w.Execution.Attempt.RunID = "other-run" }, 404},
		{"unfrozen history", func(w *store.Workspace) { w.Execution.Attempt.HistoryKind = "legacy" }, 404},
		{"missing input hash", func(w *store.Workspace) { w.Execution.Attempt.InputHash = nil }, 404},
		{"empty input hash", func(w *store.Workspace) { empty := ""; w.Execution.Attempt.InputHash = &empty }, 404},
		{"wrong business line", func(w *store.Workspace) { w.Case.BusinessLine = "SMB" }, 422},
		{"wrong scenario", func(w *store.Workspace) { w.Case.Scenario = "cloud_services" }, 422},
		{"wrong contract type", func(w *store.Workspace) { w.Case.ContractType = "purchase" }, 422},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			test.mutate(&backend.workspace)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/call", nil))
			code, publicCode := codeInternalError, ""
			if test.status == 422 {
				code, publicCode = codeInvalidParams, "unsupported_case_scope"
			}
			protocolFailure(t, response, test.status, code, publicCode)
			if backend.reserveCalls != 0 || backend.finishCalls != 0 {
				t.Fatal("mismatched frozen attempt consumed a tool budget")
			}
		})
	}
}

func TestProtocolBoundaryRejectsSourceAndParsedArtifactIdentityMismatch(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*gatewayFakeStore)
	}{
		{"empty pinned parse id", func(b *gatewayFakeStore) { b.workspace.Execution.Attempt.ParsedDocumentIDs = []string{""} }},
		{"duplicate pinned parse", func(b *gatewayFakeStore) {
			b.workspace.Execution.Attempt.ParsedDocumentIDs = []string{"parsed-1", "parsed-1"}
		}},
		{"missing pinned parse", func(b *gatewayFakeStore) { b.workspace.Execution.Attempt.ParsedDocumentIDs = []string{"missing-parse"} }},
		{"duplicate selected document", func(b *gatewayFakeStore) {
			b.workspace.Documents = append(b.workspace.Documents, b.workspace.Documents[0])
		}},
		{"missing parsed artifact", func(b *gatewayFakeStore) { b.workspace.Documents[0].ParsedDocument = nil }},
		{"source case mismatch", func(b *gatewayFakeStore) { b.workspace.Documents[0].Document.CaseID = "other-case" }},
		{"source tenant mismatch", func(b *gatewayFakeStore) { b.workspace.Documents[0].Document.TenantID = "private-other-tenant" }},
		{"source digest missing", func(b *gatewayFakeStore) { b.workspace.Documents[0].Document.Digest = "" }},
		{"parsed source id mismatch", func(b *gatewayFakeStore) { b.workspace.Documents[0].ParsedDocument.DocumentID = "other-document" }},
		{"original digest mismatch", func(b *gatewayFakeStore) { b.workspace.Documents[0].ParsedDocument.OriginalDigest = "changed-original" }},
		{"artifact digest missing", func(b *gatewayFakeStore) { b.workspace.Documents[0].ParsedDocument.ArtifactDigest = "" }},
		{"parser id missing", func(b *gatewayFakeStore) { b.workspace.Documents[0].ParsedDocument.ParserID = "" }},
		{"parser release missing", func(b *gatewayFakeStore) { b.workspace.Documents[0].ParsedDocument.ParserVersion = "" }},
		{"parser configuration missing", func(b *gatewayFakeStore) { b.workspace.Documents[0].ParsedDocument.ConfigHash = "" }},
		{"normalization release missing", func(b *gatewayFakeStore) { b.workspace.Documents[0].ParsedDocument.NormalizationVersion = "" }},
		{"normalized digest missing", func(b *gatewayFakeStore) { b.workspace.Documents[0].ParsedDocument.NormalizedDigest = "" }},
		{"normalized digest mismatch", func(b *gatewayFakeStore) {
			b.workspace.Documents[0].ParsedDocument.NormalizedDigest = strings.Repeat("0", 64)
		}},
		{"workspace text drift", func(b *gatewayFakeStore) {
			b.workspace.Documents[0].Text = "电路 LINE-001：最低保障带宽 999 Mbit/s。"
		}},
		{"empty parsed text", func(b *gatewayFakeStore) { protocolSourceText(b, "") }},
		{"invalid UTF8 parsed text", func(b *gatewayFakeStore) { protocolSourceText(b, string([]byte{0xff})+b.workspace.Documents[0].Text) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			test.mutate(backend)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/call", nil))
			protocolFailure(t, response, 409, codeInternalError, "frozen_evidence_integrity")
			if backend.finishCalls != 1 || backend.finishStatus != "failed" || backend.finishOutputSize != 0 {
				t.Fatalf("integrity refusal did not settle without evidence bytes: status=%s bytes=%d", backend.finishStatus, backend.finishOutputSize)
			}
			if strings.Contains(response.Body.String(), "最低保障带宽") || strings.Contains(response.Body.String(), "original-digest") {
				t.Fatal("integrity failure disclosed untrusted evidence")
			}
		})
	}
}

func TestProtocolBoundaryKeepsUnicodeQuoteOffsetsAndCapabilitySemantics(t *testing.T) {
	backend := testBackend(t)
	protocolSourceText(backend, "序言🙂。 \n电路 LINE-001：最低保障带宽\n100 Mbit/s。\n附注。")
	response := httptest.NewRecorder()
	NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/call", nil))
	evidence := protocolEvidence(t, response)
	if evidence.Status != "found" || len(evidence.Facts) != 1 || len(evidence.Facts[0].Citations) != 1 {
		t.Fatalf("wrapped Unicode statement was not cited: %+v", evidence)
	}
	fact, cite := evidence.Facts[0], evidence.Facts[0].Citations[0]
	if fact.RuleID != "ENT-TECH-BW-001" || fact.ObjectRef != "LINE-001" || fact.Value != "100" || fact.Unit != "Mbit/s" ||
		cite.Start != 6 || cite.End != 36 || cite.Quote != "电路 LINE-001：最低保障带宽\n100 Mbit/s。" {
		t.Fatalf("Unicode citation lost exact codepoint positions or quote: %+v", fact)
	}
	if string([]rune(backend.workspace.Documents[0].Text)[cite.Start:cite.End]) != cite.Quote ||
		cite.SourceDigest != cite.NormalizedDigest || cite.SourceVersion != "parsed-1" || cite.ParserVersion != "1" {
		t.Fatal("citation is not anchored to the immutable normalized source")
	}
	for _, test := range []struct {
		capability, rule, value, unit string
	}{
		{"dedicated_line.monthly_availability", "ENT-TECH-SLA-001", "999/10", "percent"},
		{"payment.advance_ratio.ceiling", "ENT-COM-ADV-001", "1/5", "ratio"},
	} {
		t.Run(test.capability, func(t *testing.T) {
			backend := testBackend(t)
			protocolSourceText(backend, "电路 LINE-001：最低保障带宽 100 Mbit/s。电路 LINE-001：自然月可用率 99.9%，时区 Asia/Shanghai，公历 gregorian，故障口径 all_service_unavailability，无排除项。电路 LINE-001：预付款比例 20%，预付款以合同总额为基础。")
			request := protocolRequest(t, "tools/call", func(b map[string]any) {
				b["params"].(map[string]any)["arguments"].(map[string]any)["capability_id"] = test.capability
			})
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, request)
			evidence := protocolEvidence(t, response)
			if evidence.Status != "found" || evidence.CapabilityID != test.capability || len(evidence.Facts) != 1 || len(evidence.Facts[0].Citations) != 1 {
				t.Fatalf("capability selection returned unrelated facts: %+v", evidence)
			}
			fact := evidence.Facts[0]
			if fact.RuleID != test.rule || fact.Value != test.value || fact.Unit != test.unit {
				t.Fatalf("capability lost numeric value or units: %+v", fact)
			}
			if test.unit == "percent" && (fact.MeasurementWindow != "calendar_month" || fact.TimeZone != "Asia/Shanghai" || fact.Calendar != "gregorian" || fact.OutageDefinition != "all_service_unavailability") {
				t.Fatalf("monthly availability lost its measurement semantics: %+v", fact)
			}
			if test.unit == "ratio" && fact.Basis != "contract_total" {
				t.Fatalf("advance-payment evidence lost its denominator: %+v", fact)
			}
		})
	}
}

func TestProtocolBoundaryIgnoresUnselectedArtifactsAndUnsupportedStatements(t *testing.T) {
	t.Run("newer unselected parse", func(t *testing.T) {
		backend := testBackend(t)
		other := testBackend(t)
		protocolSourceText(other, "电路 LINE-001：最低保障带宽 999 Mbit/s。")
		other.workspace.Documents[0].ParsedDocument.ID = "parsed-newer"
		backend.workspace.Documents = append([]store.Interpreted{{}, other.workspace.Documents[0]}, backend.workspace.Documents...)
		response := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/call", nil))
		evidence := protocolEvidence(t, response)
		if len(evidence.Facts) != 1 || len(evidence.Facts[0].Citations) != 1 || evidence.Facts[0].Value != "100" || evidence.Facts[0].Citations[0].ParsedDocumentID != "parsed-1" {
			t.Fatalf("a later parse replaced the pinned evidence: %+v", evidence)
		}
	})
	for _, test := range []struct {
		name   string
		mutate func(*gatewayFakeStore)
	}{
		{"non draft role", func(b *gatewayFakeStore) { b.workspace.Documents[0].Document.Role = "procurement" }},
		{"document not ready", func(b *gatewayFakeStore) { b.workspace.Documents[0].Ready = false }},
		{"parse not ready", func(b *gatewayFakeStore) { b.workspace.Documents[0].ParsedDocument.Status = "failed" }},
		{"no supported statement", func(b *gatewayFakeStore) { protocolSourceText(b, "具体带宽另行协商。") }},
		{"negated wrapped statement", func(b *gatewayFakeStore) {
			protocolSourceText(b, "未承诺\n电路 LINE-001：最低保障带宽 100 Mbit/s。")
		}},
		{"negated CRLF statement", func(b *gatewayFakeStore) {
			protocolSourceText(b, "不保证\r\n电路 LINE-001：最低保障带宽 100 Mbit/s。")
		}},
		{"negation wrapped inside token", func(b *gatewayFakeStore) {
			protocolSourceText(b, "未承\n诺电路 LINE-001：最低保障带宽 100 Mbit/s。")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			test.mutate(backend)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/call", nil))
			evidence := protocolEvidence(t, response)
			if evidence.Status != "no_supported_statement" || len(evidence.Facts) != 0 {
				t.Fatalf("unsupported or negated source became an affirmative fact: %+v", evidence)
			}
			if backend.finishStatus != "completed" || backend.finishOutputSize <= 0 {
				t.Fatal("an honest empty evidence result was not charged as a completed response")
			}
		})
	}
}

func TestProtocolBoundaryRejectsEvidenceProcessingAndResponseLimits(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*gatewayFakeStore)
		status int
		code   string
	}{
		{"too many pinned documents", func(b *gatewayFakeStore) {
			b.workspace.Execution.Attempt.ParsedDocumentIDs = make([]string, 129)
			for i := range b.workspace.Execution.Attempt.ParsedDocumentIDs {
				b.workspace.Execution.Attempt.ParsedDocumentIDs[i] = fmt.Sprintf("parsed-%d", i)
			}
		}, 413, "evidence_too_large"},
		{"source byte budget", func(b *gatewayFakeStore) { protocolSourceText(b, strings.Repeat("说明。", (2<<20)/9+1)) }, 413, "evidence_too_large"},
		{"fact budget", func(b *gatewayFakeStore) {
			protocolSourceText(b, strings.Repeat("电路 LINE-001：最低保障带宽 100 Mbit/s。", 33))
		}, 413, "evidence_too_large"},
		{"quote codepoint budget", func(b *gatewayFakeStore) {
			protocolSourceText(b, strings.Repeat("说明", 2050)+"电路 LINE-001：最低保障带宽 100 Mbit/s。")
		}, 409, "frozen_evidence_integrity"},
		{"serialized output byte budget", func(b *gatewayFakeStore) {
			protocolSourceText(b, strings.Repeat(strings.Repeat("说明", 600)+"电路 LINE-001：最低保障带宽 100 Mbit/s。", 24))
		}, 413, "evidence_too_large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			test.mutate(backend)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/call", nil))
			protocolFailure(t, response, test.status, codeInternalError, test.code)
			if response.Body.Len() > 1024 || backend.finishStatus != "failed" || backend.finishOutputSize != 0 || backend.finishCalls != 1 {
				t.Fatalf("oversized evidence was disclosed or charged as output: bytes=%d status=%s output=%d", response.Body.Len(), backend.finishStatus, backend.finishOutputSize)
			}
		})
	}
	t.Run("maximum fact count remains usable", func(t *testing.T) {
		backend := testBackend(t)
		protocolSourceText(backend, strings.Repeat("电路 LINE-001：最低保障带宽 100 Mbit/s。", 32))
		response := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/call", nil))
		evidence := protocolEvidence(t, response)
		if evidence.Status != "found" || len(evidence.Facts) != 32 {
			t.Fatalf("in-budget evidence was lost: facts=%d status=%s", len(evidence.Facts), evidence.Status)
		}
		for index, fact := range evidence.Facts {
			if len(fact.Citations) != 1 || fact.Citations[0].Start != 30*index || fact.Citations[0].End != 30*(index+1) {
				t.Fatalf("fact %d lost deterministic source order or codepoint range: %+v", index, fact)
			}
		}
	})
}

func TestProtocolBoundaryWithholdsEvidenceWhenFrozenIdentityChangesDuringRead(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*store.Workspace)
		status int
		code   string
	}{
		{"input hash changed", func(w *store.Workspace) { changed := "changed-input"; w.Execution.Attempt.InputHash = &changed }, 409, "frozen_attempt_changed"},
		{"source digest changed", func(w *store.Workspace) { w.Documents[0].Document.Digest = "changed-original" }, 409, "frozen_attempt_changed"},
		{"original parsed digest changed", func(w *store.Workspace) { w.Documents[0].ParsedDocument.OriginalDigest = "changed-original" }, 409, "frozen_attempt_changed"},
		{"parser compiler release drift", func(w *store.Workspace) { w.Documents[0].ParsedDocument.ParserVersion = "2" }, 409, "frozen_attempt_changed"},
		{"parser configuration drift", func(w *store.Workspace) { w.Documents[0].ParsedDocument.ConfigHash = "changed-config" }, 409, "frozen_attempt_changed"},
		{"normalization release drift", func(w *store.Workspace) { w.Documents[0].ParsedDocument.NormalizationVersion = "norm-v2" }, 409, "frozen_attempt_changed"},
		{"normalized source digest drift", func(w *store.Workspace) { w.Documents[0].ParsedDocument.NormalizedDigest = "changed-normalized" }, 409, "frozen_attempt_changed"},
		{"parsed artifact digest drift", func(w *store.Workspace) { w.Documents[0].ParsedDocument.ArtifactDigest = "changed-artifact" }, 409, "frozen_attempt_changed"},
		{"tenant authorization changed", func(w *store.Workspace) { w.Case.TenantID = "private-other-tenant" }, 404, ""},
		{"attempt binding changed", func(w *store.Workspace) { w.Execution.Attempt.ID = "other-attempt" }, 404, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &protocolBoundaryBackend{gatewayFakeStore: testBackend(t)}
			changed := testBackend(t).workspace
			test.mutate(&changed)
			backend.workspaceRead = func(call int) (store.Workspace, error) {
				if call == 1 {
					return backend.workspace, nil
				}
				return changed, nil
			}
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/call", nil))
			protocolFailure(t, response, test.status, codeInternalError, test.code)
			if backend.workspaceCalls != 2 || backend.finishCalls != 1 || backend.finishStatus != "failed" || backend.finishOutputSize != 0 {
				t.Fatal("evidence changed during the read was delivered or charged as completed")
			}
		})
	}
}

func TestProtocolBoundaryWithholdsEvidenceOnAccountingFailure(t *testing.T) {
	empty := ""
	for _, test := range []struct {
		name       string
		configure  func(*protocolBoundaryBackend)
		status     int
		publicCode string
		finish     int
	}{
		{"quota denied before reservation", func(b *protocolBoundaryBackend) {
			b.reserveID, b.reserveErr = &empty, fault.New(429, "tool_quota_exceeded", "Tool budget is exhausted.")
		}, 429, "tool_quota_exceeded", 0},
		{"reservation failed after allocation", func(b *protocolBoundaryBackend) { b.reserveErr = errors.New("private-reserve-storage") }, 500, "", 1},
		{"empty reservation identity", func(b *protocolBoundaryBackend) { b.reserveID = &empty }, 500, "", 0},
		{"failed allocation cannot settle", func(b *protocolBoundaryBackend) {
			b.reserveErr = errors.New("private-reserve-storage")
			b.finishErr = errors.New("private-finish-storage")
		}, 503, "", 1},
		{"completed evidence cannot settle", func(b *protocolBoundaryBackend) { b.finishErr = errors.New("private-finish-storage") }, 503, "", 1},
		{"integrity refusal cannot settle", func(b *protocolBoundaryBackend) {
			b.workspace.Documents[0].ParsedDocument.OriginalDigest = "changed-original"
			b.finishErr = errors.New("private-finish-storage")
		}, 503, "", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &protocolBoundaryBackend{gatewayFakeStore: testBackend(t)}
			test.configure(backend)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/call", nil))
			protocolFailure(t, response, test.status, codeInternalError, test.publicCode)
			if backend.finishCalls != test.finish || strings.Contains(response.Body.String(), "最低保障带宽") {
				t.Fatal("accounting failure returned evidence or did not follow the reservation lifecycle")
			}
		})
	}
}

func TestProtocolBoundaryHonorsExpiredContextsBeforeAndAfterReservation(t *testing.T) {
	t.Run("expired HTTP context", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		backend := testBackend(t)
		response := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(response, protocolRequest(t, "tools/call", nil).WithContext(ctx))
		protocolFailure(t, response, 504, codeInternalError, "")
		if backend.userCalls != 0 || backend.reserveCalls != 0 {
			t.Fatal("expired HTTP request performed authentication or consumed budget")
		}
	})
	t.Run("tool deadline during reservation", func(t *testing.T) {
		backend := &protocolBoundaryBackend{gatewayFakeStore: testBackend(t)}
		backend.onReserve = func(ctx context.Context) { <-ctx.Done() }
		request := protocolRequest(t, "tools/call", func(b map[string]any) {
			b["params"].(map[string]any)["arguments"].(map[string]any)["deadline"] = time.Now().Add(time.Second).Format(time.RFC3339Nano)
		})
		response := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(response, request)
		protocolFailure(t, response, 504, codeInternalError, "")
		if backend.finishCalls != 1 || backend.finishStatus != "failed" || backend.finishOutputSize != 0 {
			t.Fatal("deadline expiry after admission did not terminally settle without output")
		}
	})
}

func protocolWorkerRequest(t *testing.T, backend *gatewayFakeStore, mutate func(map[string]any)) *http.Request {
	t.Helper()
	claim := map[string]any{
		"tenant_id": backend.user.TenantID, "org_id": backend.user.OrgID,
		"job_id": "job-1", "worker_id": "worker-1", "generation": 1, "attempt_id": "attempt-1",
	}
	if mutate != nil {
		mutate(claim)
	}
	encoded, err := json.Marshal(claim)
	if err != nil {
		t.Fatal(err)
	}
	request := protocolRequest(t, "tools/call", nil)
	request.Header.Set("X-Contract-Job-Context", string(encoded))
	request.Header.Set("X-Contract-Service-Key", toolGatewayTestCredential)
	return request
}

func TestProtocolBoundaryRejectsInvalidWorkerHeadersBeforeReadingEvidence(t *testing.T) {
	for _, test := range []struct {
		name    string
		mutate  func(*http.Request)
		options Options
	}{
		{"duplicate claim headers", func(r *http.Request) { r.Header.Add("X-Contract-Job-Context", "{}") }, Options{WorkerCredential: toolGatewayTestCredential}},
		{"empty claim header", func(r *http.Request) { r.Header.Set("X-Contract-Job-Context", "") }, Options{WorkerCredential: toolGatewayTestCredential}},
		{"claim cannot list tools", func(r *http.Request) {
			other := protocolRequest(t, "tools/list", nil)
			r.Body = other.Body
			r.Header.Set("Mcp-Method", "tools/list")
			r.Header.Del("Mcp-Name")
		}, Options{WorkerCredential: toolGatewayTestCredential}},
		{"missing service credential", func(r *http.Request) { r.Header.Del("X-Contract-Service-Key") }, Options{WorkerCredential: toolGatewayTestCredential}},
		{"duplicate service credential", func(r *http.Request) { r.Header.Add("X-Contract-Service-Key", toolGatewayTestCredential) }, Options{WorkerCredential: toolGatewayTestCredential}},
		{"incorrect service credential", func(r *http.Request) { r.Header.Set("X-Contract-Service-Key", "private-wrong-service-key") }, Options{WorkerCredential: toolGatewayTestCredential}},
		{"unconfigured worker support", func(r *http.Request) {}, Options{}},
		{"short configured service credential", func(r *http.Request) { r.Header.Set("X-Contract-Service-Key", "short") }, Options{WorkerCredential: "short"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &protocolBoundaryResolver{gatewayFakeStore: testBackend(t)}
			request := protocolWorkerRequest(t, backend.gatewayFakeStore, nil)
			test.mutate(request)
			response := httptest.NewRecorder()
			NewHandler(backend, test.options).ServeHTTP(response, request)
			protocolFailure(t, response, 401, codeUnauthenticated, "unauthenticated")
			if backend.userCalls != 0 || backend.resolveCalls != 0 || backend.workspaceCalls != 0 {
				t.Fatal("an invalid worker header fell back to a browser session or reached evidence")
			}
			if strings.Contains(response.Body.String(), toolGatewayTestCredential) {
				t.Fatal("worker refusal exposed its service credential")
			}
		})
	}
}

func TestProtocolBoundaryRejectsStrictWorkerClaimTypesAndScopeMismatch(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown claim field", func(c map[string]any) { c["role"] = "admin" }},
		{"missing tenant", func(c map[string]any) { delete(c, "tenant_id") }},
		{"blank organization", func(c map[string]any) { c["org_id"] = " " }},
		{"missing job", func(c map[string]any) { delete(c, "job_id") }},
		{"blank worker", func(c map[string]any) { c["worker_id"] = "\t" }},
		{"null generation", func(c map[string]any) { c["generation"] = nil }},
		{"negative generation", func(c map[string]any) { c["generation"] = -1 }},
		{"string generation", func(c map[string]any) { c["generation"] = "1" }},
		{"missing attempt", func(c map[string]any) { delete(c, "attempt_id") }},
		{"claim attempt mismatch", func(c map[string]any) { c["attempt_id"] = "another-attempt" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &protocolBoundaryResolver{gatewayFakeStore: testBackend(t)}
			request := protocolWorkerRequest(t, backend.gatewayFakeStore, test.mutate)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{WorkerCredential: toolGatewayTestCredential}).ServeHTTP(response, request)
			protocolFailure(t, response, 400, codeInvalidParams, "")
			if backend.resolveCalls != 0 || backend.reserveCalls != 0 {
				t.Fatal("invalid worker claim was treated as database authority")
			}
		})
	}
	for _, raw := range []string{"{} {}", "null", strings.Repeat(" ", (4<<10)+1)} {
		t.Run(fmt.Sprintf("invalid raw claim length %d", len(raw)), func(t *testing.T) {
			backend := &protocolBoundaryResolver{gatewayFakeStore: testBackend(t)}
			request := protocolWorkerRequest(t, backend.gatewayFakeStore, nil)
			request.Header.Set("X-Contract-Job-Context", raw)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{WorkerCredential: toolGatewayTestCredential}).ServeHTTP(response, request)
			protocolFailure(t, response, 400, codeInvalidParams, "")
			if backend.resolveCalls != 0 {
				t.Fatal("malformed or oversized claim reached authority resolution")
			}
		})
	}
	t.Run("backend without resolver", func(t *testing.T) {
		backend := testBackend(t)
		request := protocolWorkerRequest(t, backend, nil)
		response := httptest.NewRecorder()
		NewHandler(backend, Options{WorkerCredential: toolGatewayTestCredential}).ServeHTTP(response, request)
		protocolFailure(t, response, 501, codeMethodNotFound, "")
		if backend.workspaceCalls != 0 || backend.userCalls != 0 {
			t.Fatal("unsupported job-scoped backend fell back to session authority")
		}
	})
}

func TestProtocolBoundaryRechecksWorkerAuthorityBeforeDeliveringEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		failAt int
		err    error
		status int
		code   int
	}{
		{"claim already stale", 1, store.ErrToolScopeUnavailable, 409, codeInvalidRequest},
		{"claim revoked after read", 2, store.ErrToolScopeUnavailable, 409, codeInvalidRequest},
		{"authority storage fails after read", 2, errors.New("private-worker-store-failure"), 500, codeInternalError},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &protocolBoundaryResolver{gatewayFakeStore: testBackend(t)}
			backend.resolve = func(call int, in store.ToolInvocationContext) (store.ToolAuthority, error) {
				if in.TenantID != backend.user.TenantID || in.OrgID != backend.user.OrgID || in.JobID != "job-1" ||
					in.WorkerID != "worker-1" || in.Generation != 1 || in.CaseID != "case-1" || in.RunID != "run-1" || in.AttemptID != "attempt-1" {
					t.Error("worker authority resolution did not bind all claim and request identities")
				}
				if call == test.failAt {
					return store.ToolAuthority{}, test.err
				}
				return store.ToolAuthority{User: backend.user}, nil
			}
			request := protocolWorkerRequest(t, backend.gatewayFakeStore, nil)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{WorkerCredential: toolGatewayTestCredential}).ServeHTTP(response, request)
			protocolFailure(t, response, test.status, test.code, "")
			if backend.resolveCalls != test.failAt || backend.userCalls != 0 || strings.Contains(response.Body.String(), "最低保障带宽") || strings.Contains(response.Body.String(), "job-1") {
				t.Fatal("worker authority was not rechecked or the refusal disclosed evidence or job identity")
			}
			if test.failAt == 1 && backend.reserveCalls != 0 {
				t.Fatal("an already stale claim consumed tool budget")
			}
		})
	}
}

func TestProtocolBoundaryReplayedHTTPRequestsConsumeSharedBudgetAndHonorSessionRevocation(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")) == "" {
		t.Skip("TEST_DATABASE_URL is required for isolated PostgreSQL replay and budget checks")
	}
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatalf("open isolated protocol boundary schema: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close isolated protocol boundary schema: %v", err)
		}
	})
	user, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo admin is unavailable")
	}
	st.UseModels(gatewayConsumerModels{})
	seedGatewayGovernance(t, st, user)
	fixture := createGatewayClaimFixture(t, st, user, "protocol-budget-worker")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := st.RunClaimedJob(ctx, fixture.claim); err != nil {
		t.Fatalf("freeze review attempt for HTTP replay: %v", err)
	}
	token, err := st.EstablishSession(user, "client")
	if err != nil {
		t.Fatalf("establish client session: %v", err)
	}
	requestForAttempt := func() *http.Request {
		request := protocolRequest(t, "tools/call", func(b map[string]any) {
			args := b["params"].(map[string]any)["arguments"].(map[string]any)
			args["case_id"], args["run_id"], args["attempt_id"] = fixture.caseID, fixture.acceptance.RunID, fixture.acceptance.AttemptID
		})
		request.Header.Del("Cookie")
		request.AddCookie(&http.Cookie{Name: "session", Value: token})
		return request
	}
	replayed := requestForAttempt()
	replayBody, err := io.ReadAll(replayed.Body)
	if err != nil {
		t.Fatal(err)
	}
	handlers := []http.Handler{NewHandler(st, Options{}), NewHandler(st, Options{})}
	var firstResponse string
	for index := 0; index <= 32; index++ {
		request := requestForAttempt()
		if index < 2 {
			request.Body = io.NopCloser(strings.NewReader(string(replayBody)))
		}
		response := httptest.NewRecorder()
		handlers[index%len(handlers)].ServeHTTP(response, request)
		if index == 32 {
			protocolFailure(t, response, 429, codeInternalError, "tool_quota_exceeded")
			if response.Body.Len() > 1024 {
				t.Fatal("budget refusal exposed an unbounded response")
			}
			continue
		}
		evidence := protocolEvidence(t, response)
		if evidence.CaseID != fixture.caseID || evidence.RunID != fixture.acceptance.RunID || evidence.AttemptID != fixture.acceptance.AttemptID ||
			evidence.Status != "found" || len(evidence.Facts) != 1 || evidence.Facts[0].Value != "100" {
			t.Fatalf("replayed request escaped its frozen attempt: %+v", evidence)
		}
		if index == 0 {
			firstResponse = response.Body.String()
		} else if index == 1 && response.Body.String() != firstResponse {
			t.Fatal("identical replay through a second handler changed the frozen evidence")
		}
	}
	if err := st.Logout(token); err != nil {
		t.Fatalf("revoke client session: %v", err)
	}
	request := protocolRequest(t, "server/discover", nil)
	request.Header.Del("Cookie")
	request.AddCookie(&http.Cookie{Name: "session", Value: token})
	response := httptest.NewRecorder()
	handlers[0].ServeHTTP(response, request)
	protocolFailure(t, response, 401, codeUnauthenticated, "unauthenticated")
	if strings.Contains(response.Body.String(), token) {
		t.Fatal("session revocation response exposed the actual session token")
	}
}

// A single in-flight slot makes a leaked reservation visible to the next HTTP
// caller. Finishing a failed read releases that slot.
type protocolRecheckBudgetBackend struct {
	*gatewayFakeStore
	pending    bool
	recheckErr error
}

func (b *protocolRecheckBudgetBackend) WorkspaceAttempt(user accounts.User, caseID, runID, attemptID string) (store.Workspace, error) {
	workspace, err := b.gatewayFakeStore.WorkspaceAttempt(user, caseID, runID, attemptID)
	if b.workspaceCalls == 2 && b.recheckErr != nil {
		return store.Workspace{}, b.recheckErr
	}
	return workspace, err
}

func (b *protocolRecheckBudgetBackend) ReserveToolCall(ctx context.Context, user accounts.User, in store.ToolCallRequest) (string, error) {
	if b.pending {
		return "", fault.New(429, "tool_quota_exceeded", "An unfinished tool call occupies the remaining budget.")
	}
	b.pending = true
	return b.gatewayFakeStore.ReserveToolCall(ctx, user, in)
}

func (b *protocolRecheckBudgetBackend) FinishToolCall(ctx context.Context, user accounts.User, callID, status string, outputBytes int64) error {
	b.pending = false
	return b.gatewayFakeStore.FinishToolCall(ctx, user, callID, status, outputBytes)
}

func TestProtocolBoundaryRecheckFailureSettlesReservationBeforeNextAuthorizedRead(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		status     int
		publicCode string
	}{
		{"authorization revoked", fault.Forbidden("Frozen attempt access was revoked."), 403, "forbidden"},
		{"storage unavailable", errors.New("private-recheck-storage-failure"), 500, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &protocolRecheckBudgetBackend{gatewayFakeStore: testBackend(t), recheckErr: test.err}
			handler := NewHandler(backend, Options{})
			refused := httptest.NewRecorder()
			handler.ServeHTTP(refused, protocolRequest(t, "tools/call", nil))
			protocolFailure(t, refused, test.status, codeInternalError, test.publicCode)
			if strings.Contains(refused.Body.String(), "最低保障带宽") {
				t.Fatal("failed authorization recheck disclosed the built evidence")
			}
			backend.recheckErr = nil
			retried := httptest.NewRecorder()
			handler.ServeHTTP(retried, protocolRequest(t, "tools/call", nil))
			evidence := protocolEvidence(t, retried)
			if evidence.Status != "found" || len(evidence.Facts) != 1 || evidence.Facts[0].Value != "100" {
				t.Fatal("a failed recheck left a reservation that blocked the next authorized read")
			}
			if backend.finishCalls != 2 || backend.finishStatus != "completed" {
				t.Fatal("the rejected and successful reads did not each reach a terminal accounting state")
			}
		})
	}
}
