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
	if err := validateServiceHandlerConfig(role, credential, opt); err != nil {
		return nil, err
	}
	if err := configureExperienceDelivery(role, credential, &opt); err != nil {
		return nil, err
	}
	return buildServiceHandler(st, opt, role, credential), nil
}

func validateServiceHandlerConfig(role, credential string, opt Options) error {
	if role != "core" && role != "document" && role != "execution" {
		return errors.New("unknown HTTP service role")
	}
	if len(credential) < 32 {
		return errors.New("a private service credential of at least 32 bytes is required")
	}
	if !opt.Durable {
		return errors.New("independent HTTP services require durable execution")
	}
	return nil
}

func configureExperienceDelivery(role, credential string, opt *Options) error {
	workerCredential := opt.ExperienceWorkerCredential
	if role != "core" || workerCredential == "" {
		return nil
	}
	if len(workerCredential) < 32 || subtle.ConstantTimeCompare([]byte(workerCredential), []byte(credential)) == 1 {
		return errors.New("experience worker credential must be private and distinct from the gateway credential")
	}
	opt.experienceDeliveryEnabled = true
	return nil
}

func buildServiceHandler(st *store.Store, opt Options, role, credential string) http.Handler {
	api := Handler(st, opt)
	mux := http.NewServeMux()
	MountHealth(mux, st.Ready)
	if opt.experienceDeliveryEnabled {
		mux.HandleFunc("/internal/v1/experience-results", experienceResultHandler(st, opt.ExperienceWorkerCredential))
	}
	mux.Handle("/", serviceRequestHandler(api, role, credential))
	return mux
}

func serviceRequestHandler(api http.Handler, role, credential string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})
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
		decoded, ok := decodeRouteSegment(part)
		if !ok {
			return nil, false
		}
		parts[i] = decoded
	}
	return parts, true
}

func decodeRouteSegment(part string) (string, bool) {
	decoded, err := url.PathUnescape(part)
	if err != nil || decoded == "" || decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "/\\") {
		return "", false
	}
	for _, r := range decoded {
		if r < 32 || r == 127 {
			return "", false
		}
	}
	return decoded, true
}

// OperationOwnedBy is the exact public routing contract shared with the edge.
// path is an escaped URL path. Each segment is decoded once, just like ServeMux.
func OperationOwnedBy(role, method, path string) bool {
	parts, ok := RouteSegments(path)
	if !ok {
		return false
	}
	methodKind := methodForRoute(method)
	for _, rule := range serviceRoutes {
		if matchesRoute(rule, role, methodKind, parts) {
			return true
		}
	}
	return false
}

type routeMethod uint8

const (
	routeGet routeMethod = 1 << iota
	routeHead
	routePost
	routePut
	routeDelete
	routeAll  = routeGet | routeHead | routePost | routePut | routeDelete
	routeRead = routeGet | routeHead
)

type routeRule struct {
	segments []string
	role     string
	methods  routeMethod
}

func serviceRoute(pattern, role string, methods routeMethod) routeRule {
	return routeRule{segments: strings.Split(strings.TrimPrefix(pattern, "/"), "/"), role: role, methods: methods}
}

var serviceRoutes = []routeRule{
	serviceRoute("/openapi.json", "core", routeRead),
	serviceRoute("/api/session", "core", routeRead|routePost|routeDelete),
	serviceRoute("/api/settings", "core", routeRead),
	serviceRoute("/api/templates", "document", routeRead),
	serviceRoute("/api/cases", "core", routeRead|routePost),
	serviceRoute("/api/workbench", "core", routeRead),
	serviceRoute("/api/system-health", "core", routeRead),
	serviceRoute("/api/capability-packs", "core", routeRead),
	serviceRoute("/api/knowledge", "core", routePost),
	serviceRoute("/api/knowledge/*", "core", routeRead),
	serviceRoute("/api/knowledge/*/versions/*", "core", routeRead),
	serviceRoute("/api/admin/settings", "core", routeRead),
	serviceRoute("/api/admin/system-health", "core", routeRead),
	serviceRoute("/api/admin/governed-knowledge", "core", routeRead|routePost),
	serviceRoute("/api/admin/governed-knowledge/*/versions/*", "core", routeRead),
	serviceRoute("/api/admin/governed-knowledge/*/versions/*/commands", "core", routePost),
	serviceRoute("/api/admin/governed-knowledge/*/versions/*/preview", "core", routeRead),
	serviceRoute("/api/admin/governed-knowledge/*/history", "core", routeRead),
	serviceRoute("/api/admin/rulepacks", "core", routeRead|routePost),
	serviceRoute("/api/admin/rulepacks/*/versions/*", "core", routeRead|routePost|routePut),
	serviceRoute("/api/admin/rulepacks/*/versions/*/*", "core", routePost),
	serviceRoute("/api/admin/rulepacks/*/rollback", "core", routePost),
	serviceRoute("/api/admin/rulepacks/*/history", "core", routeRead),
	serviceRoute("/api/auth/oidc/login", "core", routeRead),
	serviceRoute("/api/auth/oidc/callback", "core", routeRead),
	serviceRoute("/api/cases/*", "core", routeRead),
	serviceRoute("/api/cases/*/experience-feedback", "core", routePost),
	serviceRoute("/api/cases/*/experience-feedback/*", "core", routeRead),
	serviceRoute("/api/cases/*/experience-feedback/*/distillations", "core", routePost),
	serviceRoute("/api/cases/*/experience-feedback/*/distillations/*", "core", routeRead),
	serviceRoute("/api/cases/*/experience-candidates/*/versions/*", "core", routeRead),
	serviceRoute("/api/cases/*/evidence-requests", "core", routeRead|routePost),
	serviceRoute("/api/cases/*/evidence-requests/*", "core", routeRead),
	serviceRoute("/api/cases/*/evidence-requests/*/history", "core", routeRead),
	serviceRoute("/api/cases/*/evidence-requests/*/receipts", "core", routePost),
	serviceRoute("/api/cases/*/evidence-requests/*/receipts/*/verify", "core", routePost),
	serviceRoute("/api/cases/*/profile", "core", routePut),
	serviceRoute("/api/cases/*/tasks", "core", routePost),
	serviceRoute("/api/cases/*/tasks/*", "core", routePut),
	serviceRoute("/api/cases/*/relations", "core", routePost),
	serviceRoute("/api/cases/*/relations/*", "core", routeDelete),
	serviceRoute("/api/cases/*/documents", "document", routePost),
	serviceRoute("/api/cases/*/documents/*/original", "document", routeRead),
	serviceRoute("/api/cases/*/documents/*/parse", "document", routePost),
	serviceRoute("/api/cases/*/object-bindings", "core", routePost),
	serviceRoute("/api/cases/*/external-collection-jobs", "core", routePost),
	serviceRoute("/api/cases/*/external-snapshots", "core", routeRead),
	serviceRoute("/api/cases/*/fulfillment-evaluations", "core", routePost),
	serviceRoute("/api/cases/*/fulfillment-evaluations/*", "core", routeRead),
	serviceRoute("/api/cases/*/fulfillment-evaluations/*/impact", "core", routeRead),
	serviceRoute("/api/cases/*/generations", "document", routePost),
	serviceRoute("/api/cases/*/reviews", "execution", routePost),
	serviceRoute("/api/cases/*/reviews/*", "execution", routeRead),
	serviceRoute("/api/cases/*/reviews/*/events", "execution", routeRead),
	serviceRoute("/api/cases/*/reviews/*/diagnostics", "execution", routeRead),
	serviceRoute("/api/cases/*/reviews/*/retry", "execution", routePost),
	serviceRoute("/api/cases/*/reviews/*/cancel", "execution", routePost),
	serviceRoute("/api/cases/*/reviews/*/findings/*/decisions", "core", routePost),
	serviceRoute("/api/cases/*/members", "core", routePost),
	serviceRoute("/api/cases/*/archive", "core", routePost),
	serviceRoute("/api/cases/*/remediations", "core", routePost),
	serviceRoute("/api/cases/*/remediations/*/submit", "core", routePost),
	serviceRoute("/api/cases/*/remediations/*/verify", "core", routePost),
	serviceRoute("/api/cases/*/reports", "core", routePost),
	serviceRoute("/api/cases/*/reports/*", "core", routeRead),
}

func methodForRoute(method string) routeMethod {
	switch method {
	case http.MethodGet:
		return routeGet
	case http.MethodHead:
		return routeHead
	case http.MethodPost:
		return routePost
	case http.MethodPut:
		return routePut
	case http.MethodDelete:
		return routeDelete
	case http.MethodOptions:
		// Preflight ownership follows the exact route for any supported method.
		return routeAll
	default:
		return 0
	}
}

func matchesRoute(rule routeRule, role string, method routeMethod, parts []string) bool {
	if role != rule.role || rule.methods&method == 0 {
		return false
	}
	if len(rule.segments) != len(parts) {
		return false
	}
	for i, segment := range rule.segments {
		if segment != "*" && segment != parts[i] {
			return false
		}
	}
	return true
}
