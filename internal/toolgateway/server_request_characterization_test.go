package toolgateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
)

func TestRequestParsingKeepsDuplicateJSONFailureBeforeEnvelopeChecks(t *testing.T) {
	backend := testBackend(t)
	request := protocolRequest(t, "tools/list", nil)
	request.Body = io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	response := httptest.NewRecorder()
	NewHandler(backend, Options{}).ServeHTTP(response, request)
	assertToolGatewayFailure(t, response, http.StatusBadRequest, codeParseError, "Request must contain exactly one JSON object.")
	if backend.userCalls != 0 {
		t.Fatal("duplicate-key JSON reached authentication")
	}
}

func TestRequestMetadataFailurePrecedesMirroredProtocolHeaders(t *testing.T) {
	backend := testBackend(t)
	request := protocolRequest(t, "tools/list", nil)
	var body map[string]json.RawMessage
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(body["params"], &params); err != nil {
		t.Fatal(err)
	}
	delete(params, "_meta")
	body["params"], _ = json.Marshal(params)
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request.Body = io.NopCloser(strings.NewReader(string(encoded)))
	request.Header.Set("MCP-Protocol-Version", "unsupported-version")
	request.Header.Set("Mcp-Method", "tools/call")

	response := httptest.NewRecorder()
	NewHandler(backend, Options{}).ServeHTTP(response, request)
	assertToolGatewayFailure(t, response, http.StatusBadRequest, codeInvalidParams, "Request metadata is required in params._meta.")
	if backend.userCalls != 0 {
		t.Fatal("request metadata failure reached authentication")
	}
}

func TestMethodMirrorFailurePrecedesProtocolVersionMismatch(t *testing.T) {
	backend := testBackend(t)
	request := protocolRequest(t, "tools/list", nil)
	request.Header.Set("MCP-Protocol-Version", "unsupported-version")
	request.Header.Set("Mcp-Method", "tools/call")
	response := httptest.NewRecorder()
	NewHandler(backend, Options{}).ServeHTTP(response, request)
	assertToolGatewayFailure(t, response, http.StatusBadRequest, codeHeaderMismatch, "HTTP header does not match the request body.")
	var envelope struct {
		Error struct {
			Data map[string]string `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Data["header"] != "Mcp-Method" || backend.userCalls != 0 {
		t.Fatalf("first mirror failure = %q; user lookups=%d", envelope.Error.Data["header"], backend.userCalls)
	}
}

func TestPreflightDuplicateHeaderKeepsExactErrorAndCORSOrigin(t *testing.T) {
	backend := testBackend(t)
	request := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	request.Header.Set("Origin", "https://PORTAL.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "Accept, accept")
	response := httptest.NewRecorder()
	NewHandler(backend, Options{AllowedOrigins: []string{"https://portal.example"}}).ServeHTTP(response, request)
	assertToolGatewayFailure(t, response, http.StatusBadRequest, codeInvalidParams, "CORS preflight contains a duplicate header name.")
	if response.Header().Get("Access-Control-Allow-Origin") != "https://portal.example" || backend.userCalls != 0 {
		t.Fatalf("preflight headers=%v user lookups=%d", response.Header(), backend.userCalls)
	}
}

func TestRequestCancellationDuringAuthenticationStopsBeforeDispatch(t *testing.T) {
	backend := &cancelOnUserLookupBackend{
		protocolBoundaryBackend: &protocolBoundaryBackend{gatewayFakeStore: testBackend(t)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	backend.cancel = cancel
	request := protocolRequest(t, "tools/list", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	NewHandler(backend, Options{}).ServeHTTP(response, request)
	assertToolGatewayFailure(t, response, http.StatusRequestTimeout, codeInternalError, "Request was cancelled.")
	if backend.userCalls != 1 || backend.workspaceCalls != 0 {
		t.Fatalf("post-authentication cancellation user lookups=%d workspace reads=%d", backend.userCalls, backend.workspaceCalls)
	}
}

type cancelOnUserLookupBackend struct {
	*protocolBoundaryBackend
	cancel context.CancelFunc
}

func (backend *cancelOnUserLookupBackend) User(token string) (accounts.User, error) {
	user, err := backend.protocolBoundaryBackend.User(token)
	backend.cancel()
	return user, err
}
