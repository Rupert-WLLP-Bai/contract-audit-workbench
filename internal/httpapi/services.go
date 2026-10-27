package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"contractaudit/internal/store"
)

// ServiceHandler exposes only the operations owned by one process. Each request
// still passes the ordinary session and resource authorization in Handler.
func ServiceHandler(st *store.Store, opt Options, role, credential string) (http.Handler, error) {
	if role != "core" && role != "document" && role != "execution" {
		return nil, errors.New("unknown HTTP service role")
	}
	if len(credential) < 32 {
		return nil, errors.New("a private service credential of at least 32 bytes is required")
	}
	if !opt.Durable {
		return nil, errors.New("independent HTTP services require durable execution")
	}
	api := Handler(st, opt)
	mux := http.NewServeMux()
	MountHealth(mux, st.Ready)
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !OperationOwnedBy(role, r.Method, r.URL.EscapedPath()) {
			http.NotFound(w, r)
			return
		}
		values := r.Header.Values("X-Contract-Service-Key")
		if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte(credential)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "service authentication required", "code": "service_unauthenticated"})
			return
		}
		api.ServeHTTP(w, r)
	}))
	return mux, nil
}

// RouteSegments decodes one escaped URL path into its segments. It reports
// false when the path is not an unambiguous single-decoding route: a relative
// path, an empty or dot segment, an encoded path separator, a control
// character, an invalid escape, or a trailing slash. The edge gateway and the
// per-service filter read the same segments, so an escaped spelling of a
// literal segment cannot change which service owns an operation or how the
// gateway classifies it.
func RouteSegments(path string) ([]string, bool) {
	if !strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return nil, false
	}
	parts := strings.Split(path[1:], "/")
	for i, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil || decoded == "" || decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "/\\") {
			return nil, false
		}
		for _, r := range decoded {
			if r < 32 || r == 127 {
				return nil, false
			}
		}
		parts[i] = decoded
	}
	return parts, true
}

// OperationOwnedBy is the exact public routing contract shared with the edge.
// path is an escaped URL path. Each segment is decoded once, just like ServeMux.
func OperationOwnedBy(role, method, path string) bool {
	parts, ok := RouteSegments(path)
	if !ok {
		return false
	}
	read := method == http.MethodGet || method == http.MethodHead
	post := method == http.MethodPost
	put := method == http.MethodPut
	delete := method == http.MethodDelete
	// CORS preflight is still checked against the exact route; Handler validates
	// origin and requested headers. Each path has only one owning service.
	if method == http.MethodOptions {
		read, post, put, delete = true, true, true, true
	}
	if len(parts) == 1 && parts[0] == "openapi.json" {
		return role == "core" && read
	}
	if len(parts) < 2 || parts[0] != "api" {
		return false
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "session":
			return role == "core" && (read || post || delete)
		case "settings":
			return role == "core" && read
		case "templates":
			return role == "document" && read
		case "cases":
			return role == "core" && (read || post)
		case "workbench", "system-health":
			return role == "core" && read
		case "knowledge":
			return role == "core" && post
		}
	}
	if len(parts) == 3 && parts[1] == "knowledge" {
		return role == "core" && read
	}
	if len(parts) == 5 && parts[1] == "knowledge" && parts[3] == "versions" {
		return role == "core" && read
	}
	if len(parts) == 3 && parts[1] == "admin" && parts[2] == "settings" {
		return role == "core" && read
	}
	if len(parts) == 3 && parts[1] == "admin" && parts[2] == "system-health" {
		return role == "core" && read
	}
	// Rule governance is Core-owned: only the core service writes the lifecycle
	// and the active pointer. The execution and worker roles read the frozen
	// published version an acceptance transaction selects.
	if len(parts) >= 3 && parts[1] == "admin" && parts[2] == "rulepacks" {
		switch {
		case len(parts) == 3:
			return role == "core" && (read || post)
		case len(parts) == 6 && parts[4] == "versions":
			return role == "core" && (read || post || put)
		case len(parts) == 7 && parts[4] == "versions":
			return role == "core" && post
		case len(parts) == 5 && parts[4] == "rollback":
			return role == "core" && post
		case len(parts) == 5 && parts[4] == "history":
			return role == "core" && read
		}
		return false
	}
	if len(parts) == 4 && parts[1] == "auth" && parts[2] == "oidc" && (parts[3] == "login" || parts[3] == "callback") {
		return role == "core" && read
	}
	if parts[1] != "cases" || len(parts) < 3 {
		return false
	}
	if len(parts) == 3 {
		return role == "core" && read
	}
	switch parts[3] {
	case "profile":
		return role == "core" && len(parts) == 4 && put
	case "tasks":
		return role == "core" && (len(parts) == 4 && post || len(parts) == 5 && put)
	case "relations":
		return role == "core" && (len(parts) == 4 && post || len(parts) == 5 && delete)
	case "documents":
		return role == "document" && (len(parts) == 4 && post || len(parts) == 6 && (parts[5] == "original" && read || parts[5] == "parse" && post))
	case "generations":
		return role == "document" && len(parts) == 4 && post
	case "reviews":
		if len(parts) == 8 && parts[5] == "findings" && parts[7] == "decisions" {
			return role == "core" && post
		}
		return role == "execution" && (len(parts) == 4 && post || len(parts) == 5 && read || len(parts) == 6 && (parts[5] == "events" && read || (parts[5] == "retry" || parts[5] == "cancel") && post))
	case "members", "archive":
		return role == "core" && len(parts) == 4 && post
	case "remediations":
		return role == "core" && post && (len(parts) == 4 || len(parts) == 6 && (parts[5] == "submit" || parts[5] == "verify"))
	case "reports":
		return role == "core" && (len(parts) == 4 && post || len(parts) == 5 && read)
	}
	return false
}
