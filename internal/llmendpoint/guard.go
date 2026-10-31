// guard.go is the outbound boundary for operator-configured model endpoints.
//
// The endpoint URL is platform control data typed by an administrator, but the
// addresses it names must not become a tunnel into the deployment network.
// Both the probe and every later provider call therefore resolve the host,
// classify every returned address, and dial only a verified address. Literal
// loopback, private, link-local (including the cloud metadata address),
// unspecified, multicast, broadcast, and carrier-grade NAT targets are denied
// unless the deployment administrator explicitly allowlisted the exact host in
// LLM_ENDPOINT_ALLOWED_HOSTS. Hostnames are re-classified on every connection,
// so DNS rebinding between validation and dial cannot move the target.
package llmendpoint

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// AllowedHostsEnv names the deployment allowlist of exact model endpoint
// hosts. Entries are hostnames or IP literals, optionally with a port, and
// are matched exactly: no wildcards, no CIDR ranges, no whole-private-range
// grants. It is the only way a private or loopback model endpoint stays
// reachable, and it is read on the server side only.
const AllowedHostsEnv = "LLM_ENDPOINT_ALLOWED_HOSTS"

// ErrEndpointForbidden means the configured endpoint host is denied by the
// deployment outbound policy. The message is intentionally fixed: policy
// rejections never name resolved addresses, DNS results, or upstream detail.
var ErrEndpointForbidden = errors.New("model endpoint host is not allowed by the deployment outbound policy")

// guardedDialTimeout bounds one connection attempt to a verified address.
const guardedDialTimeout = 5 * time.Second

type hostRule struct {
	host    string
	port    string
	hasPort bool
}

// allowedHosts parses the deployment allowlist. Unparseable entries never
// match anything, so a malformed list fails closed.
func allowedHosts() []hostRule {
	raw := strings.TrimSpace(os.Getenv(AllowedHostsEnv))
	if raw == "" {
		return nil
	}
	var rules []hostRule
	for _, entry := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		entry = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(entry), "."))
		if entry == "" {
			continue
		}
		host, port, err := net.SplitHostPort(entry)
		if err != nil {
			rules = append(rules, hostRule{host: entry})
			continue
		}
		rules = append(rules, hostRule{host: strings.Trim(host, "[]"), port: port, hasPort: true})
	}
	return rules
}

// hostAllowlisted reports whether the exact host (with its port when the
// allowlist entry names one) was explicitly trusted by the deployment.
func hostAllowlisted(host, port string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	for _, rule := range allowedHosts() {
		if rule.host != host {
			continue
		}
		if !rule.hasPort || rule.port == port {
			return true
		}
	}
	return false
}

// forbiddenIPReason classifies one address against the default outbound
// policy. An empty string means the address may be dialed.
func forbiddenIPReason(ip net.IP) string {
	switch {
	case ip == nil:
		return "invalid address"
	case ip.IsUnspecified():
		return "unspecified address"
	case ip.IsLoopback():
		return "loopback address"
	case ip.IsPrivate():
		return "private address"
	case ip.IsLinkLocalUnicast():
		return "link-local address"
	case ip.IsLinkLocalMulticast(), ip.IsInterfaceLocalMulticast(), ip.IsMulticast():
		return "multicast address"
	case ip.Equal(net.IPv4bcast):
		return "broadcast address"
	case cgnatAddress(ip):
		return "shared address space"
	default:
		return ""
	}
}

// cgnatAddress reports RFC 6598 shared address space, which also carries the
// Alibaba cloud metadata endpoint.
func cgnatAddress(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] < 128
}

// CheckBaseURLPolicy rejects endpoint URLs whose host is already a forbidden
// address literal unless the deployment allowlisted it. Hostnames pass here
// and are classified again on every connection, where the authoritative
// decision is made.
func CheckBaseURLPolicy(baseURL string) error {
	parsed, err := parseEndpointHost(baseURL)
	if err != nil {
		return err
	}
	if hostAllowlisted(parsed.hostname, parsed.port) {
		return nil
	}
	if literal := net.ParseIP(parsed.hostname); literal != nil {
		if reason := forbiddenIPReason(literal); reason != "" {
			return fmt.Errorf("%w: base URL names a %s", ErrEndpointForbidden, reason)
		}
	}
	return nil
}

type endpointHost struct {
	hostname string
	port     string
}

func parseEndpointHost(baseURL string) (endpointHost, error) {
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return endpointHost{}, err
	}
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Hostname() == "" {
		return endpointHost{}, fmt.Errorf("%w: base URL has no host", ErrInvalidEndpoint)
	}
	port := parsed.Port()
	if port == "" {
		port = "80"
		if parsed.Scheme == "https" {
			port = "443"
		}
	}
	return endpointHost{hostname: parsed.Hostname(), port: port}, nil
}

// GuardedTransport returns a transport for operator-configured endpoint
// traffic. It never uses environment proxies, because a proxy would move the
// dial away from the verified address, and it re-classifies the resolved
// addresses of every new connection.
func GuardedTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           guardedDialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// GuardedClient returns an HTTP client that neither follows redirects nor
// proxies operator-configured endpoint traffic. Redirect responses are
// returned unmodified, so a provider answer can never move the Authorization
// header, or the request body, to another origin.
func GuardedClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: GuardedTransport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// guardedDialContext resolves the endpoint host, rejects the connection when
// any resolved address is denied, and dials a verified address. Keeping the
// classification and the dial on the same addresses closes the gap a
// validate-then-resolve check would leave open to DNS rebinding.
func guardedDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, ErrEndpointForbidden
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || port == "" {
		return nil, ErrEndpointForbidden
	}
	if hostAllowlisted(host, port) {
		return (&net.Dialer{Timeout: guardedDialTimeout}).DialContext(ctx, network, addr)
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		// The resolver detail stays here: it can name internal DNS state, so
		// callers only ever see this fixed sentence.
		return nil, errors.New("model endpoint host cannot be resolved safely")
	}
	dialer := &net.Dialer{Timeout: guardedDialTimeout}
	for _, candidate := range addresses {
		if reason := forbiddenIPReason(candidate.IP); reason != "" {
			return nil, ErrEndpointForbidden
		}
	}
	for _, candidate := range addresses {
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
	}
	return nil, errors.New("model endpoint is unreachable")
}
