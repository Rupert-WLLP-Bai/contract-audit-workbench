package agentflow

import (
	"errors"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	AdvisoryQuestionSetVersion = "knowledge-relevance/v1"

	AdvisoryModeOff      = "off"
	AdvisoryModeFixture  = "fixture"
	AdvisoryModeAdvisory = "advisory"

	AdvisoryProviderGateway = "model-gateway"
	AdvisoryProviderFixture = "local-fixture"
	// AdvisoryFixtureModel is a reserved fake model, not a provider alias. It
	// may only be used with the gateway fixture route in development/test.
	AdvisoryFixtureModel = "jev-fixture"
	// AdvisoryProviderDirect is the single-process development path. It is
	// never accepted outside development/test because no gateway ledger or
	// caller identity sits between the worker and the provider.
	AdvisoryProviderDirect = "direct"

	AdvisoryFeatureKnowledgeRerank = "knowledge_rerank"

	AdvisoryEgressMinimized    = "minimized"
	AdvisoryEgressApprovedText = "approved_text"

	DefaultAdvisoryTimeout       = 3 * time.Second
	MinAdvisoryTimeout           = 100 * time.Millisecond
	MaxAdvisoryTimeout           = 30 * time.Second
	DefaultAdvisoryMaxCandidates = 16
	MaxAdvisoryMaxCandidates     = 16

	AdvisoryThresholdHigh = 0.70
	AdvisoryThresholdLow  = 0.30
)

var advisoryPinnedModelPattern = regexp.MustCompile(`^jev-[0-9]+\.[0-9]+\.[0-9]+$`)

// AdvisoryConfiguration is the frozen identity of the default-on suggestion
// layer. It never grants authority: the worker only uses it to rerank optional
// governed knowledge, and every failure falls back to deterministic order.
type AdvisoryConfiguration struct {
	Mode               string   `json:"mode"`
	Provider           string   `json:"provider"`
	Model              string   `json:"model"`
	QuestionSetVersion string   `json:"question_set_version"`
	Egress             string   `json:"egress"`
	Features           []string `json:"features"`
	ThresholdHigh      float64  `json:"threshold_high"`
	ThresholdLow       float64  `json:"threshold_low"`
	MaxCandidates      int      `json:"max_candidates"`
	TimeoutMS          int64    `json:"timeout_ms"`
}

// ResolveAdvisoryConfiguration reads the non-secret advisory environment at
// acceptance time. An absent mode means off. An invalid configuration returns
// nil plus a bounded reason: it must never block review acceptance or silently
// become a different model. Deployment surfaces use the reason for health.
func ResolveAdvisoryConfiguration() (*AdvisoryConfiguration, string) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("TYPESAFE_JEV_MODE")))
	if mode == "" || mode == AdvisoryModeOff {
		return nil, "disabled"
	}
	if mode != AdvisoryModeAdvisory && mode != AdvisoryModeFixture {
		return nil, "invalid_mode"
	}
	if mode == AdvisoryModeFixture && !developmentEnvironment() {
		return nil, "fixture_not_allowed"
	}
	egress := strings.ToLower(strings.TrimSpace(os.Getenv("TYPESAFE_JEV_EGRESS")))
	if egress == "" {
		egress = AdvisoryEgressMinimized
	}
	if egress != AdvisoryEgressMinimized {
		return nil, "unsupported_egress"
	}
	features, reason := advisoryFeatures(os.Getenv("TYPESAFE_JEV_FEATURES"))
	if reason != "" {
		return nil, reason
	}
	model := strings.TrimSpace(os.Getenv("TYPESAFE_JEV_MODEL"))
	provider := AdvisoryProviderGateway
	if mode == AdvisoryModeFixture {
		provider = AdvisoryProviderFixture
		if model == "" {
			model = AdvisoryFixtureModel
		}
		if model != AdvisoryFixtureModel {
			return nil, "fixture_model_mismatch"
		}
	} else {
		if raw := strings.ToLower(strings.TrimSpace(os.Getenv("TYPESAFE_JEV_PROVIDER"))); raw != "" {
			provider = raw
		}
		if provider != AdvisoryProviderGateway && provider != AdvisoryProviderDirect {
			return nil, "unsupported_provider"
		}
		if provider == AdvisoryProviderDirect && !developmentEnvironment() {
			return nil, "direct_provider_not_allowed"
		}
		if model == "" {
			return nil, "model_required"
		}
		if model != AdvisoryFixtureModel && !advisoryPinnedModelPattern.MatchString(model) {
			return nil, "model_alias_not_allowed"
		}
		if model == AdvisoryFixtureModel {
			if provider != AdvisoryProviderGateway {
				return nil, "fixture_model_requires_gateway"
			}
			if !developmentEnvironment() {
				return nil, "fixture_model_not_allowed"
			}
		}
	}
	timeout := DefaultAdvisoryTimeout
	if raw := strings.TrimSpace(os.Getenv("TYPESAFE_JEV_TIMEOUT")); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value < MinAdvisoryTimeout || value > MaxAdvisoryTimeout {
			return nil, "invalid_timeout"
		}
		timeout = value
	}
	maxCandidates := DefaultAdvisoryMaxCandidates
	if raw := strings.TrimSpace(os.Getenv("TYPESAFE_JEV_MAX_CANDIDATES")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > MaxAdvisoryMaxCandidates {
			return nil, "invalid_max_candidates"
		}
		maxCandidates = value
	}
	configuration := &AdvisoryConfiguration{
		Mode: mode, Provider: provider, Model: model,
		QuestionSetVersion: AdvisoryQuestionSetVersion, Egress: egress,
		Features: features, ThresholdHigh: AdvisoryThresholdHigh, ThresholdLow: AdvisoryThresholdLow,
		MaxCandidates: maxCandidates, TimeoutMS: timeout.Milliseconds(),
	}
	if err := configuration.Validate(); err != nil {
		return nil, "invalid_configuration"
	}
	return configuration, ""
}

// developmentEnvironment matches the repository's APP_ENV convention: an empty
// value is development, not production.
func developmentEnvironment() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) {
	case "", "development", "dev", "test":
		return true
	default:
		return false
	}
}

func advisoryFeatures(raw string) ([]string, string) {
	if strings.TrimSpace(raw) == "" {
		return []string{AdvisoryFeatureKnowledgeRerank}, ""
	}
	seen := map[string]bool{}
	features := []string{}
	for _, item := range strings.Split(raw, ",") {
		feature := strings.ToLower(strings.TrimSpace(item))
		if feature == "" {
			continue
		}
		if feature != AdvisoryFeatureKnowledgeRerank {
			return nil, "unsupported_feature"
		}
		if !seen[feature] {
			seen[feature] = true
			features = append(features, feature)
		}
	}
	if len(features) == 0 {
		return nil, "unsupported_feature"
	}
	sort.Strings(features)
	return features, ""
}

func (configuration AdvisoryConfiguration) Validate() error {
	switch configuration.Mode {
	case AdvisoryModeAdvisory:
		if configuration.Provider != AdvisoryProviderGateway && configuration.Provider != AdvisoryProviderDirect {
			return errors.New("advisory mode requires the gateway or direct provider")
		}
		if configuration.Model != AdvisoryFixtureModel && !advisoryPinnedModelPattern.MatchString(configuration.Model) {
			return errors.New("advisory mode requires a pinned Jev model")
		}
		if configuration.Model == AdvisoryFixtureModel && configuration.Provider != AdvisoryProviderGateway {
			return errors.New("the gateway fixture model requires the gateway provider")
		}
	case AdvisoryModeFixture:
		if configuration.Provider != AdvisoryProviderFixture || configuration.Model != AdvisoryFixtureModel {
			return errors.New("advisory fixture requires the local fixture provider and the fixture model")
		}
	default:
		return errors.New("advisory mode must be advisory or fixture")
	}
	if configuration.QuestionSetVersion != AdvisoryQuestionSetVersion {
		return errors.New("advisory question set version is not the implemented catalog")
	}
	if configuration.Egress != AdvisoryEgressMinimized {
		return errors.New("advisory egress must be minimized")
	}
	if len(configuration.Features) == 0 || len(configuration.Features) > 8 {
		return errors.New("advisory features are outside the supported range")
	}
	seen := map[string]bool{}
	for _, feature := range configuration.Features {
		if feature != AdvisoryFeatureKnowledgeRerank || seen[feature] {
			return errors.New("advisory feature is unknown or duplicated")
		}
		seen[feature] = true
	}
	if configuration.ThresholdLow <= 0 || configuration.ThresholdHigh >= 1 ||
		configuration.ThresholdLow >= configuration.ThresholdHigh {
		return errors.New("advisory thresholds are invalid")
	}
	if configuration.MaxCandidates < 1 || configuration.MaxCandidates > MaxAdvisoryMaxCandidates {
		return errors.New("advisory candidate limit is outside the supported range")
	}
	if configuration.TimeoutMS < MinAdvisoryTimeout.Milliseconds() || configuration.TimeoutMS > MaxAdvisoryTimeout.Milliseconds() {
		return errors.New("advisory timeout is outside the supported range")
	}
	return nil
}

// Supports reports whether the frozen advisory configuration enables one
// feature. It never treats an unknown feature as enabled.
func (configuration *AdvisoryConfiguration) Supports(feature string) bool {
	if configuration == nil {
		return false
	}
	for _, enabled := range configuration.Features {
		if enabled == feature {
			return true
		}
	}
	return false
}
