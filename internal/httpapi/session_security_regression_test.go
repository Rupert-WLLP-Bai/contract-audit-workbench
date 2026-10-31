package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"contractaudit/internal/store"
)

func securityTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatalf("open isolated PostgreSQL store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close isolated PostgreSQL store: %v", err)
		}
	})
	return st
}

func securityLogin(t *testing.T, api http.Handler, username, secret, audience string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": secret, "audience": audience})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://core.internal/api/session", bytes.NewReader(body))
	req.RemoteAddr = "192.0.2.10:1234"
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	return rec
}

func TestCookieWritesRequireTrustedOriginBehindProxy(t *testing.T) {
	st := securityTestStore(t)
	api := Handler(st, Options{SecureCookies: true, CORSOrigins: []string{"https://audit.example"}})
	login := securityLogin(t, api, "demo-admin", "admin-demo", "client")
	if login.Code != http.StatusOK {
		t.Fatalf("login: status=%d body=%s", login.Code, login.Body.String())
	}
	var session *http.Cookie
	for _, cookie := range login.Result().Cookies() {
		if cookie.Name == "session" {
			session = cookie
		}
	}
	if session == nil {
		t.Fatal("login returned no client session cookie")
	}

	cases := []struct {
		name       string
		origins    []string
		referer    string
		fetchSite  string
		forwarded  string
		wantStatus int
	}{
		{name: "missing browser provenance", wantStatus: http.StatusForbidden},
		{name: "fetch metadata alone", fetchSite: "same-origin", wantStatus: http.StatusForbidden},
		{name: "foreign referer", referer: "https://attacker.example/form", wantStatus: http.StatusForbidden},
		{name: "foreign origin overrides referer", origins: []string{"https://attacker.example"}, referer: "https://audit.example/form", wantStatus: http.StatusForbidden},
		{name: "spoofed proxy headers", origins: []string{"https://attacker.example"}, forwarded: "attacker.example", wantStatus: http.StatusForbidden},
		{name: "multiple origins", origins: []string{"https://audit.example", "https://attacker.example"}, wantStatus: http.StatusForbidden},
		{name: "configured origin", origins: []string{"https://audit.example"}, wantStatus: http.StatusCreated},
		{name: "configured referer", referer: "https://audit.example/workbench?tab=cases", wantStatus: http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://core.internal/api/cases", strings.NewReader(`{"title":"Origin regression","contract_type":"procurement_ict"}`))
			req.AddCookie(session)
			for _, origin := range tc.origins {
				req.Header.Add("Origin", origin)
			}
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}
			if tc.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			if tc.forwarded != "" {
				req.Header.Set("X-Forwarded-Host", tc.forwarded)
				req.Header.Set("X-Forwarded-Proto", "https")
			}
			rec := httptest.NewRecorder()
			api.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusForbidden && rec.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("rejected origin was reflected in CORS headers")
			}
		})
	}

	list := httptest.NewRequest(http.MethodGet, "http://core.internal/api/cases", nil)
	list.AddCookie(session)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, list)
	var payload struct {
		Items []json.RawMessage `json:"items"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &payload) != nil || len(payload.Items) != 2 {
		t.Fatalf("rejected writes changed cases: status=%d body=%s", response.Code, response.Body.String())
	}

	preflight := httptest.NewRequest(http.MethodOptions, "http://core.internal/api/cases", nil)
	preflight.Header.Set("Origin", "https://audit.example")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPut)
	response = httptest.NewRecorder()
	api.ServeHTTP(response, preflight)
	if response.Code != http.StatusNoContent || !strings.Contains(response.Header().Get("Access-Control-Allow-Methods"), http.MethodPut) {
		t.Fatalf("configured PUT preflight failed: status=%d headers=%v", response.Code, response.Header())
	}

	misconfigured := Handler(st, Options{SecureCookies: true, CORSOrigins: []string{"https://audit.example/path"}})
	req := httptest.NewRequest(http.MethodPost, "http://core.internal/api/cases", strings.NewReader(`{}`))
	req.AddCookie(session)
	req.Header.Set("Origin", "https://audit.example")
	req.Header.Set("X-Forwarded-Host", "audit.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	response = httptest.NewRecorder()
	misconfigured.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("malformed external origin configuration accepted browser write: %d", response.Code)
	}
}

func TestOriginlessServiceCredentialCanUseSessionEndpoint(t *testing.T) {
	st := securityTestStore(t)
	credential := strings.Repeat("gateway-service-key-", 3)
	api, err := ServiceHandler(st, Options{Durable: true, SecureCookies: true, CORSOrigins: []string{"https://audit.example"}}, "core", credential)
	if err != nil {
		t.Fatal(err)
	}
	request := func(key string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		body := `{"username":"demo-admin","password":"admin-demo"}`
		req := httptest.NewRequest(http.MethodPost, "http://core.internal/api/session", strings.NewReader(body))
		req.Header.Set("X-Contract-Service-Key", key)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, req)
		return rec
	}
	if got := request("wrong-service-key", nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("wrong service credential was accepted: %d", got.Code)
	}
	accepted := request(credential, nil)
	if accepted.Code != http.StatusOK {
		t.Fatalf("credentialed request without a cookie or browser origin failed: %d %s", accepted.Code, accepted.Body.String())
	}
	var session *http.Cookie
	for _, cookie := range accepted.Result().Cookies() {
		if cookie.Name == "session" {
			session = cookie
		}
	}
	if session == nil {
		t.Fatal("successful service request did not establish a session")
	}
	if got := request(credential, session); got.Code != http.StatusForbidden {
		t.Fatalf("service credential bypassed cookie origin checks: %d", got.Code)
	}
}

func TestLoginFailuresAndLockoutHaveSameResponse(t *testing.T) {
	st := securityTestStore(t)
	for _, audience := range []string{"client", "admin"} {
		t.Run(audience, func(t *testing.T) {
			api := Handler(st, Options{})
			if good := securityLogin(t, api, "demo-admin", "admin-demo", audience); good.Code != http.StatusOK {
				t.Fatalf("control login failed: %d %s", good.Code, good.Body.String())
			}
			known := securityLogin(t, api, "demo-admin", "wrong-secret", audience)
			unknown := securityLogin(t, api, "not-a-registered-account", "wrong-secret", audience)
			if known.Code != http.StatusUnauthorized || unknown.Code != http.StatusUnauthorized {
				t.Fatalf("failed credentials returned unexpected statuses: known=%d unknown=%d", known.Code, unknown.Code)
			}
			var baseline any
			if err := json.Unmarshal(known.Body.Bytes(), &baseline); err != nil {
				t.Fatalf("decode failed login: %v", err)
			}
			assertEquivalent := func(label string, response *httptest.ResponseRecorder) {
				t.Helper()
				var body any
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatalf("%s response is not JSON: %v", label, err)
				}
				if response.Code != known.Code || !reflect.DeepEqual(body, baseline) ||
					response.Header().Get("Content-Type") != known.Header().Get("Content-Type") ||
					response.Header().Get("Cache-Control") != known.Header().Get("Cache-Control") ||
					len(response.Result().Cookies()) != 0 {
					t.Fatalf("%s differed from failed credentials: status=%d body=%s headers=%v", label, response.Code, response.Body.String(), response.Header())
				}
			}
			assertEquivalent("unknown username", unknown)
			for i := 1; i < loginAccountFailureLimit+1; i++ {
				assertEquivalent("repeated known failure", securityLogin(t, api, "demo-admin", "wrong-secret", audience))
				assertEquivalent("repeated unknown failure", securityLogin(t, api, "not-a-registered-account", "wrong-secret", audience))
			}
			// A correct password proves that the real account is now locked.
			assertEquivalent("locked known account", securityLogin(t, api, "demo-admin", "admin-demo", audience))
			assertEquivalent("locked unknown account", securityLogin(t, api, "not-a-registered-account", "wrong-secret", audience))
		})
	}
}
