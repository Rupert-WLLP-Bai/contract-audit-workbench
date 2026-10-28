package agentflow

import (
	"strings"
	"testing"
)

func clearAdvisoryEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"TYPESAFE_JEV_MODE", "TYPESAFE_JEV_EGRESS", "TYPESAFE_JEV_FEATURES", "TYPESAFE_JEV_MODEL",
		"TYPESAFE_JEV_PROVIDER", "TYPESAFE_JEV_TIMEOUT", "TYPESAFE_JEV_MAX_CANDIDATES", "APP_ENV",
	} {
		t.Setenv(name, "")
	}
}

func TestResolveAdvisoryConfigurationDefaultsToOff(t *testing.T) {
	clearAdvisoryEnv(t)
	if configuration, reason := ResolveAdvisoryConfiguration(); configuration != nil || reason != "disabled" {
		t.Fatalf("configuration=%+v reason=%q", configuration, reason)
	}
}

func TestResolveAdvisoryConfigurationFreezesAPinnedAdvisoryRun(t *testing.T) {
	clearAdvisoryEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("TYPESAFE_JEV_MODE", "advisory")
	t.Setenv("TYPESAFE_JEV_MODEL", "jev-1.14.0")
	t.Setenv("TYPESAFE_API_KEY", "unused-in-resolver")
	configuration, reason := ResolveAdvisoryConfiguration()
	if reason != "" || configuration == nil {
		t.Fatalf("configuration=%+v reason=%q", configuration, reason)
	}
	if configuration.Mode != AdvisoryModeAdvisory || configuration.Provider != AdvisoryProviderGateway ||
		configuration.Model != "jev-1.14.0" || configuration.Egress != AdvisoryEgressMinimized ||
		configuration.QuestionSetVersion != AdvisoryQuestionSetVersion ||
		len(configuration.Features) != 1 || configuration.Features[0] != AdvisoryFeatureKnowledgeRerank ||
		configuration.ThresholdHigh != 0.70 || configuration.ThresholdLow != 0.30 ||
		configuration.MaxCandidates != 16 || configuration.TimeoutMS != 3000 {
		t.Fatalf("configuration = %+v", configuration)
	}
	if err := configuration.Validate(); err != nil {
		t.Fatalf("frozen configuration is invalid: %v", err)
	}
}

func TestResolveAdvisoryConfigurationRejectsAliasesAndBadBounds(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T)
		reason string
	}{
		{"alias", func(t *testing.T) {
			t.Setenv("TYPESAFE_JEV_MODE", "advisory")
			t.Setenv("TYPESAFE_JEV_MODEL", "jev-latest")
		}, "model_alias_not_allowed"},
		{"missing model", func(t *testing.T) {
			t.Setenv("TYPESAFE_JEV_MODE", "advisory")
		}, "model_required"},
		{"unknown feature", func(t *testing.T) {
			t.Setenv("TYPESAFE_JEV_MODE", "advisory")
			t.Setenv("TYPESAFE_JEV_MODEL", "jev-1.14.0")
			t.Setenv("TYPESAFE_JEV_FEATURES", "citation_support")
		}, "unsupported_feature"},
		{"approved text", func(t *testing.T) {
			t.Setenv("TYPESAFE_JEV_MODE", "advisory")
			t.Setenv("TYPESAFE_JEV_MODEL", "jev-1.14.0")
			t.Setenv("TYPESAFE_JEV_EGRESS", "approved_text")
		}, "unsupported_egress"},
		{"too many candidates", func(t *testing.T) {
			t.Setenv("TYPESAFE_JEV_MODE", "advisory")
			t.Setenv("TYPESAFE_JEV_MODEL", "jev-1.14.0")
			t.Setenv("TYPESAFE_JEV_MAX_CANDIDATES", "17")
		}, "invalid_max_candidates"},
		{"bad timeout", func(t *testing.T) {
			t.Setenv("TYPESAFE_JEV_MODE", "advisory")
			t.Setenv("TYPESAFE_JEV_MODEL", "jev-1.14.0")
			t.Setenv("TYPESAFE_JEV_TIMEOUT", "10ms")
		}, "invalid_timeout"},
		{"unknown provider", func(t *testing.T) {
			t.Setenv("TYPESAFE_JEV_MODE", "advisory")
			t.Setenv("TYPESAFE_JEV_MODEL", "jev-1.14.0")
			t.Setenv("TYPESAFE_JEV_PROVIDER", "local-laya")
		}, "unsupported_provider"},
		{"invalid mode", func(t *testing.T) { t.Setenv("TYPESAFE_JEV_MODE", "shadow") }, "invalid_mode"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearAdvisoryEnv(t)
			test.mutate(t)
			if configuration, reason := ResolveAdvisoryConfiguration(); configuration != nil || reason != test.reason {
				t.Fatalf("configuration=%+v reason=%q, want %q", configuration, reason, test.reason)
			}
		})
	}
}

func TestResolveAdvisoryConfigurationGatesDevelopmentOnlyProviders(t *testing.T) {
	t.Run("direct in development", func(t *testing.T) {
		clearAdvisoryEnv(t)
		t.Setenv("APP_ENV", "development")
		t.Setenv("TYPESAFE_JEV_MODE", "advisory")
		t.Setenv("TYPESAFE_JEV_MODEL", "jev-1.14.0")
		t.Setenv("TYPESAFE_JEV_PROVIDER", "direct")
		configuration, reason := ResolveAdvisoryConfiguration()
		if reason != "" || configuration == nil || configuration.Provider != AdvisoryProviderDirect {
			t.Fatalf("configuration=%+v reason=%q", configuration, reason)
		}
	})
	t.Run("direct in production", func(t *testing.T) {
		clearAdvisoryEnv(t)
		t.Setenv("APP_ENV", "production")
		t.Setenv("TYPESAFE_JEV_MODE", "advisory")
		t.Setenv("TYPESAFE_JEV_MODEL", "jev-1.14.0")
		t.Setenv("TYPESAFE_JEV_PROVIDER", "direct")
		if configuration, reason := ResolveAdvisoryConfiguration(); configuration != nil || reason != "direct_provider_not_allowed" {
			t.Fatalf("configuration=%+v reason=%q", configuration, reason)
		}
	})
	t.Run("fixture gateway in development", func(t *testing.T) {
		clearAdvisoryEnv(t)
		t.Setenv("APP_ENV", "test")
		t.Setenv("TYPESAFE_JEV_MODE", "advisory")
		t.Setenv("TYPESAFE_JEV_MODEL", AdvisoryFixtureModel)
		configuration, reason := ResolveAdvisoryConfiguration()
		if reason != "" || configuration == nil || configuration.Provider != AdvisoryProviderGateway ||
			configuration.Model != AdvisoryFixtureModel {
			t.Fatalf("configuration=%+v reason=%q", configuration, reason)
		}
	})
	t.Run("fixture gateway in production", func(t *testing.T) {
		clearAdvisoryEnv(t)
		t.Setenv("APP_ENV", "production")
		t.Setenv("TYPESAFE_JEV_MODE", "advisory")
		t.Setenv("TYPESAFE_JEV_MODEL", AdvisoryFixtureModel)
		if configuration, reason := ResolveAdvisoryConfiguration(); configuration != nil || reason != "fixture_model_not_allowed" {
			t.Fatalf("configuration=%+v reason=%q", configuration, reason)
		}
	})
	t.Run("local fixture in test", func(t *testing.T) {
		clearAdvisoryEnv(t)
		t.Setenv("APP_ENV", "test")
		t.Setenv("TYPESAFE_JEV_MODE", "fixture")
		configuration, reason := ResolveAdvisoryConfiguration()
		if reason != "" || configuration == nil || configuration.Provider != AdvisoryProviderFixture ||
			configuration.Model != AdvisoryFixtureModel {
			t.Fatalf("configuration=%+v reason=%q", configuration, reason)
		}
	})
	t.Run("local fixture in production", func(t *testing.T) {
		clearAdvisoryEnv(t)
		t.Setenv("APP_ENV", "production")
		t.Setenv("TYPESAFE_JEV_MODE", "fixture")
		if configuration, reason := ResolveAdvisoryConfiguration(); configuration != nil || reason != "fixture_not_allowed" {
			t.Fatalf("configuration=%+v reason=%q", configuration, reason)
		}
	})
}

func TestAdvisoryConfigurationValidateRejectsUnsupportedShapes(t *testing.T) {
	valid := AdvisoryConfiguration{
		Mode: AdvisoryModeAdvisory, Provider: AdvisoryProviderGateway, Model: "jev-1.14.0",
		QuestionSetVersion: AdvisoryQuestionSetVersion, Egress: AdvisoryEgressMinimized,
		Features: []string{AdvisoryFeatureKnowledgeRerank}, ThresholdHigh: 0.70, ThresholdLow: 0.30,
		MaxCandidates: 16, TimeoutMS: 3000,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*AdvisoryConfiguration)
	}{
		{"unpinned model", func(configuration *AdvisoryConfiguration) { configuration.Model = "jev-latest" }},
		{"direct with fixed model is allowed", func(configuration *AdvisoryConfiguration) {
			configuration.Provider = AdvisoryProviderDirect
		}},
		{"fixture model without gateway", func(configuration *AdvisoryConfiguration) {
			configuration.Model = AdvisoryFixtureModel
			configuration.Provider = AdvisoryProviderDirect
		}},
		{"wrong question version", func(configuration *AdvisoryConfiguration) {
			configuration.QuestionSetVersion = "knowledge-relevance/v2"
		}},
		{"text egress", func(configuration *AdvisoryConfiguration) { configuration.Egress = AdvisoryEgressApprovedText }},
		{"duplicate feature", func(configuration *AdvisoryConfiguration) {
			configuration.Features = []string{AdvisoryFeatureKnowledgeRerank, AdvisoryFeatureKnowledgeRerank}
		}},
		{"thresholds crossed", func(configuration *AdvisoryConfiguration) {
			configuration.ThresholdHigh = 0.30
			configuration.ThresholdLow = 0.70
		}},
		{"zero candidates", func(configuration *AdvisoryConfiguration) { configuration.MaxCandidates = 0 }},
		{"timeout too small", func(configuration *AdvisoryConfiguration) { configuration.TimeoutMS = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration := valid
			configuration.Features = append([]string(nil), valid.Features...)
			test.mutate(&configuration)
			wantValid := strings.HasPrefix(test.name, "direct with fixed model")
			if err := configuration.Validate(); (err == nil) != wantValid {
				t.Fatalf("Validate() = %v, wantValid=%v", err, wantValid)
			}
		})
	}
}

func TestPlannerConfigurationHashChangesOnlyWhenAdvisoryIsFrozen(t *testing.T) {
	clearAdvisoryEnv(t)
	t.Setenv("AGENT_PLANNER_MODE", "deterministic")
	baseline, err := ResolvePlannerConfiguration("")
	if err != nil {
		t.Fatalf("resolve baseline: %v", err)
	}
	if baseline.Advisory != nil {
		t.Fatalf("baseline unexpectedly froze advisory: %+v", baseline.Advisory)
	}
	second, err := ResolvePlannerConfiguration("")
	if err != nil || second.ConfigurationID != baseline.ConfigurationID {
		t.Fatalf("baseline hash is not stable: %s vs %s (%v)", baseline.ConfigurationID, second.ConfigurationID, err)
	}
	t.Setenv("TYPESAFE_JEV_MODE", "advisory")
	t.Setenv("TYPESAFE_JEV_MODEL", "jev-1.14.0")
	advisory, err := ResolvePlannerConfiguration("")
	if err != nil {
		t.Fatalf("resolve advisory: %v", err)
	}
	if advisory.Advisory == nil || advisory.ConfigurationID == baseline.ConfigurationID {
		t.Fatalf("advisory configuration did not change the frozen identity: %+v", advisory)
	}
	t.Setenv("TYPESAFE_JEV_MODE", "off")
	off, err := ResolvePlannerConfiguration("")
	if err != nil || off.Advisory != nil || off.ConfigurationID != baseline.ConfigurationID {
		t.Fatalf("off did not restore the baseline hash: %+v (%v)", off, err)
	}
}
