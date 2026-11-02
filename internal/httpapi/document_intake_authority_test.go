package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocumentIntakeAuthorityInternalRouteRequiresPairCredentialAndOpaqueTicket(t *testing.T) {
	credential := strings.Repeat("d", 40)
	handler := documentIntakeAuthorityHandler(nil, credential)
	for _, tc := range []struct {
		name       string
		method     string
		credential string
		body       string
		status     int
	}{
		{"wrong method", http.MethodGet, credential, `{}`, http.StatusMethodNotAllowed},
		{"missing credential", http.MethodPost, "", `{}`, http.StatusUnauthorized},
		{"duplicate credential", http.MethodPost, credential, `{"ticket":"iat_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","body_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`, http.StatusUnauthorized},
		{"nonopaque ticket", http.MethodPost, credential, `{"ticket":"actor-1","body_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, documentIntakeAuthorityPath, strings.NewReader(tc.body))
			if tc.credential != "" {
				request.Header.Set("X-Contract-Service-Key", tc.credential)
			}
			if tc.name == "duplicate credential" {
				request.Header.Add("X-Contract-Service-Key", credential)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s want=%d", response.Code, response.Body.String(), tc.status)
			}
			if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
				t.Fatal("internal authority response is cacheable")
			}
		})
	}
}
