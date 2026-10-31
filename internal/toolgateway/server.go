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
	if failure := validateHTTPRequest(r); failure != nil {
		if failure.status == http.StatusMethodNotAllowed {
			w.Header().Set("Allow", "POST, OPTIONS")
		}
		h.writeFailure(w, nil, failure)
		return
	}
	request, failure := h.readAndParseRequest(w, r)
	if failure != nil {
		h.writeFailure(w, request.id, failure)
		return
	}
	h.handleParsedRequest(w, r, request)
}

func validateHTTPRequest(r *http.Request) *rpcFailure {
	if r.Method != http.MethodPost {
		return &rpcFailure{status: http.StatusMethodNotAllowed, code: codeInvalidRequest, message: "Only HTTP POST is supported."}
	}
	if hasHeader(r.Header, "MCP-Session-Id") {
		return &rpcFailure{status: http.StatusBadRequest, code: codeInvalidRequest, message: "Protocol sessions are not supported."}
	}
	if hasHeader(r.Header, "Content-Encoding") {
		return &rpcFailure{status: http.StatusUnsupportedMediaType, code: codeInvalidRequest, message: "Content-Encoding is not supported."}
	}
	contentType, contentTypeOK := oneHeader(r.Header, "Content-Type")
	if !contentTypeOK || !isJSONContentType(contentType) {
		return &rpcFailure{status: http.StatusUnsupportedMediaType, code: codeInvalidRequest, message: "Content-Type must be application/json."}
	}
	if !acceptsJSONAndSSE(r.Header.Values("Accept")) {
		return &rpcFailure{status: http.StatusNotAcceptable, code: codeInvalidRequest, message: "Accept must include application/json and text/event-stream."}
	}
	return nil
}

func (h *handler) readAndParseRequest(w http.ResponseWriter, r *http.Request) (rpcRequest, *rpcFailure) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return rpcRequest{}, &rpcFailure{status: http.StatusRequestEntityTooLarge, code: codeInvalidRequest, message: "Request body exceeds the configured limit."}
		}
		return rpcRequest{}, &rpcFailure{status: http.StatusBadRequest, code: codeParseError, message: "Request body could not be read."}
	}
	request, failure := parseRequest(body)
	if failure != nil {
		return request, failure
	}
	request.version, failure = validateRequestMetadata(request.params)
	if failure != nil {
		return request, failure
	}
	return request, nil
}

func (h *handler) handleParsedRequest(w http.ResponseWriter, r *http.Request, request rpcRequest) {
	if failure := validateMirroredHeaders(r.Header, request); failure != nil {
		h.writeFailure(w, request.id, failure)
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
	h.dispatchRequest(w, r, request)
}

func validateMirroredHeaders(header http.Header, request rpcRequest) *rpcFailure {
	protocolHeader, ok := oneHeader(header, "MCP-Protocol-Version")
	if !ok {
		return headerMismatch("MCP-Protocol-Version")
	}
	methodHeader, ok := oneHeader(header, "Mcp-Method")
	if !ok || methodHeader != request.method {
		return headerMismatch("Mcp-Method")
	}
	if protocolHeader != request.version {
		return headerMismatch("MCP-Protocol-Version")
	}
	if failure := validateMirroredToolName(header, request); failure != nil {
		return failure
	}
	return nil
}

func validateMirroredToolName(header http.Header, request rpcRequest) *rpcFailure {
	if request.method != "tools/call" {
		if hasHeader(header, "Mcp-Name") {
			return headerMismatch("Mcp-Name")
		}
		return nil
	}
	name, err := stringField(request.params, "name")
	if err != nil {
		return invalidParams("tools/call requires a tool name.")
	}
	headerName, ok := oneHeader(header, "Mcp-Name")
	if !ok || !mirroredToolNameMatches(headerName, name) {
		return headerMismatch("Mcp-Name")
	}
	return nil
}

func (h *handler) dispatchRequest(w http.ResponseWriter, r *http.Request, request rpcRequest) {
	user, workerContext, workerCall, failure := h.authorizeRequest(r, request)
	if failure != nil {
		h.writeFailure(w, request.id, failure)
		return
	}
	h.dispatchAuthorizedRequest(w, r, request, user, workerContext, workerCall)
}

func (h *handler) authorizeRequest(r *http.Request, request rpcRequest) (accounts.User, string, bool, *rpcFailure) {
	if err := r.Context().Err(); err != nil {
		return accounts.User{}, "", false, storeFailure(r.Context(), err)
	}
	user, workerContext, workerCall, failure := h.authorizeRequestIdentity(r, request)
	if failure != nil {
		return accounts.User{}, "", false, failure
	}
	if err := r.Context().Err(); err != nil {
		return accounts.User{}, "", false, storeFailure(r.Context(), err)
	}
	return user, workerContext, workerCall, nil
}

func (h *handler) authorizeRequestIdentity(r *http.Request, request rpcRequest) (accounts.User, string, bool, *rpcFailure) {
	// A durable worker presents its job claim instead of a browser session. The
	// claim header never arrives from outside: the edge strips every X-Contract-*
	// header, and the credential is a service-pair secret rather than a user
	// session. A worker may only call the read-only tool, never list or discover.
	workerContext, workerCall := "", false
	if values := r.Header.Values("X-Contract-Job-Context"); len(values) > 0 {
		if len(values) != 1 || values[0] == "" || request.method != "tools/call" {
			return accounts.User{}, "", false, unauthorizedFailure()
		}
		workerContext, workerCall = values[0], true
	}
	var user accounts.User
	if workerCall {
		if failure := h.authorizeWorkerCall(r); failure != nil {
			return accounts.User{}, "", false, failure
		}
	} else {
		var failure *rpcFailure
		user, failure = h.authenticate(r)
		if failure != nil {
			return accounts.User{}, "", false, failure
		}
	}
	return user, workerContext, workerCall, nil
}

func (h *handler) dispatchAuthorizedRequest(w http.ResponseWriter, r *http.Request, request rpcRequest, user accounts.User, workerContext string, workerCall bool) {
	if h.dispatchReadMethod(w, request, user) {
		return
	}
	if request.method == "tools/call" {
		h.dispatchToolCall(w, r, request, user, workerContext, workerCall)
		return
	}
	h.writeFailure(w, request.id, &rpcFailure{status: http.StatusNotFound, code: codeMethodNotFound, message: "Method not found."})
}

func (h *handler) dispatchReadMethod(w http.ResponseWriter, request rpcRequest, user accounts.User) bool {
	switch request.method {
	case "server/discover":
		if failure := validateParams(request.params, "_meta"); failure != nil {
			h.writeFailure(w, request.id, failure)
			return true
		}
		h.writeResult(w, request.id, discoverResult())
		return true
	case "tools/list":
		if failure := validateParams(request.params, "_meta", "cursor"); failure != nil {
			h.writeFailure(w, request.id, failure)
			return true
		}
		if raw, exists := request.params["cursor"]; exists {
			cursor, err := unmarshalString(raw)
			if err != nil || cursor != "" {
				h.writeFailure(w, request.id, invalidParams("This server exposes one unpaginated tool."))
				return true
			}
		}
		h.writeResult(w, request.id, toolsListResult(hasReadGrant(user)))
		return true
	}
	return false
}

func (h *handler) dispatchToolCall(w http.ResponseWriter, r *http.Request, request rpcRequest, user accounts.User, workerContext string, workerCall bool) {
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
	result, failure := h.executeToolCall(r, user, workerContext, workerCall, args)
	if failure != nil {
		h.writeFailure(w, request.id, failure)
		return
	}
	h.writeResult(w, request.id, toolCallResult{
		ResultType: "complete",
		Content:    []rpcContent{{Type: "text", Text: string(result)}},
		IsError:    false,
	})
}

func (h *handler) executeToolCall(r *http.Request, user accounts.User, workerContext string, workerCall bool, args toolArguments) ([]byte, *rpcFailure) {
	if workerCall {
		return h.callFrozenEvidenceForWorker(r.Context(), workerContext, args)
	}
	return h.callFrozenEvidence(r.Context(), user, args)
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
	if failure := validatePreflightRequest(r); failure != nil {
		h.writeFailure(w, nil, failure)
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "POST")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, Mcp-Method, Mcp-Name, MCP-Protocol-Version")
	w.Header().Add("Vary", "Access-Control-Request-Method")
	w.Header().Add("Vary", "Access-Control-Request-Headers")
	w.WriteHeader(http.StatusNoContent)
}

func validatePreflightRequest(r *http.Request) *rpcFailure {
	if len(r.Header.Values("Origin")) != 1 {
		return invalidParams("CORS preflight requires one Origin header.")
	}
	method, ok := oneHeader(r.Header, "Access-Control-Request-Method")
	if !ok || method != http.MethodPost {
		return invalidParams("CORS preflight only permits POST.")
	}
	requested := r.Header.Values("Access-Control-Request-Headers")
	if len(requested) > 1 {
		return invalidParams("CORS preflight headers must be provided once.")
	}
	if len(requested) == 1 {
		if failure := validatePreflightHeaderList(requested[0]); failure != nil {
			return failure
		}
	}
	return nil
}

func validatePreflightHeaderList(requested string) *rpcFailure {
	if requested == "" {
		return nil
	}
	var seen uint8
	for remaining := requested; ; {
		separator := strings.IndexByte(remaining, ',')
		header := remaining
		if separator >= 0 {
			header = remaining[:separator]
		}
		header = strings.TrimSpace(header)
		bit, failure := validatePreflightHeader(header, seen)
		if failure != nil {
			return failure
		}
		seen |= bit
		if separator < 0 {
			return nil
		}
		remaining = remaining[separator+1:]
	}
}

func validatePreflightHeader(header string, seen uint8) (uint8, *rpcFailure) {
	if header == "" || !asciiHeaderValue(header) {
		return 0, invalidParams("CORS preflight contains an invalid header name.")
	}
	bit, ok := preflightHeaderBit(header)
	if !ok {
		return 0, invalidParams("CORS preflight requested an unsupported header.")
	}
	if seen&bit != 0 {
		return 0, invalidParams("CORS preflight contains a duplicate header name.")
	}
	return bit, nil
}

func asciiHeaderValue(header string) bool {
	return safeToken(header)
}

func preflightHeaderBit(header string) (uint8, bool) {
	switch {
	case strings.EqualFold(header, "accept"):
		return 1 << 0, true
	case strings.EqualFold(header, "authorization"):
		return 1 << 1, true
	case strings.EqualFold(header, "content-type"):
		return 1 << 2, true
	case strings.EqualFold(header, "mcp-method"):
		return 1 << 3, true
	case strings.EqualFold(header, "mcp-name"):
		return 1 << 4, true
	case strings.EqualFold(header, "mcp-protocol-version"):
		return 1 << 5, true
	default:
		return 0, false
	}
}

func canonicalOrigin(raw string) (string, bool) {
	if raw == "" || raw == "null" || strings.TrimSpace(raw) != raw {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || !validOriginURL(u) {
		return "", false
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), true
}

func validOriginURL(origin *url.URL) bool {
	return (origin.Scheme == "http" || origin.Scheme == "https") && origin.Host != "" &&
		origin.User == nil && origin.Path == "" && origin.RawQuery == "" && origin.Fragment == ""
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
			mediaType, ok := acceptableMediaType(part)
			if !ok {
				continue
			}
			switch mediaType {
			case "application/json":
				acceptedJSON = true
			case "text/event-stream":
				acceptedSSE = true
			}
		}
	}
	return acceptedJSON && acceptedSSE
}

func acceptableMediaType(part string) (string, bool) {
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(part))
	if err != nil {
		return "", false
	}
	if quality, exists := params["q"]; exists {
		parsedQuality, err := strconv.ParseFloat(quality, 64)
		if err != nil || !(parsedQuality > 0 && parsedQuality <= 1) {
			return "", false
		}
	}
	return strings.ToLower(mediaType), true
}

func parseRequest(body []byte) (rpcRequest, *rpcFailure) {
	fields, failure := decodeRequestFields(body)
	if failure != nil {
		return rpcRequest{}, failure
	}
	if failure := rejectUnsupportedEnvelopeFields(fields); failure != nil {
		return rpcRequest{}, failure
	}
	requestID, method, failure := parseRequestIdentity(fields)
	if failure != nil {
		return rpcRequest{id: requestID}, failure
	}
	params, failure := parseRequestParams(fields)
	if failure != nil {
		return rpcRequest{id: requestID}, failure
	}
	return rpcRequest{id: requestID, method: method, params: params}, nil
}

func decodeRequestFields(body []byte) (map[string]json.RawMessage, *rpcFailure) {
	if len(body) == 0 || !utf8.Valid(body) {
		return nil, &rpcFailure{status: http.StatusBadRequest, code: codeParseError, message: "Invalid JSON request."}
	}
	if err := validateSingleJSON(body); err != nil {
		return nil, &rpcFailure{status: http.StatusBadRequest, code: codeParseError, message: "Request must contain exactly one JSON object."}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, &rpcFailure{status: http.StatusBadRequest, code: codeInvalidRequest, message: "JSON-RPC request must be an object."}
	}
	return fields, nil
}

func parseRequestParams(fields map[string]json.RawMessage) (map[string]json.RawMessage, *rpcFailure) {
	return requiredJSONObjectField(fields, "params", "Request metadata is required in params._meta.", "params must be one JSON object.")
}

func parseRequestIdentity(fields map[string]json.RawMessage) (json.RawMessage, string, *rpcFailure) {
	if failure := validateJSONRPCVersion(fields); failure != nil {
		return nil, "", failure
	}
	id, failure := parseJSONRPCRequestID(fields)
	if failure != nil {
		return nil, "", failure
	}
	method, failure := parseJSONRPCMethod(fields)
	if failure != nil {
		return id, "", failure
	}
	return id, method, nil
}

func validateJSONRPCVersion(fields map[string]json.RawMessage) *rpcFailure {
	jsonrpc, err := stringField(fields, "jsonrpc")
	if err != nil || jsonrpc != "2.0" {
		return &rpcFailure{status: http.StatusBadRequest, code: codeInvalidRequest, message: "jsonrpc must be 2.0."}
	}
	return nil
}

func parseJSONRPCRequestID(fields map[string]json.RawMessage) (json.RawMessage, *rpcFailure) {
	id, ok := fields["id"]
	if !ok || !validRequestID(id) {
		return nil, &rpcFailure{status: http.StatusBadRequest, code: codeInvalidRequest, message: "A non-null string or integer request id is required."}
	}
	return id, nil
}

func parseJSONRPCMethod(fields map[string]json.RawMessage) (string, *rpcFailure) {
	method, err := stringField(fields, "method")
	if err != nil || method == "" || len(method) > 128 {
		return "", &rpcFailure{status: http.StatusBadRequest, code: codeInvalidRequest, message: "A valid method is required."}
	}
	return method, nil
}

func rejectUnsupportedEnvelopeFields(fields map[string]json.RawMessage) *rpcFailure {
	for key := range fields {
		if key != "jsonrpc" && key != "id" && key != "method" && key != "params" {
			return &rpcFailure{status: http.StatusBadRequest, code: codeInvalidRequest, message: "Request contains an unsupported field."}
		}
	}
	return nil
}

func validateRequestMetadata(params map[string]json.RawMessage) (string, *rpcFailure) {
	meta, failure := requestMetadataObject(params)
	if failure != nil {
		return "", failure
	}
	version, failure := requestProtocolVersion(meta)
	if failure != nil {
		return "", failure
	}
	if failure := validateClientCapabilities(meta); failure != nil {
		return "", failure
	}
	if failure := validateClientInfo(meta); failure != nil {
		return "", failure
	}
	return version, nil
}

func requestMetadataObject(params map[string]json.RawMessage) (map[string]json.RawMessage, *rpcFailure) {
	return requiredJSONObjectField(params, "_meta", "Request metadata is required in params._meta.", "params._meta must be an object.")
}

func requestProtocolVersion(meta map[string]json.RawMessage) (string, *rpcFailure) {
	version, err := stringField(meta, "io.modelcontextprotocol/protocolVersion")
	if err != nil || version == "" {
		return "", invalidParams("Request protocolVersion metadata is required.")
	}
	return version, nil
}

func validateClientCapabilities(meta map[string]json.RawMessage) *rpcFailure {
	if raw, exists := meta["io.modelcontextprotocol/clientCapabilities"]; !exists || !isJSONObject(raw) {
		return invalidParams("Request clientCapabilities metadata is required as an object.")
	}
	return nil
}

func validateClientInfo(meta map[string]json.RawMessage) *rpcFailure {
	clientInfo, failure := clientInfoObject(meta)
	if failure != nil {
		return failure
	}
	if name, err := stringField(clientInfo, "name"); err != nil || name == "" {
		return invalidParams("Request clientInfo.name is required.")
	}
	if clientVersion, err := stringField(clientInfo, "version"); err != nil || clientVersion == "" {
		return invalidParams("Request clientInfo.version is required.")
	}
	return nil
}

func clientInfoObject(meta map[string]json.RawMessage) (map[string]json.RawMessage, *rpcFailure) {
	return requiredJSONObjectField(meta, "io.modelcontextprotocol/clientInfo", "Request clientInfo metadata is required.", "Request clientInfo metadata must be an object.")
}

func (h *handler) authenticate(r *http.Request) (accounts.User, *rpcFailure) {
	if failure := requireBackend(h.store); failure != nil {
		return accounts.User{}, failure
	}
	token, ok := requestToken(r)
	if !ok {
		return accounts.User{}, unauthorizedFailure()
	}
	user, audience, err := h.store.AuthenticateSession(r.Context(), token)
	if err != nil {
		return accounts.User{}, authenticationFailure(err)
	}
	if user.ID == "" || !user.Enabled {
		return accounts.User{}, unauthorizedFailure()
	}
	if audience != "client" {
		return accounts.User{}, unauthorizedFailure()
	}
	return bindAuthenticatedUser(user)
}

func authenticationFailure(err error) *rpcFailure {
	var public *fault.Error
	if errors.As(err, &public) {
		if public.Status == http.StatusUnauthorized {
			return unauthorizedFailure()
		}
		return storeFailure(nil, err)
	}
	return &rpcFailure{status: http.StatusServiceUnavailable, code: codeInternalError, message: "Authentication service is unavailable."}
}

func bindAuthenticatedUser(user accounts.User) (accounts.User, *rpcFailure) {
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
	cookieToken, ok := requestCookieToken(r)
	if !ok {
		return "", false
	}
	bearer, ok := requestBearerToken(r)
	if !ok {
		return "", false
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

func requestCookieToken(r *http.Request) (string, bool) {
	var token string
	foundSession := false
	for _, cookie := range r.Cookies() {
		if cookie.Name != "session" {
			continue
		}
		if foundSession {
			return "", false
		}
		foundSession = true
		token = cookie.Value
	}
	if len(token) > 4096 {
		return "", false
	}
	return token, true
}

func requestBearerToken(r *http.Request) (string, bool) {
	authorization := r.Header.Values("Authorization")
	if len(authorization) > 1 {
		return "", false
	}
	if len(authorization) == 0 {
		return "", true
	}
	parts := strings.Fields(authorization[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" || len(parts[1]) > 4096 {
		return "", false
	}
	return parts[1], true
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

func decodeJSONObjectField(fields map[string]json.RawMessage, name string) (map[string]json.RawMessage, bool, error) {
	raw, exists := fields[name]
	if !exists {
		return nil, false, nil
	}
	object, err := decodeJSONObject(raw)
	return object, true, err
}

func requiredJSONObjectField(fields map[string]json.RawMessage, name, missingMessage, invalidMessage string) (map[string]json.RawMessage, *rpcFailure) {
	object, exists, err := decodeJSONObjectField(fields, name)
	if !exists {
		return nil, invalidParams(missingMessage)
	}
	if err != nil {
		return nil, invalidParams(invalidMessage)
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
		return validStringRequestID(value)
	}
	if value[0] == '-' {
		value = value[1:]
	}
	return validIntegerRequestID(value)
}

func validStringRequestID(value []byte) bool {
	var id string
	return json.Unmarshal(value, &id) == nil && len(id) <= 128
}

func validIntegerRequestID(value []byte) bool {
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
		return scanJSONObject(decoder, depth)
	case '[':
		return scanJSONArray(decoder, depth)
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

func scanJSONObject(decoder *json.Decoder, depth int) error {
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
	return nil
}
func scanJSONArray(decoder *json.Decoder, depth int) error {
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

func mirroredToolNameMatches(headerName, requestName string) bool {
	decoded, ok := decodeMirroredValue(headerName)
	return ok && decoded == requestName
}

func decodeMirroredValue(value string) (string, bool) {
	const prefix = "=?base64?"
	const suffix = "?="
	if strings.HasPrefix(value, prefix) || strings.HasSuffix(value, suffix) {
		return decodeEncodedMirroredValue(value, prefix, suffix)
	}
	return decodeLiteralMirroredValue(value)
}

func decodeEncodedMirroredValue(value, prefix, suffix string) (string, bool) {
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, suffix) || len(value) < len(prefix)+len(suffix) {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(value[len(prefix) : len(value)-len(suffix)])
	if err != nil || !utf8.Valid(decoded) {
		return "", false
	}
	return string(decoded), true
}

func decodeLiteralMirroredValue(value string) (string, bool) {
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
