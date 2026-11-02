package coreauthority

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// NewTransport builds a fixed-origin client for authority-only Core callbacks.
// It disables proxies and redirects so credentials and bearer tokens cannot be
// sent to an operator-controlled alternate destination.
func NewTransport(origin, path, caFile, appEnv string, allowInsecureDev bool, timeout time.Duration) (string, *http.Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") || strings.ContainsAny(strings.TrimSpace(origin), "<>\r\n") {
		return "", nil, errors.New("CORE_AUTHORITY_URL must be an absolute origin without credentials, path, query, or fragment")
	}
	mode := strings.ToLower(strings.TrimSpace(appEnv))
	switch mode {
	case "development", "dev", "test", "production":
	default:
		return "", nil, errors.New("APP_ENV is invalid for Core authority transport")
	}
	if parsed.Scheme == "https" {
		if allowInsecureDev && mode == "production" {
			return "", nil, errors.New("insecure Core authority mode is forbidden in production")
		}
	} else if parsed.Scheme != "http" || !allowInsecureDev ||
		(mode != "development" && mode != "dev" && mode != "test") || !allowedInsecureDevelopmentOrigin(parsed) {
		return "", nil, errors.New("Core authority requires verified HTTPS; development HTTP is limited to approved local origins")
	}
	if timeout <= 0 || strings.TrimSpace(path) == "" || !strings.HasPrefix(path, "/") {
		return "", nil, errors.New("Core authority path and positive timeout are required")
	}
	var roots *x509.CertPool
	if strings.TrimSpace(caFile) != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil || len(pem) == 0 {
			return "", nil, errors.New("CORE_AUTHORITY_CA_FILE is unreadable or empty")
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return "", nil, errors.New("CORE_AUTHORITY_CA_FILE contains no valid CA certificates")
		}
	}
	parsed.Path = path
	parsed.RawPath = ""
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("Core authority redirects are not followed") },
	}
	return parsed.String(), client, nil
}

func allowedInsecureDevelopmentOrigin(parsed *url.URL) bool {
	if parsed == nil || parsed.Scheme != "http" {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "core-api") {
		return parsed.Port() == "8081"
	}
	if parsed.Port() == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
