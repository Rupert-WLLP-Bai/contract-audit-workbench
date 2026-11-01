package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strconv"
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
	if err := validatePaymentReceiptSources(role, credential, &opt); err != nil {
		return nil, err
	}
	return buildServiceHandler(st, opt, role, credential), nil
}

func validatePaymentReceiptSources(role, gatewayCredential string, opt *Options) error {
	if len(opt.PaymentReceiptSources) == 0 {
		return nil
	}
	if role != "core" {
		return errors.New("payment receipt sources are accepted only by Core")
	}
	seenKeyID, seenSecret := map[string]bool{}, map[string]bool{}
	for _, source := range opt.PaymentReceiptSources {
		if !validPaymentSourceID(source.TenantID, 200) || !validPaymentSourceID(source.SourceID, 200) || !validPaymentSourceID(source.KeyID, 200) || len(source.Secret) < 32 || strings.TrimSpace(source.Secret) != source.Secret || subtle.ConstantTimeCompare([]byte(source.Secret), []byte(gatewayCredential)) == 1 || source.Secret == opt.ExperienceWorkerCredential {
			return errors.New("payment receipt source configuration is invalid")
		}
		if seenKeyID[source.KeyID] || seenSecret[source.Secret] {
			return errors.New("payment receipt source mapping is ambiguous")
		}
		seenKeyID[source.KeyID], seenSecret[source.Secret] = true, true
	}
	return nil
}

func validPaymentSourceID(value string, max int) bool {
	if value == "" || len(value) > max || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
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
	if len(opt.PaymentReceiptSources) > 0 {
		mux.Handle("/internal/v1/payment-receipts", paymentReceiptHandler(st, opt.PaymentReceiptSources))
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
	if parts[0] == "subjects" {
		return subjectRoute(role, methods, parts)
	}
	if parts[0] == "admin" {
		return adminRoute(role, methods, parts)
	}
	if parts[0] == "auth" {
		return role == "core" && methods.read && oidcRoute(parts)
	}
	if parts[0] == "approval-tasks" {
		return role == "core" && approvalTaskRoute(methods, parts)
	}
	if parts[0] == "contracts" {
		return contractDraftRoute(role, methods, parts) || contractReferenceRoute(role, methods, parts)
	}
	if parts[0] == "intakes" {
		if role != "core" {
			return false
		}
		if len(parts) == 2 {
			return methods.read
		}
		return len(parts) == 3 && (parts[2] == "continuation" && methods.read || parts[2] == "archive" && methods.post)
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
	"subjects":         {role: "core", methods: routeMethodSet{read: true, post: true}},
	"contracts":        {role: "core", methods: routeMethodSet{read: true, post: true}},
	"approval-tasks":   {role: "core", methods: routeMethodSet{read: true}},
	"intakes":          {role: "core", methods: routeMethodSet{read: true, post: true}},
}

func routeMethodsMatch(got, allowed routeMethodSet) bool {
	return got.read && allowed.read || got.post && allowed.post || got.put && allowed.put || got.delete && allowed.delete
}

func contractDraftRoute(role string, methods routeMethodSet, parts []string) bool {
	if role != "core" {
		return false
	}
	// Saved views and exports live under a literal ledger namespace so their
	// routes cannot collide with the contract-ID wildcard in ServeMux.
	if len(parts) >= 3 && parts[1] == "ledger" {
		switch {
		case len(parts) == 3 && parts[2] == "views":
			return methods.read || methods.post
		case len(parts) == 4 && parts[2] == "views":
			return methods.post || methods.delete
		case len(parts) == 3 && parts[2] == "exports":
			return methods.post
		case len(parts) == 5 && parts[2] == "exports" && parts[4] == "download":
			return methods.read
		default:
			return false
		}
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "drafts":
			return methods.read
		case "legacy-imports":
			return methods.post
		case "views":
			return methods.read || methods.post
		case "exports":
			return methods.post
		default:
			return methods.read
		}
	}
	if len(parts) == 3 && parts[1] == "views" {
		return methods.delete
	}
	// A POST saved-view replacement collides with POST /contracts/{id}/revisions
	// in Go's ServeMux (neither pattern is more specific). The explicit command
	// suffix keeps both operations addressable without a wildcard dispatcher.
	if len(parts) == 4 && parts[1] == "views" && parts[3] == "replace" {
		return methods.post
	}
	if len(parts) == 4 && parts[1] == "exports" && parts[3] == "download" {
		return methods.read
	}
	if (len(parts) == 3 || len(parts) == 4) && parts[2] == "approval-rounds" {
		return methods.read
	}
	if len(parts) == 5 && parts[2] == "revisions" && parts[4] == "audit-links" {
		return methods.read || methods.post
	}
	if len(parts) == 3 {
		switch parts[2] {
		case "draft":
			return methods.put
		case "revisions":
			return methods.read || methods.post
		case "revision-diff":
			return methods.read
		case "submissions":
			return methods.post
		case "signing-checks":
			return methods.post
		case "payments":
			return methods.read
		case "payment-eligibility-checks":
			return methods.post
		case "registrations", "supplements":
			return methods.read || methods.post
		default:
			return false
		}
	}
	return len(parts) == 4 && parts[2] == "revisions" && methods.read
}

// approvalTaskRoute owns the read views and the decision command. The command
// is registered by the approval module; both share core ownership because both
// re-authorize the contract inside the store transaction.
func approvalTaskRoute(methods routeMethodSet, parts []string) bool {
	switch len(parts) {
	case 1, 2:
		return methods.read
	case 3:
		return (parts[2] == "decisions" || parts[2] == "credentials" || parts[2] == "transfers") && methods.post
	default:
		return false
	}
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
	case "settings", "system-health", "tenant-operations":
		return len(parts) == 2 && m.read
	case "company-policy-sources":
		return len(parts) == 2 && m.read
	case "llm-endpoint":
		return llmEndpointRoute(m, parts[1:])
	case "governed-knowledge":
		return governedKnowledgeRoute(m, parts[1:])
	case "legal-sources":
		return legalSourceAdminRoute(m, parts[1:])
	case "rulepacks":
		return rulePackRoute(m, parts[1:])
	case "contract-workflow":
		if len(parts) == 3 && parts[2] == "templates" {
			return m.post
		}
		return len(parts) == 4 && parts[2] == "roles" && m.put
	}
	return false
}

func llmEndpointRoute(m routeMethodSet, parts []string) bool {
	if len(parts) == 1 {
		return m.read || m.put
	}
	if len(parts) != 2 {
		return false
	}
	switch parts[1] {
	case "probe":
		return m.post
	case "models":
		return m.put
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
	return command == "commands" && m.post || (command == "preview" || command == "experience-metrics") && m.read
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
	if len(p) == 6 && p[2] == "versions" && p[4] == "shadow" {
		return m.read && (p[5] == "samples" || p[5] == "reports")
	}
	if len(p) == 7 && p[2] == "versions" && p[4] == "shadow" && p[5] == "reports" {
		return m.read
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
	"experience-usage": func(role string, m routeMethodSet, p []string) bool {
		return role == "core" && len(p) == 3 && (m.read || m.post)
	},
	"evidence-requests": evidenceRequestHandlerRoute, "profile": profileRoute,
	"tasks": taskRoute, "relations": relationRoute, "documents": documentRoute,
	"materials":                 materialRoute,
	"subjects":                  caseSubjectRoute,
	"subject-changes":           caseSubjectRoute,
	"subject-sources":           coreReadRoute,
	"company-policy-sources":    coreReadRoute,
	"subject-source-snapshots":  subjectSourceSnapshotRoute,
	"legal-knowledge":           caseLegalKnowledgeRoute,
	"legal-applicability-facts": legalApplicabilityRoute,
	"legal-sources":             caseLegalSourceRoute,
	"service-objects":           serviceObjectRoute,
	"runs":                      caseRunAttemptCitationRoute,
	"review-readiness":          reviewReadinessRoute,
	"object-bindings":           operatorMappingRoute, "object-binding-changes": operatorMappingRoute,
	"external-collection-jobs": operatorCollectionRoute,
	"external-snapshots":       coreReadRoute, "fulfillment-evaluations": fulfillmentRoute,
	"generations": documentPostRoute, "reviews": reviewRoute,
	"members": corePostRoute, "archive": corePostRoute,
	"remediations": remediationRoute, "reports": reportRoute,
}

func reviewReadinessRoute(role string, m routeMethodSet, p []string) bool {
	return role == "execution" && len(p) == 3 && p[0] == "cases" && p[2] == "review-readiness" && m.read
}

func operatorCollectionRoute(role string, m routeMethodSet, p []string) bool {
	if role != "core" || len(p) < 3 || p[0] != "cases" || p[2] != "external-collection-jobs" {
		return false
	}
	if len(p) == 3 {
		return m.post
	}
	if len(p) == 4 && m.read && p[1] == "{caseID}" && p[3] == "{collectionID}" {
		return true
	}
	return len(p) == 4 && m.read && validCollectionPathID(p[3])
}

func validCollectionPathID(value string) bool {
	if !strings.HasPrefix(value, "col_") || (len(value) != len("col_")+16 && len(value) != len("col_")+32) {
		return false
	}
	for _, c := range value[len("col_"):] {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func operatorMappingRoute(role string, m routeMethodSet, p []string) bool {
	if role != "core" || len(p) < 3 || p[0] != "cases" {
		return false
	}
	if p[2] == "object-bindings" {
		if len(p) == 3 {
			return m.post
		}
		if len(p) == 5 && p[4] == "changes" {
			return m.post && p[3] != ""
		}
		if len(p) == 6 && p[4] == "changes" && p[5] == "preview" {
			return m.post && p[3] != ""
		}
		return false
	}
	if p[2] == "object-binding-changes" {
		if len(p) == 4 {
			return m.read && p[3] != ""
		}
		if len(p) == 5 && p[4] == "decision" {
			return m.post && p[3] != ""
		}
	}
	return false
}

func caseRunAttemptCitationRoute(role string, m routeMethodSet, p []string) bool {
	return caseLegalCitationRoute(role, m, p) || caseContractCitationRoute(role, m, p)
}

func caseLegalCitationRoute(role string, m routeMethodSet, p []string) bool {
	if role != "execution" || !m.read || len(p) != 8 || p[0] != "cases" || p[2] != "runs" || p[4] != "attempts" || p[6] != "legal-citations" {
		return false
	}
	// OpenAPI ownership checks pass the route template, while the gateway
	// matcher sees concrete IDs. Accept only the exact template or concrete
	// non-empty path values with the immutable citation digest shape.
	if p[1] == "{caseID}" && p[3] == "{runID}" && p[5] == "{attemptID}" && p[7] == "{citationID}" {
		return true
	}
	if p[1] == "" || p[3] == "" || p[5] == "" || len(p[7]) != 64 {
		return false
	}
	for _, c := range p[7] {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// caseContractCitationRoute owns the read-only reader of one verified business
// agent citation. The evidence identity is generated by the agent runtime, so
// only a non-empty single segment is required; it can never select a different
// run or attempt.
func caseContractCitationRoute(role string, m routeMethodSet, p []string) bool {
	if role != "execution" || !m.read || len(p) != 8 || p[0] != "cases" || p[2] != "runs" || p[4] != "attempts" || p[6] != "contract-citations" {
		return false
	}
	if p[1] == "{caseID}" && p[3] == "{runID}" && p[5] == "{attemptID}" {
		return p[7] == "{evidenceID}"
	}
	return p[1] != "" && p[3] != "" && p[5] != "" && p[7] != ""
}

func serviceObjectRoute(role string, m routeMethodSet, p []string) bool {
	if role != "core" || len(p) < 3 || p[0] != "cases" || p[2] != "service-objects" {
		return false
	}
	if len(p) == 3 {
		return m.read || m.post
	}
	if len(p) == 4 {
		return m.read
	}
	if len(p) == 5 {
		switch p[4] {
		case "versions", "association-candidates":
			return m.read
		case "associations":
			return m.read || m.post
		}
		return false
	}
	if len(p) == 6 && m.read && (p[4] == "versions" || p[4] == "associations") {
		if p[4] == "versions" && p[5] == "{version}" || p[4] == "associations" && p[5] == "{associationVersion}" {
			return true
		}
		version, err := strconv.Atoi(p[5])
		return err == nil && version > 0
	}
	return false
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

func materialRoute(role string, m routeMethodSet, p []string) bool {
	if role != "document" {
		return false
	}
	if len(p) == 4 && p[3] == "selection" && m.post {
		return true
	}
	if len(p) == 7 && p[4] == "versions" && p[6] == "parsed-documents" && m.read {
		return true
	}
	return len(p) == 8 && p[4] == "versions" && p[6] == "parsed-documents" && m.read
}

func caseSubjectRoute(role string, m routeMethodSet, p []string) bool {
	if role != "core" || len(p) < 3 {
		return false
	}
	if p[2] == "subjects" {
		return len(p) == 3 && (m.read || m.post) || len(p) == 4 && p[3] == "bind-existing" && m.post
	}
	if p[2] != "subject-changes" {
		return false
	}
	return len(p) == 3 && (m.read || m.post) || len(p) == 4 && m.read ||
		len(p) == 5 && (p[4] == "impact" && m.read || p[4] == "decisions" && m.post)
}

func subjectSourceSnapshotRoute(role string, m routeMethodSet, p []string) bool {
	if role != "core" {
		return false
	}
	return len(p) == 3 && (m.read || m.post) || len(p) == 4 && m.read || len(p) == 5 && p[4] == "verify" && m.post
}

func legalSourceAdminRoute(m routeMethodSet, p []string) bool {
	return len(p) == 2 && p[1] == "imports" && m.post || len(p) == 4 && p[2] == "snapshots" && m.read
}

func caseLegalSourceRoute(role string, m routeMethodSet, p []string) bool {
	return role == "core" && len(p) == 5 && p[3] == "snapshots" && m.read
}

func caseLegalKnowledgeRoute(role string, m routeMethodSet, p []string) bool {
	if role != "core" || !m.read {
		return false
	}
	return len(p) == 3 || len(p) == 6 && p[4] == "versions"
}

func legalApplicabilityRoute(role string, m routeMethodSet, p []string) bool {
	if role != "core" {
		return false
	}
	return len(p) == 3 && m.read || len(p) == 4 && m.put || len(p) == 5 && p[4] == "history" && m.read
}

func subjectRoute(role string, m routeMethodSet, p []string) bool {
	if role != "core" {
		return false
	}
	if len(p) == 1 {
		return m.read || m.post
	}
	return len(p) == 2 && m.read || len(p) == 3 && m.read && (p[2] == "history" || p[2] == "case-links")
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
	if len(p) == 5 && p[4] == "evidence-bundle" {
		return role == "document" && m.read
	}
	return role == "core" && (len(p) == 3 && m.post || len(p) == 4 && m.read)
}

func evidenceRequestRoute(m routeMethodSet, p []string) bool {
	return len(p) == 3 && (m.read || m.post) || len(p) == 4 && m.read ||
		len(p) == 5 && (p[4] == "history" && m.read || p[4] == "receipts" && m.post) ||
		len(p) == 7 && p[4] == "receipts" && p[6] == "verify" && m.post
}

func reviewRoute(role string, m routeMethodSet, p []string) bool {
	if len(p) == 7 && p[4] == "attempts" && p[5] != "" && p[6] == "manual-findings" {
		return role == "core" && (m.read || m.post)
	}
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
