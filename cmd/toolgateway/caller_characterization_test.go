package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type countingCallerNext struct{ calls atomic.Int64 }

func (n *countingCallerNext) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	n.calls.Add(1)
	w.WriteHeader(http.StatusNoContent)
}

func TestRequireCallerRejectsInvalidCredentialsBeforeNext(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"missing service key", func(r *http.Request) {}},
		{"duplicate service keys", func(r *http.Request) {
			r.Header.Set("X-Contract-Service-Key", edgeCredential)
			r.Header.Add("X-Contract-Service-Key", edgeCredential)
		}},
		{"empty job context", func(r *http.Request) {
			r.Header.Set("X-Contract-Job-Context", "")
			r.Header.Set("X-Contract-Service-Key", edgeCredential)
		}},
		{"duplicate job contexts", func(r *http.Request) {
			r.Header.Add("X-Contract-Job-Context", jobContext)
			r.Header.Add("X-Contract-Job-Context", jobContext)
			r.Header.Set("X-Contract-Service-Key", workerCredential)
		}},
		{"worker key without claim", func(r *http.Request) {
			r.Header.Set("X-Contract-Service-Key", workerCredential)
		}},
		{"edge key with claim", func(r *http.Request) {
			r.Header.Set("X-Contract-Job-Context", jobContext)
			r.Header.Set("X-Contract-Service-Key", edgeCredential)
		}},
		{"wrong service key", func(r *http.Request) {
			r.Header.Set("X-Contract-Service-Key", strings.Repeat("x", 40))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			next := &countingCallerNext{}
			handler := requireCaller(edgeCredential, workerCredential, next)
			request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("invalid body is rejected before parsing"))
			request.Header.Set("Origin", "https://portal.example")
			test.mutate(request)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d, want 401", response.Code)
			}
			if response.Body.String() != "{\"error\":\"service authentication required\",\"code\":\"service_unauthenticated\"}" {
				t.Fatalf("unauthorized body changed: %s", response.Body.String())
			}
			if response.Header().Get("Content-Type") != "application/json; charset=utf-8" || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unauthorized response headers changed: %#v", response.Header())
			}
			if response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("caller middleware applied MCP CORS before authentication")
			}
			if next.calls.Load() != 0 {
				t.Fatalf("unauthorized caller reached next handler %d times", next.calls.Load())
			}
		})
	}
}

func TestRequireCallerPassesMatchingBranchesAndOptions(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		key    string
		claim  string
	}{
		{"edge public branch", http.MethodPost, edgeCredential, ""},
		{"worker claim branch", http.MethodPost, workerCredential, jobContext},
		{"preflight reaches MCP origin policy", http.MethodOptions, "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			next := &countingCallerNext{}
			handler := requireCaller(edgeCredential, workerCredential, next)
			request := httptest.NewRequest(test.method, "/mcp", nil)
			if test.key != "" {
				request.Header.Set("X-Contract-Service-Key", test.key)
			}
			if test.claim != "" {
				request.Header.Set("X-Contract-Job-Context", test.claim)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent || next.calls.Load() != 1 {
				t.Fatalf("status=%d calls=%d, want 204/1", response.Code, next.calls.Load())
			}
		})
	}
}

func TestMCPHandlerDelegatesPreflightOriginDecisionToMCP(t *testing.T) {
	backend := &fakeBackend{}
	handler := mcpHandler(backend, []string{"https://portal.example"}, edgeCredential, workerCredential)
	request := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	request.Header.Set("Origin", "https://portal.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "Content-Type, MCP-Protocol-Version, Mcp-Method, Mcp-Name")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "https://portal.example" {
		t.Fatalf("allowed preflight status=%d allow-origin=%q", response.Code, response.Header().Get("Access-Control-Allow-Origin"))
	}

	request = httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed preflight status=%d allow-origin=%q", response.Code, response.Header().Get("Access-Control-Allow-Origin"))
	}
	if backend.resolutions.Load() != 0 {
		t.Fatalf("preflight reached worker authority resolution %d times", backend.resolutions.Load())
	}
}

func TestEdgeCredentialUsesPublicSessionBranch(t *testing.T) {
	backend := &fakeBackend{}
	handler := mcpHandler(backend, nil, edgeCredential, workerCredential)
	request := workerRequest(t, edgeCredential, "")
	request.AddCookie(&http.Cookie{Name: "session", Value: "session-token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code == http.StatusUnauthorized {
		t.Fatalf("edge caller did not enter the public session branch: %s", response.Body.String())
	}
	if backend.resolutions.Load() != 0 {
		t.Fatalf("public edge call reached worker resolution %d times", backend.resolutions.Load())
	}
}

func TestConfiguredOriginsPreservesDefaultsAndConfiguredOrder(t *testing.T) {
	t.Setenv("CORS_ORIGIN", " \t")
	if got := configuredOrigins(); got != nil {
		t.Fatalf("configuredOrigins() with blank value = %#v, want nil", got)
	}
	t.Setenv("CORS_ORIGIN", " https://east.example , ,https://west.example  ")
	want := []string{"http://localhost:5173", "http://127.0.0.1:5173", "https://east.example", "https://west.example"}
	got := configuredOrigins()
	if len(got) != len(want) {
		t.Fatalf("configuredOrigins() = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("configuredOrigins() = %#v, want %#v", got, want)
		}
	}
}
