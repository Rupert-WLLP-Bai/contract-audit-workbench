package externalread

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/auditext"
)

const (
	fixtureID        = "fixture-line-001"
	fixtureVersion   = "fixture-source-v1"
	fixtureUpdatedAt = "2026-10-28T07:55:00Z"
	fixtureFrom      = "2026-10-01T00:00:00Z"
	fixtureUntil     = "2026-11-01T00:00:00Z"
)

var fixedNow = time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)

func testScope() auditext.AuthorizedScope {
	return auditext.AuthorizedScope{
		TenantID:     "fixture-tenant-a",
		CaseID:       "fixture-case-001",
		ActorID:      "fixture-actor-a",
		GrantVersion: "grant-v1",
	}
}

func testRequest() auditext.ReadRequest {
	return auditext.ReadRequest{
		Scope:            testScope(),
		ConnectorID:      "fixture.oss",
		BindingVersion:   "fixture-binding-v1",
		ExternalObjectID: fixtureID,
		BusinessAt:       fixedNow,
	}
}

func baseConfig(baseURL string) Config {
	return Config{
		ConnectorID:      "fixture.oss",
		BaseURL:          baseURL,
		SourceSystem:     "fixture-oss-east",
		Timeout:          time.Second,
		MaxResponseBytes: DefaultMaxResponseBytes,
		Clock:            func() time.Time { return fixedNow },
	}
}

func newTestConnector(t *testing.T, cfg Config) *Connector {
	t.Helper()
	connector, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return connector
}

func recordFields() map[string]any {
	return map[string]any{
		"id":             fixtureID,
		"version":        fixtureVersion,
		"state":          "active",
		"updatedAt":      fixtureUpdatedAt,
		"effectiveFrom":  fixtureFrom,
		"effectiveUntil": fixtureUntil,
		"serviceCharacteristic": []map[string]any{
			{"name": "configuredBandwidth", "value": "80", "unit": "Mbit/s"},
		},
	}
}

func recordJSONWith(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	fields := recordFields()
	if mutate != nil {
		mutate(fields)
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(encoded)
}

func serveBody(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func assertObservation(t *testing.T, observation auditext.Observation, availability auditext.Availability, reason string) {
	t.Helper()
	if observation.Availability != availability {
		t.Fatalf("availability = %q, want %q (reason %q)", observation.Availability, availability, observation.ReasonCode)
	}
	if observation.ReasonCode != reason {
		t.Fatalf("reason = %q, want %q", observation.ReasonCode, reason)
	}
	if err := observation.Validate(testScope()); err != nil {
		t.Fatalf("observation does not validate: %v", err)
	}
}

func TestReadSuccess(t *testing.T) {
	body := recordJSONWith(t, nil)
	var mu sync.Mutex
	var seen struct {
		method string
		path   string
		auth   string
		accept string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen.method = r.Method
		seen.path = r.URL.Path
		seen.auth = r.Header.Get("Authorization")
		seen.accept = r.Header.Get("Accept")
		mu.Unlock()
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	connector := newTestConnector(t, baseConfig(server.URL+"/api/"))
	observation, err := connector.Read(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	mu.Lock()
	got := seen
	mu.Unlock()
	if got.method != http.MethodGet {
		t.Fatalf("method = %q, want GET", got.method)
	}
	if got.path != "/api/records/"+fixtureID {
		t.Fatalf("path = %q, want %q", got.path, "/api/records/"+fixtureID)
	}
	if got.auth != "" {
		t.Fatalf("Authorization header was sent: %q", got.auth)
	}
	if got.accept != "application/json" {
		t.Fatalf("Accept = %q, want application/json", got.accept)
	}

	assertObservation(t, observation, auditext.AvailabilityAvailable, reasonReadSucceeded)
	if observation.TenantID != testScope().TenantID || observation.CaseID != testScope().CaseID {
		t.Fatalf("tenant/case = %q/%q, want %q/%q", observation.TenantID, observation.CaseID, testScope().TenantID, testScope().CaseID)
	}
	if observation.ConnectorID != "fixture.oss" || observation.SourceSystem != "fixture-oss-east" {
		t.Fatalf("connector/source = %q/%q", observation.ConnectorID, observation.SourceSystem)
	}
	if observation.ObjectBindingVersion != "fixture-binding-v1" {
		t.Fatalf("binding version = %q", observation.ObjectBindingVersion)
	}
	if observation.ExternalObjectID != fixtureID || observation.RecordID != fixtureID {
		t.Fatalf("external/record id = %q/%q", observation.ExternalObjectID, observation.RecordID)
	}
	if observation.RecordVersion == nil || *observation.RecordVersion != fixtureVersion {
		t.Fatalf("record version = %v, want %q", observation.RecordVersion, fixtureVersion)
	}
	if !observation.ObservedAt.Equal(fixedNow) {
		t.Fatalf("observed_at = %v, want %v", observation.ObservedAt, fixedNow)
	}
	wantUpdated, _ := time.Parse(time.RFC3339, fixtureUpdatedAt)
	if observation.SourceUpdatedAt == nil || !observation.SourceUpdatedAt.Equal(wantUpdated) {
		t.Fatalf("source_updated_at = %v, want %v", observation.SourceUpdatedAt, wantUpdated)
	}
	wantFrom, _ := time.Parse(time.RFC3339, fixtureFrom)
	wantUntil, _ := time.Parse(time.RFC3339, fixtureUntil)
	if !observation.EffectiveFrom.Equal(wantFrom) || !observation.EffectiveUntil.Equal(wantUntil) {
		t.Fatalf("effective interval = %v..%v", observation.EffectiveFrom, observation.EffectiveUntil)
	}
	if observation.DataClass != auditext.DataClassSynthetic {
		t.Fatalf("data class = %q", observation.DataClass)
	}

	field, ok := observation.FieldByName("configured_bandwidth")
	if !ok {
		t.Fatalf("configured_bandwidth field missing: %+v", observation.Fields)
	}
	if field.Value != "80" || field.Unit != "Mbit/s" {
		t.Fatalf("field = %+v", field)
	}
	if field.SourcePointer != "/serviceCharacteristic/0/value" {
		t.Fatalf("source pointer = %q, want /serviceCharacteristic/0/value", field.SourcePointer)
	}
	if field.SemanticKind != "configured_value" {
		t.Fatalf("semantic kind = %q, want configured_value", field.SemanticKind)
	}

	// The snapshot id follows the documented formula, including exact raw
	// payload bytes, and is stable across repeated reads with a fixed clock.
	expectedHash := sha256.Sum256([]byte(
		strings.Join([]string{
			"fixture.oss",
			fixtureID,
			fixtureID,
			fixtureVersion,
			fixedNow.UTC().Format(time.RFC3339Nano),
		}, snapshotSeparator) + snapshotSeparator + body,
	))
	expectedSnapshot := hex.EncodeToString(expectedHash[:])
	if observation.SnapshotID != expectedSnapshot {
		t.Fatalf("snapshot id = %q, want %q", observation.SnapshotID, expectedSnapshot)
	}
	if observation.PayloadRef == nil || *observation.PayloadRef != DefaultPayloadPrefix+expectedSnapshot {
		t.Fatalf("payload ref = %v, want %q", observation.PayloadRef, DefaultPayloadPrefix+expectedSnapshot)
	}

	second, err := connector.Read(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("second Read() error = %v", err)
	}
	if second.SnapshotID != observation.SnapshotID {
		t.Fatalf("snapshot id is not deterministic: %q vs %q", second.SnapshotID, observation.SnapshotID)
	}
	if second.PayloadRef == nil || observation.PayloadRef == nil || *second.PayloadRef != *observation.PayloadRef {
		t.Fatalf("payload ref is not deterministic: %v vs %v", second.PayloadRef, observation.PayloadRef)
	}
}

func TestCustomPayloadPrefix(t *testing.T) {
	body := recordJSONWith(t, nil)
	server := serveBody(t, http.StatusOK, body)
	cfg := baseConfig(server.URL)
	cfg.PayloadPrefix = "test://snapshots/"
	connector := newTestConnector(t, cfg)

	observation, err := connector.Read(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if observation.PayloadRef == nil || *observation.PayloadRef != "test://snapshots/"+observation.SnapshotID {
		t.Fatalf("payload ref = %v", observation.PayloadRef)
	}
}

func TestExternalObjectMismatch(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"different id", func(fields map[string]any) { fields["id"] = "fixture-line-002" }},
		{"missing id", func(fields map[string]any) { delete(fields, "id") }},
		{"numeric id", func(fields map[string]any) { fields["id"] = 42 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := recordJSONWith(t, tc.mutate)
			server := serveBody(t, http.StatusOK, body)
			connector := newTestConnector(t, baseConfig(server.URL))
			observation, err := connector.Read(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			assertObservation(t, observation, auditext.AvailabilityRejected, reasonExternalObjectMismatch)
			if observation.ExternalObjectID != fixtureID {
				t.Fatalf("external object id = %q, want server-bound %q", observation.ExternalObjectID, fixtureID)
			}
			if observation.PayloadRef != nil {
				t.Fatalf("rejected observation has payload ref %q", *observation.PayloadRef)
			}
		})
	}
}

func TestRequiredFieldMissing(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"no characteristics", func(fields map[string]any) { delete(fields, "serviceCharacteristic") }},
		{"empty array", func(fields map[string]any) { fields["serviceCharacteristic"] = []map[string]any{} }},
		{"wrong name", func(fields map[string]any) {
			fields["serviceCharacteristic"] = []map[string]any{{"name": "other", "value": "80", "unit": "Mbit/s"}}
		}},
		{"wrong unit", func(fields map[string]any) {
			fields["serviceCharacteristic"] = []map[string]any{{"name": "configuredBandwidth", "value": "80", "unit": "Kbit/s"}}
		}},
		{"non numeric value", func(fields map[string]any) {
			fields["serviceCharacteristic"] = []map[string]any{{"name": "configuredBandwidth", "value": "eighty", "unit": "Mbit/s"}}
		}},
		{"empty value", func(fields map[string]any) {
			fields["serviceCharacteristic"] = []map[string]any{{"name": "configuredBandwidth", "value": "", "unit": "Mbit/s"}}
		}},
		{"numeric value type", func(fields map[string]any) {
			fields["serviceCharacteristic"] = []map[string]any{{"name": "configuredBandwidth", "value": 80, "unit": "Mbit/s"}}
		}},
		{"missing unit", func(fields map[string]any) {
			fields["serviceCharacteristic"] = []map[string]any{{"name": "configuredBandwidth", "value": "80"}}
		}},
		{"characteristics not an array", func(fields map[string]any) {
			fields["serviceCharacteristic"] = map[string]any{"name": "configuredBandwidth"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := serveBody(t, http.StatusOK, recordJSONWith(t, tc.mutate))
			connector := newTestConnector(t, baseConfig(server.URL))
			observation, err := connector.Read(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			assertObservation(t, observation, auditext.AvailabilityRejected, reasonRequiredFieldMissing)
			if len(observation.Fields) != 0 {
				t.Fatalf("rejected observation has fields: %+v", observation.Fields)
			}
			if observation.PayloadRef != nil {
				t.Fatalf("rejected observation has payload ref")
			}
		})
	}
}

func TestEffectiveIntervalMissing(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing from", func(fields map[string]any) { delete(fields, "effectiveFrom") }},
		{"missing until", func(fields map[string]any) { delete(fields, "effectiveUntil") }},
		{"null from", func(fields map[string]any) { fields["effectiveFrom"] = nil }},
		{"unparseable from", func(fields map[string]any) { fields["effectiveFrom"] = "yesterday" }},
		{"unparseable until", func(fields map[string]any) { fields["effectiveUntil"] = "not-a-time" }},
		{"empty interval", func(fields map[string]any) {
			fields["effectiveFrom"] = fixtureUntil
			fields["effectiveUntil"] = fixtureUntil
		}},
		{"reversed interval", func(fields map[string]any) {
			fields["effectiveFrom"] = fixtureUntil
			fields["effectiveUntil"] = fixtureFrom
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := serveBody(t, http.StatusOK, recordJSONWith(t, tc.mutate))
			connector := newTestConnector(t, baseConfig(server.URL))
			observation, err := connector.Read(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			assertObservation(t, observation, auditext.AvailabilityRejected, reasonEffectiveIntervalMiss)
		})
	}
}

func TestInvalidUpdatedAtRejected(t *testing.T) {
	server := serveBody(t, http.StatusOK, recordJSONWith(t, func(fields map[string]any) {
		fields["updatedAt"] = "not-a-timestamp"
	}))
	connector := newTestConnector(t, baseConfig(server.URL))
	observation, err := connector.Read(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	assertObservation(t, observation, auditext.AvailabilityRejected, reasonSourceUpdatedAtInvalid)
}

func TestMalformedPayloadRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty", ""},
		{"plain text", "not json"},
		{"null", "null"},
		{"array", `[{"id":"fixture-line-001"}]`},
		{"truncated", `{"id":"fixture-line-001"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := serveBody(t, http.StatusOK, tc.body)
			connector := newTestConnector(t, baseConfig(server.URL))
			observation, err := connector.Read(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			assertObservation(t, observation, auditext.AvailabilityRejected, reasonSourceResponseInvalid)
		})
	}
}

func TestHTTPStatusMapping(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		availability auditext.Availability
		reason       string
	}{
		{"not found", http.StatusNotFound, auditext.AvailabilityUnavailable, reasonSourceNotFound},
		{"gone", http.StatusGone, auditext.AvailabilityUnavailable, reasonSourceNotFound},
		{"rate limited", http.StatusTooManyRequests, auditext.AvailabilityUnavailable, reasonSourceRateLimited},
		{"server error", http.StatusInternalServerError, auditext.AvailabilityUnavailable, reasonSourceUnavailable},
		{"bad gateway", http.StatusBadGateway, auditext.AvailabilityUnavailable, reasonSourceUnavailable},
		{"request timeout", http.StatusRequestTimeout, auditext.AvailabilityUnavailable, reasonSourceTimeout},
		{"bad request", http.StatusBadRequest, auditext.AvailabilityUnavailable, reasonSourceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := serveBody(t, tc.status, "upstream body that must not become evidence")
			connector := newTestConnector(t, baseConfig(server.URL))
			observation, err := connector.Read(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			assertObservation(t, observation, tc.availability, tc.reason)
			if len(observation.Fields) != 0 || observation.PayloadRef != nil {
				t.Fatalf("failure observation carries evidence: %+v", observation)
			}
		})
	}
}

func TestUnauthorizedStatus(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := serveBody(t, status, "secret upstream body")
			connector := newTestConnector(t, baseConfig(server.URL))
			observation, err := connector.Read(context.Background(), testRequest())
			if !errors.Is(err, auditext.ErrUnauthorized) {
				t.Fatalf("error = %v, want auditext.ErrUnauthorized", err)
			}
			if !reflect.DeepEqual(observation, auditext.Observation{}) {
				t.Fatalf("unauthorized read leaked observation: %+v", observation)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("error leaked upstream body: %v", err)
			}
		})
	}
}

func TestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	cfg := baseConfig(server.URL)
	cfg.Timeout = 50 * time.Millisecond
	connector := newTestConnector(t, cfg)
	observation, err := connector.Read(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	assertObservation(t, observation, auditext.AvailabilityUnavailable, reasonSourceTimeout)
}

func TestCanceledContext(t *testing.T) {
	server := serveBody(t, http.StatusOK, recordJSONWith(t, nil))
	connector := newTestConnector(t, baseConfig(server.URL))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	observation, err := connector.Read(ctx, testRequest())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if observation.Availability != auditext.AvailabilityUnavailable {
		t.Fatalf("availability = %q, want unavailable", observation.Availability)
	}
	if observation.ReasonCode != reasonSourceTimeout && observation.ReasonCode != reasonSourceUnavailable {
		t.Fatalf("reason = %q, want source_timeout or source_unavailable", observation.ReasonCode)
	}
	if err := observation.Validate(testScope()); err != nil {
		t.Fatalf("observation does not validate: %v", err)
	}
}

func TestResponseTooLarge(t *testing.T) {
	cases := []struct {
		name   string
		stream bool
	}{
		{"content length", false},
		{"chunked", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.stream {
					w.WriteHeader(http.StatusOK)
					if flusher, ok := w.(http.Flusher); ok {
						flusher.Flush()
					}
				}
				_, _ = w.Write([]byte(strings.Repeat("x", 1024)))
			}))
			t.Cleanup(server.Close)

			cfg := baseConfig(server.URL)
			cfg.MaxResponseBytes = 64
			connector := newTestConnector(t, cfg)
			observation, err := connector.Read(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			assertObservation(t, observation, auditext.AvailabilityRejected, reasonSourceResponseTooLarge)
		})
	}
}

func TestRedirectNotFollowed(t *testing.T) {
	t.Run("default client", func(t *testing.T) {
		var targetHits int32
		mux := http.NewServeMux()
		mux.HandleFunc("/records/"+fixtureID, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/target", http.StatusFound)
		})
		mux.HandleFunc("/target", func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&targetHits, 1)
			_, _ = io.WriteString(w, recordJSONWith(t, nil))
		})
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)

		connector := newTestConnector(t, baseConfig(server.URL))
		observation, err := connector.Read(context.Background(), testRequest())
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		assertObservation(t, observation, auditext.AvailabilityRejected, reasonSourceRedirectRejected)
		if hits := atomic.LoadInt32(&targetHits); hits != 0 {
			t.Fatalf("redirect target was followed %d times", hits)
		}
	})

	t.Run("caller client cannot enable redirects", func(t *testing.T) {
		var targetHits int32
		mux := http.NewServeMux()
		mux.HandleFunc("/records/"+fixtureID, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/target", http.StatusFound)
		})
		mux.HandleFunc("/target", func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&targetHits, 1)
			_, _ = io.WriteString(w, recordJSONWith(t, nil))
		})
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)

		cfg := baseConfig(server.URL)
		cfg.HTTPClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
		connector := newTestConnector(t, cfg)
		observation, err := connector.Read(context.Background(), testRequest())
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		assertObservation(t, observation, auditext.AvailabilityRejected, reasonSourceRedirectRejected)
		if hits := atomic.LoadInt32(&targetHits); hits != 0 {
			t.Fatalf("redirect target was followed %d times", hits)
		}
	})
}

func TestURLInjectionRejected(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = io.WriteString(w, recordJSONWith(t, nil))
	}))
	t.Cleanup(server.Close)

	connector := newTestConnector(t, baseConfig(server.URL+"/base"))
	objects := []string{
		"../secrets",
		"../../etc/passwd",
		"a/../b",
		"a/./b",
		"http://evil.example/x",
		"https://evil.example",
		"HTTP://evil.example/x",
		`a\b`,
		"%2e%2e%2f",
		"a?x=1",
		"a#frag",
		"..",
		"",
		"-leading-dash",
		"a b",
	}
	for _, objectID := range objects {
		t.Run(objectID, func(t *testing.T) {
			request := testRequest()
			request.ExternalObjectID = objectID
			observation, err := connector.Read(context.Background(), request)
			if !errors.Is(err, auditext.ErrInvalidRequest) {
				t.Fatalf("Read(%q) error = %v, want auditext.ErrInvalidRequest", objectID, err)
			}
			if !reflect.DeepEqual(observation, auditext.Observation{}) {
				t.Fatalf("Read(%q) returned observation: %+v", objectID, observation)
			}
		})
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("invalid object ids reached the source %d times", got)
	}
}

func TestObjectIDIsEscapedIntoOneSegment(t *testing.T) {
	objectID := "line:001/part"
	body := recordJSONWith(t, func(fields map[string]any) {
		fields["id"] = objectID
	})
	var mu sync.Mutex
	var seenURI, seenPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenURI = r.RequestURI
		seenPath = r.URL.Path
		mu.Unlock()
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	connector := newTestConnector(t, baseConfig(server.URL))
	request := testRequest()
	request.ExternalObjectID = objectID
	observation, err := connector.Read(context.Background(), request)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	assertObservation(t, observation, auditext.AvailabilityAvailable, reasonReadSucceeded)
	if observation.ExternalObjectID != objectID || observation.RecordID != objectID {
		t.Fatalf("external/record id = %q/%q, want %q", observation.ExternalObjectID, observation.RecordID, objectID)
	}

	mu.Lock()
	gotURI, gotPath := seenURI, seenPath
	mu.Unlock()
	if gotURI != "/records/line:001%2Fpart" {
		t.Fatalf("request URI = %q, want the identifier escaped into one segment", gotURI)
	}
	if gotPath != "/records/line:001/part" {
		t.Fatalf("decoded path = %q", gotPath)
	}
}

func TestFreshness(t *testing.T) {
	cases := []struct {
		name      string
		updatedAt string
		freshness time.Duration
		want      auditext.Availability
		reason    string
	}{
		{
			name:      "fresh",
			updatedAt: fixedNow.Add(-5 * time.Minute).Format(time.RFC3339),
			freshness: 30 * time.Minute,
			want:      auditext.AvailabilityAvailable,
			reason:    reasonReadSucceeded,
		},
		{
			name:      "exactly at boundary is not stale",
			updatedAt: fixedNow.Add(-30 * time.Minute).Format(time.RFC3339),
			freshness: 30 * time.Minute,
			want:      auditext.AvailabilityAvailable,
			reason:    reasonReadSucceeded,
		},
		{
			name:      "stale",
			updatedAt: fixedNow.Add(-31 * time.Minute).Format(time.RFC3339),
			freshness: 30 * time.Minute,
			want:      auditext.AvailabilityStale,
			reason:    reasonSourceStale,
		},
		{
			name:      "staleness disabled",
			updatedAt: fixedNow.Add(-100 * time.Hour).Format(time.RFC3339),
			freshness: 0,
			want:      auditext.AvailabilityAvailable,
			reason:    reasonReadSucceeded,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := recordJSONWith(t, func(fields map[string]any) {
				fields["updatedAt"] = tc.updatedAt
			})
			server := serveBody(t, http.StatusOK, body)
			cfg := baseConfig(server.URL)
			cfg.Freshness = tc.freshness
			connector := newTestConnector(t, cfg)

			observation, err := connector.Read(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			assertObservation(t, observation, tc.want, tc.reason)
			if _, ok := observation.FieldByName("configured_bandwidth"); !ok {
				t.Fatalf("field missing: %+v", observation.Fields)
			}
			if observation.PayloadRef == nil {
				t.Fatalf("payload ref missing")
			}
		})
	}
}

func TestScopeNotOverriddenByResponseBody(t *testing.T) {
	body := recordJSONWith(t, func(fields map[string]any) {
		fields["tenant_id"] = "attacker-tenant"
		fields["case_id"] = "attacker-case"
		fields["connector_id"] = "attacker.connector"
		fields["source_system"] = "attacker-source"
		fields["object_binding_version"] = "attacker-binding"
		fields["external_object_id"] = "attacker-object"
		fields["data_class"] = "production"
	})
	server := serveBody(t, http.StatusOK, body)
	connector := newTestConnector(t, baseConfig(server.URL))
	observation, err := connector.Read(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	scope := testScope()
	if observation.TenantID != scope.TenantID || observation.CaseID != scope.CaseID {
		t.Fatalf("body overrode tenant/case: %q/%q", observation.TenantID, observation.CaseID)
	}
	if observation.ConnectorID != "fixture.oss" || observation.SourceSystem != "fixture-oss-east" {
		t.Fatalf("body overrode connector/source: %q/%q", observation.ConnectorID, observation.SourceSystem)
	}
	if observation.ObjectBindingVersion != "fixture-binding-v1" {
		t.Fatalf("body overrode binding version: %q", observation.ObjectBindingVersion)
	}
	if observation.ExternalObjectID != fixtureID {
		t.Fatalf("body overrode external object id: %q", observation.ExternalObjectID)
	}
	if observation.DataClass != auditext.DataClassSynthetic {
		t.Fatalf("body overrode data class: %q", observation.DataClass)
	}
}

func TestAllowedObjects(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = io.WriteString(w, recordJSONWith(t, nil))
	}))
	t.Cleanup(server.Close)

	cfg := baseConfig(server.URL)
	cfg.AllowedObjects = []string{fixtureID}
	connector := newTestConnector(t, cfg)

	observation, err := connector.Read(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	assertObservation(t, observation, auditext.AvailabilityAvailable, reasonReadSucceeded)

	request := testRequest()
	request.ExternalObjectID = "fixture-line-002"
	observation, err = connector.Read(context.Background(), request)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	assertObservation(t, observation, auditext.AvailabilityRejected, reasonObjectNotAllowed)
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("source hit %d times, want 1 (disallowed object must not be fetched)", got)
	}
}

func TestReadRejectsInvalidRequest(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	t.Cleanup(server.Close)

	connector := newTestConnector(t, baseConfig(server.URL))
	cases := []struct {
		name   string
		mutate func(*auditext.ReadRequest)
	}{
		{"missing tenant", func(r *auditext.ReadRequest) { r.Scope.TenantID = "" }},
		{"invalid case", func(r *auditext.ReadRequest) { r.Scope.CaseID = "bad case" }},
		{"missing actor", func(r *auditext.ReadRequest) { r.Scope.ActorID = "" }},
		{"missing grant", func(r *auditext.ReadRequest) { r.Scope.GrantVersion = "" }},
		{"wrong connector", func(r *auditext.ReadRequest) { r.ConnectorID = "other.connector" }},
		{"empty connector", func(r *auditext.ReadRequest) { r.ConnectorID = "" }},
		{"missing binding", func(r *auditext.ReadRequest) { r.BindingVersion = "" }},
		{"invalid binding", func(r *auditext.ReadRequest) { r.BindingVersion = "../binding" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := testRequest()
			tc.mutate(&request)
			observation, err := connector.Read(context.Background(), request)
			if !errors.Is(err, auditext.ErrInvalidRequest) {
				t.Fatalf("Read() error = %v, want auditext.ErrInvalidRequest", err)
			}
			if !reflect.DeepEqual(observation, auditext.Observation{}) {
				t.Fatalf("invalid request returned observation: %+v", observation)
			}
		})
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("invalid requests reached the source %d times", got)
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing connector id", func(c *Config) { c.ConnectorID = "" }},
		{"invalid connector id", func(c *Config) { c.ConnectorID = "bad id" }},
		{"missing source system", func(c *Config) { c.SourceSystem = "" }},
		{"invalid source system", func(c *Config) { c.SourceSystem = "../source" }},
		{"empty base url", func(c *Config) { c.BaseURL = "" }},
		{"non http scheme", func(c *Config) { c.BaseURL = "ftp://example.invalid" }},
		{"scheme relative", func(c *Config) { c.BaseURL = "//example.invalid" }},
		{"no scheme", func(c *Config) { c.BaseURL = "example.invalid" }},
		{"userinfo", func(c *Config) { c.BaseURL = "http://user:pass@example.invalid" }},
		{"username only", func(c *Config) { c.BaseURL = "http://user@example.invalid" }},
		{"query", func(c *Config) { c.BaseURL = "http://example.invalid?x=1" }},
		{"empty query marker", func(c *Config) { c.BaseURL = "http://example.invalid/?" }},
		{"fragment", func(c *Config) { c.BaseURL = "http://example.invalid/#frag" }},
		{"empty fragment marker", func(c *Config) { c.BaseURL = "http://example.invalid/#" }},
		{"missing host", func(c *Config) { c.BaseURL = "http://" }},
		{"response limit too large", func(c *Config) { c.MaxResponseBytes = MaxResponseBytesLimit + 1 }},
		{"negative response limit", func(c *Config) { c.MaxResponseBytes = -1 }},
		{"payload prefix too long", func(c *Config) { c.PayloadPrefix = strings.Repeat("p", 449) }},
		{"invalid allowed object", func(c *Config) { c.AllowedObjects = []string{"../escape"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseConfig("https://example.invalid")
			tc.mutate(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatalf("New() succeeded for invalid config")
			}
		})
	}
}

func TestNewAcceptsMaximumResponseBytes(t *testing.T) {
	cfg := baseConfig("https://example.invalid")
	cfg.MaxResponseBytes = MaxResponseBytesLimit
	connector, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if connector.maxResponseBytes != MaxResponseBytesLimit {
		t.Fatalf("max response bytes = %d, want %d", connector.maxResponseBytes, MaxResponseBytesLimit)
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	cfg := Config{
		ConnectorID:  "fixture.oss",
		BaseURL:      "https://example.invalid/base",
		SourceSystem: "fixture-oss-east",
	}
	connector, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if connector.ID() != "fixture.oss" {
		t.Fatalf("ID() = %q", connector.ID())
	}
	if connector.maxResponseBytes != DefaultMaxResponseBytes {
		t.Fatalf("max response bytes = %d, want %d", connector.maxResponseBytes, DefaultMaxResponseBytes)
	}
	if connector.timeout != DefaultTimeout {
		t.Fatalf("timeout = %v, want %v", connector.timeout, DefaultTimeout)
	}
	if connector.payloadPrefix != DefaultPayloadPrefix {
		t.Fatalf("payload prefix = %q, want %q", connector.payloadPrefix, DefaultPayloadPrefix)
	}
	if connector.clock == nil {
		t.Fatalf("clock is nil")
	}
	if got := connector.endpoint("a.b-1"); got != "https://example.invalid/base/records/a.b-1" {
		t.Fatalf("endpoint = %q", got)
	}
}

func TestConfiguredBandwidthUnits(t *testing.T) {
	for _, unit := range []string{"Mbit/s", "Gbit/s"} {
		t.Run(unit, func(t *testing.T) {
			value := "80"
			if unit == "Gbit/s" {
				value = "0.1"
			}
			server := serveBody(t, http.StatusOK, recordJSONWith(t, func(fields map[string]any) {
				fields["serviceCharacteristic"] = []map[string]any{
					{"name": "configuredBandwidth", "value": value, "unit": unit},
				}
			}))
			connector := newTestConnector(t, baseConfig(server.URL))
			observation, err := connector.Read(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			assertObservation(t, observation, auditext.AvailabilityAvailable, reasonReadSucceeded)
			field, ok := observation.FieldByName("configured_bandwidth")
			if !ok {
				t.Fatalf("field missing")
			}
			if field.Value != value || field.Unit != unit {
				t.Fatalf("field = %+v, want %q %q", field, value, unit)
			}
			if field.SourcePointer != "/serviceCharacteristic/0/value" {
				t.Fatalf("pointer = %q", field.SourcePointer)
			}
		})
	}
}

func TestSourcePointerTargetsActualElement(t *testing.T) {
	server := serveBody(t, http.StatusOK, recordJSONWith(t, func(fields map[string]any) {
		fields["serviceCharacteristic"] = []map[string]any{
			{"name": "other", "value": "1", "unit": "Mbit/s"},
			{"name": "configuredBandwidth", "value": "80", "unit": "Mbit/s"},
		}
	}))
	connector := newTestConnector(t, baseConfig(server.URL))
	observation, err := connector.Read(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	assertObservation(t, observation, auditext.AvailabilityAvailable, reasonReadSucceeded)
	field, ok := observation.FieldByName("configured_bandwidth")
	if !ok {
		t.Fatalf("field missing")
	}
	if field.SourcePointer != "/serviceCharacteristic/1/value" {
		t.Fatalf("pointer = %q, want /serviceCharacteristic/1/value", field.SourcePointer)
	}
}

func TestNetworkErrorIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := server.URL
	server.Close()

	connector := newTestConnector(t, baseConfig(baseURL))
	observation, err := connector.Read(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if observation.Availability != auditext.AvailabilityUnavailable {
		t.Fatalf("availability = %q, want unavailable", observation.Availability)
	}
	if observation.ReasonCode != reasonSourceUnavailable && observation.ReasonCode != reasonSourceTimeout {
		t.Fatalf("reason = %q", observation.ReasonCode)
	}
	if err := observation.Validate(testScope()); err != nil {
		t.Fatalf("observation does not validate: %v", err)
	}
}

func TestRecordVersionInvalid(t *testing.T) {
	server := serveBody(t, http.StatusOK, recordJSONWith(t, func(fields map[string]any) {
		fields["version"] = strings.Repeat("v", 129)
	}))
	connector := newTestConnector(t, baseConfig(server.URL))
	observation, err := connector.Read(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	assertObservation(t, observation, auditext.AvailabilityRejected, reasonRecordVersionInvalid)
}

func TestMissingVersionAndUpdatedAtAreNil(t *testing.T) {
	server := serveBody(t, http.StatusOK, recordJSONWith(t, func(fields map[string]any) {
		delete(fields, "version")
		delete(fields, "updatedAt")
	}))
	connector := newTestConnector(t, baseConfig(server.URL))
	observation, err := connector.Read(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	assertObservation(t, observation, auditext.AvailabilityAvailable, reasonReadSucceeded)
	if observation.RecordVersion != nil {
		t.Fatalf("record version = %v, want nil", observation.RecordVersion)
	}
	if observation.SourceUpdatedAt != nil {
		t.Fatalf("source updated at = %v, want nil", observation.SourceUpdatedAt)
	}
}

func TestFailureSnapshotIDIsDeterministic(t *testing.T) {
	server := serveBody(t, http.StatusNotFound, "missing")
	connector := newTestConnector(t, baseConfig(server.URL))
	first, err := connector.Read(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	second, err := connector.Read(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if first.SnapshotID == "" || len(first.SnapshotID) != 64 {
		t.Fatalf("snapshot id = %q", first.SnapshotID)
	}
	if !strings.EqualFold(first.SnapshotID, second.SnapshotID) {
		t.Fatalf("snapshot id is not deterministic: %q vs %q", first.SnapshotID, second.SnapshotID)
	}
	if first.PayloadRef != nil {
		t.Fatalf("failure observation has payload ref")
	}
}

func TestObservationNeverClaimsValidityOnFailure(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"unavailable", http.StatusServiceUnavailable, ""},
		{"rejected with valid-looking interval", http.StatusOK, recordJSONWith(t, func(fields map[string]any) {
			fields["id"] = "fixture-line-002"
		})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := serveBody(t, tc.status, tc.body)
			connector := newTestConnector(t, baseConfig(server.URL))
			observation, err := connector.Read(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			if observation.Availability == auditext.AvailabilityAvailable || observation.Availability == auditext.AvailabilityStale {
				t.Fatalf("failure was reported as %q", observation.Availability)
			}
			if observation.EffectiveAt(fixedNow) {
				t.Fatalf("failure observation claims coverage of the business instant")
			}
			if observation.EffectiveAt(time.Time{}) {
				t.Fatalf("failure observation claims coverage of the zero instant")
			}
			if len(observation.Fields) != 0 {
				t.Fatalf("failure observation carries fields: %+v", observation.Fields)
			}
		})
	}
}

func ExampleConnector_Read() {
	body := `{"id":"fixture-line-001","version":"fixture-source-v1","updatedAt":"2026-10-28T07:55:00Z","effectiveFrom":"2026-10-01T00:00:00Z","effectiveUntil":"2026-11-01T00:00:00Z","serviceCharacteristic":[{"name":"configuredBandwidth","value":"80","unit":"Mbit/s"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()

	connector, err := New(Config{
		ConnectorID:  "fixture.oss",
		BaseURL:      server.URL,
		SourceSystem: "fixture-oss-east",
		Clock:        func() time.Time { return fixedNow },
	})
	if err != nil {
		panic(err)
	}
	observation, err := connector.Read(context.Background(), auditext.ReadRequest{
		Scope:            testScope(),
		ConnectorID:      "fixture.oss",
		BindingVersion:   "fixture-binding-v1",
		ExternalObjectID: "fixture-line-001",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(observation.Availability, observation.ReasonCode)
	field, _ := observation.FieldByName("configured_bandwidth")
	fmt.Println(field.Name, field.Value, field.Unit, field.SourcePointer)
	// Output:
	// available read_succeeded
	// configured_bandwidth 80 Mbit/s /serviceCharacteristic/0/value
}
