// File metadata (manifest mirror):
// Purpose: 实现 `Config`, `New`, `URL`, `HTTPClient` 等具体接口与边界逻辑，供 internal/testsupport/oidcfixture 的调用方使用。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package oidcfixture provides a loopback OIDC authorization-code issuer for tests.
// It validates client, redirect URI, state/nonce presence, PKCE, one-time code
// use and RS256/JWKS signatures. It never creates application sessions.
package oidcfixture

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type Config struct{ ClientRedirectURL, AdminRedirectURL string }

func New(t testing.TB, cfg Config) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{key: key, keyID: "local-test-key", expectedRedirectURL: cfg.ClientRedirectURL, expectedAdminRedirectURL: cfg.AdminRedirectURL, codes: make(map[string]authorizationCode)}
	p.server = httptest.NewServer(http.HandlerFunc(p.serveHTTP))
	p.client = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(p.server.Close)
	return p
}

func (p *Provider) URL() string              { return p.server.URL }
func (p *Provider) HTTPClient() *http.Client { return p.server.Client() }

type Provider struct {
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
	codes                    map[string]authorizationCode
}

type authorizationCode struct {
	clientID      string
	redirectURL   string
	nonce         string
	codeChallenge string
	subject       string
	tokenAudience string
	used          bool
}

func (p *Provider) SetNextClaims(subject, audience string) {
	p.mu.Lock()
	p.nextSubject = subject
	p.nextTokenAudience = audience
	p.mu.Unlock()
}

func (p *Provider) Authorize(t testing.TB, authorizationURL string) string {
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

func (p *Provider) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		writeJSON(w, map[string]string{
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

func (p *Provider) authorizeRequest(w http.ResponseWriter, r *http.Request) {
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
	p.codes[code] = authorizationCode{
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

func (p *Provider) tokenRequest(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, map[string]string{"id_token": idToken})
}

func (p *Provider) jwks(w http.ResponseWriter) {
	exponent := big.NewInt(int64(p.key.E)).Bytes()
	writeJSON(w, map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA", "kid": p.keyID, "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(p.key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(exponent),
		}},
	})
}

func (p *Provider) signIDToken(authorization authorizationCode) (string, error) {
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

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
