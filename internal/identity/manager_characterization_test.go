package identity

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
)

func testLookup(string, string) (accounts.User, bool) { return accounts.User{}, false }

func validIdentityConfig() Config {
	return Config{
		Issuer: "https://identity.example", ClientID: "client-id", ClientSecret: "client-secret",
		AdminClientID: "admin-client-id", AdminClientSecret: "admin-client-secret",
		RedirectURL:        "https://app.example/api/auth/oidc/callback",
		StateEncryptionKey: bytes.Repeat([]byte{0x31}, 32),
	}
}

func TestNewManagerRejectsInvalidStartupConfiguration(t *testing.T) {
	sessions := &testSessionStore{}
	tests := []struct {
		name   string
		lookup DirectoryLookup
		config func(*Config)
		want   string
	}{
		{name: "missing directory", lookup: nil, config: func(*Config) {}, want: "OIDC identity directory and session store are required"},
		{name: "short state key", lookup: testLookup, config: func(c *Config) { c.StateEncryptionKey = bytes.Repeat([]byte{1}, 16) }, want: "OIDC state encryption key must be exactly 32 bytes"},
		{name: "long state key", lookup: testLookup, config: func(c *Config) { c.StateEncryptionKey = bytes.Repeat([]byte{1}, 33) }, want: "OIDC state encryption key must be exactly 32 bytes"},
		{name: "relative issuer", lookup: testLookup, config: func(c *Config) { c.Issuer = "identity.example" }, want: "invalid OIDC issuer: must be an absolute URL without credentials, query, or fragment"},
		{name: "issuer userinfo", lookup: testLookup, config: func(c *Config) { c.Issuer = "https://user:pass@identity.example" }, want: "invalid OIDC issuer: must be an absolute URL without credentials, query, or fragment"},
		{name: "issuer query", lookup: testLookup, config: func(c *Config) { c.Issuer = "https://identity.example?tenant=1" }, want: "invalid OIDC issuer: must be an absolute URL without credentials, query, or fragment"},
		{name: "issuer fragment", lookup: testLookup, config: func(c *Config) { c.Issuer = "https://identity.example#issuer" }, want: "invalid OIDC issuer: must be an absolute URL without credentials, query, or fragment"},
		{name: "issuer parse error", lookup: testLookup, config: func(c *Config) { c.Issuer = "https://identity.example/%zz" }, want: "invalid OIDC issuer:"},
		{name: "issuer unsupported scheme", lookup: testLookup, config: func(c *Config) { c.Issuer = "ftp://identity.example" }, want: "invalid OIDC issuer: HTTPS is required except for loopback development endpoints"},
		{name: "issuer plain http", lookup: testLookup, config: func(c *Config) { c.Issuer = "http://identity.example" }, want: "invalid OIDC issuer: HTTPS is required except for loopback development endpoints"},
		{name: "relative redirect", lookup: testLookup, config: func(c *Config) { c.RedirectURL = "/api/auth/oidc/callback" }, want: "invalid OIDC redirect URL: must be an absolute URL without credentials, query, or fragment"},
		{name: "redirect query", lookup: testLookup, config: func(c *Config) { c.RedirectURL = "https://app.example/callback?next=1" }, want: "invalid OIDC redirect URL: must be an absolute URL without credentials, query, or fragment"},
		{name: "redirect fragment", lookup: testLookup, config: func(c *Config) { c.RedirectURL = "https://app.example/callback#done" }, want: "invalid OIDC redirect URL: must be an absolute URL without credentials, query, or fragment"},
		{name: "redirect plain http", lookup: testLookup, config: func(c *Config) { c.RedirectURL = "http://app.example/callback" }, want: "invalid OIDC redirect URL: HTTPS is required except for loopback development endpoints"},
		{name: "invalid admin redirect", lookup: testLookup, config: func(c *Config) { c.AdminRedirectURL = "/admin/callback" }, want: "invalid OIDC admin redirect URL: must be an absolute URL without credentials, query, or fragment"},
		{name: "missing client credentials", lookup: testLookup, config: func(c *Config) { c.ClientSecret = "" }, want: "both OIDC client and admin client credentials are required"},
		{name: "missing admin client id", lookup: testLookup, config: func(c *Config) { c.AdminClientID = "" }, want: "both OIDC client and admin client credentials are required"},
		{name: "missing admin secret", lookup: testLookup, config: func(c *Config) { c.AdminClientSecret = "" }, want: "both OIDC client and admin client credentials are required"},
		{name: "shared client ids", lookup: testLookup, config: func(c *Config) { c.AdminClientID = c.ClientID }, want: "OIDC client and admin client IDs must be distinct"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validIdentityConfig()
			test.config(&config)
			manager, err := NewManager(config, test.lookup, sessions)
			if err == nil || manager != nil {
				t.Fatalf("invalid configuration accepted: %v", err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), test.want)
			}
		})
	}
	if _, err := NewManager(validIdentityConfig(), testLookup, nil); err == nil {
		t.Fatal("a missing session store was accepted")
	}
}

func TestNewManagerDefaultsAdminRedirectAndClonesHTTPClient(t *testing.T) {
	config := validIdentityConfig()
	config.AdminRedirectURL = ""
	client := &http.Client{Timeout: 3 * time.Second}
	config.HTTPClient = client
	manager, err := NewManager(config, testLookup, &testSessionStore{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if manager.config.AdminRedirectURL != config.RedirectURL {
		t.Fatalf("admin redirect %q, want %q", manager.config.AdminRedirectURL, config.RedirectURL)
	}
	if manager.client == client {
		t.Fatal("the supplied HTTP client was not cloned")
	}
	if manager.client.Timeout != 3*time.Second {
		t.Fatalf("client timeout %v", manager.client.Timeout)
	}
	if manager.client.CheckRedirect == nil {
		t.Fatal("redirect following was not disabled")
	}
	if err := manager.client.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect policy %v", err)
	}
	config.StateEncryptionKey[0] ^= 0xff
	if manager.config.StateEncryptionKey[0] == config.StateEncryptionKey[0] {
		t.Fatal("the state key was not copied")
	}

	config = validIdentityConfig()
	config.HTTPClient = &http.Client{}
	config.Issuer = "http://localhost:8080"
	config.RedirectURL = "http://127.0.0.1:8080/callback"
	manager, err = NewManager(config, testLookup, &testSessionStore{})
	if err != nil {
		t.Fatalf("loopback configuration rejected: %v", err)
	}
	if manager.client.Timeout != 10*time.Second {
		t.Fatalf("default client timeout %v", manager.client.Timeout)
	}
}

type identityRoundTripFunc func(*http.Request) (*http.Response, error)

func (f identityRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func identityResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func discoveryManager(t *testing.T, issuer string, discovery func() *http.Response) *Manager {
	t.Helper()
	config := validIdentityConfig()
	config.Issuer = issuer
	config.RedirectURL = "https://app.example/api/auth/oidc/callback"
	config.HTTPClient = &http.Client{Transport: identityRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			return discovery(), nil
		}
		return identityResponse(http.StatusNotFound, "{}"), nil
	})}
	manager, err := NewManager(config, testLookup, &testSessionStore{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return manager
}

func discoveryMetadata(t *testing.T, issuer, authorization, token, jwks string) *http.Response {
	t.Helper()
	body, err := json.Marshal(providerMetadata{Issuer: issuer, AuthorizationEndpoint: authorization, TokenEndpoint: token, JWKSURI: jwks})
	if err != nil {
		t.Fatal(err)
	}
	return identityResponse(http.StatusOK, string(body))
}

func TestDiscoverAcceptsOnlyApprovedProviderEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		issuer     string
		discovery  func(*testing.T) *http.Response
		wantReject bool
	}{
		{
			name: "https endpoints", issuer: "https://identity.example",
			discovery: func(t *testing.T) *http.Response {
				return discoveryMetadata(t, "https://identity.example", "https://identity.example/authorize", "https://identity.example/token", "https://identity.example/jwks")
			},
		},
		{
			name: "plain http endpoint behind non-loopback issuer", issuer: "https://identity.example",
			discovery: func(t *testing.T) *http.Response {
				return discoveryMetadata(t, "https://identity.example", "http://127.0.0.1:9000/authorize", "https://identity.example/token", "https://identity.example/jwks")
			},
			wantReject: true,
		},
		{
			name: "loopback issuer", issuer: "http://localhost:8080",
			discovery: func(t *testing.T) *http.Response {
				return discoveryMetadata(t, "http://localhost:8080", "http://localhost:8080/authorize", "http://localhost:8080/token", "http://localhost:8080/jwks")
			},
		},
		{
			name: "plain http non-loopback endpoint", issuer: "https://identity.example",
			discovery: func(t *testing.T) *http.Response {
				return discoveryMetadata(t, "https://identity.example", "http://identity.example/authorize", "https://identity.example/token", "https://identity.example/jwks")
			},
			wantReject: true,
		},
		{
			name: "relative endpoint", issuer: "https://identity.example",
			discovery: func(t *testing.T) *http.Response {
				return discoveryMetadata(t, "https://identity.example", "authorize", "https://identity.example/token", "https://identity.example/jwks")
			},
			wantReject: true,
		},
		{
			name: "missing endpoint", issuer: "https://identity.example",
			discovery: func(t *testing.T) *http.Response {
				return discoveryMetadata(t, "https://identity.example", "", "https://identity.example/token", "https://identity.example/jwks")
			},
			wantReject: true,
		},
		{
			name: "endpoint userinfo", issuer: "https://identity.example",
			discovery: func(t *testing.T) *http.Response {
				return discoveryMetadata(t, "https://identity.example", "https://operator@identity.example/authorize", "https://identity.example/token", "https://identity.example/jwks")
			},
			wantReject: true,
		},
		{
			name: "endpoint fragment", issuer: "https://identity.example",
			discovery: func(t *testing.T) *http.Response {
				return discoveryMetadata(t, "https://identity.example", "https://identity.example/authorize#fragment", "https://identity.example/token", "https://identity.example/jwks")
			},
			wantReject: true,
		},
		{
			name: "endpoint scheme", issuer: "https://identity.example",
			discovery: func(t *testing.T) *http.Response {
				return discoveryMetadata(t, "https://identity.example", "ftp://identity.example/authorize", "https://identity.example/token", "https://identity.example/jwks")
			},
			wantReject: true,
		},
		{
			name: "issuer mismatch", issuer: "https://identity.example",
			discovery: func(t *testing.T) *http.Response {
				return discoveryMetadata(t, "https://other.example", "https://other.example/authorize", "https://other.example/token", "https://other.example/jwks")
			},
			wantReject: true,
		},
		{
			name: "discovery failure", issuer: "https://identity.example",
			discovery:  func(*testing.T) *http.Response { return identityResponse(http.StatusInternalServerError, "{}") },
			wantReject: true,
		},
		{
			name: "discovery is not json", issuer: "https://identity.example",
			discovery:  func(*testing.T) *http.Response { return identityResponse(http.StatusOK, "not json") },
			wantReject: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := discoveryManager(t, test.issuer, func() *http.Response { return test.discovery(t) })
			authorization, cookie, err := manager.Begin(context.Background(), AudienceClient)
			if test.wantReject {
				if !errors.Is(err, ErrAuthentication) || authorization != "" || cookie != "" {
					t.Fatalf("unapproved discovery accepted: %q %q %v", authorization, cookie, err)
				}
				return
			}
			if err != nil || !strings.HasPrefix(authorization, "https://") && !strings.HasPrefix(authorization, "http://") || cookie == "" {
				t.Fatalf("approved discovery rejected: %q %q %v", authorization, cookie, err)
			}
		})
	}
	manager := discoveryManager(t, "https://identity.example", func() *http.Response {
		return discoveryMetadata(t, "https://identity.example", "https://identity.example/authorize", "https://identity.example/token", "https://identity.example/jwks")
	})
	if _, _, err := manager.Begin(context.Background(), "audience"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("unknown audience accepted: %v", err)
	}
	var nilManager *Manager
	if _, _, err := nilManager.Begin(context.Background(), AudienceClient); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("nil manager accepted Begin: %v", err)
	}
	if _, _, err := nilManager.Complete(context.Background(), "code", "state", "cookie"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("nil manager accepted Complete: %v", err)
	}
}

func sealRawState(t *testing.T, key, plaintext []byte) string {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, plaintext, []byte(stateCookieAAD)))
}

func TestOpenStateRejectsEveryUnverifiedCookieShape(t *testing.T) {
	key := bytes.Repeat([]byte{0x31}, 32)
	config := validIdentityConfig()
	config.StateEncryptionKey = key
	manager, err := NewManager(config, testLookup, &testSessionStore{})
	if err != nil {
		t.Fatal(err)
	}
	validPayload := []byte(`{"state":"s","nonce":"n","verifier":"v","audience":"client","issued_at":1}`)
	tests := []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "oversized", value: strings.Repeat("a", 4097)},
		{name: "padded base64", value: "abc=def"},
		{name: "not base64", value: "!!!not-base64"},
		{name: "shorter than a nonce", value: base64.RawURLEncoding.EncodeToString([]byte{1, 2, 3})},
		{name: "invalid plaintext", value: sealRawState(t, key, []byte("not json"))},
		{name: "missing state", value: sealRawState(t, key, []byte(`{"nonce":"n","verifier":"v","audience":"client","issued_at":1}`))},
		{name: "missing nonce", value: sealRawState(t, key, []byte(`{"state":"s","verifier":"v","audience":"client","issued_at":1}`))},
		{name: "missing verifier", value: sealRawState(t, key, []byte(`{"state":"s","nonce":"n","audience":"client","issued_at":1}`))},
		{name: "missing issued-at", value: sealRawState(t, key, []byte(`{"state":"s","nonce":"n","verifier":"v","audience":"client"}`))},
		{name: "unknown audience", value: sealRawState(t, key, []byte(`{"state":"s","nonce":"n","verifier":"v","audience":"audience","issued_at":1}`))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := manager.openState(test.value)
			if err == nil || payload != (statePayload{}) {
				t.Fatalf("invalid state cookie accepted: %+v %v", payload, err)
			}
		})
	}

	sealed := sealRawState(t, key, validPayload)
	corrupted := []byte(sealed)
	corrupted[len(corrupted)-1] ^= 1
	if _, err := manager.openState(string(corrupted)); err == nil {
		t.Fatal("tampered state cookie accepted")
	}
	wrongKey := bytes.Repeat([]byte{0x41}, 32)
	if _, err := manager.openState(sealRawState(t, wrongKey, validPayload)); err == nil {
		t.Fatal("state cookie sealed with another key accepted")
	}
	payload, err := manager.sealState(statePayload{State: "s", Nonce: "n", Verifier: "v", Audience: AudienceAdmin, IssuedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := manager.openState(payload)
	if err != nil || opened.Audience != AudienceAdmin || opened.State != "s" {
		t.Fatalf("valid state cookie rejected: %+v %v", opened, err)
	}

	shortKey := &Manager{config: Config{StateEncryptionKey: []byte("short")}}
	if _, err := shortKey.sealState(statePayload{State: "s"}); err == nil {
		t.Fatal("sealState accepted a short key")
	}
	if _, err := shortKey.openState(sealRawState(t, key, validPayload)); err == nil {
		t.Fatal("openState accepted a short key")
	}
}

type failingSessionStore struct{ testSessionStore }

func (s *failingSessionStore) EstablishSession(accounts.User, string) (string, error) {
	return "", ErrAuthentication
}

type emptyTokenSessionStore struct{}

func (s *emptyTokenSessionStore) EstablishSession(accounts.User, string) (string, error) {
	return "", nil
}
func (s *emptyTokenSessionStore) SessionAudience(string) (string, error) { return "", nil }

type skewedSessionStore struct{ testSessionStore }

func (s *skewedSessionStore) SessionAudience(string) (string, error) { return AudienceAdmin, nil }

func TestCompleteDeniesUnmappedDisabledOrMismatchedUsersAndSessions(t *testing.T) {
	tests := []struct {
		name      string
		audience  string
		mutate    func(*oidcTokenFixture)
		store     SessionStore
		tokenRole string
	}{
		{name: "unknown subject", mutate: func(f *oidcTokenFixture) {
			f.token = f.tokenWithClaims(func(claims map[string]any) { claims["sub"] = "subject-2" })
		}},
		{name: "disabled user", mutate: func(f *oidcTokenFixture) {
			f.manager.lookup = func(issuer, subject string) (accounts.User, bool) {
				return accounts.User{ID: "user-1", Role: "clerk", Enabled: false, Issuer: issuer, Subject: subject}, true
			}
		}},
		{name: "user identity mismatch", mutate: func(f *oidcTokenFixture) {
			f.manager.lookup = func(issuer, subject string) (accounts.User, bool) {
				return accounts.User{ID: "user-1", Role: "clerk", Enabled: true, Issuer: issuer, Subject: "other-subject"}, true
			}
		}},
		{name: "admin login without admin role", audience: AudienceAdmin, tokenRole: "admin-client-id"},
		{name: "session establishment failure", store: &failingSessionStore{}, mutate: func(*oidcTokenFixture) {}},
		{name: "session token missing", store: &emptyTokenSessionStore{}, mutate: func(*oidcTokenFixture) {}},
		{name: "stored audience mismatch", store: &skewedSessionStore{}, mutate: func(*oidcTokenFixture) {}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOIDCTokenFixture(t)
			fixture.token = fixture.sign(fixture.signedHeader(), fixture.validClaims())
			audience := test.audience
			if audience == "" {
				audience = AudienceClient
			}
			cookie, err := fixture.manager.sealState(statePayload{
				State: fixtureState, Nonce: fixtureNonce, Verifier: "verifier", Audience: audience, IssuedAt: time.Now().Unix(),
			})
			if err != nil {
				t.Fatal(err)
			}
			fixture.cookie = cookie
			if test.tokenRole != "" {
				fixture.token = fixture.sign(fixture.signedHeader(), fixture.tokenWithRoleClaims(t, test.tokenRole))
			}
			if test.mutate != nil {
				test.mutate(fixture)
			}
			if test.store != nil {
				fixture.manager.sessions = test.store
			}
			_, returnedAudience, err := fixture.manager.Complete(context.Background(), "single-use-code", fixtureState, fixture.cookie)
			if !errors.Is(err, ErrAuthentication) || returnedAudience != "" {
				t.Fatalf("invalid user or session accepted: audience=%q err=%v", returnedAudience, err)
			}
			if fixture.sessions.created != 0 {
				t.Fatalf("invalid flow established %d session(s)", fixture.sessions.created)
			}
		})
	}
}

func (f *oidcTokenFixture) tokenWithRoleClaims(t *testing.T, audience string) map[string]any {
	t.Helper()
	claims := f.validClaims()
	claims["aud"] = audience
	return claims
}

func TestCompleteAcceptsAdministratorIntoTheAdminAudience(t *testing.T) {
	fixture := newOIDCTokenFixture(t)
	fixture.manager.lookup = func(issuer, subject string) (accounts.User, bool) {
		return accounts.User{ID: "user-1", Role: "admin", Enabled: true, Issuer: issuer, Subject: subject}, true
	}
	cookie, err := fixture.manager.sealState(statePayload{
		State: fixtureState, Nonce: fixtureNonce, Verifier: "verifier", Audience: AudienceAdmin, IssuedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.token = fixture.sign(fixture.signedHeader(), fixture.tokenWithRoleClaims(t, "admin-client-id"))
	token, audience, err := fixture.manager.Complete(context.Background(), "single-use-code", fixtureState, cookie)
	if err != nil || token != "test-session" || audience != AudienceAdmin || fixture.sessions.audience != AudienceAdmin {
		t.Fatalf("admin session: token=%q audience=%q sessions=%+v err=%v", token, audience, fixture.sessions, err)
	}
}

func TestCompleteRejectsExpiredAndMismatchedStateCookies(t *testing.T) {
	fixture := newOIDCTokenFixture(t)
	fixture.token = fixture.sign(fixture.signedHeader(), fixture.validClaims())
	expired, err := fixture.manager.sealState(statePayload{
		State: fixtureState, Nonce: fixtureNonce, Verifier: "verifier", Audience: AudienceClient, IssuedAt: time.Now().Add(-6 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.manager.Complete(context.Background(), "single-use-code", fixtureState, expired); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expired state accepted: %v", err)
	}
	if _, _, err := fixture.manager.Complete(context.Background(), "single-use-code", "other-state", fixture.cookie); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("mismatched state accepted: %v", err)
	}
	if _, _, err := fixture.manager.Complete(context.Background(), "", fixtureState, fixture.cookie); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("empty code accepted: %v", err)
	}
}
