package externalread

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"contractaudit/internal/auditext"
)

func TestConfiguredBandwidthKeepsDecimalGrammarAndByteLimit(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{"zero", "0", true},
		{"decimal", "0.125", true},
		{"maximum byte length", strings.Repeat("9", 64), true},
		{"NaN", "NaN", false},
		{"positive infinity", "Infinity", false},
		{"exponent", "1e3", false},
		{"leading plus", "+1", false},
		{"negative", "-1", false},
		{"fraction", "1/2", false},
		{"trailing decimal point", "1.", false},
		{"over byte limit", strings.Repeat("9", 65), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := serveBody(t, http.StatusOK, recordJSONWith(t, func(fields map[string]any) {
				fields["serviceCharacteristic"] = []map[string]any{{"name": "configuredBandwidth", "value": tc.value, "unit": "Mbit/s"}}
			}))
			connector := newTestConnector(t, baseConfig(server.URL))
			observation, err := connector.Read(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			if tc.valid {
				assertObservation(t, observation, auditext.AvailabilityAvailable, reasonReadSucceeded)
				field, ok := observation.FieldByName("configured_bandwidth")
				if !ok || field.Value != tc.value {
					t.Fatalf("configured field = %+v, present=%v", field, ok)
				}
				return
			}
			assertObservation(t, observation, auditext.AvailabilityRejected, reasonRequiredFieldMissing)
			if len(observation.Fields) != 0 || observation.PayloadRef != nil {
				t.Fatalf("invalid quantity became evidence: %+v", observation)
			}
		})
	}
}

func TestFirstConfiguredCharacteristicControlsParsingAndSourcePointer(t *testing.T) {
	t.Run("invalid first duplicate remains missing", func(t *testing.T) {
		server := serveBody(t, http.StatusOK, recordJSONWith(t, func(fields map[string]any) {
			fields["serviceCharacteristic"] = []map[string]any{
				{"name": "configuredBandwidth", "value": "NaN", "unit": "Mbit/s"},
				{"name": "configuredBandwidth", "value": "80", "unit": "Mbit/s"},
			}
		}))
		connector := newTestConnector(t, baseConfig(server.URL))
		observation, err := connector.Read(context.Background(), testRequest())
		if err != nil {
			t.Fatal(err)
		}
		assertObservation(t, observation, auditext.AvailabilityRejected, reasonRequiredFieldMissing)
		if len(observation.Fields) != 0 {
			t.Fatalf("later duplicate replaced invalid first characteristic: %+v", observation.Fields)
		}
	})

	t.Run("first valid duplicate keeps its own pointer", func(t *testing.T) {
		server := serveBody(t, http.StatusOK, recordJSONWith(t, func(fields map[string]any) {
			fields["serviceCharacteristic"] = []map[string]any{
				{"name": "configuredBandwidth", "value": "80", "unit": "Mbit/s"},
				{"name": "configuredBandwidth", "value": "NaN", "unit": "Mbit/s"},
			}
		}))
		connector := newTestConnector(t, baseConfig(server.URL))
		observation, err := connector.Read(context.Background(), testRequest())
		if err != nil {
			t.Fatal(err)
		}
		assertObservation(t, observation, auditext.AvailabilityAvailable, reasonReadSucceeded)
		field, ok := observation.FieldByName("configured_bandwidth")
		if !ok || field.Value != "80" || field.SourcePointer != "/serviceCharacteristic/0/value" {
			t.Fatalf("first configured characteristic = %+v, present=%v", field, ok)
		}
	})
}

func TestReadAcceptsResponseAtExactConfiguredByteLimit(t *testing.T) {
	body := recordJSONWith(t, nil)
	server := serveBody(t, http.StatusOK, body)
	cfg := baseConfig(server.URL)
	cfg.MaxResponseBytes = int64(len(body))
	connector := newTestConnector(t, cfg)
	observation, err := connector.Read(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	assertObservation(t, observation, auditext.AvailabilityAvailable, reasonReadSucceeded)
}

type failedResponseBody struct{}

func (failedResponseBody) Read([]byte) (int, error) { return 0, errors.New("private read detail") }
func (failedResponseBody) Close() error             { return nil }

type externalReadRoundTripper func(*http.Request) (*http.Response, error)

func (f externalReadRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestReadBodyFailureReturnsUnavailableWithoutPayload(t *testing.T) {
	cfg := baseConfig("https://source.example")
	cfg.HTTPClient = &http.Client{Transport: externalReadRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: failedResponseBody{}, Header: make(http.Header), ContentLength: -1}, nil
	})}
	connector := newTestConnector(t, cfg)
	observation, err := connector.Read(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	assertObservation(t, observation, auditext.AvailabilityUnavailable, reasonSourceUnavailable)
	if len(observation.Fields) != 0 || observation.PayloadRef != nil || strings.Contains(observation.ReasonCode, "private") {
		t.Fatalf("body read failure leaked body content: %+v", observation)
	}
	if err := observation.Validate(testScope()); err != nil {
		t.Fatalf("failure observation is invalid: %v", err)
	}
}
