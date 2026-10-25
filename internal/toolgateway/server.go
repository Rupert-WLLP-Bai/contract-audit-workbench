package toolgateway

import (
	"bytes"
	"context"
	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const serverInfoKey = "io.modelcontextprotocol/serverInfo"

const (
	codeParseError          = -32700
	codeInvalidRequest      = -32600
	codeMethodNotFound      = -32601
	codeInvalidParams       = -32602
	codeInternalError       = -32603
	codeHeaderMismatch      = -32020
	codeUnauthenticated     = -32021
	codeUnsupportedProtocol = -32022
	serverName              = "contract-audit-toolgateway"
	serverVersion           = "1.0.0"
)

type handler struct {
	store                Backend
	allowedOrigins       map[string]struct{}
	invalidOriginOptions bool
	workerCredential     string
}

type rpcRequest struct {
	id      json.RawMessage
	method  string
	params  map[string]json.RawMessage
	version string
}

type rpcFailure struct {
	status  int
	code    int
	message string
	data    any
}

type rpcContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolCallResult struct {
	ResultType string       `json:"resultType"`
	Content    []rpcContent `json:"content"`
	IsError    bool         `json:"isError"`
}

func newHandler(st Backend, opts Options) *handler {
	h := &handler{
		store: st, allowedOrigins: make(map[string]struct{}, len(opts.AllowedOrigins)),
		workerCredential: opts.WorkerCredential,
	}
	for _, origin := range opts.AllowedOrigins {
		canonical, ok := canonicalOrigin(origin)
		if !ok {
			h.invalidOriginOptions = true
			continue
		}
		h.allowedOrigins[canonical] = struct{}{}
	}
	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.originAllowed(r) {
		h.writeFailure(w, nil, &rpcFailure{status: http.StatusForbidden, code: codeInvalidRequest, message: "Origin is not allowed."})
		return
	}
	h.setCORS(w, r)
	if r.Method == http.MethodOptions {
		h.handlePreflight(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		h.writeFailure(w, nil, &rpcFailure{status: http.StatusMethodNotAllowed, code: codeInvalidRequest, message: "Only HTTP POST is supported."})
		return
	}
	if hasHeader(r.Header, "MCP-Session-Id") {
		h.writeFailure(w, nil, &rpcFailure{status: http.StatusBadRequest, code: codeInvalidRequest, message: "Protocol sessions are not supported."})
		return
	}
	if hasHeader(r.Header, "Content-Encoding") {
		h.writeFailure(w, nil, &rpcFailure{status: http.StatusUnsupportedMediaType, code: codeInvalidRequest, message: "Content-Encoding is not supported."})
		return
	}
	contentType, contentTypeOK := oneHeader(r.Header, "Content-Type")
	if !contentTypeOK || !isJSONContentType(contentType) {
		h.writeFailure(w, nil, &rpcFailure{status: http.StatusUnsupportedMediaType, code: codeInvalidRequest, message: "Content-Type must be application/json."})
		return
	}
	if !acceptsJSONAndSSE(r.Header.Values("Accept")) {
		h.writeFailure(w, nil, &rpcFailure{status: http.StatusNotAcceptable, code: codeInvalidRequest, message: "Accept must include application/json and text/event-stream."})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.writeFailure(w, nil, &rpcFailure{status: http.StatusRequestEntityTooLarge, code: codeInvalidRequest, message: "Request body exceeds the configured limit."})
			return
		}
		h.writeFailure(w, nil, &rpcFailure{status: http.StatusBadRequest, code: codeParseError, message: "Request body could not be read."})
		return
	}
	request, failure := parseRequest(body)
	if failure != nil {
		h.writeFailure(w, request.id, failure)
		return
	}
	request.version, failure = validateRequestMetadata(request.params)
	if failure != nil {
		h.writeFailure(w, request.id, failure)
		return
	}

	protocolHeader, ok := oneHeader(r.Header, "MCP-Protocol-Version")
	if !ok {
		h.writeFailure(w, request.id, headerMismatch("MCP-Protocol-Version"))
		return
	}
	methodHeader, ok := oneHeader(r.Header, "Mcp-Method")
	if !ok || methodHeader != request.method {
		h.writeFailure(w, request.id, headerMismatch("Mcp-Method"))
		return
	}
	if protocolHeader != request.version {
		h.writeFailure(w, request.id, headerMismatch("MCP-Protocol-Version"))
		return
	}
	if request.method == "tools/call" {
		name, err := stringField(request.params, "name")
		if err != nil {
			h.writeFailure(w, request.id, invalidParams("tools/call requires a tool name."))
			return
		}
		headerName, ok := oneHeader(r.Header, "Mcp-Name")
		if !ok {
			h.writeFailure(w, request.id, headerMismatch("Mcp-Name"))
			return
		}
		decoded, ok := decodeMirroredValue(headerName)
		if !ok || decoded != name {
			h.writeFailure(w, request.id, headerMismatch("Mcp-Name"))
			return
		}
	} else if hasHeader(r.Header, "Mcp-Name") {
		h.writeFailure(w, request.id, headerMismatch("Mcp-Name"))
		return
	}
	if request.version != ProtocolVersion {
		h.writeFailure(w, request.id, &rpcFailure{
			status: http.StatusBadRequest, code: codeUnsupportedProtocol, message: "Unsupported protocol version.",
			data: map[string]any{"supported": []string{ProtocolVersion}, "requested": request.version},
		})
		return
	}
	if request.method == "initialize" {
		h.writeFailure(w, request.id, &rpcFailure{status: http.StatusNotFound, code: codeMethodNotFound, message: "Method not found."})
		return
	}

	if err := r.Context().Err(); err != nil {
		h.writeFailure(w, request.id, storeFailure(r.Context(), err))
		return
	}
	// A durable worker presents its job claim instead of a browser session. The
	// claim header never arrives from outside: the edge strips every X-Contract-*
	// header, and the credential is a service-pair secret rather than a user
	// session. A worker may only call the read-only tool, never list or discover.
	workerContext, workerCall := "", false
	if values := r.Header.Values("X-Contract-Job-Context"); len(values) > 0 {
		if len(values) != 1 || values[0] == "" || request.method != "tools/call" {
			h.writeFailure(w, request.id, unauthorizedFailure())
			return
		}
		workerContext, workerCall = values[0], true
	}
	var user accounts.User
	if workerCall {
		if failure := h.authorizeWorkerCall(r); failure != nil {
			h.writeFailure(w, request.id, failure)
			return
		}
	} else {
		var failure *rpcFailure
		user, failure = h.authenticate(r)
		if failure != nil {
			h.writeFailure(w, request.id, failure)
			return
		}
	}
	if err := r.Context().Err(); err != nil {
		h.writeFailure(w, request.id, storeFailure(r.Context(), err))
		return
	}

	switch request.method {
	case "server/discover":
		if failure := validateParams(request.params, "_meta"); failure != nil {
			h.writeFailure(w, request.id, failure)
			return
		}
		h.writeResult(w, request.id, discoverResult())
	case "tools/list":
		if failure := validateParams(request.params, "_meta", "cursor"); failure != nil {
			h.writeFailure(w, request.id, failure)
			return
		}
		if raw, exists := request.params["cursor"]; exists {
			cursor, err := unmarshalString(raw)
			if err != nil || cursor != "" {
				h.writeFailure(w, request.id, invalidParams("This server exposes one unpaginated tool."))
				return
			}
		}
		h.writeResult(w, request.id, toolsListResult(hasReadGrant(user)))
	case "tools/call":
		if failure := validateParams(request.params, "_meta", "name", "arguments"); failure != nil {
			h.writeFailure(w, request.id, failure)
			return
		}
		name, _ := stringField(request.params, "name")
		if name != ToolName {
			h.writeFailure(w, request.id, invalidParams("Unknown tool."))
			return
		}
		args, failure := parseToolArguments(request.params["arguments"])
		if failure != nil {
			h.writeFailure(w, request.id, failure)
			return
		}
		if failure := requireBackend(h.store); failure != nil {
			h.writeFailure(w, request.id, failure)
			return
		}
		var result []byte
		if workerCall {
			result, failure = h.callFrozenEvidenceForWorker(r.Context(), workerContext, args)
		} else {
			result, failure = h.callFrozenEvidence(r.Context(), user, args)
		}
		if failure != nil {
			h.writeFailure(w, request.id, failure)
			return
		}
		h.writeResult(w, request.id, toolCallResult{
			ResultType: "complete",
			Content:    []rpcContent{{Type: "text", Text: string(result)}},
			IsError:    false,
		})
	default:
		h.writeFailure(w, request.id, &rpcFailure{status: http.StatusNotFound, code: codeMethodNotFound, message: "Method not found."})
	}
}

func (h *handler) originAllowed(r *http.Request) bool {
	values := r.Header.Values("Origin")
	if len(values) == 0 {
		return true
	}
	if h.invalidOriginOptions || len(values) != 1 {
		return false
	}
	origin, ok := canonicalOrigin(values[0])
	if !ok {
		return false
	}
	_, allowed := h.allowedOrigins[origin]
	return allowed
}

func (h *handler) setCORS(w http.ResponseWriter, r *http.Request) {
	values := r.Header.Values("Origin")
	if len(values) != 1 {
		return
	}
	origin, ok := canonicalOrigin(values[0])
	if !ok {
		return
	}
	if _, allowed := h.allowedOrigins[origin]; !allowed {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Expose-Headers", "MCP-Protocol-Version, Mcp-Method, Mcp-Name")
	w.Header().Add("Vary", "Origin")
}

func (h *handler) handlePreflight(w http.ResponseWriter, r *http.Request) {
	if len(r.Header.Values("Origin")) != 1 {
		h.writeFailure(w, nil, invalidParams("CORS preflight requires one Origin header."))
		return
	}
	method, ok := oneHeader(r.Header, "Access-Control-Request-Method")
	if !ok || method != http.MethodPost {
		h.writeFailure(w, nil, invalidParams("CORS preflight only permits POST."))
		return
	}
	requested := r.Header.Values("Access-Control-Request-Headers")
	if len(requested) > 1 {
		h.writeFailure(w, nil, invalidParams("CORS preflight headers must be provided once."))
		return
	}
	if len(requested) == 1 && requested[0] != "" {
		var seen uint8
		for remaining := requested[0]; ; {
			separator := strings.IndexByte(remaining, ',')
			header := remaining
			if separator >= 0 {
				header = remaining[:separator]
			}
			header = strings.TrimSpace(header)
			if header == "" {
				h.writeFailure(w, nil, invalidParams("CORS preflight contains an invalid header name."))
				return
			}
			for i := range header {
				if header[i] < 0x21 || header[i] > 0x7e {
					h.writeFailure(w, nil, invalidParams("CORS preflight contains an invalid header name."))
					return
				}
			}
			var bit uint8
			switch {
			case strings.EqualFold(header, "accept"):
				bit = 1 << 0
			case strings.EqualFold(header, "authorization"):
				bit = 1 << 1
			case strings.EqualFold(header, "content-type"):
				bit = 1 << 2
			case strings.EqualFold(header, "mcp-method"):
				bit = 1 << 3
			case strings.EqualFold(header, "mcp-name"):
				bit = 1 << 4
			case strings.EqualFold(header, "mcp-protocol-version"):
				bit = 1 << 5
			default:
				h.writeFailure(w, nil, invalidParams("CORS preflight requested an unsupported header."))
				return
			}
			if seen&bit != 0 {
				h.writeFailure(w, nil, invalidParams("CORS preflight contains a duplicate header name."))
				return
			}
			seen |= bit
			if separator < 0 {
				break
			}
			remaining = remaining[separator+1:]
		}
	}
	w.Header().Set("Access-Control-Allow-Methods", "POST")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, Mcp-Method, Mcp-Name, MCP-Protocol-Version")
	w.Header().Add("Vary", "Access-Control-Request-Method")
	w.Header().Add("Vary", "Access-Control-Request-Headers")
	w.WriteHeader(http.StatusNoContent)
}

func canonicalOrigin(raw string) (string, bool) {
	if raw == "" || raw == "null" || strings.TrimSpace(raw) != raw {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), true
}

func isJSONContentType(value string) bool {
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return false
	}
	if charset, exists := params["charset"]; exists && !strings.EqualFold(charset, "utf-8") {
		return false
	}
	for key := range params {
		if !strings.EqualFold(key, "charset") {
			return false
		}
	}
	return true
}

func acceptsJSONAndSSE(values []string) bool {
	acceptedJSON, acceptedSSE := false, false
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(part))
			if err != nil {
				continue
			}
			if quality, exists := params["q"]; exists {
				parsedQuality, err := strconv.ParseFloat(quality, 64)
				if err != nil || !(parsedQuality > 0 && parsedQuality <= 1) {
					continue
				}
			}
			switch strings.ToLower(mediaType) {
			case "application/json":
				acceptedJSON = true
			case "text/event-stream":
				acceptedSSE = true
			}
		}
	}
	return acceptedJSON && acceptedSSE
}

func parseRequest(body []byte) (rpcRequest, *rpcFailure) {
	if len(body) == 0 || !utf8.Valid(body) {
		return rpcRequest{}, &rpcFailure{status: http.StatusBadRequest, code: codeParseError, message: "Invalid JSON request."}
	}
	if err := validateSingleJSON(body); err != nil {
		return rpcRequest{}, &rpcFailure{status: http.StatusBadRequest, code: codeParseError, message: "Request must contain exactly one JSON object."}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return rpcRequest{}, &rpcFailure{status: http.StatusBadRequest, code: codeInvalidRequest, message: "JSON-RPC request must be an object."}
	}
	for key := range fields {
		if key != "jsonrpc" && key != "id" && key != "method" && key != "params" {
			return rpcRequest{}, &rpcFailure{status: http.StatusBadRequest, code: codeInvalidRequest, message: "Request contains an unsupported field."}
		}
	}
	jsonrpc, err := stringField(fields, "jsonrpc")
	if err != nil || jsonrpc != "2.0" {
		return rpcRequest{}, &rpcFailure{status: http.StatusBadRequest, code: codeInvalidRequest, message: "jsonrpc must be 2.0."}
	}
	id, ok := fields["id"]
	if !ok || !validRequestID(id) {
		return rpcRequest{}, &rpcFailure{status: http.StatusBadRequest, code: codeInvalidRequest, message: "A non-null string or integer request id is required."}
	}
	method, err := stringField(fields, "method")
	requestID := id
	if err != nil || method == "" || len(method) > 128 {
		return rpcRequest{id: requestID}, &rpcFailure{status: http.StatusBadRequest, code: codeInvalidRequest, message: "A valid method is required."}
	}
	paramsRaw, ok := fields["params"]
	if !ok {
		return rpcRequest{id: requestID}, invalidParams("Request metadata is required in params._meta.")
	}
	params, err := decodeJSONObject(paramsRaw)
	if err != nil {
		return rpcRequest{id: requestID}, invalidParams("params must be one JSON object.")
	}
	return rpcRequest{id: requestID, method: method, params: params}, nil
}
func validateRequestMetadata(params map[string]json.RawMessage) (string, *rpcFailure) {
	metaRaw, ok := params["_meta"]
	if !ok {
		return "", invalidParams("Request metadata is required in params._meta.")
	}
	meta, err := decodeJSONObject(metaRaw)
	if err != nil {
		return "", invalidParams("params._meta must be an object.")
	}
	version, err := stringField(meta, "io.modelcontextprotocol/protocolVersion")
	if err != nil || version == "" {
		return "", invalidParams("Request protocolVersion metadata is required.")
	}
	if raw, exists := meta["io.modelcontextprotocol/clientCapabilities"]; !exists || !isJSONObject(raw) {
		return "", invalidParams("Request clientCapabilities metadata is required as an object.")
	}
	clientRaw, exists := meta["io.modelcontextprotocol/clientInfo"]
	if !exists {
		return "", invalidParams("Request clientInfo metadata is required.")
	}
	clientInfo, err := decodeJSONObject(clientRaw)
	if err != nil {
		return "", invalidParams("Request clientInfo metadata must be an object.")
	}
	if name, err := stringField(clientInfo, "name"); err != nil || name == "" {
		return "", invalidParams("Request clientInfo.name is required.")
	}
	if clientVersion, err := stringField(clientInfo, "version"); err != nil || clientVersion == "" {
		return "", invalidParams("Request clientInfo.version is required.")
	}
	return version, nil
}

func (h *handler) authenticate(r *http.Request) (accounts.User, *rpcFailure) {
	if failure := requireBackend(h.store); failure != nil {
		return accounts.User{}, failure
	}
	token, ok := requestToken(r)
	if !ok {
		return accounts.User{}, unauthorizedFailure()
	}
	user, err := h.store.User(token)
	if err != nil {
		var public *fault.Error
		if errors.As(err, &public) {
			if public.Status == http.StatusUnauthorized {
				return accounts.User{}, unauthorizedFailure()
			}
			return accounts.User{}, storeFailure(nil, err)
		}
		return accounts.User{}, &rpcFailure{status: http.StatusServiceUnavailable, code: codeInternalError, message: "Authentication service is unavailable."}
	}
	if user.ID == "" || !user.Enabled {
		return accounts.User{}, unauthorizedFailure()
	}
	audience, err := h.store.SessionAudience(token)
	if err != nil {
		var public *fault.Error
		if errors.As(err, &public) {
			if public.Status == http.StatusUnauthorized {
				return accounts.User{}, unauthorizedFailure()
			}
			return accounts.User{}, storeFailure(nil, err)
		}
		return accounts.User{}, &rpcFailure{status: http.StatusServiceUnavailable, code: codeInternalError, message: "Authentication service is unavailable."}
	}
	if audience != "client" {
		return accounts.User{}, unauthorizedFailure()
	}
	current, ok := accounts.ByID(user.ID)
	if !ok || !current.Enabled || current.ID != user.ID {
		return accounts.User{}, unauthorizedFailure()
	}
	if user.TenantID != "" && user.TenantID != current.TenantID {
		return accounts.User{}, unauthorizedFailure()
	}
	if user.OrgID != "" && user.OrgID != current.OrgID {
		return accounts.User{}, unauthorizedFailure()
	}
	if accounts.Tenant(current) == "" {
		return accounts.User{}, unauthorizedFailure()
	}
	return current, nil
}

func requestToken(r *http.Request) (string, bool) {
	var cookieToken string
	cookieCount := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == "session" {
			cookieToken = cookie.Value
			cookieCount++
		}
	}
	if cookieCount > 1 || len(cookieToken) > 4096 {
		return "", false
	}
	var bearer string
	authorization := r.Header.Values("Authorization")
	if len(authorization) > 1 {
		return "", false
	}
	if len(authorization) == 1 {
		parts := strings.Fields(authorization[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" || len(parts[1]) > 4096 {
			return "", false
		}
		bearer = parts[1]
	}
	if cookieToken != "" && bearer != "" && cookieToken != bearer {
		return "", false
	}
	token := bearer
	if token == "" {
		token = cookieToken
	}
	if token == "" || !safeToken(token) {
		return "", false
	}
	return token, true
}

func safeToken(value string) bool {
	for i := range value {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func requireBackend(st Backend) *rpcFailure {
	if st == nil {
		return &rpcFailure{status: http.StatusServiceUnavailable, code: codeInternalError, message: "Gateway storage is unavailable."}
	}
	value := reflect.ValueOf(st)
	if (value.Kind() == reflect.Ptr || value.Kind() == reflect.Interface || value.Kind() == reflect.Map || value.Kind() == reflect.Slice || value.Kind() == reflect.Func) && value.IsNil() {
		return &rpcFailure{status: http.StatusServiceUnavailable, code: codeInternalError, message: "Gateway storage is unavailable."}
	}
	return nil
}

func (h *handler) writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	response := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
	h.writeJSON(w, http.StatusOK, response)
}

func (h *handler) writeFailure(w http.ResponseWriter, id json.RawMessage, failure *rpcFailure) {
	if failure == nil {
		failure = &rpcFailure{status: http.StatusInternalServerError, code: codeInternalError, message: "Internal gateway error."}
	}
	errorBody := map[string]any{"code": failure.code, "message": failure.message}
	if failure.data != nil {
		errorBody["data"] = failure.data
	}
	response := map[string]any{"jsonrpc": "2.0", "error": errorBody}
	if len(id) != 0 {
		response["id"] = id
	}
	h.writeJSON(w, failure.status, response)
}

func (h *handler) writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"jsonrpc":"2.0","error":{"code":-32603,"message":"Internal gateway error."}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func discoverResult() map[string]any {
	return map[string]any{
		"resultType":        "complete",
		"supportedVersions": []string{ProtocolVersion},
		"capabilities":      map[string]any{"tools": map[string]any{}},
		"_meta":             serverMeta(),
		"instructions":      "Reads cited statements from one authorized frozen contract-review attempt.",
	}
}

func toolsListResult(canRead bool) map[string]any {
	tools := make([]map[string]any, 0, 1)
	if canRead {
		tools = append(tools, toolDefinition())
	}
	return map[string]any{
		"resultType": "complete",
		"tools":      tools,
		"_meta":      serverMeta(),
	}
}

func hasReadGrant(user accounts.User) bool {
	tenantID := accounts.Tenant(user)
	if tenantID == "" {
		return false
	}
	principal := accounts.Principal(user)
	now := time.Now().UTC()
	for _, grant := range user.Grants {
		resource := access.Resource{TenantID: tenantID, CaseID: "mcp-tool-catalog", OrgID: user.OrgID, OwnerID: user.ID}
		switch grant.Scope {
		case "case":
			resource.CaseID = grant.CaseID
		case "org":
			resource.OrgID = grant.OrgID
		case "org_subtree":
			for _, orgID := range grant.ResolvedOrgIDs {
				resource.OrgID = orgID
				if access.Check(access.Read, principal, resource, now).Allowed {
					return true
				}
			}
			continue
		}
		if access.Check(access.Read, principal, resource, now).Allowed {
			return true
		}
	}
	return false
}

func serverMeta() map[string]any {
	return map[string]any{serverInfoKey: map[string]string{"name": serverName, "version": serverVersion}}
}

func toolDefinition() map[string]any {
	return map[string]any{
		"name":        ToolName,
		"title":       "Read frozen contract evidence",
		"description": "Reads supported, cited contract statements from the exact frozen review attempt. It does not read arbitrary tenant data or make an approval decision.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"case_id":       map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9._:-]+$", "description": "Authorized case identity."},
				"run_id":        map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9._:-]+$", "description": "Review run identity."},
				"attempt_id":    map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9._:-]+$", "description": "Exact frozen attempt identity."},
				"capability_id": map[string]any{"type": "string", "enum": supportedCapabilities(), "description": "One supported read capability."},
				"deadline":      map[string]any{"type": "string", "format": "date-time", "description": "RFC3339 deadline, in the future and no more than 60 seconds away."},
			},
			"required":             []string{"case_id", "run_id", "attempt_id", "capability_id", "deadline"},
			"additionalProperties": false,
		},
		"annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": false},
	}
}

func supportedCapabilities() [3]string {
	return [3]string{
		"dedicated_line.minimum_bandwidth",
		"dedicated_line.monthly_availability",
		"payment.advance_ratio.ceiling",
	}
}

func validateParams(params map[string]json.RawMessage, allowed ...string) *rpcFailure {
	for key := range params {
		matched := false
		for _, candidate := range allowed {
			if key == candidate {
				matched = true
				break
			}
		}
		if !matched {
			return invalidParams("Request contains an unsupported parameter.")
		}
	}
	return nil
}

func isJSONObject(raw json.RawMessage) bool {
	_, err := decodeJSONObject(raw)
	return err == nil
}

func decodeJSONObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("not an object")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
		return nil, errors.New("not an object")
	}
	return object, nil
}

func stringField(fields map[string]json.RawMessage, key string) (string, error) {
	raw, ok := fields[key]
	if !ok {
		return "", errors.New("missing string field")
	}
	return unmarshalString(raw)
}

func unmarshalString(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return "", errors.New("value is not a string")
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return "", err
	}
	return value, nil
}

func validRequestID(raw json.RawMessage) bool {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return false
	}
	if value[0] == '"' {
		var id string
		return json.Unmarshal(value, &id) == nil && len(id) <= 128
	}
	if value[0] == '-' {
		value = value[1:]
	}
	if len(value) == 0 || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func validateSingleJSON(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return err
	}
	if first != json.Delim('{') {
		return errors.New("top-level value must be an object")
	}
	if err := scanJSONContainer(decoder, first, 1); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func scanJSONContainer(decoder *json.Decoder, opening json.Token, depth int) error {
	if depth > 32 {
		return errors.New("JSON nesting limit exceeded")
	}
	delim, ok := opening.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate JSON object key")
			}
			seen[key] = struct{}{}
			value, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := scanJSONContainer(decoder, value, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("unterminated JSON object")
		}
	case '[':
		for decoder.More() {
			value, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := scanJSONContainer(decoder, value, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("unterminated JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func oneHeader(header http.Header, name string) (string, bool) {
	values := header.Values(name)
	if len(values) != 1 || values[0] == "" {
		return "", false
	}
	return values[0], true
}

func hasHeader(header http.Header, name string) bool {
	_, exists := header[http.CanonicalHeaderKey(name)]
	if exists {
		return true
	}
	for key := range header {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func decodeMirroredValue(value string) (string, bool) {
	const prefix = "=?base64?"
	const suffix = "?="
	if strings.HasPrefix(value, prefix) || strings.HasSuffix(value, suffix) {
		if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, suffix) || len(value) < len(prefix)+len(suffix) {
			return "", false
		}
		decoded, err := base64.StdEncoding.DecodeString(value[len(prefix) : len(value)-len(suffix)])
		if err != nil || !utf8.Valid(decoded) {
			return "", false
		}
		return string(decoded), true
	}
	if strings.TrimSpace(value) != value {
		return "", false
	}
	for i := range value {
		if value[i] < 0x20 || value[i] > 0x7e {
			return "", false
		}
	}
	return value, true
}

func headerMismatch(name string) *rpcFailure {
	return &rpcFailure{status: http.StatusBadRequest, code: codeHeaderMismatch, message: "HTTP header does not match the request body.", data: map[string]string{"header": name}}
}

func invalidParams(message string) *rpcFailure {
	return &rpcFailure{status: http.StatusBadRequest, code: codeInvalidParams, message: message}
}

func unauthorizedFailure() *rpcFailure {
	return &rpcFailure{status: http.StatusUnauthorized, code: codeUnauthenticated, message: "Authentication is required or no longer valid.", data: map[string]string{"code": "unauthenticated"}}
}

func storeFailure(ctx context.Context, err error) *rpcFailure {
	if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		return &rpcFailure{status: http.StatusRequestTimeout, code: codeInternalError, message: "Request was cancelled."}
	}
	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &rpcFailure{status: http.StatusGatewayTimeout, code: codeInternalError, message: "Request deadline exceeded."}
	}
	var public *fault.Error
	if errors.As(err, &public) {
		status := public.Status
		if status < 400 || status > 599 {
			status = http.StatusInternalServerError
		}
		data := map[string]string{}
		if public.Code != "" {
			data["code"] = public.Code
		}
		return &rpcFailure{status: status, code: codeInternalError, message: public.Message, data: data}
	}
	return &rpcFailure{status: http.StatusInternalServerError, code: codeInternalError, message: "Gateway storage operation failed."}
}

func (h *handler) finishCall(user accounts.User, callID, status string, outputBytes int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return h.store.FinishToolCall(ctx, user, callID, status, outputBytes)
}
