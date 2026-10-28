package gateway

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func tokenFor(role string) string { return "private-" + role + "-credential-0123456789" }

func baseConfig() Config {
	return Config{
		CoreURLs:       []string{"http://127.0.0.1:1"},
		DocumentURL:    "http://127.0.0.1:1",
		ExecutionURL:   "http://127.0.0.1:1",
		ToolURLs:       []string{"http://127.0.0.1:1"},
		CoreToken:      tokenFor("core"),
		DocumentToken:  tokenFor("document"),
		ExecutionToken: tokenFor("execution"),
		ToolToken:      tokenFor("tool"),

		MaxUploadBytes: defaultMaxUploadBytes,
		MaxInflight:    defaultMaxInflight,
		MaxUploads:     defaultMaxUploads,
		RequestTimeout: defaultRequestTimeout,
		UploadTimeout:  defaultUploadTimeout,
	}
}

func startGateway(t *testing.T, config Config) *httptest.Server {
	t.Helper()
	if config.HealthInterval == 0 {
		config.HealthInterval = minHealthInterval
	}
	if config.HealthTimeout == 0 {
		config.HealthTimeout = minHealthTimeout
	}
	edge, err := New(config)
	if err != nil {
		t.Fatalf("gateway configuration was rejected: %v", err)
	}
	if err := edge.Start(context.Background()); err != nil {
		edge.Close()
		t.Fatalf("gateway health monitor did not start: %v", err)
	}
	server := httptest.NewServer(edge.Handler())
	t.Cleanup(server.Close)
	t.Cleanup(edge.Close)
	return server
}

// waitGatewayReady waits for the first health sweep to mark every configured
// upstream healthy. Tests that use startGateway with live upstreams must call
// this before asserting on routed requests: the probe loop starts
// asynchronously, so an immediate request can legitimately see no healthy
// replica and answer 503.
func waitGatewayReady(t *testing.T, edge *httptest.Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(edge.URL + "/health/ready")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("gateway did not report every configured upstream ready")
}

type captured struct {
	method string
	path   string
	header http.Header
	body   []byte
}

func recordingUpstream(t *testing.T, status int) (*httptest.Server, func() captured) {
	t.Helper()
	seen := make(chan captured, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		body, _ := io.ReadAll(r.Body)
		seen <- captured{method: r.Method, path: r.URL.EscapedPath(), header: r.Header.Clone(), body: body}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"status":"upstream"}`))
	}))
	t.Cleanup(server.Close)
	return server, func() captured {
		t.Helper()
		select {
		case got := <-seen:
			return got
		case <-time.After(3 * time.Second):
			t.Fatal("the owning service never received the request")
			return captured{}
		}
	}
}

// An escaped segment must reach the same service as its literal spelling, and a
// caller must not be able to inject an internal credential, a delegated claim,
// or a forwarded identity.
func TestEscapedPathKeepsRouteAndForgedHeadersAreStripped(t *testing.T) {
	document, received := recordingUpstream(t, http.StatusCreated)
	config := baseConfig()
	config.DocumentURL = document.URL
	edge := startGateway(t, config)

	request, err := http.NewRequest(http.MethodPost, edge.URL+"/api/cases/c/%64ocuments", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Contract-Service-Key", "attacker-supplied-credential-0123456789")
	request.Header.Set("X-Contract-Job-Context", `{"job_id":"attacker"}`)
	request.Header.Set("X-Audit-Execution-Scope", "attacker")
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	request.Header.Set("X-Forwarded-Host", "attacker.example")
	request.Header.Set("X-Real-IP", "203.0.113.7")
	request.Header.Set("Forwarded", "for=203.0.113.7")
	request.Header.Set("X-Original-URL", "/admin/settings")
	request.Header.Set("Cookie", "session=user-session")
	request.Header.Set("Authorization", "Bearer user-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("edge request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("escaped document route answered %d", response.StatusCode)
	}
	got := received()
	if got.method != http.MethodPost || got.path != "/api/cases/c/%64ocuments" || string(got.body) != "payload" {
		t.Fatalf("owning service saw %s %s %q", got.method, got.path, got.body)
	}
	if credential := got.header.Get("X-Contract-Service-Key"); credential != tokenFor("document") {
		t.Fatalf("service credential was %q", credential)
	}
	for _, name := range []string{
		"X-Contract-Job-Context", "X-Audit-Execution-Scope", "X-Forwarded-Host",
		"X-Real-Ip", "Forwarded", "X-Original-Url",
	} {
		if value := strings.Join(got.header.Values(name), ","); value != "" {
			t.Fatalf("forged %s reached the service: %q", name, value)
		}
	}
	if value := strings.Join(got.header.Values("X-Forwarded-For"), ","); strings.Contains(value, "203.0.113.7") {
		t.Fatalf("forged X-Forwarded-For reached the service: %q", value)
	}
	if got.header.Get("Cookie") != "session=user-session" || got.header.Get("Authorization") != "Bearer user-token" {
		t.Fatal("the user's own credentials must pass through untouched")
	}
}

// Traversal, encoded separators, empty segments, and a trailing slash are
// rejected before routing, so no service and no redirect sees them.
func TestInvalidPathsAreRejectedBeforeRouting(t *testing.T) {
	core, _ := recordingUpstream(t, http.StatusOK)
	document, _ := recordingUpstream(t, http.StatusOK)
	config := baseConfig()
	config.CoreURLs = []string{core.URL}
	config.DocumentURL = document.URL
	edge := startGateway(t, config)

	for _, path := range []string{
		"/api/cases/../documents",
		"/api/cases/c%2Fd/documents",
		"/api/cases/./documents",
		"//api/cases",
		"/api/cases/",
	} {
		request, err := http.NewRequest(http.MethodPost, edge.URL+path, strings.NewReader("x"))
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("%s reached the edge but failed: %v", path, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s answered %d instead of 404", path, response.StatusCode)
		}
	}
}

// The upload path is bounded in bytes and in wall time: an oversized body is
// refused, and a client that stops writing mid-body is released by the read
// deadline instead of holding the edge open forever.
func TestUploadIsBoundedInBytesAndWallTime(t *testing.T) {
	document, _ := recordingUpstream(t, http.StatusCreated)
	config := baseConfig()
	config.DocumentURL = document.URL
	config.MaxUploadBytes = 1024
	config.UploadTimeout = 300 * time.Millisecond
	edge := startGateway(t, config)

	response, err := http.Post(edge.URL+"/api/cases/c/documents", "text/plain", strings.NewReader(strings.Repeat("x", 4096)))
	if err != nil {
		t.Fatalf("oversized upload failed: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload answered %d", response.StatusCode)
	}

	conn, err := net.Dial("tcp", strings.TrimPrefix(edge.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "POST /api/cases/c/documents HTTP/1.1\r\nHost: edge\r\nContent-Length: 1000\r\nContent-Type: multipart/form-data; boundary=b\r\n\r\n0123456789"); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	buffer := make([]byte, 1)
	if _, err := conn.Read(buffer); err != nil && !strings.Contains(err.Error(), "EOF") && !strings.Contains(err.Error(), "reset") {
		t.Fatalf("stalled upload read failed with %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("a stalled upload held the edge for %s", elapsed)
	}
}

// A POST is delivered at most once even when the upstream fails mid-request.
//
// The scenario is the one Go's transport replays: a warm reusable connection, a
// bodyless request that carries an idempotency key (which makes it replayable),
// and an upstream that receives the POST and then disappears without answering.
// The service must observe exactly one operation and the caller must receive the
// stable edge failure, whatever transport the gateway chooses.
func TestPostIsNotReplayedWhenTheUpstreamFailsMidRequest(t *testing.T) {
	var reads, posts atomic.Int64
	execution := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet {
			reads.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		posts.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(execution.Close)

	config := baseConfig()
	config.ExecutionURL = execution.URL
	edge := startGateway(t, config)

	// A safe request first, so the edge holds a reusable connection to the same
	// service before the POST is attempted.
	response, err := http.Get(edge.URL + "/api/cases/c/reviews/r")
	if err != nil {
		t.Fatalf("warm-up read failed: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || reads.Load() != 1 {
		t.Fatalf("warm-up read answered %d and reached the service %d times", response.StatusCode, reads.Load())
	}

	request, err := http.NewRequest(http.MethodPost, edge.URL+"/api/cases/c/reviews", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Idempotency-Key", "deliver-once")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("a failed upstream must still produce a gateway answer: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("a mid-request upstream failure answered %d", response.StatusCode)
	}
	if !strings.Contains(string(body), "upstream_unavailable") {
		t.Fatalf("the edge answered an unstable failure body: %q", body)
	}
	if got := posts.Load(); got != 1 {
		t.Fatalf("the service observed %d POSTs for one client request", got)
	}
}

// A stream is flushed as soon as the upstream writes, and a client that leaves
// cancels the upstream request instead of leaving it running.
func TestStreamFlushesImmediatelyAndCancelsUpstream(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	cancelled := make(chan struct{})
	var once sync.Once
	execution := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("data: first\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			once.Do(func() { close(cancelled) })
		case <-release:
		}
	}))
	t.Cleanup(execution.Close)

	config := baseConfig()
	config.ExecutionURL = execution.URL
	config.RequestTimeout = 50 * time.Millisecond
	edge := startGateway(t, config)

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, edge.URL+"/api/cases/c/reviews/r/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("stream request failed: %v", err)
	}
	first := make([]byte, len("data: first\n\n"))
	read := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(response.Body, first)
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatalf("stream read failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the first event was buffered until the stream closed")
	}
	if string(first) != "data: first\n\n" {
		t.Fatalf("stream delivered %q", first)
	}
	select {
	case <-cancelled:
		t.Fatal("the ordinary request timeout cancelled the event stream")
	case <-time.After(2 * config.RequestTimeout):
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("the upstream request was not cancelled when the client left")
	}
	response.Body.Close()
}

func TestStreamDoesNotSpliceAfterFirstEvent(t *testing.T) {
	var firstRequests, secondRequests atomic.Int64
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstRequests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: first\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
	}))
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondRequests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: second\n\n")
	}))
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)
	config := baseConfig()
	config.ExecutionURL = first.URL
	gateway, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
	upstream, ok := gateway.upstreamFor("execution")
	if !ok {
		t.Fatal("execution upstream was not constructed")
	}
	secondTarget, err := baseURL(second.URL)
	if err != nil {
		t.Fatal(err)
	}
	upstream.mu.Lock()
	upstream.replicas[0].checked = true
	upstream.replicas[0].healthy = true
	upstream.replicas = append(upstream.replicas, &replica{target: secondTarget, identity: "second", checked: true, healthy: true})
	upstream.mu.Unlock()

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/cases/c/reviews/r/events", nil)
	upstream.serve(http.MethodGet, classStream, response, request)
	if response.Code != http.StatusOK || response.Body.String() != "data: first\n\n" {
		t.Fatalf("stream response was spliced or lost its first event: status=%d body=%q", response.Code, response.Body.String())
	}
	if firstRequests.Load() != 1 || secondRequests.Load() != 0 {
		t.Fatalf("a stream failure after headers reached another replica: first=%d second=%d", firstRequests.Load(), secondRequests.Load())
	}
}
