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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
)

const (
	fixtureClientID = "client-id"
	fixtureNonce    = "fixture-nonce"
	fixtureState    = "fixture-state"
)

type oidcTokenFixture struct {
	t        *testing.T
	key      *rsa.PrivateKey
	server   *httptest.Server
	manager  *Manager
	sessions *testSessionStore
	token    string
	jwks     any
	cookie   string
}

func newOIDCTokenFixture(t *testing.T) *oidcTokenFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &oidcTokenFixture{t: t, key: key, sessions: &testSessionStore{}}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeIdentityJSON(t, w, providerMetadata{
				Issuer: fixture.server.URL, AuthorizationEndpoint: fixture.server.URL + "/authorize",
				TokenEndpoint: fixture.server.URL + "/token", JWKSURI: fixture.server.URL + "/jwks",
			})
		case "/token":
			writeIdentityJSON(t, w, map[string]string{"id_token": fixture.token})
		case "/jwks":
			if responder, ok := fixture.jwks.(func(http.ResponseWriter)); ok {
				responder(w)
				return
			}
			writeIdentityJSON(t, w, fixture.jwks)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fixture.server.Close)
	fixture.jwks = jwksForKey(key, "test-key")
	fixture.manager, err = NewManager(Config{
		Issuer: fixture.server.URL, ClientID: fixtureClientID, ClientSecret: "client-secret",
		AdminClientID: "admin-client-id", AdminClientSecret: "admin-client-secret",
		RedirectURL:        fixture.server.URL + "/api/auth/oidc/callback",
		StateEncryptionKey: bytes.Repeat([]byte{0x51}, 32), HTTPClient: fixture.server.Client(),
	}, func(issuer, subject string) (accounts.User, bool) {
		if issuer != fixture.server.URL || subject != "subject-1" {
			return accounts.User{}, false
		}
		return accounts.User{ID: "user-1", Role: "clerk", Enabled: true, Issuer: issuer, Subject: subject}, true
	}, fixture.sessions)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	fixture.cookie, err = fixture.manager.sealState(statePayload{
		State: fixtureState, Nonce: fixtureNonce, Verifier: "verifier", Audience: AudienceClient, IssuedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("seal state: %v", err)
	}
	return fixture
}

func writeIdentityJSON(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func (f *oidcTokenFixture) signedHeader() map[string]any {
	return map[string]any{"alg": "RS256", "kid": "test-key", "typ": "JWT"}
}

func (f *oidcTokenFixture) validClaims() map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": f.server.URL, "sub": "subject-1", "aud": fixtureClientID,
		"exp": now.Add(5 * time.Minute).Unix(), "iat": now.Unix(), "nonce": fixtureNonce,
	}
}

func (f *oidcTokenFixture) sign(header, claims map[string]any) string {
	f.t.Helper()
	headerJSON, err := json.Marshal(header)
	if err != nil {
		f.t.Fatal(err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		f.t.Fatal(err)
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(headerJSON)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := encodedHeader + "." + encodedPayload
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func (f *oidcTokenFixture) tokenWithClaims(mutate func(map[string]any)) string {
	f.t.Helper()
	claims := f.validClaims()
	mutate(claims)
	return f.sign(f.signedHeader(), claims)
}

func (f *oidcTokenFixture) complete(t *testing.T) (string, string, error) {
	t.Helper()
	return f.manager.Complete(context.Background(), "single-use-code", fixtureState, f.cookie)
}

func jwksForKey(key *rsa.PrivateKey, keyID string) jwksDocument {
	return jwksDocument{Keys: []jwk{{
		KeyType: "RSA", KeyID: keyID, Use: "sig", Algorithm: "RS256",
		Modulus: base64.RawURLEncoding.EncodeToString(key.N.Bytes()), Exponent: base64.RawURLEncoding.EncodeToString(bigEndian(key.E)),
	}}}
}

func jwksResponder(status int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// Every malformed token, JWKS document and claim set must fail closed with the
// single public authentication error and must never establish a session.
func TestVerifyIDTokenDeniesMalformedTokensJWKSAndClaims(t *testing.T) {
	cases := []struct {
		name  string
		token func(*oidcTokenFixture) string
		jwks  func(*oidcTokenFixture) any
	}{
		{name: "two token parts", token: func(*oidcTokenFixture) string { return "header.payload" }},
		{name: "four token parts", token: func(*oidcTokenFixture) string { return "a.b.c.d" }},
		{name: "empty header part", token: func(f *oidcTokenFixture) string {
			parts := strings.Split(f.sign(f.signedHeader(), f.validClaims()), ".")
			return "." + parts[1] + "." + parts[2]
		}},
		{name: "empty signature part", token: func(f *oidcTokenFixture) string {
			parts := strings.Split(f.sign(f.signedHeader(), f.validClaims()), ".")
			return parts[0] + "." + parts[1] + "."
		}},
		{name: "header is not base64", token: func(f *oidcTokenFixture) string {
			parts := strings.Split(f.sign(f.signedHeader(), f.validClaims()), ".")
			return "!!!." + parts[1] + "." + parts[2]
		}},
		{name: "header is not json", token: func(f *oidcTokenFixture) string {
			parts := strings.Split(f.sign(f.signedHeader(), f.validClaims()), ".")
			return base64.RawURLEncoding.EncodeToString([]byte("not-json")) + "." + parts[1] + "." + parts[2]
		}},
		{name: "unsupported algorithm", token: func(f *oidcTokenFixture) string {
			return f.sign(map[string]any{"alg": "HS256", "kid": "test-key"}, f.validClaims())
		}},
		{name: "missing key id", token: func(f *oidcTokenFixture) string {
			return f.sign(map[string]any{"alg": "RS256"}, f.validClaims())
		}},
		{name: "critical header", token: func(f *oidcTokenFixture) string {
			return f.sign(map[string]any{"alg": "RS256", "kid": "test-key", "crit": []string{"exp"}}, f.validClaims())
		}},
		{name: "encoded payload disabled", token: func(f *oidcTokenFixture) string {
			return f.sign(map[string]any{"alg": "RS256", "kid": "test-key", "b64": false}, f.validClaims())
		}},
		{name: "payload is not base64", token: func(f *oidcTokenFixture) string {
			parts := strings.Split(f.sign(f.signedHeader(), f.validClaims()), ".")
			return parts[0] + ".!!!." + parts[2]
		}},
		{name: "payload is not json", token: func(f *oidcTokenFixture) string {
			parts := strings.Split(f.sign(f.signedHeader(), f.validClaims()), ".")
			return parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte("not-json")) + "." + parts[2]
		}},
		{name: "signature is not base64", token: func(f *oidcTokenFixture) string {
			parts := strings.Split(f.sign(f.signedHeader(), f.validClaims()), ".")
			return parts[0] + "." + parts[1] + ".!!!"
		}},
		{name: "signature is corrupted", token: func(f *oidcTokenFixture) string {
			return corruptTokenSignature(f.sign(f.signedHeader(), f.validClaims()))
		}},
		{name: "signature from another key", token: func(f *oidcTokenFixture) string {
			other, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				f.t.Fatal(err)
			}
			foreign := &oidcTokenFixture{t: f.t, key: other}
			return foreign.sign(f.signedHeader(), f.validClaims())
		}},
		{name: "jwks request failure", jwks: func(*oidcTokenFixture) any { return jwksResponder(http.StatusInternalServerError, "{}") }},
		{name: "jwks is not json", jwks: func(*oidcTokenFixture) any { return jwksResponder(http.StatusOK, "not json") }},
		{name: "jwks has no keys", jwks: func(*oidcTokenFixture) any { return jwksDocument{} }},
		{name: "jwks has too many keys", jwks: func(*oidcTokenFixture) any {
			keys := make([]jwk, 4097)
			for i := range keys {
				keys[i] = jwk{KeyType: "RSA", KeyID: fmt.Sprintf("key-%d", i)}
			}
			return jwksDocument{Keys: keys}
		}},
		{name: "duplicate matching key id", jwks: func(f *oidcTokenFixture) any {
			document := jwksForKey(f.key, "test-key")
			document.Keys = append(document.Keys, document.Keys[0])
			return document
		}},
		{name: "unknown key id", jwks: func(f *oidcTokenFixture) any { return jwksForKey(f.key, "other-key") }},
		{name: "wrong key type", jwks: func(f *oidcTokenFixture) any {
			document := jwksForKey(f.key, "test-key")
			document.Keys[0].KeyType = "EC"
			return document
		}},
		{name: "encryption use", jwks: func(f *oidcTokenFixture) any {
			document := jwksForKey(f.key, "test-key")
			document.Keys[0].Use = "enc"
			return document
		}},
		{name: "wrong signing algorithm", jwks: func(f *oidcTokenFixture) any {
			document := jwksForKey(f.key, "test-key")
			document.Keys[0].Algorithm = "RS512"
			return document
		}},
		{name: "sign-only key operations", jwks: func(f *oidcTokenFixture) any {
			document := jwksForKey(f.key, "test-key")
			document.Keys[0].KeyOps = []string{"sign"}
			return document
		}},
		{name: "modulus is not base64", jwks: func(f *oidcTokenFixture) any {
			document := jwksForKey(f.key, "test-key")
			document.Keys[0].Modulus = "!!!"
			return document
		}},
		{name: "modulus is too short", jwks: func(f *oidcTokenFixture) any {
			document := jwksForKey(f.key, "test-key")
			document.Keys[0].Modulus = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, 10))
			return document
		}},
		{name: "modulus is too long", jwks: func(f *oidcTokenFixture) any {
			document := jwksForKey(f.key, "test-key")
			document.Keys[0].Modulus = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, 1025))
			return document
		}},
		{name: "modulus is below the key size floor", jwks: func(f *oidcTokenFixture) any {
			document := jwksForKey(f.key, "test-key")
			modulus := make([]byte, 256)
			modulus[255] = 0x01
			document.Keys[0].Modulus = base64.RawURLEncoding.EncodeToString(modulus)
			return document
		}},
		{name: "exponent is not base64", jwks: func(f *oidcTokenFixture) any {
			document := jwksForKey(f.key, "test-key")
			document.Keys[0].Exponent = "!!!"
			return document
		}},
		{name: "exponent is empty", jwks: func(f *oidcTokenFixture) any {
			document := jwksForKey(f.key, "test-key")
			document.Keys[0].Exponent = ""
			return document
		}},
		{name: "exponent is too long", jwks: func(f *oidcTokenFixture) any {
			document := jwksForKey(f.key, "test-key")
			document.Keys[0].Exponent = base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1, 0, 1})
			return document
		}},
		{name: "exponent is one", jwks: func(f *oidcTokenFixture) any {
			document := jwksForKey(f.key, "test-key")
			document.Keys[0].Exponent = base64.RawURLEncoding.EncodeToString([]byte{0x01})
			return document
		}},
		{name: "exponent is even", jwks: func(f *oidcTokenFixture) any {
			document := jwksForKey(f.key, "test-key")
			document.Keys[0].Exponent = base64.RawURLEncoding.EncodeToString([]byte{0x04})
			return document
		}},
		{name: "issuer mismatch", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["iss"] = "https://wrong.example" })
		}},
		{name: "empty subject", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["sub"] = "" })
		}},
		{name: "oversized subject", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["sub"] = strings.Repeat("s", 1025) })
		}},
		{name: "empty nonce", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["nonce"] = "" })
		}},
		{name: "nonce mismatch", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["nonce"] = "other-nonce" })
		}},
		{name: "authorized party mismatch", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["azp"] = "other-client" })
		}},
		{name: "audience mismatch", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["aud"] = "other-client" })
		}},
		{name: "empty audience", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["aud"] = "" })
		}},
		{name: "empty audience array", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["aud"] = []string{} })
		}},
		{name: "audience array with empty entry", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["aud"] = []string{fixtureClientID, ""} })
		}},
		{name: "missing audience", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { delete(claims, "aud") })
		}},
		{name: "multiple audiences without authorized party", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["aud"] = []string{fixtureClientID, "other-client"} })
		}},
		{name: "multiple audiences with wrong authorized party", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) {
				claims["aud"] = []string{fixtureClientID, "other-client"}
				claims["azp"] = "other-client"
			})
		}},
		{name: "missing expiry", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { delete(claims, "exp") })
		}},
		{name: "non-numeric expiry", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["exp"] = "soon" })
		}},
		{name: "expired token", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["exp"] = time.Now().Add(-time.Minute).Unix() })
		}},
		{name: "missing issued-at", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { delete(claims, "iat") })
		}},
		{name: "non-numeric issued-at", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["iat"] = "yesterday" })
		}},
		{name: "issued-at in the future", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["iat"] = time.Now().Add(61 * time.Second).Unix() })
		}},
		{name: "issued-at at the epoch", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["iat"] = 0 })
		}},
		{name: "non-numeric not-before", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["nbf"] = "later" })
		}},
		{name: "not-before in the future", token: func(f *oidcTokenFixture) string {
			return f.tokenWithClaims(func(claims map[string]any) { claims["nbf"] = time.Now().Add(61 * time.Second).Unix() })
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOIDCTokenFixture(t)
			fixture.token = fixture.sign(fixture.signedHeader(), fixture.validClaims())
			if test.token != nil {
				fixture.token = test.token(fixture)
			}
			if test.jwks != nil {
				fixture.jwks = test.jwks(fixture)
			}
			_, _, err := fixture.complete(t)
			if !errors.Is(err, ErrAuthentication) {
				t.Fatalf("invalid token accepted: %v", err)
			}
			if fixture.sessions.created != 0 {
				t.Fatalf("invalid token established %d session(s)", fixture.sessions.created)
			}
		})
	}
}

func TestVerifyIDTokenAcceptsValidClaimVariants(t *testing.T) {
	fixture := newOIDCTokenFixture(t)
	fixture.token = fixture.sign(fixture.signedHeader(), fixture.validClaims())
	token, audience, err := fixture.complete(t)
	if err != nil || token != "test-session" || audience != AudienceClient || fixture.sessions.created != 1 {
		t.Fatalf("valid token rejected: token=%q audience=%q err=%v", token, audience, err)
	}

	fixture = newOIDCTokenFixture(t)
	claims := fixture.validClaims()
	claims["aud"] = []string{fixtureClientID, "other-client"}
	claims["azp"] = fixtureClientID
	claims["nbf"] = time.Now().Add(30 * time.Second).Unix()
	fixture.token = fixture.sign(fixture.signedHeader(), claims)
	if _, _, err := fixture.complete(t); err != nil {
		t.Fatalf("valid audience and not-before inside skew rejected: %v", err)
	}
}
