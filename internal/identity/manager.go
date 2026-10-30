package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"contractaudit/internal/accounts"
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
	if client != nil {
		cloned := *client
		cloned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		if cloned.Timeout <= 0 {
			cloned.Timeout = 10 * time.Second
		}
		return &cloned
	}
	return &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
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
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])
	cookie, err := m.sealState(statePayload{
		State: state, Nonce: nonce, Verifier: verifier, Audience: audience, IssuedAt: time.Now().Unix(),
	})
	if err != nil {
		return "", "", ErrAuthentication
	}
	authorizationURL, err := url.Parse(metadata.AuthorizationEndpoint)
	if err != nil {
		return "", "", ErrAuthentication
	}
	query := authorizationURL.Query()
	query.Set("client_id", clientID)
	query.Set("response_type", "code")
	query.Set("redirect_uri", m.redirectURL(audience))
	query.Set("scope", "openid profile email")
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	authorizationURL.RawQuery = query.Encode()
	return authorizationURL.String(), cookie, nil
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
	discoveryURL := strings.TrimRight(m.config.Issuer, "/") + "/.well-known/openid-configuration"
	body, err := m.getJSON(ctx, discoveryURL)
	if err != nil {
		return providerMetadata{}, err
	}
	var metadata providerMetadata
	if err := json.Unmarshal(body, &metadata); err != nil || metadata.Issuer != m.config.Issuer {
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

func (m *Manager) exchangeCode(ctx context.Context, metadata providerMetadata, clientID, clientSecret, code, verifier, redirectURL string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURL},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code_verifier": {verifier},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, metadata.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", ErrAuthentication
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := m.client.Do(request)
	if err != nil {
		return "", ErrAuthentication
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body, maxProviderJSON)
	if err != nil || response.StatusCode != http.StatusOK {
		return "", ErrAuthentication
	}
	var result struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.IDToken == "" {
		return "", ErrAuthentication
	}
	return result.IDToken, nil
}

func (m *Manager) getJSON(ctx context.Context, target string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, ErrAuthentication
	}
	request.Header.Set("Accept", "application/json")
	response, err := m.client.Do(request)
	if err != nil {
		return nil, ErrAuthentication
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrAuthentication
	}
	body, err := readBounded(response.Body, maxProviderJSON)
	if err != nil {
		return nil, ErrAuthentication
	}
	return body, nil
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
