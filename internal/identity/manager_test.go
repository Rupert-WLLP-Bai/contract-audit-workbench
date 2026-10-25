package identity

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
)

type testSessionStore struct {
	token    string
	audience string
	user     accounts.User
	created  int
}

func (s *testSessionStore) EstablishSession(user accounts.User, audience string) (string, error) {
	s.token = "test-session"
	s.audience = audience
	s.user = user
	s.created++
	return s.token, nil
}

func (s *testSessionStore) SessionAudience(token string) (string, error) {
	if token != s.token || token == "" {
		return "", ErrAuthentication
	}
	return s.audience, nil
}

type testMutation struct {
	name                string
	audience            string
	role                string
	stateMismatch       bool
	stateExpired        bool
	tamperedStateCookie bool
	badIDTokenSignature bool
	mutateClaims        func(map[string]any)
}

func TestManagerOIDCFlowAndRejectedResponses(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("constructor performs no provider requests", func(t *testing.T) {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests++
		}))
		defer server.Close()
		_, err := NewManager(Config{
			Issuer: server.URL, ClientID: "client-id", ClientSecret: "client-secret",
			AdminClientID: "admin-client-id", AdminClientSecret: "admin-client-secret",
			RedirectURL:        server.URL + "/api/auth/oidc/callback",
			StateEncryptionKey: bytes.Repeat([]byte{0x42}, 32), HTTPClient: server.Client(),
		}, func(string, string) (accounts.User, bool) { return accounts.User{}, false }, &testSessionStore{})
		if err != nil {
			t.Fatalf("NewManager() error = %v", err)
		}
		if requests != 0 {
			t.Fatalf("NewManager() made %d provider request(s)", requests)
		}
	})

	t.Run("client login exchanges PKCE and establishes audience session", func(t *testing.T) {
		manager, authorization, stateCookie, sessions := newOIDCTestManager(t, key, testMutation{
			name: "valid", audience: AudienceClient, role: "clerk",
		})
		query := authorization.Query()
		if query.Get("response_type") != "code" || query.Get("client_id") != "client-id" || query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" || query.Get("nonce") == "" || query.Get("state") == "" {
			t.Fatalf("authorization request missing OIDC or PKCE parameters: %v", query)
		}
		if strings.Contains(stateCookie, query.Get("state")) || strings.Contains(stateCookie, query.Get("nonce")) || strings.Contains(stateCookie, query.Get("code_challenge")) {
			t.Fatal("state cookie exposed its authenticated state payload")
		}
		token, audience, err := manager.Complete(context.Background(), "single-use-code", query.Get("state"), stateCookie)
		if err != nil {
			t.Fatalf("Complete() error = %v", err)
		}
		if token != "test-session" || audience != AudienceClient || sessions.created != 1 || sessions.audience != AudienceClient || sessions.user.Subject != "subject-1" {
			t.Fatalf("unexpected session: token=%q audience=%q sessions=%+v", token, audience, sessions)
		}
	})
	t.Run("admin login uses separate client and requires admin role", func(t *testing.T) {
		manager, authorization, stateCookie, sessions := newOIDCTestManager(t, key, testMutation{
			name: "valid admin", audience: AudienceAdmin, role: "admin",
		})
		query := authorization.Query()
		if query.Get("client_id") != "admin-client-id" {
			t.Fatalf("admin login used client ID %q", query.Get("client_id"))
		}
		token, audience, err := manager.Complete(context.Background(), "single-use-code", query.Get("state"), stateCookie)
		if err != nil {
			t.Fatalf("Complete() error = %v", err)
		}
		if token != "test-session" || audience != AudienceAdmin || sessions.audience != AudienceAdmin || sessions.user.Role != "admin" {
			t.Fatalf("unexpected admin session: token=%q audience=%q sessions=%+v", token, audience, sessions)
		}
	})

	cases := []testMutation{
		{name: "state mismatch", audience: AudienceClient, role: "clerk", stateMismatch: true},
		{name: "expired state cookie", audience: AudienceClient, role: "clerk", stateExpired: true},
		{name: "state cookie tampering", audience: AudienceClient, role: "clerk", tamperedStateCookie: true},
		{name: "ID token signature failure", audience: AudienceClient, role: "clerk", badIDTokenSignature: true},
		{name: "nonce mismatch", audience: AudienceClient, role: "clerk", mutateClaims: func(claims map[string]any) { claims["nonce"] = "wrong-nonce" }},
		{name: "audience mismatch", audience: AudienceClient, role: "clerk", mutateClaims: func(claims map[string]any) { claims["aud"] = "some-other-client" }},
		{name: "issuer mismatch", audience: AudienceClient, role: "clerk", mutateClaims: func(claims map[string]any) { claims["iss"] = "https://wrong.example" }},
		{name: "expired token", audience: AudienceClient, role: "clerk", mutateClaims: func(claims map[string]any) { claims["exp"] = time.Now().Add(-time.Minute).Unix() }},
		{name: "multiple audience without matching azp", audience: AudienceClient, role: "clerk", mutateClaims: func(claims map[string]any) { claims["aud"] = []string{"client-id", "other-client"} }},
		{name: "wrong azp", audience: AudienceClient, role: "clerk", mutateClaims: func(claims map[string]any) {
			claims["aud"] = []string{"client-id", "other-client"}
			claims["azp"] = "other-client"
		}},
		{name: "non-admin admin login", audience: AudienceAdmin, role: "reviewer"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manager, authorization, stateCookie, sessions := newOIDCTestManager(t, key, testCase)
			state := authorization.Query().Get("state")
			if testCase.stateMismatch {
				state += "changed"
			}
			if testCase.tamperedStateCookie {
				stateCookie = corruptBase64Value(stateCookie)
			}
			if testCase.stateExpired {
				var err error
				stateCookie, err = manager.sealState(statePayload{
					State: authorization.Query().Get("state"), Nonce: authorization.Query().Get("nonce"),
					Verifier: "test-verifier", Audience: AudienceClient, IssuedAt: time.Now().Add(-6 * time.Minute).Unix(),
				})
				if err != nil {
					t.Fatalf("seal expired state: %v", err)
				}
			}
			_, _, err := manager.Complete(context.Background(), "single-use-code", state, stateCookie)
			if err == nil {
				t.Fatal("Complete() accepted an invalid OIDC response")
			}
			if sessions.created != 0 {
				t.Fatalf("invalid OIDC response established %d session(s)", sessions.created)
			}
		})
	}
}

func newOIDCTestManager(t *testing.T, signingKey *rsa.PrivateKey, testCase testMutation) (*Manager, *url.URL, string, *testSessionStore) {
	t.Helper()
	var expectedNonce string
	var expectedChallenge string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(providerMetadata{
				Issuer: server.URL, AuthorizationEndpoint: server.URL + "/authorize",
				TokenEndpoint: server.URL + "/token", JWKSURI: server.URL + "/jwks",
			})
		case "/token":
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			clientID, clientSecret := "client-id", "client-secret"
			if testCase.audience == AudienceAdmin {
				clientID, clientSecret = "admin-client-id", "admin-client-secret"
			}
			verifier := r.PostForm.Get("code_verifier")
			challengeBytes := sha256.Sum256([]byte(verifier))
			if r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("code") != "single-use-code" || r.PostForm.Get("client_id") != clientID || r.PostForm.Get("client_secret") != clientSecret || verifier == "" || base64.RawURLEncoding.EncodeToString(challengeBytes[:]) != expectedChallenge || r.PostForm.Get("redirect_uri") != server.URL+"/api/auth/oidc/callback" {
				http.Error(w, "bad exchange", http.StatusBadRequest)
				return
			}
			claims := map[string]any{
				"iss": server.URL, "sub": "subject-1", "aud": clientID,
				"exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": expectedNonce,
			}
			if testCase.mutateClaims != nil {
				testCase.mutateClaims(claims)
			}
			idToken := signTestIDToken(t, signingKey, claims)
			if testCase.badIDTokenSignature {
				idToken = corruptTokenSignature(idToken)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id_token": idToken})
		case "/jwks":
			exponent := bigEndian(signingKey.E)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(jwksDocument{Keys: []jwk{{
				KeyType: "RSA", KeyID: "test-key", Use: "sig", Algorithm: "RS256",
				Modulus:  base64.RawURLEncoding.EncodeToString(signingKey.N.Bytes()),
				Exponent: base64.RawURLEncoding.EncodeToString(exponent),
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	sessions := &testSessionStore{}
	userRole := testCase.role
	manager, err := NewManager(Config{
		Issuer:   server.URL,
		ClientID: "client-id", ClientSecret: "client-secret",
		AdminClientID: "admin-client-id", AdminClientSecret: "admin-client-secret",
		RedirectURL:        server.URL + "/api/auth/oidc/callback",
		StateEncryptionKey: bytes.Repeat([]byte{0x42}, 32), HTTPClient: server.Client(),
	}, func(issuer, subject string) (accounts.User, bool) {
		if issuer != server.URL || subject != "subject-1" {
			return accounts.User{}, false
		}
		return accounts.User{ID: "user-1", Role: userRole, Enabled: true, Issuer: issuer, Subject: subject}, true
	}, sessions)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	audience := testCase.audience
	if audience == "" {
		audience = AudienceClient
	}
	authorizationURL, stateCookie, err := manager.Begin(context.Background(), audience)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	authorization, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	expectedNonce = authorization.Query().Get("nonce")
	expectedChallenge = authorization.Query().Get("code_challenge")
	return manager, authorization, stateCookie, sessions
}

func signTestIDToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "kid": "test-key", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := encodedHeader + "." + encodedPayload
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func bigEndian(value int) []byte {
	var result [strconv.IntSize / 8]byte
	for index := len(result) - 1; index >= 0; index-- {
		result[index] = byte(value)
		value >>= 8
	}
	first := 0
	for first < len(result)-1 && result[first] == 0 {
		first++
	}
	return result[first:]
}

func corruptBase64Value(value string) string {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) == 0 {
		return value + "!"
	}
	raw[len(raw)-1] ^= 1
	return base64.RawURLEncoding.EncodeToString(raw)
}

func corruptTokenSignature(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return token + "!"
	}
	parts[2] = corruptBase64Value(parts[2])
	return strings.Join(parts, ".")
}
