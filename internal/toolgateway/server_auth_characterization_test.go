package toolgateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func TestAuthenticationFailureAndBackendPriorityRemainExact(t *testing.T) {
	t.Run("backend availability precedes missing credentials", func(t *testing.T) {
		request := protocolRequest(t, "tools/list", nil)
		request.Header.Del("Cookie")
		response := httptest.NewRecorder()
		NewHandler(nil, Options{}).ServeHTTP(response, request)
		assertToolGatewayFailure(t, response, http.StatusServiceUnavailable, codeInternalError, "Gateway storage is unavailable.")
	})

	t.Run("malformed authorization is not bypassed by a valid cookie", func(t *testing.T) {
		backend := testBackend(t)
		request := protocolRequest(t, "tools/list", nil)
		request.Header.Set("Authorization", "Basic private-credential")
		response := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(response, request)
		assertToolGatewayFailure(t, response, http.StatusUnauthorized, codeUnauthenticated, "Authentication is required or no longer valid.")
		if backend.userCalls != 0 {
			t.Fatalf("malformed Authorization reached backend lookup %d times", backend.userCalls)
		}
	})

	t.Run("store unauthorized details are replaced with the public message", func(t *testing.T) {
		backend := &protocolBoundaryBackend{gatewayFakeStore: testBackend(t), userErr: errors.New("private session lookup detail")}
		request := protocolRequest(t, "tools/list", nil)
		response := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(response, request)
		assertToolGatewayFailure(t, response, http.StatusServiceUnavailable, codeInternalError, "Authentication service is unavailable.")
		if strings.Contains(response.Body.String(), "private session lookup detail") {
			t.Fatal("authentication storage detail escaped in the public response")
		}
	})
}

func TestAuthenticationCredentialBoundaryPrecedence(t *testing.T) {
	t.Run("duplicate session cookies do not fall back to a valid bearer", func(t *testing.T) {
		backend := testBackend(t)
		request := protocolRequest(t, "tools/list", nil)
		request.Header.Add("Cookie", "session=session-token")
		request.Header.Set("Authorization", "Bearer session-token")
		response := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(response, request)
		assertToolGatewayFailure(t, response, http.StatusUnauthorized, codeUnauthenticated, "Authentication is required or no longer valid.")
		if backend.userCalls != 0 {
			t.Fatalf("duplicate session cookies reached the user store %d times", backend.userCalls)
		}
	})

	t.Run("oversized session cookie does not fall back to a valid bearer", func(t *testing.T) {
		backend := testBackend(t)
		request := protocolRequest(t, "tools/list", nil)
		request.Header.Set("Cookie", "session="+strings.Repeat("x", 4097))
		request.Header.Set("Authorization", "Bearer session-token")
		response := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(response, request)
		assertToolGatewayFailure(t, response, http.StatusUnauthorized, codeUnauthenticated, "Authentication is required or no longer valid.")
		if backend.userCalls != 0 {
			t.Fatalf("oversized session cookie reached the user store %d times", backend.userCalls)
		}
	})

	t.Run("wrong bearer scheme without cookie never reaches user lookup", func(t *testing.T) {
		backend := testBackend(t)
		request := protocolRequest(t, "tools/list", nil)
		request.Header.Del("Cookie")
		request.Header.Set("Authorization", "Basic private-token")
		response := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(response, request)
		assertToolGatewayFailure(t, response, http.StatusUnauthorized, codeUnauthenticated, "Authentication is required or no longer valid.")
		if backend.userCalls != 0 {
			t.Fatalf("wrong-scheme Authorization reached the user store %d times", backend.userCalls)
		}
	})

	for _, source := range []string{"cookie", "bearer"} {
		t.Run("4096-byte "+source+" token remains accepted", func(t *testing.T) {
			token := strings.Repeat("x", 4096)
			backend := &boundaryCredentialBackend{gatewayFakeStore: testBackend(t), acceptedToken: token}
			request := protocolRequest(t, "tools/list", nil)
			if source == "cookie" {
				request.Header.Set("Cookie", "session="+token)
			} else {
				request.Header.Del("Cookie")
				request.Header.Set("Authorization", "Bearer "+token)
			}
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, request)
			if response.Code != http.StatusOK || backend.userCalls != 1 {
				t.Fatalf("exact-limit %s token status=%d user lookups=%d", source, response.Code, backend.userCalls)
			}
		})
	}
}

type boundaryCredentialBackend struct {
	*gatewayFakeStore
	acceptedToken string
}

func (backend *boundaryCredentialBackend) User(token string) (accounts.User, error) {
	backend.userCalls++
	if token != backend.acceptedToken {
		return accounts.User{}, fault.Unauthenticated("invalid token")
	}
	return backend.user, nil
}

func (backend *boundaryCredentialBackend) SessionAudience(token string) (string, error) {
	if token != backend.acceptedToken {
		return "", fault.Unauthenticated("invalid token")
	}
	return "client", nil
}

func TestOriginAndResponseMediaValidationKeepTheirOrder(t *testing.T) {
	t.Run("allowed HTTP Origin is canonicalized and accepted", func(t *testing.T) {
		backend := testBackend(t)
		request := protocolRequest(t, "tools/list", nil)
		request.Header.Set("Origin", "http://PORTAL.example")
		response := httptest.NewRecorder()
		NewHandler(backend, Options{AllowedOrigins: []string{"http://portal.example"}}).ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Header().Get("Access-Control-Allow-Origin") != "http://portal.example" {
			t.Fatalf("allowed HTTP Origin response status=%d allow-origin=%q", response.Code, response.Header().Get("Access-Control-Allow-Origin"))
		}
	})

	t.Run("origin rejection precedes method and Accept checks", func(t *testing.T) {
		backend := testBackend(t)
		request := protocolRequest(t, "tools/list", nil)
		request.Method = http.MethodGet
		request.Header.Set("Origin", "https://attacker.example")
		request.Header.Set("Accept", "application/json")
		request.Body = protocolUnreadableBody{}
		response := httptest.NewRecorder()
		NewHandler(backend, Options{AllowedOrigins: []string{"https://portal.example"}}).ServeHTTP(response, request)
		assertToolGatewayFailure(t, response, http.StatusForbidden, codeInvalidRequest, "Origin is not allowed.")
		if backend.userCalls != 0 {
			t.Fatal("rejected origin reached authentication")
		}
	})

	t.Run("content type rejection precedes Accept and body parsing", func(t *testing.T) {
		backend := testBackend(t)
		request := protocolRequest(t, "tools/list", nil)
		request.Header.Set("Content-Type", "text/plain")
		request.Header.Set("Accept", "application/json")
		request.Body = protocolUnreadableBody{}
		response := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(response, request)
		assertToolGatewayFailure(t, response, http.StatusUnsupportedMediaType, codeInvalidRequest, "Content-Type must be application/json.")
		if backend.userCalls != 0 {
			t.Fatal("invalid content type reached authentication")
		}
	})

	t.Run("Accept rejection precedes body parsing and authentication", func(t *testing.T) {
		backend := testBackend(t)
		request := protocolRequest(t, "tools/list", nil)
		request.Header.Del("Cookie")
		request.Header.Set("Accept", "application/json")
		request.Body = protocolUnreadableBody{}
		response := httptest.NewRecorder()
		NewHandler(backend, Options{}).ServeHTTP(response, request)
		assertToolGatewayFailure(t, response, http.StatusNotAcceptable, codeInvalidRequest, "Accept must include application/json and text/event-stream.")
		if backend.userCalls != 0 {
			t.Fatal("invalid Accept reached authentication")
		}
	})
}

func TestToolListRequiresAnExplicitReadGrant(t *testing.T) {
	for _, test := range []struct {
		name   string
		grants []access.Grant
		want   bool
	}{
		{name: "no grants defaults to no tool"},
		{
			name:   "matching case grant exposes read tool",
			grants: []access.Grant{{Role: "viewer", Scope: "case", CaseID: "mcp-tool-catalog"}},
			want:   true,
		},
		{
			name:   "read grant for another case still exposes read tool",
			grants: []access.Grant{{Role: "viewer", Scope: "case", CaseID: "other-case"}},
			want:   true,
		},
		{
			name:   "matching organization grant exposes read tool",
			grants: []access.Grant{{Role: "reviewer", Scope: "org", OrgID: "org-a"}},
			want:   true,
		},
		{
			name:   "read grant for another organization still exposes read tool",
			grants: []access.Grant{{Role: "reviewer", Scope: "org", OrgID: "org-b"}},
			want:   true,
		},
		{
			name:   "resolved subtree grant exposes read tool",
			grants: []access.Grant{{Role: "viewer", Scope: "org_subtree", ResolvedOrgIDs: []string{"", "org-a"}}},
			want:   true,
		},
		{name: "empty subtree grant does not expose tool", grants: []access.Grant{{Role: "viewer", Scope: "org_subtree", ResolvedOrgIDs: []string{""}}}},
		{
			name:   "tenant read grant exposes tool",
			grants: []access.Grant{{Role: "viewer", Scope: "tenant"}},
			want:   true,
		},
		{name: "unknown scope does not expose tool", grants: []access.Grant{{Role: "viewer", Scope: "all"}}},
		{
			name:   "expired grant does not expose tool",
			grants: []access.Grant{{Role: "viewer", Scope: "case", CaseID: "mcp-tool-catalog", ExpiresAt: time.Now().Add(-time.Second)}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := testBackend(t)
			configureToolListIdentity(t, backend, test.grants)
			request := protocolRequest(t, "tools/list", nil)
			response := httptest.NewRecorder()
			NewHandler(backend, Options{}).ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("tools/list status=%d body=%s", response.Code, response.Body.String())
			}
			var envelope struct {
				Result struct {
					Tools []struct {
						Name string `json:"name"`
					} `json:"tools"`
				} `json:"result"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if got := len(envelope.Result.Tools) == 1 && envelope.Result.Tools[0].Name == ToolName; got != test.want {
				t.Fatalf("read tool listed=%v, want %v; tools=%+v", got, test.want, envelope.Result.Tools)
			}
		})
	}
}

func configureToolListIdentity(t *testing.T, backend *gatewayFakeStore, grants []access.Grant) {
	t.Helper()
	user := accounts.User{
		ID: "tool-list-user", DisplayName: "Tool list user", Role: "viewer",
		TenantID: "tenant-a", OrgID: "org-a", Enabled: true,
		Issuer: "https://identity.example/realm/tool-list", Subject: "tool-list-subject", Grants: grants,
	}
	body, err := json.Marshal(struct {
		Users []accounts.User `json:"users"`
	}{Users: []accounts.User{user}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "directory.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = accounts.ConfigureDirectory("") })
	backend.user = user
}

func assertToolGatewayFailure(t *testing.T, response *httptest.ResponseRecorder, status, code int, message string) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Code != status || envelope.Error.Code != code || envelope.Error.Message != message {
		t.Fatalf("error response status=%d code=%d message=%q; want %d/%d/%q", response.Code, envelope.Error.Code, envelope.Error.Message, status, code, message)
	}
}
