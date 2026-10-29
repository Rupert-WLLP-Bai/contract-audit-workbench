package modelgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/agentflow"
	"contractaudit/internal/jev"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

const (
	jevMaxConcurrency  = 4
	jevQueueCapacity   = 16
	maxJevRequestBytes = 64 << 10
)

var pinnedJevModelPattern = regexp.MustCompile(`^jev-[0-9]+\.[0-9]+\.[0-9]+$`)

// JevConfig is the gateway's provider identity. APIKey is never returned by a
// health or status surface and is never forwarded as an audit header.
type JevConfig struct {
	Mode            string
	BaseURL         string
	APIKey          string
	Model           string
	Features        []string
	Egress          string
	Timeout         time.Duration
	MaxOutputTokens int64
	Problem         string
}

func jevConfigFromEnv() JevConfig {
	config := JevConfig{
		Mode:    strings.ToLower(strings.TrimSpace(os.Getenv("TYPESAFE_JEV_MODE"))),
		BaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("TYPESAFE_JEV_BASE_URL")), "/"),
		APIKey:  strings.TrimSpace(os.Getenv("TYPESAFE_API_KEY")),
		Model:   strings.TrimSpace(os.Getenv("TYPESAFE_JEV_MODEL")),
		Egress:  strings.ToLower(strings.TrimSpace(os.Getenv("TYPESAFE_JEV_EGRESS"))),
		Timeout: agentflow.DefaultAdvisoryTimeout, MaxOutputTokens: 2048,
	}
	if config.Mode == "" {
		config.Mode = agentflow.AdvisoryModeOff
	}
	if config.BaseURL == "" {
		config.BaseURL = jev.DefaultBaseURL
	}
	if config.Egress == "" {
		config.Egress = agentflow.AdvisoryEgressMinimized
	}
	if config.Egress != agentflow.AdvisoryEgressMinimized {
		config.Problem = "unsupported_egress"
	}
	config.Features, config.Problem = configuredJevFeatures(os.Getenv("TYPESAFE_JEV_FEATURES"), config.Problem)
	applyJevTimingOverrides(&config)
	return config
}

func configuredJevFeatures(rawValue, problem string) ([]string, string) {
	rawValue = strings.TrimSpace(rawValue)
	if rawValue == "" {
		return []string{agentflow.AdvisoryFeatureKnowledgeRerank}, problem
	}
	seen := map[string]bool{}
	features := make([]string, 0)
	for _, raw := range strings.Split(rawValue, ",") {
		feature := strings.ToLower(strings.TrimSpace(raw))
		if feature == "" {
			continue
		}
		if feature != agentflow.AdvisoryFeatureKnowledgeRerank {
			problem = "unsupported_feature"
			continue
		}
		if !seen[feature] {
			seen[feature] = true
			features = append(features, feature)
		}
	}
	sort.Strings(features)
	if len(features) == 0 {
		problem = "unsupported_feature"
	}
	return features, problem
}

func applyJevTimingOverrides(config *JevConfig) {
	if raw := strings.TrimSpace(os.Getenv("TYPESAFE_JEV_TIMEOUT")); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value < agentflow.MinAdvisoryTimeout || value > agentflow.MaxAdvisoryTimeout {
			config.Problem = "invalid_timeout"
		} else {
			config.Timeout = value
		}
	}
	if raw := strings.TrimSpace(os.Getenv("TYPESAFE_JEV_MAX_OUTPUT_TOKENS")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 || value > 8192 {
			config.Problem = "invalid_max_output_tokens"
		} else {
			config.MaxOutputTokens = value
		}
	}
}

// normalizeJev keeps an advisory misconfiguration local to the Jev route. It
// never blocks chat/OCR readiness and never makes the gateway fail to start.
func normalizeJev(config *JevConfig, budget store.ModelBudgetLimits) (status, reason string, client *jev.Client) {
	if config.Mode == agentflow.AdvisoryModeOff {
		return "off", "disabled", nil
	}
	if config.Problem != "" {
		return "unavailable", config.Problem, nil
	}
	if reason := jevConfigurationProblem(config, budget); reason != "" {
		return "unavailable", reason, nil
	}
	switch config.Mode {
	case agentflow.AdvisoryModeFixture:
		return normalizeFixtureJev(config)
	case agentflow.AdvisoryModeAdvisory:
		return normalizeRemoteJev(config)
	}
	return "unavailable", "invalid_mode", nil
}

func jevConfigurationProblem(config *JevConfig, budget store.ModelBudgetLimits) string {
	if config.Mode != agentflow.AdvisoryModeAdvisory && config.Mode != agentflow.AdvisoryModeFixture {
		return "invalid_mode"
	}
	if config.Egress != agentflow.AdvisoryEgressMinimized {
		return "unsupported_egress"
	}
	if !supportsConfiguredFeatures(config.Features) {
		return "unsupported_feature"
	}
	if config.Timeout < agentflow.MinAdvisoryTimeout || config.Timeout > agentflow.MaxAdvisoryTimeout {
		return "invalid_timeout"
	}
	if config.MaxOutputTokens < 1 || config.MaxOutputTokens > budget.MaxOutputTokenReservation {
		return "invalid_max_output_tokens"
	}
	return ""
}

func supportsConfiguredFeatures(features []string) bool {
	if len(features) == 0 {
		return false
	}
	for _, feature := range features {
		if feature != agentflow.AdvisoryFeatureKnowledgeRerank {
			return false
		}
	}
	return true
}

func normalizeFixtureJev(config *JevConfig) (string, string, *jev.Client) {
	if config.Model == "" {
		config.Model = agentflow.AdvisoryFixtureModel
	}
	if config.Model != agentflow.AdvisoryFixtureModel {
		return "unavailable", "fixture_model_mismatch", nil
	}
	environment := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	if environment != "development" && environment != "dev" && environment != "test" {
		return "unavailable", "fixture_not_allowed", nil
	}
	return "fixture", "", nil
}

func normalizeRemoteJev(config *JevConfig) (string, string, *jev.Client) {
	if !pinnedJevModelPattern.MatchString(config.Model) {
		if config.Model == "" {
			return "unavailable", "model_required", nil
		}
		return "unavailable", "model_alias_not_allowed", nil
	}
	if config.APIKey == "" {
		return "unconfigured", "api_key_missing", nil
	}
	if len(config.APIKey) < 20 {
		return "unavailable", "api_key_invalid", nil
	}
	client, err := jev.New(jev.Config{BaseURL: config.BaseURL, APIKey: config.APIKey, Model: config.Model, Timeout: config.Timeout})
	if err != nil {
		return "unavailable", "endpoint_or_model_invalid", nil
	}
	return "configured", "", client
}

func (s *Service) JevStatus() (mode, status, modelID string, features []string, egress, reason string) {
	return s.jev.Mode, s.jevStatus, s.jev.Model, append([]string(nil), s.jev.Features...), s.jev.Egress, s.jevReason
}

func (s *Service) jevHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	mode, status, modelID, features, egress, reason := s.JevStatus()
	body := map[string]any{
		"mode": mode, "status": status, "model": modelID, "features": features, "egress": egress,
	}
	if reason != "" {
		body["reason"] = reason
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Service) systemOne(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	caller, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if caller != "worker" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the worker may use the advisory route", "code": "jev_worker_identity_required"})
		return
	}
	s.serveSystemOne(w, r, caller)
}

func (s *Service) serveSystemOne(w http.ResponseWriter, r *http.Request, caller string) {
	if !s.jevAvailable(w) {
		return
	}
	request, ok := s.readSystemOneRequest(w, r)
	if !ok {
		return
	}
	s.executeSystemOne(w, r, caller, request)
}

func (s *Service) jevAvailable(w http.ResponseWriter) bool {
	switch s.jevStatus {
	case "off":
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Jev is disabled", "code": "jev_disabled"})
		return false
	case "unconfigured":
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Jev is enabled but not configured", "code": "jev_unconfigured"})
		return false
	case "unavailable":
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Jev is unavailable", "code": "jev_unavailable"})
		return false
	case "configured", "fixture":
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Jev is unavailable", "code": "jev_unavailable"})
		return false
	}
	return true
}

type systemOneRequest struct {
	body      []byte
	state     string
	questions map[string]jev.NoulQuestion
	scope     model.DelegatedScope
}

func (s *Service) readSystemOneRequest(w http.ResponseWriter, r *http.Request) (systemOneRequest, bool) {
	if !s.validateSystemOneHeaders(w, r) {
		return systemOneRequest{}, false
	}
	body, ok := readSystemOneBody(w, r)
	if !ok {
		return systemOneRequest{}, false
	}
	state, questions, ok := decodeSystemOnePayload(w, body, s.jev.Model)
	if !ok {
		return systemOneRequest{}, false
	}
	scope, err := decodeDelegatedScope(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "delegated model scope is missing", "code": "jev_scope_missing"})
		return systemOneRequest{}, false
	}
	if int64(len(body)) > s.config.Budget.MaxInputTokenReservation || s.jev.MaxOutputTokens > s.config.Budget.MaxOutputTokenReservation {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "advisory token bound exceeds the configured reservation", "code": "jev_token_bound_exceeded"})
		return systemOneRequest{}, false
	}
	return systemOneRequest{body: body, state: state, questions: questions, scope: scope}, true
}

func decodeSystemOnePayload(w http.ResponseWriter, body []byte, configuredModel string) (string, map[string]jev.NoulQuestion, bool) {
	requestModel, state, questions, err := decodeSystemOneRequest(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "advisory request body is invalid", "code": "jev_request_invalid"})
		return "", nil, false
	}
	if requestModel != configuredModel {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "requested Jev model is not configured", "code": "jev_model_mismatch"})
		return "", nil, false
	}
	if jev.ValidateNoulInput(state, questions) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "advisory state or questions exceed their bound", "code": "jev_request_invalid"})
		return "", nil, false
	}
	return state, questions, true
}

func (s *Service) validateSystemOneHeaders(w http.ResponseWriter, r *http.Request) bool {
	feature, ok := singleHeader(r, "X-Contract-Jev-Feature")
	if !ok || !s.jevSupports(feature) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Jev feature is missing or not configured", "code": "jev_feature_invalid"})
		return false
	}
	classification, ok := singleHeader(r, "X-Contract-Data-Classification")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "data classification is required", "code": "jev_classification_missing"})
		return false
	}
	if classification == agentflow.AdvisoryEgressApprovedText {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "approved text egress is not enabled", "code": "jev_egress_not_approved"})
		return false
	}
	if classification != s.jev.Egress {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "data classification does not match the gateway", "code": "jev_classification_mismatch"})
		return false
	}
	return true
}

func readSystemOneBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	bodyReader := http.MaxBytesReader(w, r.Body, maxJevRequestBytes)
	defer bodyReader.Close()
	body, err := io.ReadAll(bodyReader)
	if err != nil || len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "advisory request body is invalid", "code": "jev_request_invalid"})
		return nil, false
	}
	return body, true
}

func (s *Service) executeSystemOne(w http.ResponseWriter, r *http.Request, caller string, request systemOneRequest) {
	ctx, release, ok := s.acquireSystemOneCapacity(w, r)
	if !ok {
		return
	}
	defer release()
	allowed, probe := s.jevCircuit.allow(time.Now())
	if !allowed {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Jev provider circuit is open", "code": "jev_circuit_open"})
		return
	}
	defer s.jevCircuit.release(probe)
	s.completeSystemOne(w, ctx, caller, request, probe)
}

func (s *Service) acquireSystemOneCapacity(w http.ResponseWriter, r *http.Request) (context.Context, func(), bool) {
	select {
	case s.jevQueue <- struct{}{}:
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "advisory gateway capacity is full", "code": "jev_queue_full"})
		return nil, nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.jev.Timeout)
	select {
	case s.jevSlots <- struct{}{}:
		return ctx, func() { <-s.jevSlots; <-s.jevQueue; cancel() }, true
	case <-ctx.Done():
		cancel()
		<-s.jevQueue
		writeJevContextError(w, ctx.Err())
		return nil, nil, false
	}
}

func (s *Service) completeSystemOne(w http.ResponseWriter, ctx context.Context, caller string, request systemOneRequest, probe circuitProbe) {
	lease, err := s.reserveSystemOne(ctx, caller, request)
	if err != nil {
		writeReservationError(w, err, jevReservationErrors)
		return
	}
	s.executeReservedSystemOne(w, ctx, caller, request, probe, lease)
}

func (s *Service) executeReservedSystemOne(w http.ResponseWriter, ctx context.Context, caller string, request systemOneRequest, probe circuitProbe, lease store.ModelCallLease) {
	response, usage, requestErr := s.dispatchJev(ctx, request.state, request.questions)
	if err := s.settleSystemOne(lease, usage, requestErr); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "advisory usage could not be recorded", "code": "jev_usage_unavailable"})
		return
	}
	s.respondSystemOne(w, ctx, caller, request, probe, response, requestErr)
}

func (s *Service) respondSystemOne(w http.ResponseWriter, ctx context.Context, caller string, request systemOneRequest, probe circuitProbe, response []byte, requestErr error) {
	if requestErr != nil {
		s.writeSystemOneFailure(w, requestErr, probe)
		return
	}
	s.jevCircuit.record(probe, true, false, time.Now())
	s.writeAuthorizedSystemOneResponse(w, ctx, caller, request, response)
}

func (s *Service) writeAuthorizedSystemOneResponse(w http.ResponseWriter, ctx context.Context, caller string, request systemOneRequest, response []byte) {
	if err := s.store.ReauthorizeModelScope(ctx, caller, request.scope); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "current model scope is unavailable", "code": "model_scope_unavailable"})
		return
	}
	writeSystemOneResponse(w, s.jevStatus, response)
}

func (s *Service) settleSystemOne(lease store.ModelCallLease, usage *model.TokenUsage, requestErr error) error {
	modelStatus, errorCode := "completed", ""
	if requestErr != nil {
		modelStatus, errorCode = "failed", jevErrorCode(requestErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.store.FinishModelCall(ctx, lease, store.ModelCallResult{
		Status: modelStatus, ErrorCode: errorCode, Usage: usage, OccurredAt: time.Now().UTC(),
	})
}

func (s *Service) writeSystemOneFailure(w http.ResponseWriter, err error, probe circuitProbe) {
	s.jevCircuit.record(probe, false, true, time.Now())
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		writeJevContextError(w, err)
		return
	}
	code := "jev_provider_failed"
	if errors.Is(err, errJevInvalidResponse) {
		code = "jev_invalid_response"
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Jev provider request failed", "code": code})
}

func writeSystemOneResponse(w http.ResponseWriter, status string, response []byte) {
	w.Header().Set("Content-Type", "application/json")
	if status == "fixture" {
		w.Header().Set("X-Model-Backend", "local-fixture")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(response)
}

func (s *Service) reserveSystemOne(ctx context.Context, caller string, request systemOneRequest) (store.ModelCallLease, error) {
	return s.store.ReserveModelCall(ctx, caller, request.scope, "typesafe/"+s.jev.Model, 1,
		int64(len(request.body)), s.jev.MaxOutputTokens, s.config.Budget)
}

func singleHeader(r *http.Request, name string) (string, bool) {
	values := r.Header.Values(name)
	if len(values) != 1 {
		return "", false
	}
	value := strings.TrimSpace(values[0])
	return value, value != ""
}

func (s *Service) jevSupports(feature string) bool {
	for _, enabled := range s.jev.Features {
		if enabled == feature {
			return true
		}
	}
	return false
}

func decodeSystemOneRequest(body []byte) (string, string, map[string]jev.NoulQuestion, error) {
	var request struct {
		Model     string `json:"model"`
		State     string `json:"state"`
		Questions map[string]struct {
			Type         string `json:"type"`
			Instructions string `json:"instructions"`
			Criteria     struct {
				True  string `json:"true"`
				False string `json:"false"`
			} `json:"criteria"`
		} `json:"questions"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.Decode(new(any)) != io.EOF {
		return "", "", nil, errors.New("advisory request is not valid json")
	}
	if strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.State) == "" ||
		len(request.Questions) == 0 || len(request.Questions) > jev.MaxQuestions {
		return "", "", nil, errors.New("advisory request identity is invalid")
	}
	questions := make(map[string]jev.NoulQuestion, len(request.Questions))
	for id, question := range request.Questions {
		if question.Type != "noul" {
			return "", "", nil, errors.New("only noul questions are supported")
		}
		questions[id] = jev.NoulQuestion{
			Instructions: question.Instructions,
			Criteria:     jev.NoulCriteria{True: question.Criteria.True, False: question.Criteria.False},
		}
	}
	return strings.TrimSpace(request.Model), request.State, questions, nil
}

var errJevInvalidResponse = errors.New("Jev response is invalid")

func (s *Service) dispatchJev(ctx context.Context, state string, questions map[string]jev.NoulQuestion) ([]byte, *model.TokenUsage, error) {
	if s.jevStatus == "fixture" {
		zero := int64(0)
		return fixtureJevResponse(s.jev.Model, questions), &model.TokenUsage{Source: "local_fixture", InputTokens: &zero, OutputTokens: &zero}, nil
	}
	return s.dispatchRemoteJev(ctx, state, questions)
}

func (s *Service) dispatchRemoteJev(ctx context.Context, state string, questions map[string]jev.NoulQuestion) ([]byte, *model.TokenUsage, error) {
	if s.jevClient == nil {
		return nil, nil, errors.New("Jev client is unavailable")
	}
	answers, meta, err := s.jevClient.AskNouls(ctx, state, questions)
	if err != nil {
		return nil, nil, err
	}
	if meta.Model != s.jev.Model {
		return nil, nil, errJevInvalidResponse
	}
	return encodeJevResponse(meta, answers), tokenUsageFromJev(meta.Usage), nil
}

// encodeJevResponse rebuilds the provider envelope from already-validated
// answers. The worker never sees a half-validated upstream payload, and the
// gateway response is bounded by the same question set it accepted.
func encodeJevResponse(meta jev.ResponseMeta, answers map[string]jev.NoulAnswer) []byte {
	encodedAnswers := make(map[string]any, len(answers))
	for id, answer := range answers {
		encodedAnswers[id] = map[string]any{"type": "noul", "noul": answer.Noul}
	}
	body := map[string]any{"model": meta.Model, "answers": encodedAnswers}
	usage := map[string]any{}
	if meta.Usage.InputTokens != nil {
		usage["input_tokens"] = *meta.Usage.InputTokens
	}
	if meta.Usage.OutputTokens != nil {
		usage["output_tokens"] = *meta.Usage.OutputTokens
	}
	if len(usage) > 0 {
		body["usage"] = usage
	}
	return mustJSON(body)
}

func fixtureJevResponse(modelID string, questions map[string]jev.NoulQuestion) []byte {
	ids := make([]string, 0, len(questions))
	for id := range questions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	answers := make(map[string]any, len(ids))
	for _, id := range ids {
		index := fixtureJevIndex(id)
		probability := 0.50
		switch index % 3 {
		case 0:
			probability = 0.82
		case 1:
			probability = 0.12
		}
		answers[id] = map[string]any{"type": "noul", "noul": probability}
	}
	return mustJSON(map[string]any{
		"model": modelID, "answers": answers,
		"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
	})
}

func fixtureJevIndex(questionID string) int {
	trimmed := strings.TrimSuffix(questionID, "_relevance")
	trimmed = strings.TrimPrefix(trimmed, "candidate_")
	index, err := strconv.Atoi(trimmed)
	if err != nil || index < 0 {
		return 0
	}
	return index
}

func tokenUsageFromJev(usage jev.Usage) *model.TokenUsage {
	if usage.InputTokens == nil && usage.OutputTokens == nil {
		return nil
	}
	result := &model.TokenUsage{Source: "typesafe_response.usage", InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens}
	if usage.InputTokens != nil && usage.OutputTokens != nil {
		total := *usage.InputTokens + *usage.OutputTokens
		result.TotalTokens = &total
	}
	return result
}

func jevErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, errJevInvalidResponse):
		return "invalid_response"
	default:
		return "provider_failed"
	}
}

func writeJevContextError(w http.ResponseWriter, err error) {
	writeContextError(w, err, "Jev", "jev_timeout", "jev_cancelled")
}
