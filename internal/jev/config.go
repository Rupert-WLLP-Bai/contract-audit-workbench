package jev

import (
	"os"
	"strings"
)

const (
	// EnvBaseURL overrides the official TypeSafe API origin.
	EnvBaseURL = "TYPESAFE_JEV_BASE_URL"
	// EnvModel overrides the stable Jev model alias.
	EnvModel = "TYPESAFE_JEV_MODEL"
	// EnvAPIKey is the provider credential and the enable switch. It is never
	// returned by a status or health surface.
	EnvAPIKey = "TYPESAFE_API_KEY"
)

// FromEnv reads the optional TypeSafe Jev provider configuration. An empty
// TYPESAFE_API_KEY leaves the provider unconfigured; the base URL and model
// fall back to the documented defaults.
func FromEnv() Config {
	return Config{
		BaseURL: envOrDefault(EnvBaseURL, DefaultBaseURL),
		APIKey:  strings.TrimSpace(os.Getenv(EnvAPIKey)),
		Model:   envOrDefault(EnvModel, DefaultModel),
		Timeout: DefaultTimeout,
	}
}

// State is a credential-free view of the provider configuration. Endpoint is
// the configured base URL, not a request URL; callers must still sanitize it
// before exposing it.
type State struct {
	Endpoint      string
	Model         string
	KeyConfigured bool
	// Valid is true when the provider is disabled or its enabled configuration
	// passes the same local validation as New. It makes no network request.
	Valid bool
}

// State summarizes the configuration without exposing the API key.
func (c Config) State() State {
	endpoint := strings.TrimSpace(c.BaseURL)
	if endpoint == "" {
		endpoint = DefaultBaseURL
	}
	model := strings.TrimSpace(c.Model)
	if model == "" {
		model = DefaultModel
	}
	keyConfigured := strings.TrimSpace(c.APIKey) != ""
	valid := true
	if keyConfigured {
		valid = c.Validate() == nil
	}
	return State{Endpoint: endpoint, Model: model, KeyConfigured: keyConfigured, Valid: valid}
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
