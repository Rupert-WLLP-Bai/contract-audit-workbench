package httpapi_test

import (
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestClientAndAdminSessionsAreIndependentAndNotInterchangeable(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-admin", "admin-demo")
	base, err := url.Parse(api.base)
	if err != nil {
		t.Fatal(err)
	}
	var clientCookie *http.Cookie
	for _, cookie := range api.client.Jar.Cookies(base) {
		if cookie.Name == "session" {
			clientCookie = cookie
		}
	}
	if clientCookie == nil {
		t.Fatal("client login did not establish its session")
	}
	status, _ := api.do(t, http.MethodPost, "/api/session", map[string]any{"username": "demo-admin", "password": "admin-demo", "audience": "admin"})
	if status != http.StatusOK {
		t.Fatalf("admin login status=%d", status)
	}
	var adminCookie *http.Cookie
	for _, cookie := range api.client.Jar.Cookies(base) {
		if cookie.Name == "admin_session" {
			adminCookie = cookie
		}
	}
	if adminCookie == nil {
		t.Fatal("admin login did not establish its separate session")
	}
	status, _ = api.do(t, http.MethodGet, "/api/admin/settings", nil)
	if status != http.StatusOK {
		t.Fatalf("authorized admin settings status=%d", status)
	}
	withoutJar := &http.Client{Timeout: 5 * time.Second}
	requestWithCookie := func(path string, cookie *http.Cookie) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, api.base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(cookie)
		res, err := withoutJar.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if _, err := io.Copy(io.Discard, res.Body); err != nil {
			t.Fatal(err)
		}
		return res.StatusCode
	}
	if got := requestWithCookie("/api/admin/settings", clientCookie); got != http.StatusUnauthorized {
		t.Fatalf("client cookie granted admin settings: %d", got)
	}
	if got := requestWithCookie("/api/cases", adminCookie); got != http.StatusUnauthorized {
		t.Fatalf("admin cookie granted client cases: %d", got)
	}
	copiedAdmin := *adminCookie
	copiedAdmin.Name = "session"
	if got := requestWithCookie("/api/cases", &copiedAdmin); got != http.StatusForbidden {
		t.Fatalf("renamed admin token bypassed client audience: %d", got)
	}
	copiedClient := *clientCookie
	copiedClient.Name = "admin_session"
	if got := requestWithCookie("/api/admin/settings", &copiedClient); got != http.StatusForbidden {
		t.Fatalf("renamed client token bypassed admin audience: %d", got)
	}
	status, _ = api.do(t, http.MethodPost, "/api/session", map[string]any{"username": "demo-viewer", "password": "viewer-demo", "audience": "admin"})
	if status != http.StatusForbidden {
		t.Fatalf("viewer established admin session: %d", status)
	}
	status, _ = api.do(t, http.MethodDelete, "/api/session?audience=admin", nil)
	if status != http.StatusNoContent {
		t.Fatalf("admin logout status=%d", status)
	}
	status, _ = api.do(t, http.MethodGet, "/api/session", nil)
	if status != http.StatusOK {
		t.Fatalf("admin logout revoked independent client session: %d", status)
	}
	status, _ = api.do(t, http.MethodGet, "/api/session?audience=admin", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("admin session survived logout: %d", status)
	}
}
