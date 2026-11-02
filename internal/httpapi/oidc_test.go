// File metadata (manifest mirror):
// Purpose: Exercises the `httpapi_test` package behavior through TestOIDCAuthorizationCodeHTTPFlowsUseAudienceBoundPostgresSessions, TestOIDCAuthorizationCodeSeparatesClientAndAdminOrigins; these checks pin observable state, error handling, or wire/database compatibility for this module. Test cases include a callback cannot combine state from one flow with another flow cookie, an unknown subject cannot create a session, an ID token for an unconfigured audience cannot create a session, client and admin authorization codes create isolated revocable sessions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/identity"
	"contractaudit/internal/store"
	"contractaudit/internal/testsupport/oidcfixture"
	"github.com/jackc/pgx/v5"
)

func TestOIDCAuthorizationCodeHTTPFlowsUseAudienceBoundPostgresSessions(t *testing.T) {
	fixture := newOIDCHTTPFixture(t, false)

	for _, path := range []string{
		"/api/auth/oidc/login",
		"/api/auth/oidc/login?audience=unknown",
		"/api/auth/oidc/login?audience=client&audience=admin",
	} {
		response := fixture.request(t, fixture.newClient(t), http.MethodGet, path, nil)
		if response.status != http.StatusBadRequest {
			t.Errorf("login %s status=%d, want %d", path, response.status, http.StatusBadRequest)
		}
	}

	t.Run("a callback cannot combine state from one flow with another flow cookie", func(t *testing.T) {
		client := fixture.newClient(t)
		first := fixture.startFlow(t, client, identity.AudienceClient)
		_ = fixture.startFlow(t, client, identity.AudienceClient)

		response := fixture.request(t, client, http.MethodGet, first.callbackURL, nil)
		if response.status != http.StatusUnauthorized {
			t.Fatalf("callback with a different flow cookie status=%d, want %d", response.status, http.StatusUnauthorized)
		}
		if got := fixture.request(t, client, http.MethodGet, "/api/session", nil).status; got != http.StatusUnauthorized {
			t.Fatalf("state/cookie mismatch created a session: status=%d", got)
		}
	})

	t.Run("an unknown subject cannot create a session", func(t *testing.T) {
		client := fixture.newClient(t)
		fixture.issuer.SetNextClaims("subject-outside-directory", "")
		flow := fixture.startFlow(t, client, identity.AudienceClient)

		response := fixture.request(t, client, http.MethodGet, flow.callbackURL, nil)
		if response.status != http.StatusUnauthorized {
			t.Fatalf("unknown subject callback status=%d, want %d", response.status, http.StatusUnauthorized)
		}
		if got := fixture.request(t, client, http.MethodGet, "/api/session", nil).status; got != http.StatusUnauthorized {
			t.Fatalf("unknown subject created a session: status=%d", got)
		}
	})

	t.Run("an ID token for an unconfigured audience cannot create a session", func(t *testing.T) {
		client := fixture.newClient(t)
		fixture.issuer.SetNextClaims("", "unconfigured-client")
		flow := fixture.startFlow(t, client, identity.AudienceClient)

		response := fixture.request(t, client, http.MethodGet, flow.callbackURL, nil)
		if response.status != http.StatusUnauthorized {
			t.Fatalf("unauthorized token audience callback status=%d, want %d", response.status, http.StatusUnauthorized)
		}
		if got := fixture.request(t, client, http.MethodGet, "/api/session", nil).status; got != http.StatusUnauthorized {
			t.Fatalf("unauthorized token audience created a session: status=%d", got)
		}
	})

	t.Run("client and admin authorization codes create isolated revocable sessions", func(t *testing.T) {
		client := fixture.newClient(t)

		clientFlow := fixture.startFlow(t, client, identity.AudienceClient)
		clientCallback := fixture.request(t, client, http.MethodGet, clientFlow.callbackURL, nil)
		if clientCallback.status != http.StatusSeeOther || clientCallback.header.Get("Location") != "/" {
			t.Fatalf("client callback status=%d location=%q", clientCallback.status, clientCallback.header.Get("Location"))
		}
		assertOIDCStateCookieCleared(t, clientCallback)
		fixture.assertSession(t, client, "/api/session", http.StatusOK, "oidc-client", identity.AudienceClient)
		clientCookie := fixture.sessionCookie(t, client, "session")

		// Reusing the state cookie and authorization code must fail at the issuer,
		// and must not replace the already established client session.
		replayCookie := *clientFlow.stateCookie
		replay := fixture.request(t, client, http.MethodGet, clientFlow.callbackURL, &replayCookie)
		if replay.status != http.StatusUnauthorized {
			t.Fatalf("replayed authorization code status=%d, want %d", replay.status, http.StatusUnauthorized)
		}
		for _, cookie := range replay.cookies {
			if cookie.Name == "session" {
				t.Fatal("replayed authorization code returned a client session cookie")
			}
		}
		if current := fixture.sessionCookie(t, client, "session"); current.Value != clientCookie.Value {
			t.Fatal("replayed authorization code replaced the existing client session")
		}
		fixture.assertSession(t, client, "/api/session", http.StatusOK, "oidc-client", identity.AudienceClient)

		adminFlow := fixture.startFlow(t, client, identity.AudienceAdmin)
		adminCallback := fixture.request(t, client, http.MethodGet, adminFlow.callbackURL, nil)
		if adminCallback.status != http.StatusSeeOther || adminCallback.header.Get("Location") != "/admin" {
			t.Fatalf("admin callback status=%d location=%q", adminCallback.status, adminCallback.header.Get("Location"))
		}
		assertOIDCStateCookieCleared(t, adminCallback)
		fixture.assertSession(t, client, "/api/session?audience=admin", http.StatusOK, "oidc-admin", identity.AudienceAdmin)
		fixture.assertSession(t, client, "/api/session", http.StatusOK, "oidc-client", identity.AudienceClient)

		clientSessionCookie := fixture.sessionCookie(t, client, "session")
		adminSessionCookie := fixture.sessionCookie(t, client, "admin_session")
		bareClient := fixture.newBareClient()
		misnamedClient := *clientSessionCookie
		misnamedClient.Name = "admin_session"
		if got := fixture.request(t, bareClient, http.MethodGet, "/api/admin/settings", &misnamedClient).status; got != http.StatusForbidden {
			t.Fatalf("client session used as admin session status=%d, want %d", got, http.StatusForbidden)
		}
		misnamedAdmin := *adminSessionCookie
		misnamedAdmin.Name = "session"
		if got := fixture.request(t, bareClient, http.MethodGet, "/api/session", &misnamedAdmin).status; got != http.StatusForbidden {
			t.Fatalf("admin session used as client session status=%d, want %d", got, http.StatusForbidden)
		}

		fixture.logoutSession(t, client, identity.AudienceAdmin)
		fixture.assertSession(t, client, "/api/session?audience=admin", http.StatusUnauthorized, "", "")
		fixture.assertSession(t, client, "/api/session", http.StatusOK, "oidc-client", identity.AudienceClient)

		fixture.logoutSession(t, client, identity.AudienceClient)
		fixture.assertSession(t, client, "/api/session", http.StatusUnauthorized, "", "")
	})
}

func TestOIDCAuthorizationCodeSeparatesClientAndAdminOrigins(t *testing.T) {
	fixture := newOIDCHTTPFixture(t, true)
	client := fixture.newClient(t)
	admin := *fixture
	admin.base = fixture.adminBase
	for _, entry := range []struct {
		fixture     *oidcHTTPFixture
		audience    string
		actor       string
		sessionPath string
	}{
		{fixture, identity.AudienceClient, "oidc-client", "/api/session"},
		{&admin, identity.AudienceAdmin, "oidc-admin", "/api/session?audience=admin"},
	} {
		flow := entry.fixture.startFlow(t, client, entry.audience)
		response := entry.fixture.request(t, client, http.MethodGet, flow.callbackURL, nil)
		if response.status != http.StatusSeeOther {
			t.Fatalf("%s callback status=%d: %s", entry.audience, response.status, response.body)
		}
		entry.fixture.assertSession(t, client, entry.sessionPath, http.StatusOK, entry.actor, entry.audience)
	}
	fixture.assertSession(t, client, "/api/session?audience=admin", http.StatusUnauthorized, "", "")
	admin.assertSession(t, client, "/api/session", http.StatusUnauthorized, "", "")
	fixture.assertSession(t, client, "/api/session", http.StatusOK, "oidc-client", identity.AudienceClient)
}

func TestOIDCPendingMembershipHTTPChoiceIsSameOriginAndOneTime(t *testing.T) {
	fixture := newOIDCHTTPFixture(t, false)
	client := fixture.newClient(t)
	fixture.issuer.SetNextClaims("multi-subject", "")
	flow := fixture.startFlow(t, client, identity.AudienceClient)
	callback := fixture.request(t, client, http.MethodGet, flow.callbackURL, nil)
	if callback.status != http.StatusSeeOther || callback.header.Get("Location") != "/login/membership" {
		t.Fatalf("multi-membership callback status=%d location=%q", callback.status, callback.header.Get("Location"))
	}
	var pendingCookie *http.Cookie
	for _, cookie := range callback.cookies {
		if cookie.Name == "oidc_pending" {
			pendingCookie = cookie
		}
	}
	if pendingCookie == nil || pendingCookie.Value == "" || !pendingCookie.HttpOnly || pendingCookie.Path != "/api/auth/oidc/memberships" || pendingCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("callback did not issue a protected pending cookie: %+v", pendingCookie)
	}

	listed := fixture.request(t, client, http.MethodGet, "/api/auth/oidc/memberships?audience=client", nil)
	if listed.status != http.StatusOK {
		t.Fatalf("pending list status=%d body=%s", listed.status, listed.body)
	}
	var choices struct {
		Memberships []struct {
			ID       string `json:"id"`
			TenantID string `json:"tenant_id"`
		} `json:"memberships"`
	}
	if err := json.Unmarshal(listed.body, &choices); err != nil || len(choices.Memberships) != 2 {
		t.Fatalf("pending membership list=%s err=%v", listed.body, err)
	}

	body, _ := json.Marshal(map[string]string{"membership_id": choices.Memberships[0].ID, "audience": "client"})
	wrongOrigin, err := fixture.requestBody(t, client, http.MethodPost, "/api/auth/oidc/memberships", body, pendingCookie, "https://attacker.example")
	if err != nil || wrongOrigin.status != http.StatusForbidden {
		t.Fatalf("cross-origin membership choice status=%d err=%v", wrongOrigin.status, err)
	}
	selected, err := fixture.requestBody(t, client, http.MethodPost, "/api/auth/oidc/memberships", body, pendingCookie, "")
	if err != nil || selected.status != http.StatusNoContent || len(selected.body) != 0 {
		t.Fatalf("membership selection status=%d body=%s err=%v", selected.status, selected.body, err)
	}
	fixture.assertSession(t, client, "/api/session", http.StatusOK, "oidc-multi", identity.AudienceClient)
	if _, err := fixture.requestBody(t, client, http.MethodPost, "/api/auth/oidc/memberships", body, pendingCookie, ""); err != nil {
		t.Fatal(err)
	} else if replay := fixture.request(t, client, http.MethodGet, "/api/auth/oidc/memberships?audience=client", pendingCookie); replay.status != http.StatusUnauthorized {
		t.Fatalf("consumed pending challenge remained usable: status=%d", replay.status)
	} else {
		cleared := false
		for _, cookie := range replay.cookies {
			if cookie.Name == "oidc_pending" && cookie.MaxAge < 0 {
				cleared = true
			}
		}
		if !cleared {
			t.Fatal("invalid or consumed pending challenge did not clear its browser cookie")
		}
	}
}

type oidcHTTPFixture struct {
	base      string
	adminBase string
	issuer    *oidcfixture.Provider
}

type oidcHTTPResponse struct {
	status  int
	header  http.Header
	cookies []*http.Cookie
	body    []byte
}

type oidcHTTPFlow struct {
	callbackURL string
	stateCookie *http.Cookie
}

func newOIDCHTTPFixture(t *testing.T, separateOrigins bool) *oidcHTTPFixture {
	t.Helper()
	testDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if testDSN == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL-backed OIDC membership flows")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	base := "http://" + listener.Addr().String()
	adminBase := base
	if separateOrigins {
		adminBase = strings.Replace(base, "127.0.0.1", "localhost", 1)
	}

	issuer := oidcfixture.New(t, oidcfixture.Config{ClientRedirectURL: base + "/api/auth/oidc/callback", AdminRedirectURL: adminBase + "/api/auth/oidc/callback"})

	users := []accounts.User{
		{
			ID: "oidc-client", DisplayName: "OIDC Client", Role: "reviewer",
			TenantID: "tenant-oidc", OrgID: "org-oidc", Enabled: true,
			Issuer: issuer.URL(), Subject: "client-subject",
		},
		{
			ID: "oidc-admin", DisplayName: "OIDC Admin", Role: "admin",
			TenantID: "tenant-oidc", OrgID: "org-oidc", Enabled: true,
			Issuer: issuer.URL(), Subject: "admin-subject",
		},
		{
			ID: "oidc-multi", DisplayName: "OIDC Multi", Role: "admin",
			TenantID: "directory-stale-multi", OrgID: "directory-stale-multi", Enabled: true,
			Issuer: issuer.URL(), Subject: "multi-subject",
		},
	}
	directoryPath := filepath.Join(t.TempDir(), "identity-directory.json")
	directoryJSON, err := json.Marshal(struct {
		Users []accounts.User `json:"users"`
	}{Users: users})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directoryPath, directoryJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(directoryPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := accounts.ConfigureDirectory(""); err != nil {
			t.Errorf("restore the default trusted directory: %v", err)
		}
	})

	st, err := store.OpenTest(t.TempDir(), testDSN)
	if err != nil {
		t.Fatal(err)
	}
	adminConfig, err := pgx.ParseConfig(testDSN)
	if err != nil {
		t.Fatal(err)
	}
	document := store.IdentityCrosswalk{
		Version: 1,
		Tenants: []store.IdentityTenantMap{
			{ID: "oidc-tenant-client", DisplayName: "Client tenant", Status: "active"},
			{ID: "oidc-tenant-admin", DisplayName: "Admin tenant", Status: "active"},
			{ID: "oidc-tenant-multi-a", DisplayName: "Multi tenant A", Status: "active"},
			{ID: "oidc-tenant-multi-b", DisplayName: "Multi tenant B", Status: "active"},
		},
		OrgUnits: []store.IdentityOrgUnitMap{
			{TenantID: "oidc-tenant-client", ID: "org-client", DisplayName: "Client org", Status: "active"},
			{TenantID: "oidc-tenant-admin", ID: "org-admin", DisplayName: "Admin org", Status: "active"},
			{TenantID: "oidc-tenant-multi-a", ID: "org-multi-a", DisplayName: "Multi org A", Status: "active"},
			{TenantID: "oidc-tenant-multi-b", ID: "org-multi-b", DisplayName: "Multi org B", Status: "active"},
		},
	}
	for i, user := range users {
		principalID := fmt.Sprintf("30000000-0000-4000-8000-%012d", i+1)
		document.Principals = append(document.Principals, store.IdentityPrincipalMap{ID: principalID, ActorID: user.ID, Issuer: user.Issuer, Subject: user.Subject, Status: "active"})
		tenantIDs, orgIDs := []string{"oidc-tenant-client"}, []string{"org-client"}
		if user.ID == "oidc-admin" {
			tenantIDs, orgIDs = []string{"oidc-tenant-admin"}, []string{"org-admin"}
		} else if user.ID == "oidc-multi" {
			tenantIDs, orgIDs = []string{"oidc-tenant-multi-a", "oidc-tenant-multi-b"}, []string{"org-multi-a", "org-multi-b"}
		}
		for j := range tenantIDs {
			roleCode := "reviewer"
			if user.ID == "oidc-admin" {
				roleCode = "admin"
			}
			document.Memberships = append(document.Memberships, store.IdentityMembershipMap{
				ID: fmt.Sprintf("40000000-0000-4000-8000-%012d", (i+1)*10+j+1), ActorID: user.ID,
				TenantID: tenantIDs[j], OrgUnitID: orgIDs[j], RoleCode: roleCode,
				Grants: []access.Grant{{Role: "reviewer", Scope: "tenant"}}, Status: "active",
			})
		}
	}
	if _, err := store.ImportIdentityCrosswalk(context.Background(), testDSN, st.SchemaName(), adminConfig.Database, document, true); err != nil {
		t.Fatalf("seed explicit OIDC membership crosswalk: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close OIDC test store: %v", err)
		}
	})

	manager, err := identity.NewManager(identity.Config{
		Issuer:   issuer.URL(),
		ClientID: "oidc-client-id", ClientSecret: "oidc-client-secret",
		AdminClientID: "oidc-admin-client-id", AdminClientSecret: "oidc-admin-client-secret",
		RedirectURL:        base + "/api/auth/oidc/callback",
		AdminRedirectURL:   adminBase + "/api/auth/oidc/callback",
		StateEncryptionKey: bytes.Repeat([]byte{0x42}, 32),
		HTTPClient:         issuer.HTTPClient(),
	}, accounts.BySubject, st)
	if err != nil {
		t.Fatal(err)
	}
	apiServer := &http.Server{Handler: httpapi.Handler(st, httpapi.Options{OIDC: manager})}
	serveDone := make(chan error, 1)
	go func() { serveDone <- apiServer.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := apiServer.Shutdown(ctx); err != nil {
			_ = apiServer.Close()
			t.Errorf("shut down OIDC test API: %v", err)
		}
		select {
		case err := <-serveDone:
			if err != nil && err != http.ErrServerClosed {
				t.Errorf("serve OIDC test API: %v", err)
			}
		case <-time.After(2 * time.Second):
			_ = apiServer.Close()
			t.Error("OIDC test API did not stop within its cleanup bound")
		}
	})

	return &oidcHTTPFixture{base: base, adminBase: adminBase, issuer: issuer}
}

func (f *oidcHTTPFixture) newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Jar: jar, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (f *oidcHTTPFixture) newBareClient() *http.Client {
	return &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (f *oidcHTTPFixture) request(t *testing.T, client *http.Client, method, path string, cookie *http.Cookie) oidcHTTPResponse {
	t.Helper()
	response, err := f.requestBody(t, client, method, path, nil, cookie, "")
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func (f *oidcHTTPFixture) requestBody(t *testing.T, client *http.Client, method, path string, body []byte, cookie *http.Cookie, origin string) (oidcHTTPResponse, error) {
	t.Helper()
	baseURL, err := url.Parse(f.base)
	if err != nil {
		t.Fatal(err)
	}
	target, err := baseURL.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(method, target.String(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet && method != http.MethodHead {
		if origin == "" {
			origin = target.Scheme + "://" + target.Host
		}
		request.Header.Set("Origin", origin)
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := client.Do(request)
	if err != nil {
		return oidcHTTPResponse{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		t.Fatalf("read %s %s response: %v", method, path, readErr)
	}
	if closeErr != nil {
		t.Fatalf("close %s %s response: %v", method, path, closeErr)
	}
	return oidcHTTPResponse{
		status: response.StatusCode, header: response.Header.Clone(),
		cookies: response.Cookies(), body: body,
	}, nil
}

func (f *oidcHTTPFixture) startFlow(t *testing.T, client *http.Client, audience string) oidcHTTPFlow {
	t.Helper()
	response := f.request(t, client, http.MethodGet, "/api/auth/oidc/login?audience="+url.QueryEscape(audience), nil)
	if response.status != http.StatusFound {
		t.Fatalf("OIDC login audience=%q status=%d, want %d: %s", audience, response.status, http.StatusFound, response.body)
	}
	authorizationURL := response.header.Get("Location")
	parsedAuthorizationURL, err := url.Parse(authorizationURL)
	if err != nil || parsedAuthorizationURL.Host != strings.TrimPrefix(f.issuer.URL(), "http://") || parsedAuthorizationURL.Path != "/authorize" {
		t.Fatalf("login did not redirect to the local issuer authorization endpoint: %q (%v)", authorizationURL, err)
	}
	var stateCookie *http.Cookie
	for _, cookie := range response.cookies {
		if cookie.Name == "oidc_state" {
			stateCookie = cookie
			break
		}
	}
	if stateCookie == nil || stateCookie.Value == "" || !stateCookie.HttpOnly || stateCookie.Path != "/api/auth/oidc" || stateCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("login did not set a protected OIDC state cookie: %+v", stateCookie)
	}
	callbackURL := f.issuer.Authorize(t, authorizationURL)
	return oidcHTTPFlow{callbackURL: callbackURL, stateCookie: stateCookie}
}

func (f *oidcHTTPFixture) assertSession(t *testing.T, client *http.Client, path string, status int, actorID, audience string) {
	t.Helper()
	response := f.request(t, client, http.MethodGet, path, nil)
	if response.status != status {
		t.Fatalf("GET %s status=%d, want %d: %s", path, response.status, status, response.body)
	}
	if status != http.StatusOK {
		return
	}
	var body struct {
		Actor struct {
			ID string `json:"id"`
		} `json:"actor"`
		Audience string `json:"audience"`
	}
	if err := json.Unmarshal(response.body, &body); err != nil {
		t.Fatalf("decode session response for %s: %v", path, err)
	}
	if body.Actor.ID != actorID || body.Audience != audience {
		t.Fatalf("GET %s returned actor=%q audience=%q, want actor=%q audience=%q", path, body.Actor.ID, body.Audience, actorID, audience)
	}
}

func (f *oidcHTTPFixture) logoutSession(t *testing.T, client *http.Client, audience string) {
	t.Helper()
	path := "/api/session?audience=" + url.QueryEscape(audience)
	session := f.request(t, client, http.MethodGet, path, nil)
	if session.status != http.StatusOK {
		t.Fatalf("read logout session audience=%q status=%d: %s", audience, session.status, session.body)
	}
	var metadata struct {
		store.SessionContext
		Audience string `json:"audience"`
	}
	if err := json.Unmarshal(session.body, &metadata); err != nil || metadata.Audience != audience || !store.ValidContextVersion(metadata.ActiveContext.ContextVersion) || metadata.CSRFToken == "" {
		t.Fatalf("invalid logout session context audience=%q: %v", audience, err)
	}
	request, err := http.NewRequest(http.MethodDelete, f.base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", f.base)
	request.Header.Set("X-Context-Version", metadata.ActiveContext.ContextVersion)
	request.Header.Set("X-CSRF-Token", metadata.CSRFToken)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf("logout audience=%q status=%d, want %d: %s", audience, response.StatusCode, http.StatusNoContent, body)
	}
}

func (f *oidcHTTPFixture) sessionCookie(t *testing.T, client *http.Client, name string) *http.Cookie {
	t.Helper()
	jar, ok := client.Jar.(*cookiejar.Jar)
	if !ok {
		t.Fatal("session cookie lookup requires a browser cookie jar")
	}
	baseURL, err := url.Parse(f.base)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range jar.Cookies(baseURL) {
		if cookie.Name == name && cookie.Value != "" {
			return cookie
		}
	}
	t.Fatalf("browser did not retain %q session cookie", name)
	return nil
}

func assertOIDCStateCookieCleared(t *testing.T, response oidcHTTPResponse) {
	t.Helper()
	for _, cookie := range response.cookies {
		if cookie.Name == "oidc_state" && cookie.MaxAge < 0 {
			return
		}
	}
	t.Fatal("OIDC callback did not clear its state cookie")
}
