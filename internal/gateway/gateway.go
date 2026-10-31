// Package gateway implements the edge reverse proxy in front of the
// independently deployed services.
//
// The gateway is deliberately thin: it is not an authorization authority and it
// repeats no business logic. It chooses an owning service from the shared
// routing table (httpapi.OperationOwnedBy), removes every internal or
// forwarded header a caller could have forged, and presents exactly the
// per-service credential that service is configured to require. The user's
// session cookie and Authorization header pass through untouched, and every
// downstream service still performs its own session, audience, and case
// authorization.
package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"contractaudit/internal/httpapi"
)

const (
	defaultMaxUploadBytes    = 33 << 20
	defaultMaxInflight       = 128
	defaultMaxUploads        = 16
	defaultRequestTimeout    = 3 * time.Minute
	defaultUploadTimeout     = 10 * time.Minute
	defaultHealthInterval    = time.Second
	defaultHealthTimeout     = 500 * time.Millisecond
	defaultFailureThreshold  = 2
	defaultRecoveryThreshold = 2
	maxReplicasPerService    = 16
	minHealthInterval        = 100 * time.Millisecond
	maxHealthInterval        = 30 * time.Second
	minHealthTimeout         = 50 * time.Millisecond
	maxHealthTimeout         = 5 * time.Second
	maxHealthThreshold       = 10
	upstreamHeaderTimeout    = 60 * time.Second
	streamHeaderTimeout      = 15 * time.Second
)

var errNoHealthyReplica = errors.New("no healthy upstream replica")

// Config is the complete edge configuration. Every upstream URL and every
// per-service credential is required: the gateway has no fallback that could
// silently route a request to a service it cannot authenticate to.
type Config struct {
	CoreURLs       []string
	DocumentURL    string
	ExecutionURL   string
	ToolURLs       []string
	CoreToken      string
	DocumentToken  string
	ExecutionToken string
	ToolToken      string

	MaxUploadBytes int64
	MaxInflight    int
	MaxUploads     int
	RequestTimeout time.Duration
	UploadTimeout  time.Duration

	HealthInterval          time.Duration
	HealthTimeout           time.Duration
	HealthFailureThreshold  int
	HealthRecoveryThreshold int
}

// FromEnv reads the edge configuration. Lists accept comma-separated base URLs;
// the gateway rotates across replicas that its readiness monitor marks healthy.
func FromEnv() (Config, error) {
	config := Config{
		CoreURLs:       splitList(os.Getenv("CORE_URLS")),
		ToolURLs:       splitList(os.Getenv("TOOL_URLS")),
		DocumentURL:    strings.TrimSpace(os.Getenv("DOCUMENT_URL")),
		ExecutionURL:   strings.TrimSpace(os.Getenv("EXECUTION_URL")),
		CoreToken:      strings.TrimSpace(os.Getenv("GATEWAY_CORE_TOKEN")),
		DocumentToken:  strings.TrimSpace(os.Getenv("GATEWAY_DOCUMENT_TOKEN")),
		ExecutionToken: strings.TrimSpace(os.Getenv("GATEWAY_EXECUTION_TOKEN")),
		ToolToken:      strings.TrimSpace(os.Getenv("GATEWAY_TOOL_TOKEN")),

		MaxUploadBytes:          defaultMaxUploadBytes,
		MaxInflight:             defaultMaxInflight,
		MaxUploads:              defaultMaxUploads,
		RequestTimeout:          defaultRequestTimeout,
		UploadTimeout:           defaultUploadTimeout,
		HealthInterval:          defaultHealthInterval,
		HealthTimeout:           defaultHealthTimeout,
		HealthFailureThreshold:  defaultFailureThreshold,
		HealthRecoveryThreshold: defaultRecoveryThreshold,
	}
	if err := loadGatewayEnv(&config); err != nil {
		return Config{}, err
	}
	if err := normalizeHealthConfig(&config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func loadGatewayEnv(config *Config) error {
	if err := loadPositiveUploadLimit(&config.MaxUploadBytes); err != nil {
		return err
	}
	if err := loadPositiveInts([]struct {
		name   string
		target *int
	}{
		{"GATEWAY_MAX_INFLIGHT", &config.MaxInflight}, {"GATEWAY_MAX_UPLOADS", &config.MaxUploads},
		{"GATEWAY_HEALTH_FAILURE_THRESHOLD", &config.HealthFailureThreshold}, {"GATEWAY_HEALTH_RECOVERY_THRESHOLD", &config.HealthRecoveryThreshold},
	}); err != nil {
		return err
	}
	return loadPositiveDurations([]struct {
		name   string
		target *time.Duration
	}{
		{"GATEWAY_REQUEST_TIMEOUT", &config.RequestTimeout}, {"GATEWAY_UPLOAD_TIMEOUT", &config.UploadTimeout},
		{"GATEWAY_HEALTH_INTERVAL", &config.HealthInterval}, {"GATEWAY_HEALTH_TIMEOUT", &config.HealthTimeout},
	})
}

func loadPositiveUploadLimit(target *int64) error {
	raw := strings.TrimSpace(os.Getenv("GATEWAY_MAX_UPLOAD_BYTES"))
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 1 {
		return errors.New("GATEWAY_MAX_UPLOAD_BYTES must be a positive integer")
	}
	*target = value
	return nil
}

func loadPositiveInts(items []struct {
	name   string
	target *int
}) error {
	for _, item := range items {
		raw := strings.TrimSpace(os.Getenv(item.name))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return fmt.Errorf("%s must be a positive integer", item.name)
		}
		*item.target = value
	}
	return nil
}

func loadPositiveDurations(items []struct {
	name   string
	target *time.Duration
}) error {
	for _, item := range items {
		raw := strings.TrimSpace(os.Getenv(item.name))
		if raw == "" {
			continue
		}
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return fmt.Errorf("%s must be a positive Go duration", item.name)
		}
		*item.target = value
	}
	return nil
}

func normalizeHealthConfig(config *Config) error {
	if config.HealthInterval == 0 {
		config.HealthInterval = defaultHealthInterval
	}
	if config.HealthTimeout == 0 {
		config.HealthTimeout = defaultHealthTimeout
	}
	if config.HealthFailureThreshold == 0 {
		config.HealthFailureThreshold = defaultFailureThreshold
	}
	if config.HealthRecoveryThreshold == 0 {
		config.HealthRecoveryThreshold = defaultRecoveryThreshold
	}
	return validateHealthConfig(*config)
}

func validateHealthConfig(config Config) error {
	if config.HealthInterval < minHealthInterval || config.HealthInterval > maxHealthInterval {
		return errors.New("GATEWAY_HEALTH_INTERVAL must be between 100ms and 30s")
	}
	if config.HealthTimeout < minHealthTimeout || config.HealthTimeout > maxHealthTimeout || config.HealthTimeout > config.HealthInterval {
		return errors.New("GATEWAY_HEALTH_TIMEOUT must be between 50ms and 5s and no greater than the health interval")
	}
	return validateHealthThresholds(config)
}

func validateHealthThresholds(config Config) error {
	if config.HealthFailureThreshold < 1 || config.HealthFailureThreshold > maxHealthThreshold {
		return errors.New("GATEWAY_HEALTH_FAILURE_THRESHOLD must be between 1 and 10")
	}
	if config.HealthRecoveryThreshold < 1 || config.HealthRecoveryThreshold > maxHealthThreshold {
		return errors.New("GATEWAY_HEALTH_RECOVERY_THRESHOLD must be between 1 and 10")
	}
	return nil
}

func splitList(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

type class int

const (
	classRequest class = iota
	classStream
	classUpload
)

// Gateway routes one operation to the single service that owns it.
type Gateway struct {
	upstreams []*upstream

	inflight chan struct{}
	uploads  chan struct{}

	maxUploadBytes int64
	requestTimeout time.Duration
	uploadTimeout  time.Duration

	healthInterval time.Duration
	healthTimeout  time.Duration
	probe          *http.Client

	lifecycleMu    sync.Mutex
	healthStarted  bool
	healthClosed   bool
	healthCancel   context.CancelFunc
	healthDone     chan struct{}
	healthInitial  chan struct{}
	transportClose sync.Once
}

type upstreamRoleConfig struct {
	role      string
	urls      []string
	token     string
	tokenName string
}

// New validates the configuration and builds one reverse proxy per service.
func New(config Config) (*Gateway, error) {
	roles := []upstreamRoleConfig{
		{"core", config.CoreURLs, config.CoreToken, "GATEWAY_CORE_TOKEN"},
		{"document", oneURL(config.DocumentURL), config.DocumentToken, "GATEWAY_DOCUMENT_TOKEN"},
		{"execution", oneURL(config.ExecutionURL), config.ExecutionToken, "GATEWAY_EXECUTION_TOKEN"},
		{"tool", config.ToolURLs, config.ToolToken, "GATEWAY_TOOL_TOKEN"},
	}
	if err := normalizeHealthConfig(&config); err != nil {
		return nil, err
	}
	if config.MaxUploadBytes < 1 || config.MaxInflight < 1 || config.MaxUploads < 1 ||
		config.RequestTimeout <= 0 || config.UploadTimeout <= 0 {
		return nil, errors.New("edge limits and timeouts must be positive")
	}
	seenTokens := map[string]string{}
	gateway := &Gateway{
		upstreams:      make([]*upstream, 0, len(roles)),
		inflight:       make(chan struct{}, config.MaxInflight),
		uploads:        make(chan struct{}, config.MaxUploads),
		maxUploadBytes: config.MaxUploadBytes,
		requestTimeout: config.RequestTimeout,
		uploadTimeout:  config.UploadTimeout,
		healthInterval: config.HealthInterval,
		healthTimeout:  config.HealthTimeout,
	}
	for _, role := range roles {
		targets, err := validateUpstreamRole(role, seenTokens)
		if err != nil {
			return nil, err
		}
		gateway.upstreams = append(gateway.upstreams, newUpstream(role.role, targets, role.token, config.HealthFailureThreshold, config.HealthRecoveryThreshold))
	}
	gateway.probe = &http.Client{Transport: upstreamTransport(config.HealthTimeout, true)}
	return gateway, nil
}

func validateUpstreamRole(role upstreamRoleConfig, seenTokens map[string]string) ([]*url.URL, error) {
	if len(role.urls) == 0 {
		return nil, fmt.Errorf("no %s upstream URL is configured", role.role)
	}
	if len(role.urls) > maxReplicasPerService {
		return nil, fmt.Errorf("%s supports at most %d upstream replicas", role.role, maxReplicasPerService)
	}
	if len(role.token) < 32 {
		return nil, fmt.Errorf("%s must hold a private credential of at least 32 bytes", role.tokenName)
	}
	if other, duplicate := seenTokens[role.token]; duplicate {
		return nil, fmt.Errorf("%s must differ from %s", role.tokenName, other)
	}
	seenTokens[role.token] = role.tokenName

	targets := make([]*url.URL, 0, len(role.urls))
	for _, raw := range role.urls {
		target, err := baseURL(raw)
		if err != nil {
			// The message never repeats the configured value: a base URL can
			// carry credentials, and a startup error is logged.
			return nil, fmt.Errorf("the %s upstream URL is not an HTTP(S) base URL without credentials, query, or path prefix", role.role)
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func oneURL(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return []string{value}
}

func baseURL(raw string) (*url.URL, error) {
	target, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("upstream URL is not a plain HTTP(S) base URL")
	}
	if err := validateBaseURLAuthority(target); err != nil {
		return nil, err
	}
	if err := validateBaseURLPath(target); err != nil {
		return nil, err
	}
	return target, nil
}

func validateBaseURLAuthority(target *url.URL) error {
	if target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") ||
		target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return errors.New("upstream URL is not a plain HTTP(S) base URL")
	}
	return nil
}

func validateBaseURLPath(target *url.URL) error {
	if target.Path != "" && target.Path != "/" {
		return errors.New("upstream URL must not carry a path prefix")
	}
	return nil
}

// Handler mounts the unified probe surface next to the proxy. A request is
// rejected before ServeMux canonicalization, so a traversal or an encoded
// separator can never be rewritten into a different route.
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	httpapi.MountHealth(mux, g.Ready)
	mux.Handle("/", g)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := httpapi.RouteSegments(r.URL.EscapedPath()); !ok {
			http.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// Start launches the bounded readiness monitor and waits for its first probe pass.
func (g *Gateway) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	g.lifecycleMu.Lock()
	if g.healthClosed {
		g.lifecycleMu.Unlock()
		return errors.New("gateway is closed")
	}
	if g.healthStarted {
		initial := g.healthInitial
		g.lifecycleMu.Unlock()
		<-initial
		g.lifecycleMu.Lock()
		closed := g.healthClosed
		g.lifecycleMu.Unlock()
		if closed {
			return errors.New("gateway is closed")
		}
		return nil
	}
	healthCtx, cancel := context.WithCancel(ctx)
	initial := make(chan struct{})
	done := make(chan struct{})
	g.healthStarted = true
	g.healthCancel = cancel
	g.healthInitial = initial
	g.healthDone = done
	g.lifecycleMu.Unlock()
	go g.healthLoop(healthCtx, initial, done)
	<-initial
	g.lifecycleMu.Lock()
	closed := g.healthClosed
	g.lifecycleMu.Unlock()
	if closed {
		return errors.New("gateway is closed")
	}
	return nil
}

// Close stops the probe loop and releases idle proxy connections.
func (g *Gateway) Close() {
	g.lifecycleMu.Lock()
	g.healthClosed = true
	cancel := g.healthCancel
	done := g.healthDone
	started := g.healthStarted
	g.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if started && done != nil {
		<-done
	}
	g.transportClose.Do(func() {
		if g.probe != nil {
			g.probe.CloseIdleConnections()
		}
		for _, upstream := range g.upstreams {
			upstream.close()
		}
	})
}

func (g *Gateway) healthLoop(ctx context.Context, initial, done chan struct{}) {
	defer close(done)
	g.probeAll(ctx)
	close(initial)
	ticker := time.NewTicker(g.healthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.probeAll(ctx)
		}
	}
}

func (g *Gateway) probeAll(ctx context.Context) {
	var wait sync.WaitGroup
	for _, service := range g.upstreams {
		for _, backend := range service.replicas {
			wait.Add(1)
			go func(service *upstream, backend *replica) {
				defer wait.Done()
				probeStarted := time.Now()
				probeCtx, cancel := context.WithTimeout(ctx, g.healthTimeout)
				err := probeReady(probeCtx, g.probe, backend.target.String())
				cancel()
				if ctx.Err() == nil {
					service.observeProbe(backend, probeStarted, err == nil)
				}
			}(service, backend)
		}
	}
	wait.Wait()
}

// Ready reads the authoritative replica state maintained by the health loop.
// The same state gates pickHealthy, so readiness cannot claim a replica that
// request routing still treats as unavailable.
func (g *Gateway) Ready(ctx context.Context) error {
	g.lifecycleMu.Lock()
	started := g.healthStarted
	closed := g.healthClosed
	g.lifecycleMu.Unlock()
	if closed {
		return errors.New("gateway is closed")
	}
	if !started {
		return errors.New("gateway health monitor is not started")
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	for _, upstream := range g.upstreams {
		if !upstream.hasHealthyReplica() {
			return fmt.Errorf("the %s service has no ready replica", upstream.role)
		}
	}
	return nil
}

func probeReady(ctx context.Context, client *http.Client, baseURL string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health/ready", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK {
		return errors.New("replica is not ready")
	}
	return nil
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.EscapedPath()
	segments, ok := httpapi.RouteSegments(path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	upstream, ok := g.route(r.Method, path, segments)
	if !ok {
		http.NotFound(w, r)
		return
	}
	g.serveRoutedRequest(w, r, upstream, segments)
}

func (g *Gateway) serveRoutedRequest(w http.ResponseWriter, r *http.Request, upstream *upstream, segments []string) {
	// The concurrency budget covers every proxied request, including a stream
	// that stays open: a bounded gateway is worth more than an unbounded one.
	select {
	case g.inflight <- struct{}{}:
		defer func() { <-g.inflight }()
	default:
		writeGatewayError(w, http.StatusServiceUnavailable, "gateway_busy")
		return
	}
	class := classify(upstream.role, r.Method, segments)
	switch class {
	case classUpload:
		select {
		case g.uploads <- struct{}{}:
			defer func() { <-g.uploads }()
		default:
			writeGatewayError(w, http.StatusServiceUnavailable, "gateway_busy")
			return
		}
		// The upload is streamed, never buffered, and bounded in bytes, in
		// concurrency, and in wall time. The read deadline is what actually
		// unblocks a client that stops writing; the request context alone cannot
		// interrupt a socket read. A request that exceeds a bound is terminated;
		// the gateway never re-sends a POST.
		r.Body = http.MaxBytesReader(w, r.Body, g.maxUploadBytes)
		ctx, cancel := context.WithTimeout(r.Context(), g.uploadTimeout)
		defer cancel()
		setReadDeadline(w, g.uploadTimeout)
		upstream.serve(r.Method, classUpload, w, r.WithContext(ctx))
	case classStream:
		// A stream has no read or write deadline and no overall timeout: the
		// response is a long-lived event feed, and only its connect and header
		// phases are bounded by the transport.
		upstream.serve(r.Method, classStream, w, r)
	default:
		ctx, cancel := context.WithTimeout(r.Context(), g.requestTimeout)
		defer cancel()
		setReadDeadline(w, g.requestTimeout)
		upstream.serve(r.Method, classRequest, w, r.WithContext(ctx))
	}
}

// setReadDeadline bounds the remaining client-side read for the handler. It is
// best effort: a ResponseWriter that does not support deadlines leaves the
// request context deadline as the only bound.
func setReadDeadline(w http.ResponseWriter, timeout time.Duration) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(timeout))
}

// route selects the single owning service for one decoded request.
func (g *Gateway) route(method, path string, segments []string) (*upstream, bool) {
	if path == "/mcp" {
		// Only the MCP endpoint itself is public, and only for the two methods
		// the frozen profile defines. Internal paths are never prefix-routed.
		if method != http.MethodPost && method != http.MethodOptions {
			return nil, false
		}
		return g.upstreamFor("tool")
	}
	for _, role := range []string{"core", "document", "execution"} {
		if httpapi.OperationOwnedBy(role, method, path) {
			return g.upstreamFor(role)
		}
	}
	return nil, false
}

func (g *Gateway) upstreamFor(role string) (*upstream, bool) {
	for _, upstream := range g.upstreams {
		if upstream.role == role {
			return upstream, true
		}
	}
	return nil, false
}

// classify keeps in step with OperationOwnedBy and reads the same decoded
// segments, so an escaped spelling of a segment (for example %64ocuments) gets
// the same class as its literal form. Document uploads and Core intake uploads
// stream a request body; only the review event feed and the stored original are
// long-lived responses.
func classify(role, method string, segments []string) class {
	if isUploadRequest(role, method, segments) {
		return classUpload
	}
	if isStreamRequest(role, method, segments) {
		return classStream
	}
	return classRequest
}

func isUploadRequest(role, method string, s []string) bool {
	if method != http.MethodPost {
		return false
	}
	if role == "document" && len(s) == 4 && s[0] == "api" && s[1] == "cases" && s[3] == "documents" {
		return true
	}
	return role == "core" && len(s) == 2 && s[0] == "api" && s[1] == "intakes"
}

func isStreamRequest(role, method string, s []string) bool {
	if method != http.MethodGet || len(s) != 6 || s[0] != "api" || s[1] != "cases" {
		return false
	}
	return isReviewEventRoute(s) || isDocumentOriginalRoute(role, s)
}

func isReviewEventRoute(s []string) bool { return s[3] == "reviews" && s[5] == "events" }

func isDocumentOriginalRoute(role string, s []string) bool {
	return role == "document" && s[3] == "documents" && s[5] == "original"
}

type replica struct {
	target             *url.URL
	identity           string
	checked            bool
	healthy            bool
	failures           int
	successes          int
	failureSince       time.Time
	unhealthySince     time.Time
	lastRequestFailure time.Time
}

type upstream struct {
	role            string
	replicas        []*replica
	token           string
	requestSequence atomic.Uint64

	mu                sync.Mutex
	next              int
	failureThreshold  int
	recoveryThreshold int
	transports        []*http.Transport

	// pooled and stream may retry only safe reads before any response is
	// returned. nonReplayable handles methods and bodies that must be sent once.
	pooled        *httputil.ReverseProxy
	nonReplayable *httputil.ReverseProxy
	stream        *httputil.ReverseProxy
}

func newUpstream(role string, urls []*url.URL, token string, failureThreshold, recoveryThreshold int) *upstream {
	up := &upstream{
		role:              role,
		replicas:          make([]*replica, len(urls)),
		token:             token,
		failureThreshold:  failureThreshold,
		recoveryThreshold: recoveryThreshold,
	}
	for i, target := range urls {
		identity := sha256.Sum256([]byte(target.String()))
		up.replicas[i] = &replica{target: target, identity: hex.EncodeToString(identity[:8])}
	}
	pooledTransport := upstreamTransport(upstreamHeaderTimeout, true)
	headerTimeout := upstreamHeaderTimeout
	if role == "document" {
		// Document uploads and parsing can take longer than the generic header
		// timeout. Their request contexts already enforce the configured limits.
		headerTimeout = 0
	}
	nonReplayableTransport := upstreamTransport(headerTimeout, false)
	streamTransport := upstreamTransport(streamHeaderTimeout, true)
	up.transports = []*http.Transport{pooledTransport, nonReplayableTransport, streamTransport}
	up.pooled = up.newProxy(pooledTransport, 0)
	up.nonReplayable = up.newProxy(nonReplayableTransport, 0)
	up.stream = up.newProxy(streamTransport, streamHeaderTimeout)
	return up
}

func (up *upstream) close() {
	for _, transport := range up.transports {
		transport.CloseIdleConnections()
	}
}

func upstreamTransport(headerTimeout time.Duration, keepAlives bool) *http.Transport {
	return &http.Transport{
		DisableKeepAlives: !keepAlives,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: headerTimeout,
	}
}

// idempotent reports whether the transport may reuse a connection for this
// method. Only methods the frozen contract treats as safe are pooled.
func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func (up *upstream) serve(method string, class class, w http.ResponseWriter, r *http.Request) {
	proxy := up.nonReplayable
	if idempotent(method) {
		proxy = up.pooled
		if class == classStream {
			proxy = up.stream
		}
	}
	proxy.ServeHTTP(w, r)
}

func (up *upstream) newProxy(transport http.RoundTripper, streamHeaderBudget time.Duration) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport:     &replicaTransport{upstream: up, base: transport, streamHeaderBudget: streamHeaderBudget},
		FlushInterval: -1,
		ErrorLog:      log.New(log.Writer(), "gateway "+up.role+": ", log.LstdFlags),
		ErrorHandler:  proxyError,
		Director: func(request *http.Request) {
			// ReverseProxy requires an absolute target before it invokes Transport.
			// replicaTransport replaces this placeholder with a currently healthy target.
			target := up.replicas[0].target
			request.URL.Scheme = target.Scheme
			request.URL.Host = target.Host
			// The incoming Host header is preserved: a service decides whether a
			// browser origin matches its own host, and rewriting Host would break
			// that check without adding any authority here.
			stripForgedHeaders(request.Header)
			request.Header.Set("X-Contract-Service-Key", up.token)
		},
	}
}

type replicaTransport struct {
	upstream           *upstream
	base               http.RoundTripper
	streamHeaderBudget time.Duration
}

func (transport *replicaTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	requestID := transport.upstream.requestSequence.Add(1)
	if err := request.Context().Err(); err != nil {
		logGatewayRequestAttempt(transport.upstream, requestID, 0, nil, request.Method, "context_closed", 0, time.Now())
		return nil, err
	}
	retrySafe := retrySafeRequest(request)
	maxAttempts := 1
	if retrySafe {
		maxAttempts = len(transport.upstream.replicas)
	}
	var tried [maxReplicasPerService]bool
	streamGuard := newStreamHeaderGuard(request, transport.streamHeaderBudget)
	defer streamGuard.stop()
	for attempt := range maxAttempts {
		attemptStarted := time.Now()
		replica, ok := transport.upstream.pickHealthy(tried[:len(transport.upstream.replicas)])
		if !ok {
			streamGuard.stop()
			logGatewayRequestAttempt(transport.upstream, requestID, attempt+1, nil, request.Method, "no_healthy_replica", 0, attemptStarted)
			return nil, errNoHealthyReplica
		}
		response, err, requestBuildFailed := transport.roundTripAttempt(request, replica, attempt, streamGuard, requestID, attemptStarted)
		if requestBuildFailed {
			return nil, err
		}
		if err == nil {
			return transport.finishResponse(response, replica, request.Method, streamGuard, requestID, attempt+1, attemptStarted)
		}
		if terminalErr := transport.reportRoundTripFailure(request, replica, streamGuard, requestID, attempt+1, attemptStarted); terminalErr != nil {
			return nil, terminalErr
		}
		if !retrySafe {
			return nil, err
		}
	}
	return nil, errNoHealthyReplica
}

func (transport *replicaTransport) finishResponse(response *http.Response, replica *replica, method string, guard *streamHeaderGuard, requestID uint64, attempt int, started time.Time) (*http.Response, error) {
	if guard.responseTimedOut(response) {
		logGatewayRequestAttempt(transport.upstream, requestID, attempt, replica, method, "stream_header_timeout", 0, started)
		return nil, context.DeadlineExceeded
	}
	logGatewayRequestAttempt(transport.upstream, requestID, attempt, replica, method, "response_headers", response.StatusCode, started)
	return response, nil
}

func retrySafeRequest(request *http.Request) bool {
	return (request.Method == http.MethodGet || request.Method == http.MethodHead) && replayableBody(request)
}

type streamHeaderGuard struct {
	requestContext context.Context
	cancel         context.CancelFunc
	timer          *time.Timer
	deadline       <-chan struct{}
}

func newStreamHeaderGuard(request *http.Request, budget time.Duration) *streamHeaderGuard {
	guard := &streamHeaderGuard{}
	if budget <= 0 {
		return guard
	}
	if _, hasDeadline := request.Context().Deadline(); hasDeadline {
		return guard
	}
	streamContext, cancel := context.WithCancel(request.Context())
	deadline := make(chan struct{})
	guard.requestContext = streamContext
	guard.cancel = cancel
	guard.deadline = deadline
	guard.timer = time.AfterFunc(budget, func() {
		close(deadline)
		cancel()
	})
	return guard
}

func (guard *streamHeaderGuard) stop() {
	if guard.timer != nil {
		guard.timer.Stop()
	}
	if guard.cancel != nil {
		guard.cancel()
	}
}

func (guard *streamHeaderGuard) bind(request *http.Request) *http.Request {
	if guard.requestContext == nil {
		return request
	}
	return request.WithContext(guard.requestContext)
}

func (guard *streamHeaderGuard) responseTimedOut(response *http.Response) bool {
	if guard.timer == nil {
		return false
	}
	if !guard.timer.Stop() {
		if response.Body != nil {
			_ = response.Body.Close()
		}
		guard.cancel()
		return true
	}
	if response.Body != nil {
		response.Body = &cancelReadCloser{ReadCloser: response.Body, cancel: guard.cancel}
		guard.cancel = nil
	} else {
		guard.cancel()
		guard.cancel = nil
	}
	guard.timer = nil
	return false
}

func (transport *replicaTransport) roundTripAttempt(request *http.Request, replica *replica, attempt int, guard *streamHeaderGuard, requestID uint64, started time.Time) (*http.Response, error, bool) {
	attemptRequest, err := requestForReplica(request, replica.target, attempt > 0)
	if err != nil {
		guard.stop()
		logGatewayRequestAttempt(transport.upstream, requestID, attempt+1, replica, request.Method, "request_build_failure", 0, started)
		return nil, err, true
	}
	attemptRequest = guard.bind(attemptRequest)
	response, err := transport.base.RoundTrip(attemptRequest)
	return response, err, false
}

func (transport *replicaTransport) reportRoundTripFailure(request *http.Request, replica *replica, guard *streamHeaderGuard, requestID uint64, attempt int, started time.Time) error {
	select {
	case <-guard.deadline:
		guard.stop()
		logGatewayRequestAttempt(transport.upstream, requestID, attempt, replica, request.Method, "stream_header_timeout", 0, started)
		return context.DeadlineExceeded
	default:
	}
	if err := request.Context().Err(); err != nil {
		guard.stop()
		outcome := "context_cancelled"
		if errors.Is(err, context.DeadlineExceeded) {
			outcome = "context_deadline"
		}
		logGatewayRequestAttempt(transport.upstream, requestID, attempt, replica, request.Method, outcome, 0, started)
		return err
	}
	transport.upstream.observeRequestFailure(replica)
	logGatewayRequestAttempt(transport.upstream, requestID, attempt, replica, request.Method, "transport_failure", 0, started)
	return nil
}

func logGatewayRequestAttempt(upstream *upstream, requestID uint64, attempt int, replica *replica, method, outcome string, status int, started time.Time) {
	replicaID := "none"
	if replica != nil {
		replicaID = replica.identity
	}
	log.Printf("gateway_upstream_request role=%s request=%d attempt=%d replica=%s method=%s outcome=%s status=%d elapsed_ms=%d", upstream.role, requestID, attempt, replicaID, method, outcome, status, time.Since(started).Milliseconds())
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (body *cancelReadCloser) Close() error {
	err := body.ReadCloser.Close()
	body.cancel()
	return err
}

func replayableBody(request *http.Request) bool {
	return request.Body == nil || request.Body == http.NoBody || request.GetBody != nil
}

func requestForReplica(request *http.Request, target *url.URL, retry bool) (*http.Request, error) {
	attempt := request.Clone(request.Context())
	attempt.URL.Scheme = target.Scheme
	attempt.URL.Host = target.Host
	if retry && request.Body != nil && request.Body != http.NoBody {
		body, err := request.GetBody()
		if err != nil {
			return nil, err
		}
		attempt.Body = body
	}
	return attempt, nil
}

func (up *upstream) pickHealthy(excluded []bool) (*replica, bool) {
	up.mu.Lock()
	defer up.mu.Unlock()
	count := len(up.replicas)
	for offset := range count {
		index := (up.next + offset) % count
		if excluded[index] || !up.replicas[index].healthy {
			continue
		}
		up.next = (index + 1) % count
		excluded[index] = true
		return up.replicas[index], true
	}
	return nil, false
}

func (up *upstream) hasHealthyReplica() bool {
	up.mu.Lock()
	defer up.mu.Unlock()
	for _, replica := range up.replicas {
		if replica.healthy {
			return true
		}
	}
	return false
}

func (up *upstream) observeProbe(replica *replica, probeStarted time.Time, healthy bool) {
	up.mu.Lock()
	defer up.mu.Unlock()
	if !probeStarted.After(replica.lastRequestFailure) {
		return
	}
	wasChecked, wasHealthy := replica.checked, replica.healthy
	if healthy {
		up.applySuccessfulProbe(replica)
	} else {
		up.applyFailedProbe(replica, probeStarted)
	}
	replica.checked = true
	if !wasChecked || wasHealthy != replica.healthy {
		up.logProbeTransition(replica, wasChecked, wasHealthy, probeStarted)
	}
}

func (up *upstream) applySuccessfulProbe(replica *replica) {
	replica.failures = 0
	replica.failureSince = time.Time{}
	if !replica.checked || replica.healthy {
		replica.healthy = true
		replica.successes = 0
		return
	}
	replica.successes++
	if replica.successes >= up.recoveryThreshold {
		replica.healthy = true
		replica.successes = 0
	}
}

func (up *upstream) applyFailedProbe(replica *replica, probeStarted time.Time) {
	replica.successes = 0
	if replica.failures == 0 {
		replica.failureSince = probeStarted
	}
	if replica.failures < up.failureThreshold {
		replica.failures++
	}
	if replica.failures >= up.failureThreshold {
		replica.healthy = false
	}
}

func (up *upstream) logProbeTransition(replica *replica, wasChecked, wasHealthy bool, probeStarted time.Time) {
	previous, state := "unknown", "unhealthy"
	if wasChecked {
		previous = "unhealthy"
		if wasHealthy {
			previous = "healthy"
		}
	}
	if replica.healthy {
		state = "healthy"
	}
	transitionMS := int64(0)
	if state == "healthy" && !replica.unhealthySince.IsZero() {
		transitionMS = time.Since(replica.unhealthySince).Milliseconds()
		replica.unhealthySince = time.Time{}
		replica.failureSince = time.Time{}
	} else if state == "unhealthy" {
		if replica.failureSince.IsZero() {
			replica.failureSince = probeStarted
		}
		replica.unhealthySince = replica.failureSince
		transitionMS = time.Since(replica.failureSince).Milliseconds()
	}
	log.Printf("gateway_upstream_health role=%s replica=%s previous=%s state=%s source=probe transition_ms=%d probe_ms=%d", up.role, replica.identity, previous, state, transitionMS, time.Since(probeStarted).Milliseconds())
}

func (up *upstream) observeRequestFailure(replica *replica) {
	up.mu.Lock()
	wasHealthy := replica.healthy
	now := time.Now()
	replica.checked = true
	replica.healthy = false
	replica.failures = up.failureThreshold
	replica.successes = 0
	replica.failureSince = now
	replica.lastRequestFailure = now
	if wasHealthy {
		replica.unhealthySince = now
		log.Printf("gateway_upstream_health role=%s replica=%s previous=healthy state=unhealthy source=request transition_ms=0", up.role, replica.identity)
	}
	up.mu.Unlock()
}

// stripForgedHeaders removes anything a caller could use to impersonate the
// edge, another service, a proxy, or a delegated actor. Every internal header
// family is removed by prefix, so a new internal header cannot be forgotten
// here; only the gateway's own token is added afterwards.
func stripForgedHeaders(header http.Header) {
	for name := range header {
		canonical := http.CanonicalHeaderKey(name)
		switch canonical {
		case "Forwarded",
			"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Prefix",
			"X-Real-Ip", "X-Original-Host", "X-Original-Url", "X-Rewrite-Url",
			"X-Http-Method-Override", "X-Method-Override":
			delete(header, name)
			continue
		}
		if strings.HasPrefix(canonical, "X-Contract-") || strings.HasPrefix(canonical, "X-Audit-") {
			delete(header, name)
		}
	}
}

func proxyError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	// A socket read deadline may cancel the parent before the context timer runs.
	deadline, hasDeadline := r.Context().Deadline()
	switch {
	case errors.As(err, &tooLarge):
		writeGatewayError(w, http.StatusRequestEntityTooLarge, "request_body_too_large")
	case errors.Is(err, errNoHealthyReplica):
		writeGatewayError(w, http.StatusServiceUnavailable, "upstream_unavailable")
	case errors.Is(err, context.DeadlineExceeded) || hasDeadline && !time.Now().Before(deadline):
		writeGatewayError(w, http.StatusGatewayTimeout, "upstream_timeout")
	default:
		writeGatewayError(w, http.StatusBadGateway, "upstream_unavailable")
	}
}

// writeGatewayError answers with a stable, non-identifying error body: a
// failure at the edge never repeats an upstream message, a credential, or a
// vendor error.
func writeGatewayError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"error":"`+code+`","code":"`+code+`"}`)
}
