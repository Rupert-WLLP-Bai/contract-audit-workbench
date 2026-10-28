package modelgateway

import (
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
