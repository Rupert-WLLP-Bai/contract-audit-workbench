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
	methods := methodsForRoute(method)
	if len(parts) == 1 && parts[0] == "openapi.json" {
		return role == "core" && methods.read
	}
	if len(parts) < 2 || parts[0] != "api" {
		return false
	}
	return routeOwned(role, methods, parts[1:])
}

type routeMethodSet struct{ read, post, put, delete bool }

func methodsForRoute(method string) routeMethodSet {
	methods := routeMethodSet{read: method == http.MethodGet || method == http.MethodHead,
		post: method == http.MethodPost, put: method == http.MethodPut, delete: method == http.MethodDelete}
	// Preflight remains checked against the exact route; Handler validates origin and headers.
	if method == http.MethodOptions {
		return routeMethodSet{read: true, post: true, put: true, delete: true}
	}
	return methods
}

func routeOwned(role string, methods routeMethodSet, parts []string) bool {
	if len(parts) == 1 {
		return topLevelRoute(role, methods, parts[0])
	}
	if parts[0] == "knowledge" {
		return role == "core" && methods.read && knowledgeRoute(parts)
	}
	if parts[0] == "admin" {
		return adminRoute(role, methods, parts)
	}
	if parts[0] == "auth" {
		return role == "core" && methods.read && oidcRoute(parts)
	}
	return caseRoute(role, methods, parts)
}

func topLevelRoute(role string, m routeMethodSet, path string) bool {
	rule, exists := topLevelRoutes[path]
	return exists && role == rule.role && routeMethodsMatch(m, rule.methods)
}

type topLevelRouteRule struct {
	role    string
	methods routeMethodSet
}

var topLevelRoutes = map[string]topLevelRouteRule{
	"session":          {role: "core", methods: routeMethodSet{read: true, post: true, delete: true}},
	"settings":         {role: "core", methods: routeMethodSet{read: true}},
	"workbench":        {role: "core", methods: routeMethodSet{read: true}},
	"system-health":    {role: "core", methods: routeMethodSet{read: true}},
	"capability-packs": {role: "core", methods: routeMethodSet{read: true}},
	"templates":        {role: "document", methods: routeMethodSet{read: true}},
	"cases":            {role: "core", methods: routeMethodSet{read: true, post: true}},
	"knowledge":        {role: "core", methods: routeMethodSet{post: true}},
}

func routeMethodsMatch(got, allowed routeMethodSet) bool {
	return got.read && allowed.read || got.post && allowed.post || got.put && allowed.put || got.delete && allowed.delete
}

func knowledgeRoute(parts []string) bool {
	return len(parts) == 2 || len(parts) == 4 && parts[2] == "versions"
}

func oidcRoute(parts []string) bool {
	return len(parts) == 3 && parts[1] == "oidc" && (parts[2] == "login" || parts[2] == "callback")
}

func adminRoute(role string, m routeMethodSet, parts []string) bool {
	if role != "core" || len(parts) < 2 {
		return false
	}
	switch parts[1] {
	case "settings", "system-health":
		return len(parts) == 2 && m.read
	case "governed-knowledge":
		return governedKnowledgeRoute(m, parts[1:])
	case "rulepacks":
		return rulePackRoute(m, parts[1:])
	}
	return false
}

func governedKnowledgeRoute(m routeMethodSet, p []string) bool {
	if len(p) == 1 {
		return m.read || m.post
	}
	if len(p) == 4 && p[2] == "versions" {
		return m.read
	}
	if len(p) == 5 && p[2] == "versions" {
		return governedVersionCommand(m, p[4])
	}
	return len(p) == 3 && p[2] == "history" && m.read
}

func governedVersionCommand(m routeMethodSet, command string) bool {
	return command == "commands" && m.post || command == "preview" && m.read
}

func rulePackRoute(m routeMethodSet, p []string) bool {
	if len(p) == 1 {
		return m.read || m.post
	}
	if len(p) == 4 && p[2] == "versions" {
		return m.read || m.post || m.put
	}
	if len(p) == 5 && p[2] == "versions" {
		return m.post
	}
	if len(p) == 3 && p[2] == "rollback" {
		return m.post
	}
	return len(p) == 3 && p[2] == "history" && m.read
}

func caseRoute(role string, m routeMethodSet, p []string) bool {
	if len(p) < 2 || p[0] != "cases" {
		return false
	}
	if len(p) == 2 {
		return role == "core" && m.read
	}
	if len(p) < 3 {
		return false
	}
	handler, exists := caseRouteHandlers[p[2]]
	return exists && handler(role, m, p)
}

type caseRouteHandler func(string, routeMethodSet, []string) bool

var caseRouteHandlers = map[string]caseRouteHandler{
	"experience-feedback": experienceFeedbackRoute, "experience-candidates": experienceCandidateRoute,
	"evidence-requests": evidenceRequestHandlerRoute, "profile": profileRoute,
	"tasks": taskRoute, "relations": relationRoute, "documents": documentRoute,
	"object-bindings": corePostRoute, "external-collection-jobs": corePostRoute,
	"external-snapshots": coreReadRoute, "fulfillment-evaluations": fulfillmentRoute,
	"generations": documentPostRoute, "reviews": reviewRoute,
	"members": corePostRoute, "archive": corePostRoute,
	"remediations": remediationRoute, "reports": reportRoute,
}

func corePostRoute(role string, m routeMethodSet, p []string) bool {
	return role == "core" && len(p) == 3 && m.post
}
func coreReadRoute(role string, m routeMethodSet, p []string) bool {
	return role == "core" && len(p) == 3 && m.read
}
func documentPostRoute(role string, m routeMethodSet, p []string) bool {
	return role == "document" && len(p) == 3 && m.post
}

func experienceFeedbackRoute(role string, m routeMethodSet, p []string) bool {
	return role == "core" && experienceFeedbackPath(m, p)
}

func experienceFeedbackPath(m routeMethodSet, p []string) bool {
	if len(p) == 3 {
		return m.post
	}
	if len(p) == 4 {
		return m.read
	}
	if len(p) == 5 && p[4] == "distillations" {
		return m.post
	}
	return len(p) == 6 && p[4] == "distillations" && m.read
}

func experienceCandidateRoute(role string, m routeMethodSet, p []string) bool {
	return role == "core" && len(p) == 6 && p[4] == "versions" && m.read
}

func evidenceRequestHandlerRoute(role string, m routeMethodSet, p []string) bool {
	return role == "core" && evidenceRequestRoute(m, p)
}
func profileRoute(role string, m routeMethodSet, p []string) bool {
	return role == "core" && len(p) == 3 && m.put
}
func taskRoute(role string, m routeMethodSet, p []string) bool {
	return role == "core" && (len(p) == 3 && m.post || len(p) == 4 && m.put)
}
func relationRoute(role string, m routeMethodSet, p []string) bool {
	return role == "core" && (len(p) == 3 && m.post || len(p) == 4 && m.delete)
}
func documentRoute(role string, m routeMethodSet, p []string) bool {
	return role == "document" && (len(p) == 3 && m.post || len(p) == 5 && (p[4] == "original" && m.read || p[4] == "parse" && m.post))
}
func fulfillmentRoute(role string, m routeMethodSet, p []string) bool {
	return role == "core" && (len(p) == 3 && m.post || len(p) == 4 && m.read || len(p) == 5 && p[4] == "impact" && m.read)
}
func remediationRoute(role string, m routeMethodSet, p []string) bool {
	return role == "core" && m.post && (len(p) == 3 || len(p) == 5 && (p[4] == "submit" || p[4] == "verify"))
}
func reportRoute(role string, m routeMethodSet, p []string) bool {
	return role == "core" && (len(p) == 3 && m.post || len(p) == 4 && m.read)
}

func evidenceRequestRoute(m routeMethodSet, p []string) bool {
	return len(p) == 3 && (m.read || m.post) || len(p) == 4 && m.read ||
		len(p) == 5 && (p[4] == "history" && m.read || p[4] == "receipts" && m.post) ||
		len(p) == 7 && p[4] == "receipts" && p[6] == "verify" && m.post
}

func reviewRoute(role string, m routeMethodSet, p []string) bool {
	if len(p) == 7 && p[4] == "findings" && p[6] == "decisions" {
		return role == "core" && m.post
	}
	if role != "execution" {
		return false
	}
	return len(p) == 3 && m.post || len(p) == 4 && m.read || len(p) == 5 && reviewAction(p[4], m)
}

func reviewAction(action string, m routeMethodSet) bool {
	return (action == "events" || action == "diagnostics") && m.read ||
		(action == "retry" || action == "cancel") && m.post
}
