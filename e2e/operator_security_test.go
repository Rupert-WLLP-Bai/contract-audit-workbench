package e2e

// Black-box security tests for the read-only OSS connector and the
// deterministic verification boundary.
//
// Everything runs against 127.0.0.1 httptest servers. No database, no external
// network and no production data are involved. The assertions pin the security
// contract: caller input can never choose a URL, method or credential; source
// failures never become "available"; response bodies can never bind a different
// tenant or case; and injected instructions cannot change the outcome.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/externalread"
	"contractaudit/internal/fulfillment"
)

const (
	operatorConnectorID   = "fixture.oss"
	operatorSourceSystem  = "fixture-oss-east"
	operatorBindingVer    = "fixture-binding-v1"
	operatorExternalObjID = "fixture-line-001"
)

var operatorSecurityClock = time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)

func operatorSecurityScope() auditext.AuthorizedScope {
	return operatorScopeFixture{
		TenantID:     "fixture-tenant-a",
		CaseID:       "fixture-case-001",
		ActorID:      "fixture-actor-q",
		GrantVersion: "fixture-grant-v1",
	}.scope()
}

func operatorSecurityReadRequest() auditext.ReadRequest {
	return auditext.ReadRequest{
		Scope:            operatorSecurityScope(),
		ConnectorID:      operatorConnectorID,
		BindingVersion:   operatorBindingVer,
		ExternalObjectID: operatorExternalObjID,
		BusinessAt:       time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC),
	}
}

func operatorSecurityConnector(t *testing.T, baseURL string, mutate func(*externalread.Config)) *externalread.Connector {
	t.Helper()
	config := externalread.Config{
		ConnectorID:    operatorConnectorID,
		BaseURL:        baseURL,
		SourceSystem:   operatorSourceSystem,
		AllowedObjects: []string{operatorExternalObjID},
		PayloadPrefix:  "fixture://oss/",
		Timeout:        2 * time.Second,
		Clock:          func() time.Time { return operatorSecurityClock },
	}
	if mutate != nil {
		mutate(&config)
	}
	connector, err := externalread.New(config)
	if err != nil {
		t.Fatalf("externalread.New: %v", err)
	}
	if connector.ID() != operatorConnectorID {
		t.Fatalf("connector ID = %q, want %q", connector.ID(), operatorConnectorID)
	}
	return connector
}

type operatorRequestCapture struct {
	mu      sync.Mutex
	count   int
	method  string
	path    string
	query   string
	headers http.Header
}

func (c *operatorRequestCapture) record(request *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
	c.method = request.Method
	c.path = request.URL.Path
	c.query = request.URL.RawQuery
	c.headers = request.Header.Clone()
}

func (c *operatorRequestCapture) snapshot() (count int, method, path, query string, headers http.Header) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count, c.method, c.path, c.query, c.headers.Clone()
}

func operatorAssertNoCallerCredentials(t *testing.T, headers http.Header) {
	t.Helper()
	for name := range headers {
		switch http.CanonicalHeaderKey(name) {
		case "Authorization", "Cookie", "Proxy-Authorization", "X-Api-Key":
			t.Fatalf("connector sent caller credential header %q", name)
		}
	}
}

func operatorAssertObservation(t *testing.T, observation auditext.Observation, scope auditext.AuthorizedScope) {
	t.Helper()
	if err := observation.Validate(scope); err != nil {
		t.Fatalf("observation does not validate against the server-bound scope: %v", err)
	}
	if observation.TenantID != scope.TenantID || observation.CaseID != scope.CaseID {
		t.Fatalf("observation tenant/case = %s/%s, want %s/%s", observation.TenantID, observation.CaseID, scope.TenantID, scope.CaseID)
	}
	if observation.DataClass != auditext.DataClassSynthetic {
		t.Fatalf("observation data_class = %q, want synthetic", observation.DataClass)
	}
}

func TestOperatorSecurityConnectorRequestBoundary(t *testing.T) {
	rawBody, err := os.ReadFile(operatorFixture(t, "raw", "oss-bandwidth.json"))
	if err != nil {
		t.Fatalf("read raw fixture: %v", err)
	}

	capture := &operatorRequestCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		capture.record(request)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(rawBody)
	}))
	defer server.Close()

	connector := operatorSecurityConnector(t, server.URL, nil)
	scope := operatorSecurityScope()

	observation, err := connector.Read(context.Background(), operatorSecurityReadRequest())
	if err != nil {
		t.Fatalf("Read(valid) = %v", err)
	}
	operatorAssertObservation(t, observation, scope)
	if observation.Availability != auditext.AvailabilityAvailable {
		t.Fatalf("availability = %q, want available", observation.Availability)
	}
	if observation.SourceSystem != operatorSourceSystem {
		t.Fatalf("source_system = %q, want %q", observation.SourceSystem, operatorSourceSystem)
	}
	field, ok := observation.FieldByName(fulfillment.ExternalFieldConfiguredBandwidth)
	if !ok || field.Value != "80" || field.Unit != "Mbit/s" {
		t.Fatalf("configured_bandwidth field = %+v, %v", field, ok)
	}

	count, method, path, query, headers := capture.snapshot()
	if count != 1 {
		t.Fatalf("server received %d requests, want exactly 1", count)
	}
	if method != http.MethodGet {
		t.Fatalf("connector used method %q; only GET is allowed", method)
	}
	if path != "/records/"+operatorExternalObjID {
		t.Fatalf("request path = %q, want pinned /records/%s", path, operatorExternalObjID)
	}
	if query != "" {
		t.Fatalf("request carried query %q", query)
	}
	operatorAssertNoCallerCredentials(t, headers)

	// The object identifier is never a URL and never escapes the pinned route.
	badObjects := []string{
		"https://evil.example/records/fixture-line-001",
		"http://127.0.0.1/records/fixture-line-001",
		"../fixture-line-001",
		"fixture-line-001/../../admin",
		"fixture-line-001?method=POST",
		"fixture-line-001#fragment",
		"fixture line 001",
		"",
	}
	for _, objectID := range badObjects {
		request := operatorSecurityReadRequest()
		request.ExternalObjectID = objectID
		if _, err := connector.Read(context.Background(), request); err == nil {
			t.Fatalf("Read(ExternalObjectID=%q) was accepted; object ids must be identifiers, not URLs", objectID)
		}
	}
	if got, _, _, _, _ := capture.snapshot(); got != 1 {
		t.Fatalf("invalid object ids reached the server: request count = %d, want 1", got)
	}

	t.Run("connector id is fixed by configuration", func(t *testing.T) {
		request := operatorSecurityReadRequest()
		request.ConnectorID = "other.connector"
		if _, err := connector.Read(context.Background(), request); !errors.Is(err, auditext.ErrInvalidRequest) {
			t.Fatalf("Read(other connector) = %v, want ErrInvalidRequest", err)
		}
	})

	t.Run("invalid scope is rejected", func(t *testing.T) {
		request := operatorSecurityReadRequest()
		request.Scope.GrantVersion = ""
		if _, err := connector.Read(context.Background(), request); err == nil {
			t.Fatal("Read with an empty grant version was accepted")
		}
		if got, _, _, _, _ := capture.snapshot(); got != 1 {
			t.Fatalf("request with invalid scope reached the server (count %d)", got)
		}
	})

	t.Run("base url cannot smuggle credentials or schemes", func(t *testing.T) {
		badURLs := []string{
			"",
			"ftp://example.com",
			"https://user:pass@example.com",
			"https://example.com/?redirect=https://evil.example",
			"https://example.com/#fragment",
			"file:///etc/passwd",
		}
		for _, baseURL := range badURLs {
			if _, err := externalread.New(externalread.Config{
				ConnectorID:  operatorConnectorID,
				BaseURL:      baseURL,
				SourceSystem: operatorSourceSystem,
			}); err == nil {
				t.Fatalf("externalread.New accepted BaseURL %q", baseURL)
			}
		}
	})
}

func TestOperatorSecurityConnectorFailureMapping(t *testing.T) {
	scope := operatorSecurityScope()

	t.Run("403 does not leak a snapshot", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte("victim-secret-payload"))
		}))
		defer server.Close()
		connector := operatorSecurityConnector(t, server.URL, nil)
		observation, err := connector.Read(context.Background(), operatorSecurityReadRequest())
		if !errors.Is(err, auditext.ErrUnauthorized) {
			t.Fatalf("403 error = %v, want ErrUnauthorized", err)
		}
		if strings.Contains(err.Error(), "victim-secret-payload") {
			t.Fatalf("403 error leaked the response body: %v", err)
		}
		if observation.TenantID != "" || observation.SnapshotID != "" || len(observation.Fields) != 0 {
			t.Fatalf("403 returned snapshot content: %+v", observation)
		}
	})

	t.Run("429 is unavailable, not success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusTooManyRequests)
		}))
		defer server.Close()
		connector := operatorSecurityConnector(t, server.URL, nil)
		observation, err := connector.Read(context.Background(), operatorSecurityReadRequest())
		if err != nil {
			t.Fatalf("429 returned a hard error instead of an unavailable observation: %v", err)
		}
		operatorAssertObservation(t, observation, scope)
		if observation.Availability != auditext.AvailabilityUnavailable {
			t.Fatalf("429 availability = %q, want unavailable", observation.Availability)
		}
		if observation.ReasonCode != "source_rate_limited" {
			t.Fatalf("429 reason = %q, want source_rate_limited", observation.ReasonCode)
		}
	})

	t.Run("5xx is unavailable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()
		connector := operatorSecurityConnector(t, server.URL, nil)
		observation, err := connector.Read(context.Background(), operatorSecurityReadRequest())
		if err != nil {
			t.Fatalf("5xx returned error: %v", err)
		}
		operatorAssertObservation(t, observation, scope)
		if observation.Availability != auditext.AvailabilityUnavailable || observation.ReasonCode != "source_unavailable" {
			t.Fatalf("5xx = %q/%q, want unavailable/source_unavailable", observation.Availability, observation.ReasonCode)
		}
	})

	t.Run("redirect is not followed", func(t *testing.T) {
		var followed int
		var followedMu sync.Mutex
		evil := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			followedMu.Lock()
			followed++
			followedMu.Unlock()
			_, _ = writer.Write([]byte(`{"id":"fixture-line-001"}`))
		}))
		defer evil.Close()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, evil.URL+"/records/"+operatorExternalObjID, http.StatusFound)
		}))
		defer server.Close()
		connector := operatorSecurityConnector(t, server.URL, nil)
		observation, err := connector.Read(context.Background(), operatorSecurityReadRequest())
		if err != nil {
			t.Fatalf("redirect returned error: %v", err)
		}
		operatorAssertObservation(t, observation, scope)
		if observation.Availability != auditext.AvailabilityRejected || observation.ReasonCode != "source_redirect_rejected" {
			t.Fatalf("redirect = %q/%q, want rejected/source_redirect_rejected", observation.Availability, observation.ReasonCode)
		}
		followedMu.Lock()
		defer followedMu.Unlock()
		if followed != 0 {
			t.Fatalf("connector followed a redirect to %s", evil.URL)
		}
	})

	t.Run("timeout is unavailable", func(t *testing.T) {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			select {
			case <-release:
			case <-time.After(2 * time.Second):
			}
			_, _ = writer.Write([]byte(`{}`))
		}))
		defer server.Close()
		defer close(release)
		connector := operatorSecurityConnector(t, server.URL, func(config *externalread.Config) {
			config.Timeout = 40 * time.Millisecond
		})
		start := time.Now()
		observation, err := connector.Read(context.Background(), operatorSecurityReadRequest())
		if err != nil {
			t.Fatalf("timeout returned error: %v", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("timeout took %v; the configured deadline was not applied", elapsed)
		}
		operatorAssertObservation(t, observation, scope)
		if observation.Availability != auditext.AvailabilityUnavailable || observation.ReasonCode != "source_timeout" {
			t.Fatalf("timeout = %q/%q, want unavailable/source_timeout", observation.Availability, observation.ReasonCode)
		}
	})

	t.Run("oversized response is rejected", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = writer.Write([]byte(strings.Repeat("x", 4096)))
		}))
		defer server.Close()
		connector := operatorSecurityConnector(t, server.URL, func(config *externalread.Config) {
			config.MaxResponseBytes = 256
		})
		observation, err := connector.Read(context.Background(), operatorSecurityReadRequest())
		if err != nil {
			t.Fatalf("oversized response returned error: %v", err)
		}
		operatorAssertObservation(t, observation, scope)
		if observation.Availability != auditext.AvailabilityRejected || observation.ReasonCode != "source_response_too_large" {
			t.Fatalf("oversized = %q/%q, want rejected/source_response_too_large", observation.Availability, observation.ReasonCode)
		}
	})

	t.Run("malformed and incomplete records are rejected", func(t *testing.T) {
		cases := []struct {
			name       string
			body       string
			wantReason string
		}{
			{"malformed-json", `{"id":`, "source_response_invalid"},
			{"missing-field", `{"id":"fixture-line-001","version":"v1","updatedAt":"2026-10-19T23:55:00Z","effectiveFrom":"2026-10-01T00:00:00Z","effectiveUntil":"2026-11-01T00:00:00Z","serviceCharacteristic":[]}`, "required_field_missing"},
			{"wrong-object", `{"id":"fixture-line-victim","version":"v1","updatedAt":"2026-10-19T23:55:00Z","effectiveFrom":"2026-10-01T00:00:00Z","effectiveUntil":"2026-11-01T00:00:00Z","serviceCharacteristic":[{"name":"configuredBandwidth","value":"9999","unit":"Mbit/s"}]}`, "external_object_mismatch"},
			{"bad-interval", `{"id":"fixture-line-001","version":"v1","updatedAt":"2026-10-19T23:55:00Z","effectiveFrom":"2026-11-01T00:00:00Z","effectiveUntil":"2026-10-01T00:00:00Z","serviceCharacteristic":[{"name":"configuredBandwidth","value":"80","unit":"Mbit/s"}]}`, "effective_interval_missing"},
			{"bad-updated-at", `{"id":"fixture-line-001","version":"v1","updatedAt":"not-a-time","effectiveFrom":"2026-10-01T00:00:00Z","effectiveUntil":"2026-11-01T00:00:00Z","serviceCharacteristic":[{"name":"configuredBandwidth","value":"80","unit":"Mbit/s"}]}`, "source_updated_at_invalid"},
			{"negative-interval-body", `null`, "source_response_invalid"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					_, _ = writer.Write([]byte(tc.body))
				}))
				defer server.Close()
				connector := operatorSecurityConnector(t, server.URL, nil)
				observation, err := connector.Read(context.Background(), operatorSecurityReadRequest())
				if err != nil {
					t.Fatalf("Read = %v", err)
				}
				operatorAssertObservation(t, observation, scope)
				if observation.Availability != auditext.AvailabilityRejected {
					t.Fatalf("availability = %q, want rejected", observation.Availability)
				}
				if observation.ReasonCode != tc.wantReason {
					t.Fatalf("reason = %q, want %q", observation.ReasonCode, tc.wantReason)
				}
				if observation.Availability == auditext.AvailabilityAvailable {
					t.Fatal("invalid source record was reported as available")
				}
			})
		}
	})

	t.Run("object outside allow-list never reaches the server", func(t *testing.T) {
		capture := &operatorRequestCapture{}
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			capture.record(request)
			_, _ = writer.Write([]byte(`{}`))
		}))
		defer server.Close()
		connector := operatorSecurityConnector(t, server.URL, nil)
		request := operatorSecurityReadRequest()
		request.ExternalObjectID = "fixture-line-002"
		observation, err := connector.Read(context.Background(), request)
		if err != nil {
			t.Fatalf("Read(disallowed object) = %v", err)
		}
		operatorAssertObservation(t, observation, scope)
		if observation.Availability != auditext.AvailabilityRejected || observation.ReasonCode != "external_object_not_allowed" {
			t.Fatalf("disallowed object = %q/%q", observation.Availability, observation.ReasonCode)
		}
		if count, _, _, _, _ := capture.snapshot(); count != 0 {
			t.Fatalf("disallowed object reached the server %d times", count)
		}
	})
}

func TestOperatorSecurityTenantAndRevocation(t *testing.T) {
	scope := operatorSecurityScope()
	input := operatorSecurityFrozenInput(t)

	t.Run("response body cannot bind another tenant or case", func(t *testing.T) {
		body, err := os.ReadFile(operatorFixture(t, "security", "oss-tenant-claim.json"))
		if err != nil {
			t.Fatalf("read tenant claim fixture: %v", err)
		}
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = writer.Write(body)
		}))
		defer server.Close()
		connector := operatorSecurityConnector(t, server.URL, nil)
		observation, err := connector.Read(context.Background(), operatorSecurityReadRequest())
		if err != nil {
			t.Fatalf("Read = %v", err)
		}
		operatorAssertObservation(t, observation, scope)
		if observation.TenantID == "fixture-tenant-victim" || observation.CaseID == "fixture-case-victim" {
			t.Fatalf("response body overrode tenant/case: %+v", observation)
		}
		victimScope := scope
		victimScope.TenantID = "fixture-tenant-victim"
		if err := observation.Validate(victimScope); !errors.Is(err, auditext.ErrUnauthorized) {
			t.Fatalf("observation bound to the requesting scope validated against another tenant: %v", err)
		}
		crossTenant := observation
		crossTenant.TenantID = "fixture-tenant-victim"
		results, err := fulfillment.New().Evaluate(context.Background(), input, []auditext.Observation{crossTenant})
		if !errors.Is(err, auditext.ErrUnauthorized) {
			t.Fatalf("cross-tenant observation Evaluate = %v, want ErrUnauthorized", err)
		}
		if len(results) != 0 {
			t.Fatalf("cross-tenant observation produced %d results", len(results))
		}
	})

	t.Run("revoked context yields no available evidence", func(t *testing.T) {
		body, err := os.ReadFile(operatorFixture(t, "raw", "oss-bandwidth.json"))
		if err != nil {
			t.Fatalf("read raw fixture: %v", err)
		}
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = writer.Write(body)
		}))
		defer server.Close()
		connector := operatorSecurityConnector(t, server.URL, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		observation, err := connector.Read(ctx, operatorSecurityReadRequest())
		if err != nil {
			t.Fatalf("canceled read returned a hard error: %v", err)
		}
		operatorAssertObservation(t, observation, scope)
		if observation.Availability == auditext.AvailabilityAvailable {
			t.Fatal("canceled read was reported as available evidence")
		}
		if observation.Availability != auditext.AvailabilityUnavailable || observation.ReasonCode != "source_unavailable" {
			t.Fatalf("canceled read = %q/%q", observation.Availability, observation.ReasonCode)
		}
		results, err := fulfillment.New().Evaluate(context.Background(), input, []auditext.Observation{observation})
		if err != nil {
			t.Fatalf("Evaluate(unavailable) = %v", err)
		}
		if len(results) != 1 || results[0].Relation != auditext.RelationInsufficient || results[0].ReasonCode != "source_unavailable" {
			t.Fatalf("unavailable observation became %+v", results)
		}
		if results[0].Relation == auditext.RelationCompatible || results[0].Relation == auditext.RelationConflict {
			t.Fatal("unavailable source was turned into a business relation")
		}
	})
}

// TestOperatorSecurityInjectionText proves that instruction-like text inside a
// source body cannot change the HTTP method, permissions or verification
// outcome.
func TestOperatorSecurityInjectionText(t *testing.T) {
	input := operatorSecurityFrozenInput(t)

	t.Run("injection in an unrelated field cannot approve", func(t *testing.T) {
		body, err := os.ReadFile(operatorFixture(t, "security", "oss-injection-field.json"))
		if err != nil {
			t.Fatalf("read injection fixture: %v", err)
		}
		capture := &operatorRequestCapture{}
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			capture.record(request)
			_, _ = writer.Write(body)
		}))
		defer server.Close()
		connector := operatorSecurityConnector(t, server.URL, nil)
		observation, err := connector.Read(context.Background(), operatorSecurityReadRequest())
		if err != nil {
			t.Fatalf("Read = %v", err)
		}
		operatorAssertObservation(t, observation, operatorSecurityScope())
		if observation.Availability != auditext.AvailabilityAvailable {
			t.Fatalf("injection field changed availability to %q/%q", observation.Availability, observation.ReasonCode)
		}
		count, method, path, query, headers := capture.snapshot()
		if count != 1 || method != http.MethodGet || path != "/records/"+operatorExternalObjID || query != "" {
			t.Fatalf("injection changed the request: count=%d method=%q path=%q query=%q", count, method, path, query)
		}
		operatorAssertNoCallerCredentials(t, headers)

		results, err := fulfillment.New().Evaluate(context.Background(), input, []auditext.Observation{observation})
		if err != nil {
			t.Fatalf("Evaluate = %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("Evaluate returned %d results", len(results))
		}
		if results[0].Relation == auditext.RelationCompatible {
			t.Fatalf("injection text turned a below-contract configuration into %q", results[0].Relation)
		}
		if results[0].Relation != auditext.RelationConflict || results[0].ReasonCode != "configured_bandwidth_below_contract" {
			t.Fatalf("injection result = %q/%q", results[0].Relation, results[0].ReasonCode)
		}
		if !results[0].RequiresHumanReview {
			t.Fatal("injection result skipped human review")
		}
	})

	t.Run("injection in the configured value is rejected", func(t *testing.T) {
		body, err := os.ReadFile(operatorFixture(t, "security", "oss-injection-value.json"))
		if err != nil {
			t.Fatalf("read injection-value fixture: %v", err)
		}
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = writer.Write(body)
		}))
		defer server.Close()
		connector := operatorSecurityConnector(t, server.URL, nil)
		observation, err := connector.Read(context.Background(), operatorSecurityReadRequest())
		if err != nil {
			t.Fatalf("Read = %v", err)
		}
		operatorAssertObservation(t, observation, operatorSecurityScope())
		if observation.Availability != auditext.AvailabilityRejected || observation.ReasonCode != "required_field_missing" {
			t.Fatalf("injected value = %q/%q, want rejected/required_field_missing", observation.Availability, observation.ReasonCode)
		}
		results, err := fulfillment.New().Evaluate(context.Background(), input, []auditext.Observation{observation})
		if err != nil {
			t.Fatalf("Evaluate = %v", err)
		}
		if len(results) != 1 || results[0].Relation != auditext.RelationInsufficient {
			t.Fatalf("rejected observation became %+v", results)
		}
		if results[0].Relation == auditext.RelationCompatible {
			t.Fatal("injected source text produced a compatible relation")
		}
	})

	t.Run("wrong source object is rejected before verification", func(t *testing.T) {
		body, err := os.ReadFile(operatorFixture(t, "security", "oss-wrong-object.json"))
		if err != nil {
			t.Fatalf("read wrong-object fixture: %v", err)
		}
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = writer.Write(body)
		}))
		defer server.Close()
		connector := operatorSecurityConnector(t, server.URL, nil)
		observation, err := connector.Read(context.Background(), operatorSecurityReadRequest())
		if err != nil {
			t.Fatalf("Read = %v", err)
		}
		operatorAssertObservation(t, observation, operatorSecurityScope())
		if observation.Availability == auditext.AvailabilityAvailable || observation.ReasonCode != "external_object_mismatch" {
			t.Fatalf("wrong object = %q/%q, want rejected/external_object_mismatch", observation.Availability, observation.ReasonCode)
		}
		results, err := fulfillment.New().Evaluate(context.Background(), input, []auditext.Observation{observation})
		if err != nil {
			t.Fatalf("Evaluate = %v", err)
		}
		if len(results) != 1 || results[0].Relation != auditext.RelationInsufficient || results[0].ReasonCode != "external_object_mismatch" {
			t.Fatalf("wrong-object observation became %+v", results)
		}
	})
}

func operatorSecurityFrozenInput(t *testing.T) auditext.FrozenInput {
	t.Helper()
	evidence := operatorEvidenceFixture{
		Kind:              "document",
		DocumentVersionID: "fixture-doc-v1",
		ParsedVersionID:   "fixture-parse-v1",
		Start:             0,
		End:               32,
		Quote:             "专线 LINE-001 最低保障带宽不低于100 Mbit/s。",
	}
	fact := operatorFactFixture{
		Name:         "minimum_bandwidth",
		Value:        auditext.ExactQuantity{Value: "100", Unit: "Mbit/s"},
		Evidence:     []operatorEvidenceFixture{evidence},
		SemanticKind: "configured_value",
	}
	input := auditext.FrozenInput{
		Scope:                  operatorSecurityScope(),
		Pack:                   auditext.PackRef{ID: "operator.dedicated_line", Version: "1.0.0"},
		ContractVersion:        "fixture-contract-v1",
		RulePackVersion:        "5",
		BindingStatus:          auditext.BindingConfirmed,
		BindingVersion:         operatorBindingVer,
		SnapshotSetID:          "fixture-snapshot-set-v1",
		ExternalObjectID:       operatorExternalObjID,
		SourceSystem:           operatorSourceSystem,
		ModelConfigurationID:   "fixture-model-v1",
		PlannerConfigurationID: "fixture-planner-v1",
		ParserConfigurationID:  "fixture-parser-v1",
		BusinessAt:             time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC),
		Facts:                  []auditext.Fact{fact.fact(t)},
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("security frozen input is invalid: %v", err)
	}
	return input
}
