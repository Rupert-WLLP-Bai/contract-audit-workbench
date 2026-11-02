package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCoreSessionAuthorityRequiresOneBearerAndPost(t *testing.T) {
	handler := coreSessionAuthorityHandler(nil)
	for _, test := range []struct {
		name   string
		method string
		auth   []string
		want   int
	}{
		{name: "method", method: http.MethodGet, want: http.StatusMethodNotAllowed},
		{name: "missing bearer", method: http.MethodPost, want: http.StatusUnauthorized},
		{name: "duplicate bearer", method: http.MethodPost, auth: []string{"Bearer one", "Bearer two"}, want: http.StatusUnauthorized},
		{name: "malformed bearer", method: http.MethodPost, auth: []string{"Bearer two tokens"}, want: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/internal/v1/session-authority", nil)
			for _, value := range test.auth {
				request.Header.Add("Authorization", value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%q, want %d", response.Code, response.Body.String(), test.want)
			}
			if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
				t.Fatalf("authority response is cacheable: %v", response.Header())
			}
		})
	}
}
