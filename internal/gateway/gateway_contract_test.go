package gateway

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFromEnvReadsLimitsAndRejectsInvalidValues(t *testing.T) {
	for _, name := range []string{
		"CORE_URLS", "TOOL_URLS", "DOCUMENT_URL", "EXECUTION_URL",
		"GATEWAY_CORE_TOKEN", "GATEWAY_DOCUMENT_TOKEN", "GATEWAY_EXECUTION_TOKEN", "GATEWAY_TOOL_TOKEN",
		"GATEWAY_MAX_UPLOAD_BYTES", "GATEWAY_MAX_INFLIGHT", "GATEWAY_MAX_UPLOADS",
		"GATEWAY_REQUEST_TIMEOUT", "GATEWAY_UPLOAD_TIMEOUT", "GATEWAY_HEALTH_INTERVAL", "GATEWAY_HEALTH_TIMEOUT",
		"GATEWAY_HEALTH_FAILURE_THRESHOLD", "GATEWAY_HEALTH_RECOVERY_THRESHOLD",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("CORE_URLS", " http://core-a, ,http://core-b ")
	t.Setenv("TOOL_URLS", "http://tool-a,http://tool-b")
	t.Setenv("DOCUMENT_URL", " http://document ")
	t.Setenv("EXECUTION_URL", "http://execution")
	t.Setenv("GATEWAY_CORE_TOKEN", tokenFor("core"))
	t.Setenv("GATEWAY_DOCUMENT_TOKEN", tokenFor("document"))
	t.Setenv("GATEWAY_EXECUTION_TOKEN", tokenFor("execution"))
	t.Setenv("GATEWAY_TOOL_TOKEN", tokenFor("tool"))
	t.Setenv("GATEWAY_MAX_UPLOAD_BYTES", "2048")
	t.Setenv("GATEWAY_MAX_INFLIGHT", "7")
	t.Setenv("GATEWAY_MAX_UPLOADS", "2")
	t.Setenv("GATEWAY_REQUEST_TIMEOUT", "12s")
	t.Setenv("GATEWAY_UPLOAD_TIMEOUT", "45s")
	t.Setenv("GATEWAY_HEALTH_INTERVAL", "250ms")
	t.Setenv("GATEWAY_HEALTH_TIMEOUT", "50ms")
	t.Setenv("GATEWAY_HEALTH_FAILURE_THRESHOLD", "3")
	t.Setenv("GATEWAY_HEALTH_RECOVERY_THRESHOLD", "4")

	config, err := FromEnv()
	if err != nil {
		t.Fatalf("valid edge environment was rejected: %v", err)
	}
	if strings.Join(config.CoreURLs, ",") != "http://core-a,http://core-b" || strings.Join(config.ToolURLs, ",") != "http://tool-a,http://tool-b" {
		t.Fatalf("replica lists were not trimmed: core=%q tool=%q", config.CoreURLs, config.ToolURLs)
	}
	if config.DocumentURL != "http://document" || config.ExecutionURL != "http://execution" {
		t.Fatalf("single upstreams were not trimmed: document=%q execution=%q", config.DocumentURL, config.ExecutionURL)
	}
	if config.HealthInterval != 250*time.Millisecond || config.HealthTimeout != 50*time.Millisecond ||
		config.HealthFailureThreshold != 3 || config.HealthRecoveryThreshold != 4 {
		t.Fatalf("health settings were not read exactly: interval=%s timeout=%s fail=%d recover=%d",
			config.HealthInterval, config.HealthTimeout, config.HealthFailureThreshold, config.HealthRecoveryThreshold)
	}

	for _, test := range []struct {
		name  string
		value string
	}{
		{"GATEWAY_MAX_UPLOAD_BYTES", "0"},
		{"GATEWAY_MAX_UPLOAD_BYTES", "not-a-number"},
		{"GATEWAY_MAX_INFLIGHT", "-1"},
		{"GATEWAY_MAX_INFLIGHT", "many"},
		{"GATEWAY_MAX_UPLOADS", "0"},
		{"GATEWAY_REQUEST_TIMEOUT", "1s nope"},
		{"GATEWAY_REQUEST_TIMEOUT", "0s"},
		{"GATEWAY_UPLOAD_TIMEOUT", "-1s"},
		{"GATEWAY_HEALTH_INTERVAL", "0s"},
		{"GATEWAY_HEALTH_TIMEOUT", "0s"},
		{"GATEWAY_HEALTH_INTERVAL", "49ms"},
		{"GATEWAY_HEALTH_TIMEOUT", "300ms"},
		{"GATEWAY_HEALTH_FAILURE_THRESHOLD", "11"},
		{"GATEWAY_HEALTH_RECOVERY_THRESHOLD", "0"},
	} {
		t.Run(test.name+"="+test.value, func(t *testing.T) {
			t.Setenv(test.name, test.value)
			if _, err := FromEnv(); err == nil {
				t.Fatalf("invalid %s=%q was accepted", test.name, test.value)
			}
		})
	}
}

func TestNewRejectsMissingAndUnsafeServiceCredentialsAndTargets(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Config)
	}{
		{"missing core target", func(c *Config) { c.CoreURLs = nil }},
		{"missing document target", func(c *Config) { c.DocumentURL = " " }},
		{"short service credential", func(c *Config) { c.ToolToken = "short" }},
		{"reused service credential", func(c *Config) { c.ExecutionToken = c.CoreToken }},
		{"credential in URL", func(c *Config) { c.DocumentURL = "http://user:password@example.test" }},
		{"query in URL", func(c *Config) { c.CoreURLs = []string{"http://example.test?target=private"} }},
		{"path prefix in URL", func(c *Config) { c.ToolURLs = []string{"http://example.test/internal"} }},
		{"zero inflight", func(c *Config) { c.MaxInflight = 0 }},
		{"zero upload capacity", func(c *Config) { c.MaxUploads = 0 }},
		{"zero request timeout", func(c *Config) { c.RequestTimeout = 0 }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			config := baseConfig()
			test.change(&config)
			if _, err := New(config); err == nil {
				t.Fatal("unsafe edge configuration was accepted")
			}
		})
	}
}

func TestPublicOperationsUseOnlyTheirOwningServiceCredential(t *testing.T) {
	type received struct {
		role          string
		path          string
		credential    string
		authorization string
		cookie        string
	}
	var mu sync.Mutex
	var calls []received
	servers := make(map[string]*httptest.Server)
	for _, role := range []string{"core", "document", "execution", "tool"} {
		role := role
		servers[role] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health/ready" {
				w.WriteHeader(http.StatusOK)
				return
			}
			mu.Lock()
			calls = append(calls, received{role: role, path: r.URL.EscapedPath(), credential: r.Header.Get("X-Contract-Service-Key"), authorization: r.Header.Get("Authorization"), cookie: r.Header.Get("Cookie")})
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok")
		}))
		t.Cleanup(servers[role].Close)
	}
	config := baseConfig()
	config.CoreURLs = []string{servers["core"].URL}
	config.DocumentURL = servers["document"].URL
	config.ExecutionURL = servers["execution"].URL
	config.ToolURLs = []string{servers["tool"].URL}
	edge := startGateway(t, config)
	expectedOwner := make(map[string]string, 7)
	for _, request := range []struct {
		method, path, owner string
	}{
		{http.MethodGet, "/api/cases", "core"},
		{http.MethodPost, "/api/cases/c/documents", "document"},
		{http.MethodGet, "/api/templates", "document"},
		{http.MethodPost, "/api/cases/c/reviews", "execution"},
		{http.MethodGet, "/api/cases/c/reviews/r/events", "execution"},
		{http.MethodPost, "/api/cases/c/reviews/r/findings/f/decisions", "core"},
		{http.MethodPost, "/mcp", "tool"},
	} {
		expectedOwner[request.path] = request.owner
		req, err := http.NewRequest(request.method, edge.URL+request.path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer end-user")
		req.Header.Set("Cookie", "session=end-user")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s failed: %v", request.method, request.path, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s %s answered %d", request.method, request.path, response.StatusCode)
		}
	}
	invalid, err := http.Get(edge.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	invalid.Body.Close()
	if invalid.StatusCode != http.StatusNotFound {
		t.Fatalf("unsupported MCP method answered %d", invalid.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 7 {
		t.Fatalf("the edge sent %d operations to services, want 7", len(calls))
	}
	for _, call := range calls {
		want, ok := expectedOwner[call.path]
		if !ok || call.role != want {
			t.Errorf("%s reached %s, want owning service %q", call.path, call.role, want)
		}
		delete(expectedOwner, call.path)
		if call.credential != tokenFor(call.role) {
			t.Errorf("%s received the wrong service credential %q", call.role, call.credential)
		}
		if call.authorization != "Bearer end-user" || call.cookie != "session=end-user" {
			t.Errorf("%s did not receive the user's downstream authentication unchanged", call.role)
		}
	}
	if len(expectedOwner) != 0 {
		t.Errorf("some public operations reached no service: %v", expectedOwner)
	}
}

func TestKnownBadReplicaIsSkippedAndAllUnhealthyReturns503(t *testing.T) {
	unavailable := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := unavailable.URL
	unavailable.Close()
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "core")
	}))
	document := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	execution := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	tool := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(core.Close)
	t.Cleanup(document.Close)
	t.Cleanup(execution.Close)
	t.Cleanup(tool.Close)
	config := baseConfig()
	config.CoreURLs = []string{deadURL, core.URL}
	config.DocumentURL = document.URL
	config.ExecutionURL = execution.URL
	config.ToolURLs = []string{tool.URL}
	config.HealthInterval = maxHealthInterval
	gateway, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		gateway.Close()
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
	if err := gateway.Ready(context.Background()); err != nil {
		t.Fatalf("one healthy replica per service should make the edge ready: %v", err)
	}

	edge := httptest.NewServer(gateway.Handler())
	t.Cleanup(edge.Close)
	readyResponse, err := http.Get(edge.URL + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	readyResponse.Body.Close()
	if readyResponse.StatusCode != http.StatusOK {
		t.Fatalf("edge readiness route answered %d while each service had a healthy replica", readyResponse.StatusCode)
	}
	for attempt := 0; attempt < 2; attempt++ {
		response, err := http.Get(edge.URL + "/api/cases")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("known-bad replica was selected on request %d: status=%d", attempt+1, response.StatusCode)
		}
	}

	core.Close()
	gateway.probeAll(context.Background())
	gateway.probeAll(context.Background())
	if err := gateway.Ready(context.Background()); err == nil || !strings.Contains(err.Error(), "core") {
		t.Fatalf("edge remained ready after every core replica failed: %v", err)
	}
	readyResponse, err = http.Get(edge.URL + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	readyResponse.Body.Close()
	if readyResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("edge readiness route answered %d with no healthy core replica", readyResponse.StatusCode)
	}
	response, err := http.Get(edge.URL + "/api/cases")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("all-unhealthy service answered %d, want 503", response.StatusCode)
	}
}

func TestSafeReadFailsOverAndQuarantinesFailedReplica(t *testing.T) {
	var failedReplicaRequests, healthyReplicaRequests atomic.Int64
	var logOutput bytes.Buffer
	previousLogOutput := log.Writer()
	log.SetOutput(&logOutput)
	t.Cleanup(func() { log.SetOutput(previousLogOutput) })
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		failedReplicaRequests.Add(1)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("failed replica could not close its response connection: %v", err)
			return
		}
		_ = connection.Close()
	}))
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		healthyReplicaRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(failed.Close)
	t.Cleanup(healthy.Close)
	config := baseConfig()
	config.CoreURLs = []string{failed.URL, healthy.URL}
	config.HealthInterval = maxHealthInterval
	gateway, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		gateway.Close()
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
	edge := httptest.NewServer(gateway.Handler())
	t.Cleanup(edge.Close)
	coreUpstream, ok := gateway.upstreamFor("core")
	if !ok {
		t.Fatal("core upstream was not constructed")
	}
	failedReplicaID := coreUpstream.replicas[0].identity
	healthyReplicaID := coreUpstream.replicas[1].identity

	for attempt := 0; attempt < 2; attempt++ {
		response, err := http.Get(edge.URL + "/api/cases")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("safe read %d did not reach the healthy replica: status=%d", attempt+1, response.StatusCode)
		}
	}
	if got := failedReplicaRequests.Load(); got != 1 {
		t.Fatalf("unhealthy replica received %d requests after one transport failure, want 1", got)
	}
	if got := healthyReplicaRequests.Load(); got != 2 {
		t.Fatalf("healthy replica received %d reads, want the failed-read retry plus the next request", got)
	}
	logs := logOutput.String()
	for _, expected := range []string{
		"gateway_upstream_health role=core replica=" + failedReplicaID + " previous=unknown state=healthy source=probe",
		"gateway_upstream_health role=core replica=" + failedReplicaID + " previous=healthy state=unhealthy source=request",
		"gateway_upstream_request role=core request=1 attempt=1 replica=" + failedReplicaID + " method=GET outcome=transport_failure status=0 elapsed_ms=",
		"gateway_upstream_request role=core request=1 attempt=2 replica=" + healthyReplicaID + " method=GET outcome=response_headers status=200 elapsed_ms=",
		"gateway_upstream_request role=core request=2 attempt=1 replica=" + healthyReplicaID + " method=GET outcome=response_headers status=200 elapsed_ms=",
	} {
		if !strings.Contains(logs, expected) {
			t.Errorf("gateway log missed attribution %q in %q", expected, logs)
		}
	}
	if strings.Contains(logs, "/api/cases") || strings.Contains(logs, failed.URL) || strings.Contains(logs, healthy.URL) {
		t.Fatalf("gateway request log exposed a path or upstream URL: %q", logs)
	}
}

func TestReplicaHealthRequiresConfiguredFailureAndRecoveryThresholds(t *testing.T) {
	upstream := &upstream{failureThreshold: 2, recoveryThreshold: 2}
	replica := &replica{}
	started := time.Now()

	upstream.observeProbe(replica, started, true)
	if !replica.healthy {
		t.Fatal("first successful probe did not mark the replica ready")
	}
	upstream.observeProbe(replica, started.Add(time.Millisecond), false)
	if !replica.healthy {
		t.Fatal("one failed probe crossed the two-failure threshold")
	}
	upstream.observeProbe(replica, started.Add(2*time.Millisecond), false)
	if replica.healthy {
		t.Fatal("two failed probes did not quarantine the replica")
	}

	staleProbeStarted := time.Now()
	upstream.observeRequestFailure(replica)
	upstream.observeProbe(replica, staleProbeStarted, true)
	if replica.healthy {
		t.Fatal("a probe started before a request failure restored the replica")
	}
	upstream.observeProbe(replica, time.Now().Add(time.Second), true)
	if replica.healthy {
		t.Fatal("one successful probe crossed the two-success recovery threshold")
	}
	upstream.observeProbe(replica, time.Now().Add(2*time.Second), true)
	if !replica.healthy {
		t.Fatal("two successful probes did not restore the replica")
	}
}

func TestHealthConvergesAndRoutesToRecoveredReplica(t *testing.T) {
	var primaryReady atomic.Bool
	var primaryRequests, secondaryRequests atomic.Int64
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			if !primaryReady.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		primaryRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		secondaryRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(primary.Close)
	t.Cleanup(secondary.Close)
	config := baseConfig()
	config.CoreURLs = []string{primary.URL, secondary.URL}
	config.DocumentURL = secondary.URL
	config.ExecutionURL = secondary.URL
	config.ToolURLs = []string{secondary.URL}
	config.HealthInterval = maxHealthInterval
	config.HealthFailureThreshold = 2
	config.HealthRecoveryThreshold = 2
	gateway, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		gateway.Close()
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
	edge := httptest.NewServer(gateway.Handler())
	t.Cleanup(edge.Close)
	if err := gateway.Ready(context.Background()); err != nil {
		t.Fatalf("one ready replica should keep the service ready: %v", err)
	}

	response, err := http.Get(edge.URL + "/api/cases")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || primaryRequests.Load() != 0 || secondaryRequests.Load() != 1 {
		t.Fatalf("request did not avoid the initially unready replica: status=%d primary=%d secondary=%d",
			response.StatusCode, primaryRequests.Load(), secondaryRequests.Load())
	}

	primaryReady.Store(true)
	gateway.probeAll(context.Background())
	gateway.probeAll(context.Background())
	if err := gateway.Ready(context.Background()); err != nil {
		t.Fatalf("recovered replica did not converge to ready: %v", err)
	}
	response, err = http.Get(edge.URL + "/api/cases")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || primaryRequests.Load() != 1 || secondaryRequests.Load() != 1 {
		t.Fatalf("request did not return to the recovered replica: status=%d primary=%d secondary=%d",
			response.StatusCode, primaryRequests.Load(), secondaryRequests.Load())
	}
}

func TestRequestTimeoutCancelsUpstreamAndReturnsStable504(t *testing.T) {
	entered := make(chan struct{}, 1)
	cancelled := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-release:
		}
	}))
	t.Cleanup(upstream.Close)
	config := baseConfig()
	config.CoreURLs = []string{upstream.URL}
	config.RequestTimeout = 80 * time.Millisecond
	edge := startGateway(t, config)
	response, err := (&http.Client{Timeout: 2 * time.Second}).Get(edge.URL + "/api/cases")
	if err != nil {
		t.Fatalf("gateway did not return its bounded timeout response: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusGatewayTimeout || !strings.Contains(string(body), `"upstream_timeout"`) {
		t.Fatalf("timed-out request answered %d with %q", response.StatusCode, body)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request never reached the owning service")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("gateway timeout did not cancel the downstream request")
	}
}

func TestRequestCancellationReachesUpstream(t *testing.T) {
	entered := make(chan struct{}, 1)
	cancelled := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-release:
		}
	}))
	t.Cleanup(upstream.Close)
	config := baseConfig()
	config.CoreURLs = []string{upstream.URL}
	edge := startGateway(t, config)
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, edge.URL+"/api/cases", nil)
	if err != nil {
		t.Fatal(err)
	}
	responseDone := make(chan struct{})
	go func() {
		defer close(responseDone)
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("request never reached the owning service")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("client cancellation did not reach the downstream request")
	}
	select {
	case <-responseDone:
	case <-time.After(time.Second):
		t.Fatal("client request did not finish after cancellation")
	}
}

func TestConcurrencyBudgetsRejectOverloadAndReleasePermits(t *testing.T) {
	t.Run("global inflight", func(t *testing.T) {
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health/ready" {
				w.WriteHeader(http.StatusOK)
				return
			}
			entered <- struct{}{}
			select {
			case <-release:
				w.WriteHeader(http.StatusOK)
			case <-r.Context().Done():
			}
		}))
		t.Cleanup(upstream.Close)
		config := baseConfig()
		config.CoreURLs = []string{upstream.URL}
		config.MaxInflight = 1
		edge := startGateway(t, config)
		first := make(chan int, 1)
		go func() {
			response, err := http.Get(edge.URL + "/api/cases")
			if err != nil {
				first <- 0
				return
			}
			response.Body.Close()
			first <- response.StatusCode
		}()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("first request did not acquire the inflight permit")
		}
		response, err := (&http.Client{Timeout: time.Second}).Get(edge.URL + "/api/cases")
		if err != nil {
			t.Fatalf("overload should receive an edge response: %v", err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "gateway_busy") {
			t.Fatalf("full inflight budget answered %d %q err=%v", response.StatusCode, body, err)
		}
		unblock()
		select {
		case status := <-first:
			if status != http.StatusOK {
				t.Fatalf("request did not succeed after releasing its permit: %d", status)
			}
		case <-time.After(time.Second):
			t.Fatal("inflight permit was not released after the request")
		}
	})

	t.Run("upload-only capacity", func(t *testing.T) {
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		var uploads atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				uploads.Add(1)
				entered <- struct{}{}
				select {
				case <-release:
					w.WriteHeader(http.StatusCreated)
				case <-r.Context().Done():
				}
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(upstream.Close)
		config := baseConfig()
		config.CoreURLs = []string{upstream.URL}
		config.DocumentURL = upstream.URL
		config.MaxInflight = 2
		config.MaxUploads = 1
		edge := startGateway(t, config)
		first := make(chan int, 1)
		go func() {
			response, err := http.Post(edge.URL+"/api/cases/c/documents", "application/octet-stream", strings.NewReader("first"))
			if err != nil {
				first <- 0
				return
			}
			response.Body.Close()
			first <- response.StatusCode
		}()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("first upload did not acquire the upload permit")
		}
		second, err := (&http.Client{Timeout: time.Second}).Post(edge.URL+"/api/cases/c/documents", "application/octet-stream", strings.NewReader("second"))
		if err != nil {
			t.Fatalf("upload overload should receive an edge response: %v", err)
		}
		body, err := io.ReadAll(second.Body)
		second.Body.Close()
		if err != nil || second.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "gateway_busy") {
			t.Fatalf("full upload budget answered %d %q err=%v", second.StatusCode, body, err)
		}
		if uploads.Load() != 1 {
			t.Fatalf("over-capacity upload reached the document service: %d requests", uploads.Load())
		}
		read, err := (&http.Client{Timeout: time.Second}).Get(edge.URL + "/api/cases")
		if err != nil {
			t.Fatalf("read capacity was blocked by an upload: %v", err)
		}
		read.Body.Close()
		if read.StatusCode != http.StatusOK {
			t.Fatalf("read while an upload occupies its permit answered %d", read.StatusCode)
		}
		unblock()
		select {
		case status := <-first:
			if status != http.StatusCreated {
				t.Fatalf("accepted upload did not complete after releasing capacity: %d", status)
			}
		case <-time.After(time.Second):
			t.Fatal("upload permit was not released after the upload")
		}
	})
}

type probeRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip probeRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type countingReadCloser struct {
	source io.Reader
	read   int
}

func (body *countingReadCloser) Read(buffer []byte) (int, error) {
	read, err := body.source.Read(buffer)
	body.read += read
	return read, err
}

func (*countingReadCloser) Close() error { return nil }

func TestReadinessProbeRejectsNonReadyStatusAndBoundsResponseBody(t *testing.T) {
	body := &countingReadCloser{source: strings.NewReader(strings.Repeat("x", 8192))}
	client := &http.Client{Transport: probeRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/health/ready" || request.Method != http.MethodGet {
			t.Errorf("unexpected readiness probe %s %s", request.Method, request.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: body, Request: request}, nil
	})}
	if err := probeReady(context.Background(), client, "http://replica.invalid"); err == nil {
		t.Fatal("non-ready replica was accepted")
	}
	if body.read != 4096 {
		t.Fatalf("readiness probe consumed %d response bytes, want bounded 4096", body.read)
	}
}

func TestExpiredDeadlineKeepsTimeoutStatusAfterParentCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	ctx, stop := context.WithDeadline(parent, time.Now().Add(-time.Second))
	defer stop()
	if ctx.Err() != context.Canceled {
		t.Fatalf("expected cancellation to win the context race, got %v", ctx.Err())
	}
	proxy, err := New(baseConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/cases", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusGatewayTimeout || !strings.Contains(response.Body.String(), `"upstream_timeout"`) {
		t.Fatalf("expired request lost its timeout status after cancellation: status=%d body=%s", response.Code, response.Body.String())
	}
}
