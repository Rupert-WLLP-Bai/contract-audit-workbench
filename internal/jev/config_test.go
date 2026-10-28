package jev

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFromEnvReadsTheModelConfiguration(t *testing.T) {
	t.Setenv(EnvBaseURL, " https://jev.example.test ")
	t.Setenv(EnvModel, " jev-preview ")
	t.Setenv(EnvAPIKey, "  "+testKey+"  ")
	config := FromEnv()
	if config.BaseURL != "https://jev.example.test" || config.Model != "jev-preview" ||
		config.APIKey != testKey || config.Timeout != DefaultTimeout {
		t.Fatalf("config = %+v", config)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("valid environment rejected: %v", err)
	}
}

func TestFromEnvUsesDefaultsWithoutOverrides(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvAPIKey, "")
	config := FromEnv()
	if config.BaseURL != DefaultBaseURL || config.Model != DefaultModel ||
		config.APIKey != "" || config.Timeout != DefaultTimeout {
		t.Fatalf("defaults = %+v", config)
	}
}

func TestStateIsCredentialFreeAndNeverSerializesTheKey(t *testing.T) {
	config := Config{BaseURL: "https://api.typesafe.ai", APIKey: testKey, Model: "jev-latest", Timeout: DefaultTimeout}
	state := config.State()
	if !state.KeyConfigured || !state.Valid || state.Model != "jev-latest" || state.Endpoint != "https://api.typesafe.ai" {
		t.Fatalf("state = %+v", state)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if strings.Contains(string(encoded), testKey) || strings.Contains(strings.ToLower(string(encoded)), "apikey_test") {
		t.Fatalf("state leaked the API key: %s", encoded)
	}
}

func TestStateDistinguishesDisabledFromInvalidEnabledConfiguration(t *testing.T) {
	for _, test := range []struct {
		name          string
		config        Config
		keyConfigured bool
		valid         bool
	}{
		{name: "disabled", config: Config{}, keyConfigured: false, valid: true},
		{name: "invalid enabled endpoint", config: Config{BaseURL: "http://api.typesafe.ai", APIKey: testKey}, keyConfigured: true, valid: false},
		{name: "invalid enabled model", config: Config{BaseURL: "https://api.typesafe.ai", APIKey: testKey, Model: "bad model"}, keyConfigured: true, valid: false},
		{name: "valid enabled", config: Config{APIKey: testKey}, keyConfigured: true, valid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := test.config.State()
			if state.KeyConfigured != test.keyConfigured || state.Valid != test.valid {
				t.Fatalf("state = %+v", state)
			}
			if state.Endpoint == "" || state.Model == "" {
				t.Fatalf("state omitted the effective endpoint or model: %+v", state)
			}
		})
	}
}
