package modelgateway

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"contractaudit/internal/jev"
	"contractaudit/internal/llmendpoint"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

const (
	maxRequestBytes  = 33 << 20 // 24 MiB PNG after base64 encoding plus JSON overhead
	maxResponseBytes = 1 << 20
	maxScopeBytes    = 16 << 10
)

type Config struct {
	Mode            string
	UpstreamBaseURL string
	UpstreamAPIKey  string
	// ProviderURL is the address the configuration ids of callers name. It is
	// normally LLM_BASE_URL; deployment tooling that fronts the provider with a
	// different gateway address sets it explicitly.
	ProviderURL             string
	ChatModel               string
	OCRModel                string
	WorkerCredential        string
	DocumentCredential      string
	FixtureChatContent      string
	FixtureOCRContent       string
	MaxConcurrency          int
	QueueCapacity           int
	RequestTimeout          time.Duration
	Retries                 int
	CircuitFailureThreshold int
	CircuitCooldown         time.Duration
	Budget                  store.ModelBudgetLimits
	Jev                     JevConfig
}

// FromEnv reads an explicit real-provider or local-only deterministic fixture
// profile. The fixture has no token-usage fields and cannot start in production.
func FromEnv() (Config, error) {
	config := Config{
		Mode:                    strings.TrimSpace(os.Getenv("MODEL_GATEWAY_MODE")),
		UpstreamBaseURL:         strings.TrimRight(strings.TrimSpace(os.Getenv("LLM_BASE_URL")), "/"),
		UpstreamAPIKey:          strings.TrimSpace(os.Getenv("LLM_API_KEY")),
		ChatModel:               strings.TrimSpace(os.Getenv("LLM_MODEL")),
		OCRModel:                strings.TrimSpace(os.Getenv("OCR_MODEL")),
		WorkerCredential:        strings.TrimSpace(os.Getenv("WORKER_MODEL_TOKEN")),
		DocumentCredential:      strings.TrimSpace(os.Getenv("DOCUMENT_MODEL_TOKEN")),
		FixtureChatContent:      os.Getenv("MODEL_GATEWAY_FIXTURE_CHAT_CONTENT"),
		FixtureOCRContent:       os.Getenv("MODEL_GATEWAY_FIXTURE_OCR_CONTENT"),
		MaxConcurrency:          8,
		QueueCapacity:           32,
		RequestTimeout:          120 * time.Second,
		Retries:                 1,
		CircuitFailureThreshold: 5,
		CircuitCooldown:         30 * time.Second,
		Budget: store.ModelBudgetLimits{
			GlobalMaxInFlight: 32, TenantMaxInFlight: 4,
			GlobalCallsPerMinute: 600, TenantCallsPerMinute: 120,
			GlobalTokensPerMinute: 2_000_000, TenantTokensPerMinute: 400_000,
			MaxInputTokenReservation: 64_000, MaxOutputTokenReservation: 8_192,
		},
	}
	if config.Mode == "" {
		config.Mode = "openai-compatible"
	}
	if config.ChatModel == "" {
		config.ChatModel = model.DefaultChatModel
	}
	if config.OCRModel == "" {
		config.OCRModel = model.DefaultOCRModel
	}
	if err := applyEnvIntOverrides(&config); err != nil {
		return Config{}, err
	}
	if err := applyEnvInt64Overrides(&config); err != nil {
		return Config{}, err
	}
	if err := applyEnvDurationOverrides(&config); err != nil {
		return Config{}, err
	}
	config.Budget.LeaseDuration = config.RequestTimeout + 30*time.Second
	config.Jev = jevConfigFromEnv()
	return config, nil
}

func applyEnvIntOverrides(config *Config) error {
	ints := []struct {
		name   string
		target *int
		min    int
		max    int
	}{
		{"MODEL_MAX_CONCURRENCY", &config.MaxConcurrency, 1, 1024},
		{"MODEL_QUEUE_CAPACITY", &config.QueueCapacity, 1, 4096},
		{"MODEL_RETRIES", &config.Retries, 0, 3},
		{"MODEL_CIRCUIT_FAILURES", &config.CircuitFailureThreshold, 1, 100},
		{"MODEL_GLOBAL_MAX_INFLIGHT", &config.Budget.GlobalMaxInFlight, 1, 10000},
		{"MODEL_TENANT_MAX_INFLIGHT", &config.Budget.TenantMaxInFlight, 1, 10000},
		{"MODEL_GLOBAL_CALLS_PER_MINUTE", &config.Budget.GlobalCallsPerMinute, 1, 1000000},
		{"MODEL_TENANT_CALLS_PER_MINUTE", &config.Budget.TenantCallsPerMinute, 1, 1000000},
	}
	for _, item := range ints {
		raw := strings.TrimSpace(os.Getenv(item.name))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < item.min || value > item.max {
			return fmt.Errorf("%s is outside its supported integer range", item.name)
		}
		*item.target = value
	}
	return nil
}

func applyEnvInt64Overrides(config *Config) error {
	for _, item := range []struct {
		name   string
		target *int64
	}{
		{"MODEL_GLOBAL_TOKENS_PER_MINUTE", &config.Budget.GlobalTokensPerMinute},
		{"MODEL_TENANT_TOKENS_PER_MINUTE", &config.Budget.TenantTokensPerMinute},
		{"MODEL_MAX_INPUT_TOKEN_RESERVATION", &config.Budget.MaxInputTokenReservation},
		{"MODEL_MAX_OUTPUT_TOKEN_RESERVATION", &config.Budget.MaxOutputTokenReservation},
	} {
		raw := strings.TrimSpace(os.Getenv(item.name))
		if raw == "" {
			continue
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			return fmt.Errorf("%s must be a positive integer", item.name)
		}
		*item.target = value
	}
	return nil
}

func applyEnvDurationOverrides(config *Config) error {
	for _, item := range []struct {
		name   string
		target *time.Duration
		max    time.Duration
	}{
		{"MODEL_REQUEST_TIMEOUT", &config.RequestTimeout, 10 * time.Minute},
		{"MODEL_CIRCUIT_COOLDOWN", &config.CircuitCooldown, 10 * time.Minute},
	} {
		raw := strings.TrimSpace(os.Getenv(item.name))
		if raw == "" {
			continue
		}
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 || value > item.max {
			return fmt.Errorf("%s must be a positive duration within the supported limit", item.name)
		}
		*item.target = value
	}
	return nil
}

type Service struct {
	store         *store.Store
	config        Config
	client        *http.Client
	guardedClient *http.Client
	queue         chan struct{}
	concurrency   chan struct{}
	circuit       circuitBreaker
	catalogMu     sync.Mutex
	catalogAt     time.Time
	catalogKey    string
	catalogIDs    []string
	upstreamMu    sync.Mutex
	upstreamAt    time.Time
	upstreamCfg   upstreamConfig
	upstreamErr   error

	jev        JevConfig
	jevClient  *jev.Client
	jevQueue   chan struct{}
	jevSlots   chan struct{}
	jevCircuit circuitBreaker
	jevStatus  string
	jevReason  string
}

func New(st *store.Store, config Config) (*Service, error) {
	config.Mode = strings.TrimSpace(config.Mode)
	config.UpstreamBaseURL = strings.TrimRight(strings.TrimSpace(config.UpstreamBaseURL), "/")
	if err := validateBaseConfig(st, config); err != nil {
		return nil, err
	}
	if err := validateModeConfig(config); err != nil {
		return nil, err
	}
	service := newService(st, config)
	service.jevStatus, service.jevReason, service.jevClient = normalizeJev(&service.jev, config.Budget)
	return service, nil
}

func validateBaseConfig(st *store.Store, config Config) error {
	if st == nil || (config.Mode != "openai-compatible" && config.Mode != "fixture") || !validServiceCredentials(config) || !validServiceCapacity(config) {
		return errors.New("model gateway configuration is incomplete or invalid")
	}
	return nil
}

func validServiceCredentials(config Config) bool {
	return len(config.WorkerCredential) >= 32 && len(config.DocumentCredential) >= 32 && subtle.ConstantTimeCompare([]byte(config.WorkerCredential), []byte(config.DocumentCredential)) != 1
}

func validServiceCapacity(config Config) bool {
	return config.MaxConcurrency >= 1 && config.QueueCapacity >= 1 && config.RequestTimeout > 0 && config.CircuitFailureThreshold >= 1 && config.CircuitCooldown > 0 && config.Budget.LeaseDuration > 0
}

func validateModeConfig(config Config) error {
	if config.Mode == "fixture" {
		return validateFixtureConfig(config)
	}
	return validateUpstreamURL(config.UpstreamBaseURL)
}

func validateUpstreamURL(baseURL string) error {
	if baseURL == "" {
		return nil
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("LLM_BASE_URL must be an HTTP(S) base URL without credentials or query")
	}
	return nil
}

func validateFixtureConfig(config Config) error {
	environment := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	if environment != "development" && environment != "dev" && environment != "test" {
		return errors.New("local model fixture is disabled outside development and test")
	}
	if strings.TrimSpace(config.FixtureOCRContent) == "" {
		return errors.New("local model fixture OCR content must be explicitly configured")
	}
	if model.GatewayID(config.ChatModel) == model.GatewayID(config.OCRModel) {
		return errors.New("local model fixture requires distinct chat and OCR model identifiers")
	}
	return nil
}

func newService(st *store.Store, config Config) *Service {
	return &Service{
		store: st, config: config, client: &http.Client{Timeout: config.RequestTimeout},
		guardedClient: llmendpoint.GuardedClient(config.RequestTimeout),
		queue:         make(chan struct{}, config.QueueCapacity), concurrency: make(chan struct{}, config.MaxConcurrency),
		circuit: circuitBreaker{threshold: config.CircuitFailureThreshold, cooldown: config.CircuitCooldown},
		jev:     config.Jev, jevQueue: make(chan struct{}, jevQueueCapacity), jevSlots: make(chan struct{}, jevMaxConcurrency),
		jevCircuit: circuitBreaker{threshold: config.CircuitFailureThreshold, cooldown: config.CircuitCooldown},
	}
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", s.live)
	mux.HandleFunc("/health/ready", s.ready)
	mux.HandleFunc("/health", s.live)
	mux.HandleFunc("/ready", s.ready)
	mux.HandleFunc("/v1/models", s.models)
	mux.HandleFunc("/v1/chat/completions", s.chatCompletions)
	mux.HandleFunc("/v1/systemone", s.systemOne)
	mux.HandleFunc("/health/jev", s.jevHealth)
	return mux
}

func (s *Service) live(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "model"})
}

func (s *Service) ready(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.store.Ready(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "service": "model"})
		return
	}
	if err := s.readyProvider(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "service": "model", "dependency": "provider"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "service": "model", "mode": s.config.Mode})
}

func (s *Service) readyProvider(ctx context.Context) error {
	if s.config.Mode == "fixture" {
		return nil
	}
	upstream, err := s.upstreamFor(ctx)
	if err != nil {
		return err
	}
	ids, err := s.fetchCatalogFrom(ctx, upstream)
	if err != nil {
		return err
	}
	if !containsModel(ids, upstream.ChatModel) || !containsModel(ids, upstream.OCRModel) {
		return errors.New("configured model is absent from upstream catalog")
	}
	return nil
}

func (s *Service) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.authenticate(w, r); !ok {
		return
	}
	ids, err := s.catalog(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "model catalog is unavailable"})
		return
	}
	items := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		items = append(items, map[string]string{"id": id, "object": "model"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": items})
}

type chatRequest struct {
	caller          string
	body            []byte
	requestedModel  string
	requestedTokens int
	scope           model.DelegatedScope
	upstream        upstreamConfig
	frozenMatches   bool
}

func (s *Service) chatCompletions(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	caller, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	s.serveChatCompletions(w, r, caller)
}

func (s *Service) serveChatCompletions(w http.ResponseWriter, r *http.Request, caller string) {
	ctx, release, probe, ok := s.acquireChatCapacity(w, r)
	if !ok {
		return
	}
	defer release()
	request, ok := s.readChatRequest(w, r, caller)
	if !ok {
		return
	}
	s.runChatAttempts(w, ctx, request, probe)
}

func (s *Service) acquireChatCapacity(w http.ResponseWriter, r *http.Request) (context.Context, func(), circuitProbe, bool) {
	select {
	case s.queue <- struct{}{}:
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "model gateway capacity is full", "code": "model_queue_full"})
		return nil, nil, 0, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.config.RequestTimeout)
	select {
	case s.concurrency <- struct{}{}:
	case <-ctx.Done():
		<-s.queue
		cancel()
		writeModelContextError(w, ctx.Err())
		return nil, nil, 0, false
	}
	allowed, probe := s.circuit.allow(time.Now())
	if !allowed {
		<-s.concurrency
		<-s.queue
		cancel()
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "model provider circuit is open", "code": "model_circuit_open"})
		return nil, nil, 0, false
	}
	return ctx, func() { s.circuit.release(probe); <-s.concurrency; <-s.queue; cancel() }, probe, true
}

func (s *Service) readChatRequest(w http.ResponseWriter, r *http.Request, caller string) (chatRequest, bool) {
	body, requestedModel, requestedTokens, scope, err := decodeRequest(w, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid OpenAI-compatible request"})
		return chatRequest{}, false
	}
	upstream, err := s.upstreamFor(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "model upstream configuration is unavailable", "code": "model_upstream_unavailable"})
		return chatRequest{}, false
	}
	if !upstream.allows(requestedModel) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "requested model is not configured", "code": "model_not_configured"})
		return chatRequest{}, false
	}
	if int64(len(body)) > s.config.Budget.MaxInputTokenReservation || int64(requestedTokens) > s.config.Budget.MaxOutputTokenReservation {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "requested token bound exceeds the configured reservation", "code": "model_token_bound_exceeded"})
		return chatRequest{}, false
	}
	return chatRequest{caller: caller, body: body, requestedModel: requestedModel, requestedTokens: requestedTokens, scope: scope, upstream: upstream, frozenMatches: scope.Kind != "document" || upstream.matchesFrozen(scope)}, true
}

func (s *Service) runChatAttempts(w http.ResponseWriter, ctx context.Context, request chatRequest, probe circuitProbe) {
	for attempt := 1; attempt <= s.config.Retries+1; attempt++ {
		if !s.runChatAttempt(w, ctx, request, probe, attempt) {
			return
		}
	}
}

func (s *Service) runChatAttempt(w http.ResponseWriter, ctx context.Context, request chatRequest, probe circuitProbe, attempt int) bool {
	lease, ok := s.reserveChatAttempt(w, ctx, request, attempt)
	if !ok {
		return false
	}
	if !request.frozenMatches {
		if err := s.finishChatAttempt(lease, "failed", "model_configuration_changed", nil, true); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "model usage could not be recorded", "code": "model_usage_unavailable"})
			return false
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": "model configuration no longer matches the configured endpoint", "code": "model_configuration_changed"})
		return false
	}
	response, status, usage, requestErr, retryable, preDispatch := s.dispatchUpstream(ctx, request.upstream, request.requestedModel, request.body)
	modelStatus, errorCode := chatAttemptResult(requestErr, status)
	return s.settleAndRespondChatAttempt(w, ctx, request, probe, attempt, lease, response, status, usage, requestErr, retryable, preDispatch, modelStatus, errorCode)
}

func (s *Service) reserveChatAttempt(w http.ResponseWriter, ctx context.Context, request chatRequest, attempt int) (store.ModelCallLease, bool) {
	identity := model.ConfigurationIDFor(request.upstream.ProviderURL, request.upstream.ChatModel, request.upstream.OCRModel)
	lease, err := s.store.ReserveModelChatCall(ctx, request.caller, request.scope, request.requestedModel, attempt,
		int64(len(request.body)), int64(request.requestedTokens), s.config.Budget, identity)
	if errors.Is(err, store.ErrModelConfigurationChanged) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "frozen model configuration no longer matches the configured endpoint", "code": "model_configuration_changed"})
		return store.ModelCallLease{}, false
	}
	if err != nil {
		writeReservationError(w, err, modelReservationErrors)
		return store.ModelCallLease{}, false
	}
	return lease, true
}

func (s *Service) settleAndRespondChatAttempt(w http.ResponseWriter, ctx context.Context, request chatRequest, probe circuitProbe, attempt int, lease store.ModelCallLease, response []byte, status int, usage *model.TokenUsage, requestErr error, retryable, preDispatch bool, modelStatus, errorCode string) bool {
	if err := s.finishChatAttempt(lease, modelStatus, errorCode, usage, preDispatch); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "model usage could not be recorded", "code": "model_usage_unavailable"})
		return false
	}
	return s.respondToChatAttempt(w, ctx, request, probe, attempt, response, status, requestErr, retryable)
}

func chatAttemptResult(requestErr error, status int) (string, string) {
	if requestErr == nil && status >= 200 && status < 300 {
		return "completed", ""
	}
	return "failed", requestErrCode(requestErr, status)
}

func (s *Service) finishChatAttempt(lease store.ModelCallLease, status, errorCode string, usage *model.TokenUsage, preDispatch bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.store.FinishModelCall(ctx, lease, store.ModelCallResult{
		Status: status, ErrorCode: errorCode, Usage: usage, OccurredAt: time.Now().UTC(), PreDispatchFailure: preDispatch,
	})
}

func (s *Service) respondToChatAttempt(w http.ResponseWriter, ctx context.Context, request chatRequest, probe circuitProbe, attempt int, response []byte, status int, requestErr error, retryable bool) bool {
	if requestErr != nil {
		return s.respondToChatError(w, probe, attempt, requestErr, retryable)
	}
	return s.respondToChatStatus(w, ctx, request, probe, attempt, response, status)
}

func (s *Service) respondToChatStatus(w http.ResponseWriter, ctx context.Context, request chatRequest, probe circuitProbe, attempt int, response []byte, status int) bool {
	if status >= 500 && attempt <= s.config.Retries {
		s.circuit.record(probe, false, true, time.Now())
		return true
	}
	s.circuit.record(probe, status >= 200 && status < 300, status >= 500, time.Now())
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "model provider request failed", "code": "model_provider_failed"})
		return false
	}
	s.writeAuthorizedChatResponse(w, ctx, request, status, response)
	return false
}

func (s *Service) writeAuthorizedChatResponse(w http.ResponseWriter, ctx context.Context, request chatRequest, status int, response []byte) {
	if err := s.store.ReauthorizeModelScope(ctx, request.caller, request.scope); err != nil {
		writeChatReauthorizationError(w, err)
		return
	}
	writeChatResponse(w, s.config.Mode, status, response)
}

func (s *Service) respondToChatError(w http.ResponseWriter, probe circuitProbe, attempt int, err error, retryable bool) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		s.circuit.record(probe, false, true, time.Now())
		writeModelContextError(w, err)
		return false
	}
	s.circuit.record(probe, false, true, time.Now())
	if retryable && attempt <= s.config.Retries {
		return true
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "model provider request failed", "code": "model_provider_failed"})
	return false
}

func writeChatReauthorizationError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrModelScopeUnavailable) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "current model scope is unavailable", "code": "model_scope_unavailable"})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "model scope could not be rechecked", "code": "model_scope_unavailable"})
}

func writeChatResponse(w http.ResponseWriter, mode string, status int, response []byte) {
	w.Header().Set("Content-Type", "application/json")
	if mode == "fixture" {
		w.Header().Set("X-Model-Backend", "local-fixture")
	}
	w.WriteHeader(status)
	_, _ = w.Write(response)
}

type reservationErrorMessages struct {
	budget          string
	budgetCode      string
	unavailable     string
	unavailableCode string
}

var modelReservationErrors = reservationErrorMessages{
	budget: "model call quota is exhausted", budgetCode: "model_budget_exhausted",
	unavailable: "model call could not be reserved", unavailableCode: "model_budget_unavailable",
}
var jevReservationErrors = reservationErrorMessages{
	budget: "advisory call quota is exhausted", budgetCode: "jev_budget_exhausted",
	unavailable: "advisory call could not be reserved", unavailableCode: "jev_budget_unavailable",
}

func writeReservationError(w http.ResponseWriter, err error, messages reservationErrorMessages) {
	switch {
	case errors.Is(err, store.ErrModelBudgetExceeded):
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": messages.budget, "code": messages.budgetCode})
	case errors.Is(err, store.ErrModelScopeUnavailable):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "current model scope is unavailable", "code": "model_scope_unavailable"})
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": messages.unavailable, "code": messages.unavailableCode})
	}
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	w.WriteHeader(http.StatusMethodNotAllowed)
	return false
}

func (s *Service) authenticate(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("X-Contract-Service-Key")
	if len(values) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "service authentication required", "code": "service_unauthenticated"})
		return "", false
	}
	credential := []byte(values[0])
	workerOK := subtle.ConstantTimeCompare(credential, []byte(s.config.WorkerCredential)) == 1
	documentOK := subtle.ConstantTimeCompare(credential, []byte(s.config.DocumentCredential)) == 1
	if workerOK == documentOK {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "service authentication required", "code": "service_unauthenticated"})
		return "", false
	}
	if workerOK {
		return "worker", true
	}
	return "document", true
}

func decodeRequest(w http.ResponseWriter, r *http.Request) ([]byte, string, int, model.DelegatedScope, error) {
	body := http.MaxBytesReader(w, r.Body, maxRequestBytes)
	defer body.Close()
	// A declared oversized request is already invalid; do not buffer its body.
	// Unknown-length requests still pass through MaxBytesReader below.
	if r.ContentLength > maxRequestBytes {
		return nil, "", 0, model.DelegatedScope{}, errors.New("request body is invalid")
	}
	payload, err := io.ReadAll(body)
	if err != nil || len(payload) == 0 {
		return nil, "", 0, model.DelegatedScope{}, errors.New("request body is invalid")
	}
	var request struct {
		Model     string `json:"model"`
		Stream    bool   `json:"stream"`
		MaxTokens int    `json:"max_tokens"`
	}
	if err := json.Unmarshal(payload, &request); err != nil || strings.TrimSpace(request.Model) == "" || request.Stream || request.MaxTokens < 1 {
		return nil, "", 0, model.DelegatedScope{}, errors.New("request model, stream mode, or max_tokens is invalid")
	}
	scope, err := decodeDelegatedScope(r)
	if err != nil {
		return nil, "", 0, model.DelegatedScope{}, err
	}
	return payload, strings.TrimSpace(request.Model), request.MaxTokens, scope, nil
}

func decodeDelegatedScope(r *http.Request) (model.DelegatedScope, error) {
	values := r.Header.Values("X-Audit-Execution-Scope")
	if len(values) != 1 || len(values[0]) > maxScopeBytes {
		return model.DelegatedScope{}, errors.New("delegated model scope is missing")
	}
	var scope model.DelegatedScope
	decoder := json.NewDecoder(strings.NewReader(values[0]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&scope); err != nil || decoder.Decode(new(any)) != io.EOF {
		return model.DelegatedScope{}, errors.New("delegated model scope is invalid")
	}
	return scope, nil
}

func (s *Service) dispatch(ctx context.Context, modelID string, body []byte) ([]byte, int, *model.TokenUsage, error, bool, bool) {
	upstream, err := s.upstreamFor(ctx)
	if err != nil {
		return nil, http.StatusServiceUnavailable, nil, err, false, true
	}
	return s.dispatchUpstream(ctx, upstream, modelID, body)
}

func (s *Service) dispatchUpstream(ctx context.Context, upstream upstreamConfig, modelID string, body []byte) ([]byte, int, *model.TokenUsage, error, bool, bool) {
	if s.config.Mode == "fixture" {
		return s.fixtureResponse(modelID, body), http.StatusOK, nil, nil, false, false
	}
	if upstream.BaseURL == "" {
		return nil, http.StatusServiceUnavailable, nil, errors.New("model upstream is not configured"), false, true
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.BaseURL+"/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		return nil, http.StatusBadGateway, nil, err, false, true
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if upstream.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+upstream.APIKey)
	}
	response, err := s.clientFor(upstream).Do(request)
	if err != nil {
		return nil, http.StatusBadGateway, nil, err, retryableTransport(err, ctx), false
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, http.StatusBadGateway, nil, err, true, false
	}
	if len(responseBody) > maxResponseBytes {
		return nil, http.StatusBadGateway, nil, errors.New("model provider response exceeds the size limit"), false, false
	}
	return responseBody, response.StatusCode, reportedUsage(responseBody), nil, response.StatusCode >= 500, false
}

func (s *Service) fixtureResponse(modelID string, body []byte) []byte {
	var request struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &request)
	content := s.config.FixtureChatContent
	if model.GatewayID(modelID) == model.GatewayID(s.config.OCRModel) {
		content = s.config.FixtureOCRContent
	} else if strings.TrimSpace(content) == "" {
		if derived, ok := fixturePlannerAnswer(body); ok {
			content = derived
		} else if derived, ok := fixtureWarrantyAnswer(body); ok {
			content = derived
		}
	}
	digest := sha256.Sum256(body)
	return mustJSON(map[string]any{
		"id": "chatcmpl-fixture-" + hex.EncodeToString(digest[:8]), "object": "chat.completion", "created": 0,
		"model":   "local-fixture/" + model.GatewayID(request.Model),
		"choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": content}, "finish_reason": "stop"}},
	})
}

type fixturePlannerInput struct {
	Scope []string `json:"scope"`
	Gaps  []struct {
		ID         string `json:"id"`
		Domain     string `json:"domain"`
		Capability string `json:"capability"`
	} `json:"gaps"`
	Tools []struct {
		ID           string   `json:"id"`
		Domain       string   `json:"domain"`
		Capabilities []string `json:"capabilities"`
	} `json:"tools"`
	PreviouslyCalled []struct {
		ToolID               string `json:"tool_id"`
		GapID                string `json:"gap_id"`
		NewEvidenceAvailable bool   `json:"new_evidence_available"`
	} `json:"previously_called"`
}

type fixturePlanCall struct {
	ToolID string `json:"tool_id"`
	Domain string `json:"domain"`
	GapID  string `json:"gap_id"`
	Reason string `json:"reason"`
}

func fixturePlannerAnswer(body []byte) (string, bool) {
	messages, ok := decodeFixturePlannerMessages(body)
	if !ok {
		return "", false
	}
	var input fixturePlannerInput
	if json.Unmarshal([]byte(messages[1].Content), &input) != nil || !validFixturePlannerInput(input) {
		return "", false
	}
	calls, ok := buildFixturePlannerCalls(input)
	if !ok {
		return "", false
	}
	encoded, err := json.Marshal(struct {
		Calls []fixturePlanCall `json:"calls"`
	}{Calls: calls})
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func decodeFixturePlannerMessages(body []byte) ([]struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}, bool) {
	if len(body) == 0 || len(body) > 256<<10 {
		return nil, false
	}
	var request struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &request) != nil || len(request.Messages) != 2 || request.Messages[0].Role != "system" || request.Messages[1].Role != "user" {
		return nil, false
	}
	return request.Messages, true
}

func validFixturePlannerInput(input fixturePlannerInput) bool {
	return len(input.Scope) > 0 && len(input.Scope) <= 5 && len(input.Gaps) > 0 && len(input.Gaps) <= 16 &&
		len(input.Tools) > 0 && len(input.Tools) <= 32 && len(input.PreviouslyCalled) <= 128
}

func buildFixturePlannerCalls(input fixturePlannerInput) ([]fixturePlanCall, bool) {
	called := make(map[string]bool, len(input.PreviouslyCalled))
	for _, prior := range input.PreviouslyCalled {
		called[prior.GapID+"\x00"+prior.ToolID] = !prior.NewEvidenceAvailable
	}
	calls := make([]fixturePlanCall, 0, len(input.Gaps))
	for _, gap := range input.Gaps {
		if gap.ID == "" || gap.Domain == "" || gap.Capability == "" {
			return nil, false
		}
		if call, ok := fixtureCallForGap(gap.ID, gap.Domain, gap.Capability, input.Tools, called); ok {
			calls = append(calls, call)
		}
	}
	return calls, true
}

func fixtureCallForGap(gapID, domain, capability string, tools []struct {
	ID           string   `json:"id"`
	Domain       string   `json:"domain"`
	Capabilities []string `json:"capabilities"`
}, called map[string]bool) (fixturePlanCall, bool) {
	for _, tool := range tools {
		if tool.ID == "" || tool.Domain != domain || called[gapID+"\x00"+tool.ID] {
			continue
		}
		if hasFixtureCapability(tool.Capabilities, capability) {
			return fixturePlanCall{ToolID: tool.ID, Domain: domain, GapID: gapID,
				Reason: "fixture selected a registered tool for this requested gap"}, true
		}
	}
	return fixturePlanCall{}, false
}

func hasFixtureCapability(capabilities []string, wanted string) bool {
	for _, capability := range capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

// fixtureWarrantyAnswer answers a review prompt with the evidence lines that
// prompt already carries.
//
// The fixture is a deterministic stub, not a model: it never invents a
// citation, it re-uses the material spans the prompt itself embeds, so the
// deterministic citation verification and location stages still exercise the
// real pipeline. An explicit MODEL_GATEWAY_FIXTURE_CHAT_CONTENT replaces this
// answer; a prompt without material spans keeps it too.
type fixtureWarrantyMessage struct {
	Content string `json:"content"`
}

func fixtureWarrantyAnswer(body []byte) (string, bool) {
	var request struct {
		Messages []fixtureWarrantyMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return "", false
	}
	quotes := fixtureWarrantyQuotes(request.Messages)
	if len(quotes) == 0 {
		return "", false
	}
	answer, err := json.Marshal(map[string]any{
		"claim": "采购文件与合同草稿的质保期要求不一致", "relation": "conflict",
		"disposition": "confirm_risk", "quotes": quotes,
	})
	if err != nil {
		return "", false
	}
	return string(answer), true
}

func fixtureWarrantyQuotes(messages []fixtureWarrantyMessage) []map[string]string {
	quotes := make([]map[string]string, 0, len(messages))
	var documentID string
	for _, message := range messages {
		for _, line := range strings.Split(message.Content, "\n") {
			if _, rest, ok := strings.Cut(line, "document_id="); ok {
				documentID, _, _ = strings.Cut(strings.TrimSpace(rest), " ")
				continue
			}
			text, ok := strings.CutPrefix(line, "正文：")
			if !ok || documentID == "" {
				continue
			}
			if text = strings.TrimSpace(text); text != "" {
				quotes = append(quotes, map[string]string{"document_id": documentID, "quote": text})
			}
		}
	}
	return quotes
}

func (s *Service) catalog(ctx context.Context) ([]string, error) {
	upstream, err := s.upstreamFor(ctx)
	if err != nil {
		return nil, err
	}
	if s.config.Mode == "fixture" {
		return []string{model.GatewayID(upstream.ChatModel), model.GatewayID(upstream.OCRModel)}, nil
	}
	key := catalogCacheKey(upstream)
	s.catalogMu.Lock()
	if time.Since(s.catalogAt) < 5*time.Second && s.catalogIDs != nil && s.catalogKey == key {
		ids := append([]string(nil), s.catalogIDs...)
		s.catalogMu.Unlock()
		return ids, nil
	}
	s.catalogMu.Unlock()
	ids, err := s.fetchCatalogFrom(ctx, upstream)
	if err != nil {
		return nil, err
	}
	s.catalogMu.Lock()
	s.catalogIDs = append([]string(nil), ids...)
	s.catalogAt = time.Now()
	s.catalogKey = key
	s.catalogMu.Unlock()
	return ids, nil
}

func catalogCacheKey(upstream upstreamConfig) string {
	return strconv.FormatInt(upstream.Revision, 10) + "|" + upstream.BaseURL
}

func (s *Service) fetchCatalog(ctx context.Context) ([]string, error) {
	upstream, err := s.upstreamFor(ctx)
	if err != nil {
		return nil, err
	}
	return s.fetchCatalogFrom(ctx, upstream)
}

func (s *Service) fetchCatalogFrom(ctx context.Context, upstream upstreamConfig) ([]string, error) {
	response, err := s.catalogResponse(ctx, upstream)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return decodeCatalog(response.Body)
}

func (s *Service) catalogResponse(ctx context.Context, upstream upstreamConfig) (*http.Response, error) {
	if upstream.BaseURL == "" {
		return nil, errors.New("model upstream is not configured")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if upstream.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+upstream.APIKey)
	}
	response, err := s.clientFor(upstream).Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("model catalog returned HTTP %d", response.StatusCode)
	}
	return response, nil
}

func decodeCatalog(body io.Reader) ([]string, error) {
	var catalog struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(body, maxResponseBytes+1))
	if err := decoder.Decode(&catalog); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(catalog.Data))
	for _, item := range catalog.Data {
		if id := strings.TrimSpace(item.ID); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("model catalog is empty")
	}
	return ids, nil
}

func containsModel(ids []string, configured string) bool {
	want := model.GatewayID(configured)
	for _, id := range ids {
		if model.GatewayID(id) == want {
			return true
		}
	}
	return false
}

func reportedUsage(payload []byte) *model.TokenUsage {
	var response struct {
		Usage *struct {
			Prompt     *int64 `json:"prompt_tokens"`
			Completion *int64 `json:"completion_tokens"`
			Total      *int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(payload, &response) != nil || response.Usage == nil {
		return nil
	}
	return &model.TokenUsage{Source: "openai_response.usage", InputTokens: response.Usage.Prompt, OutputTokens: response.Usage.Completion, TotalTokens: response.Usage.Total}
}

func requestErrCode(err error, status int) string {
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "provider_timeout"
		}
		return "provider_transport_error"
	}
	return fmt.Sprintf("provider_http_%d", status)
}

// clientFor pins provider calls to the right outbound client. A stored,
// operator-configured upstream (revision >= 1) is guarded: its connections
// land only on policy-verified addresses and never follow redirects. The
// deployment-configured upstream keeps the default client, because
// process-to-process environment URLs are not operator-supplied endpoints.
func (s *Service) clientFor(upstream upstreamConfig) *http.Client {
	if upstream.Revision > 0 && s.guardedClient != nil {
		return s.guardedClient
	}
	return s.client
}

func retryableTransport(err error, ctx context.Context) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, llmendpoint.ErrEndpointForbidden) {
		return false
	}
	return true
}

func writeModelContextError(w http.ResponseWriter, err error) {
	writeContextError(w, err, "model", "model_timeout", "model_cancelled")
}

func writeContextError(w http.ResponseWriter, err error, service, timeoutCode, cancelledCode string) {
	if errors.Is(err, context.DeadlineExceeded) {
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": service + " request timed out", "code": timeoutCode})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": service + " request cancelled", "code": cancelledCode})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func mustJSON(value any) []byte {
	payload, _ := json.Marshal(value)
	return payload
}

type circuitProbe uint64

type circuitBreaker struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration
	failures  int
	openedAt  time.Time
	open      bool
	nextProbe circuitProbe
	probing   circuitProbe
}

func (c *circuitBreaker) allow(now time.Time) (bool, circuitProbe) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.open {
		return true, 0
	}
	if now.Sub(c.openedAt) < c.cooldown || c.probing != 0 {
		return false, 0
	}
	c.nextProbe++
	if c.nextProbe == 0 {
		c.nextProbe++
	}
	c.probing = c.nextProbe
	return true, c.probing
}

func (c *circuitBreaker) release(probe circuitProbe) {
	if probe == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.probing == probe {
		c.probing = 0
	}
}

func (c *circuitBreaker) record(probe circuitProbe, success, failure bool, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if success {
		c.failures, c.open, c.probing = 0, false, 0
		return
	}
	if !failure {
		if c.probing == probe {
			c.probing = 0
		}
		return
	}
	c.failures++
	if probe != 0 || c.failures >= c.threshold {
		c.open, c.openedAt = true, now
		if probe == 0 || c.probing == probe {
			c.probing = 0
		}
	}
}
