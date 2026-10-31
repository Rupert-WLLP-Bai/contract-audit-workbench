package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"contractaudit/internal/store"
)

func TestServiceObjectRoutesAreMountedAndRequireAnActor(t *testing.T) {
	handler := Handler(&store.Store{}, Options{})
	tests := []struct{ method, path string }{
		{http.MethodGet, "/api/cases/c/service-objects"},
		{http.MethodPost, "/api/cases/c/service-objects"},
		{http.MethodGet, "/api/cases/c/service-objects/o"},
		{http.MethodGet, "/api/cases/c/service-objects/o/versions"},
		{http.MethodGet, "/api/cases/c/service-objects/o/versions/1"},
		{http.MethodGet, "/api/cases/c/service-objects/o/associations"},
		{http.MethodGet, "/api/cases/c/service-objects/o/associations/1"},
		{http.MethodGet, "/api/cases/c/service-objects/o/association-candidates?run_id=r&attempt_id=a&material_id=m&material_version=1&document_id=d&parsed_document_id=p"},
		{http.MethodPost, "/api/cases/c/service-objects/o/associations"},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code == http.StatusNotFound || response.Code == http.StatusMethodNotAllowed {
				t.Fatalf("route is not mounted: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestServiceObjectPageQueryBoundsLimit(t *testing.T) {
	for _, test := range []struct {
		query     string
		wantLimit int
		wantError bool
	}{
		{"", 0, false},
		{"?limit=1&cursor=abc", 1, false},
		{"?limit=100", 100, false},
		{"?limit=0", 0, true},
		{"?limit=101", 0, true},
		{"?limit=abc", 0, true},
	} {
		t.Run(test.query, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/"+test.query, nil)
			cursor, limit, err := serviceObjectPageQuery(request)
			if (err != nil) != test.wantError || limit != test.wantLimit {
				t.Fatalf("serviceObjectPageQuery() = (%q,%d,%v), want limit=%d error=%v", cursor, limit, err, test.wantLimit, test.wantError)
			}
		})
	}
}
