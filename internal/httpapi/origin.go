package httpapi

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// A proxy may terminate HTTPS before this handler, so its plain HTTP hop does
// not establish the browser's scheme. Only local development may infer an
// HTTP origin from Host; deployed external origins must be configured.
func directRequestOrigin(r *http.Request, secureCookies bool) string {
	if r.TLS != nil {
		origin, _ := browserOrigin("https://"+r.Host, false)
		return origin
	}
	if secureCookies {
		return ""
	}
	origin, ok := browserOrigin("http://"+r.Host, false)
	if !ok {
		return ""
	}
	u, _ := url.Parse(origin)
	host := u.Hostname()
	if host == "localhost" || net.ParseIP(host).IsLoopback() {
		return origin
	}
	return ""
}

// A missing Origin is common for native clients and for some same-origin
// browser requests. A session cookie needs positive same-origin evidence;
// non-cookie service and CLI clients can continue to send requests without it.
func originlessWriteAllowed(r *http.Request, trusted func(string) bool) bool {
	referers := r.Header.Values("Referer")
	if len(referers) > 0 {
		if len(referers) != 1 {
			return false
		}
		origin, ok := browserOrigin(referers[0], true)
		return ok && trusted(origin)
	}
	fetchSite := r.Header.Values("Sec-Fetch-Site")
	if len(fetchSite) > 0 && (len(fetchSite) != 1 || fetchSite[0] != "same-origin") {
		return false
	}
	_, sessionErr := r.Cookie(sessionCookie)
	_, adminErr := r.Cookie("admin_session")
	return sessionErr != nil && adminErr != nil
}

// browserOrigin reduces a serialized origin or Referer URL to its canonical
// scheme, host and effective port. Origin headers cannot contain URL paths.
func browserOrigin(raw string, referer bool) (string, bool) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	if !referer && (u.Path != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#")) {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.Contains(host, "%") || strings.HasSuffix(u.Host, ":") {
		return "", false
	}
	port := u.Port()
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", false
		}
		if scheme == "http" && value == 80 || scheme == "https" && value == 443 {
			port = ""
		}
	}
	authority := host
	if port != "" {
		authority = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	return scheme + "://" + authority, true
}
