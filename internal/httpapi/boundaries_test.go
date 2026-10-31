package httpapi

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"contractaudit/internal/fault"
)

func TestJSONRequiresExactlyOneBoundedObject(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"valid", `{"name":"中文"}`, 0},
		{"empty-object", `{}`, 0},
		{"whitespace", " \n{}\t ", 0},
		{"empty", "", 422},
		{"null", `null`, 422},
		{"array", `[]`, 422},
		{"unknown-field", `{"extra":true}`, 422},
		{"two-objects", `{} {}`, 422},
		{"trailing-close", `{}]`, 422},
		{"trailing-garbage", `{}no`, 422},
		{"trailing-null", `{}null`, 422},
		{"truncated", `{"name":`, 422},
		{"oversized", `{"name":"` + strings.Repeat("a", 1<<20) + `"}`, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			var body struct {
				Name string `json:"name"`
			}
			err := readJSON(w, r, &body)
			if tc.status == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var fe *fault.Error
			if !errors.As(err, &fe) || fe.Status != tc.status {
				t.Fatalf("error = %v; expected HTTP %d", err, tc.status)
			}
		})
	}
}

func TestOriginalMediaAllowsOnlyPassiveInlineFormats(t *testing.T) {
	for _, tc := range []struct {
		media  string
		inline bool
	}{
		{"text/plain; charset=utf-8", true}, {"image/png", true}, {"image/jpeg", true},
		{"text/html", false}, {"image/svg+xml", false}, {"application/pdf", false},
		{"text/html\r\nInjected: yes", false}, {"", false},
	} {
		media, inline := originalMedia(tc.media)
		if inline != tc.inline || (!inline && media != "application/octet-stream") {
			t.Fatalf("%q => %q inline=%v", tc.media, media, inline)
		}
	}
}

func TestOriginGuardBlocksBrowserWritesBeforeHandler(t *testing.T) {
	for _, tc := range []struct {
		name, method, origin string
		tls                  bool
		status               int
	}{
		{"trusted-ui", "POST", "http://localhost:5173", false, 204},
		{"unconfigured-plain-http", "POST", "http://api.example", false, 403},
		{"tls-same-origin", "POST", "https://api.example", true, 204},
		{"untrusted-write", "POST", "http://attacker.example", false, 403},
		{"untrusted-delete", "DELETE", "http://attacker.example", false, 403},
		{"null-origin", "POST", "null", false, 403},
		{"untrusted-preflight", "OPTIONS", "http://attacker.example", false, 403},
		{"non-browser", "POST", "", false, 204},
		{"cross-origin-read", "GET", "http://attacker.example", false, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hit := false
			h := cors([]string{"http://localhost:5173"}, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hit = true
				w.WriteHeader(http.StatusNoContent)
			}))
			r := httptest.NewRequest(tc.method, "http://api.example/api/cases", nil)
			r.Header.Set("Origin", tc.origin)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || (tc.status == 403 && hit) {
				t.Fatalf("status=%d hit=%v", w.Code, hit)
			}
			if tc.origin == "http://attacker.example" && w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("untrusted origin was reflected")
			}
		})
	}
}
