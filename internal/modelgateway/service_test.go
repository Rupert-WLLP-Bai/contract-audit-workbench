package modelgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

func tokenFor(role string) string { return "private-" + role + "-credential-0123456789" }

func TestDecodeRequestAcceptsEncodedOCRAndRejectsOversize(t *testing.T) {
	const prefix = `{"model":"ocr","max_tokens":1,"image":"`
	const suffix = `"}`
	for _, test := range []struct {
		name      string
		size      int
		wantError bool
	}{
		{"encoded maximum PNG", (24 << 20) * 4 / 3, false},
		{"at request limit", maxRequestBytes - len(prefix) - len(suffix), false},
		{"over request limit", maxRequestBytes - len(prefix) - len(suffix) + 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := prefix + strings.Repeat("A", test.size) + suffix
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			req.Header.Set("X-Audit-Execution-Scope", "{}")
			got, _, _, _, err := decodeRequest(httptest.NewRecorder(), req)
			if (err != nil) != test.wantError || (!test.wantError && len(got) != len(body)) {
				t.Fatalf("decode length=%d err=%v", len(got), err)
			}
		})
	}
}

func contentOf(t *testing.T, response []byte) string {
	t.Helper()
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(response, &parsed); err != nil {
		t.Fatalf("fixture response is not a chat completion: %v", err)
	}
	if len(parsed.Choices) != 1 {
		t.Fatalf("fixture response has %d choices", len(parsed.Choices))
	}
	return parsed.Choices[0].Message.Content
}

func fixtureService() *Service {
	return &Service{config: Config{
		Mode: "fixture", ChatModel: "fixture-chat", OCRModel: "fixture-ocr",
		FixtureOCRContent: "质保期为36个月。",
	}}
}

func TestHandlerRejectsUnauthorizedAndMalformedModelRequests(t *testing.T) {
	service := &Service{
		config: Config{
			Mode: "fixture", WorkerCredential: tokenFor("worker"), DocumentCredential: tokenFor("document"),
			RequestTimeout: time.Second, Retries: 0,
		},
		jev:       JevConfig{Model: "jev-fixture-v1", Egress: "minimized", Features: []string{"knowledge_rerank"}},
		jevStatus: "fixture", jevQueue: make(chan struct{}, 1), jevSlots: make(chan struct{}, 1),
		queue: make(chan struct{}, 1), concurrency: make(chan struct{}, 1),
		circuit: circuitBreaker{threshold: 2, cooldown: time.Second},
	}
	handler := service.Handler()
	tests := []struct {
		name   string
		path   string
		method string
		token  string
		jev    bool
		body   string
		status int
		code   string
	}{
		{name: "chat authentication", path: "/v1/chat/completions", method: http.MethodPost, status: http.StatusUnauthorized, code: "service_unauthenticated"},
		{name: "chat malformed JSON", path: "/v1/chat/completions", method: http.MethodPost, token: tokenFor("worker"), body: "{", status: http.StatusBadRequest},
		{name: "advisory wrong identity", path: "/v1/systemone", method: http.MethodPost, token: tokenFor("document"), status: http.StatusForbidden, code: "jev_worker_identity_required"},
		{name: "advisory invalid JSON", path: "/v1/systemone", method: http.MethodPost, token: tokenFor("worker"), jev: true, body: "{", status: http.StatusBadRequest, code: "jev_request_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.token != "" {
				request.Header.Set("X-Contract-Service-Key", test.token)
			}
			if test.jev {
				request.Header.Set("X-Contract-Jev-Feature", "knowledge_rerank")
				request.Header.Set("X-Contract-Data-Classification", "minimized")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("HTTP status=%d, want %d; body=%s", response.Code, test.status, response.Body)
			}
			if test.code != "" && gatewayErrorCode(t, response.Body.Bytes()) != test.code {
				t.Fatalf("response code=%q, want %q", gatewayErrorCode(t, response.Body.Bytes()), test.code)
			}
		})
	}
}

func TestHandlerRejectsDisallowedModelsAndFullQueues(t *testing.T) {
	service := &Service{
		config: Config{
			Mode: "openai-compatible", ChatModel: "chat-configured", OCRModel: "ocr-configured",
			WorkerCredential: tokenFor("worker"), DocumentCredential: tokenFor("document"),
			RequestTimeout: time.Second,
			Budget:         store.ModelBudgetLimits{MaxInputTokenReservation: 64_000, MaxOutputTokenReservation: 4096},
		},
		jev:       JevConfig{Model: "jev-fixture-v1", Egress: "minimized", Features: []string{"knowledge_rerank"}, MaxOutputTokens: 1024, Timeout: time.Second},
		jevStatus: "fixture", jevQueue: make(chan struct{}, 1), jevSlots: make(chan struct{}, 1),
		queue: make(chan struct{}, 1), concurrency: make(chan struct{}, 1),
		circuit: circuitBreaker{threshold: 2, cooldown: time.Second},
	}
	handler := service.Handler()
	requestWithAuth := func(path, body string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("X-Contract-Service-Key", tokenFor("worker"))
		r.Header.Set("X-Audit-Execution-Scope", `{}`)
		return r
	}

	badModel := requestWithAuth("/v1/chat/completions", `{"model":"unconfigured","max_tokens":1}`)
	badModelResponse := httptest.NewRecorder()
	handler.ServeHTTP(badModelResponse, badModel)
	if badModelResponse.Code != http.StatusBadRequest || gatewayErrorCode(t, badModelResponse.Body.Bytes()) != "model_not_configured" {
		t.Fatalf("unconfigured model response=%d %s", badModelResponse.Code, badModelResponse.Body)
	}

	fullQueue := requestWithAuth("/v1/chat/completions", `{}`)
	service.queue <- struct{}{}
	fullQueueResponse := httptest.NewRecorder()
	handler.ServeHTTP(fullQueueResponse, fullQueue)
	<-service.queue
	if fullQueueResponse.Code != http.StatusServiceUnavailable || gatewayErrorCode(t, fullQueueResponse.Body.Bytes()) != "model_queue_full" {
		t.Fatalf("full chat queue response=%d %s", fullQueueResponse.Code, fullQueueResponse.Body)
	}

	jevBody := `{"model":"jev-fixture-v1","state":"{}","questions":{"candidate_0_relevance":{"type":"noul","instructions":"check relevance","criteria":{"true":"applies","false":"does not apply"}}}}`
	jevRequest := requestWithAuth("/v1/systemone", jevBody)
	jevRequest.Header.Set("X-Contract-Jev-Feature", "knowledge_rerank")
	jevRequest.Header.Set("X-Contract-Data-Classification", "minimized")
	service.jevQueue <- struct{}{}
	jevResponse := httptest.NewRecorder()
	handler.ServeHTTP(jevResponse, jevRequest)
	<-service.jevQueue
	if jevResponse.Code != http.StatusServiceUnavailable || gatewayErrorCode(t, jevResponse.Body.Bytes()) != "jev_queue_full" {
		t.Fatalf("full advisory queue response=%d %s", jevResponse.Code, jevResponse.Body)
	}
}

func TestChatHandlerReportsReservationFailureBeforeDispatch(t *testing.T) {
	service := &Service{
		store: &store.Store{},
		config: Config{
			Mode: "fixture", ChatModel: "fixture-chat", OCRModel: "fixture-ocr",
			WorkerCredential: tokenFor("worker"), DocumentCredential: tokenFor("document"),
			RequestTimeout: time.Second,
			Budget:         store.ModelBudgetLimits{MaxInputTokenReservation: 4096, MaxOutputTokenReservation: 4096},
		},
		queue: make(chan struct{}, 1), concurrency: make(chan struct{}, 1),
		circuit: circuitBreaker{threshold: 2, cooldown: time.Second},
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fixture-chat","max_tokens":1,"messages":[]}`))
	request.Header.Set("X-Contract-Service-Key", tokenFor("worker"))
	request.Header.Set("X-Audit-Execution-Scope", `{}`)
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || gatewayErrorCode(t, response.Body.Bytes()) != "model_budget_unavailable" {
		t.Fatalf("reservation failure response=%d %s", response.Code, response.Body)
	}
}

func TestModelsHandlerReadsProviderCatalogAndFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   int
		body     string
		wantCode int
		wantID   bool
	}{
		{name: "catalog available", status: http.StatusOK, body: `{"data":[{"id":"chat-a"},{"id":"  "}]}`, wantCode: http.StatusOK, wantID: true},
		{name: "provider unavailable", status: http.StatusBadGateway, body: `{"error":"upstream"}`, wantCode: http.StatusServiceUnavailable},
		{name: "empty catalog", status: http.StatusOK, body: `{"data":[]}`, wantCode: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer upstream.Close()
			service := &Service{
				config: Config{Mode: "openai-compatible", UpstreamBaseURL: upstream.URL, WorkerCredential: tokenFor("worker"), DocumentCredential: tokenFor("document")},
				client: upstream.Client(),
			}
			request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			request.Header.Set("X-Contract-Service-Key", tokenFor("worker"))
			response := httptest.NewRecorder()
			service.Handler().ServeHTTP(response, request)
			if response.Code != test.wantCode {
				t.Fatalf("HTTP status=%d, want %d; body=%s", response.Code, test.wantCode, response.Body)
			}
			if test.wantID && !strings.Contains(response.Body.String(), `"id":"chat-a"`) {
				t.Fatalf("catalog response omitted provider model: %s", response.Body)
			}
		})
	}

	service := &Service{}
	if _, err := service.fetchCatalog(context.Background()); err == nil {
		t.Fatal("catalog fetch without an upstream URL succeeded")
	}
}

func TestDispatchPreservesProviderResponseAndPreDispatchClassification(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantErr    bool
		wantUsage  bool
	}{
		{name: "success with usage", status: http.StatusOK, body: `{"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`, wantStatus: http.StatusOK, wantUsage: true},
		{name: "provider error response", status: http.StatusBadGateway, body: `{"error":"private detail"}`, wantStatus: http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
					t.Errorf("upstream request=%s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer upstream.Close()
			service := &Service{config: Config{Mode: "openai-compatible", UpstreamBaseURL: upstream.URL}, client: upstream.Client()}
			body, status, usage, err, retryable, preDispatch := service.dispatch(context.Background(), "chat-a", []byte(`{"model":"chat-a"}`))
			if status != test.wantStatus || (err != nil) != test.wantErr || (usage != nil) != test.wantUsage || retryable != (test.status >= 500) || preDispatch {
				t.Fatalf("status=%d usage=%+v err=%v retryable=%t preDispatch=%t", status, usage, err, retryable, preDispatch)
			}
			if string(body) != test.body {
				t.Fatalf("body=%q, want %q", body, test.body)
			}
		})
	}
	service := &Service{}
	_, status, _, err, retryable, preDispatch := service.dispatch(context.Background(), "chat-a", nil)
	if status != http.StatusServiceUnavailable || err == nil || retryable || !preDispatch {
		t.Fatalf("unconfigured provider status=%d err=%v retryable=%t preDispatch=%t", status, err, retryable, preDispatch)
	}
}

func TestProviderAttemptErrorsAndJevStatusUsePublicResponses(t *testing.T) {
	service := &Service{
		config:   Config{WorkerCredential: tokenFor("worker"), DocumentCredential: tokenFor("document"), Retries: 1},
		jev:      JevConfig{Model: "jev-fixture-v1", Egress: "minimized", Features: []string{"knowledge_rerank"}},
		jevQueue: make(chan struct{}, 1), jevSlots: make(chan struct{}, 1),
		queue: make(chan struct{}, 1), concurrency: make(chan struct{}, 1),
		circuit: circuitBreaker{threshold: 4, cooldown: time.Second},
	}
	handler := service.Handler()
	for _, test := range []struct {
		status int
		want   int
		code   string
	}{
		{status: http.StatusBadRequest, want: http.StatusBadGateway, code: "model_provider_failed"},
		{status: http.StatusServiceUnavailable, want: http.StatusServiceUnavailable, code: "jev_disabled"},
	} {
		service.jevStatus = "off"
		request := httptest.NewRequest(http.MethodPost, "/v1/systemone", nil)
		request.Header.Set("X-Contract-Service-Key", tokenFor("worker"))
		response := httptest.NewRecorder()
		if test.status == http.StatusBadRequest {
			attemptResponse := httptest.NewRecorder()
			done := service.respondToChatAttempt(attemptResponse, context.Background(), chatRequest{}, 1, 1, nil, test.status, nil, false)
			if done || attemptResponse.Code != test.want || gatewayErrorCode(t, attemptResponse.Body.Bytes()) != test.code {
				t.Fatalf("provider error response=%d %s done=%t", attemptResponse.Code, attemptResponse.Body, done)
			}
			continue
		}
		handler.ServeHTTP(response, request)
		if response.Code != test.want || gatewayErrorCode(t, response.Body.Bytes()) != test.code {
			t.Fatalf("advisory disabled response=%d %s", response.Code, response.Body)
		}
	}
	retryResponse := httptest.NewRecorder()
	if !service.respondToChatAttempt(retryResponse, context.Background(), chatRequest{}, 1, 1, nil, http.StatusServiceUnavailable, nil, false) {
		t.Fatal("provider 5xx response did not request a configured retry")
	}
	if !service.respondToChatError(httptest.NewRecorder(), 1, 1, errors.New("transient provider error"), true) {
		t.Fatal("retryable provider failure did not request another attempt")
	}
}

func TestGatewayErrorMappingsAndProviderReadiness(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		want   string
	}{
		{errors.New("offline"), 502, "provider_transport_error"},
		{context.DeadlineExceeded, 502, "provider_timeout"},
		{nil, 429, "provider_http_429"},
	} {
		if got := requestErrCode(test.err, test.status); got != test.want {
			t.Errorf("requestErrCode(%v, %d)=%q, want %q", test.err, test.status, got, test.want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if retryableTransport(errors.New("offline"), context.Background()) != true || retryableTransport(context.DeadlineExceeded, context.Background()) || retryableTransport(errors.New("offline"), ctx) {
		t.Fatal("transport retry classification changed")
	}
	if !containsModel([]string{"chat-a", "ocr-a"}, "chat-a") || containsModel([]string{"chat-a"}, "missing") {
		t.Fatal("configured model catalog membership was not preserved")
	}
	service := &Service{config: Config{Mode: "fixture", ChatModel: "chat-a", OCRModel: "ocr-a"}}
	if err := service.readyProvider(context.Background()); err != nil {
		t.Fatalf("fixture provider readiness: %v", err)
	}
	service.config.Mode = "openai-compatible"
	if err := service.readyProvider(context.Background()); err == nil {
		t.Fatal("remote readiness without configured catalog succeeded")
	}
}

func TestReservationAndAdvisoryProviderErrorResponses(t *testing.T) {
	for _, test := range []struct {
		err    error
		jev    bool
		status int
		code   string
	}{
		{store.ErrModelBudgetExceeded, false, http.StatusTooManyRequests, "model_budget_exhausted"},
		{store.ErrModelScopeUnavailable, false, http.StatusForbidden, "model_scope_unavailable"},
		{errors.New("storage"), false, http.StatusServiceUnavailable, "model_budget_unavailable"},
		{store.ErrModelBudgetExceeded, true, http.StatusTooManyRequests, "jev_budget_exhausted"},
		{store.ErrModelScopeUnavailable, true, http.StatusForbidden, "model_scope_unavailable"},
		{errors.New("storage"), true, http.StatusServiceUnavailable, "jev_budget_unavailable"},
	} {
		response := httptest.NewRecorder()
		messages := modelReservationErrors
		if test.jev {
			messages = jevReservationErrors
		}
		writeReservationError(response, test.err, messages)
		if response.Code != test.status || gatewayErrorCode(t, response.Body.Bytes()) != test.code {
			t.Errorf("reservation response=%d %s, want %d %s", response.Code, response.Body, test.status, test.code)
		}
	}
	for _, test := range []struct {
		err    error
		code   string
		status int
	}{
		{context.DeadlineExceeded, "jev_timeout", http.StatusGatewayTimeout},
		{context.Canceled, "jev_cancelled", http.StatusServiceUnavailable},
		{errJevInvalidResponse, "jev_invalid_response", http.StatusBadGateway},
		{errors.New("provider"), "jev_provider_failed", http.StatusBadGateway},
	} {
		response := httptest.NewRecorder()
		service := &Service{jevCircuit: circuitBreaker{threshold: 3, cooldown: time.Second}}
		service.writeSystemOneFailure(response, test.err, 1)
		if response.Code != test.status || gatewayErrorCode(t, response.Body.Bytes()) != test.code {
			t.Errorf("advisory provider error response=%d %s, want %d %s", response.Code, response.Body, test.status, test.code)
		}
	}
}

func TestReadyHandlerFailsClosedWithoutDatabase(t *testing.T) {
	service := &Service{store: &store.Store{}}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		request := httptest.NewRequest(method, "/ready", nil)
		response := httptest.NewRecorder()
		service.Handler().ServeHTTP(response, request)
		if method == http.MethodGet && response.Code != http.StatusServiceUnavailable {
			t.Fatalf("readiness without database status=%d", response.Code)
		}
		if method == http.MethodPost && (response.Code != http.StatusMethodNotAllowed) {
			t.Fatalf("readiness method response=%d allow=%q", response.Code, response.Header().Get("Allow"))
		}
	}
}

func TestSystemOneHandlerPreservesAvailabilityAndClassificationGates(t *testing.T) {
	service := &Service{
		config:   Config{WorkerCredential: tokenFor("worker"), DocumentCredential: tokenFor("document"), Budget: store.ModelBudgetLimits{MaxInputTokenReservation: 64_000, MaxOutputTokenReservation: 4096}},
		jev:      JevConfig{Model: "jev-fixture-v1", Egress: "minimized", Features: []string{"knowledge_rerank"}, MaxOutputTokens: 1024},
		jevQueue: make(chan struct{}, 1), jevSlots: make(chan struct{}, 1),
	}
	handler := service.Handler()
	for _, test := range []struct {
		name           string
		status         string
		feature        string
		classification string
		body           string
		wantStatus     int
		wantCode       string
	}{
		{"off", "off", "", "", "", http.StatusServiceUnavailable, "jev_disabled"},
		{"unconfigured", "unconfigured", "", "", "", http.StatusServiceUnavailable, "jev_unconfigured"},
		{"unavailable", "unavailable", "", "", "", http.StatusServiceUnavailable, "jev_unavailable"},
		{"invalid state", "fixture", "", "", "", http.StatusBadRequest, "jev_feature_invalid"},
		{"classification required", "fixture", "knowledge_rerank", "", "", http.StatusBadRequest, "jev_classification_missing"},
		{"approved egress refused", "fixture", "knowledge_rerank", "approved_text", "", http.StatusForbidden, "jev_egress_not_approved"},
		{"classification mismatch", "fixture", "knowledge_rerank", "other", "", http.StatusBadRequest, "jev_classification_mismatch"},
		{"invalid request JSON", "fixture", "knowledge_rerank", "minimized", "{", http.StatusBadRequest, "jev_request_invalid"},
		{"model mismatch", "fixture", "knowledge_rerank", "minimized", `{"model":"other","state":"{}","questions":{"candidate_0_relevance":{"type":"noul","instructions":"check relevance","criteria":{"true":"applies","false":"does not apply"}}}}`, http.StatusBadRequest, "jev_model_mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service.jevStatus = test.status
			request := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(test.body))
			request.Header.Set("X-Contract-Service-Key", tokenFor("worker"))
			if test.feature != "" {
				request.Header.Set("X-Contract-Jev-Feature", test.feature)
			}
			if test.classification != "" {
				request.Header.Set("X-Contract-Data-Classification", test.classification)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus || gatewayErrorCode(t, response.Body.Bytes()) != test.wantCode {
				t.Fatalf("response=%d %s, want %d %s", response.Code, response.Body, test.wantStatus, test.wantCode)
			}
		})
	}
	validBody := `{"model":"jev-fixture-v1","state":"{}","questions":{"candidate_0_relevance":{"type":"noul","instructions":"check relevance","criteria":{"true":"applies","false":"does not apply"}}}}`
	newRequest := func(scope bool) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(validBody))
		r.Header.Set("X-Contract-Service-Key", tokenFor("worker"))
		r.Header.Set("X-Contract-Jev-Feature", "knowledge_rerank")
		r.Header.Set("X-Contract-Data-Classification", "minimized")
		if scope {
			r.Header.Set("X-Audit-Execution-Scope", `{}`)
		}
		return r
	}
	service.jevStatus = "fixture"
	missingScope := httptest.NewRecorder()
	handler.ServeHTTP(missingScope, newRequest(false))
	if missingScope.Code != http.StatusBadRequest || gatewayErrorCode(t, missingScope.Body.Bytes()) != "jev_scope_missing" {
		t.Fatalf("missing scope response=%d %s", missingScope.Code, missingScope.Body)
	}
	service.config.Budget.MaxInputTokenReservation = 1
	oversizedReservation := httptest.NewRecorder()
	handler.ServeHTTP(oversizedReservation, newRequest(true))
	if oversizedReservation.Code != http.StatusBadRequest || gatewayErrorCode(t, oversizedReservation.Body.Bytes()) != "jev_token_bound_exceeded" {
		t.Fatalf("reservation bound response=%d %s", oversizedReservation.Code, oversizedReservation.Body)
	}
}

func TestSystemOneAndChatCapacityReleaseOnAcquisition(t *testing.T) {
	service := &Service{
		config: Config{RequestTimeout: time.Second},
		jev:    JevConfig{Timeout: time.Second},
		queue:  make(chan struct{}, 1), concurrency: make(chan struct{}, 1),
		jevQueue: make(chan struct{}, 1), jevSlots: make(chan struct{}, 1),
		circuit:    circuitBreaker{threshold: 2, cooldown: time.Second},
		jevCircuit: circuitBreaker{threshold: 2, cooldown: time.Second},
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	ctx, releaseChat, _, ok := service.acquireChatCapacity(httptest.NewRecorder(), req)
	if !ok || ctx == nil {
		t.Fatal("chat capacity acquisition failed")
	}
	releaseChat()
	if len(service.queue) != 0 || len(service.concurrency) != 0 {
		t.Fatal("chat capacity was not released")
	}
	ctx, releaseJev, ok := service.acquireSystemOneCapacity(httptest.NewRecorder(), req)
	if !ok || ctx == nil {
		t.Fatal("advisory capacity acquisition failed")
	}
	releaseJev()
	if len(service.jevQueue) != 0 || len(service.jevSlots) != 0 {
		t.Fatal("advisory capacity was not released")
	}
}

type fixturePlan struct {
	Calls []struct {
		ToolID string `json:"tool_id"`
		Domain string `json:"domain"`
		GapID  string `json:"gap_id"`
	} `json:"calls"`
}

func fixturePlannerPlan(t *testing.T, user any) fixturePlan {
	t.Helper()
	encoded, err := json.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"model": "fixture-chat", "messages": []map[string]string{
		{"role": "system", "content": "Select only registered read-only tools."},
		{"role": "user", "content": string(encoded)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	content := contentOf(t, fixtureService().fixtureResponse("fixture-chat", body))
	var plan fixturePlan
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		t.Fatalf("fixture did not return a plan: %v", err)
	}
	return plan
}

func TestFixturePlannerSelectsOnlyRequestedRegisteredTools(t *testing.T) {
	plan := fixturePlannerPlan(t, map[string]any{
		"scope": []string{"compliance"},
		"gaps":  []map[string]string{{"id": "gap-amount", "domain": "compliance", "capability": "amount"}},
		"tools": []map[string]any{{"id": "baseline.compliance", "domain": "compliance", "capabilities": []string{"authorization_basis"}},
			{"id": "source.compliance", "domain": "compliance", "capabilities": []string{"amount"}}},
		"previously_called": []any{},
	})
	if len(plan.Calls) != 1 || plan.Calls[0].ToolID != "source.compliance" ||
		plan.Calls[0].Domain != "compliance" || plan.Calls[0].GapID != "gap-amount" {
		t.Fatalf("fixture plan exceeded its registered request: %+v", plan)
	}
}

// Selection is per gap, not per id: when two gaps share an id, the second gap
// still reaches the tool that satisfies its own capability instead of stopping
// at the tool the previous gap used.
func TestFixturePlannerSelectsEachGapWhenIDsRepeat(t *testing.T) {
	plan := fixturePlannerPlan(t, map[string]any{
		"scope": []string{"compliance"},
		"gaps": []map[string]string{
			{"id": "gap-shared", "domain": "compliance", "capability": "amount"},
			{"id": "gap-shared", "domain": "compliance", "capability": "deadline"},
		},
		"tools": []map[string]any{
			{"id": "source.amount", "domain": "compliance", "capabilities": []string{"amount"}},
			{"id": "source.deadline", "domain": "compliance", "capabilities": []string{"deadline"}},
		},
		"previously_called": []any{},
	})
	if len(plan.Calls) != 2 ||
		plan.Calls[0].ToolID != "source.amount" || plan.Calls[0].GapID != "gap-shared" ||
		plan.Calls[1].ToolID != "source.deadline" || plan.Calls[1].GapID != "gap-shared" {
		t.Fatalf("repeated gap id did not select per gap: %+v", plan)
	}
}

// The fixture is a stub, not a model: it may cite only material spans the review
// prompt itself carries, and it must not answer a prompt that carries none.
func TestFixtureChatAnswerCitesOnlyPromptSpans(t *testing.T) {
	service := fixtureService()
	prompt := `{"messages":[{"content":"document_id=doc-a 采购文件\n正文：采购文件要求质保36个月。\ndocument_id=doc-b 合同草稿\n正文：拟签合同约定质保24个月。"}]}`

	content := contentOf(t, service.fixtureResponse("fixture-chat", []byte(prompt)))
	warranty, err := model.ParseWarranty(content)
	if err != nil {
		t.Fatalf("the derived fixture answer is not a usable warranty answer: %v", err)
	}
	if warranty.Relation != "conflict" || warranty.Disposition == "" {
		t.Fatalf("derived answer %+v", warranty)
	}
	if len(warranty.Quotes) != 2 {
		t.Fatalf("derived %d quotes, want the two prompt spans", len(warranty.Quotes))
	}
	if warranty.Quotes[0].DocumentID != "doc-a" || warranty.Quotes[0].Quote != "采购文件要求质保36个月。" ||
		warranty.Quotes[1].DocumentID != "doc-b" || warranty.Quotes[1].Quote != "拟签合同约定质保24个月。" {
		t.Fatalf("derived quotes are not the prompt spans: %+v", warranty.Quotes)
	}

	if answer, ok := fixtureWarrantyAnswer([]byte(`{"messages":[{"content":"请判断质保期是否一致"}]}`)); ok {
		t.Fatalf("the fixture answered a prompt without material spans: %q", answer)
	}

	service.config.FixtureChatContent = `{"claim":"固定输出","relation":"compatible"}`
	if explicit := contentOf(t, service.fixtureResponse("fixture-chat", []byte(prompt))); explicit != service.config.FixtureChatContent {
		t.Fatalf("explicit fixture content was replaced: %q", explicit)
	}

	if ocr := contentOf(t, service.fixtureResponse("fixture-ocr", []byte(prompt))); ocr != "质保期为36个月。" {
		t.Fatalf("the OCR model answered with %q", ocr)
	}
}

// A fixture without distinct model identifiers cannot dispatch an answer
// unambiguously, and one without an OCR transcription is refused.
func TestFixtureRequiresDistinctIdentifiersAndOCRContent(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	for _, config := range []Config{
		{Mode: "fixture", ChatModel: "same", OCRModel: "same", FixtureOCRContent: "text"},
		{Mode: "fixture", ChatModel: "fixture-chat", OCRModel: "fixture-ocr"},
	} {
		config.WorkerCredential = tokenFor("worker")
		config.DocumentCredential = tokenFor("document")
		config.MaxConcurrency, config.QueueCapacity = 1, 1
		config.RequestTimeout, config.CircuitCooldown = time.Minute, time.Minute
		config.CircuitFailureThreshold = 1
		config.Budget = store.ModelBudgetLimits{LeaseDuration: time.Minute}
		if _, err := New(&store.Store{}, config); err == nil {
			t.Fatalf("incomplete fixture configuration was accepted: %+v", config)
		}
	}
	valid, err := New(&store.Store{}, Config{
		Mode: "fixture", ChatModel: "fixture-chat", OCRModel: "fixture-ocr", FixtureOCRContent: "text",
		WorkerCredential: tokenFor("worker"), DocumentCredential: tokenFor("document"),
		MaxConcurrency: 1, QueueCapacity: 1, RequestTimeout: time.Minute,
		CircuitFailureThreshold: 1, CircuitCooldown: time.Minute,
		Budget: store.ModelBudgetLimits{LeaseDuration: time.Minute},
	})
	if err != nil || valid == nil {
		t.Fatalf("a complete fixture configuration was refused: %v", err)
	}
}
