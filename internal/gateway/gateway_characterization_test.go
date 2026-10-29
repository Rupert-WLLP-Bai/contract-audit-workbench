package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewPreservesSafeConfigurationFailureMessages(t *testing.T) {
	tooManyReplicas := make([]string, maxReplicasPerService+1)
	for i := range tooManyReplicas {
		tooManyReplicas[i] = "http://core.example.test"
	}

	for _, test := range []struct {
		name    string
		change  func(*Config)
		wantErr string
	}{
		{
			name:    "too many replicas",
			change:  func(config *Config) { config.CoreURLs = tooManyReplicas },
			wantErr: "core supports at most 16 upstream replicas",
		},
		{
			name:    "unsafe URL does not echo credentials",
			change:  func(config *Config) { config.CoreURLs = []string{"http://alice:secret@core.example.test"} },
			wantErr: "the core upstream URL is not an HTTP(S) base URL without credentials, query, or path prefix",
		},
		{
			name:    "unsupported URL scheme",
			change:  func(config *Config) { config.CoreURLs = []string{"ftp://core.example.test"} },
			wantErr: "the core upstream URL is not an HTTP(S) base URL without credentials, query, or path prefix",
		},
		{
			name: "invalid health interval precedes missing target",
			change: func(config *Config) {
				config.CoreURLs = nil
				config.HealthInterval = minHealthInterval - time.Nanosecond
			},
			wantErr: "GATEWAY_HEALTH_INTERVAL must be between 100ms and 30s",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := baseConfig()
			test.change(&config)
			_, err := New(config)
			if err == nil || err.Error() != test.wantErr {
				t.Fatalf("New() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestNewAcceptsExactReplicaAndCredentialLimits(t *testing.T) {
	config := baseConfig()
	config.CoreURLs = make([]string, maxReplicasPerService)
	for i := range config.CoreURLs {
		config.CoreURLs[i] = "http://core.example.test"
	}
	config.CoreToken = strings.Repeat("c", 32)

	gateway, err := New(config)
	if err != nil {
		t.Fatalf("New() rejected the exact supported replica and credential limits: %v", err)
	}
	gateway.Close()

	config = baseConfig()
	config.CoreToken = strings.Repeat("c", 31)
	if _, err := New(config); err == nil || err.Error() != "GATEWAY_CORE_TOKEN must hold a private credential of at least 32 bytes" {
		t.Fatalf("New() with a 31-byte core credential returned %v", err)
	}
}

func TestDocumentOriginalStreamOutlivesRequestTimeoutAndCancelsUpstream(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/cases/case-1/documents/doc-1/original" {
			t.Errorf("unexpected document request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = io.WriteString(w, "original-chunk")
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-release:
		}
	}))
	t.Cleanup(upstream.Close)

	config := baseConfig()
	config.DocumentURL = upstream.URL
	config.RequestTimeout = 40 * time.Millisecond
	edge := startGateway(t, config)

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		edge.URL+"/api/cases/case-1/documents/doc-1/original", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("document original request failed: %v", err)
	}
	first := make([]byte, len("original-chunk"))
	read := make(chan error, 1)
	go func() {
		_, readErr := io.ReadFull(response.Body, first)
		read <- readErr
	}()
	select {
	case readErr := <-read:
		if readErr != nil {
			t.Fatalf("document original stream read failed: %v", readErr)
		}
	case <-time.After(time.Second):
		t.Fatal("document original bytes were buffered until stream completion")
	}
	if string(first) != "original-chunk" {
		t.Fatalf("document original stream delivered %q", first)
	}
	select {
	case <-cancelled:
		t.Fatal("ordinary request timeout cancelled the document original stream")
	case <-time.After(3 * config.RequestTimeout):
	}

	cancel()
	_ = response.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("client cancellation did not cancel the document original request")
	}
}

func TestRoundTripReplaysSafeReadBodyAfterReplicaFailure(t *testing.T) {
	firstTarget, err := baseURL("http://first.example.test")
	if err != nil {
		t.Fatal(err)
	}
	secondTarget, err := baseURL("http://second.example.test")
	if err != nil {
		t.Fatal(err)
	}
	upstream := &upstream{
		role: "core",
		replicas: []*replica{
			{target: firstTarget, healthy: true, checked: true},
			{target: secondTarget, healthy: true, checked: true},
		},
		failureThreshold:  1,
		recoveryThreshold: 1,
	}
	var bodies []string
	transport := &replicaTransport{
		upstream: upstream,
		base: probeRoundTripper(func(request *http.Request) (*http.Response, error) {
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				return nil, readErr
			}
			bodies = append(bodies, string(body))
			if len(bodies) == 1 {
				return nil, errors.New("first replica closed before responding")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("ok")),
				Request:    request,
			}, nil
		}),
	}
	request, err := http.NewRequest(http.MethodGet, "http://edge.example.test/api/cases", strings.NewReader("safe-read-body"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatalf("safe GET did not fail over: %v", err)
	}
	_ = response.Body.Close()
	if len(bodies) != 2 || bodies[0] != "safe-read-body" || bodies[1] != "safe-read-body" {
		t.Fatalf("retry bodies = %q, want two copies of the original request body", bodies)
	}
}

func TestRoundTripLogsContextFailureWithoutResponseStatus(t *testing.T) {
	target, err := baseURL("http://core.example.test")
	if err != nil {
		t.Fatal(err)
	}
	upstream := &upstream{
		role: "core",
		replicas: []*replica{
			{target: target, healthy: true, checked: true},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	transport := &replicaTransport{
		upstream: upstream,
		base: probeRoundTripper(func(*http.Request) (*http.Response, error) {
			cancel()
			return nil, context.Canceled
		}),
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://edge.example.test/api/cases", nil)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previousLogOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousLogOutput) })
	if _, err := transport.RoundTrip(request); !errors.Is(err, context.Canceled) {
		t.Fatalf("RoundTrip error = %v, want context cancellation", err)
	}
	if !strings.Contains(logs.String(), "method=GET outcome=context_cancelled status=0") {
		t.Fatalf("request failure log reports a response status: %q", logs.String())
	}
}

func TestProbeTransitionsKeepFailureAndRecoveryLogDurations(t *testing.T) {
	var logs bytes.Buffer
	previousLogOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousLogOutput) })

	upstream := &upstream{role: "core", failureThreshold: 2, recoveryThreshold: 2}
	replica := &replica{identity: "core-replica"}
	upstream.observeProbe(replica, time.Now().Add(-10*time.Second), true)
	firstFailure := time.Now().Add(-300 * time.Millisecond)
	upstream.observeProbe(replica, firstFailure, false)
	upstream.observeProbe(replica, time.Now(), false)
	if replica.healthy {
		t.Fatal("two failed probes did not quarantine the replica")
	}
	upstream.observeProbe(replica, time.Now(), true)
	if replica.healthy {
		t.Fatal("one successful probe crossed the two-success recovery threshold")
	}
	upstream.observeProbe(replica, time.Now().Add(time.Millisecond), true)
	if !replica.healthy {
		t.Fatal("two successful probes did not restore the replica")
	}

	var initialReady, unavailable, recovered bool
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		switch {
		case strings.Contains(line, "previous=unknown state=healthy source=probe"):
			initialReady = true
			if !strings.Contains(line, "transition_ms=0 ") {
				t.Errorf("initial readiness transition duration = %q, want zero", line)
			}
		case strings.Contains(line, "previous=healthy state=unhealthy source=probe"):
			unavailable = true
			if !transitionDurationBetween(line, 100, 2000) {
				t.Errorf("unhealthy transition did not use the first failure time: %q", line)
			}
		case strings.Contains(line, "previous=unhealthy state=healthy source=probe"):
			recovered = true
			if !transitionDurationBetween(line, 100, 2000) {
				t.Errorf("recovery transition did not use the unhealthy interval: %q", line)
			}
		}
	}
	if !initialReady || !unavailable || !recovered {
		t.Fatalf("probe transition logs missing: initial=%v unavailable=%v recovered=%v logs=%q", initialReady, unavailable, recovered, logs.String())
	}
}

func transitionDurationBetween(line string, minimum, maximum int64) bool {
	for _, field := range strings.Fields(line) {
		if !strings.HasPrefix(field, "transition_ms=") {
			continue
		}
		var elapsed int64
		if _, err := fmt.Sscanf(strings.TrimPrefix(field, "transition_ms="), "%d", &elapsed); err != nil {
			return false
		}
		return elapsed >= minimum && elapsed < maximum
	}
	return false
}
