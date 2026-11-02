package httpapi_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/identity"
	"contractaudit/internal/store"
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
		fixture.issuer.setNextClaims("subject-outside-directory", "")
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
		fixture.issuer.setNextClaims("", "unconfigured-client")
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

		if got := fixture.request(t, client, http.MethodDelete, "/api/session?audience=admin", nil).status; got != http.StatusNoContent {
			t.Fatalf("admin logout status=%d, want %d", got, http.StatusNoContent)
		}
		fixture.assertSession(t, client, "/api/session?audience=admin", http.StatusUnauthorized, "", "")
		fixture.assertSession(t, client, "/api/session", http.StatusOK, "oidc-client", identity.AudienceClient)

		if got := fixture.request(t, client, http.MethodDelete, "/api/session", nil).status; got != http.StatusNoContent {
			t.Fatalf("client logout status=%d, want %d", got, http.StatusNoContent)
		}
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
	fixture.issuer.setNextClaims("multi-subject", "")
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
	issuer    *localOIDCIssuer
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

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	issuer := &localOIDCIssuer{
		key: key, keyID: "local-test-key", codes: make(map[string]localOIDCCode),
	}
	issuer.server = httptest.NewServer(http.HandlerFunc(issuer.serveHTTP))
	t.Cleanup(issuer.server.Close)
	issuer.client = &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	base := "http://" + listener.Addr().String()
	issuer.expectedRedirectURL = base + "/api/auth/oidc/callback"
	adminBase := base
	if separateOrigins {
		adminBase = strings.Replace(base, "127.0.0.1", "localhost", 1)
	}
	issuer.expectedAdminRedirectURL = adminBase + "/api/auth/oidc/callback"

	users := []accounts.User{
		{
			ID: "oidc-client", DisplayName: "OIDC Client", Role: "reviewer",
			TenantID: "tenant-oidc", OrgID: "org-oidc", Enabled: true,
			Issuer: issuer.server.URL, Subject: "client-subject",
		},
		{
			ID: "oidc-admin", DisplayName: "OIDC Admin", Role: "admin",
			TenantID: "tenant-oidc", OrgID: "org-oidc", Enabled: true,
			Issuer: issuer.server.URL, Subject: "admin-subject",
		},
		{
			ID: "oidc-multi", DisplayName: "OIDC Multi", Role: "admin",
			TenantID: "directory-stale-multi", OrgID: "directory-stale-multi", Enabled: true,
			Issuer: issuer.server.URL, Subject: "multi-subject",
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
		Issuer:   issuer.server.URL,
		ClientID: "oidc-client-id", ClientSecret: "oidc-client-secret",
		AdminClientID: "oidc-admin-client-id", AdminClientSecret: "oidc-admin-client-secret",
		RedirectURL:        issuer.expectedRedirectURL,
		AdminRedirectURL:   issuer.expectedAdminRedirectURL,
		StateEncryptionKey: bytes.Repeat([]byte{0x42}, 32),
		HTTPClient:         issuer.server.Client(),
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
	if err != nil || parsedAuthorizationURL.Host != strings.TrimPrefix(f.issuer.server.URL, "http://") || parsedAuthorizationURL.Path != "/authorize" {
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
	callbackURL := f.issuer.authorize(t, authorizationURL)
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

type localOIDCIssuer struct {
	server                   *httptest.Server
	client                   *http.Client
	key                      *rsa.PrivateKey
	keyID                    string
	expectedRedirectURL      string
	expectedAdminRedirectURL string
	mu                       sync.Mutex
	nextCode                 int
	nextSubject              string
	nextTokenAudience        string
	codes                    map[string]localOIDCCode
}

type localOIDCCode struct {
	clientID      string
	redirectURL   string
	nonce         string
	codeChallenge string
	subject       string
	tokenAudience string
	used          bool
}

func (p *localOIDCIssuer) setNextClaims(subject, audience string) {
	p.mu.Lock()
	p.nextSubject = subject
	p.nextTokenAudience = audience
	p.mu.Unlock()
}

func (p *localOIDCIssuer) authorize(t *testing.T, authorizationURL string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, authorizationURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := p.client.Do(request)
	if err != nil {
		t.Fatalf("request local issuer authorization endpoint: %v", err)
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read local issuer authorization response: read=%v close=%v", readErr, closeErr)
	}
	if response.StatusCode != http.StatusFound {
		t.Fatalf("local issuer authorization status=%d, want %d", response.StatusCode, http.StatusFound)
	}
	return response.Header.Get("Location")
}

func (p *localOIDCIssuer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		writeOIDCTestJSON(w, map[string]string{
			"issuer": p.server.URL, "authorization_endpoint": p.server.URL + "/authorize",
			"token_endpoint": p.server.URL + "/token", "jwks_uri": p.server.URL + "/jwks",
		})
	case "/authorize":
		p.authorizeRequest(w, r)
	case "/token":
		p.tokenRequest(w, r)
	case "/jwks":
		p.jwks(w)
	default:
		http.NotFound(w, r)
	}
}

func (p *localOIDCIssuer) authorizeRequest(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	clientID := query.Get("client_id")
	expectedRedirect := p.expectedRedirectURL
	if clientID == "oidc-admin-client-id" {
		expectedRedirect = p.expectedAdminRedirectURL
	}
	if r.Method != http.MethodGet || (clientID != "oidc-client-id" && clientID != "oidc-admin-client-id") ||
		query.Get("response_type") != "code" || query.Get("redirect_uri") != expectedRedirect ||
		query.Get("state") == "" || query.Get("nonce") == "" || query.Get("code_challenge_method") != "S256" ||
		query.Get("code_challenge") == "" || !strings.Contains(" "+query.Get("scope")+" ", " openid ") {
		http.Error(w, "invalid authorization request", http.StatusBadRequest)
		return
	}

	p.mu.Lock()
	p.nextCode++
	code := "local-code-" + big.NewInt(int64(p.nextCode)).String()
	subject := "client-subject"
	if clientID == "oidc-admin-client-id" {
		subject = "admin-subject"
	}
	audience := clientID
	if p.nextSubject != "" {
		subject = p.nextSubject
	}
	if p.nextTokenAudience != "" {
		audience = p.nextTokenAudience
	}
	p.nextSubject, p.nextTokenAudience = "", ""
	p.codes[code] = localOIDCCode{
		clientID: clientID, redirectURL: query.Get("redirect_uri"), nonce: query.Get("nonce"),
		codeChallenge: query.Get("code_challenge"), subject: subject, tokenAudience: audience,
	}
	p.mu.Unlock()

	callback, err := url.Parse(query.Get("redirect_uri"))
	if err != nil {
		http.Error(w, "invalid redirect URI", http.StatusBadRequest)
		return
	}
	callbackQuery := callback.Query()
	callbackQuery.Set("code", code)
	callbackQuery.Set("state", query.Get("state"))
	callback.RawQuery = callbackQuery.Encode()
	http.Redirect(w, r, callback.String(), http.StatusFound)
}

func (p *localOIDCIssuer) tokenRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.ParseForm() != nil || r.PostForm.Get("grant_type") != "authorization_code" {
		http.Error(w, "invalid token request", http.StatusBadRequest)
		return
	}
	code := r.PostForm.Get("code")
	p.mu.Lock()
	authorization, exists := p.codes[code]
	clientSecret := ""
	switch r.PostForm.Get("client_id") {
	case "oidc-client-id":
		clientSecret = "oidc-client-secret"
	case "oidc-admin-client-id":
		clientSecret = "oidc-admin-client-secret"
	}
	verifier := r.PostForm.Get("code_verifier")
	challenge := sha256.Sum256([]byte(verifier))
	valid := exists && !authorization.used && clientSecret != "" &&
		authorization.clientID == r.PostForm.Get("client_id") &&
		r.PostForm.Get("client_secret") == clientSecret &&
		r.PostForm.Get("redirect_uri") == authorization.redirectURL && verifier != "" &&
		base64.RawURLEncoding.EncodeToString(challenge[:]) == authorization.codeChallenge
	if valid {
		authorization.used = true
		p.codes[code] = authorization
	}
	p.mu.Unlock()
	if !valid {
		http.Error(w, "authorization code rejected", http.StatusBadRequest)
		return
	}

	idToken, err := p.signIDToken(authorization)
	if err != nil {
		http.Error(w, "could not sign ID token", http.StatusInternalServerError)
		return
	}
	writeOIDCTestJSON(w, map[string]string{"id_token": idToken})
}

func (p *localOIDCIssuer) jwks(w http.ResponseWriter) {
	exponent := big.NewInt(int64(p.key.E)).Bytes()
	writeOIDCTestJSON(w, map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA", "kid": p.keyID, "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(p.key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(exponent),
		}},
	})
}

func (p *localOIDCIssuer) signIDToken(authorization localOIDCCode) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "RS256", "kid": p.keyID, "typ": "JWT"})
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	claims, err := json.Marshal(map[string]any{
		"iss": p.server.URL, "sub": authorization.subject, "aud": authorization.tokenAudience,
		"exp": now.Add(5 * time.Minute).Unix(), "iat": now.Unix(), "nonce": authorization.nonce,
	})
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func writeOIDCTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
