package modelgateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/agentflow"
	"contractaudit/internal/jev"
	"contractaudit/internal/store"
)

func jevTestBudget() store.ModelBudgetLimits {
	return store.ModelBudgetLimits{MaxOutputTokenReservation: 4096}
}

func jevTestConfig() JevConfig {
	return JevConfig{
		Mode: agentflow.AdvisoryModeFixture, Model: agentflow.AdvisoryFixtureModel,
		Features: []string{agentflow.AdvisoryFeatureKnowledgeRerank},
		Egress:   agentflow.AdvisoryEgressMinimized, Timeout: 3 * time.Second, MaxOutputTokens: 1024,
	}
}

func TestNormalizeJevKeepsMisconfigurationLocalToTheAdvisoryRoute(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	tests := []struct {
		name   string
		config func() JevConfig
		status string
		reason string
	}{
		{"off", func() JevConfig { config := jevTestConfig(); config.Mode = agentflow.AdvisoryModeOff; return config }, "off", "disabled"},
		{"fixture", jevTestConfig, "fixture", ""},
		{"fixture model mismatch", func() JevConfig {
			config := jevTestConfig()
			config.Model = "jev-1.14.0"
			return config
		}, "unavailable", "fixture_model_mismatch"},
		{"alias is not a pinned model", func() JevConfig {
			config := jevTestConfig()
			config.Mode = agentflow.AdvisoryModeAdvisory
			config.Model = "jev-latest"
			config.APIKey = strings.Repeat("k", 32)
			return config
		}, "unavailable", "model_alias_not_allowed"},
		{"missing key", func() JevConfig {
			config := jevTestConfig()
			config.Mode = agentflow.AdvisoryModeAdvisory
			config.Model = "jev-1.14.0"
			return config
		}, "unconfigured", "api_key_missing"},
		{"configured", func() JevConfig {
			config := jevTestConfig()
			config.Mode = agentflow.AdvisoryModeAdvisory
			config.Model = "jev-1.14.0"
			config.APIKey = strings.Repeat("k", 32)
			return config
		}, "configured", ""},
		{"unsupported feature", func() JevConfig {
			config := jevTestConfig()
			config.Features = []string{"citation_support"}
			return config
		}, "unavailable", "unsupported_feature"},
		{"approved text egress", func() JevConfig {
			config := jevTestConfig()
			config.Egress = agentflow.AdvisoryEgressApprovedText
			return config
		}, "unavailable", "unsupported_egress"},
		{"output reservation", func() JevConfig {
			config := jevTestConfig()
			config.MaxOutputTokens = 8192
			return config
		}, "unavailable", "invalid_max_output_tokens"},
		{"invalid timeout", func() JevConfig {
			config := jevTestConfig()
			config.Timeout = time.Millisecond
			return config
		}, "unavailable", "invalid_timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := test.config()
			status, reason, client := normalizeJev(&config, jevTestBudget())
			if status != test.status || reason != test.reason {
				t.Fatalf("status=%q reason=%q, want %q %q", status, reason, test.status, test.reason)
			}
			if status == "configured" && client == nil {
				t.Fatal("configured status has no client")
			}
			if status != "configured" && client != nil {
				t.Fatal("non-configured status returned a client")
			}
		})
	}
}

func TestNormalizeJevRefusesFixtureOutsideDevelopment(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	config := jevTestConfig()
	status, reason, _ := normalizeJev(&config, jevTestBudget())
	if status != "unavailable" || reason != "fixture_not_allowed" {
		t.Fatalf("production fixture = %q %q", status, reason)
	}
}

func TestJevConfigFromEnvPinsAndDefaultsWithoutExposingSecrets(t *testing.T) {
	t.Setenv("TYPESAFE_JEV_MODE", "advisory")
	t.Setenv("TYPESAFE_JEV_MODEL", "jev-1.14.0")
	t.Setenv("TYPESAFE_JEV_EGRESS", "")
	t.Setenv("TYPESAFE_JEV_FEATURES", "knowledge_rerank")
	t.Setenv("TYPESAFE_API_KEY", strings.Repeat("k", 32))
	config := jevConfigFromEnv()
	if config.Mode != "advisory" || config.Model != "jev-1.14.0" ||
		config.Egress != agentflow.AdvisoryEgressMinimized ||
		len(config.Features) != 1 || config.Features[0] != agentflow.AdvisoryFeatureKnowledgeRerank ||
		config.Timeout != agentflow.DefaultAdvisoryTimeout || config.MaxOutputTokens != 2048 {
		t.Fatalf("config = %+v", config)
	}
	status, reason, _ := normalizeJev(&config, jevTestBudget())
	if status != "configured" || reason != "" {
		t.Fatalf("status=%q reason=%q", status, reason)
	}
}

func TestFixtureResponseIsDeterministicAndShapedLikeTheProvider(t *testing.T) {
	questions := map[string]jev.NoulQuestion{
		"candidate_0_relevance": {Instructions: "Is candidate 0 relevant?"},
		"candidate_1_relevance": {Instructions: "Is candidate 1 relevant?"},
		"candidate_2_relevance": {Instructions: "Is candidate 2 relevant?"},
	}
	first := fixtureJevResponse(agentflow.AdvisoryFixtureModel, questions)
	second := fixtureJevResponse(agentflow.AdvisoryFixtureModel, questions)
	if string(first) != string(second) {
		t.Fatal("fixture response is not deterministic")
	}
	answers, meta, err := jev.ParseSystemOne(first, questions)
	if err != nil {
		t.Fatalf("fixture response is not parseable: %v", err)
	}
	if meta.Model != agentflow.AdvisoryFixtureModel {
		t.Fatalf("fixture model = %q", meta.Model)
	}
	for questionID, want := range map[string]float64{
		"candidate_0_relevance": 0.82,
		"candidate_1_relevance": 0.12,
		"candidate_2_relevance": 0.50,
	} {
		answer, ok := answers[questionID]
		if !ok || answer.Noul != want || answer.Model != agentflow.AdvisoryFixtureModel {
			t.Fatalf("answer %s = %+v", questionID, answer)
		}
	}
}

func TestDecodeSystemOneRequestEnforcesShapeAndRejectsTrailingValues(t *testing.T) {
	valid := `{"model":"jev-fixture-v1","state":"{}","questions":{"q1":{"type":"noul","instructions":"check","criteria":{"true":"yes","false":"no"}}}}`
	modelID, state, questions, err := decodeSystemOneRequest([]byte(valid))
	if err != nil || modelID != "jev-fixture-v1" || state != "{}" || questions["q1"].Criteria.True != "yes" {
		t.Fatalf("decoded request model=%q state=%q questions=%+v err=%v", modelID, state, questions, err)
	}
	for _, body := range []string{
		"{", valid + ` {}`, strings.TrimSuffix(valid, "}") + `,"unknown":true}`,
		`{"model":"","state":"{}","questions":{"q1":{"type":"noul"}}}`,
		`{"model":"m","state":"{}","questions":{"q1":{"type":"freeform"}}}`,
	} {
		if _, _, _, err := decodeSystemOneRequest([]byte(body)); err == nil {
			t.Errorf("invalid request was accepted: %s", body)
		}
	}
}

func TestJevUsageEncodingAndErrorClassification(t *testing.T) {
	input, output := int64(5), int64(3)
	usage := tokenUsageFromJev(jev.Usage{InputTokens: &input, OutputTokens: &output})
	if usage == nil || usage.TotalTokens == nil || *usage.TotalTokens != 8 || *usage.InputTokens != 5 || *usage.OutputTokens != 3 {
		t.Fatalf("usage=%+v", usage)
	}
	if tokenUsageFromJev(jev.Usage{}) != nil {
		t.Fatal("empty usage should stay absent")
	}
	encoded := encodeJevResponse(jev.ResponseMeta{Model: "jev-1.2.3", Usage: jev.Usage{InputTokens: &input}}, map[string]jev.NoulAnswer{"q1": {Noul: 0.75}})
	var response struct {
		Model   string `json:"model"`
		Answers map[string]struct {
			Type string  `json:"type"`
			Noul float64 `json:"noul"`
		} `json:"answers"`
		Usage map[string]int64 `json:"usage"`
	}
	if err := json.Unmarshal(encoded, &response); err != nil || response.Model != "jev-1.2.3" || response.Answers["q1"].Noul != 0.75 || response.Usage["input_tokens"] != input {
		t.Fatalf("encoded advisory response=%s err=%v", encoded, err)
	}
	for _, test := range []struct {
		err  error
		want string
	}{
		{nil, ""}, {context.DeadlineExceeded, "timeout"}, {context.Canceled, "cancelled"}, {errJevInvalidResponse, "invalid_response"}, {errors.New("provider"), "provider_failed"},
	} {
		if got := jevErrorCode(test.err); got != test.want {
			t.Errorf("jevErrorCode(%v)=%q, want %q", test.err, got, test.want)
		}
	}
}

func TestJevTimingOverridesKeepValidValuesAndFlagInvalidOnes(t *testing.T) {
	t.Setenv("TYPESAFE_JEV_TIMEOUT", "4s")
	t.Setenv("TYPESAFE_JEV_MAX_OUTPUT_TOKENS", "4096")
	config := jevConfigFromEnv()
	if config.Timeout != 4*time.Second || config.MaxOutputTokens != 4096 || config.Problem != "" {
		t.Fatalf("valid timing overrides: %+v", config)
	}
	for _, test := range []struct{ timeout, tokens, problem string }{
		{"bad", "12", "invalid_timeout"}, {"4s", "bad", "invalid_max_output_tokens"}, {"10h", "8193", "invalid_max_output_tokens"},
	} {
		t.Setenv("TYPESAFE_JEV_TIMEOUT", test.timeout)
		t.Setenv("TYPESAFE_JEV_MAX_OUTPUT_TOKENS", test.tokens)
		got := jevConfigFromEnv()
		if got.Problem != test.problem {
			t.Errorf("problem=%q for timeout=%q tokens=%q, want %q", got.Problem, test.timeout, test.tokens, test.problem)
		}
	}
}

func TestDispatchJevReturnsFixtureSuggestionsAndFailsClosedWithoutClient(t *testing.T) {
	question := map[string]jev.NoulQuestion{"candidate_0_relevance": {Instructions: "check relevance"}}
	service := &Service{jevStatus: "fixture", jev: JevConfig{Model: "jev-fixture-v1"}}
	response, usage, err := service.dispatchJev(context.Background(), `{}`, question)
	if err != nil || usage == nil || usage.Source != "local_fixture" {
		t.Fatalf("fixture advisory response usage=%+v err=%v", usage, err)
	}
	if _, _, err := jev.ParseSystemOne(response, question); err != nil {
		t.Fatalf("fixture advisory response does not parse: %v", err)
	}
	service.jevStatus = "configured"
	if _, _, err := service.dispatchJev(context.Background(), `{}`, question); err == nil {
		t.Fatal("configured advisory without a client succeeded")
	}
}

func TestDispatchRemoteJevValidatesProviderModelAndMapsProviderFailure(t *testing.T) {
	question := map[string]jev.NoulQuestion{"candidate_0_relevance": {Instructions: "check relevance", Criteria: jev.NoulCriteria{True: "applies", False: "does not apply"}}}
	for _, test := range []struct {
		name        string
		status      int
		model       string
		wantError   bool
		wantInvalid bool
	}{
		{"valid answer", http.StatusOK, "jev-1.2.3", false, false},
		{"wrong reported model", http.StatusOK, "jev-2.0.0", true, true},
		{"provider status", http.StatusBadGateway, "", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, `{"model":"`+test.model+`","answers":{"candidate_0_relevance":{"type":"noul","noul":0.7}},"usage":{"input_tokens":2,"output_tokens":1}}`)
			}))
			defer server.Close()
			client, err := jev.New(jev.Config{BaseURL: server.URL, APIKey: strings.Repeat("k", 32), Model: "jev-1.2.3", Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			service := &Service{jev: JevConfig{Model: "jev-1.2.3"}, jevClient: client}
			response, usage, dispatchErr := service.dispatchRemoteJev(context.Background(), `{}`, question)
			if (dispatchErr != nil) != test.wantError || errors.Is(dispatchErr, errJevInvalidResponse) != test.wantInvalid {
				t.Fatalf("dispatch err=%v, wantError=%t wantInvalid=%t", dispatchErr, test.wantError, test.wantInvalid)
			}
			if !test.wantError && (usage == nil || len(response) == 0) {
				t.Fatalf("valid response=%s usage=%+v", response, usage)
			}
		})
	}
}

func TestJevHealthHandlerReturnsStatusAndMethodContract(t *testing.T) {
	service := &Service{jev: JevConfig{Mode: "fixture", Model: "jev-fixture-v1", Features: []string{"knowledge_rerank"}, Egress: "minimized"}, jevStatus: "fixture"}
	handler := service.Handler()
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest("GET", "/health/jev", nil))
	if get.Code != 200 || !strings.Contains(get.Body.String(), `"status":"fixture"`) {
		t.Fatalf("health response=%d %s", get.Code, get.Body)
	}
	post := httptest.NewRecorder()
	handler.ServeHTTP(post, httptest.NewRequest("POST", "/health/jev", nil))
	if post.Code != 405 || post.Header().Get("Allow") != "GET" {
		t.Fatalf("method response=%d allow=%q", post.Code, post.Header().Get("Allow"))
	}
}

func TestChatAttemptSettlementStatusMapping(t *testing.T) {
	for _, test := range []struct {
		err        error
		status     int
		wantStatus string
		wantCode   string
	}{
		{nil, 200, "completed", ""},
		{nil, 503, "failed", "provider_http_503"},
		{errors.New("transport"), 502, "failed", "provider_transport_error"},
		{context.DeadlineExceeded, 502, "failed", "provider_timeout"},
	} {
		status, code := chatAttemptResult(test.err, test.status)
		if status != test.wantStatus || code != test.wantCode {
			t.Errorf("chat result=(%q,%q), want (%q,%q)", status, code, test.wantStatus, test.wantCode)
		}
	}
}
