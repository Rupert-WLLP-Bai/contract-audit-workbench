package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExperienceResultEndpointHasIndependentWorkerCredential(t *testing.T) {
	gateway := strings.Repeat("g", 40)
	worker := strings.Repeat("w", 40)
	if _, err := ServiceHandler(nil, Options{Durable: true, ExperienceWorkerCredential: gateway}, "core", gateway); err == nil {
		t.Fatal("gateway and worker credentials must be distinct")
	}
	h, err := ServiceHandler(nil, Options{Durable: true, ExperienceWorkerCredential: worker}, "core", gateway)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, method, credential, body string
		status                         int
	}{
		{"missing", http.MethodPost, "", `{}`, http.StatusUnauthorized},
		{"gateway cannot deliver", http.MethodPost, gateway, `{}`, http.StatusUnauthorized},
		{"authenticated invalid payload", http.MethodPost, worker, `[]`, http.StatusUnprocessableEntity},
		{"method rejected", http.MethodGet, worker, "", http.StatusMethodNotAllowed},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "/internal/v1/experience-results", strings.NewReader(test.body))
			if test.credential != "" {
				req.Header.Set("X-Contract-Service-Key", test.credential)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, test.status, rec.Body.String())
			}
		})
	}
}
