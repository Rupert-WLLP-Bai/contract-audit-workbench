// File metadata (manifest mirror):
// Purpose: AudienceClient and AudienceAdmin identify the supported OIDC session audiences. Related `identity` operations include NewManager, validateOIDCConfig, validateOIDCEndpoints, validateOIDCCredentials.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package identity

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"contractaudit/internal/accounts"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// AudienceClient and AudienceAdmin identify the supported OIDC session audiences.
const (
	AudienceClient = "client"
	AudienceAdmin  = "admin"

	// StateCookieLifetime bounds an in-progress OIDC authorization flow.
	StateCookieLifetime = 5 * time.Minute
	maxProviderJSON     = 1 << 20
)

// ErrAuthentication is returned for every failed authentication flow.
var ErrAuthentication = errors.New("authentication failed")

// Config contains the operator supplied OIDC settings. StateEncryptionKey must
// be a stable 32-byte secret shared by every API process.
type Config struct {
	Issuer             string
	ClientID           string
	ClientSecret       string
	AdminClientID      string
	AdminClientSecret  string
	RedirectURL        string
	AdminRedirectURL   string
	StateEncryptionKey []byte
	HTTPClient         *http.Client
}

// DirectoryLookup resolves a verified issuer and subject through the trusted
// operator-managed account directory.
type DirectoryLookup func(issuer, subject string) (accounts.User, bool)

// SessionStore creates audience-bound sessions and reports their stored audience.
type SessionStore interface {
	EstablishSession(accounts.User, string) (string, error)
	SessionAudience(string) (string, error)
}

type membershipSessionStore interface {
	ResolveVerifiedOIDC(context.Context, accounts.User, string) (accounts.OIDCLoginResult, error)
}

// Manager performs OIDC authorization-code flows and establishes trusted sessions.
type Manager struct {
	config   Config
	lookup   DirectoryLookup
	sessions SessionStore
	client   *http.Client
}

type providerMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// NewManager validates startup configuration without contacting the issuer.
func NewManager(config Config, lookup DirectoryLookup, sessions SessionStore) (*Manager, error) {
	if lookup == nil || sessions == nil {
		return nil, errors.New("OIDC identity directory and session store are required")
	}
	if err := validateOIDCConfig(&config); err != nil {
		return nil, err
	}
	config.HTTPClient = oidcHTTPClient(config.HTTPClient)
	config.StateEncryptionKey = append([]byte(nil), config.StateEncryptionKey...)
	return &Manager{config: config, lookup: lookup, sessions: sessions, client: config.HTTPClient}, nil
}

func validateOIDCConfig(config *Config) error {
	if len(config.StateEncryptionKey) != 32 {
		return errors.New("OIDC state encryption key must be exactly 32 bytes")
	}
	if err := validateOIDCEndpoints(config); err != nil {
		return err
	}
	return validateOIDCCredentials(config)
}

func validateOIDCEndpoints(config *Config) error {
	if _, err := parseConfiguredURL(config.Issuer); err != nil {
		return fmt.Errorf("invalid OIDC issuer: %w", err)
	}
	if _, err := parseConfiguredURL(config.RedirectURL); err != nil {
		return fmt.Errorf("invalid OIDC redirect URL: %w", err)
	}
	if config.AdminRedirectURL == "" {
		config.AdminRedirectURL = config.RedirectURL
	}
	if _, err := parseConfiguredURL(config.AdminRedirectURL); err != nil {
		return fmt.Errorf("invalid OIDC admin redirect URL: %w", err)
	}
	return nil
}

func validateOIDCCredentials(config *Config) error {
	if config.ClientID == "" || config.ClientSecret == "" || config.AdminClientID == "" || config.AdminClientSecret == "" {
		return errors.New("both OIDC client and admin client credentials are required")
	}
	if config.ClientID == config.AdminClientID {
		return errors.New("OIDC client and admin client IDs must be distinct")
	}
	return nil
}

func oidcHTTPClient(client *http.Client) *http.Client {
	var transport http.RoundTripper = http.DefaultTransport
	if client != nil {
		cloned := *client
		if cloned.Transport != nil {
			transport = cloned.Transport
		}
		cloned.Transport = boundedProviderTransport{base: transport}
		cloned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		if cloned.Timeout <= 0 {
			cloned.Timeout = 10 * time.Second
		}
		return &cloned
	}
	return &http.Client{
		Timeout:       10 * time.Second,
		Transport:     boundedProviderTransport{base: transport},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type jwksValidationKey struct{}
type jwksOriginalContextKey struct{}
type exactStatusValidationKey struct{}

type jwksValidation struct {
	uri string
	kid string
}

type exactStatusValidation struct {
	uri    string
	status int
}

type boundedProviderTransport struct {
	base http.RoundTripper
}

func (t boundedProviderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if original, ok := request.Context().Value(jwksOriginalContextKey{}).(context.Context); ok {
		requestContext, cancel := context.WithCancel(request.Context())
		stop := context.AfterFunc(original, cancel)
		defer func() {
			stop()
			cancel()
		}()
		request = request.WithContext(requestContext)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	response, err := base.RoundTrip(request)
	if err != nil || response == nil || response.Body == nil {
		return response, err
	}
	body, readErr := readBounded(response.Body, maxProviderJSON)
	_ = response.Body.Close()
	if readErr != nil {
		return nil, ErrAuthentication
	}
	if validation, ok := request.Context().Value(jwksValidationKey{}).(jwksValidation); ok {
		if request.URL.String() != normalizedProviderURL(validation.uri) {
			return nil, ErrAuthentication
		}
		if response.StatusCode == http.StatusOK {
			if err := validateJWKSForID(body, validation.kid); err != nil {
				return nil, ErrAuthentication
			}
		}
	}
	if validation, ok := request.Context().Value(exactStatusValidationKey{}).(exactStatusValidation); ok {
		if request.URL.String() != normalizedProviderURL(validation.uri) || response.StatusCode != validation.status {
			return nil, ErrAuthentication
		}
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	return response, nil
}

// Begin creates the provider authorization URL and an authenticated encrypted
// state-cookie value. Provider discovery is deliberately deferred until login.
func (m *Manager) Begin(ctx context.Context, audience string) (string, string, error) {
	if m == nil {
		return "", "", ErrAuthentication
	}
	clientID, _, err := m.clientCredentials(audience)
	if err != nil {
		return "", "", err
	}
	metadata, err := m.discover(ctx)
	if err != nil {
		return "", "", ErrAuthentication
	}
	state, err := randomURLToken(32)
	if err != nil {
		return "", "", ErrAuthentication
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return "", "", ErrAuthentication
	}
	verifier, err := randomURLToken(32)
	if err != nil {
		return "", "", ErrAuthentication
	}
	cookie, err := m.sealState(statePayload{
		State: state, Nonce: nonce, Verifier: verifier, Audience: audience, IssuedAt: time.Now().Unix(),
	})
	if err != nil {
		return "", "", ErrAuthentication
	}
	config := oauthConfig(metadata, clientID, "", m.redirectURL(audience))
	return config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)), cookie, nil
}

// Complete validates the one-time state cookie, exchanges the authorization
// code, verifies the ID token, maps the trusted user, and creates an
// audience-bound server session.
func (m *Manager) Complete(ctx context.Context, code, returnedState, stateCookie string) (string, string, error) {
	if !m.completeRequestPresent(code, returnedState, stateCookie) {
		return "", "", ErrAuthentication
	}
	payload, ok := m.authenticatedState(stateCookie, returnedState)
	if !ok {
		return "", "", ErrAuthentication
	}
	clientID, clientSecret, err := m.clientCredentials(payload.Audience)
	if err != nil {
		return "", "", ErrAuthentication
	}
	metadata, err := m.discover(ctx)
	if err != nil {
		return "", "", ErrAuthentication
	}
	idToken, err := m.exchangeCode(ctx, metadata, clientID, clientSecret, code, payload.Verifier, m.redirectURL(payload.Audience))
	if err != nil {
		return "", "", ErrAuthentication
	}
	claims, err := m.verifyIDToken(ctx, metadata, idToken, clientID, payload.Nonce)
	if err != nil {
		return "", "", ErrAuthentication
	}
	user, err := m.trustedUser(claims, payload.Audience)
	if err != nil {
		return "", "", ErrAuthentication
	}
	token, err := m.establishSession(user, payload.Audience)
	if err != nil {
		return "", "", ErrAuthentication
	}
	return token, payload.Audience, nil
}

// CompleteWithMembership verifies OIDC as usual, then delegates every
// tenant/org/role/grant decision to the durable database membership resolver.
// It is the only completion method used by the production HTTP callback.
func (m *Manager) CompleteWithMembership(ctx context.Context, code, returnedState, stateCookie string) (accounts.OIDCLoginResult, string, error) {
	if !m.completeRequestPresent(code, returnedState, stateCookie) {
		return accounts.OIDCLoginResult{}, "", ErrAuthentication
	}
	payload, ok := m.authenticatedState(stateCookie, returnedState)
	if !ok {
		return accounts.OIDCLoginResult{}, "", ErrAuthentication
	}
	store, ok := m.sessions.(membershipSessionStore)
	if !ok {
		return accounts.OIDCLoginResult{}, "", ErrAuthentication
	}
	clientID, clientSecret, err := m.clientCredentials(payload.Audience)
	if err != nil {
		return accounts.OIDCLoginResult{}, "", ErrAuthentication
	}
	metadata, err := m.discover(ctx)
	if err != nil {
		return accounts.OIDCLoginResult{}, "", ErrAuthentication
	}
	idToken, err := m.exchangeCode(ctx, metadata, clientID, clientSecret, code, payload.Verifier, m.redirectURL(payload.Audience))
	if err != nil {
		return accounts.OIDCLoginResult{}, "", ErrAuthentication
	}
	claims, err := m.verifyIDToken(ctx, metadata, idToken, clientID, payload.Nonce)
	if err != nil {
		return accounts.OIDCLoginResult{}, "", ErrAuthentication
	}
	profile, found := m.lookup(claims.Issuer, claims.Subject)
	if !found || !profile.Enabled || profile.Issuer != claims.Issuer || profile.Subject != claims.Subject {
		return accounts.OIDCLoginResult{}, "", ErrAuthentication
	}
	result, err := store.ResolveVerifiedOIDC(ctx, profile, payload.Audience)
	if err != nil || (result.SessionToken == "") == (result.PendingToken == "") {
		return accounts.OIDCLoginResult{}, "", ErrAuthentication
	}
	return result, payload.Audience, nil
}

func (m *Manager) completeRequestPresent(code, returnedState, stateCookie string) bool {
	return m != nil && code != "" && returnedState != "" && stateCookie != ""
}

func (m *Manager) authenticatedState(stateCookie, returnedState string) (statePayload, bool) {
	payload, err := m.openState(stateCookie)
	if err != nil || !validReturnedState(payload, returnedState, time.Now()) {
		return statePayload{}, false
	}
	return payload, true
}

func validReturnedState(payload statePayload, returnedState string, now time.Time) bool {
	return subtleStringEqual(payload.State, returnedState) && validStateAge(payload.IssuedAt, now)
}

func (m *Manager) trustedUser(claims idTokenClaims, audience string) (accounts.User, error) {
	user, found := m.lookup(claims.Issuer, claims.Subject)
	if !found || !user.Enabled || user.Issuer != claims.Issuer || user.Subject != claims.Subject {
		return accounts.User{}, ErrAuthentication
	}
	if audience == AudienceAdmin && user.Role != "admin" {
		return accounts.User{}, ErrAuthentication
	}
	return user, nil
}

func (m *Manager) establishSession(user accounts.User, audience string) (string, error) {
	token, err := m.sessions.EstablishSession(user, audience)
	if err != nil || token == "" {
		return "", ErrAuthentication
	}
	storedAudience, err := m.sessions.SessionAudience(token)
	if err != nil || storedAudience != audience {
		return "", ErrAuthentication
	}
	return token, nil
}

func (m *Manager) clientCredentials(audience string) (string, string, error) {
	switch audience {
	case AudienceClient:
		return m.config.ClientID, m.config.ClientSecret, nil
	case AudienceAdmin:
		return m.config.AdminClientID, m.config.AdminClientSecret, nil
	default:
		return "", "", ErrAuthentication
	}
}

func (m *Manager) redirectURL(audience string) string {
	if audience == AudienceAdmin {
		return m.config.AdminRedirectURL
	}
	return m.config.RedirectURL
}

func (m *Manager) discover(ctx context.Context) (providerMetadata, error) {
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, m.client), m.config.Issuer)
	if err != nil {
		return providerMetadata{}, ErrAuthentication
	}
	var metadata providerMetadata
	if err := provider.Claims(&metadata); err != nil || metadata.Issuer != m.config.Issuer {
		return providerMetadata{}, ErrAuthentication
	}
	if !validProviderEndpoints(metadata, m.config.Issuer) {
		return providerMetadata{}, ErrAuthentication
	}
	return metadata, nil
}

func validProviderEndpoints(metadata providerMetadata, issuer string) bool {
	issuerURL, _ := url.Parse(issuer)
	loopbackIssuer := issuerURL != nil && isLoopbackHost(issuerURL.Hostname())
	for _, endpoint := range []string{metadata.AuthorizationEndpoint, metadata.TokenEndpoint, metadata.JWKSURI} {
		if !providerEndpointAllowed(endpoint, loopbackIssuer) {
			return false
		}
	}
	return metadata.AuthorizationEndpoint != "" && metadata.TokenEndpoint != "" && metadata.JWKSURI != ""
}

func providerEndpointAllowed(endpoint string, loopbackIssuer bool) bool {
	parsed, err := parseProviderURL(endpoint)
	if err != nil {
		return false
	}
	return parsed.Scheme != "http" || loopbackIssuer
}

func normalizedProviderURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.String()
}

func (m *Manager) exchangeCode(ctx context.Context, metadata providerMetadata, clientID, clientSecret, code, verifier, redirectURL string) (string, error) {
	config := oauthConfig(metadata, clientID, clientSecret, redirectURL)
	ctx = oidc.ClientContext(ctx, m.client)
	ctx = context.WithValue(ctx, exactStatusValidationKey{}, exactStatusValidation{uri: metadata.TokenEndpoint, status: http.StatusOK})
	token, err := config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil || token == nil {
		return "", ErrAuthentication
	}
	idToken, ok := token.Extra("id_token").(string)
	if !ok || idToken == "" {
		return "", ErrAuthentication
	}
	return idToken, nil
}

func oauthConfig(metadata providerMetadata, clientID, clientSecret, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       []string{oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail},
		Endpoint: oauth2.Endpoint{
			AuthURL:   metadata.AuthorizationEndpoint,
			TokenURL:  metadata.TokenEndpoint,
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, ErrAuthentication
	}
	return body, nil
}

func parseConfiguredURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return nil, errors.New("must be an absolute URL without credentials, query, or fragment")
	}
	if err := validateURLScheme(parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func parseProviderURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, ErrAuthentication
	}
	if err := validateURLScheme(parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func validateURLScheme(parsed *url.URL) error {
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(parsed.Hostname()) {
			return nil
		}
	}
	return errors.New("HTTPS is required except for loopback development endpoints")
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func randomURLToken(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func validStateAge(issuedAt int64, now time.Time) bool {
	issued := time.Unix(issuedAt, 0)
	return !issued.After(now.Add(time.Minute)) && now.Sub(issued) <= StateCookieLifetime
}

func subtleStringEqual(left, right string) bool {
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
