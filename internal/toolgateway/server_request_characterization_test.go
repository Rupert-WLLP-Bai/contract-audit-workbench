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
	for _, header := range []string{"Accept", "Authorization", "Content-Type", "Mcp-Method", "Mcp-Name", "MCP-Protocol-Version"} {
		t.Run(header, func(t *testing.T) {
			backend := testBackend(t)
			request := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
			request.Header.Set("Origin", "https://PORTAL.example")
			request.Header.Set("Access-Control-Request-Method", http.MethodPost)
			request.Header.Set("Access-Control-Request-Headers", header+", "+strings.ToLower(header))
			response := httptest.NewRecorder()
			NewHandler(backend, Options{AllowedOrigins: []string{"https://portal.example"}}).ServeHTTP(response, request)
			assertToolGatewayFailure(t, response, http.StatusBadRequest, codeInvalidParams, "CORS preflight contains a duplicate header name.")
			if response.Header().Get("Access-Control-Allow-Origin") != "https://portal.example" || backend.userCalls != 0 {
				t.Fatalf("preflight headers=%v user lookups=%d", response.Header(), backend.userCalls)
			}
		})
	}
}

func TestPreflightRejectsLeadingAndTrailingEmptyHeaderEntries(t *testing.T) {
	for _, headerList := range []string{",accept", "accept,"} {
		t.Run(strings.ReplaceAll(headerList, ",", "_comma_"), func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
			request.Header.Set("Origin", "https://portal.example")
			request.Header.Set("Access-Control-Request-Method", http.MethodPost)
			request.Header.Set("Access-Control-Request-Headers", headerList)
			response := httptest.NewRecorder()
			NewHandler(testBackend(t), Options{AllowedOrigins: []string{"https://portal.example"}}).ServeHTTP(response, request)
			assertToolGatewayFailure(t, response, http.StatusBadRequest, codeInvalidParams, "CORS preflight contains an invalid header name.")
		})
	}
}

func TestJSONArrayScannerRejectsUnterminatedAndNestedInvalidArrays(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{"unterminated root array", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":[`},
		{"array closes with object delimiter", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":[1}`},
		{"duplicate object key nested in array", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":[{"x":1,"x":2}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := protocolRequest(t, "tools/list", nil)
			request.Body = io.NopCloser(strings.NewReader(test.body))
			response := httptest.NewRecorder()
			backend := testBackend(t)
			NewHandler(backend, Options{}).ServeHTTP(response, request)
			assertToolGatewayFailure(t, response, http.StatusBadRequest, codeParseError, "Request must contain exactly one JSON object.")
			if backend.userCalls != 0 {
				t.Fatal("malformed array reached authentication")
			}
		})
	}
}

func TestJSONArrayScannerAcceptsNestedArraysInsideMetadataObjects(t *testing.T) {
	request := protocolRequest(t, "tools/list", func(body map[string]any) {
		meta := body["params"].(map[string]any)["_meta"].(map[string]any)
		meta["io.modelcontextprotocol/clientCapabilities"] = map[string]any{
			"extensions": []any{1, []any{2, map[string]any{"value": true}}},
		}
	})
	backend := testBackend(t)
	response := httptest.NewRecorder()
	NewHandler(backend, Options{}).ServeHTTP(response, request)
	if response.Code != http.StatusOK || backend.userCalls != 1 {
		t.Fatalf("valid nested JSON arrays were rejected: status=%d user lookups=%d body=%s", response.Code, backend.userCalls, response.Body.String())
	}
}

func TestEncodedMirroredToolNameRejectsMalformedAndPinsEmptyBoundary(t *testing.T) {
	for _, test := range []struct {
		name        string
		requestName string
		headerName  string
		wantCode    int
		wantMessage string
		wantHeader  string
	}{
		{"empty payload remains an exact empty mirrored value", "", "=?base64??=", codeInvalidParams, "Unknown tool.", ""},
		{"missing encoded suffix can decode but must not mirror", "a", "=?base64?YQ==xx", codeHeaderMismatch, "HTTP header does not match the request body.", "Mcp-Name"},
		{"missing encoded prefix can decode but must not mirror", "a", "123456789YQ==?=", codeHeaderMismatch, "HTTP header does not match the request body.", "Mcp-Name"},
		{"short encoded prefix is rejected before slicing", "", "=?base64?", codeHeaderMismatch, "HTTP header does not match the request body.", "Mcp-Name"},
		{"overlapping encoded prefix and suffix are rejected before slicing", "", "=?base64?=", codeHeaderMismatch, "HTTP header does not match the request body.", "Mcp-Name"},
		{"missing encoded prefix cannot mirror a literal", "foo", "123456789Zm9v?=", codeHeaderMismatch, "HTTP header does not match the request body.", "Mcp-Name"},
		{"missing encoded suffix cannot mirror a literal", "foo", "=?base64?Zm9vab", codeHeaderMismatch, "HTTP header does not match the request body.", "Mcp-Name"},
		{"invalid base64 with a decodable prefix is rejected", "foo", "=?base64?Zm9v!?=", codeHeaderMismatch, "HTTP header does not match the request body.", "Mcp-Name"},
		{"invalid base64 cannot mirror an empty tool name", "", "=?base64?%%%?=", codeHeaderMismatch, "HTTP header does not match the request body.", "Mcp-Name"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := protocolRequest(t, "tools/call", func(body map[string]any) {
				body["params"].(map[string]any)["name"] = test.requestName
			})
			request.Header.Set("Mcp-Name", test.headerName)
			response := httptest.NewRecorder()
			NewHandler(testBackend(t), Options{}).ServeHTTP(response, request)
			assertToolGatewayFailure(t, response, http.StatusBadRequest, test.wantCode, test.wantMessage)
			if test.wantHeader != "" {
				var envelope struct {
					Error struct {
						Data map[string]string `json:"data"`
					} `json:"error"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Error.Data["header"] != test.wantHeader {
					t.Fatalf("header mismatch data = %v, want %q", envelope.Error.Data, test.wantHeader)
				}
			}
		})
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
